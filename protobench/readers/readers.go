// Package readers simulates Golbat's per-method field accesses: decode a raw
// payload, read the same subtrees Golbat's decode path reads, drop the
// message. Every read folds into Sink so the compiler cannot eliminate the
// accesses. Access sets mirror decoder/gmo_decode.go and friends; keep them
// in sync when Golbat starts reading new fields.
package readers

import (
	"sync/atomic"

	"google.golang.org/protobuf/proto"

	"protobench/pogo"
)

var Sink atomic.Int64

type Reader func(payload []byte, o proto.UnmarshalOptions) error

var Registry = map[string]Reader{
	"GET_MAP_OBJECTS": ReadGMO,
	"ENCOUNTER":       ReadEncounter,
}

func readDisplay(d *pogo.PokemonDisplayProto) int64 {
	if d == nil {
		return 0
	}
	acc := int64(d.GetForm()) + int64(d.GetCostume()) + int64(d.GetGender()) +
		int64(d.GetWeatherBoostedCondition()) + int64(d.GetAlignment())
	if d.GetShiny() {
		acc++
	}
	return acc
}

func readPokemon(p *pogo.PokemonProto) int64 {
	if p == nil {
		return 0
	}
	acc := int64(p.GetPokemonId()) + int64(p.GetCp()) + int64(p.GetStamina()) +
		int64(p.GetMaxStamina()) + int64(p.GetMove1()) + int64(p.GetMove2()) +
		int64(p.GetIndividualAttack()) + int64(p.GetIndividualDefense()) +
		int64(p.GetIndividualStamina()) + int64(p.GetSize()) +
		int64(p.GetHeightM()*100) + int64(p.GetWeightKg()*100) +
		int64(p.GetCpMultiplier()*1000)
	return acc + readDisplay(p.GetPokemonDisplay())
}

func readWild(w *pogo.WildPokemonProto) int64 {
	if w == nil {
		return 0
	}
	return int64(w.GetEncounterId()) + int64(len(w.GetSpawnPointId())) +
		int64(w.GetTimeTillHiddenMs()) + w.GetLastModifiedMs() +
		int64(w.GetLatitude()*1e5) + int64(w.GetLongitude()*1e5) +
		readPokemon(w.GetPokemon())
}

func readFort(f *pogo.PokemonFortProto) int64 {
	if f == nil {
		return 0
	}
	acc := int64(len(f.GetFortId())) + int64(f.GetLatitude()*1e5) +
		int64(f.GetLongitude()*1e5) + f.GetLastModifiedMs() +
		int64(f.GetTeam()) + int64(f.GetGuardPokemonId()) +
		int64(f.GetFortType()) + f.GetCooldownCompleteMs() +
		int64(f.GetPowerUpProgressPoints()) + f.GetPowerUpLevelExpirationMs() +
		int64(len(f.GetActiveFortModifier())) + int64(len(f.GetPartnerId()))
	if f.GetEnabled() {
		acc++
	}
	if ri := f.GetRaidInfo(); ri != nil {
		acc += ri.GetRaidSpawnMs() + ri.GetRaidBattleMs() + ri.GetRaidEndMs() +
			int64(ri.GetRaidLevel()) + readPokemon(ri.GetRaidPokemon())
	}
	if gd := f.GetGymDisplay(); gd != nil {
		acc += int64(gd.GetTotalGymCp()) + int64(gd.GetSlotsAvailable()) + gd.GetOccupiedMillis()
	}
	for _, pd := range f.GetPokestopDisplays() {
		acc += int64(len(pd.GetIncidentId())) + pd.GetIncidentStartMs() +
			pd.GetIncidentExpirationMs() + int64(pd.GetIncidentDisplayType())
	}
	if pd := f.GetPokestopDisplay(); pd != nil {
		acc += int64(len(pd.GetIncidentId()))
	}
	return acc
}

func readWeather(cw *pogo.ClientWeatherProto) int64 {
	if cw == nil {
		return 0
	}
	acc := cw.GetS2CellId()
	if gw := cw.GetGameplayWeather(); gw != nil {
		acc += int64(gw.GetGameplayCondition())
	}
	if dw := cw.GetDisplayWeather(); dw != nil {
		acc += int64(dw.GetCloudLevel()) + int64(dw.GetRainLevel()) +
			int64(dw.GetWindLevel()) + int64(dw.GetSnowLevel()) +
			int64(dw.GetFogLevel()) + int64(dw.GetWindDirection())
	}
	return acc + int64(len(cw.GetAlerts()))
}

func readStation(s *pogo.StationProto) int64 {
	if s == nil {
		return 0
	}
	acc := int64(len(s.GetId())) + int64(len(s.GetName())) +
		int64(s.GetLat()*1e5) + int64(s.GetLng()*1e5) +
		s.GetStartTimeMs() + s.GetEndTimeMs() + s.GetCooldownCompleteMs()
	if s.GetIsBreadBattleAvailable() {
		acc++
	}
	return acc
}

func ReadGMO(payload []byte, o proto.UnmarshalOptions) error {
	var gmo pogo.GetMapObjectsOutProto
	if err := o.Unmarshal(payload, &gmo); err != nil {
		return err
	}
	var acc int64
	for _, cell := range gmo.GetMapCell() {
		acc += int64(cell.GetS2CellId()) + cell.GetAsOfTimeMs()
		for _, f := range cell.GetFort() {
			acc += readFort(f)
		}
		for _, w := range cell.GetWildPokemon() {
			acc += readWild(w)
		}
		for _, n := range cell.GetNearbyPokemon() {
			acc += int64(n.GetPokedexNumber()) + int64(n.GetEncounterId()) +
				int64(len(n.GetFortId())) + readDisplay(n.GetPokemonDisplay())
		}
		for _, m := range cell.GetCatchablePokemon() {
			acc += int64(m.GetEncounterId()) + int64(m.GetPokedexTypeId()) +
				m.GetExpirationTimeMs() + int64(len(m.GetSpawnpointId())) +
				readDisplay(m.GetPokemonDisplay())
		}
		for _, s := range cell.GetStations() {
			acc += readStation(s)
		}
	}
	for _, cw := range gmo.GetClientWeather() {
		acc += readWeather(cw)
	}
	Sink.Add(acc)
	return nil
}

func ReadEncounter(payload []byte, o proto.UnmarshalOptions) error {
	var e pogo.EncounterOutProto
	if err := o.Unmarshal(payload, &e); err != nil {
		return err
	}
	acc := int64(e.GetStatus()) + readWild(e.GetPokemon())
	if cp := e.GetCaptureProbability(); cp != nil {
		for _, p := range cp.GetCaptureProbability() {
			acc += int64(p * 1000)
		}
	}
	Sink.Add(acc)
	return nil
}
