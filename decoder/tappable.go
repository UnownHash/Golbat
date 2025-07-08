package decoder

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"golbat/db"
	"golbat/pogo"
	"strconv"
	"time"

	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
	"gopkg.in/guregu/null.v4"
)

type Tappable struct {
	Id                      uint64      `db:"id" json:"id"`
	Lat                     float64     `db:"lat" json:"lat"`
	Lon                     float64     `db:"lon" json:"lon"`
	FortId                  null.String `db:"fort_id" json:"fort_id"` // either fortId or spawnpointId are given
	SpawnId                 null.Int    `db:"spawn_id" json:"spawn_id"`
	Type                    string      `db:"type" json:"type"`
	Encounter               null.Int    `db:"pokemon_id" json:"pokemon_id"`
	ItemId                  null.Int    `db:"item_id" json:"item_id"`
	Count                   null.Int    `db:"count" json:"count"`
	ExpireTimestamp         null.Int    `db:"expire_timestamp" json:"expire_timestamp"`
	ExpireTimestampVerified bool        `db:"expire_timestamp_verified" json:"expire_timestamp_verified"`
	Updated                 int64       `db:"updated" json:"updated"`
}

func (ta *Tappable) updateFromProcessTappableProto(ctx context.Context, db db.DbDetails, tappable *pogo.ProcessTappableOutProto, request *pogo.ProcessTappableProto, timestampMs int64) {
	// update from request
	ta.Id = request.EncounterId
	location := request.GetLocation()
	if spawnPointId := location.GetSpawnpointId(); spawnPointId != "" {
		spawnId, err := strconv.ParseInt(spawnPointId, 16, 64)
		if err != nil {
			panic(err)
		}
		ta.SpawnId = null.IntFrom(spawnId)
	}
	if fortId := location.GetFortId(); fortId != "" {
		ta.FortId = null.StringFrom(fortId)
	}
	ta.Type = request.TappableTypeId
	ta.Lat = request.LocationHintLat
	ta.Lon = request.LocationHintLng
	ta.setExpireTimestamp(ctx, db, timestampMs)

	// update from tappable
	if encounter := tappable.GetEncounter(); encounter != nil {
		// tappable is a Pokèmon, encounter is sent in a separate proto
		// we store this to link tappable with Pokèmon from encounter proto
		ta.Encounter = null.IntFrom(int64(encounter.Pokemon.PokemonId))
	} else if reward := tappable.GetReward(); reward != nil {
		for _, lootProto := range reward {
			for _, itemProto := range lootProto.GetLootItem() {
				switch t := itemProto.Type.(type) {
				case *pogo.LootItemProto_Item:
					ta.ItemId = null.IntFrom(int64(t.Item))
					ta.Count = null.IntFrom(int64(itemProto.Count))
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
	ta.ExpireTimestampVerified = false
	if spawnId := ta.SpawnId.ValueOrZero(); spawnId != 0 {
		spawnPoint, _ := getSpawnpointRecord(ctx, db, spawnId)
		if spawnPoint != nil && spawnPoint.DespawnSec.Valid {
			despawnSecond := int(spawnPoint.DespawnSec.ValueOrZero())

			date := time.Unix(timestampMs/1000, 0)
			secondOfHour := date.Second() + date.Minute()*60

			despawnOffset := despawnSecond - secondOfHour
			if despawnOffset < 0 {
				despawnOffset += 3600
			}
			ta.ExpireTimestamp = null.IntFrom(int64(timestampMs)/1000 + int64(despawnOffset))
			ta.ExpireTimestampVerified = true
		} else {
			ta.setUnknownTimestamp(timestampMs / 1000)
		}
	} else if fortId := ta.FortId.ValueOrZero(); fortId != "" {
		// we don't know any despawn times from lured/fort tappables
		ta.ExpireTimestamp = null.IntFrom(int64(timestampMs)/1000 + int64(120))
	}
}

func (ta *Tappable) setUnknownTimestamp(now int64) {
	if !ta.ExpireTimestamp.Valid {
		ta.ExpireTimestamp = null.IntFrom(now + 20*60)
	} else {
		if ta.ExpireTimestamp.Int64 < now {
			ta.ExpireTimestamp = null.IntFrom(now + 10*60)
		}
	}
}

func GetTappableRecord(ctx context.Context, db db.DbDetails, id uint64) (*Tappable, error) {
	inMemoryTappable := tappableCache.Get(id)
	if inMemoryTappable != nil {
		tappable := inMemoryTappable.Value()
		return &tappable, nil
	}
	tappable := Tappable{}
	err := db.GeneralDb.GetContext(ctx, &tappable,
		`SELECT id, lat, lon, fort_id, spawn_id, type, pokemon_id, item_id, count, expire_timestamp, expire_timestamp_verified, updated
         FROM tappable 
         WHERE id = ?`, strconv.FormatUint(id, 10))
	statsCollector.IncDbQuery("select tappable", err)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}
	return &tappable, nil
}

func saveTappableRecord(ctx context.Context, details db.DbDetails, tappable *Tappable) {
	oldTappable, _ := GetTappableRecord(ctx, details, tappable.Id)
	now := time.Now().Unix()
	if oldTappable != nil && !hasChangesTappable(oldTappable, tappable) {
		return
	}
	tappable.Updated = now
	if oldTappable == nil {
		res, err := details.GeneralDb.NamedExecContext(ctx, fmt.Sprintf(`
			INSERT INTO tappable (
				id, lat, lon, fort_id, spawn_id, type, pokemon_id, item_id, count, expire_timestamp, expire_timestamp_verified, updated
			) VALUES (
				"%d", :lat, :lon, :fort_id, :spawn_id, :type, :pokemon_id, :item_id, :count, :expire_timestamp, :expire_timestamp_verified, :updated
			)
			`, tappable.Id), tappable)
		statsCollector.IncDbQuery("insert tappable", err)
		if err != nil {
			log.Errorf("insert tappable %d: %s", tappable.Id, err)
			return
		}
		_ = res
	} else {
		res, err := details.GeneralDb.NamedExecContext(ctx, fmt.Sprintf(`
			UPDATE tappable SET
				lat = :lat,
				lon = :lon,
				fort_id = :fort_id,
				spawn_id = :spawn_id,
				type = :type,
				pokemon_id = :pokemon_id,
				item_id = :item_id,
				count = :count,
				expire_timestamp = :expire_timestamp, 
				expire_timestamp_verified = :expire_timestamp_verified,
				updated = :updated
			WHERE id = "%d"
			`, tappable.Id), tappable)
		statsCollector.IncDbQuery("update tappable", err)
		if err != nil {
			log.Errorf("update tappable %d: %s", tappable.Id, err)
			return
		}
		_ = res
	}
	tappableCache.Set(tappable.Id, *tappable, ttlcache.DefaultTTL)
}

func hasChangesTappable(old *Tappable, new *Tappable) bool {
	return old.Id != new.Id ||
		old.FortId != new.FortId ||
		old.SpawnId != new.SpawnId ||
		old.Type != new.Type ||
		old.Encounter != new.Encounter ||
		old.ItemId != new.ItemId ||
		old.Count != new.Count ||
		old.ExpireTimestamp != new.ExpireTimestamp ||
		old.ExpireTimestampVerified != new.ExpireTimestampVerified ||
		!floatAlmostEqual(old.Lat, new.Lat, floatTolerance) ||
		!floatAlmostEqual(old.Lon, new.Lon, floatTolerance)
}

func UpdateTappable(ctx context.Context, db db.DbDetails, request *pogo.ProcessTappableProto, tappableDetails *pogo.ProcessTappableOutProto, timestampMs int64) string {
	id := request.GetEncounterId()
	tappableMutex, _ := tappableStripedMutex.GetLock(id)
	tappableMutex.Lock()
	defer tappableMutex.Unlock()

	tappable, err := GetTappableRecord(ctx, db, id)
	if err != nil {
		log.Printf("Get tappable %s", err)
		return "Error getting tappable"
	}

	if tappable == nil {
		tappable = &Tappable{}
	}

	tappable.updateFromProcessTappableProto(ctx, db, tappableDetails, request, timestampMs)
	saveTappableRecord(ctx, db, tappable)
	return fmt.Sprintf("ProcessTappableOutProto %d", id)
}
