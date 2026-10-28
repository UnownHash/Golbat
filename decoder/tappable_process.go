package decoder

import (
	"context"
	"fmt"

	log "github.com/sirupsen/logrus"

	"golbat/db"
	"golbat/pogo"
)

func UpdateTappable(ctx context.Context, db db.DbDetails, request *pogo.ProcessTappableProto, tappableDetails *pogo.ProcessTappableOutProto, timestampMs int64) string {
	id := request.GetEncounterId()
	if id == 0 {
		return "Ignored ProcessTappableOutProto without encounter id"
	}

	// before the tappable lock: never hold two entity locks
	var pokestopName *string
	if fortId, ok := ParseFortId(request.GetLocation().GetFortId()); ok {
		name := "Unknown"
		if stop, unlockStop, _ := getPokestopRecordReadOnly(ctx, db, fortId, "UpdateTappable"); stop != nil {
			name = stop.Name.ValueOrZero()
			unlockStop()
		}
		pokestopName = &name
	}

	tappable, unlock, err := getOrCreateTappableRecord(ctx, db, id, "UpdateTappableRecord")
	if err != nil {
		log.Printf("getOrCreateTappableRecord: %s", err)
		return "Error getting tappable"
	}
	defer unlock()

	tappable.updateFromProcessTappableProto(ctx, db, tappableDetails, request, timestampMs)
	saveTappableRecord(ctx, db, tappable, pokestopName)
	return fmt.Sprintf("ProcessTappableOutProto %d", id)
}
