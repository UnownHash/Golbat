package main

import (
	"strconv"
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"golbat/config"
	"golbat/pogo"
	"golbat/pogoshim"
	"golbat/stats_collector"
)

// recordingProtoShadowStats wraps a noop StatsCollector and records every
// IncProtoShadow call so tests can assert on match/mismatch counts without
// requiring a real prometheus registry.
type recordingProtoShadowStats struct {
	stats_collector.StatsCollector
	mu      sync.Mutex
	results map[string]int // "<method>|<result>" -> count
}

func newRecordingProtoShadowStats() *recordingProtoShadowStats {
	return &recordingProtoShadowStats{
		StatsCollector: stats_collector.NewNoopStatsCollector(),
		results:        map[string]int{},
	}
}

func (r *recordingProtoShadowStats) IncProtoShadow(method, result string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.results[method+"|"+result]++
}

func (r *recordingProtoShadowStats) count(method, result string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.results[method+"|"+result]
}

// --- synthetic payload builders ---------------------------------------------

func buildTestGymFort() *pogo.PokemonFortProto {
	return &pogo.PokemonFortProto{
		FortId:                   "GYM1",
		Latitude:                 12.345,
		Longitude:                -54.321,
		FortType:                 pogo.FortType_GYM,
		Team:                     pogo.Team_TEAM_RED,
		Enabled:                  true,
		IsArScanEligible:         true,
		PowerUpProgressPoints:    120,
		PowerUpLevelExpirationMs: 1_700_000_000_000,
		LastModifiedMs:           1_699_000_000_000,
		ImageUrl:                 "https://example.test/gym.png",
		PartnerId:                "partner-1",
		IsInBattle:               true,
		GuardPokemonId:           pogo.HoloPokemonId_CHARMANDER,
		GuardPokemonDisplay: &pogo.PokemonDisplayProto{
			Form:                       4,
			Costume:                    1,
			Gender:                     pogo.PokemonDisplayProto_MALE,
			Shiny:                      true,
			CurrentTempEvolution:       2,
			TemporaryEvolutionFinishMs: 1_701_000_000_000,
			Alignment:                  1,
			PokemonBadge:               1,
		},
		GymDisplay: &pogo.GymDisplayProto{
			SlotsAvailable: 2,
			TotalGymCp:     3200,
		},
		RaidInfo: &pogo.RaidInfoProto{
			RaidEndMs:    1_702_000_000_000,
			RaidSpawnMs:  1_701_500_000_000,
			RaidSeed:     998877,
			RaidBattleMs: 1_701_800_000_000,
			RaidLevel:    pogo.RaidLevel_RAID_LEVEL_5,
			RaidPokemon: &pogo.PokemonProto{
				PokemonId: pogo.HoloPokemonId_CHARMANDER,
				Move1:     pogo.HoloPokemonMove_THUNDER_SHOCK,
				Move2:     pogo.HoloPokemonMove_QUICK_ATTACK,
				Cp:        54321,
				PokemonDisplay: &pogo.PokemonDisplayProto{
					Form:                 5,
					Alignment:            2,
					Gender:               pogo.PokemonDisplayProto_FEMALE,
					Costume:              3,
					CurrentTempEvolution: 1,
				},
			},
		},
	}
}

func buildTestPokestopFort() *pogo.PokemonFortProto {
	return &pogo.PokemonFortProto{
		FortId:                "STOP1",
		Latitude:              1.111,
		Longitude:             2.222,
		FortType:              pogo.FortType_CHECKPOINT,
		Enabled:               true,
		Sponsor:               pogo.FortSponsor_MCDONALDS,
		IsArScanEligible:      true,
		PowerUpProgressPoints: 40,
		LastModifiedMs:        1_699_100_000_000,
		ActiveFortModifier:    []pogo.Item{pogo.Item_ITEM_POKE_BALL},
		ImageUrl:              "https://example.test/stop.png",
		PartnerId:             "",
		PokestopDisplay: &pogo.PokestopIncidentDisplayProto{
			IncidentId:           "incident-1",
			IncidentStartMs:      1_699_200_000_000,
			IncidentExpirationMs: 1_699_300_000_000,
			IncidentDisplayType:  pogo.IncidentDisplayType_INCIDENT_DISPLAY_TYPE_INVASION_GRUNT,
			MapDisplay: &pogo.PokestopIncidentDisplayProto_CharacterDisplay{
				CharacterDisplay: &pogo.CharacterDisplayProto{
					Style:     pogo.EnumWrapper_POKESTOP_ROCKET_INVASION,
					Character: pogo.EnumWrapper_CHARACTER_GRUNT_MALE,
				},
			},
		},
		PokestopDisplays: []*pogo.PokestopIncidentDisplayProto{
			{
				IncidentId:           "incident-2",
				IncidentStartMs:      1_699_400_000_000,
				IncidentExpirationMs: 1_699_500_000_000,
				IncidentDisplayType:  pogo.IncidentDisplayType_INCIDENT_DISPLAY_TYPE_INVASION_LEADER,
				MapDisplay: &pogo.PokestopIncidentDisplayProto_CharacterDisplay{
					CharacterDisplay: &pogo.CharacterDisplayProto{
						Style:     pogo.EnumWrapper_POKESTOP_ROCKET_VICTORY,
						Character: pogo.EnumWrapper_CHARACTER_CANDELA,
					},
				},
			},
		},
	}
}

func buildTestPokemonProto(cp int32) *pogo.PokemonProto {
	return &pogo.PokemonProto{
		Id:                7,
		PokemonId:         pogo.HoloPokemonId_BULBASAUR,
		Cp:                cp,
		Move1:             pogo.HoloPokemonMove_THUNDER_SHOCK,
		Move2:             pogo.HoloPokemonMove_QUICK_ATTACK,
		HeightM:           0.71,
		WeightKg:          6.9,
		IndividualAttack:  15,
		IndividualDefense: 14,
		IndividualStamina: 13,
		CpMultiplier:      0.79,
		Size:              pogo.HoloPokemonSize_M,
		PokemonDisplay: &pogo.PokemonDisplayProto{
			Form:                    1,
			Costume:                 2,
			Gender:                  pogo.PokemonDisplayProto_FEMALE,
			Shiny:                   true,
			WeatherBoostedCondition: pogo.GameplayWeatherProto_PARTLY_CLOUDY,
			IsStrongPokemon:         false,
		},
	}
}

func buildTestWild(cp int32) *pogo.WildPokemonProto {
	return &pogo.WildPokemonProto{
		EncounterId:      123456789,
		LastModifiedMs:   1_699_000_000_000,
		Latitude:         10.1,
		Longitude:        20.2,
		SpawnPointId:     "abcd1234",
		TimeTillHiddenMs: 890000,
		Pokemon:          buildTestPokemonProto(cp),
	}
}

func buildTestNearby() *pogo.NearbyPokemonProto {
	return &pogo.NearbyPokemonProto{
		PokedexNumber:  1,
		DistanceMeters: 42.5,
		EncounterId:    987654321,
		FortId:         "STOP1",
		FortImageUrl:   "https://example.test/stop.png",
		PokemonDisplay: &pogo.PokemonDisplayProto{
			Form:    1,
			Costume: 0,
			Gender:  pogo.PokemonDisplayProto_MALE,
		},
	}
}

func buildTestCatchable() *pogo.MapPokemonProto {
	return &pogo.MapPokemonProto{
		SpawnpointId:     "abcd1234",
		EncounterId:      555555,
		PokedexTypeId:    4,
		ExpirationTimeMs: 1_699_600_000_000,
		Latitude:         30.3,
		Longitude:        40.4,
		PokemonDisplay: &pogo.PokemonDisplayProto{
			Form:    2,
			Costume: 1,
			Gender:  pogo.PokemonDisplayProto_FEMALE,
		},
	}
}

func buildTestStation() *pogo.StationProto {
	return &pogo.StationProto{
		Id:                     "STATION1",
		Lat:                    5.5,
		Lng:                    6.6,
		Name:                   "Test Station",
		StartTimeMs:            1_699_000_000_000,
		EndTimeMs:              1_700_000_000_000,
		CooldownCompleteMs:     1_700_100_000_000,
		IsBreadBattleAvailable: true,
		BattleDetails: &pogo.BreadBattleDetailProto{
			BreadBattleSeed:     42,
			BattleLevel:         pogo.BreadBattleLevel_BREAD_BATTLE_LEVEL_2,
			BattleWindowStartMs: 1_699_050_000_000,
			BattleWindowEndMs:   1_699_060_000_000,
			BattlePokemon:       buildTestPokemonProto(1234),
		},
	}
}

func buildTestGmo(wildCp int32) *pogo.GetMapObjectsOutProto {
	return &pogo.GetMapObjectsOutProto{
		Status:         pogo.GetMapObjectsOutProto_SUCCESS,
		TimeOfDay:      pogo.GetMapObjectsOutProto_DAY,
		MoonPhase:      pogo.GetMapObjectsOutProto_FULL,
		TwilightPeriod: pogo.GetMapObjectsOutProto_DUSK,
		ClientWeather: []*pogo.ClientWeatherProto{
			{
				S2CellId: 12345,
				DisplayWeather: &pogo.DisplayWeatherProto{
					CloudLevel:         pogo.DisplayWeatherProto_LEVEL_2,
					RainLevel:          pogo.DisplayWeatherProto_LEVEL_1,
					WindLevel:          pogo.DisplayWeatherProto_LEVEL_0,
					SnowLevel:          pogo.DisplayWeatherProto_LEVEL_0,
					FogLevel:           pogo.DisplayWeatherProto_LEVEL_0,
					WindDirection:      90,
					SpecialEffectLevel: pogo.DisplayWeatherProto_LEVEL_0,
				},
				GameplayWeather: &pogo.GameplayWeatherProto{
					GameplayCondition: pogo.GameplayWeatherProto_PARTLY_CLOUDY,
				},
				Alerts: []*pogo.WeatherAlertProto{
					{Severity: pogo.WeatherAlertProto_EXTREME, WarnWeather: true},
				},
			},
		},
		MapCell: []*pogo.ClientMapCellProto{
			{
				S2CellId:         999,
				AsOfTimeMs:       1000,
				Fort:             []*pogo.PokemonFortProto{buildTestGymFort(), buildTestPokestopFort()},
				WildPokemon:      []*pogo.WildPokemonProto{buildTestWild(wildCp)},
				NearbyPokemon:    []*pogo.NearbyPokemonProto{buildTestNearby()},
				CatchablePokemon: []*pogo.MapPokemonProto{buildTestCatchable()},
				Stations:         []*pogo.StationProto{buildTestStation()},
			},
		},
	}
}

// --- helpers to pull a uint64 digest through decodeStd/decodeHyperpb -------

func digestViaStd[T any](t *testing.T, method string, payload []byte, wrap func(protoreflect.Message) T, digest func(T) uint64) uint64 {
	t.Helper()
	s, err := decodeStd(method, payload, wrap, func(v T) string { return strconv.FormatUint(digest(v), 16) })
	if err != nil {
		t.Fatalf("decodeStd failed: %v", err)
	}
	n, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		t.Fatalf("parse digest: %v", err)
	}
	return n
}

func digestViaHyperpb[T any](t *testing.T, method string, payload []byte, wrap func(protoreflect.Message) T, digest func(T) uint64) uint64 {
	t.Helper()
	s, err := decodeHyperpb(method, payload, wrap, func(v T) string { return strconv.FormatUint(digest(v), 16) })
	if err != nil {
		t.Fatalf("decodeHyperpb failed: %v", err)
	}
	n, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		t.Fatalf("parse digest: %v", err)
	}
	return n
}

func TestShadowDigestGmoMatchesAcrossEngines(t *testing.T) {
	gmo := buildTestGmo(500)
	payload, err := proto.Marshal(gmo)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	stdDigest := digestViaStd(t, engMethodGmo, payload, pogoshim.AsGetMapObjectsOutProto, digestGmo)
	hyperDigest := digestViaHyperpb(t, engMethodGmo, payload, pogoshim.AsGetMapObjectsOutProto, digestGmo)
	if stdDigest != hyperDigest {
		t.Fatalf("digest mismatch: std=%x hyperpb=%x", stdDigest, hyperDigest)
	}
	if stdDigest == 0 {
		t.Fatal("expected non-zero digest for a populated GMO payload")
	}
}

func TestShadowDigestGmoDetectsCorruption(t *testing.T) {
	original := buildTestGmo(500)
	originalPayload, err := proto.Marshal(original)
	if err != nil {
		t.Fatalf("marshal original: %v", err)
	}

	corrupted := buildTestGmo(501) // Cp+1 on the wild pokemon's nested pokemon
	corruptedPayload, err := proto.Marshal(corrupted)
	if err != nil {
		t.Fatalf("marshal corrupted: %v", err)
	}

	originalDigest := digestViaStd(t, engMethodGmo, originalPayload, pogoshim.AsGetMapObjectsOutProto, digestGmo)
	corruptedDigest := digestViaStd(t, engMethodGmo, corruptedPayload, pogoshim.AsGetMapObjectsOutProto, digestGmo)
	if originalDigest == corruptedDigest {
		t.Fatal("expected corrupted payload (Cp+1) to produce a different digest")
	}
}

func TestShadowDigestEncounterMatchesAcrossEngines(t *testing.T) {
	enc := &pogo.EncounterOutProto{
		Pokemon:    buildTestWild(777),
		Background: pogo.EncounterOutProto_PARK,
		Status:     pogo.EncounterOutProto_ENCOUNTER_SUCCESS,
		ActiveItem: pogo.Item_ITEM_GREAT_BALL,
		CaptureProbability: &pogo.CaptureProbabilityProto{
			PokeballType:           []pogo.Item{pogo.Item_ITEM_POKE_BALL, pogo.Item_ITEM_GREAT_BALL},
			CaptureProbability:     []float32{0.1, 0.2, 0.3},
			ReticleDifficultyScale: 1.25,
		},
		ArplusAttemptsUntilFlee: 3,
	}
	payload, err := proto.Marshal(enc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	stdDigest := digestViaStd(t, engMethodEncounter, payload, pogoshim.AsEncounterOutProto, digestEncounter)
	hyperDigest := digestViaHyperpb(t, engMethodEncounter, payload, pogoshim.AsEncounterOutProto, digestEncounter)
	if stdDigest != hyperDigest {
		t.Fatalf("digest mismatch: std=%x hyperpb=%x", stdDigest, hyperDigest)
	}

	corrupted := &pogo.EncounterOutProto{
		Pokemon:    buildTestWild(778), // Cp+1
		Background: pogo.EncounterOutProto_PARK,
		Status:     pogo.EncounterOutProto_ENCOUNTER_SUCCESS,
		ActiveItem: pogo.Item_ITEM_GREAT_BALL,
	}
	corruptedPayload, err := proto.Marshal(corrupted)
	if err != nil {
		t.Fatalf("marshal corrupted: %v", err)
	}
	corruptedDigest := digestViaStd(t, engMethodEncounter, corruptedPayload, pogoshim.AsEncounterOutProto, digestEncounter)
	if stdDigest == corruptedDigest {
		t.Fatal("expected corrupted encounter payload (Cp+1) to produce a different digest")
	}
}

func TestShadowDigestDiskEncounterMatchesAcrossEngines(t *testing.T) {
	disk := &pogo.DiskEncounterOutProto{
		Result:     pogo.DiskEncounterOutProto_SUCCESS,
		Pokemon:    buildTestPokemonProto(321),
		ActiveItem: pogo.Item_ITEM_RAZZ_BERRY,
		CaptureProbability: &pogo.CaptureProbabilityProto{
			PokeballType:           []pogo.Item{pogo.Item_ITEM_POKE_BALL},
			CaptureProbability:     []float32{0.42},
			ReticleDifficultyScale: 0.9,
		},
	}
	payload, err := proto.Marshal(disk)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	stdDigest := digestViaStd(t, engMethodDiskEncounter, payload, pogoshim.AsDiskEncounterOutProto, digestDiskEncounter)
	hyperDigest := digestViaHyperpb(t, engMethodDiskEncounter, payload, pogoshim.AsDiskEncounterOutProto, digestDiskEncounter)
	if stdDigest != hyperDigest {
		t.Fatalf("digest mismatch: std=%x hyperpb=%x", stdDigest, hyperDigest)
	}

	corrupted := &pogo.DiskEncounterOutProto{
		Result:     pogo.DiskEncounterOutProto_SUCCESS,
		Pokemon:    buildTestPokemonProto(322), // Cp+1
		ActiveItem: pogo.Item_ITEM_RAZZ_BERRY,
	}
	corruptedPayload, err := proto.Marshal(corrupted)
	if err != nil {
		t.Fatalf("marshal corrupted: %v", err)
	}
	corruptedDigest := digestViaStd(t, engMethodDiskEncounter, corruptedPayload, pogoshim.AsDiskEncounterOutProto, digestDiskEncounter)
	if stdDigest == corruptedDigest {
		t.Fatal("expected corrupted disk encounter payload (Cp+1) to produce a different digest")
	}
}

// TestShadowCompareMatchesForWellFormedPayloads is the core correctness
// property from the task brief: shadowCompare must return true (match) for
// every well-formed payload, across all three shadow-verified methods.
func TestShadowCompareMatchesForWellFormedPayloads(t *testing.T) {
	gmoPayload, err := proto.Marshal(buildTestGmo(500))
	if err != nil {
		t.Fatalf("marshal gmo: %v", err)
	}
	encPayload, err := proto.Marshal(&pogo.EncounterOutProto{
		Pokemon:            buildTestWild(500),
		ActiveItem:         pogo.Item_ITEM_GREAT_BALL,
		CaptureProbability: &pogo.CaptureProbabilityProto{CaptureProbability: []float32{0.5}},
	})
	if err != nil {
		t.Fatalf("marshal encounter: %v", err)
	}
	diskPayload, err := proto.Marshal(&pogo.DiskEncounterOutProto{
		Result:     pogo.DiskEncounterOutProto_SUCCESS,
		Pokemon:    buildTestPokemonProto(321),
		ActiveItem: pogo.Item_ITEM_RAZZ_BERRY,
	})
	if err != nil {
		t.Fatalf("marshal disk encounter: %v", err)
	}

	cases := []struct {
		method  string
		payload []byte
	}{
		{engMethodGmo, gmoPayload},
		{engMethodEncounter, encPayload},
		{engMethodDiskEncounter, diskPayload},
	}
	for _, c := range cases {
		t.Run(c.method, func(t *testing.T) {
			if !shadowCompare(c.method, c.payload) {
				t.Fatalf("shadowCompare(%s, ...) = false, want true for a well-formed payload", c.method)
			}
		})
	}
}

// TestMaybeShadowForcedRateRecordsMatchNotMismatch exercises maybeShadow end
// to end with the sample rate forced to 1.0: every call must decode and
// compare, and a well-formed payload must never be counted as a mismatch.
func TestMaybeShadowForcedRateRecordsMatchNotMismatch(t *testing.T) {
	setEngine(engMethodGmo, "hyperpb")
	prevRate := config.Config.ProtoEngine.ShadowSampleRate
	config.Config.ProtoEngine.ShadowSampleRate = 1.0
	defer func() { config.Config.ProtoEngine.ShadowSampleRate = prevRate }()

	previousStats := statsCollector
	recorder := newRecordingProtoShadowStats()
	statsCollector = recorder
	defer func() { statsCollector = previousStats }()

	payload, err := proto.Marshal(buildTestGmo(500))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	maybeShadow(engMethodGmo, payload)

	if got := recorder.count(engMethodGmo, "mismatch"); got != 0 {
		t.Fatalf("expected 0 mismatches for a well-formed payload, got %d", got)
	}
	if got := recorder.count(engMethodGmo, "match"); got != 1 {
		t.Fatalf("expected 1 match for a well-formed payload, got %d", got)
	}
}

// TestMaybeShadowSkipsWhenLiveEngineIsStd ensures maybeShadow never even
// samples/decodes when the configured live engine for the method is std --
// shadow verification only makes sense when hyperpb is the one being
// verified against.
func TestMaybeShadowSkipsWhenLiveEngineIsStd(t *testing.T) {
	setEngine(engMethodGmo, "std")
	prevRate := config.Config.ProtoEngine.ShadowSampleRate
	config.Config.ProtoEngine.ShadowSampleRate = 1.0
	defer func() { config.Config.ProtoEngine.ShadowSampleRate = prevRate }()

	previousStats := statsCollector
	recorder := newRecordingProtoShadowStats()
	statsCollector = recorder
	defer func() { statsCollector = previousStats }()

	payload, err := proto.Marshal(buildTestGmo(500))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	maybeShadow(engMethodGmo, payload)

	if got := recorder.count(engMethodGmo, "match") + recorder.count(engMethodGmo, "mismatch"); got != 0 {
		t.Fatalf("expected maybeShadow to skip entirely when live engine is std, got %d stats calls", got)
	}

	setEngine(engMethodGmo, "hyperpb") // restore TestMain's baseline for other tests
}
