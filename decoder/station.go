package decoder

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"golbat/db"
	"golbat/pogo"
	"golbat/util"
	"time"

	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
	"gopkg.in/guregu/null.v4"
)

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
}

func getStationRecord(ctx context.Context, db db.DbDetails, stationId string) (*Station, error) {
	inMemoryStation := stationCache.Get(stationId)
	if inMemoryStation != nil {
		station := inMemoryStation.Value()
		return &station, nil
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
	return &station, nil
}

func saveStationRecord(ctx context.Context, db db.DbDetails, station *Station) {
	oldStation, _ := getStationRecord(ctx, db, station.Id)
	now := time.Now().Unix()
	if oldStation != nil && !hasChangesStation(oldStation, station) {
		if oldStation.Updated > now-900 {
			// if a gym is unchanged, but we did see it again after 15 minutes, then save again
			return
		}
	}

	station.Updated = now

	//log.Traceln(cmp.Diff(oldStation, station))
	if oldStation == nil {
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

	stationCache.Set(station.Id, *station, ttlcache.DefaultTTL)
	createStationWebhooks(oldStation, station)

}

// hasChangesStation compares two Station structs
// Float tolerance: Lat, Lon
func hasChangesStation(old *Station, new *Station) bool {
	return old.Id != new.Id ||
		old.Name != new.Name ||
		old.StartTime != new.StartTime ||
		old.EndTime != new.EndTime ||
		old.StationedPokemon != new.StationedPokemon ||
		old.CooldownComplete != new.CooldownComplete ||
		old.IsBattleAvailable != new.IsBattleAvailable ||
		old.BattleLevel != new.BattleLevel ||
		old.BattleStart != new.BattleStart ||
		old.BattleEnd != new.BattleEnd ||
		old.BattlePokemonId != new.BattlePokemonId ||
		old.BattlePokemonForm != new.BattlePokemonForm ||
		old.BattlePokemonCostume != new.BattlePokemonCostume ||
		old.BattlePokemonGender != new.BattlePokemonGender ||
		old.BattlePokemonAlignment != new.BattlePokemonAlignment ||
		old.BattlePokemonBreadMode != new.BattlePokemonBreadMode ||
		old.BattlePokemonMove1 != new.BattlePokemonMove1 ||
		old.BattlePokemonMove2 != new.BattlePokemonMove2 ||
		old.BattlePokemonStamina != new.BattlePokemonStamina ||
		old.BattlePokemonCpMultiplier != new.BattlePokemonCpMultiplier ||
		!floatAlmostEqual(old.Lat, new.Lat, floatTolerance) ||
		!floatAlmostEqual(old.Lon, new.Lon, floatTolerance)
}

func (station *Station) updateFromStationProto(stationProto *pogo.StationProto, cellId uint64) *Station {
	station.Id = stationProto.Id
	station.Name = stationProto.Name
	// NOTE: Some names have more than 255 runes, which won't fit in our
	// varchar(255).
	if truncateStr, truncated := util.TruncateUTF8(stationProto.Name, 255); truncated {
		log.Warnf("truncating name for station id '%s'. Orig name: %s",
			stationProto.Id,
			stationProto.Name,
		)
		station.Name = truncateStr
	}
	station.Lat = stationProto.Lat
	station.Lon = stationProto.Lng
	station.StartTime = stationProto.StartTimeMs / 1000
	station.EndTime = stationProto.EndTimeMs / 1000
	station.CooldownComplete = stationProto.CooldownCompleteMs
	station.IsBattleAvailable = stationProto.IsBreadBattleAvailable
	if battleDetails := stationProto.BattleDetails; battleDetails != nil {
		station.BattleLevel = null.IntFrom(int64(battleDetails.BattleLevel))
		station.BattleStart = null.IntFrom(battleDetails.BattleWindowStartMs / 1000)
		station.BattleEnd = null.IntFrom(battleDetails.BattleWindowEndMs / 1000)
		if pokemon := battleDetails.BattlePokemon; pokemon != nil {
			station.BattlePokemonId = null.IntFrom(int64(pokemon.PokemonId))
			station.BattlePokemonMove1 = null.IntFrom(int64(pokemon.Move1))
			station.BattlePokemonMove2 = null.IntFrom(int64(pokemon.Move2))
			station.BattlePokemonForm = null.IntFrom(int64(pokemon.PokemonDisplay.Form))
			station.BattlePokemonCostume = null.IntFrom(int64(pokemon.PokemonDisplay.Costume))
			station.BattlePokemonGender = null.IntFrom(int64(pokemon.PokemonDisplay.Gender))
			station.BattlePokemonAlignment = null.IntFrom(int64(pokemon.PokemonDisplay.Alignment))
			station.BattlePokemonBreadMode = null.IntFrom(int64(pokemon.PokemonDisplay.BreadModeEnum))
			station.BattlePokemonStamina = null.IntFrom(int64(pokemon.Stamina))
			station.BattlePokemonCpMultiplier = null.FloatFrom(float64(pokemon.CpMultiplier))
			if rewardPokemon := battleDetails.RewardPokemon; rewardPokemon != nil && pokemon.PokemonId != rewardPokemon.PokemonId {
				log.Infof("[DYNAMAX] Pokemon reward differs from battle: Battle %v - Reward %v", pokemon, rewardPokemon)
			}
		}
	}
	station.CellId = int64(cellId)
	return station
}

func (station *Station) updateFromGetStationedPokemonDetailsOutProto(stationProto *pogo.GetStationedPokemonDetailsOutProto) *Station {
	type stationedPokemonDetail struct {
		PokemonId int `json:"pokemon_id"`
		Form      int `json:"form"`
		Costume   int `json:"costume"`
		Gender    int `json:"gender"`
		BreadMode int `json:"bread_mode"`
	}

	var stationedPokemon []stationedPokemonDetail
	stationedGmax := int64(0)
	for _, stationedPokemonDetails := range stationProto.StationedPokemons {
		pokemon := stationedPokemonDetails.Pokemon
		stationedPokemon = append(stationedPokemon, stationedPokemonDetail{
			PokemonId: int(pokemon.PokemonId),
			Form:      int(pokemon.PokemonDisplay.Form),
			Costume:   int(pokemon.PokemonDisplay.Costume),
			Gender:    int(pokemon.PokemonDisplay.Gender),
			BreadMode: int(pokemon.PokemonDisplay.BreadModeEnum),
		})
		if pokemon.PokemonDisplay.BreadModeEnum == pogo.BreadModeEnum_BREAD_DOUGH_MODE || pokemon.PokemonDisplay.BreadModeEnum == pogo.BreadModeEnum_BREAD_DOUGH_MODE_2 {
			stationedGmax++
		}
	}
	jsonString, _ := json.Marshal(stationedPokemon)
	station.StationedPokemon = null.StringFrom(string(jsonString))
	station.TotalStationedPokemon = null.IntFrom(int64(stationProto.TotalNumStationedPokemon))
	station.TotalStationedGmax = null.IntFrom(stationedGmax)
	return station
}

func (station *Station) resetStationedPokemonFromStationDetailsNotFound() *Station {
	jsonString, _ := json.Marshal([]string{})
	station.StationedPokemon = null.StringFrom(string(jsonString))
	station.TotalStationedPokemon = null.IntFrom(0)
	station.TotalStationedGmax = null.IntFrom(0)
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

func createStationWebhooks(oldStation *Station, station *Station) {
	//TODO we need to define webhooks, are they needed for stations, or only for battles?
}
