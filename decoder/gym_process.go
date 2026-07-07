package decoder

import (
	"context"
	"fmt"

	"github.com/guregu/null/v6"

	"golbat/db"
	"golbat/pogoshim"
)

func UpdateGymRecordWithFortDetailsOutProto(ctx context.Context, db db.DbDetails, fort pogoshim.FortDetailsOutProto) string {
	gym, unlock, err := getOrCreateGymRecord(ctx, db, fort.GetId(), "UpdateGymFromFortDetails")
	if err != nil {
		return err.Error()
	}
	defer unlock()

	gym.updateGymFromFortProto(fort)

	updateGymGetMapFortCache(gym, true)
	saveGymRecord(ctx, db, gym)

	return fmt.Sprintf("%s %s", gym.Id, gym.Name.ValueOrZero())
}

func UpdateGymRecordWithGymInfoProto(ctx context.Context, db db.DbDetails, gymInfo pogoshim.GymGetInfoOutProto) string {
	gym, unlock, err := getOrCreateGymRecord(ctx, db, gymInfo.GetGymStatusAndDefenders().GetPokemonFortProto().GetFortId(), "UpdateGymFromGymInfo")
	if err != nil {
		return err.Error()
	}
	defer unlock()

	gym.updateGymFromGymInfoOutProto(gymInfo)

	updateGymGetMapFortCache(gym, true)
	saveGymRecord(ctx, db, gym)
	return fmt.Sprintf("%s %s", gym.Id, gym.Name.ValueOrZero())
}

func UpdateGymRecordWithGetMapFortsOutProto(ctx context.Context, db db.DbDetails, mapFort pogoshim.GetMapFortsOutProto_FortProto) (bool, string) {
	gym, unlock, err := getGymRecordForUpdate(ctx, db, mapFort.GetId(), "UpdateGymFromGetMapForts")
	if err != nil {
		return false, err.Error()
	}

	// we missed it in Pokestop & Gym. Lets save it to cache
	if gym == nil {
		return false, ""
	}
	defer unlock()

	gym.updateGymFromMapFortSummary(mapFortSummaryFromShim(mapFort), false)
	saveGymRecord(ctx, db, gym)
	return true, fmt.Sprintf("%s %s", gym.Id, gym.Name.ValueOrZero())
}

func UpdateGymRecordWithRsvpProto(ctx context.Context, db db.DbDetails, req pogoshim.RaidDetails, resp pogoshim.GetEventRsvpsOutProto) string {
	gym, unlock, err := getGymRecordForUpdate(ctx, db, req.GetFortId(), "UpdateGymWithRsvp")
	if err != nil {
		return err.Error()
	}

	if gym == nil {
		// Do not add RSVP details to unknown gyms
		return fmt.Sprintf("%s Gym not present", req.GetFortId())
	}
	defer unlock()

	gym.updateGymFromRsvpProto(resp)

	saveGymRecord(ctx, db, gym)

	return fmt.Sprintf("%s %s", gym.Id, gym.Name.ValueOrZero())
}

func ClearGymRsvp(ctx context.Context, db db.DbDetails, fortId string) string {
	gym, unlock, err := getGymRecordForUpdate(ctx, db, fortId, "ClearGymRsvp")
	if err != nil {
		return err.Error()
	}

	if gym == nil {
		// Do not add RSVP details to unknown gyms
		return fmt.Sprintf("%s Gym not present", fortId)
	}
	defer unlock()

	if gym.Rsvps.Valid {
		gym.SetRsvps(null.NewString("", false))

		saveGymRecord(ctx, db, gym)
	}

	return fmt.Sprintf("%s %s", gym.Id, gym.Name.ValueOrZero())
}
