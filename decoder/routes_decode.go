package decoder

import (
	"encoding/json"

	"github.com/guregu/null/v6"
	log "github.com/sirupsen/logrus"

	"golbat/pogoshim"
	"golbat/util"
)

// routeWaypointJSON mirrors *pogo.RouteWaypointProto's `encoding/json` shape
// exactly (field-for-field, same json tags/omitempty) -- pogoshim's
// RouteWaypointProto is a protoreflect wrapper with no exported fields of
// its own, so json.Marshal can't be pointed at it directly the way the
// pre-shim code pointed json.Marshal at a []*pogo.RouteWaypointProto. This
// keeps the persisted Waypoints JSON column byte-identical to what the
// original marshal produced.
type routeWaypointJSON struct {
	FortId            string  `json:"fort_id,omitempty"`
	LatDegrees        float64 `json:"lat_degrees,omitempty"`
	LngDegrees        float64 `json:"lng_degrees,omitempty"`
	ElevationInMeters float64 `json:"elevation_in_meters,omitempty"`
	TimestampMs       int64   `json:"timestamp_ms,omitempty"`
}

func (route *Route) updateFromSharedRouteProto(sharedRouteProto pogoshim.SharedRouteProto) {
	route.SetName(sharedRouteProto.GetName())
	if sharedRouteProto.GetShortCode() != "" {
		route.SetShortcode(sharedRouteProto.GetShortCode())
	}
	description := sharedRouteProto.GetDescription()
	// NOTE: Some descriptions have more than 255 runes, which won't fit in our
	// varchar(255).
	if truncateStr, truncated := util.TruncateUTF8(description, 255); truncated {
		log.Warnf("truncating description for route id '%s'. Orig description: %s",
			route.Id,
			description,
		)
		description = truncateStr
	}
	route.SetDescription(description)
	route.SetDistanceMeters(sharedRouteProto.GetRouteDistanceMeters())
	route.SetDurationSeconds(sharedRouteProto.GetRouteDurationSeconds())
	route.SetEndFortId(sharedRouteProto.GetEndPoi().GetAnchor().GetFortId())
	route.SetEndImage(sharedRouteProto.GetEndPoi().GetImageUrl())
	route.SetEndLat(sharedRouteProto.GetEndPoi().GetAnchor().GetLatDegrees())
	route.SetEndLon(sharedRouteProto.GetEndPoi().GetAnchor().GetLngDegrees())
	route.SetImage(sharedRouteProto.GetImage().GetImageUrl())
	route.SetImageBorderColor(sharedRouteProto.GetImage().GetBorderColorHex())
	route.SetReversible(sharedRouteProto.GetReversible())
	route.SetStartFortId(sharedRouteProto.GetStartPoi().GetAnchor().GetFortId())
	route.SetStartImage(sharedRouteProto.GetStartPoi().GetImageUrl())
	route.SetStartLat(sharedRouteProto.GetStartPoi().GetAnchor().GetLatDegrees())
	route.SetStartLon(sharedRouteProto.GetStartPoi().GetAnchor().GetLngDegrees())
	route.SetType(int8(sharedRouteProto.GetType()))
	route.SetVersion(sharedRouteProto.GetVersion())

	// nil (not an empty, allocated slice) when there are no waypoints, so
	// json.Marshal renders "null" exactly like the pre-shim
	// []*pogo.RouteWaypointProto(nil) it replaces -- see quest's analogous
	// nil-vs-[] fix (Wave 3 Task 2) for the same JSON-rendering concern.
	var waypointsJSON []routeWaypointJSON
	waypointsList := sharedRouteProto.GetWaypoints()
	if n := waypointsList.Len(); n > 0 {
		waypointsJSON = make([]routeWaypointJSON, n)
		for i := 0; i < n; i++ {
			wp := waypointsList.At(i)
			waypointsJSON[i] = routeWaypointJSON{
				FortId:            wp.GetFortId(),
				LatDegrees:        wp.GetLatDegrees(),
				LngDegrees:        wp.GetLngDegrees(),
				ElevationInMeters: wp.GetElevationInMeters(),
				TimestampMs:       wp.GetTimestampMs(),
			}
		}
	}
	waypoints, _ := json.Marshal(waypointsJSON)
	route.SetWaypoints(string(waypoints))

	tagsList := sharedRouteProto.GetTags()
	if tagsList.Len() > 0 {
		tagsSlice := make([]string, tagsList.Len())
		for i := 0; i < tagsList.Len(); i++ {
			tagsSlice[i] = tagsList.At(i)
		}
		tags, _ := json.Marshal(tagsSlice)
		route.SetTags(null.StringFrom(string(tags)))
	}
}
