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

// fenceBBoxWhere selects the candidate rows for every geofence query: enabled
// pokestops inside the fence's bounding box, edges included. The Go matcher
// counts the fence boundary as inside, so the pre-filter must not drop a stop
// sitting exactly on the box edge. Its four placeholders bind FenceBoundArgs.
const fenceBBoxWhere = "WHERE lat >= ? AND lon >= ? AND lat <= ? AND lon <= ? AND enabled = 1 "

// errFenceNotArea is returned for a fence whose geometry is missing or is not
// a polygon. NormaliseFenceFromBytes already rejects such request bodies with
// a 400; this guards direct callers.
var errFenceNotArea = errors.New("geofence must be a Polygon or MultiPolygon")

// FenceBoundArgs returns just the bounding-box corners, as min-lat, min-lon,
// max-lat, max-lon, for the queries that select candidates by bounding box and
// test containment in Go.
func FenceBoundArgs(fence *geojson.Feature) []any {
	bbox := fence.Geometry.Bound()
	return []any{bbox.Min.Lat(), bbox.Min.Lon(), bbox.Max.Lat(), bbox.Max.Lon()}
}

// fenceMatcher tests candidate rows against a fence in Go. There is no SQL
// containment path: the only geometries that are meaningful fences are
// polygons, and those always compile.
//
// Asking MariaDB to evaluate ST_CONTAINS per candidate row is what made large
// geofences time out. Measured on 1M rows against a 2000-vertex fence whose
// bounding box held 628,560 candidates: the whole query took 55 s, while the
// bounding-box scan alone took under a second and testing the same number of
// points against a compiled fence in Go took 3.3 s. Hoisting the GeoJSON parse
// out of the row loop, with a user variable or a CTE, changed nothing, which is
// what identifies per-row containment rather than repeated parsing as the cost.
type fenceMatcher struct{ compiled *geo.CompiledFence }

// newFenceMatcher compiles fence, or returns errFenceNotArea when its
// geometry is missing or not an area (a Polygon, a MultiPolygon, or a
// GeometryCollection holding polygons).
func newFenceMatcher(fence *geojson.Feature) (fenceMatcher, error) {
	if fence == nil {
		return fenceMatcher{}, errFenceNotArea
	}
	compiled := geo.CompileFence(fence)
	if compiled == nil {
		return fenceMatcher{}, errFenceNotArea
	}
	return fenceMatcher{compiled: compiled}, nil
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
func PokestopIdsWithinFence(ctx context.Context, dbDetails DbDetails, fence *geojson.Feature) ([]string, error) {
	const label = "select pokestops for quest removal"

	matcher, err := newFenceMatcher(fence)
	if err != nil {
		return nil, err
	}

	var pokestopIds []string
	var id string
	var lat, lon float64
	err = matcher.forEachCandidate(ctx, dbDetails.GeneralDb, label,
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
