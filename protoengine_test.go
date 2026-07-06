package main

import (
	"os"
	"strconv"
	"sync"
	"testing"

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
	config.Config.ProtoEngine.Pgo = true
	config.Config.ProtoEngine.ShadowSampleRate = 0.01
	initProtoEngines()

	// Guard against a silently broken hyperpb wiring: if initProtoEngines
	// didn't populate an entry for one of these methods, decodeHyperpb would
	// fall back to decodeStd without any test ever noticing.
	for _, method := range []string{engMethodGmo, engMethodEncounter, engMethodDiskEncounter} {
		if hyperEngines[method] == nil {
			panic("initProtoEngines did not populate hyperEngines[" + method + "]")
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
			if engine == "hyperpb" && hyperEngines[engMethodGmo] == nil {
				t.Fatal("hyperEngines[engMethodGmo] is nil; hyperpb subtest would silently fall back to decodeStd")
			}

			in := &pogo.GetMapObjectsOutProto{Status: pogo.GetMapObjectsOutProto_SUCCESS}
			raw, err := proto.Marshal(in)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			var called bool
			got, err := decodeWithArena(engMethodGmo, raw, pogoshim.AsGetMapObjectsOutProto, func(g pogoshim.GetMapObjectsOutProto) string {
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
			if _, err := decodeWithArena(engMethodGmo, malformedPayload, pogoshim.AsGetMapObjectsOutProto, func(g pogoshim.GetMapObjectsOutProto) string {
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
			if engine == "hyperpb" && hyperEngines[engMethodEncounter] == nil {
				t.Fatal("hyperEngines[engMethodEncounter] is nil; hyperpb subtest would silently fall back to decodeStd")
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
			got, err := decodeWithArena(engMethodEncounter, raw, pogoshim.AsEncounterOutProto, func(e pogoshim.EncounterOutProto) string {
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
			if _, err := decodeWithArena(engMethodEncounter, malformedPayload, pogoshim.AsEncounterOutProto, func(e pogoshim.EncounterOutProto) string {
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
			if engine == "hyperpb" && hyperEngines[engMethodDiskEncounter] == nil {
				t.Fatal("hyperEngines[engMethodDiskEncounter] is nil; hyperpb subtest would silently fall back to decodeStd")
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
			got, err := decodeWithArena(engMethodDiskEncounter, raw, pogoshim.AsDiskEncounterOutProto, func(d pogoshim.DiskEncounterOutProto) string {
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
			if _, err := decodeWithArena(engMethodDiskEncounter, malformedPayload, pogoshim.AsDiskEncounterOutProto, func(d pogoshim.DiskEncounterOutProto) string {
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
	if hyperEngines[method] == nil {
		t.Fatalf("hyperEngines[%s] is nil; hyperpb engine not wired", method)
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
				_, err := decodeWithArena(method, raw, pogoshim.AsDiskEncounterOutProto, func(d pogoshim.DiskEncounterOutProto) string {
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

	if !hyperEngines[method].profile.done.Load() {
		t.Fatal("expected PGO profile to be done after well over pgoWarmupSamples decodes")
	}
}
