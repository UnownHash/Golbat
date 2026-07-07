package geo

import (
	"math"
	"runtime"
	"sync"
	"unsafe"

	"github.com/golang/geo/s2"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
	log "github.com/sirupsen/logrus"
)

const (
	// S2LookupLevel is the level lookups arrive at (GMO cell ids are level 15).
	S2LookupLevel = 15
	// s2LookupMinLevel is the coarsest level interior containment is stored
	// at. Large fences store their interior as a handful of coarse cells
	// (resolved by Lookup's parent walk) instead of tens of thousands of
	// level-15 cells — orders of magnitude less build time and memory.
	s2LookupMinLevel = 10
)

type S2CellLookup struct {
	cells     map[s2.CellID][]AreaName
	edgeCells map[s2.CellID]struct{}
}

func NewS2CellLookup() *S2CellLookup {
	return &S2CellLookup{
		cells:     make(map[s2.CellID][]AreaName),
		edgeCells: make(map[s2.CellID]struct{}),
	}
}

// pruneEdgeOverlaps drops interior entries that coincide exactly with an
// edge cell — Lookup bails to the polygon fallback for those, so the
// entries would be dead weight. Unlike the previous implementation the
// edge set itself is retained: it is what tells Lookup a level-15 cell
// straddles some fence boundary.
func (l *S2CellLookup) pruneEdgeOverlaps() int {
	removed := 0
	for cellID := range l.edgeCells {
		if _, exists := l.cells[cellID]; exists {
			delete(l.cells, cellID)
			removed++
		}
	}
	return removed
}

// Lookup resolves the areas containing a (level-15) cell. Returns nil when
// the cell touches any fence boundary — the caller must fall back to exact
// polygon matching. Interior containment may be recorded at any level from
// the cell's own up to s2LookupMinLevel (overlapping fences may store at
// different levels), so the parent chain is walked and results unioned.
func (l *S2CellLookup) Lookup(cellID s2.CellID) []AreaName {
	if _, edge := l.edgeCells[cellID]; edge {
		return nil
	}
	// Fast path: the overwhelmingly common case is exactly one level
	// holding a match — return the stored slice directly, no allocation.
	// This runs on the per-save hot path.
	var single []AreaName
	var merged []AreaName
	for level := cellID.Level(); level >= s2LookupMinLevel; level-- {
		hit := l.cells[cellID.Parent(level)]
		if len(hit) == 0 {
			continue
		}
		switch {
		case single == nil && merged == nil:
			single = hit
		case merged == nil:
			merged = append(append(make([]AreaName, 0, len(single)+len(hit)), single...), hit...)
		default:
			merged = append(merged, hit...)
		}
	}
	if merged != nil {
		return merged
	}
	return single
}

func (l *S2CellLookup) SizeBytes() int64 {
	var size int64

	size += int64(unsafe.Sizeof(l.cells))
	size += int64(len(l.edgeCells)) * int64(unsafe.Sizeof(s2.CellID(0)))

	for cellID, areas := range l.cells {
		size += int64(unsafe.Sizeof(cellID))
		size += int64(unsafe.Sizeof(areas))
		for _, area := range areas {
			size += int64(unsafe.Sizeof(area))
			size += int64(len(area.Name))
			size += int64(len(area.Parent))
		}
	}

	return size
}

func (l *S2CellLookup) CellCount() int {
	return len(l.cells)
}

type polygonWork struct {
	polygon orb.Polygon
	area    AreaName
}

func BuildS2LookupFromFeatures(featureCollection *geojson.FeatureCollection) *S2CellLookup {
	if featureCollection == nil {
		return NewS2CellLookup()
	}

	lookup := NewS2CellLookup()
	var mu sync.Mutex // Only used during build phase

	// Helper closures for thread-safe writes during build
	addArea := func(cellID s2.CellID, area AreaName) {
		mu.Lock()
		lookup.cells[cellID] = append(lookup.cells[cellID], area)
		mu.Unlock()
	}

	addEdgeCell := func(cellID s2.CellID) {
		mu.Lock()
		lookup.edgeCells[cellID] = struct{}{}
		mu.Unlock()
	}

	numWorkers := max(runtime.NumCPU(), 4)

	workChan := make(chan polygonWork, 100)
	var wg sync.WaitGroup

	for range numWorkers {
		wg.Go(func() {
			for work := range workChan {
				processPolygon(work.polygon, work.area, addArea, addEdgeCell)
			}
		})
	}

	for _, f := range featureCollection.Features {
		name := f.Properties.MustString("name", "unknown")
		parent := f.Properties.MustString("parent", name)
		area := AreaName{Parent: parent, Name: name}

		geoType := f.Geometry.GeoJSONType()
		switch geoType {
		case "Polygon":
			polygon := f.Geometry.(orb.Polygon)
			workChan <- polygonWork{polygon: polygon, area: area}
		case "MultiPolygon":
			multiPolygon := f.Geometry.(orb.MultiPolygon)
			for _, polygon := range multiPolygon {
				workChan <- polygonWork{polygon: polygon, area: area}
			}
		}
	}

	close(workChan)
	wg.Wait()

	removed := lookup.pruneEdgeOverlaps()
	log.Infof("GEO: Pruned %d interior cells shadowed by edge cells", removed)

	sizeMB := float64(lookup.SizeBytes()) / (1024 * 1024)
	log.Infof("GEO: S2 lookup table built with %d interior cells, %d edge cells, size: %.2f MB",
		lookup.CellCount(), len(lookup.edgeCells), sizeMB)

	return lookup
}

func processPolygon(
	polygon orb.Polygon,
	area AreaName,
	addArea func(s2.CellID, AreaName),
	addEdgeCell func(s2.CellID),
) {
	if len(polygon) == 0 || len(polygon[0]) == 0 {
		return
	}

	// Convert the outer ring to a sanitized s2.Loop (see ringToS2Points)
	points := ringToS2Points(polygon[0])
	if len(points) < 3 {
		log.Warnf("GEO: fence %s/%s has a degenerate outer ring (<3 distinct vertices); skipped from S2 index (polygon fallback still applies)", area.Parent, area.Name)
		return
	}

	loop := s2.LoopFromPoints(points)
	// GeoJSON in the wild carries both winding orders. S2 interprets a
	// clockwise ring as its complement — the whole planet minus the fence —
	// and the forced fine-level covering of "the planet" effectively never
	// finishes. Normalize picks the interpretation with area < 2*pi.
	loop.Normalize()
	if err := loop.Validate(); err != nil {
		log.Warnf("GEO: fence %s/%s produced an invalid S2 loop (%v); skipped from S2 index (polygon fallback still applies)", area.Parent, area.Name, err)
		return
	}
	// Inverted-loop guard: a pathological ring can survive Normalize
	// covering the COMPLEMENT of the fence — one production file had 14
	// such fences, each producing a planet-sized covering (6.3M interior
	// cells, 4.5 GB, OOM on small hosts). No legitimate scan fence
	// approaches 25% of Earth.
	if loop.Area() > math.Pi {
		log.Warnf("GEO: fence %s/%s covers %.0f%% of the planet after normalization — inverted or corrupt ring; skipped from S2 index (polygon fallback still applies)", area.Parent, area.Name, 100*loop.Area()/(4*math.Pi))
		return
	}
	s2Polygon := s2.PolygonFromLoops([]*s2.Loop{loop})

	// Hole rings become standalone normalized polygons: a cell fully inside
	// a hole is not part of the fence (skipped entirely), a cell crossing a
	// hole boundary is an edge cell (exact polygon fallback). This keeps
	// the S2 fast path consistent with the CompiledFence fallback, which
	// has always honored holes.
	var holes []*s2.Polygon
	for _, holeRing := range polygon[1:] {
		if len(holeRing) == 0 {
			continue
		}
		hp := ringToS2Points(holeRing)
		if len(hp) < 3 {
			continue
		}
		hl := s2.LoopFromPoints(hp)
		hl.Normalize()
		if hl.Validate() != nil || hl.Area() > math.Pi {
			log.Warnf("GEO: fence %s/%s has an invalid hole ring; hole ignored in S2 index", area.Parent, area.Name)
			continue
		}
		holes = append(holes, s2.PolygonFromLoops([]*s2.Loop{hl}))
	}

	coverer := s2.RegionCoverer{
		MinLevel: s2LookupMinLevel,
		MaxLevel: S2LookupLevel,
		MaxCells: 1 << 20,
	}
	for _, cellID := range coverer.Covering(s2Polygon) {
		classifyCell(s2Polygon, holes, cellID, area, addArea, addEdgeCell)
	}
}

// ringToS2Points converts a GeoJSON ring to s2 points, dropping the closing
// duplicate vertex and any consecutive duplicates. GeoJSON rings repeat the
// first vertex as the last; s2 loops are implicitly closed and a repeated
// vertex creates a degenerate edge — an INVALID loop on which Normalize's
// turning-angle math is undefined. In one production geofence file, 14 of
// 100 city fences (valid GeoJSON, clean vertices) came out INVERTED from
// exactly this, each covering the whole planet minus the city.
func ringToS2Points(ring orb.Ring) []s2.Point {
	points := make([]s2.Point, 0, len(ring))
	var last s2.Point
	for i, p := range ring {
		pt := s2.PointFromLatLng(s2.LatLngFromDegrees(p.Lat(), p.Lon()))
		if i > 0 && pt == last {
			continue
		}
		points = append(points, pt)
		last = pt
	}
	// drop closing duplicate (ring[0] repeated at the end)
	if len(points) > 1 && points[0] == points[len(points)-1] {
		points = points[:len(points)-1]
	}
	return points
}

// classifyCell stores fully-contained cells as interior at their own level;
// partially-covered cells subdivide until S2LookupLevel, where they become
// edge cells (exact-polygon fallback territory). ContainsCell is exact,
// unlike the previous 4-vertex sampling, which could misclassify concave
// boundaries as interior. Hole polygons exclude or edge the cells they
// touch, keeping the fast path consistent with the polygon fallback.
func classifyCell(
	p *s2.Polygon,
	holes []*s2.Polygon,
	cellID s2.CellID,
	area AreaName,
	addArea func(s2.CellID, AreaName),
	addEdgeCell func(s2.CellID),
) {
	cell := s2.CellFromCellID(cellID)
	if p.ContainsCell(cell) {
		crossesHole := false
		for _, hole := range holes {
			if !hole.IntersectsCell(cell) {
				continue
			}
			if hole.ContainsCell(cell) {
				// Entirely inside a hole: not part of the fence at all.
				return
			}
			crossesHole = true
			break
		}
		if !crossesHole {
			addArea(cellID, area)
			return
		}
		// Hole boundary crosses this cell — subdivide or mark as edge.
	} else if !p.IntersectsCell(cell) {
		// Reachable via subdivision of a partially-covered parent.
		return
	}
	if cellID.Level() >= S2LookupLevel {
		addEdgeCell(cellID)
		return
	}
	for _, child := range cellID.Children() {
		if p.IntersectsCell(s2.CellFromCellID(child)) {
			classifyCell(p, holes, child, area, addArea, addEdgeCell)
		}
	}
}
