package readers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"protobench/pogo"
)

// All engines walk the same access set and fold into Sink with the same
// arithmetic, so for any payload every engine must produce an identical
// Sink delta. This is the cross-engine correctness gate.

func sinkDelta(t *testing.T, r Reader, payload []byte) int64 {
	t.Helper()
	before := Sink.Load()
	if err := r(payload, proto.UnmarshalOptions{}); err != nil {
		t.Fatalf("reader failed: %v", err)
	}
	return Sink.Load() - before
}

func engines(method string) map[string]Reader {
	return map[string]Reader{
		"std":       Registry[method],
		"vt":        RegistryVT[method],
		"vtpool":    RegistryVTPool[method],
		"hyperpb":   RegistryHyperpb[method],
		"hypershim": RegistryHypershim[method],
	}
}

func assertAllEnginesAgree(t *testing.T, method string, payload []byte) {
	t.Helper()
	want := sinkDelta(t, Registry[method], payload)
	for name, r := range engines(method) {
		if got := sinkDelta(t, r, payload); got != want {
			t.Errorf("engine %s: sink delta %d, std %d", name, got, want)
		}
	}
}

func TestEnginesAgreeSynthetic(t *testing.T) {
	wild := pogo.WildPokemonProto_builder{
		EncounterId:  7,
		SpawnPointId: "ABCD",
		Latitude:     51.5,
		Longitude:    -0.13,
		Pokemon: pogo.PokemonProto_builder{
			Cp: 500, IndividualAttack: 15, HeightM: 0.42, CpMultiplier: 0.79,
			PokemonDisplay: pogo.PokemonDisplayProto_builder{Shiny: true}.Build(),
		}.Build(),
	}.Build()
	fort := pogo.PokemonFortProto_builder{
		FortId: "fort.1", Latitude: 51.5, Longitude: -0.1, Enabled: true,
		PowerUpProgressPoints: 55,
	}.Build()
	cell := pogo.ClientMapCellProto_builder{
		S2CellId:    123456789,
		AsOfTimeMs:  1751800000000,
		Fort:        []*pogo.PokemonFortProto{fort},
		WildPokemon: []*pogo.WildPokemonProto{wild},
	}.Build()
	gmo := pogo.GetMapObjectsOutProto_builder{MapCell: []*pogo.ClientMapCellProto{cell}}.Build()
	raw, err := proto.Marshal(gmo)
	if err != nil {
		t.Fatal(err)
	}
	assertAllEnginesAgree(t, "GET_MAP_OBJECTS", raw)

	enc := pogo.EncounterOutProto_builder{
		Pokemon: wild,
		CaptureProbability: pogo.CaptureProbabilityProto_builder{
			CaptureProbability: []float32{0.4, 0.5, 0.6},
		}.Build(),
	}.Build()
	rawEnc, err := proto.Marshal(enc)
	if err != nil {
		t.Fatal(err)
	}
	assertAllEnginesAgree(t, "ENCOUNTER", rawEnc)
}

// TestEnginesAgreeCorpus sweeps every real payload when a corpus is present
// (PROTOBENCH_CORPUS or ../../capture); skips otherwise.
func TestEnginesAgreeCorpus(t *testing.T) {
	dir := os.Getenv("PROTOBENCH_CORPUS")
	if dir == "" {
		dir = "../../capture"
	}
	for _, method := range []string{"GET_MAP_OBJECTS", "ENCOUNTER"} {
		files, err := os.ReadDir(filepath.Join(dir, method))
		if err != nil {
			t.Skipf("no corpus at %s: %v", dir, err)
		}
		checked := 0
		for _, f := range files {
			if !strings.HasSuffix(f.Name(), ".bin") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, method, f.Name()))
			if err != nil {
				t.Fatal(err)
			}
			assertAllEnginesAgree(t, method, data)
			checked++
		}
		t.Logf("%s: %d payloads, all engines agree", method, checked)
	}
}
