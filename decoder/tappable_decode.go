package decoder

import (
	"context"
	"strconv"
	"time"

	"github.com/guregu/null/v6"
	log "github.com/sirupsen/logrus"

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
	if fortId := location.GetFortId(); fortId != "" {
		ta.SetFortId(null.StringFrom(fortId))
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
	ta.SetExpireTimestampVerified(false)
	if spawnId := ta.SpawnId.ValueOrZero(); spawnId != 0 {
		spawnPoint, unlock, _ := getSpawnpointRecord(ctx, db, spawnId)
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
