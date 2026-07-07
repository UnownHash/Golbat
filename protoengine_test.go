package main

import (
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"golbat/config"
	"golbat/pogo"
	"golbat/pogoshim"
)

// malformedPayload is not a valid protobuf encoding for any message used
// here: a run of continuation-bit-set bytes with no terminator, which both
// protobuf-go and hyperpb reject while parsing the leading varint.
var malformedPayload = []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF}

func TestMain(m *testing.M) {
	// decodeWithArena reads config.Config.ProtoEngine per call, so the exact
	// values here don't matter beyond giving initProtoEngines something to
	// compile for every method; individual subtests override per-method
	// engine selection before calling decodeWithArena.
	config.Config.ProtoEngine.Gmo = "hyperpb"
	config.Config.ProtoEngine.Encounter = "hyperpb"
	config.Config.ProtoEngine.DiskEncounter = "hyperpb"
	config.Config.ProtoEngine.Default = "hyperpb"
	config.Config.ProtoEngine.Pgo = true
	config.Config.ProtoEngine.ShadowSampleRate = 0.01
	initProtoEngines()

	// Guard against a silently broken hyperpb wiring: if initProtoEngines
	// didn't populate one of these package-var handles, decodeHyperpb would
	// fall back to decodeStd without any test ever noticing.
	for name, eng := range map[string]*protoEngineHandle{
		engMethodGmo:               gmoEngine,
		engMethodEncounter:         encounterEngine,
		engMethodDiskEncounter:     diskEncounterEngine,
		engMethodFortDetails:       fortDetailsEngine,
		engMethodGymInfo:           gymInfoEngine,
		engMethodQuest:             questEngine,
		engMethodGetMapForts:       mapFortsEngine,
		engMethodRoutes:            routesEngine,
		engMethodStartIncident:     startIncidentEngine,
		"open_invasion_request":    openInvasionReqEngine,
		"open_invasion_data":       openInvasionEngine,
		engMethodNebulaBattleState: battleStateEngine,
		"contest_data_request":     contestDataReqEngine,
		"contest_data_data":        contestDataEngine,
		"size_entry_request":       sizeEntryReqEngine,
		"size_entry_data":          sizeEntryEngine,
		"station_details_request":  stationDetailsReqEngine,
		"station_details_data":     stationDetailsEngine,
		"tappable_request":         tappableReqEngine,
		"tappable_data":            tappableEngine,
		"rsvp_request":             rsvpReqEngine,
		"rsvp_data":                rsvpEngine,
		engMethodEventRsvpCount:    rsvpCountEngine,
		"proxy_request":            proxyReqEngine,
		"proxy_response":           proxyRespEngine,
		"friend_details":           friendDetailsEngine,
		"search_player_out":        searchPlayerOutEngine,
		"search_player_req":        searchPlayerReqEngine,
	} {
		if eng == nil {
			panic("initProtoEngines did not populate the " + name + " engine handle")
		}
	}

	os.Exit(m.Run())
}

func setEngine(method, engine string) {
	switch method {
	case engMethodGmo:
		config.Config.ProtoEngine.Gmo = engine
	case engMethodEncounter:
		config.Config.ProtoEngine.Encounter = engine
	case engMethodDiskEncounter:
		config.Config.ProtoEngine.DiskEncounter = engine
	}
}

func TestDecodeWithArenaGmo(t *testing.T) {
	for _, engine := range []string{"std", "hyperpb"} {
		t.Run(engine, func(t *testing.T) {
			setEngine(engMethodGmo, engine)
			if engine == "hyperpb" && gmoEngine == nil {
				t.Fatal("gmoEngine is nil; hyperpb subtest would silently fall back to decodeStd")
			}

			in := &pogo.GetMapObjectsOutProto{Status: pogo.GetMapObjectsOutProto_SUCCESS}
			raw, err := proto.Marshal(in)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			var called bool
			got, err := decodeWithArena(engMethodGmo, gmoEngine, raw, pogoshim.AsGetMapObjectsOutProto, func(g pogoshim.GetMapObjectsOutProto) string {
				called = true
				return g.GetStatus().String()
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !called {
				t.Fatal("process was not called")
			}
			if want := in.Status.String(); got != want {
				t.Fatalf("digest mismatch: got %q want %q", got, want)
			}

			called = false
			if _, err := decodeWithArena(engMethodGmo, gmoEngine, malformedPayload, pogoshim.AsGetMapObjectsOutProto, func(g pogoshim.GetMapObjectsOutProto) string {
				called = true
				return ""
			}); err == nil {
				t.Fatal("expected error for malformed payload")
			}
			if called {
				t.Fatal("process must not be called when unmarshal fails")
			}
		})
	}
}

func TestDecodeWithArenaEncounter(t *testing.T) {
	for _, engine := range []string{"std", "hyperpb"} {
		t.Run(engine, func(t *testing.T) {
			setEngine(engMethodEncounter, engine)
			if engine == "hyperpb" && encounterEngine == nil {
				t.Fatal("encounterEngine is nil; hyperpb subtest would silently fall back to decodeStd")
			}

			in := &pogo.EncounterOutProto{
				Pokemon: &pogo.WildPokemonProto{
					EncounterId:  7,
					SpawnPointId: "ABCD",
					Pokemon: &pogo.PokemonProto{
						Cp:           500,
						CpMultiplier: 0.79,
					},
				},
				ActiveItem: pogo.Item_ITEM_GREAT_BALL,
			}
			raw, err := proto.Marshal(in)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			var called bool
			got, err := decodeWithArena(engMethodEncounter, encounterEngine, raw, pogoshim.AsEncounterOutProto, func(e pogoshim.EncounterOutProto) string {
				called = true
				w := e.GetPokemon()
				p := w.GetPokemon()
				return w.GetSpawnPointId() + "|" + e.GetActiveItem().String() + "|" + p.GetPokemonId().String()
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !called {
				t.Fatal("process was not called")
			}
			want := in.Pokemon.SpawnPointId + "|" + in.ActiveItem.String() + "|" + in.Pokemon.Pokemon.PokemonId.String()
			if got != want {
				t.Fatalf("digest mismatch: got %q want %q", got, want)
			}

			called = false
			if _, err := decodeWithArena(engMethodEncounter, encounterEngine, malformedPayload, pogoshim.AsEncounterOutProto, func(e pogoshim.EncounterOutProto) string {
				called = true
				return ""
			}); err == nil {
				t.Fatal("expected error for malformed payload")
			}
			if called {
				t.Fatal("process must not be called when unmarshal fails")
			}
		})
	}
}

func TestDecodeWithArenaDiskEncounter(t *testing.T) {
	for _, engine := range []string{"std", "hyperpb"} {
		t.Run(engine, func(t *testing.T) {
			setEngine(engMethodDiskEncounter, engine)
			if engine == "hyperpb" && diskEncounterEngine == nil {
				t.Fatal("diskEncounterEngine is nil; hyperpb subtest would silently fall back to decodeStd")
			}

			in := &pogo.DiskEncounterOutProto{
				Result: pogo.DiskEncounterOutProto_SUCCESS,
				Pokemon: &pogo.PokemonProto{
					Cp: 321,
				},
				ActiveItem: pogo.Item_ITEM_RAZZ_BERRY,
			}
			raw, err := proto.Marshal(in)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			var called bool
			got, err := decodeWithArena(engMethodDiskEncounter, diskEncounterEngine, raw, pogoshim.AsDiskEncounterOutProto, func(d pogoshim.DiskEncounterOutProto) string {
				called = true
				return d.GetResult().String() + "|" + d.GetActiveItem().String() + "|" + strconv.Itoa(int(d.GetPokemon().GetCp()))
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !called {
				t.Fatal("process was not called")
			}
			want := in.Result.String() + "|" + in.ActiveItem.String() + "|" + strconv.Itoa(int(in.Pokemon.Cp))
			if got != want {
				t.Fatalf("digest mismatch: got %q want %q", got, want)
			}

			called = false
			if _, err := decodeWithArena(engMethodDiskEncounter, diskEncounterEngine, malformedPayload, pogoshim.AsDiskEncounterOutProto, func(d pogoshim.DiskEncounterOutProto) string {
				called = true
				return ""
			}); err == nil {
				t.Fatal("expected error for malformed payload")
			}
			if called {
				t.Fatal("process must not be called when unmarshal fails")
			}
		})
	}
}

func TestEngineForRespectsConfig(t *testing.T) {
	setEngine(engMethodGmo, "std")
	if got := engineFor(engMethodGmo); got != "std" {
		t.Fatalf("engineFor(gmo) = %q, want std", got)
	}
	setEngine(engMethodGmo, "hyperpb")
	want := "hyperpb"
	if !hyperpbSupported {
		want = "std"
	}
	if got := engineFor(engMethodGmo); got != want {
		t.Fatalf("engineFor(gmo) = %q, want %q", got, want)
	}
}

// TestDecodeWithArenaPGoRace exercises the PGO warmup/recompile path under
// concurrent decodeWithArena callers. recordPGO mutates profile.seen and
// profile.done under profile.mu, but decodeHyperpb's hot-path check of
// profile.done happens outside that lock -- run with -race to catch a
// regression back to a plain bool there.
func TestDecodeWithArenaPGoRace(t *testing.T) {
	const method = engMethodDiskEncounter
	setEngine(method, "hyperpb")
	if diskEncounterEngine == nil {
		t.Fatalf("diskEncounterEngine is nil; hyperpb engine not wired")
	}
	if !config.Config.ProtoEngine.Pgo {
		t.Fatal("test requires config.Config.ProtoEngine.Pgo = true")
	}

	in := &pogo.DiskEncounterOutProto{
		Result: pogo.DiskEncounterOutProto_SUCCESS,
		Pokemon: &pogo.PokemonProto{
			Cp: 321,
		},
		ActiveItem: pogo.Item_ITEM_RAZZ_BERRY,
	}
	raw, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	const goroutines = 8
	const iterations = 100 // 8*100 = 800, well over pgoWarmupSamples (256)

	var wg sync.WaitGroup
	errCh := make(chan error, goroutines*iterations)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				_, err := decodeWithArena(method, diskEncounterEngine, raw, pogoshim.AsDiskEncounterOutProto, func(d pogoshim.DiskEncounterOutProto) string {
					return d.GetResult().String()
				})
				if err != nil {
					errCh <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("decode failed: %v", err)
	}

	if !diskEncounterEngine.profile.done.Load() {
		t.Fatal("expected PGO profile to be done after well over pgoWarmupSamples decodes")
	}
}

// TestPgoWarmupDeadlineStopsRecording guards the Wave 3 PGO deadline:
// once pgoWarmupDeadline has elapsed since startPgoWarmupClock() ran, even
// far short of pgoWarmupSamples, recordPGO must record no further samples,
// keep the BASELINE parser (no recompile), release the pending profile
// (recorder medians measured ~28MB resident across the 28-handle fleet),
// and flip done so the hot path stops entering recordPGO at all.
func TestPgoWarmupDeadlineStopsRecording(t *testing.T) {
	if !hyperpbSupported {
		t.Skip("no PGO warmup concept on the std-only build")
	}
	saved := config.Config.ProtoEngine
	defer func() { config.Config.ProtoEngine = saved }()
	config.Config.ProtoEngine.Pgo = true

	savedDeadline := pgoWarmupDeadlineAt.Load()
	defer pgoWarmupDeadlineAt.Store(savedDeadline)

	eng := newProtoEngine(engMethodFortDetails, (*pogo.FortDetailsOutProto)(nil).ProtoReflect().Descriptor(),
		func() proto.Message { return &pogo.FortDetailsOutProto{} })
	baselineTy := eng.ty.Load()

	// Simulate the warmup deadline having already passed.
	pgoWarmupDeadlineAt.Store(time.Now().Add(-time.Minute).UnixNano())

	raw, err := proto.Marshal(&pogo.FortDetailsOutProto{Id: "FORT1"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	eng.recordPGO(raw)

	if eng.profile.seen != 0 {
		t.Fatalf("expected recordPGO to record no samples past the warmup deadline, but profile.seen = %d", eng.profile.seen)
	}
	if eng.ty.Load() != baselineTy {
		t.Fatal("expected the baseline parser to be kept past the warmup deadline (no recompile)")
	}
	if eng.profile.pending != nil {
		t.Fatal("expected the pending profile to be released at the warmup deadline")
	}
	if !eng.profile.done.Load() {
		t.Fatal("expected profile.done to be set at the warmup deadline so the hot path stops calling recordPGO")
	}
}

// TestPgoWarmupDeadlineNotYetExpiredStillRecords is the control case for
// TestPgoWarmupDeadlineStopsRecording: with a deadline safely in the
// future, recordPGO must behave normally (profile.seen increments).
func TestPgoWarmupDeadlineNotYetExpiredStillRecords(t *testing.T) {
	if !hyperpbSupported {
		t.Skip("no PGO warmup concept on the std-only build")
	}
	saved := config.Config.ProtoEngine
	defer func() { config.Config.ProtoEngine = saved }()
	config.Config.ProtoEngine.Pgo = true

	savedDeadline := pgoWarmupDeadlineAt.Load()
	defer pgoWarmupDeadlineAt.Store(savedDeadline)
	pgoWarmupDeadlineAt.Store(time.Now().Add(pgoWarmupDeadline).UnixNano())

	eng := newProtoEngine(engMethodFortDetails, (*pogo.FortDetailsOutProto)(nil).ProtoReflect().Descriptor(),
		func() proto.Message { return &pogo.FortDetailsOutProto{} })

	raw, err := proto.Marshal(&pogo.FortDetailsOutProto{Id: "FORT1"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	eng.recordPGO(raw)

	if eng.profile.seen != 1 {
		t.Fatalf("expected recordPGO to record one sample before the deadline, got profile.seen = %d", eng.profile.seen)
	}
}

// TestInvalidProtoEngineValuesDetectsTypo guards the config-typo warning:
// engineFor() silently treats anything other than "hyperpb" as "std", so a
// mistyped value (e.g. "hyperbp") must be flagged rather than passing
// through unnoticed.
func TestInvalidProtoEngineValuesDetectsTypo(t *testing.T) {
	saved := config.Config.ProtoEngine
	defer func() { config.Config.ProtoEngine = saved }()

	config.Config.ProtoEngine.Gmo = "hyperbp" // typo for "hyperpb"
	config.Config.ProtoEngine.Encounter = "std"
	config.Config.ProtoEngine.DiskEncounter = "hyperpb"

	bad := invalidProtoEngineValues()
	if len(bad) != 1 {
		t.Fatalf("expected exactly one invalid value, got %v", bad)
	}
	if v, ok := bad[engMethodGmo]; !ok || v != "hyperbp" {
		t.Fatalf("expected gmo=%q flagged as invalid, got %v", "hyperbp", bad)
	}
}

// TestInvalidProtoEngineValuesAcceptsValidValues ensures the two recognized
// values never produce a false-positive warning.
func TestInvalidProtoEngineValuesAcceptsValidValues(t *testing.T) {
	saved := config.Config.ProtoEngine
	defer func() { config.Config.ProtoEngine = saved }()

	config.Config.ProtoEngine.Gmo = "std"
	config.Config.ProtoEngine.Encounter = "hyperpb"
	config.Config.ProtoEngine.DiskEncounter = "std"

	if bad := invalidProtoEngineValues(); len(bad) != 0 {
		t.Fatalf("expected no invalid values, got %v", bad)
	}
}

// TestInvalidProtoEngineValuesCoversDefaultAndOverrides extends the typo
// guard to the Wave 3 config surface: Default and each Overrides entry must
// be flagged exactly like the legacy per-method keys, under a descriptive
// "default"/"overrides.<method>" key, while an empty value (the "inherit"
// sentinel used throughout this config) must never be flagged.
func TestInvalidProtoEngineValuesCoversDefaultAndOverrides(t *testing.T) {
	saved := config.Config.ProtoEngine
	defer func() { config.Config.ProtoEngine = saved }()

	config.Config.ProtoEngine.Gmo = ""
	config.Config.ProtoEngine.Encounter = ""
	config.Config.ProtoEngine.DiskEncounter = ""
	config.Config.ProtoEngine.Default = "hyperbp" // typo
	config.Config.ProtoEngine.Overrides = map[string]string{
		engMethodFortDetails: "hyperpb",  // valid
		engMethodGymInfo:     "",         // valid (inherit)
		engMethodQuest:       "hyperbop", // typo
	}

	bad := invalidProtoEngineValues()
	if len(bad) != 2 {
		t.Fatalf("expected exactly two invalid values, got %v", bad)
	}
	if v, ok := bad["default"]; !ok || v != "hyperbp" {
		t.Fatalf("expected default=%q flagged as invalid, got %v", "hyperbp", bad)
	}
	if v, ok := bad["overrides."+engMethodQuest]; !ok || v != "hyperbop" {
		t.Fatalf("expected overrides.%s=%q flagged as invalid, got %v", engMethodQuest, "hyperbop", bad)
	}
}

// TestEngineForResolutionMatrix exercises engineFor's full resolution order:
// an explicit legacy key wins outright when non-empty; otherwise a
// per-method override; otherwise the package default. Table-driven across
// legacy set/unset x override set/unset x default, per the task brief.
func TestEngineForResolutionMatrix(t *testing.T) {
	saved := config.Config.ProtoEngine
	defer func() { config.Config.ProtoEngine = saved }()

	resolve := func(want string) string {
		if !hyperpbSupported {
			return "std"
		}
		return want
	}

	cases := []struct {
		name     string
		legacy   string // engMethodGmo's legacy field only
		override string // engMethodFortDetails's Overrides entry only
		def      string
		method   string
		want     string
	}{
		{"no legacy, no override: default hyperpb wins", "", "", "hyperpb", engMethodFortDetails, resolve("hyperpb")},
		{"no legacy, no override: default std wins", "", "", "std", engMethodFortDetails, "std"},
		{"no legacy: override hyperpb beats default std", "", "hyperpb", "std", engMethodFortDetails, resolve("hyperpb")},
		{"no legacy: override std beats default hyperpb", "", "std", "hyperpb", engMethodFortDetails, "std"},
		{"no legacy: empty override falls through to default", "", "", "std", engMethodFortDetails, "std"},
		{"legacy std beats override+default hyperpb", "std", "hyperpb", "hyperpb", engMethodGmo, "std"},
		{"legacy hyperpb beats override+default std", "hyperpb", "std", "std", engMethodGmo, resolve("hyperpb")},
		{"legacy empty falls through to default (no override for gmo)", "", "", "std", engMethodGmo, "std"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			config.Config.ProtoEngine.Gmo = ""
			config.Config.ProtoEngine.Encounter = ""
			config.Config.ProtoEngine.DiskEncounter = ""
			if c.method == engMethodGmo {
				config.Config.ProtoEngine.Gmo = c.legacy
			}
			config.Config.ProtoEngine.Default = c.def
			config.Config.ProtoEngine.Overrides = map[string]string{}
			if c.method != engMethodGmo || c.override != "" {
				config.Config.ProtoEngine.Overrides[c.method] = c.override
			}

			if got := engineFor(c.method); got != c.want {
				t.Fatalf("engineFor(%s) = %q, want %q", c.method, got, c.want)
			}
		})
	}
}

// TestDecodeWithArenaFortDetails is the task brief's "one new-root decode
// via both engines" case: fort_details has no legacy config field, so this
// also exercises the Overrides path end to end against a real new-root
// handle (fortDetailsEngine) rather than one of the three original methods.
func TestDecodeWithArenaFortDetails(t *testing.T) {
	saved := config.Config.ProtoEngine.Overrides
	defer func() { config.Config.ProtoEngine.Overrides = saved }()

	for _, engine := range []string{"std", "hyperpb"} {
		t.Run(engine, func(t *testing.T) {
			config.Config.ProtoEngine.Overrides = map[string]string{engMethodFortDetails: engine}
			if engine == "hyperpb" && fortDetailsEngine == nil {
				t.Fatal("fortDetailsEngine is nil; hyperpb subtest would silently fall back to decodeStd")
			}

			in := &pogo.FortDetailsOutProto{
				Id:        "FORT1",
				Latitude:  12.5,
				Longitude: -54.25,
				FortType:  pogo.FortType_CHECKPOINT,
			}
			raw, err := proto.Marshal(in)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			var called bool
			got, err := decodeWithArena(engMethodFortDetails, fortDetailsEngine, raw, pogoshim.AsFortDetailsOutProto, func(fd pogoshim.FortDetailsOutProto) string {
				called = true
				return fd.GetId()
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !called {
				t.Fatal("process was not called")
			}
			if got != in.Id {
				t.Fatalf("got %q want %q", got, in.Id)
			}

			called = false
			if _, err := decodeWithArena(engMethodFortDetails, fortDetailsEngine, malformedPayload, pogoshim.AsFortDetailsOutProto, func(fd pogoshim.FortDetailsOutProto) string {
				called = true
				return ""
			}); err == nil {
				t.Fatal("expected error for malformed payload")
			}
			if called {
				t.Fatal("process must not be called when unmarshal fails")
			}
		})
	}
}
