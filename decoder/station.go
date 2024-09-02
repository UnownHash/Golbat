package decoder

import (
	"context"
	"database/sql"
	"errors"
	"golbat/db"
	"golbat/pogo"
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

	BattleLevel            null.Int `db:"battle_level"`
	BattlePokemonId        null.Int `db:"battle_pokemon_id"`
	BattlePokemonForm      null.Int `db:"battle_pokemon_form"`
	BattlePokemonCostume   null.Int `db:"battle_pokemon_costume"`
	BattlePokemonGender    null.Int `db:"battle_pokemon_gender"`
	BattlePokemonAlignment null.Int `db:"battle_pokemon_alignment"`
	Updated                int64    `db:"updated"`
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
			SELECT id, lat, lon, name, cell_id, start_time, end_time, cooldown_complete, is_battle_available, is_inactive, battle_level, battle_pokemon_id, battle_pokemon_form, battle_pokemon_costume, battle_pokemon_gender, battle_pokemon_alignment, updated			       
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
			INSERT INTO station (id, lat, lon, name, cell_id, start_time, end_time, cooldown_complete, is_battle_available, is_inactive, battle_level, battle_pokemon_id, battle_pokemon_form, battle_pokemon_costume, battle_pokemon_gender, battle_pokemon_alignment, updated)
			VALUES (:id,:lat,:lon,:name,:cell_id,:start_time,:end_time,:cooldown_complete,:is_battle_available,:is_inactive,:battle_level,:battle_pokemon_id,:battle_pokemon_form,:battle_pokemon_costume,:battle_pokemon_gender,:battle_pokemon_alignment,:updated)
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
			    battle_level = :battle_level,
			    battle_pokemon_id = :battle_pokemon_id,
			    battle_pokemon_form = :battle_pokemon_form,
			    battle_pokemon_costume = :battle_pokemon_costume,
			    battle_pokemon_gender = :battle_pokemon_gender,
			    battle_pokemon_alignment = :battle_pokemon_alignment,
			    updated = :updated
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
		old.CooldownComplete != new.CooldownComplete ||
		old.IsBattleAvailable != new.IsBattleAvailable ||
		old.BattleLevel != new.BattleLevel ||
		old.BattlePokemonId != new.BattlePokemonId ||
		old.BattlePokemonForm != new.BattlePokemonForm ||
		old.BattlePokemonCostume != new.BattlePokemonCostume ||
		old.BattlePokemonGender != new.BattlePokemonGender ||
		old.BattlePokemonAlignment != new.BattlePokemonAlignment ||
		!floatAlmostEqual(old.Lat, new.Lat, floatTolerance) ||
		!floatAlmostEqual(old.Lon, new.Lon, floatTolerance)
}

func (station *Station) updateFromStationProto(stationProto *pogo.StationProto, cellId uint64) *Station {
	station.Id = stationProto.Id
	station.Name = stationProto.Name
	station.Lat = stationProto.Lat
	station.Lon = stationProto.Lng
	station.StartTime = stationProto.StartTimeMs / 1000
	station.EndTime = stationProto.EndTimeMs / 1000
	station.CooldownComplete = stationProto.CooldownCompleteMs
	station.IsBattleAvailable = stationProto.IsBreadBattleAvailable
	if battleDetails := stationProto.BattleDetails; battleDetails != nil {
		station.BattleLevel = null.IntFrom(int64(battleDetails.BattleLevel))
		if pokemon := battleDetails.BattlePokemon; pokemon != nil {
			station.BattlePokemonId = null.IntFrom(int64(pokemon.PokemonId))
			station.BattlePokemonForm = null.IntFrom(int64(pokemon.PokemonDisplay.Form))
			station.BattlePokemonCostume = null.IntFrom(int64(pokemon.PokemonDisplay.Costume))
			station.BattlePokemonGender = null.IntFrom(int64(pokemon.PokemonDisplay.Gender))
			station.BattlePokemonAlignment = null.IntFrom(int64(pokemon.PokemonDisplay.Alignment))
		}
	}
	station.CellId = int64(cellId)
	return station
}

func createStationWebhooks(oldStation *Station, station *Station) {
	//TODO we need to define webhooks, are they needed for stations, or only for battles?
}
