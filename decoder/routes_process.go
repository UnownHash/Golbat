package decoder

import (
	"context"

	"golbat/db"
	"golbat/pogo"
)

func UpdateRouteRecordWithSharedRouteProto(ctx context.Context, db db.DbDetails, sharedRouteProto *pogo.SharedRouteProto) error {
	route, unlock, err := getOrCreateRouteRecord(ctx, db, sharedRouteProto.GetId())
	if err != nil {
		return err
	}
	defer unlock()

	route.updateFromSharedRouteProto(sharedRouteProto)
	saveError := saveRouteRecord(ctx, db, route)
	return saveError
}
