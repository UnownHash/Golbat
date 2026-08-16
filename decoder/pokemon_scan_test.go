package decoder

import (
	"encoding/hex"
	"testing"

	"golbat/grpc"
	"google.golang.org/protobuf/proto"
)

// legacyGolbatInternalHex is a real golbat_internal payload produced by the
// code that predates the pokemonScan type — i.e. proto.Marshal of a
// grpc.PokemonInternal holding two grpc.PokemonScan entries, exactly what the
// write boundary in savePokemonRecordAsAtTime used to emit. Operators have rows
// containing bytes of this shape, so decoding it correctly is a compatibility
// requirement, not a stylistic one.
const legacyGolbatInternalHex = "0a1a08031001181f200f280e300d38054084014807500258b80760010a0c181428043009380140135001"

// legacyScanHistory is the same content as legacyGolbatInternalHex expressed in
// the old protobuf shape.
func legacyScanHistory() *grpc.PokemonInternal {
	return &grpc.PokemonInternal{ScanHistory: []*grpc.PokemonScan{
		{Weather: 3, Strong: true, Level: 31, Attack: 15, Defense: 14, Stamina: 13,
			CellWeather: 5, Pokemon: 132, Costume: 7, Gender: 2, Form: 952, Confirmed: true},
		{Level: 20, Defense: 4, Stamina: 9, CellWeather: 1, Pokemon: 19, Gender: 1},
	}}
}

// wantScanHistory is legacyScanHistory in the in-memory shape.
func wantScanHistory() []pokemonScan {
	return []pokemonScan{
		{Weather: 3, Strong: true, Level: 31, Attack: 15, Defense: 14, Stamina: 13,
			CellWeather: 5, Pokemon: 132, Costume: 7, Gender: 2, Form: 952, Confirmed: true},
		{Level: 20, Defense: 4, Stamina: 9, CellWeather: 1, Pokemon: 19, Gender: 1},
	}
}

func assertScanHistory(t *testing.T, got []*pokemonScan, want []pokemonScan) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("scan history length = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] == nil {
			t.Fatalf("scan history entry %d is nil", i)
		}
		if *got[i] != want[i] {
			t.Errorf("scan history entry %d = %+v, want %+v", i, *got[i], want[i])
		}
	}
}

// TestPopulateInternalDecodesLegacyBytes is the test that protects existing
// operator data: bytes written by the pre-pokemonScan code must still decode
// into the correct in-memory scan history.
func TestPopulateInternalDecodesLegacyBytes(t *testing.T) {
	stored, err := hex.DecodeString(legacyGolbatInternalHex)
	if err != nil {
		t.Fatalf("bad golden hex: %s", err)
	}

	pokemon := &Pokemon{}
	pokemon.GolbatInternal = stored
	pokemon.populateInternal()
	assertScanHistory(t, pokemon.scanHistory, wantScanHistory())

	// The golden bytes must also be what the old shape marshals to today —
	// if this fails, either the .pb.go was regenerated with different field
	// numbers (a real wire break) or the golden was mistyped.
	fresh, err := proto.Marshal(legacyScanHistory())
	if err != nil {
		t.Fatalf("marshal legacy shape: %s", err)
	}
	if hex.EncodeToString(fresh) != legacyGolbatInternalHex {
		t.Errorf("legacy shape now marshals to %s, want %s", hex.EncodeToString(fresh), legacyGolbatInternalHex)
	}
}

// TestScanHistoryWireRoundTrip covers the write boundary and back: what the new
// code writes into golbat_internal must decode to the same history, and must be
// byte-identical to what the old code wrote for the same content (so a row
// written after this change is still readable by a Golbat running before it).
func TestScanHistoryWireRoundTrip(t *testing.T) {
	history := make([]*pokemonScan, 0, 2)
	for _, scan := range wantScanHistory() {
		history = append(history, &scan)
	}

	marshaled, err := proto.Marshal(scanHistoryToProto(history))
	if err != nil {
		t.Fatalf("marshal: %s", err)
	}
	if hex.EncodeToString(marshaled) != legacyGolbatInternalHex {
		t.Errorf("new write path produced %s, want the legacy bytes %s",
			hex.EncodeToString(marshaled), legacyGolbatInternalHex)
	}
	if !proto.Equal(scanHistoryToProto(history), legacyScanHistory()) {
		t.Errorf("new write path built a different message than the legacy shape")
	}

	var decoded grpc.PokemonInternal
	if err := proto.Unmarshal(marshaled, &decoded); err != nil {
		t.Fatalf("unmarshal: %s", err)
	}
	assertScanHistory(t, scanHistoryFromProto(&decoded), wantScanHistory())
}

func TestScanHistoryEmptyRoundTrip(t *testing.T) {
	marshaled, err := proto.Marshal(scanHistoryToProto(nil))
	if err != nil {
		t.Fatalf("marshal: %s", err)
	}
	if len(marshaled) != 0 {
		t.Errorf("empty history marshaled to %d bytes, want 0", len(marshaled))
	}
	if got := scanHistoryFromProto(&grpc.PokemonInternal{}); got != nil {
		t.Errorf("scanHistoryFromProto(empty) = %v, want nil", got)
	}
}

func TestPopulateInternalGuards(t *testing.T) {
	t.Run("no stored bytes", func(t *testing.T) {
		pokemon := &Pokemon{}
		pokemon.populateInternal()
		if pokemon.scanHistory != nil {
			t.Errorf("scanHistory = %v, want nil", pokemon.scanHistory)
		}
	})

	t.Run("existing history wins", func(t *testing.T) {
		stored, _ := hex.DecodeString(legacyGolbatInternalHex)
		existing := &pokemonScan{Level: 7}
		pokemon := &Pokemon{scanHistory: []*pokemonScan{existing}}
		pokemon.GolbatInternal = stored
		pokemon.populateInternal()
		if len(pokemon.scanHistory) != 1 || pokemon.scanHistory[0] != existing {
			t.Errorf("populateInternal overwrote an already-hydrated history: %v", pokemon.scanHistory)
		}
	})

	t.Run("undecodable bytes clear the history", func(t *testing.T) {
		pokemon := &Pokemon{}
		pokemon.GolbatInternal = []byte{0xff, 0xff, 0xff, 0xff}
		pokemon.populateInternal()
		if pokemon.scanHistory != nil {
			t.Errorf("scanHistory = %v, want nil after a decode failure", pokemon.scanHistory)
		}
	})
}

// TestLocateScansSharePointers pins the property the []*pokemonScan element
// type exists for: the scans handed back by locateScan/locateAllScans are the
// history's own entries, and mutating through them (as the Ditto paths and
// RemoveDittoAuxInfo do) updates the history in place.
func TestLocateScansSharePointers(t *testing.T) {
	stored, _ := hex.DecodeString(legacyGolbatInternalHex)
	pokemon := &Pokemon{}
	pokemon.GolbatInternal = stored

	unboosted, boosted, strong := pokemon.locateAllScans()
	if strong != pokemon.scanHistory[0] {
		t.Errorf("strong scan = %v, want history entry 0", strong)
	}
	if unboosted != pokemon.scanHistory[1] {
		t.Errorf("unboosted scan = %v, want history entry 1", unboosted)
	}
	if boosted != nil {
		t.Errorf("boosted scan = %v, want nil", boosted)
	}

	scan, isBoostedMatches := pokemon.locateScan(false, false)
	if scan != pokemon.scanHistory[1] || !isBoostedMatches {
		t.Errorf("locateScan(false, false) = %v, %v; want history entry 1, true", scan, isBoostedMatches)
	}

	unboosted.RemoveDittoAuxInfo()
	if pokemon.scanHistory[1].Pokemon != 0 || pokemon.scanHistory[1].CellWeather != 0 {
		t.Errorf("mutation through the returned scan did not reach the history: %+v", *pokemon.scanHistory[1])
	}
}

func TestPokemonScanHelpers(t *testing.T) {
	scan := &pokemonScan{Attack: 15, Defense: 14, Stamina: 13, Level: 31}
	if got, want := scan.CompressedIv(), int32(15|14<<4|13<<8); got != want {
		t.Errorf("CompressedIv() = %d, want %d", got, want)
	}
	if !scan.MustBeBoosted() {
		t.Error("MustBeBoosted() = false, want true for level 31")
	}
	if scan.MustBeUnboosted() {
		t.Error("MustBeUnboosted() = true, want false")
	}
	if (&pokemonScan{Level: 5}).MustBeBoosted() {
		t.Error("MustBeBoosted() = true, want false for level 5")
	}
	if !(&pokemonScan{Level: 20, Attack: 3}).MustBeUnboosted() {
		t.Error("MustBeUnboosted() = false, want true for attack 3")
	}
	if scan.MustHaveRerolled(&pokemonScan{}) {
		t.Error("MustHaveRerolled() = true, want false for identical display fields")
	}
	if !scan.MustHaveRerolled(&pokemonScan{Pokemon: 132}) {
		t.Error("MustHaveRerolled() = false, want true for a different species")
	}

	aux := &pokemonScan{CellWeather: 1, Pokemon: 2, Costume: 3, Gender: 4, Form: 5, Confirmed: true,
		Weather: 6, Level: 7, Attack: 8, Defense: 9, Stamina: 10, Strong: true}
	aux.RemoveDittoAuxInfo()
	want := pokemonScan{Weather: 6, Level: 7, Attack: 8, Defense: 9, Stamina: 10, Strong: true}
	if *aux != want {
		t.Errorf("RemoveDittoAuxInfo() left %+v, want %+v", *aux, want)
	}
}

// TestPokemonScanString keeps the Ditto debug logs and error messages readable:
// the generated String() this replaced emitted proto text format, and nil
// scans (checkScans and resetDittoAttributes both pass them) printed "<nil>".
func TestPokemonScanString(t *testing.T) {
	var nilScan *pokemonScan
	if got := nilScan.String(); got != "<nil>" {
		t.Errorf("nil scan String() = %q, want %q", got, "<nil>")
	}
	history := wantScanHistory()
	cases := []struct {
		scan pokemonScan
		want string
	}{
		{history[0], "weather:3 strong:true level:31 attack:15 defense:14 stamina:13 " +
			"cell_weather:5 pokemon:132 costume:7 gender:2 form:952 confirmed:true"},
		{history[1], "level:20 defense:4 stamina:9 cell_weather:1 pokemon:19 gender:1"},
		{pokemonScan{}, ""},
	}
	for _, c := range cases {
		if got := c.scan.String(); got != c.want {
			t.Errorf("String() = %q, want %q", got, c.want)
		}
	}
}
