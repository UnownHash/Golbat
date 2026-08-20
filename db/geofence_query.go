package db

import "github.com/paulmach/orb/geojson"

// FenceContainsPredicate is the SQL fragment testing that a row's (lon, lat)
// lies inside a geofence.
//
// The fence is a bind parameter, never interpolated into the statement text.
// It arrives as a request body, and a geojson.Feature round-trips its whole
// properties map through MarshalJSON, so a property value containing a single
// quote would otherwise close the string literal it was concatenated into.
const FenceContainsPredicate = "ST_CONTAINS(ST_GeomFromGeoJSON(?, 2, 0), POINT(lon, lat))"

// FenceQueryArgs returns the bind arguments for a geofence query: the bounding
// box corners as min-lat, min-lon, max-lat, max-lon, then the fence JSON for
// FenceContainsPredicate.
//
// Every geofence query places its bounding-box comparisons before the fence
// predicate, so this order matches all of them. A query that departs from that
// layout must not use this helper.
func FenceQueryArgs(fence *geojson.Feature) ([]any, error) {
	fenceJSON, err := fence.MarshalJSON()
	if err != nil {
		return nil, err
	}
	bbox := fence.Geometry.Bound()
	return []any{
		bbox.Min.Lat(), bbox.Min.Lon(),
		bbox.Max.Lat(), bbox.Max.Lon(),
		string(fenceJSON),
	}, nil
}
