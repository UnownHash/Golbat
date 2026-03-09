package decoder

import (
	"encoding/json"

	"github.com/guregu/null/v6"
	log "github.com/sirupsen/logrus"

	"golbat/pogo"
	"golbat/util"
)

func (route *Route) updateFromSharedRouteProto(sharedRouteProto *pogo.SharedRouteProto) {
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
	waypoints, _ := json.Marshal(sharedRouteProto.GetWaypoints())
	route.SetWaypoints(string(waypoints))

	if len(sharedRouteProto.GetTags()) > 0 {
		tags, _ := json.Marshal(sharedRouteProto.GetTags())
		route.SetTags(null.StringFrom(string(tags)))
	}
}
