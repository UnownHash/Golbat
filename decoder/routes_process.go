package decoder

import (
	"context"
	"fmt"

	log "github.com/sirupsen/logrus"

	"golbat/db"
	"golbat/pogo"
)

// A route's start/end fort ids are the anchors that define it (start_fort_id
// and end_fort_id are NOT NULL columns), not an optional attribute — so a
// parse failure here is treated like a primary-id failure elsewhere (fort
// batch, incident processing): parsed upfront, and the whole update is
// abandoned rather than saving a route with a missing anchor.
func UpdateRouteRecordWithSharedRouteProto(ctx context.Context, db db.DbDetails, sharedRouteProto *pogo.SharedRouteProto) error {
	startFortIdStr := sharedRouteProto.GetStartPoi().GetAnchor().GetFortId()
	startFortId, ok := ParseFortId(startFortIdStr)
	if !ok {
		log.Errorf("UpdateRouteRecordWithSharedRouteProto: unparseable start fort id %q", startFortIdStr)
		return fmt.Errorf("unparseable start fort id %q", startFortIdStr)
	}
	endFortIdStr := sharedRouteProto.GetEndPoi().GetAnchor().GetFortId()
	endFortId, ok := ParseFortId(endFortIdStr)
	if !ok {
		log.Errorf("UpdateRouteRecordWithSharedRouteProto: unparseable end fort id %q", endFortIdStr)
		return fmt.Errorf("unparseable end fort id %q", endFortIdStr)
	}

	route, unlock, err := getOrCreateRouteRecord(ctx, db, sharedRouteProto.GetId(), "UpdateRouteRecord")
	if err != nil {
		return err
	}
	defer unlock()

	route.updateFromSharedRouteProto(sharedRouteProto, startFortId, endFortId)
	saveError := saveRouteRecord(ctx, db, route)
	return saveError
}
