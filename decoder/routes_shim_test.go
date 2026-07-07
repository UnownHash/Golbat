package decoder

import (
	"testing"

	"buf.build/go/hyperpb"
	"google.golang.org/protobuf/proto"

	"golbat/pogo"
	"golbat/pogoshim"
)

// hyperpbWrapSharedRoute marshals in and returns a hyperpb-backed shim; the
// returned Shared must be Freed by the caller once done with the shim (and
// everything reachable from it).
func hyperpbWrapSharedRoute(t *testing.T, in *pogo.SharedRouteProto) (pogoshim.SharedRouteProto, *hyperpb.Shared) {
	t.Helper()
	raw, err := proto.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	ty := hyperpb.CompileMessageDescriptor((*pogo.SharedRouteProto)(nil).ProtoReflect().Descriptor())
	shared := new(hyperpb.Shared)
	msg := shared.NewMessage(ty)
	if err := msg.Unmarshal(raw); err != nil {
		shared.Free()
		t.Fatal(err)
	}
	return pogoshim.AsSharedRouteProto(msg.ProtoReflect()), shared
}

// TestUpdateFromSharedRouteProtoShim locks in Wave 3 Task 3 behavior for
// updateFromSharedRouteProto: pogoshim.SharedRouteProto has no exported
// fields for json.Marshal to walk the way the pre-shim
// json.Marshal(sharedRouteProto.GetWaypoints()) did on a []*pogo.RouteWaypointProto,
// so the migration must reproduce the exact persisted JSON shape
// (routeWaypointJSON) field-for-field, plus the tags list. Runs through
// both the std and hyperpb wraps.
func TestUpdateFromSharedRouteProtoShim(t *testing.T) {
	build := func() *pogo.SharedRouteProto {
		return &pogo.SharedRouteProto{
			Id:                   "ROUTE1",
			Name:                 "Test Route",
			ShortCode:            "SC1",
			Description:          "A nice route",
			RouteDistanceMeters:  1000,
			RouteDurationSeconds: 600,
			Reversible:           true,
			Version:              3,
			Type:                 pogo.RouteType_ROUTE_TYPE_ORGANIC,
			Tags:                 []string{"scenic", "urban"},
			Waypoints: []*pogo.RouteWaypointProto{
				{FortId: "FORT1", LatDegrees: 1.5, LngDegrees: 2.5, ElevationInMeters: 10, TimestampMs: 111},
				{FortId: "FORT2", LatDegrees: 3.5, LngDegrees: 4.5},
			},
			StartPoi: &pogo.RoutePoiAnchor{
				Anchor:   &pogo.RouteWaypointProto{FortId: "START_FORT", LatDegrees: 1.1, LngDegrees: 2.2},
				ImageUrl: "http://example.com/start.png",
			},
			EndPoi: &pogo.RoutePoiAnchor{
				Anchor:   &pogo.RouteWaypointProto{FortId: "END_FORT", LatDegrees: 5.5, LngDegrees: 6.6},
				ImageUrl: "http://example.com/end.png",
			},
			Image: &pogo.RouteImageProto{ImageUrl: "http://example.com/route.png", BorderColorHex: "#ABCDEF"},
		}
	}

	const wantWaypointsJSON = `[{"fort_id":"FORT1","lat_degrees":1.5,"lng_degrees":2.5,"elevation_in_meters":10,"timestamp_ms":111},{"fort_id":"FORT2","lat_degrees":3.5,"lng_degrees":4.5}]`
	const wantTagsJSON = `["scenic","urban"]`

	check := func(name string, shim pogoshim.SharedRouteProto) {
		route := &Route{RouteData: RouteData{Id: "ROUTE1"}}
		route.updateFromSharedRouteProto(shim)

		if route.Name != "Test Route" {
			t.Errorf("%s: Name = %q", name, route.Name)
		}
		if route.Shortcode != "SC1" {
			t.Errorf("%s: Shortcode = %q", name, route.Shortcode)
		}
		if route.Description != "A nice route" {
			t.Errorf("%s: Description = %q", name, route.Description)
		}
		if route.DistanceMeters != 1000 || route.DurationSeconds != 600 {
			t.Errorf("%s: distance/duration = %d/%d", name, route.DistanceMeters, route.DurationSeconds)
		}
		if route.StartFortId != "START_FORT" || route.StartLat != 1.1 || route.StartLon != 2.2 {
			t.Errorf("%s: start poi mismatch: %s %f %f", name, route.StartFortId, route.StartLat, route.StartLon)
		}
		if route.EndFortId != "END_FORT" || route.EndLat != 5.5 || route.EndLon != 6.6 {
			t.Errorf("%s: end poi mismatch: %s %f %f", name, route.EndFortId, route.EndLat, route.EndLon)
		}
		if route.StartImage != "http://example.com/start.png" || route.EndImage != "http://example.com/end.png" {
			t.Errorf("%s: poi images mismatch: %s %s", name, route.StartImage, route.EndImage)
		}
		if route.Image != "http://example.com/route.png" || route.ImageBorderColor != "#ABCDEF" {
			t.Errorf("%s: image mismatch: %s %s", name, route.Image, route.ImageBorderColor)
		}
		if !route.Reversible {
			t.Errorf("%s: Reversible = false, want true", name)
		}
		if route.Version != 3 {
			t.Errorf("%s: Version = %d, want 3", name, route.Version)
		}
		if route.Type != int8(pogo.RouteType_ROUTE_TYPE_ORGANIC) {
			t.Errorf("%s: Type = %d, want %d", name, route.Type, int8(pogo.RouteType_ROUTE_TYPE_ORGANIC))
		}
		if route.Waypoints != wantWaypointsJSON {
			t.Errorf("%s: Waypoints JSON =\n%s\nwant\n%s", name, route.Waypoints, wantWaypointsJSON)
		}
		if !route.Tags.Valid || route.Tags.String != wantTagsJSON {
			t.Errorf("%s: Tags = %+v, want %s", name, route.Tags, wantTagsJSON)
		}
	}

	stdIn := build()
	check("std", pogoshim.AsSharedRouteProto(stdIn.ProtoReflect()))

	hyperShim, shared := hyperpbWrapSharedRoute(t, build())
	defer shared.Free()
	check("hyperpb", hyperShim)
}

// TestUpdateFromSharedRouteProtoShim_NoWaypointsOrTags locks in the
// nil-vs-empty-slice JSON rendering the migration must preserve: an absent
// waypoints/tags list must render as a JSON "null" (Waypoints) or leave Tags
// unset entirely (matching the pre-shim `len(...) > 0` guard), never an
// empty-but-present `[]` -- the same class of fix Wave 3 Task 2 made for
// quest's WITH_LOCATION cell ids.
func TestUpdateFromSharedRouteProtoShim_NoWaypointsOrTags(t *testing.T) {
	build := func() *pogo.SharedRouteProto {
		return &pogo.SharedRouteProto{Id: "ROUTE2", Name: "Bare Route"}
	}

	check := func(name string, shim pogoshim.SharedRouteProto) {
		route := &Route{RouteData: RouteData{Id: "ROUTE2"}}
		route.updateFromSharedRouteProto(shim)

		if route.Waypoints != "null" {
			t.Errorf("%s: Waypoints = %q, want \"null\"", name, route.Waypoints)
		}
		if route.Tags.Valid {
			t.Errorf("%s: Tags should remain unset, got %+v", name, route.Tags)
		}
	}

	stdIn := build()
	check("std", pogoshim.AsSharedRouteProto(stdIn.ProtoReflect()))

	hyperShim, shared := hyperpbWrapSharedRoute(t, build())
	defer shared.Free()
	check("hyperpb", hyperShim)
}
