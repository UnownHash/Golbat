package decoder

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"golbat/db"
	"golbat/pogo"

	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
	"gopkg.in/guregu/null.v4"
)

// Tappable struct.
// REMINDER! Dirty flag pattern - use setter methods to modify fields
type Tappable struct {
	mu sync.Mutex `db:"-" json:"-"` // Object-level mutex

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

	dirty         bool     `db:"-" json:"-"` // Not persisted - tracks if object needs saving
	newRecord     bool     `db:"-" json:"-"` // Not persisted - tracks if this is a new record
	changedFields []string `db:"-" json:"-"` // Track which fields changed (only when dbDebugEnabled)
}

// IsDirty returns true if any field has been modified
func (ta *Tappable) IsDirty() bool {
	return ta.dirty
}

// ClearDirty resets the dirty flag (call after saving to DB)
func (ta *Tappable) ClearDirty() {
	ta.dirty = false
}

// IsNewRecord returns true if this is a new record (not yet in DB)
func (ta *Tappable) IsNewRecord() bool {
	return ta.newRecord
}

// Lock acquires the Tappable's mutex
func (ta *Tappable) Lock() {
	ta.mu.Lock()
}

// Unlock releases the Tappable's mutex
func (ta *Tappable) Unlock() {
	ta.mu.Unlock()
}

// --- Set methods with dirty tracking ---

func (ta *Tappable) SetLat(v float64) {
	if !floatAlmostEqual(ta.Lat, v, floatTolerance) {
		ta.Lat = v
		ta.dirty = true
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, "Lat")
		}
	}
}

func (ta *Tappable) SetLon(v float64) {
	if !floatAlmostEqual(ta.Lon, v, floatTolerance) {
		ta.Lon = v
		ta.dirty = true
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, "Lon")
		}
	}
}

func (ta *Tappable) SetFortId(v null.String) {
	if ta.FortId != v {
		ta.FortId = v
		ta.dirty = true
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, "FortId")
		}
	}
}

func (ta *Tappable) SetSpawnId(v null.Int) {
	if ta.SpawnId != v {
		ta.SpawnId = v
		ta.dirty = true
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, "SpawnId")
		}
	}
}

func (ta *Tappable) SetType(v string) {
	if ta.Type != v {
		ta.Type = v
		ta.dirty = true
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, "Type")
		}
	}
}

func (ta *Tappable) SetEncounter(v null.Int) {
	if ta.Encounter != v {
		ta.Encounter = v
		ta.dirty = true
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, "Encounter")
		}
	}
}

func (ta *Tappable) SetItemId(v null.Int) {
	if ta.ItemId != v {
		ta.ItemId = v
		ta.dirty = true
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, "ItemId")
		}
	}
}

func (ta *Tappable) SetCount(v null.Int) {
	if ta.Count != v {
		ta.Count = v
		ta.dirty = true
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, "Count")
		}
	}
}

func (ta *Tappable) SetExpireTimestamp(v null.Int) {
	if ta.ExpireTimestamp != v {
		ta.ExpireTimestamp = v
		ta.dirty = true
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, "ExpireTimestamp")
		}
	}
}

func (ta *Tappable) SetExpireTimestampVerified(v bool) {
	if ta.ExpireTimestampVerified != v {
		ta.ExpireTimestampVerified = v
		ta.dirty = true
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, "ExpireTimestampVerified")
		}
	}
}

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
		spawnPoint, _ := getSpawnpointRecord(ctx, db, spawnId)
		if spawnPoint != nil && spawnPoint.DespawnSec.Valid {
			despawnSecond := int(spawnPoint.DespawnSec.ValueOrZero())

			date := time.Unix(timestampMs/1000, 0)
			secondOfHour := date.Second() + date.Minute()*60

			despawnOffset := despawnSecond - secondOfHour
			if despawnOffset < 0 {
				despawnOffset += 3600
			}
			ta.SetExpireTimestamp(null.IntFrom(int64(timestampMs)/1000 + int64(despawnOffset)))
			ta.SetExpireTimestampVerified(true)
		} else {
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

func loadTappableFromDatabase(ctx context.Context, db db.DbDetails, id uint64, tappable *Tappable) error {
	err := db.GeneralDb.GetContext(ctx, tappable,
		`SELECT id, lat, lon, fort_id, spawn_id, type, pokemon_id, item_id, count, expire_timestamp, expire_timestamp_verified, updated
         FROM tappable WHERE id = ?`, strconv.FormatUint(id, 10))
	statsCollector.IncDbQuery("select tappable", err)
	return err
}

// peekTappableRecord - cache-only lookup, no DB fallback, returns locked.
// Caller MUST call returned unlock function if non-nil.
func peekTappableRecord(id uint64) (*Tappable, func(), error) {
	if item := tappableCache.Get(id); item != nil {
		tappable := item.Value()
		tappable.Lock()
		return tappable, func() { tappable.Unlock() }, nil
	}
	return nil, nil, nil
}

// getTappableRecordReadOnly acquires lock but does NOT take snapshot.
// Use for read-only checks. Will cause a backing database lookup.
// Caller MUST call returned unlock function if non-nil.
func getTappableRecordReadOnly(ctx context.Context, db db.DbDetails, id uint64) (*Tappable, func(), error) {
	// Check cache first
	if item := tappableCache.Get(id); item != nil {
		tappable := item.Value()
		tappable.Lock()
		return tappable, func() { tappable.Unlock() }, nil
	}

	dbTappable := Tappable{}
	err := loadTappableFromDatabase(ctx, db, id, &dbTappable)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	dbTappable.ClearDirty()

	// Atomically cache the loaded Tappable - if another goroutine raced us,
	// we'll get their Tappable and use that instead (ensuring same mutex)
	existingTappable, _ := tappableCache.GetOrSetFunc(id, func() *Tappable {
		return &dbTappable
	})

	tappable := existingTappable.Value()
	tappable.Lock()
	return tappable, func() { tappable.Unlock() }, nil
}

// getOrCreateTappableRecord gets existing or creates new, locked.
// Caller MUST call returned unlock function.
func getOrCreateTappableRecord(ctx context.Context, db db.DbDetails, id uint64) (*Tappable, func(), error) {
	// Create new Tappable atomically - function only called if key doesn't exist
	tappableItem, _ := tappableCache.GetOrSetFunc(id, func() *Tappable {
		return &Tappable{Id: id, newRecord: true}
	})

	tappable := tappableItem.Value()
	tappable.Lock()

	if tappable.newRecord {
		// We should attempt to load from database
		err := loadTappableFromDatabase(ctx, db, id, tappable)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				tappable.Unlock()
				return nil, nil, err
			}
		} else {
			// We loaded from DB
			tappable.newRecord = false
			tappable.ClearDirty()
		}
	}

	return tappable, func() { tappable.Unlock() }, nil
}

// GetTappableRecord is an exported function for API use.
// Returns a tappable if found, nil if not found.
func GetTappableRecord(ctx context.Context, db db.DbDetails, id uint64) (*Tappable, error) {
	tappable, unlock, err := getTappableRecordReadOnly(ctx, db, id)
	if err != nil {
		return nil, err
	}
	if tappable == nil {
		return nil, nil
	}
	defer unlock()
	return tappable, nil
}

func saveTappableRecord(ctx context.Context, details db.DbDetails, tappable *Tappable) {
	// Skip save if not dirty and not new
	if !tappable.IsDirty() && !tappable.IsNewRecord() {
		return
	}

	now := time.Now().Unix()
	tappable.Updated = now

	if tappable.IsNewRecord() {
		if dbDebugEnabled {
			dbDebugLog("INSERT", "Tappable", strconv.FormatUint(tappable.Id, 10), tappable.changedFields)
		}
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
		if dbDebugEnabled {
			dbDebugLog("UPDATE", "Tappable", strconv.FormatUint(tappable.Id, 10), tappable.changedFields)
		}
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
	if dbDebugEnabled {
		tappable.changedFields = tappable.changedFields[:0]
	}
	tappable.ClearDirty()
	if tappable.IsNewRecord() {
		tappableCache.Set(tappable.Id, tappable, ttlcache.DefaultTTL)
		tappable.newRecord = false
	}
}

func UpdateTappable(ctx context.Context, db db.DbDetails, request *pogo.ProcessTappableProto, tappableDetails *pogo.ProcessTappableOutProto, timestampMs int64) string {
	id := request.GetEncounterId()

	tappable, unlock, err := getOrCreateTappableRecord(ctx, db, id)
	if err != nil {
		log.Printf("getOrCreateTappableRecord: %s", err)
		return "Error getting tappable"
	}
	defer unlock()

	tappable.updateFromProcessTappableProto(ctx, db, tappableDetails, request, timestampMs)
	saveTappableRecord(ctx, db, tappable)
	return fmt.Sprintf("ProcessTappableOutProto %d", id)
}
