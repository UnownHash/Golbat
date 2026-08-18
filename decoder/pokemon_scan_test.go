package decoder

import (
	"bytes"
	"encoding/hex"
	"reflect"
	"sync"
	"testing"

	"golbat/grpc"
	"golbat/stats_collector"

	"google.golang.org/protobuf/encoding/protowire"
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
	scans := wantScanHistory()
	history := make([]*pokemonScan, len(scans))
	for i := range scans {
		history[i] = &scans[i]
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

// TestPokemonScanCoversEveryProtoField fails loudly when grpc.PokemonScan
// gains, loses or renames a field without pokemonScan following it. That drift
// is invisible to every other test here: the golden fixture only knows about
// the fields it was built with, so a thirteenth proto field would decode into
// the temporary at the read boundary, never reach pokemonScan, and never be
// written back — silent data loss on a column operators keep.
func TestPokemonScanCoversEveryProtoField(t *testing.T) {
	exportedFields := func(v any) map[string]struct{} {
		names := map[string]struct{}{}
		typ := reflect.TypeOf(v)
		for i := 0; i < typ.NumField(); i++ {
			if field := typ.Field(i); field.IsExported() {
				names[field.Name] = struct{}{}
			}
		}
		return names
	}

	want := exportedFields(grpc.PokemonScan{})
	got := exportedFields(pokemonScan{})
	for name := range want {
		if _, ok := got[name]; !ok {
			t.Errorf("grpc.PokemonScan.%s has no pokemonScan counterpart — add it to pokemonScan "+
				"AND to both converters in pokemon_scan.go, or it is dropped at the boundary", name)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("pokemonScan.%s does not exist on grpc.PokemonScan — it can never round-trip "+
				"through golbat_internal", name)
		}
	}

	// Cross-check against the wire descriptor: exported struct fields and
	// declared proto fields should be the same set of twelve.
	if n := (&grpc.PokemonScan{}).ProtoReflect().Descriptor().Fields().Len(); n != len(want) {
		t.Errorf("grpc.PokemonScan has %d exported Go fields but %d proto fields", len(want), n)
	}
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

// Field numbers used to build golbat_internal bytes that this build cannot
// fully parse. scanHistoryField is PokemonInternal's only declared field;
// the other two are numbers no version of pokemon_internal.proto has used, so
// bytes carrying them land in unknownFields exactly the way a future field
// written by a newer Golbat would.
const (
	scanHistoryField      protowire.Number = 1
	unknownInternalField  protowire.Number = 15
	unknownScanEntryField protowire.Number = 99
)

// internalWithUnknownTopLevelField returns the legacy content plus one field
// on PokemonInternal itself that this build has no definition for.
func internalWithUnknownTopLevelField(t *testing.T) []byte {
	t.Helper()
	stored, err := proto.Marshal(legacyScanHistory())
	if err != nil {
		t.Fatalf("marshal legacy shape: %s", err)
	}
	stored = protowire.AppendTag(stored, unknownInternalField, protowire.VarintType)
	return protowire.AppendVarint(stored, 1)
}

// internalWithUnknownElementField puts the unknown field inside the first
// scan_history element instead, which is the shape a real extension is more
// likely to take: the one time this proto has been extended, the new fields
// went on PokemonScan rather than on PokemonInternal.
func internalWithUnknownElementField(t *testing.T) []byte {
	t.Helper()
	var stored []byte
	for i, entry := range legacyScanHistory().ScanHistory {
		element, err := proto.Marshal(entry)
		if err != nil {
			t.Fatalf("marshal scan element %d: %s", i, err)
		}
		if i == 0 {
			element = protowire.AppendTag(element, unknownScanEntryField, protowire.VarintType)
			element = protowire.AppendVarint(element, 42)
		}
		stored = protowire.AppendTag(stored, scanHistoryField, protowire.BytesType)
		stored = protowire.AppendBytes(stored, element)
	}
	return stored
}

// internalSkipCountingCollector embeds the noop collector so every other
// method keeps its normal behavior, and counts the one call these tests care
// about — the noop discards it, so it cannot be asserted against directly.
type internalSkipCountingCollector struct {
	stats_collector.StatsCollector
	mu      sync.Mutex
	skipped int
}

func (c *internalSkipCountingCollector) IncPokemonInternalRewriteSkipped() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.skipped++
}

func (c *internalSkipCountingCollector) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.skipped
}

func withInternalSkipCountingCollector(t *testing.T) *internalSkipCountingCollector {
	t.Helper()
	fake := &internalSkipCountingCollector{StatsCollector: stats_collector.NewNoopStatsCollector()}
	setStatsCollectorForTest(t, fake)
	return fake
}

// TestRewriteGolbatInternalPreservesUnknownFields is the regression test for
// the data loss the pokemonScan conversion would otherwise introduce. The old
// write boundary marshaled the message it had unmarshaled, so fields written
// by a newer Golbat rode along in unknownFields; rebuilding from pokemonScan
// copies known fields only, so the next encounter would replace a newer node's
// row with a subset of itself.
//
// Both levels are covered because both can carry them: PokemonInternal itself,
// and any one scan_history element.
func TestRewriteGolbatInternalPreservesUnknownFields(t *testing.T) {
	cases := []struct {
		name   string
		stored []byte
	}{
		{"unknown field on PokemonInternal", internalWithUnknownTopLevelField(t)},
		{"unknown field inside a scan_history element", internalWithUnknownElementField(t)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// If this fires, the fixture stopped being a fixture: either
			// pokemon_internal.proto now declares the field number it uses,
			// or protowire built something proto.Unmarshal rejects.
			var decoded grpc.PokemonInternal
			if err := proto.Unmarshal(c.stored, &decoded); err != nil {
				t.Fatalf("fixture does not decode: %s", err)
			}
			if !storedInternalHasUnknownFields(c.stored) {
				t.Fatalf("fixture carries no unknown fields, so it tests nothing")
			}

			fake := withInternalSkipCountingCollector(t)
			original := bytes.Clone(c.stored)

			pokemon := &Pokemon{}
			pokemon.GolbatInternal = c.stored
			pokemon.populateInternal()
			// The known fields still decode; it is only the unknown ones
			// that have nowhere to live.
			assertScanHistory(t, pokemon.scanHistory, wantScanHistory())

			pokemon.rewriteGolbatInternal()
			if !bytes.Equal(pokemon.GolbatInternal, original) {
				t.Errorf("stored bytes were rewritten to %s, want them left at %s",
					hex.EncodeToString(pokemon.GolbatInternal), hex.EncodeToString(original))
			}
			if got := fake.count(); got != 1 {
				t.Errorf("skip count = %d, want 1", got)
			}
			// The Ditto trimming is skipped along with the write, so the
			// in-memory history still matches the bytes on the row.
			if pokemon.scanHistory[0].Pokemon != 132 {
				t.Errorf("strong scan was trimmed despite the refusal: %+v", *pokemon.scanHistory[0])
			}

			// A second encounter on the same row refuses again rather than
			// catching up on the overwrite it declined the first time.
			pokemon.rewriteGolbatInternal()
			if !bytes.Equal(pokemon.GolbatInternal, original) {
				t.Errorf("second save rewrote the stored bytes to %s",
					hex.EncodeToString(pokemon.GolbatInternal))
			}
			if got := fake.count(); got != 2 {
				t.Errorf("skip count after two saves = %d, want 2", got)
			}
		})
	}
}

// TestRewriteGolbatInternalRewritesKnownBytes is the other half: bytes this
// build understands completely are still rewritten, trimming included, so the
// guard costs nothing on the ordinary path.
func TestRewriteGolbatInternalRewritesKnownBytes(t *testing.T) {
	fake := withInternalSkipCountingCollector(t)

	stored, err := hex.DecodeString(legacyGolbatInternalHex)
	if err != nil {
		t.Fatalf("bad golden hex: %s", err)
	}
	pokemon := &Pokemon{}
	pokemon.GolbatInternal = stored
	pokemon.populateInternal()
	pokemon.rewriteGolbatInternal()

	if got := fake.count(); got != 0 {
		t.Errorf("skip count = %d, want 0 for bytes with no unknown fields", got)
	}
	// legacyScanHistory's first entry is the strong scan, so RemoveDittoAuxInfo
	// clears its aux fields — in memory and in the bytes that follow from them.
	if pokemon.scanHistory[0].Pokemon != 0 || pokemon.scanHistory[0].CellWeather != 0 {
		t.Errorf("strong scan kept its Ditto aux info: %+v", *pokemon.scanHistory[0])
	}
	want, err := proto.Marshal(scanHistoryToProto(pokemon.scanHistory))
	if err != nil {
		t.Fatalf("marshal: %s", err)
	}
	if !bytes.Equal(pokemon.GolbatInternal, want) {
		t.Errorf("stored bytes = %s, want the trimmed history's %s",
			hex.EncodeToString(pokemon.GolbatInternal), hex.EncodeToString(want))
	}
}

func TestStoredInternalHasUnknownFields(t *testing.T) {
	known, err := hex.DecodeString(legacyGolbatInternalHex)
	if err != nil {
		t.Fatalf("bad golden hex: %s", err)
	}
	cases := []struct {
		name   string
		stored []byte
		want   bool
	}{
		{"no stored bytes", nil, false},
		{"empty stored bytes", []byte{}, false},
		{"every field known", known, false},
		// Garbage is not a newer binary's data, and populateInternal has
		// already dropped the history for it — refusing forever would strand
		// the row, so it stays rewritable.
		{"undecodable bytes", []byte{0xff, 0xff, 0xff, 0xff}, false},
		{"unknown field on PokemonInternal", internalWithUnknownTopLevelField(t), true},
		{"unknown field inside a scan_history element", internalWithUnknownElementField(t), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := storedInternalHasUnknownFields(c.stored); got != c.want {
				t.Errorf("storedInternalHasUnknownFields = %t, want %t", got, c.want)
			}
		})
	}
}
