package decoder

import (
	"context"
	"strconv"
	"time"

	"github.com/guregu/null/v6"
	log "github.com/sirupsen/logrus"

	"golbat/db"
	"golbat/pogoshim"
)

func (ta *Tappable) updateFromProcessTappableProto(ctx context.Context, db db.DbDetails, tappable pogoshim.ProcessTappableOutProto, request pogoshim.ProcessTappableProto, timestampMs int64) {
	// update from request
	ta.Id = request.GetEncounterId() // Id is primary key, don't track as dirty
	location := request.GetLocation()
	if spawnPointId := location.GetSpawnpointId(); spawnPointId != "" {
		spawnId, err := strconv.ParseInt(spawnPointId, 16, 64)
		if err != nil {
			panic(err)
		}
		ta.SetSpawnId(null.IntFrom(spawnId))
	}
	if fortId := location.GetFortId(); fortId != "" {
		ta.SetFortId(null.StringFrom(fortId))
	}
	ta.SetType(request.GetTappableTypeId())
	ta.SetLat(request.GetLocationHintLat())
	ta.SetLon(request.GetLocationHintLng())
	ta.setExpireTimestamp(ctx, db, timestampMs)

	// update from tappable
	if tappable.HasEncounter() {
		// tappable is a Pokèmon, encounter is sent in a separate proto
		// we store this to link tappable with Pokèmon from encounter proto
		ta.SetEncounter(null.IntFrom(int64(tappable.GetEncounter().GetPokemon().GetPokemonId())))
	} else if reward := tappable.GetReward(); reward.Len() > 0 {
		for lootProto := range reward.All() {
			for itemProto := range lootProto.GetLootItem().All() {
				// LootItemProto's 14-way "Type" oneof has no type-switch
				// equivalent over the shim (there's no interface value to
				// switch on) -- Has<Field>() replaces each case, in the same
				// order as the original type switch. This oneof is exactly
				// why cmd/pogoshimgen/main.go's generator was taught to emit
				// Has<Field>() for scalar/enum oneof members too (previously
				// only singular message fields got one): GetItem()==0 alone
				// can't distinguish "Item explicitly set to enum value 0"
				// from "some other Type member is set", which would have
				// silently miscategorized rewards.
				switch {
				case itemProto.HasItem():
					ta.SetItemId(null.IntFrom(int64(itemProto.GetItem())))
					ta.SetCount(null.IntFrom(int64(itemProto.GetCount())))
				case itemProto.HasStardust():
					log.Warnf("[TAPPABLE] Reward is Stardust: %t", itemProto.GetStardust())
				case itemProto.HasPokecoin():
					log.Warnf("[TAPPABLE] Reward is Pokecoin: %t", itemProto.GetPokecoin())
				case itemProto.HasPokemonCandy():
					log.Warnf("[TAPPABLE] Reward is Pokemon Candy: %v", itemProto.GetPokemonCandy())
				case itemProto.HasExperience():
					log.Warnf("[TAPPABLE] Reward is Experience: %t", itemProto.GetExperience())
				case itemProto.HasPokemonEgg():
					// Message-typed oneof members already had a shim
					// Has/Get pair before this task's generator fix; only
					// the %v rendering differs from the pre-shim pointer
					// (shim has no String() method) -- log-line-only, see
					// the Wave 3 Task 4 report's Concerns section.
					log.Warnf("[TAPPABLE] Reward is a Pokemon Egg: %v", itemProto.GetPokemonEgg())
				case itemProto.HasAvatarTemplateId():
					log.Warnf("[TAPPABLE] Reward is an Avatar Template ID: %v", itemProto.GetAvatarTemplateId())
				case itemProto.HasStickerId():
					log.Warnf("[TAPPABLE] Reward is a Sticker ID: %s", itemProto.GetStickerId())
				case itemProto.HasMegaEnergyPokemonId():
					log.Warnf("[TAPPABLE] Reward is Mega Energy Pokemon ID: %v", itemProto.GetMegaEnergyPokemonId())
				case itemProto.HasXlCandy():
					log.Warnf("[TAPPABLE] Reward is XL Candy: %v", itemProto.GetXlCandy())
				case itemProto.HasFollowerPokemon():
					log.Warnf("[TAPPABLE] Reward is a Follower Pokemon: %v", itemProto.GetFollowerPokemon())
				case itemProto.HasNeutralAvatarTemplateId():
					log.Warnf("[TAPPABLE] Reward is a Neutral Avatar Template ID: %v", itemProto.GetNeutralAvatarTemplateId())
				case itemProto.HasNeutralAvatarItemTemplate():
					log.Warnf("[TAPPABLE] Reward is a Neutral Avatar Item Template: %v", itemProto.GetNeutralAvatarItemTemplate())
				case itemProto.HasNeutralAvatarItemDisplay():
					log.Warnf("[TAPPABLE] Reward is a Neutral Avatar Item Display: %v", itemProto.GetNeutralAvatarItemDisplay())
				default:
					log.Warnf("Unknown or unset Type")
				}
			}
		}
	}
}

func (ta *Tappable) setExpireTimestamp(ctx context.Context, db db.DbDetails, timestampMs int64) {
	ta.SetExpireTimestampVerified(false)
	if spawnId := ta.SpawnId.ValueOrZero(); spawnId != 0 {
		spawnPoint, unlock, _ := getSpawnpointRecord(ctx, db, spawnId, "updateFromTappableEncounter")
		if spawnPoint != nil && spawnPoint.DespawnSec.Valid {
			despawnSecond := int(spawnPoint.DespawnSec.ValueOrZero())
			unlock()

			date := time.Unix(timestampMs/1000, 0)
			secondOfHour := date.Second() + date.Minute()*60

			despawnOffset := despawnSecond - secondOfHour
			if despawnOffset < 0 {
				despawnOffset += 3600
			}
			ta.SetExpireTimestamp(null.IntFrom(int64(timestampMs)/1000 + int64(despawnOffset)))
			ta.SetExpireTimestampVerified(true)
		} else {
			if unlock != nil {
				unlock()
			}
			ta.setUnknownTimestamp(timestampMs / 1000)
		}
	} else if fortId := ta.FortId.ValueOrZero(); fortId != "" {
		// we don't know any despawn times from lured/fort tappables
		ta.SetExpireTimestamp(null.IntFrom(int64(timestampMs)/1000 + int64(120)))
	}
}

func (ta *Tappable) setUnknownTimestamp(now int64) {
	if !ta.ExpireTimestamp.Valid {
		ta.SetExpireTimestamp(null.IntFrom(now + 20*60))
	} else {
		if ta.ExpireTimestamp.Int64 < now {
			ta.SetExpireTimestamp(null.IntFrom(now + 10*60))
		}
	}
}
