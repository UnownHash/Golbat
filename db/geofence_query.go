package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jmoiron/sqlx"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"

	"golbat/geo"
)

// FenceContainsPredicate is the SQL fragment testing that a row's (lon, lat)
// lies inside a geofence.
//
// The fence is a bind parameter, never interpolated into the statement text.
// It arrives as a request body, and a geojson.Feature round-trips its whole
// properties map through MarshalJSON, so a property value containing a single
// quote would otherwise close the string literal it was concatenated into.
const FenceContainsPredicate = "ST_CONTAINS(ST_GeomFromGeoJSON(?, 2, 0), POINT(lon, lat))"

// fenceBBoxWhere selects the candidate rows for every geofence query: enabled
// pokestops inside the fence's bounding box, edges included. The Go matcher
// counts the fence boundary as inside, so the pre-filter must not drop a stop
// sitting exactly on the box edge. Its four placeholders bind FenceBoundArgs.
const fenceBBoxWhere = "WHERE lat >= ? AND lon >= ? AND lat <= ? AND lon <= ? AND enabled = 1 "

var errFenceNoGeometry = errors.New("geofence has no geometry")

// FenceQueryArgs returns the bind arguments for a geofence query: the bounding
// box corners as min-lat, min-lon, max-lat, max-lon, then the fence JSON for
// FenceContainsPredicate.
//
// Every geofence query places its bounding-box comparisons before the fence
// predicate, so this order matches all of them. A query that departs from that
// layout must not use this helper.
func FenceQueryArgs(fence *geojson.Feature) ([]any, error) {
	if fence == nil || fence.Geometry == nil {
		return nil, errFenceNoGeometry
	}
	fenceJSON, err := fence.MarshalJSON()
	if err != nil {
		return nil, err
	}
	return append(FenceBoundArgs(fence), string(fenceJSON)), nil
}

// FenceBoundArgs returns just the bounding-box corners, as min-lat, min-lon,
// max-lat, max-lon, for the queries that select candidates by bounding box and
// test containment in Go.
func FenceBoundArgs(fence *geojson.Feature) []any {
	bbox := fence.Geometry.Bound()
	return []any{bbox.Min.Lat(), bbox.Min.Lon(), bbox.Max.Lat(), bbox.Max.Lon()}
}

// fenceMatcher tests candidate rows against a fence in Go rather than in SQL.
//
// Asking MariaDB to evaluate ST_CONTAINS per candidate row is what made large
// geofences time out. Measured on 1M rows against a 2000-vertex fence whose
// bounding box held 628,560 candidates: the whole query took 55 s, while the
// bounding-box scan alone took under a second and testing the same number of
// points against a compiled fence in Go took 3.3 s. Hoisting the GeoJSON parse
// out of the row loop, with a user variable or a CTE, changed nothing, which is
// what identifies per-row containment rather than repeated parsing as the cost.
type fenceMatcher struct{ compiled *geo.CompiledFence }

// newFenceMatcher returns a matcher for fence, or ok=false when the fence is
// not a Polygon or MultiPolygon. Callers fall back to SQL containment in that
// case, so exotic geometries keep working exactly as before.
func newFenceMatcher(fence *geojson.Feature) (fenceMatcher, bool) {
	compiled := geo.CompileFence(fence)
	if compiled == nil {
		return fenceMatcher{}, false
	}
	return fenceMatcher{compiled: compiled}, true
}

// contains reports whether (lat, lon) is inside the fence, boundary included.
//
// MariaDB's ST_CONTAINS also counts boundary points as inside (checked on
// 11.8: a vertex and an edge midpoint both return 1), so this matches the SQL
// predicate it replaces as well as MatchGeofences, which decides stats and
// webhook area attribution.
func (m fenceMatcher) contains(lat, lon float64) bool {
	return m.compiled.Contains(orb.Point{lon, lat})
}

// forEachCandidate streams the rows of a bounding-box query, calling scan for
// each row and onMatch for each row whose (lat, lon) is inside the fence.
//
// The query outcome is recorded once under label, after the stream ends, so a
// failure that surfaces only through rows.Err() (a dropped connection, a
// context deadline hit mid-stream) is counted as an error rather than as a
// successful query.
func (m fenceMatcher) forEachCandidate(ctx context.Context, sdb *sqlx.DB, label, query string, args []any,
	scan func(*sql.Rows) (lat, lon float64, err error), onMatch func()) error {
	err := func() error {
		rows, err := sdb.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			lat, lon, err := scan(rows)
			if err != nil {
				return err
			}
			if m.contains(lat, lon) {
				onMatch()
			}
		}
		return rows.Err()
	}()
	statsCollector.IncDbQuery(label, err)
	return err
}

// PokestopIdsWithinFence returns the ids of enabled pokestops inside fence.
//
// Containment runs in Go whenever the fence is a polygon, for the reason on
// fenceMatcher; other geometries fall back to SQL containment.
func PokestopIdsWithinFence(ctx context.Context, dbDetails DbDetails, fence *geojson.Feature) ([]string, error) {
	const label = "select pokestops for quest removal"

	var pokestopIds []string

	matcher, ok := newFenceMatcher(fence)
	if !ok {
		args, err := FenceQueryArgs(fence)
		if err != nil {
			return nil, err
		}
		err = dbDetails.GeneralDb.SelectContext(ctx, &pokestopIds,
			"SELECT id FROM pokestop "+fenceBBoxWhere+"AND "+FenceContainsPredicate, args...)
		statsCollector.IncDbQuery(label, err)
		if err != nil {
			return nil, err
		}
		return pokestopIds, nil
	}

	var id string
	var lat, lon float64
	err := matcher.forEachCandidate(ctx, dbDetails.GeneralDb, label,
		"SELECT id, lat, lon FROM pokestop "+fenceBBoxWhere, FenceBoundArgs(fence),
		func(rows *sql.Rows) (float64, float64, error) {
			err := rows.Scan(&id, &lat, &lon)
			return lat, lon, err
		},
		func() { pokestopIds = append(pokestopIds, id) })
	if err != nil {
		return nil, err
	}
	return pokestopIds, nil
}
