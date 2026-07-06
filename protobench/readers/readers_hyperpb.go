// hyperpb engine: parse with buf.build/go/hyperpb (compiled parser tables,
// arena allocation via hyperpb.Shared reuse, optional profile-guided
// recompilation) and read the same access set as readers.go through
// protoreflect with pre-resolved FieldDescriptors. Descriptors come from the
// pruned pogovt package so schema compile cost covers 216 messages, not the
// full vbase.
package readers

import (
	"sync"
	"sync/atomic"

	"buf.build/go/hyperpb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"protobench/pogovt"
)

// RegistryHyperpb selects the hyperpb engine.
var RegistryHyperpb = map[string]Reader{
	"GET_MAP_OBJECTS": ReadGMOHyper,
	"ENCOUNTER":       ReadEncounterHyper,
}

func fd(md protoreflect.MessageDescriptor, name string) protoreflect.FieldDescriptor {
	f := md.Fields().ByName(protoreflect.Name(name))
	if f == nil {
		panic("hyperpb reader: missing field " + name + " on " + string(md.FullName()))
	}
	return f
}

var (
	gmoMD = (*pogovt.GetMapObjectsOutProto)(nil).ProtoReflect().Descriptor()
	encMD = (*pogovt.EncounterOutProto)(nil).ProtoReflect().Descriptor()

	cellMD    = (*pogovt.ClientMapCellProto)(nil).ProtoReflect().Descriptor()
	fortMD    = (*pogovt.PokemonFortProto)(nil).ProtoReflect().Descriptor()
	wildMD    = (*pogovt.WildPokemonProto)(nil).ProtoReflect().Descriptor()
	nearbyMD  = (*pogovt.NearbyPokemonProto)(nil).ProtoReflect().Descriptor()
	mapPokeMD = (*pogovt.MapPokemonProto)(nil).ProtoReflect().Descriptor()
	pokeMD    = (*pogovt.PokemonProto)(nil).ProtoReflect().Descriptor()
	displayMD = (*pogovt.PokemonDisplayProto)(nil).ProtoReflect().Descriptor()
	weatherMD = (*pogovt.ClientWeatherProto)(nil).ProtoReflect().Descriptor()
	gwMD      = (*pogovt.GameplayWeatherProto)(nil).ProtoReflect().Descriptor()
	dwMD      = (*pogovt.DisplayWeatherProto)(nil).ProtoReflect().Descriptor()
	stationMD = (*pogovt.StationProto)(nil).ProtoReflect().Descriptor()
	raidMD    = (*pogovt.RaidInfoProto)(nil).ProtoReflect().Descriptor()
	gymDispMD = (*pogovt.GymDisplayProto)(nil).ProtoReflect().Descriptor()
	incDispMD = (*pogovt.PokestopIncidentDisplayProto)(nil).ProtoReflect().Descriptor()
	capMD     = (*pogovt.CaptureProbabilityProto)(nil).ProtoReflect().Descriptor()

	fdGmoMapCell       = fd(gmoMD, "map_cell")
	fdGmoClientWeather = fd(gmoMD, "client_weather")

	fdCellS2        = fd(cellMD, "s2_cell_id")
	fdCellAsOf      = fd(cellMD, "as_of_time_ms")
	fdCellFort      = fd(cellMD, "fort")
	fdCellWild      = fd(cellMD, "wild_pokemon")
	fdCellNearby    = fd(cellMD, "nearby_pokemon")
	fdCellCatchable = fd(cellMD, "catchable_pokemon")
	fdCellStations  = fd(cellMD, "stations")

	fdFortId        = fd(fortMD, "fort_id")
	fdFortLastMod   = fd(fortMD, "last_modified_ms")
	fdFortLat       = fd(fortMD, "latitude")
	fdFortLon       = fd(fortMD, "longitude")
	fdFortTeam      = fd(fortMD, "team")
	fdFortGuard     = fd(fortMD, "guard_pokemon_id")
	fdFortEnabled   = fd(fortMD, "enabled")
	fdFortType      = fd(fortMD, "fort_type")
	fdFortModifier  = fd(fortMD, "active_fort_modifier")
	fdFortCooldown  = fd(fortMD, "cooldown_complete_ms")
	fdFortRaidInfo  = fd(fortMD, "raid_info")
	fdFortGymDisp   = fd(fortMD, "gym_display")
	fdFortPartnerId = fd(fortMD, "partner_id")
	fdFortIncDisp   = fd(fortMD, "pokestop_display")
	fdFortIncDisps  = fd(fortMD, "pokestop_displays")
	fdFortPowerPts  = fd(fortMD, "power_up_progress_points")
	fdFortPowerExp  = fd(fortMD, "power_up_level_expiration_ms")

	fdWildEnc     = fd(wildMD, "encounter_id")
	fdWildLastMod = fd(wildMD, "last_modified_ms")
	fdWildLat     = fd(wildMD, "latitude")
	fdWildLon     = fd(wildMD, "longitude")
	fdWildSpawn   = fd(wildMD, "spawn_point_id")
	fdWildPokemon = fd(wildMD, "pokemon")
	fdWildTTH     = fd(wildMD, "time_till_hidden_ms")

	fdNearbyDex     = fd(nearbyMD, "pokedex_number")
	fdNearbyEnc     = fd(nearbyMD, "encounter_id")
	fdNearbyFortId  = fd(nearbyMD, "fort_id")
	fdNearbyDisplay = fd(nearbyMD, "pokemon_display")

	fdMapSpawn   = fd(mapPokeMD, "spawnpoint_id")
	fdMapEnc     = fd(mapPokeMD, "encounter_id")
	fdMapDex     = fd(mapPokeMD, "pokedex_type_id")
	fdMapExpiry  = fd(mapPokeMD, "expiration_time_ms")
	fdMapDisplay = fd(mapPokeMD, "pokemon_display")

	fdPokeId      = fd(pokeMD, "pokemon_id")
	fdPokeCp      = fd(pokeMD, "cp")
	fdPokeStamina = fd(pokeMD, "stamina")
	fdPokeMaxStam = fd(pokeMD, "max_stamina")
	fdPokeMove1   = fd(pokeMD, "move1")
	fdPokeMove2   = fd(pokeMD, "move2")
	fdPokeHeight  = fd(pokeMD, "height_m")
	fdPokeWeight  = fd(pokeMD, "weight_kg")
	fdPokeIVA     = fd(pokeMD, "individual_attack")
	fdPokeIVD     = fd(pokeMD, "individual_defense")
	fdPokeIVS     = fd(pokeMD, "individual_stamina")
	fdPokeCpMult  = fd(pokeMD, "cp_multiplier")
	fdPokeDisplay = fd(pokeMD, "pokemon_display")
	fdPokeSize    = fd(pokeMD, "size")

	fdDispCostume = fd(displayMD, "costume")
	fdDispGender  = fd(displayMD, "gender")
	fdDispShiny   = fd(displayMD, "shiny")
	fdDispForm    = fd(displayMD, "form")
	fdDispWeather = fd(displayMD, "weather_boosted_condition")
	fdDispAlign   = fd(displayMD, "alignment")

	fdWthS2      = fd(weatherMD, "s2_cell_id")
	fdWthDisplay = fd(weatherMD, "display_weather")
	fdWthGame    = fd(weatherMD, "gameplay_weather")
	fdWthAlerts  = fd(weatherMD, "alerts")

	fdGwCondition = fd(gwMD, "gameplay_condition")

	fdDwCloud   = fd(dwMD, "cloud_level")
	fdDwRain    = fd(dwMD, "rain_level")
	fdDwWind    = fd(dwMD, "wind_level")
	fdDwSnow    = fd(dwMD, "snow_level")
	fdDwFog     = fd(dwMD, "fog_level")
	fdDwWindDir = fd(dwMD, "wind_direction")

	fdStId       = fd(stationMD, "id")
	fdStName     = fd(stationMD, "name")
	fdStLat      = fd(stationMD, "lat")
	fdStLng      = fd(stationMD, "lng")
	fdStStart    = fd(stationMD, "start_time_ms")
	fdStEnd      = fd(stationMD, "end_time_ms")
	fdStCooldown = fd(stationMD, "cooldown_complete_ms")
	fdStBread    = fd(stationMD, "is_bread_battle_available")

	fdRaidSpawn   = fd(raidMD, "raid_spawn_ms")
	fdRaidBattle  = fd(raidMD, "raid_battle_ms")
	fdRaidEnd     = fd(raidMD, "raid_end_ms")
	fdRaidLevel   = fd(raidMD, "raid_level")
	fdRaidPokemon = fd(raidMD, "raid_pokemon")

	fdGymCp       = fd(gymDispMD, "total_gym_cp")
	fdGymSlots    = fd(gymDispMD, "slots_available")
	fdGymOccupied = fd(gymDispMD, "occupied_millis")

	fdIncId     = fd(incDispMD, "incident_id")
	fdIncStart  = fd(incDispMD, "incident_start_ms")
	fdIncExpiry = fd(incDispMD, "incident_expiration_ms")
	fdIncType   = fd(incDispMD, "incident_display_type")

	fdEncPokemon = fd(encMD, "pokemon")
	fdEncStatus  = fd(encMD, "status")
	fdEncCapture = fd(encMD, "capture_probability")

	fdCapProbs = fd(capMD, "capture_probability")
)

// Compiled hyperpb types; swapped by InitHyperpbPGO after profile-guided
// recompilation.
var (
	gmoType atomic.Pointer[hyperpb.MessageType]
	encType atomic.Pointer[hyperpb.MessageType]

	compileOnce sync.Once
)

func compileHyperpb() {
	compileOnce.Do(func() {
		gmoType.Store(hyperpb.CompileMessageDescriptor(gmoMD))
		encType.Store(hyperpb.CompileMessageDescriptor(encMD))
	})
}

// InitHyperpbPGO records a parsing profile over sample payloads and swaps in
// recompiled, profile-optimized types. Call before the timed window.
func InitHyperpbPGO(samples map[string][][]byte) {
	compileHyperpb()
	recompile := func(tp *atomic.Pointer[hyperpb.MessageType], payloads [][]byte) {
		ty := tp.Load()
		if len(payloads) == 0 {
			return
		}
		profile := ty.NewProfile()
		shared := new(hyperpb.Shared)
		for _, p := range payloads {
			msg := shared.NewMessage(ty)
			_ = msg.Unmarshal(p, hyperpb.WithRecordProfile(profile, 1.0))
			shared.Free()
		}
		tp.Store(ty.Recompile(profile))
	}
	recompile(&gmoType, samples["GET_MAP_OBJECTS"])
	recompile(&encType, samples["ENCOUNTER"])
}

var sharedPool = sync.Pool{New: func() any { return new(hyperpb.Shared) }}

func hyperDisplay(m protoreflect.Message) int64 {
	acc := m.Get(fdDispForm).Enum() + m.Get(fdDispCostume).Enum() +
		m.Get(fdDispGender).Enum() + m.Get(fdDispWeather).Enum() + m.Get(fdDispAlign).Enum()
	if m.Get(fdDispShiny).Bool() {
		return int64(acc) + 1
	}
	return int64(acc)
}

func hyperPokemon(m protoreflect.Message) int64 {
	acc := int64(m.Get(fdPokeId).Enum()) + m.Get(fdPokeCp).Int() + m.Get(fdPokeStamina).Int() +
		m.Get(fdPokeMaxStam).Int() + int64(m.Get(fdPokeMove1).Enum()) + int64(m.Get(fdPokeMove2).Enum()) +
		m.Get(fdPokeIVA).Int() + m.Get(fdPokeIVD).Int() + m.Get(fdPokeIVS).Int() +
		int64(m.Get(fdPokeSize).Enum()) +
		// float32 fields: reproduce the generated getters' float32 arithmetic
		// exactly, or int64 truncation diverges (0.42*100 is 42.0 in float32
		// but 41.999998... in float64).
		int64(float32(m.Get(fdPokeHeight).Float())*100) + int64(float32(m.Get(fdPokeWeight).Float())*100) +
		int64(float32(m.Get(fdPokeCpMult).Float())*1000)
	if m.Has(fdPokeDisplay) {
		acc += hyperDisplay(m.Get(fdPokeDisplay).Message())
	}
	return acc
}

func hyperWild(m protoreflect.Message) int64 {
	acc := int64(m.Get(fdWildEnc).Uint()) + int64(len(m.Get(fdWildSpawn).String())) +
		m.Get(fdWildTTH).Int() + m.Get(fdWildLastMod).Int() +
		int64(m.Get(fdWildLat).Float()*1e5) + int64(m.Get(fdWildLon).Float()*1e5)
	if m.Has(fdWildPokemon) {
		acc += hyperPokemon(m.Get(fdWildPokemon).Message())
	}
	return acc
}

func hyperFort(m protoreflect.Message) int64 {
	acc := int64(len(m.Get(fdFortId).String())) + int64(m.Get(fdFortLat).Float()*1e5) +
		int64(m.Get(fdFortLon).Float()*1e5) + m.Get(fdFortLastMod).Int() +
		int64(m.Get(fdFortTeam).Enum()) + int64(m.Get(fdFortGuard).Enum()) +
		int64(m.Get(fdFortType).Enum()) + m.Get(fdFortCooldown).Int() +
		m.Get(fdFortPowerPts).Int() + m.Get(fdFortPowerExp).Int() +
		int64(m.Get(fdFortModifier).List().Len()) + int64(len(m.Get(fdFortPartnerId).String()))
	if m.Get(fdFortEnabled).Bool() {
		acc++
	}
	if m.Has(fdFortRaidInfo) {
		ri := m.Get(fdFortRaidInfo).Message()
		acc += ri.Get(fdRaidSpawn).Int() + ri.Get(fdRaidBattle).Int() + ri.Get(fdRaidEnd).Int() +
			int64(ri.Get(fdRaidLevel).Enum())
		if ri.Has(fdRaidPokemon) {
			acc += hyperPokemon(ri.Get(fdRaidPokemon).Message())
		}
	}
	if m.Has(fdFortGymDisp) {
		gd := m.Get(fdFortGymDisp).Message()
		acc += gd.Get(fdGymCp).Int() + gd.Get(fdGymSlots).Int() + gd.Get(fdGymOccupied).Int()
	}
	incs := m.Get(fdFortIncDisps).List()
	for i := 0; i < incs.Len(); i++ {
		pd := incs.Get(i).Message()
		acc += int64(len(pd.Get(fdIncId).String())) + pd.Get(fdIncStart).Int() +
			pd.Get(fdIncExpiry).Int() + int64(pd.Get(fdIncType).Enum())
	}
	if m.Has(fdFortIncDisp) {
		acc += int64(len(m.Get(fdFortIncDisp).Message().Get(fdIncId).String()))
	}
	return acc
}

func hyperWeather(m protoreflect.Message) int64 {
	acc := m.Get(fdWthS2).Int()
	if m.Has(fdWthGame) {
		acc += int64(m.Get(fdWthGame).Message().Get(fdGwCondition).Enum())
	}
	if m.Has(fdWthDisplay) {
		dw := m.Get(fdWthDisplay).Message()
		acc += int64(dw.Get(fdDwCloud).Enum()) + int64(dw.Get(fdDwRain).Enum()) +
			int64(dw.Get(fdDwWind).Enum()) + int64(dw.Get(fdDwSnow).Enum()) +
			int64(dw.Get(fdDwFog).Enum()) + dw.Get(fdDwWindDir).Int()
	}
	return acc + int64(m.Get(fdWthAlerts).List().Len())
}

func hyperStation(m protoreflect.Message) int64 {
	acc := int64(len(m.Get(fdStId).String())) + int64(len(m.Get(fdStName).String())) +
		int64(m.Get(fdStLat).Float()*1e5) + int64(m.Get(fdStLng).Float()*1e5) +
		m.Get(fdStStart).Int() + m.Get(fdStEnd).Int() + m.Get(fdStCooldown).Int()
	if m.Get(fdStBread).Bool() {
		acc++
	}
	return acc
}

func ReadGMOHyper(payload []byte, _ proto.UnmarshalOptions) error {
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
	var acc int64
	cells := msg.Get(fdGmoMapCell).List()
	for i := 0; i < cells.Len(); i++ {
		cell := cells.Get(i).Message()
		acc += int64(cell.Get(fdCellS2).Uint()) + cell.Get(fdCellAsOf).Int()
		forts := cell.Get(fdCellFort).List()
		for j := 0; j < forts.Len(); j++ {
			acc += hyperFort(forts.Get(j).Message())
		}
		wilds := cell.Get(fdCellWild).List()
		for j := 0; j < wilds.Len(); j++ {
			acc += hyperWild(wilds.Get(j).Message())
		}
		nearbys := cell.Get(fdCellNearby).List()
		for j := 0; j < nearbys.Len(); j++ {
			n := nearbys.Get(j).Message()
			acc += n.Get(fdNearbyDex).Int() + int64(n.Get(fdNearbyEnc).Uint()) +
				int64(len(n.Get(fdNearbyFortId).String()))
			if n.Has(fdNearbyDisplay) {
				acc += hyperDisplay(n.Get(fdNearbyDisplay).Message())
			}
		}
		catchables := cell.Get(fdCellCatchable).List()
		for j := 0; j < catchables.Len(); j++ {
			c := catchables.Get(j).Message()
			acc += int64(c.Get(fdMapEnc).Uint()) + c.Get(fdMapDex).Int() +
				c.Get(fdMapExpiry).Int() + int64(len(c.Get(fdMapSpawn).String()))
			if c.Has(fdMapDisplay) {
				acc += hyperDisplay(c.Get(fdMapDisplay).Message())
			}
		}
		stations := cell.Get(fdCellStations).List()
		for j := 0; j < stations.Len(); j++ {
			acc += hyperStation(stations.Get(j).Message())
		}
	}
	weathers := msg.Get(fdGmoClientWeather).List()
	for i := 0; i < weathers.Len(); i++ {
		acc += hyperWeather(weathers.Get(i).Message())
	}
	Sink.Add(acc)
	return nil
}

func ReadEncounterHyper(payload []byte, _ proto.UnmarshalOptions) error {
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
	acc := int64(msg.Get(fdEncStatus).Enum())
	if msg.Has(fdEncPokemon) {
		acc += hyperWild(msg.Get(fdEncPokemon).Message())
	}
	if msg.Has(fdEncCapture) {
		probs := msg.Get(fdEncCapture).Message().Get(fdCapProbs).List()
		for i := 0; i < probs.Len(); i++ {
			acc += int64(float32(probs.Get(i).Float()) * 1000)
		}
	}
	Sink.Add(acc)
	return nil
}
