package decoder

import (
	"context"
	"fmt"

	log "github.com/sirupsen/logrus"

	"golbat/db"
	"golbat/pogoshim"
)

func UpdateTappable(ctx context.Context, db db.DbDetails, request pogoshim.ProcessTappableProto, tappableDetails pogoshim.ProcessTappableOutProto, timestampMs int64) string {
	id := request.GetEncounterId()

	tappable, unlock, err := getOrCreateTappableRecord(ctx, db, id, "UpdateTappableRecord")
	if err != nil {
		log.Printf("getOrCreateTappableRecord: %s", err)
		return "Error getting tappable"
	}
	defer unlock()

	tappable.updateFromProcessTappableProto(ctx, db, tappableDetails, request, timestampMs)
	saveTappableRecord(ctx, db, tappable)
	return fmt.Sprintf("ProcessTappableOutProto %d", id)
}
