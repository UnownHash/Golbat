package decoder

import (
	"time"

	log "github.com/sirupsen/logrus"
)

// ApiStationBattleAvailable is one distinct active (battle_level, pokemon, form)
// option on resident stations. ReactMap derives its <id>-<form> and j<level> keys.
type ApiStationBattleAvailable struct {
	BattleLevel int8  `json:"battle_level" doc:"Max battle level"`
	PokemonId   int16 `json:"pokemon_id" doc:"Battle pokemon id, else 0"`
	Form        int16 `json:"form" doc:"Battle pokemon form id, else 0"`
	Count       int   `json:"count" doc:"Number of resident stations with this active battle option"`
}

// ApiAvailableStations is the whole-instance station filter snapshot served by
// GET /api/station/available.
type ApiAvailableStations struct {
	Battles []ApiStationBattleAvailable `json:"battles" doc:"Distinct active battle level/pokemon options on resident stations"`
}

// GetAvailableStations builds the station filter snapshot from a single
// fortLookupCache range. Mirrors isFortDnfMatch's station branch: iterate the
// StationBattles slice when present, else fall back to the top-battle
// projection; skip expired and level-0 battles.
// Unlike isFortDnfMatch, level-0 battles are excluded here (ReactMap's !battle_level convention).
// stationAvailAcc accumulates the station availability aggregate; ingest
// assumes the fort is a STATION. Shared by the per-type and combined builders.
type stationAvailAcc struct {
	battles map[ApiStationBattleAvailable]int
	forts   int
}

func newStationAvailAcc() *stationAvailAcc {
	return &stationAvailAcc{battles: map[ApiStationBattleAvailable]int{}}
}

func (a *stationAvailAcc) add(level int8, pokemonId, form int16, end, now int64) {
	if level == 0 || end <= now {
		return
	}
	a.battles[ApiStationBattleAvailable{BattleLevel: level, PokemonId: pokemonId, Form: form}]++
}

func (a *stationAvailAcc) ingest(fl *FortLookup, now int64) {
	a.forts++
	if len(fl.StationBattles) == 0 {
		a.add(fl.BattleLevel, fl.BattlePokemonId, fl.BattlePokemonForm, fl.BattleEndTimestamp, now)
		return
	}
	for _, b := range fl.StationBattles {
		a.add(b.BattleLevel, b.BattlePokemonId, b.BattlePokemonForm, b.BattleEndTimestamp, now)
	}
}

// result is a pure finalizer — no logging (see gymAvailAcc.result).
func (a *stationAvailAcc) result() *ApiAvailableStations {
	res := &ApiAvailableStations{Battles: []ApiStationBattleAvailable{}}
	for k, n := range a.battles {
		k.Count = n
		res.Battles = append(res.Battles, k)
	}
	return res
}

func GetAvailableStations(now int64) *ApiAvailableStations {
	start := time.Now()
	acc := newStationAvailAcc()
	fortLookupCache.Range(func(_ string, fl FortLookup) bool {
		if fl.FortType == STATION {
			acc.ingest(&fl, now)
		}
		return true
	})
	res := acc.result()
	if statsCollector != nil {
		statsCollector.ObserveApiScan("available-stations", time.Since(start).Seconds())
	}
	log.Infof("available-stations built in %s: scanned %d stations -> %d battle options",
		time.Since(start), acc.forts, len(res.Battles))
	return res
}
