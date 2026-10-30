package decoder

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"

	"github.com/guregu/null/v6"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"

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
