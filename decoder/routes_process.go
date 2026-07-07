package decoder

import (
	"context"

	"golbat/db"
	"golbat/pogoshim"
)

func UpdateRouteRecordWithSharedRouteProto(ctx context.Context, db db.DbDetails, sharedRouteProto pogoshim.SharedRouteProto) error {
	route, unlock, err := getOrCreateRouteRecord(ctx, db, sharedRouteProto.GetId(), "UpdateRouteRecord")
	if err != nil {
		return err
	}
	defer unlock()

	route.updateFromSharedRouteProto(sharedRouteProto)
	saveError := saveRouteRecord(ctx, db, route)
	return saveError
}
