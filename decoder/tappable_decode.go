package decoder

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/guregu/null/v6"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"

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
		ta.setRewards(tappableRewards{})
	} else if reward := tappable.GetReward(); reward != nil {
		ta.SetEncounter(null.Int{})
		ta.setRewards(decodeLootRewards(ta.Id, reward))
	}
}

// tappableRewards holds the reward fields of a tappable: the columns describe
// the first stored loot item, Rewards holds all of them.
type tappableRewards struct {
	Type      null.String
	ItemId    null.Int
	PokemonId null.Int
	Count     null.Int
	Rewards   null.String
}

// decodeLootRewards builds the reward fields from a tappable's loot. Items that
// can't be identified or marshaled are skipped, so the columns always match rewards[0].
func decodeLootRewards(id uint64, loot []*pogo.LootProto) tappableRewards {
	var r tappableRewards
	var buf bytes.Buffer
	stored := 0
	for _, lootProto := range loot {
		for _, itemProto := range lootProto.GetLootItem() {
			field := itemProto.ProtoReflect().WhichOneof(lootItemTypeOneof)
			if field == nil {
				log.Warnf("[TAPPABLE] %d: unknown or unset loot type (unknown fields: %x)", id, itemProto.ProtoReflect().GetUnknown())
				continue
			}
			itemJson, err := lootItemJsonOptions.Marshal(itemProto)
			if err != nil {
				log.Errorf("[TAPPABLE] %d: marshal loot item: %s", id, err)
				continue
			}
			before := buf.Len()
			if stored == 0 {
				buf.WriteByte('[')
			} else {
				buf.WriteByte(',')
			}
			// protojson whitespace is unstable; compact so unchanged loot doesn't mark the tappable dirty
			if err := json.Compact(&buf, itemJson); err != nil {
				buf.Truncate(before)
				log.Errorf("[TAPPABLE] %d: compact loot item: %s", id, err)
				continue
			}
			if stored == 0 {
				r.Type = null.StringFrom(string(field.Name()))
				r.ItemId, r.PokemonId = lootItemIds(itemProto)
				r.Count = null.IntFrom(int64(itemProto.Count))
			}
			stored++
		}
	}
	if stored > 0 {
		buf.WriteByte(']')
		r.Rewards = null.StringFrom(buf.String())
	}
	return r
}

// lootItemJsonOptions renders loot items with proto field names and numeric enums,
// e.g. {"pokemon_candy":25,"count":6}.
var lootItemJsonOptions = protojson.MarshalOptions{UseProtoNames: true, UseEnumNumbers: true}

// lootItemIds returns the item id or species a loot item carries, if any.
func lootItemIds(item *pogo.LootItemProto) (itemId, pokemonId null.Int) {
	switch t := item.Type.(type) {
	case *pogo.LootItemProto_Item:
		itemId = null.IntFrom(int64(t.Item))
	case *pogo.LootItemProto_PokemonCandy:
		pokemonId = null.IntFrom(int64(t.PokemonCandy))
	case *pogo.LootItemProto_XlCandy:
		pokemonId = null.IntFrom(int64(t.XlCandy))
	case *pogo.LootItemProto_MegaEnergyPokemonId:
		pokemonId = null.IntFrom(int64(t.MegaEnergyPokemonId))
	case *pogo.LootItemProto_TempEvoPokemonBranch:
		// temp evo id (mega X/Y) is only in rewards
		pokemonId = null.IntFrom(int64(t.TempEvoPokemonBranch.GetPokedexId()))
	}
	return itemId, pokemonId
}

// lootItemTypeOneof is LootItemProto's reward oneof; its set field name is stored as reward_type.
var lootItemTypeOneof = (&pogo.LootItemProto{}).ProtoReflect().Descriptor().Oneofs().ByName("Type")

// setRewards sets all reward fields together, so none is left from a previous decode.
func (ta *Tappable) setRewards(r tappableRewards) {
	ta.SetRewardType(r.Type)
	ta.SetItemId(r.ItemId)
	ta.SetRewardPokemonId(r.PokemonId)
	ta.SetCount(r.Count)
	ta.SetRewards(r.Rewards)
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
	} else if ta.FortId.Valid() {
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
