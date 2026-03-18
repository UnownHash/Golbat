package geo

import (
	"runtime"
	"sync"
	"unsafe"

	"github.com/golang/geo/s2"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
	log "github.com/sirupsen/logrus"
)

const S2LookupLevel = 15

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

func (l *S2CellLookup) removeEdgeCells() int {
	removed := 0
	for cellID := range l.edgeCells {
		if _, exists := l.cells[cellID]; exists {
			delete(l.cells, cellID)
			removed++
		}
	}
	l.edgeCells = nil // free memory
	return removed
}

func (l *S2CellLookup) Lookup(cellID s2.CellID) []AreaName {
	return l.cells[cellID]
}

func (l *S2CellLookup) SizeBytes() int64 {
	var size int64

	size += int64(unsafe.Sizeof(l.cells))

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

	removed := lookup.removeEdgeCells()
	log.Infof("GEO: Removed %d edge cells from lookup", removed)

	sizeMB := float64(lookup.SizeBytes()) / (1024 * 1024)
	log.Infof("GEO: S2 lookup table built with %d cells, size: %.2f MB", lookup.CellCount(), sizeMB)

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

	// Convert orb.Polygon to s2.Loop for efficient covering
	ring := polygon[0] // outer ring
	points := make([]s2.Point, len(ring))
	for i, p := range ring {
		points[i] = s2.PointFromLatLng(s2.LatLngFromDegrees(p.Lat(), p.Lon()))
	}

	loop := s2.LoopFromPoints(points)
	s2Polygon := s2.PolygonFromLoops([]*s2.Loop{loop})

	coverer := s2.RegionCoverer{
		MinLevel: S2LookupLevel,
		MaxLevel: S2LookupLevel,
	}
	covering := coverer.Covering(s2Polygon)

	for _, cellID := range covering {
		cell := s2.CellFromCellID(cellID)
		allInside := true
		for i := range 4 {
			if !s2Polygon.ContainsPoint(cell.Vertex(i)) {
				allInside = false
				break
			}
		}
		if allInside {
			addArea(cellID, area)
		} else {
			addEdgeCell(cellID)
		}
	}
}
