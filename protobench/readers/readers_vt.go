// vtprotobuf engine: same access walk as readers.go, but against the pruned
// protobench/pogovt package using generated UnmarshalVT (and, for the pool
// variant, vtproto's message pools with whole-graph return). The
// proto.UnmarshalOptions argument is ignored — vtproto has no lazy or
// discard-unknown modes. Keep the walks in sync with readers.go.
package readers

import (
	"google.golang.org/protobuf/proto"

	"protobench/pogovt"
)

// RegistryVT selects generated vtprotobuf unmarshal (no pooling).
var RegistryVT = map[string]Reader{
	"GET_MAP_OBJECTS": ReadGMOVT,
	"ENCOUNTER":       ReadEncounterVT,
}

// RegistryVTPool additionally sources the top-level message from vtproto's
// pool and returns the whole decoded graph to the pools after reading —
// modeling Golbat's decode → read → drop with reuse.
var RegistryVTPool = map[string]Reader{
	"GET_MAP_OBJECTS": ReadGMOVTPool,
	"ENCOUNTER":       ReadEncounterVTPool,
}

func readDisplayVT(d *pogovt.PokemonDisplayProto) int64 {
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

func readPokemonVT(p *pogovt.PokemonProto) int64 {
	if p == nil {
		return 0
	}
	acc := int64(p.GetPokemonId()) + int64(p.GetCp()) + int64(p.GetStamina()) +
		int64(p.GetMaxStamina()) + int64(p.GetMove1()) + int64(p.GetMove2()) +
		int64(p.GetIndividualAttack()) + int64(p.GetIndividualDefense()) +
		int64(p.GetIndividualStamina()) + int64(p.GetSize()) +
		int64(p.GetHeightM()*100) + int64(p.GetWeightKg()*100) +
		int64(p.GetCpMultiplier()*1000)
	return acc + readDisplayVT(p.GetPokemonDisplay())
}

func readWildVT(w *pogovt.WildPokemonProto) int64 {
	if w == nil {
		return 0
	}
	return int64(w.GetEncounterId()) + int64(len(w.GetSpawnPointId())) +
		int64(w.GetTimeTillHiddenMs()) + w.GetLastModifiedMs() +
		int64(w.GetLatitude()*1e5) + int64(w.GetLongitude()*1e5) +
		readPokemonVT(w.GetPokemon())
}

func readFortVT(f *pogovt.PokemonFortProto) int64 {
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
			int64(ri.GetRaidLevel()) + readPokemonVT(ri.GetRaidPokemon())
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

func readWeatherVT(cw *pogovt.ClientWeatherProto) int64 {
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

func readStationVT(s *pogovt.StationProto) int64 {
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

func walkGMOVT(gmo *pogovt.GetMapObjectsOutProto) {
	var acc int64
	for _, cell := range gmo.GetMapCell() {
		acc += int64(cell.GetS2CellId()) + cell.GetAsOfTimeMs()
		for _, f := range cell.GetFort() {
			acc += readFortVT(f)
		}
		for _, w := range cell.GetWildPokemon() {
			acc += readWildVT(w)
		}
		for _, n := range cell.GetNearbyPokemon() {
			acc += int64(n.GetPokedexNumber()) + int64(n.GetEncounterId()) +
				int64(len(n.GetFortId())) + readDisplayVT(n.GetPokemonDisplay())
		}
		for _, m := range cell.GetCatchablePokemon() {
			acc += int64(m.GetEncounterId()) + int64(m.GetPokedexTypeId()) +
				m.GetExpirationTimeMs() + int64(len(m.GetSpawnpointId())) +
				readDisplayVT(m.GetPokemonDisplay())
		}
		for _, s := range cell.GetStations() {
			acc += readStationVT(s)
		}
	}
	for _, cw := range gmo.GetClientWeather() {
		acc += readWeatherVT(cw)
	}
	Sink.Add(acc)
}

func ReadGMOVT(payload []byte, _ proto.UnmarshalOptions) error {
	var gmo pogovt.GetMapObjectsOutProto
	if err := gmo.UnmarshalVT(payload); err != nil {
		return err
	}
	walkGMOVT(&gmo)
	return nil
}

func ReadGMOVTPool(payload []byte, _ proto.UnmarshalOptions) error {
	gmo := pogovt.GetMapObjectsOutProtoFromVTPool()
	defer gmo.ReturnToVTPool()
	if err := gmo.UnmarshalVT(payload); err != nil {
		return err
	}
	walkGMOVT(gmo)
	return nil
}

func readEncounterVT(e *pogovt.EncounterOutProto) {
	acc := int64(e.GetStatus()) + readWildVT(e.GetPokemon())
	if cp := e.GetCaptureProbability(); cp != nil {
		for _, p := range cp.GetCaptureProbability() {
			acc += int64(p * 1000)
		}
	}
	Sink.Add(acc)
}

func ReadEncounterVT(payload []byte, _ proto.UnmarshalOptions) error {
	var e pogovt.EncounterOutProto
	if err := e.UnmarshalVT(payload); err != nil {
		return err
	}
	readEncounterVT(&e)
	return nil
}

func ReadEncounterVTPool(payload []byte, _ proto.UnmarshalOptions) error {
	e := pogovt.EncounterOutProtoFromVTPool()
	defer e.ReturnToVTPool()
	if err := e.UnmarshalVT(payload); err != nil {
		return err
	}
	readEncounterVT(e)
	return nil
}
