package decoder

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/guregu/null/v6"
	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"

	"golbat/db"
	"golbat/webhooks"
)

// stationSelectColumns defines the columns for station queries.
// Used by both single-row and bulk load queries to keep them in sync.
const stationSelectColumns = `id, lat, lon, name, cell_id, start_time, end_time, cooldown_complete,
	is_battle_available, is_inactive, updated, battle_level, battle_start, battle_end,
	battle_pokemon_id, battle_pokemon_form, battle_pokemon_costume, battle_pokemon_gender,
	battle_pokemon_alignment, battle_pokemon_bread_mode, battle_pokemon_move_1, battle_pokemon_move_2,
	battle_pokemon_stamina, battle_pokemon_cp_multiplier, total_stationed_pokemon, total_stationed_gmax,
	stationed_pokemon`

type StationWebhook struct {
	Id                     string   `json:"id"`
	Latitude               float64  `json:"latitude"`
	Longitude              float64  `json:"longitude"`
	Name                   string   `json:"name"`
	StartTime              int64    `json:"start_time"`
	EndTime                int64    `json:"end_time"`
	IsBattleAvailable      bool     `json:"is_battle_available"`
	BattleLevel            null.Int `json:"battle_level"`
	BattleStart            null.Int `json:"battle_start"`
	BattleEnd              null.Int `json:"battle_end"`
	BattlePokemonId        null.Int `json:"battle_pokemon_id"`
	BattlePokemonForm      null.Int `json:"battle_pokemon_form"`
	BattlePokemonCostume   null.Int `json:"battle_pokemon_costume"`
	BattlePokemonGender    null.Int `json:"battle_pokemon_gender"`
	BattlePokemonAlignment null.Int `json:"battle_pokemon_alignment"`
	BattlePokemonBreadMode null.Int `json:"battle_pokemon_bread_mode"`
	BattlePokemonMove1     null.Int `json:"battle_pokemon_move_1"`
	BattlePokemonMove2     null.Int `json:"battle_pokemon_move_2"`
	TotalStationedPokemon  null.Int `json:"total_stationed_pokemon"`
	TotalStationedGmax     null.Int `json:"total_stationed_gmax"`
	Updated                int64    `json:"updated"`
}

func loadStationFromDatabase(ctx context.Context, db db.DbDetails, stationId string, station *Station) error {
	err := db.GeneralDb.GetContext(ctx, station,
		`SELECT `+stationSelectColumns+` FROM station WHERE id = ?`, stationId)
	statsCollector.IncDbQuery("select station", err)
	return err
}

// peekStationRecord - cache-only lookup, no DB fallback, returns locked.
// Caller MUST call returned unlock function if non-nil.
func peekStationRecord(stationId string) (*Station, func(), error) {
	if item := stationCache.Get(stationId); item != nil {
		station := item.Value()
		station.Lock()
		return station, func() { station.Unlock() }, nil
	}
	return nil, nil, nil
}

// getStationRecordReadOnly acquires lock but does NOT take snapshot.
// Use for read-only checks. Will cause a backing database lookup.
// Caller MUST call returned unlock function if non-nil.
func getStationRecordReadOnly(ctx context.Context, db db.DbDetails, stationId string) (*Station, func(), error) {
	// Check cache first
	if item := stationCache.Get(stationId); item != nil {
		station := item.Value()
		station.Lock()
		return station, func() { station.Unlock() }, nil
	}

	dbStation := Station{}
	err := loadStationFromDatabase(ctx, db, stationId, &dbStation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	dbStation.ClearDirty()

	// Atomically cache the loaded Station - if another goroutine raced us,
	// we'll get their Station and use that instead (ensuring same mutex)
	existingStation, _ := stationCache.GetOrSetFunc(stationId, func() *Station {
		return &dbStation
	})

	station := existingStation.Value()
	station.Lock()
	return station, func() { station.Unlock() }, nil
}

// getStationRecordForUpdate acquires lock AND takes snapshot for webhook comparison.
// Caller MUST call returned unlock function if non-nil.
func getStationRecordForUpdate(ctx context.Context, db db.DbDetails, stationId string) (*Station, func(), error) {
	station, unlock, err := getStationRecordReadOnly(ctx, db, stationId)
	if err != nil || station == nil {
		return nil, nil, err
	}
	station.snapshotOldValues()
	return station, unlock, nil
}

// getOrCreateStationRecord gets existing or creates new, locked with snapshot.
// Caller MUST call returned unlock function.
func getOrCreateStationRecord(ctx context.Context, db db.DbDetails, stationId string) (*Station, func(), error) {
	// Create new Station atomically - function only called if key doesn't exist
	stationItem, _ := stationCache.GetOrSetFunc(stationId, func() *Station {
		return &Station{Id: stationId, newRecord: true}
	})

	station := stationItem.Value()
	station.Lock()

	if station.newRecord {
		// We should attempt to load from database
		err := loadStationFromDatabase(ctx, db, stationId, station)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				station.Unlock()
				return nil, nil, err
			}
		} else {
			// We loaded from DB
			station.newRecord = false
			station.ClearDirty()
		}
	}

	station.snapshotOldValues()
	return station, func() { station.Unlock() }, nil
}

func saveStationRecord(ctx context.Context, db db.DbDetails, station *Station) {
	now := time.Now().Unix()

	// Skip save if not dirty and was updated recently (15-min debounce)
	if !station.IsDirty() && !station.IsNewRecord() {
		if station.Updated > now-GetUpdateThreshold(900) {
			return
		}
	}

	station.SetUpdated(now)

	// Capture isNewRecord before state changes
	isNewRecord := station.IsNewRecord()

	// Debug logging before queueing
	if dbDebugEnabled {
		if isNewRecord {
			dbDebugLog("INSERT", "Station", station.Id, station.changedFields)
		} else {
			dbDebugLog("UPDATE", "Station", station.Id, station.changedFields)
		}
	}

	// Queue the write through the write-behind system
	if writeBehindQueue != nil {
		writeBehindQueue.Enqueue(station, isNewRecord, 0)
	} else {
		// Fallback to direct write if queue not initialized
		_ = stationWriteDB(db, station, isNewRecord)
	}

	if dbDebugEnabled {
		station.changedFields = station.changedFields[:0]
	}
	station.ClearDirty()
	createStationWebhooks(station)
	if isNewRecord {
		stationCache.Set(station.Id, station, ttlcache.DefaultTTL)
		station.newRecord = false
	}
}

// stationWriteDB performs the actual database INSERT/UPDATE for a Station
// This is called by both direct writes and the write-behind queue
func stationWriteDB(db db.DbDetails, station *Station, isNewRecord bool) error {
	ctx := context.Background()

	if isNewRecord {
		res, err := db.GeneralDb.NamedExecContext(ctx,
			`
			INSERT INTO station (id, lat, lon, name, cell_id, start_time, end_time, cooldown_complete, is_battle_available, is_inactive, updated, battle_level, battle_start, battle_end, battle_pokemon_id, battle_pokemon_form, battle_pokemon_costume, battle_pokemon_gender, battle_pokemon_alignment, battle_pokemon_bread_mode, battle_pokemon_move_1, battle_pokemon_move_2, battle_pokemon_stamina, battle_pokemon_cp_multiplier, total_stationed_pokemon, total_stationed_gmax, stationed_pokemon)
			VALUES (:id,:lat,:lon,:name,:cell_id,:start_time,:end_time,:cooldown_complete,:is_battle_available,:is_inactive,:updated,:battle_level,:battle_start,:battle_end,:battle_pokemon_id,:battle_pokemon_form,:battle_pokemon_costume,:battle_pokemon_gender,:battle_pokemon_alignment,:battle_pokemon_bread_mode,:battle_pokemon_move_1,:battle_pokemon_move_2,:battle_pokemon_stamina,:battle_pokemon_cp_multiplier,:total_stationed_pokemon,:total_stationed_gmax,:stationed_pokemon)
			`, station)

		statsCollector.IncDbQuery("insert station", err)
		if err != nil {
			log.Errorf("insert station: %s", err)
			return err
		}
		_, _ = res, err
	} else {
		res, err := db.GeneralDb.NamedExecContext(ctx, `
			UPDATE station
			SET
			    lat = :lat,
			    lon = :lon,
			    name = :name,
			    cell_id = :cell_id,
			    start_time = :start_time,
			    end_time = :end_time,
			    cooldown_complete = :cooldown_complete,
			    is_battle_available = :is_battle_available,
			    is_inactive = :is_inactive,
			    updated = :updated,
			    battle_level = :battle_level,
			    battle_start = :battle_start,
			    battle_end = :battle_end,
			    battle_pokemon_id = :battle_pokemon_id,
			    battle_pokemon_form = :battle_pokemon_form,
			    battle_pokemon_costume = :battle_pokemon_costume,
			    battle_pokemon_gender = :battle_pokemon_gender,
			    battle_pokemon_alignment = :battle_pokemon_alignment,
			    battle_pokemon_bread_mode = :battle_pokemon_bread_mode,
			    battle_pokemon_move_1 = :battle_pokemon_move_1,
			    battle_pokemon_move_2 = :battle_pokemon_move_2,
			    battle_pokemon_stamina = :battle_pokemon_stamina,
			    battle_pokemon_cp_multiplier = :battle_pokemon_cp_multiplier,
			    total_stationed_pokemon = :total_stationed_pokemon,
			    total_stationed_gmax = :total_stationed_gmax,
			    stationed_pokemon = :stationed_pokemon
			WHERE id = :id
		`, station,
		)
		statsCollector.IncDbQuery("update station", err)
		if err != nil {
			log.Errorf("Update station %s", err)
			return err
		}
		_, _ = res, err
	}
	return nil
}

func createStationWebhooks(station *Station) {
	old := &station.oldValues
	isNew := station.IsNewRecord()

	if isNew || station.BattlePokemonId.Valid && (old.EndTime != station.EndTime ||
		old.BattleEnd != station.BattleEnd ||
		old.BattlePokemonId != station.BattlePokemonId ||
		old.BattlePokemonForm != station.BattlePokemonForm ||
		old.BattlePokemonCostume != station.BattlePokemonCostume ||
		old.BattlePokemonGender != station.BattlePokemonGender ||
		old.BattlePokemonBreadMode != station.BattlePokemonBreadMode) {
		stationHook := StationWebhook{
			Id:                     station.Id,
			Latitude:               station.Lat,
			Longitude:              station.Lon,
			Name:                   station.Name,
			StartTime:              station.StartTime,
			EndTime:                station.EndTime,
			IsBattleAvailable:      station.IsBattleAvailable,
			BattleLevel:            station.BattleLevel,
			BattleStart:            station.BattleStart,
			BattleEnd:              station.BattleEnd,
			BattlePokemonId:        station.BattlePokemonId,
			BattlePokemonForm:      station.BattlePokemonForm,
			BattlePokemonCostume:   station.BattlePokemonCostume,
			BattlePokemonGender:    station.BattlePokemonGender,
			BattlePokemonAlignment: station.BattlePokemonAlignment,
			BattlePokemonBreadMode: station.BattlePokemonBreadMode,
			BattlePokemonMove1:     station.BattlePokemonMove1,
			BattlePokemonMove2:     station.BattlePokemonMove2,
			TotalStationedPokemon:  station.TotalStationedPokemon,
			TotalStationedGmax:     station.TotalStationedGmax,
			Updated:                station.Updated,
		}
		areas := MatchStatsGeofence(station.Lat, station.Lon)
		webhooksSender.AddMessage(webhooks.MaxBattle, stationHook, areas)
		statsCollector.UpdateMaxBattleCount(areas, station.BattleLevel.ValueOrZero())
	}
}
