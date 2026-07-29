// hypershim engine: hyperpb parsing accessed through the generated typed
// shims (hypershim package) instead of hand-rolled protoreflect calls. This
// is the ergonomic path a Golbat migration would take — the walk below reads
// almost identically to readers.go — and exists to measure the shim layer's
// overhead against readers_hyperpb.go's hand-rolled walk.
package readers

import (
	"buf.build/go/hyperpb"
	"google.golang.org/protobuf/proto"

	"protobench/hypershim"
)

// RegistryHypershim selects hyperpb parsing behind generated typed shims.
var RegistryHypershim = map[string]Reader{
	"GET_MAP_OBJECTS": ReadGMOShim,
	"ENCOUNTER":       ReadEncounterShim,
}

func shimDisplay(d hypershim.PokemonDisplayProto) int64 {
	if d.IsZero() {
		return 0
	}
	acc := int64(d.GetForm()) + int64(d.GetCostume()) + int64(d.GetGender()) +
		int64(d.GetWeatherBoostedCondition()) + int64(d.GetAlignment())
	if d.GetShiny() {
		acc++
	}
	return acc
}

func shimPokemon(p hypershim.PokemonProto) int64 {
	if p.IsZero() {
		return 0
	}
	acc := int64(p.GetPokemonId()) + int64(p.GetCp()) + int64(p.GetStamina()) +
		int64(p.GetMaxStamina()) + int64(p.GetMove1()) + int64(p.GetMove2()) +
		int64(p.GetIndividualAttack()) + int64(p.GetIndividualDefense()) +
		int64(p.GetIndividualStamina()) + int64(p.GetSize()) +
		int64(p.GetHeightM()*100) + int64(p.GetWeightKg()*100) +
		int64(p.GetCpMultiplier()*1000)
	return acc + shimDisplay(p.GetPokemonDisplay())
}

func shimWild(w hypershim.WildPokemonProto) int64 {
	if w.IsZero() {
		return 0
	}
	return int64(w.GetEncounterId()) + int64(len(w.GetSpawnPointId())) +
		int64(w.GetTimeTillHiddenMs()) + w.GetLastModifiedMs() +
		int64(w.GetLatitude()*1e5) + int64(w.GetLongitude()*1e5) +
		shimPokemon(w.GetPokemon())
}

func shimFort(f hypershim.PokemonFortProto) int64 {
	if f.IsZero() {
		return 0
	}
	acc := int64(len(f.GetFortId())) + int64(f.GetLatitude()*1e5) +
		int64(f.GetLongitude()*1e5) + f.GetLastModifiedMs() +
		int64(f.GetTeam()) + int64(f.GetGuardPokemonId()) +
		int64(f.GetFortType()) + f.GetCooldownCompleteMs() +
		int64(f.GetPowerUpProgressPoints()) + f.GetPowerUpLevelExpirationMs() +
		int64(f.GetActiveFortModifier().Len()) + int64(len(f.GetPartnerId()))
	if f.GetEnabled() {
		acc++
	}
	if ri := f.GetRaidInfo(); !ri.IsZero() {
		acc += ri.GetRaidSpawnMs() + ri.GetRaidBattleMs() + ri.GetRaidEndMs() +
			int64(ri.GetRaidLevel()) + shimPokemon(ri.GetRaidPokemon())
	}
	if gd := f.GetGymDisplay(); !gd.IsZero() {
		acc += int64(gd.GetTotalGymCp()) + int64(gd.GetSlotsAvailable()) + gd.GetOccupiedMillis()
	}
	for pd := range f.GetPokestopDisplays().All() {
		acc += int64(len(pd.GetIncidentId())) + pd.GetIncidentStartMs() +
			pd.GetIncidentExpirationMs() + int64(pd.GetIncidentDisplayType())
	}
	if pd := f.GetPokestopDisplay(); !pd.IsZero() {
		acc += int64(len(pd.GetIncidentId()))
	}
	return acc
}

func shimWeather(cw hypershim.ClientWeatherProto) int64 {
	if cw.IsZero() {
		return 0
	}
	acc := cw.GetS2CellId()
	if gw := cw.GetGameplayWeather(); !gw.IsZero() {
		acc += int64(gw.GetGameplayCondition())
	}
	if dw := cw.GetDisplayWeather(); !dw.IsZero() {
		acc += int64(dw.GetCloudLevel()) + int64(dw.GetRainLevel()) +
			int64(dw.GetWindLevel()) + int64(dw.GetSnowLevel()) +
			int64(dw.GetFogLevel()) + int64(dw.GetWindDirection())
	}
	return acc + int64(cw.GetAlerts().Len())
}

func shimStation(s hypershim.StationProto) int64 {
	if s.IsZero() {
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

func ReadGMOShim(payload []byte, _ proto.UnmarshalOptions) error {
	compileHyperpb()
	shared := sharedPool.Get().(*hyperpb.Shared)
	defer func() {
		shared.Free()
		sharedPool.Put(shared)
	}()
	msg := shared.NewMessage(gmoType.Load())
	if err := msg.Unmarshal(payload); err != nil {
		return err
	}
	gmo := hypershim.AsGetMapObjectsOutProto(msg.ProtoReflect())
	var acc int64
	for cell := range gmo.GetMapCell().All() {
		acc += int64(cell.GetS2CellId()) + cell.GetAsOfTimeMs()
		for f := range cell.GetFort().All() {
			acc += shimFort(f)
		}
		for w := range cell.GetWildPokemon().All() {
			acc += shimWild(w)
		}
		for n := range cell.GetNearbyPokemon().All() {
			acc += int64(n.GetPokedexNumber()) + int64(n.GetEncounterId()) +
				int64(len(n.GetFortId())) + shimDisplay(n.GetPokemonDisplay())
		}
		for m := range cell.GetCatchablePokemon().All() {
			acc += int64(m.GetEncounterId()) + int64(m.GetPokedexTypeId()) +
				m.GetExpirationTimeMs() + int64(len(m.GetSpawnpointId())) +
				shimDisplay(m.GetPokemonDisplay())
		}
		for s := range cell.GetStations().All() {
			acc += shimStation(s)
		}
	}
	for cw := range gmo.GetClientWeather().All() {
		acc += shimWeather(cw)
	}
	Sink.Add(acc)
	return nil
}

func ReadEncounterShim(payload []byte, _ proto.UnmarshalOptions) error {
	compileHyperpb()
	shared := sharedPool.Get().(*hyperpb.Shared)
	defer func() {
		shared.Free()
		sharedPool.Put(shared)
	}()
	msg := shared.NewMessage(encType.Load())
	if err := msg.Unmarshal(payload); err != nil {
		return err
	}
	e := hypershim.AsEncounterOutProto(msg.ProtoReflect())
	acc := int64(e.GetStatus())
	acc += shimWild(e.GetPokemon())
	if cp := e.GetCaptureProbability(); !cp.IsZero() {
		probs := cp.GetCaptureProbability()
		for i := 0; i < probs.Len(); i++ {
			acc += int64(float32(probs.At(i).Float()) * 1000)
		}
	}
	Sink.Add(acc)
	return nil
}
