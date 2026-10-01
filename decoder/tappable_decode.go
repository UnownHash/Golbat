package decoder

import (
	"context"
	"strconv"

	"github.com/guregu/null/v6"
	log "github.com/sirupsen/logrus"

	"golbat/config"
	"golbat/db"
	"golbat/pogo"
)

func (ta *Tappable) updateFromProcessTappableProto(ctx context.Context, db db.DbDetails, tappable *pogo.ProcessTappableOutProto, request *pogo.ProcessTappableProto, timestampMs int64) {
	// update from request
	ta.Id = request.EncounterId // Id is primary key, don't track as dirty
	location := request.GetLocation()
	if spawnPointId := location.GetSpawnpointId(); spawnPointId != "" {
		spawnId, err := strconv.ParseInt(spawnPointId, 16, 64)
		if err != nil {
			panic(err)
		}
		ta.SetSpawnId(null.IntFrom(spawnId))
	}
	if fortIdStr := location.GetFortId(); fortIdStr != "" {
		if fortId, ok := ParseFortId(fortIdStr); ok {
			ta.SetFortId(fortId)
		} else {
			log.Errorf("[TAPPABLE] unparseable fort id %q, skipping", fortIdStr)
		}
	}
	ta.SetType(request.TappableTypeId)
	ta.SetLat(request.LocationHintLat)
	ta.SetLon(request.LocationHintLng)
	ta.setExpireTimestamp(ctx, db, timestampMs)

	// update from tappable
	if encounter := tappable.GetEncounter(); encounter != nil {
		// tappable is a Pokèmon, encounter is sent in a separate proto
		// we store this to link tappable with Pokèmon from encounter proto
		ta.SetEncounter(null.IntFrom(int64(encounter.Pokemon.PokemonId)))
	} else if reward := tappable.GetReward(); reward != nil {
		for _, lootProto := range reward {
			for _, itemProto := range lootProto.GetLootItem() {
				switch t := itemProto.Type.(type) {
				case *pogo.LootItemProto_Item:
					ta.SetItemId(null.IntFrom(int64(t.Item)))
					ta.SetCount(null.IntFrom(int64(itemProto.Count)))
				case *pogo.LootItemProto_Stardust:
					log.Warnf("[TAPPABLE] Reward is Stardust: %t", t.Stardust)
				case *pogo.LootItemProto_Pokecoin:
					log.Warnf("[TAPPABLE] Reward is Pokecoin: %t", t.Pokecoin)
				case *pogo.LootItemProto_PokemonCandy:
					log.Warnf("[TAPPABLE] Reward is Pokemon Candy: %v", t.PokemonCandy)
				case *pogo.LootItemProto_Experience:
					log.Warnf("[TAPPABLE] Reward is Experience: %t", t.Experience)
				case *pogo.LootItemProto_PokemonEgg:
					log.Warnf("[TAPPABLE] Reward is a Pokemon Egg: %v", t.PokemonEgg)
				case *pogo.LootItemProto_AvatarTemplateId:
					log.Warnf("[TAPPABLE] Reward is an Avatar Template ID: %v", t.AvatarTemplateId)
				case *pogo.LootItemProto_StickerId:
					log.Warnf("[TAPPABLE] Reward is a Sticker ID: %s", t.StickerId)
				case *pogo.LootItemProto_MegaEnergyPokemonId:
					log.Warnf("[TAPPABLE] Reward is Mega Energy Pokemon ID: %v", t.MegaEnergyPokemonId)
				case *pogo.LootItemProto_XlCandy:
					log.Warnf("[TAPPABLE] Reward is XL Candy: %v", t.XlCandy)
				case *pogo.LootItemProto_FollowerPokemon:
					log.Warnf("[TAPPABLE] Reward is a Follower Pokemon: %v", t.FollowerPokemon)
				case *pogo.LootItemProto_NeutralAvatarTemplateId:
					log.Warnf("[TAPPABLE] Reward is a Neutral Avatar Template ID: %v", t.NeutralAvatarTemplateId)
				case *pogo.LootItemProto_NeutralAvatarItemTemplate:
					log.Warnf("[TAPPABLE] Reward is a Neutral Avatar Item Template: %v", t.NeutralAvatarItemTemplate)
				case *pogo.LootItemProto_NeutralAvatarItemDisplay:
					log.Warnf("[TAPPABLE] Reward is a Neutral Avatar Item Display: %v", t.NeutralAvatarItemDisplay)
				default:
					log.Warnf("Unknown or unset Type")
				}
			}
		}
	}
}

func (ta *Tappable) setExpireTimestamp(ctx context.Context, db db.DbDetails, timestampMs int64) {
	if spawnId := ta.SpawnId.ValueOrZero(); spawnId != 0 {
		spawnPoint, unlock, err := getSpawnpointRecord(ctx, db, spawnId, "updateFromTappableEncounter")
		lookupFailed := spawnpointLookupFailed(spawnPoint, err)
		if spawnPoint != nil && spawnPoint.DespawnSec.Valid {
			despawnSecond := int(spawnPoint.DespawnSec.ValueOrZero())
			unlock()
			ta.applyVerifiedDespawn(despawnSecond, timestampMs)
			return
		}
		if unlock != nil {
			unlock()
		}
		// A failed lookup is not an unknown despawn: the row may hold the
		// despawn time an existing expiry came from. One still in the future
		// is kept, with its flag, as Pokemon.setExpireTimestampFromSpawnpoint
		// does.
		if lookupFailed && ta.ExpireTimestamp.Valid && ta.ExpireTimestamp.Int64 > timestampMs/1000 {
			return
		}
		ta.SetExpireTimestampVerified(false)
		ta.applyDerivedDespawn(spawnId, timestampMs)
		return
	}
	ta.SetExpireTimestampVerified(false)
	if ta.FortId.Valid() {
		// we don't know any despawn times from lured/fort tappables
		ta.SetExpireTimestamp(null.IntFrom(int64(timestampMs)/1000 + int64(120)))
	}
}

// applyVerifiedDespawn converts a spawnpoint despawn second-of-hour into a
// verified expire timestamp for this tappable — mirrors
// Pokemon.applyVerifiedDespawn (pokemon_decode.go), sharing the same
// offset/wrap/clamp arithmetic via resolveDespawnExpiry. despawn_sec is
// `unix % 3600`, an absolute second-of-hour with no process timezone
// involved — see despawnSecondOfHour's doc comment.
func (ta *Tappable) applyVerifiedDespawn(despawnSecond int, timestampMs int64) {
	nowSec := timestampMs / 1000
	expiry, clamped := resolveDespawnExpiry(despawnSecond, nowSec, ta.firstSeen)

	countDespawnWrapClamp(clamped, ta.ExpireTimestamp.Valid, ta.ExpireTimestamp.Int64, expiry)

	ta.SetExpireTimestamp(null.IntFrom(expiry))
	ta.SetExpireTimestampVerified(true)
}

// applyDerivedDespawn derives a tappable's expiry from its spawnpoint id, as
// Pokemon.applyDerivedDespawn does, and leaves it unverified. A tappable's
// timestamp is the receipt time, so an expiry already derived for this
// spawnpoint is kept unless it passed more than despawnClockSkewTolerance
// ago, for the same reason as there. With derive_despawn off it sets the
// guess setUnknownTimestamp sets instead.
func (ta *Tappable) applyDerivedDespawn(spawnId int64, timestampMs int64) {
	nowSec := timestampMs / 1000
	if !config.Config.DeriveDespawn {
		ta.setUnknownTimestamp(nowSec)
		return
	}
	if ta.ExpireTimestamp.Valid {
		if existing := ta.ExpireTimestamp.Int64; existing >= nowSec-despawnClockSkewTolerance && onDerivedPhase(spawnId, existing) {
			return
		}
	}
	ta.SetExpireTimestamp(null.IntFrom(deriveExpiry(spawnId, nowSec)))
}

// setUnknownTimestamp is the expiry guess for a tappable whose despawn is
// neither observed nor derived: 20 minutes from now, or 10 more once a guess
// has passed.
func (ta *Tappable) setUnknownTimestamp(now int64) {
	if !ta.ExpireTimestamp.Valid {
		ta.SetExpireTimestamp(null.IntFrom(now + 20*60))
	} else if ta.ExpireTimestamp.Int64 < now {
		ta.SetExpireTimestamp(null.IntFrom(now + 10*60))
	}
}
