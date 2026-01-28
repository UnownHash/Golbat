package decoder

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"golbat/db"
	"golbat/pogo"
	"golbat/util"
	"golbat/webhooks"

	log "github.com/sirupsen/logrus"
	"gopkg.in/guregu/null.v4"
)

// Station struct.
// REMINDER! Dirty flag pattern - use setter methods to modify fields
type Station struct {
	Id                string  `db:"id"`
	Lat               float64 `db:"lat"`
	Lon               float64 `db:"lon"`
	Name              string  `db:"name"`
	CellId            int64   `db:"cell_id"`
	StartTime         int64   `db:"start_time"`
	EndTime           int64   `db:"end_time"`
	CooldownComplete  int64   `db:"cooldown_complete"`
	IsBattleAvailable bool    `db:"is_battle_available"`
	IsInactive        bool    `db:"is_inactive"`
	Updated           int64   `db:"updated"`

	BattleLevel               null.Int   `db:"battle_level"`
	BattleStart               null.Int   `db:"battle_start"`
	BattleEnd                 null.Int   `db:"battle_end"`
	BattlePokemonId           null.Int   `db:"battle_pokemon_id"`
	BattlePokemonForm         null.Int   `db:"battle_pokemon_form"`
	BattlePokemonCostume      null.Int   `db:"battle_pokemon_costume"`
	BattlePokemonGender       null.Int   `db:"battle_pokemon_gender"`
	BattlePokemonAlignment    null.Int   `db:"battle_pokemon_alignment"`
	BattlePokemonBreadMode    null.Int   `db:"battle_pokemon_bread_mode"`
	BattlePokemonMove1        null.Int   `db:"battle_pokemon_move_1"`
	BattlePokemonMove2        null.Int   `db:"battle_pokemon_move_2"`
	BattlePokemonStamina      null.Int   `db:"battle_pokemon_stamina"`
	BattlePokemonCpMultiplier null.Float `db:"battle_pokemon_cp_multiplier"`

	TotalStationedPokemon null.Int    `db:"total_stationed_pokemon"`
	TotalStationedGmax    null.Int    `db:"total_stationed_gmax"`
	StationedPokemon      null.String `db:"stationed_pokemon"`

	dirty     bool `db:"-" json:"-"` // Not persisted - tracks if object needs saving
	newRecord bool `db:"-" json:"-"` // Not persisted - tracks if this is a new record

	oldValues StationOldValues `db:"-" json:"-"` // Old values for webhook comparison
}

// StationOldValues holds old field values for webhook comparison
type StationOldValues struct {
	EndTime                int64
	BattleEnd              null.Int
	BattlePokemonId        null.Int
	BattlePokemonForm      null.Int
	BattlePokemonCostume   null.Int
	BattlePokemonGender    null.Int
	BattlePokemonBreadMode null.Int
}

// IsDirty returns true if any field has been modified
func (station *Station) IsDirty() bool {
	return station.dirty
}

// ClearDirty resets the dirty flag (call after saving to DB)
func (station *Station) ClearDirty() {
	station.dirty = false
}

// IsNewRecord returns true if this is a new record (not yet in DB)
func (station *Station) IsNewRecord() bool {
	return station.newRecord
}

// snapshotOldValues saves current values for webhook comparison
// Call this after loading from cache/DB but before modifications
func (station *Station) snapshotOldValues() {
	station.oldValues = StationOldValues{
		EndTime:                station.EndTime,
		BattleEnd:              station.BattleEnd,
		BattlePokemonId:        station.BattlePokemonId,
		BattlePokemonForm:      station.BattlePokemonForm,
		BattlePokemonCostume:   station.BattlePokemonCostume,
		BattlePokemonGender:    station.BattlePokemonGender,
		BattlePokemonBreadMode: station.BattlePokemonBreadMode,
	}
}

// --- Set methods with dirty tracking ---

func (station *Station) SetId(v string) {
	if station.Id != v {
		station.Id = v
		station.dirty = true
	}
}

func (station *Station) SetLat(v float64) {
	if !floatAlmostEqual(station.Lat, v, floatTolerance) {
		station.Lat = v
		station.dirty = true
	}
}

func (station *Station) SetLon(v float64) {
	if !floatAlmostEqual(station.Lon, v, floatTolerance) {
		station.Lon = v
		station.dirty = true
	}
}

func (station *Station) SetName(v string) {
	if station.Name != v {
		station.Name = v
		station.dirty = true
	}
}

func (station *Station) SetCellId(v int64) {
	if station.CellId != v {
		station.CellId = v
		station.dirty = true
	}
}

func (station *Station) SetStartTime(v int64) {
	if station.StartTime != v {
		station.StartTime = v
		station.dirty = true
	}
}

func (station *Station) SetEndTime(v int64) {
	if station.EndTime != v {
		station.EndTime = v
		station.dirty = true
	}
}

func (station *Station) SetCooldownComplete(v int64) {
	if station.CooldownComplete != v {
		station.CooldownComplete = v
		station.dirty = true
	}
}

func (station *Station) SetIsBattleAvailable(v bool) {
	if station.IsBattleAvailable != v {
		station.IsBattleAvailable = v
		station.dirty = true
	}
}

func (station *Station) SetIsInactive(v bool) {
	if station.IsInactive != v {
		station.IsInactive = v
		station.dirty = true
	}
}

func (station *Station) SetBattleLevel(v null.Int) {
	if station.BattleLevel != v {
		station.BattleLevel = v
		station.dirty = true
	}
}

func (station *Station) SetBattleStart(v null.Int) {
	if station.BattleStart != v {
		station.BattleStart = v
		station.dirty = true
	}
}

func (station *Station) SetBattleEnd(v null.Int) {
	if station.BattleEnd != v {
		station.BattleEnd = v
		station.dirty = true
	}
}

func (station *Station) SetBattlePokemonId(v null.Int) {
	if station.BattlePokemonId != v {
		station.BattlePokemonId = v
		station.dirty = true
	}
}

func (station *Station) SetBattlePokemonForm(v null.Int) {
	if station.BattlePokemonForm != v {
		station.BattlePokemonForm = v
		station.dirty = true
	}
}

func (station *Station) SetBattlePokemonCostume(v null.Int) {
	if station.BattlePokemonCostume != v {
		station.BattlePokemonCostume = v
		station.dirty = true
	}
}

func (station *Station) SetBattlePokemonGender(v null.Int) {
	if station.BattlePokemonGender != v {
		station.BattlePokemonGender = v
		station.dirty = true
	}
}

func (station *Station) SetBattlePokemonAlignment(v null.Int) {
	if station.BattlePokemonAlignment != v {
		station.BattlePokemonAlignment = v
		station.dirty = true
	}
}

func (station *Station) SetBattlePokemonBreadMode(v null.Int) {
	if station.BattlePokemonBreadMode != v {
		station.BattlePokemonBreadMode = v
		station.dirty = true
	}
}

func (station *Station) SetBattlePokemonMove1(v null.Int) {
	if station.BattlePokemonMove1 != v {
		station.BattlePokemonMove1 = v
		station.dirty = true
	}
}

func (station *Station) SetBattlePokemonMove2(v null.Int) {
	if station.BattlePokemonMove2 != v {
		station.BattlePokemonMove2 = v
		station.dirty = true
	}
}

func (station *Station) SetBattlePokemonStamina(v null.Int) {
	if station.BattlePokemonStamina != v {
		station.BattlePokemonStamina = v
		station.dirty = true
	}
}

func (station *Station) SetBattlePokemonCpMultiplier(v null.Float) {
	if !nullFloatAlmostEqual(station.BattlePokemonCpMultiplier, v, floatTolerance) {
		station.BattlePokemonCpMultiplier = v
		station.dirty = true
	}
}

func (station *Station) SetTotalStationedPokemon(v null.Int) {
	if station.TotalStationedPokemon != v {
		station.TotalStationedPokemon = v
		station.dirty = true
	}
}

func (station *Station) SetTotalStationedGmax(v null.Int) {
	if station.TotalStationedGmax != v {
		station.TotalStationedGmax = v
		station.dirty = true
	}
}

func (station *Station) SetStationedPokemon(v null.String) {
	if station.StationedPokemon != v {
		station.StationedPokemon = v
		station.dirty = true
	}
}

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

func getStationRecord(ctx context.Context, db db.DbDetails, stationId string) (*Station, error) {
	inMemoryStation := stationCache.Get(stationId)
	if inMemoryStation != nil {
		station := inMemoryStation.Value()
		station.snapshotOldValues()
		return station, nil
	}
	station := Station{}
	err := db.GeneralDb.GetContext(ctx, &station,
		`
			SELECT id, lat, lon, name, cell_id, start_time, end_time, cooldown_complete, is_battle_available, is_inactive, updated, battle_level, battle_start, battle_end, battle_pokemon_id, battle_pokemon_form, battle_pokemon_costume, battle_pokemon_gender, battle_pokemon_alignment, battle_pokemon_bread_mode, battle_pokemon_move_1, battle_pokemon_move_2, battle_pokemon_stamina, battle_pokemon_cp_multiplier, total_stationed_pokemon, total_stationed_gmax, stationed_pokemon
			FROM station WHERE id = ?
		`, stationId)
	statsCollector.IncDbQuery("select station", err)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}
	station.snapshotOldValues()
	return &station, nil
}

func saveStationRecord(ctx context.Context, db db.DbDetails, station *Station) {
	now := time.Now().Unix()

	// Skip save if not dirty and was updated recently (15-min debounce)
	if !station.IsDirty() && !station.IsNewRecord() {
		if station.Updated > now-900 {
			return
		}
	}

	station.Updated = now

	if station.IsNewRecord() {
		res, err := db.GeneralDb.NamedExecContext(ctx,
			`
			INSERT INTO station (id, lat, lon, name, cell_id, start_time, end_time, cooldown_complete, is_battle_available, is_inactive, updated, battle_level, battle_start, battle_end, battle_pokemon_id, battle_pokemon_form, battle_pokemon_costume, battle_pokemon_gender, battle_pokemon_alignment, battle_pokemon_bread_mode, battle_pokemon_move_1, battle_pokemon_move_2, battle_pokemon_stamina, battle_pokemon_cp_multiplier, total_stationed_pokemon, total_stationed_gmax, stationed_pokemon)
			VALUES (:id,:lat,:lon,:name,:cell_id,:start_time,:end_time,:cooldown_complete,:is_battle_available,:is_inactive,:updated,:battle_level,:battle_start,:battle_end,:battle_pokemon_id,:battle_pokemon_form,:battle_pokemon_costume,:battle_pokemon_gender,:battle_pokemon_alignment,:battle_pokemon_bread_mode,:battle_pokemon_move_1,:battle_pokemon_move_2,:battle_pokemon_stamina,:battle_pokemon_cp_multiplier,:total_stationed_pokemon,:total_stationed_gmax,:stationed_pokemon)
			`, station)

		statsCollector.IncDbQuery("insert station", err)
		if err != nil {
			log.Errorf("insert station: %s", err)
			return
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
		}
		_, _ = res, err
	}

	station.ClearDirty()
	station.newRecord = false
	//stationCache.Set(station.Id, station, ttlcache.DefaultTTL)
	createStationWebhooks(station)
}

func (station *Station) updateFromStationProto(stationProto *pogo.StationProto, cellId uint64) *Station {
	station.SetId(stationProto.Id)
	name := stationProto.Name
	// NOTE: Some names have more than 255 runes, which won't fit in our
	// varchar(255).
	if truncateStr, truncated := util.TruncateUTF8(stationProto.Name, 255); truncated {
		log.Warnf("truncating name for station id '%s'. Orig name: %s",
			stationProto.Id,
			stationProto.Name,
		)
		name = truncateStr
	}
	station.SetName(name)
	station.SetLat(stationProto.Lat)
	station.SetLon(stationProto.Lng)
	station.SetStartTime(stationProto.StartTimeMs / 1000)
	station.SetEndTime(stationProto.EndTimeMs / 1000)
	station.SetCooldownComplete(stationProto.CooldownCompleteMs)
	station.SetIsBattleAvailable(stationProto.IsBreadBattleAvailable)
	if battleDetails := stationProto.BattleDetails; battleDetails != nil {
		station.SetBattleLevel(null.IntFrom(int64(battleDetails.BattleLevel)))
		station.SetBattleStart(null.IntFrom(battleDetails.BattleWindowStartMs / 1000))
		station.SetBattleEnd(null.IntFrom(battleDetails.BattleWindowEndMs / 1000))
		if pokemon := battleDetails.BattlePokemon; pokemon != nil {
			station.SetBattlePokemonId(null.IntFrom(int64(pokemon.PokemonId)))
			station.SetBattlePokemonMove1(null.IntFrom(int64(pokemon.Move1)))
			station.SetBattlePokemonMove2(null.IntFrom(int64(pokemon.Move2)))
			station.SetBattlePokemonForm(null.IntFrom(int64(pokemon.PokemonDisplay.Form)))
			station.SetBattlePokemonCostume(null.IntFrom(int64(pokemon.PokemonDisplay.Costume)))
			station.SetBattlePokemonGender(null.IntFrom(int64(pokemon.PokemonDisplay.Gender)))
			station.SetBattlePokemonAlignment(null.IntFrom(int64(pokemon.PokemonDisplay.Alignment)))
			station.SetBattlePokemonBreadMode(null.IntFrom(int64(pokemon.PokemonDisplay.BreadModeEnum)))
			station.SetBattlePokemonStamina(null.IntFrom(int64(pokemon.Stamina)))
			station.SetBattlePokemonCpMultiplier(null.FloatFrom(float64(pokemon.CpMultiplier)))
			if rewardPokemon := battleDetails.RewardPokemon; rewardPokemon != nil && pokemon.PokemonId != rewardPokemon.PokemonId {
				log.Infof("[DYNAMAX] Pokemon reward differs from battle: Battle %v - Reward %v", pokemon, rewardPokemon)
			}
		}
	}
	station.SetCellId(int64(cellId))
	return station
}

func (station *Station) updateFromGetStationedPokemonDetailsOutProto(stationProto *pogo.GetStationedPokemonDetailsOutProto) *Station {
	type stationedPokemonDetail struct {
		PokemonId             int    `json:"pokemon_id"`
		Form                  int    `json:"form"`
		Costume               int    `json:"costume"`
		Gender                int    `json:"gender"`
		Shiny                 bool   `json:"shiny,omitempty"`
		TempEvolution         int    `json:"temp_evolution,omitempty"`
		TempEvolutionFinishMs int64  `json:"temp_evolution_finish_ms,omitempty"`
		Alignment             int    `json:"alignment,omitempty"`
		Badge                 int    `json:"badge,omitempty"`
		Background            *int64 `json:"background,omitempty"`
		BreadMode             int    `json:"bread_mode"`
	}

	var stationedPokemon []stationedPokemonDetail
	stationedGmax := int64(0)
	for _, stationedPokemonDetails := range stationProto.StationedPokemons {
		pokemon := stationedPokemonDetails.Pokemon
		display := pokemon.PokemonDisplay
		stationedPokemon = append(stationedPokemon, stationedPokemonDetail{
			PokemonId:             int(pokemon.PokemonId),
			Form:                  int(display.Form),
			Costume:               int(display.Costume),
			Gender:                int(display.Gender),
			Shiny:                 display.Shiny,
			TempEvolution:         int(display.CurrentTempEvolution),
			TempEvolutionFinishMs: display.TemporaryEvolutionFinishMs,
			Alignment:             int(display.Alignment),
			Badge:                 int(display.PokemonBadge),
			Background:            util.ExtractBackgroundFromDisplay(display),
			BreadMode:             int(display.BreadModeEnum),
		})
		if display.BreadModeEnum == pogo.BreadModeEnum_BREAD_DOUGH_MODE || display.BreadModeEnum == pogo.BreadModeEnum_BREAD_DOUGH_MODE_2 {
			stationedGmax++
		}
	}
	jsonString, _ := json.Marshal(stationedPokemon)
	station.SetStationedPokemon(null.StringFrom(string(jsonString)))
	station.SetTotalStationedPokemon(null.IntFrom(int64(stationProto.TotalNumStationedPokemon)))
	station.SetTotalStationedGmax(null.IntFrom(stationedGmax))
	return station
}

func (station *Station) resetStationedPokemonFromStationDetailsNotFound() *Station {
	jsonString, _ := json.Marshal([]string{})
	station.SetStationedPokemon(null.StringFrom(string(jsonString)))
	station.SetTotalStationedPokemon(null.IntFrom(0))
	station.SetTotalStationedGmax(null.IntFrom(0))
	return station
}

func ResetStationedPokemonWithStationDetailsNotFound(ctx context.Context, db db.DbDetails, request *pogo.GetStationedPokemonDetailsProto) string {
	stationId := request.StationId
	stationMutex, _ := stationStripedMutex.GetLock(stationId)
	stationMutex.Lock()
	defer stationMutex.Unlock()

	station, err := getStationRecord(ctx, db, stationId)
	if err != nil {
		log.Printf("Get station %s", err)
		return "Error getting station"
	}

	if station == nil {
		log.Infof("Stationed pokemon details for station %s not found", stationId)
		return fmt.Sprintf("Stationed pokemon details for station %s not found", stationId)
	}

	station.resetStationedPokemonFromStationDetailsNotFound()
	saveStationRecord(ctx, db, station)
	return fmt.Sprintf("StationedPokemonDetails %s", stationId)
}

func UpdateStationWithStationDetails(ctx context.Context, db db.DbDetails, request *pogo.GetStationedPokemonDetailsProto, stationDetails *pogo.GetStationedPokemonDetailsOutProto) string {
	stationId := request.StationId
	stationMutex, _ := stationStripedMutex.GetLock(stationId)
	stationMutex.Lock()
	defer stationMutex.Unlock()

	station, err := getStationRecord(ctx, db, stationId)
	if err != nil {
		log.Printf("Get station %s", err)
		return "Error getting station"
	}

	if station == nil {
		log.Infof("Stationed pokemon details for station %s not found", stationId)
		return fmt.Sprintf("Stationed pokemon details for station %s not found", stationId)
	}

	station.updateFromGetStationedPokemonDetailsOutProto(stationDetails)
	saveStationRecord(ctx, db, station)
	return fmt.Sprintf("StationedPokemonDetails %s", stationId)
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
