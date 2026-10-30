package encounter

import (
	"math"
	"testing"
)

// Computed from the reference implementation and exact, so a failure here is
// a port bug.
func TestKnownEncounter(t *testing.T) {
	got := For(8429844873227918432, false, 0)
	want := Values{Attack: 6, Defense: 0, Stamina: 11, Level: 5, Size: SizeM}
	if got.Values != want {
		t.Fatalf("unboosted: got %+v want %+v", got.Values, want)
	}

	got = For(8429844873227918432, true, 0)
	want = Values{Attack: 8, Defense: 4, Stamina: 6, Level: 10, Size: SizeM}
	if got.Values != want {
		t.Fatalf("boosted: got %+v want %+v", got.Values, want)
	}
}

// Boosted rolls floor at 4 and cap at 15; unboosted span 0..15. Level gains 5.
func TestBoostedRanges(t *testing.T) {
	for id := uint64(1); id < 5000; id++ {
		b := For(id, true, 0)
		if b.Attack < 4 || b.Attack > 15 || b.Defense < 4 || b.Defense > 15 || b.Stamina < 4 || b.Stamina > 15 {
			t.Fatalf("id %d boosted ivs out of range: %+v", id, b)
		}
		if b.Level < 6 || b.Level > 35 {
			t.Fatalf("id %d boosted level out of range: %d", id, b.Level)
		}
		u := For(id, false, 0)
		if u.Attack < 0 || u.Attack > 15 || u.Level < 1 || u.Level > 30 {
			t.Fatalf("id %d unboosted out of range: %+v", id, u)
		}
	}
}

// Every size class, each from a live encounter. This is the test that actually
// pins the Gaussian advance count: M spans ~94% of the roll range, so a wrong
// number of advances still lands on M nearly every time and a M-only assertion
// proves almost nothing. The four tail classes do not forgive.
func TestAllSizeClassesFromLiveEncounters(t *testing.T) {
	cases := []struct {
		encounterID uint64
		want        int32
	}{
		{8528827905241145830, SizeXXS},
		{15474965362462865625, SizeXS},
		{14719367010819113616, SizeM},
		{13251475803505730439, SizeXL},
		{6264748011300813614, SizeXXL},
	}
	for _, c := range cases {
		if got := For(c.encounterID, false, 0).Size; got != c.want {
			t.Errorf("encounter %d: got size %d want %d", c.encounterID, got, c.want)
		}
	}
}

// This encounter's first polar-method pair is rejected, so the size roll comes
// only after a second pair is accepted. A port that assumes a fixed advance
// count passes every other test here and fails this one.
func TestSizeAfterRejectedGaussianPair(t *testing.T) {
	if got := For(5379006789627013438, false, 0).Size; got != SizeXL {
		t.Fatalf("got size %d want %d (XL)", got, SizeXL)
	}
}

// Two more live encounters, both weather boosted.
func TestBoostedLiveEncounters(t *testing.T) {
	cases := []struct {
		encounterID          uint64
		atk, def, sta, level int32
	}{
		{12642008892750441874, 13, 6, 11, 20},
		{13197918358335336224, 4, 6, 5, 7},
	}
	for _, c := range cases {
		got := For(c.encounterID, true, 0)
		want := Values{Attack: c.atk, Defense: c.def, Stamina: c.sta, Level: c.level, Size: SizeM}
		if got.Values != want {
			t.Errorf("encounter %d: got %+v want %+v", c.encounterID, got.Values, want)
		}
	}
}

// Pumpkaboo and Gourgeist display their form's size rather than the rolled one.
func TestFormSizeOverride(t *testing.T) {
	if got := For(0xe5e27e38997354eb, false, 2641); got.Size != SizeXS {
		t.Fatalf("form 2641: got size %d want %d", got.Size, SizeXS)
	}
	base := For(0xe5e27e38997354eb, false, 0)
	withForm := For(0xe5e27e38997354eb, false, 2641)
	if base.Attack != withForm.Attack || base.Level != withForm.Level {
		t.Fatal("form override must change only size, never ivs or level")
	}
}

// Every formSizes key has a hardcoded expectation rather than one read back
// from formSizes: an expectation taken from the map would follow a typo in it
// (an XL override becoming M, say) and pass.
func TestAllFormSizeOverrides(t *testing.T) {
	const encounterID = 0xe5e27e38997354eb
	for _, c := range []struct {
		form int32
		want int32
	}{
		{2641, SizeXS},
		{2642, SizeM},
		{2643, SizeXL},
		{2644, SizeXXL},
		{2645, SizeXS},
		{2646, SizeM},
		{2647, SizeXL},
		{2648, SizeXXL},
	} {
		if got := For(encounterID, false, c.form).Size; got != c.want {
			t.Errorf("form %d: got size %d want %d", c.form, got, c.want)
		}
	}
}

// classifySize's boundaries, pinned exactly. A live encounter's roll almost
// never lands in the narrow band a drifted constant would move (e.g. 0.97 vs
// 0.971), so the live-encounter tests would not notice one.
func TestSizeClassBoundaries(t *testing.T) {
	for _, c := range []struct {
		roll float64
		want int32
	}{
		{0, SizeXXS},
		{0.0039, SizeXXS},
		{0.004, SizeXS},
		{0.0289, SizeXS},
		{0.029, SizeM},
		{0.9705, SizeM}, // in [0.97, 0.971): only M if the boundary is 0.971, not 0.97
		{0.971, SizeXL},
		{0.9955, SizeXL}, // in [0.995, 0.996): only XL if the boundary is 0.996, not 0.995
		{0.996, SizeXXL},
		{0.9999, SizeXXL},
	} {
		if got := classifySize(c.roll); got != c.want {
			t.Errorf("roll %v: got size %d want %d", c.roll, got, c.want)
		}
	}
}

// Two Draws from independent For calls on the same inputs yield the same
// MoveIndices, in both boost states: MoveIndices depends only on the state For
// left in Draw, with no entropy of its own. Both sides run the same code, so
// this cannot detect a wrong draw order or position; the real-encounter
// vectors in TestMoveIndicesMatchesRealEncounter pin those.
func TestMoveIndicesContinuesRatherThanRestarts(t *testing.T) {
	const eid = uint64(8429844873227918432)
	for _, boosted := range []bool{false, true} {
		first := For(eid, boosted, 0)
		second := For(eid, boosted, 0)

		q1, c1 := first.MoveIndices(7, 5)
		q2, c2 := second.MoveIndices(7, 5)
		if q1 != q2 || c1 != c2 {
			t.Errorf("boosted=%v: two Draws of the same encounter disagree: (%d,%d) vs (%d,%d)",
				boosted, q1, c1, q2, c2)
		}
	}
}

// A second MoveIndices call on one Draw panics even with the same pool sizes:
// it is always a programming error, so a same-size repeat is not memoized.
// Fails if MoveIndices returns the first call's indices for a same-size
// repeat instead of panicking.
func TestMoveIndicesPanicsOnRepeatedCallWithSameSizes(t *testing.T) {
	d := For(8429844873227918432, false, 0)
	d.MoveIndices(7, 5)

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected MoveIndices to panic when called again, even with identical pool sizes")
		}
	}()
	d.MoveIndices(7, 5) // deliberately identical pool sizes
	t.Fatal("unreachable: MoveIndices should have panicked before returning")
}

// A second call with different pool sizes panics too: the generator state
// that would answer it is spent, so any returned indices would look valid
// while being wrong for the sizes asked for.
func TestMoveIndicesPanicsOnMismatchedSizes(t *testing.T) {
	d := For(8429844873227918432, false, 0)
	d.MoveIndices(7, 5)

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected MoveIndices to panic when called again with different pool sizes")
		}
	}()
	d.MoveIndices(3, 2) // deliberately different pool sizes
	t.Fatal("unreachable: MoveIndices should have panicked before returning")
}

// TestMoveIndicesMatchesRealEncounter pins Draw.MoveIndices against two real
// PRODUCTION encounters — not synthetic vectors. Both were logged with IVs,
// level and size that matched exactly what For derives from the encounter id,
// so the leading draws are known correct and the move draws are the only
// thing these vectors measure. What they pin is the generator position the
// moves are read from: the size roll is a nextDouble and advances TWICE, and
// reading it as a single-advance nextFloat leaves every later draw one
// position short — matching IVs, level and size throughout, and wrong
// moves.
func TestMoveIndicesMatchesRealEncounter(t *testing.T) {
	for _, c := range []struct {
		name             string
		encounterID      uint64
		boosted          bool
		quickPoolSize    int32
		chargedPoolSize  int32
		wantQuickIndex   int32
		wantChargedIndex int32
	}{
		// quick pool [202, 236], charged pool [50, 131, 90]; observed moves
		// were 202 (index 0 of quick) and 131 (index 1 of charged).
		{
			name:             "boosted, encounter 11091830087922789714",
			encounterID:      11091830087922789714,
			boosted:          true,
			quickPoolSize:    2,
			chargedPoolSize:  3,
			wantQuickIndex:   0,
			wantChargedIndex: 1,
		},
		// quick pool [209, 221], charged pool [95, 42, 127]; observed moves
		// were 209 (index 0 of quick) and 95 (index 0 of charged).
		{
			name:             "unboosted, encounter 15172301940982038039",
			encounterID:      15172301940982038039,
			boosted:          false,
			quickPoolSize:    2,
			chargedPoolSize:  3,
			wantQuickIndex:   0,
			wantChargedIndex: 0,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := For(c.encounterID, c.boosted, 0)
			gotQuick, gotCharged := d.MoveIndices(c.quickPoolSize, c.chargedPoolSize)
			if gotQuick != c.wantQuickIndex || gotCharged != c.wantChargedIndex {
				t.Fatalf("encounter %d: got (%d,%d) want (%d,%d)",
					c.encounterID, gotQuick, gotCharged, c.wantQuickIndex, c.wantChargedIndex)
			}
		})
	}
}

// The same inputs always produce the same indices, so a logged encounter id
// is enough to recompute what checkDerivation compared. Each side gets a fresh
// Draw because MoveIndices is one-shot per Draw.
func TestMoveIndicesIsDeterministicAcrossFreshDraws(t *testing.T) {
	const eid = uint64(13579246813579)
	first := For(eid, true, 0)
	second := For(eid, true, 0)
	q1, c1 := first.MoveIndices(9, 6)
	q2, c2 := second.MoveIndices(9, 6)
	if q1 != q2 || c1 != c2 {
		t.Fatalf("MoveIndices is not stable for a fixed input: (%d,%d) vs (%d,%d)", q1, c1, q2, c2)
	}
}

// Every index falls inside [0, poolSize). Callers index a move-id slice with
// them directly (see decoder.movePoolsFor's callers), so an out-of-range index
// would panic rather than mis-derive a move.
func TestMoveIndicesWithinPoolBounds(t *testing.T) {
	const quickPoolSize, chargedPoolSize = 7, 5
	for id := uint64(1); id < 2000; id++ {
		for _, boosted := range []bool{false, true} {
			d := For(id, boosted, 0)
			q, c := d.MoveIndices(quickPoolSize, chargedPoolSize)
			if q < 0 || q >= quickPoolSize {
				t.Fatalf("id %d boosted=%v: quick index %d out of [0,%d)", id, boosted, q, quickPoolSize)
			}
			if c < 0 || c >= chargedPoolSize {
				t.Fatalf("id %d boosted=%v: charged index %d out of [0,%d)", id, boosted, c, chargedPoolSize)
			}
		}
	}
}

// A single-entry pool always yields index 0: Int(1) takes the power-of-two
// path int32((1*next(31))>>31), and next(31) < 2^31, so the shift is always 0.
// Ditto's pools (quick=[242], charged=[133]) have this shape, so a Ditto's
// derived moves are 242/133 for every encounter id.
func TestMoveIndicesSinglePoolAlwaysZero(t *testing.T) {
	for id := uint64(1); id < 500; id++ {
		d := For(id, id%2 == 0, 0)
		q, c := d.MoveIndices(1, 1)
		if q != 0 || c != 0 {
			t.Fatalf("id %d: a single-entry pool must always index 0, got (%d,%d)", id, q, c)
		}
	}
}

// The roll is deterministic and in [0,1).
func TestDittoCandidateRollIsStableAndInRange(t *testing.T) {
	const eid = uint64(8429844873227918432)

	a := DittoCandidateRoll(eid)
	b := DittoCandidateRoll(eid)
	if a != b {
		t.Fatal("the roll must be deterministic")
	}
	if a < 0 || a >= 1 {
		t.Fatalf("roll out of [0,1): %v", a)
	}
}

// The roll matches a value computed independently of this package, in Python
// from the java.util.Random specification:
//
//	seed    = (encounterID ^ 0x18ADE5CD737B ^ 0x5DEECE66D) & (2^48 - 1)
//	next(b): seed = (seed*0x5DEECE66D + 0xB) & (2^48 - 1); return seed >> (48-b)
//	advance_3 = the third next(24), as a float32 exactly as nextFloat does
//
// For this encounter id advance 3's next(24) is 0x09523c, float32 bits
// 0x3d1523c0. Compared by exact bit pattern, since the reference is an exact
// float32.
func TestDittoCandidateRollMatchesReferenceVector(t *testing.T) {
	const eid = uint64(8429844873227918432)
	want := uint32(0x3d1523c0)

	if got := math.Float32bits(DittoCandidateRoll(eid)); got != want {
		t.Errorf("roll bits = %#08x, want %#08x", got, want)
	}
}

// DittoCandidateRoll (used by decoder's rollWouldMark, which has no Draw) and
// Draw.DittoRoll (used by checkDerivation) must agree, or the two would act on
// and log different rolls for the same encounter. The roll is
// advance 3, before anything weatherBoosted affects, so it is the same in
// both boost states.
func TestDittoCandidateRollMatchesDrawDittoRoll(t *testing.T) {
	const eid = uint64(8429844873227918432)

	roll := DittoCandidateRoll(eid)
	for _, boosted := range []bool{false, true} {
		if got := For(eid, boosted, 0).DittoRoll; got != roll {
			t.Errorf("boosted=%v: Draw.DittoRoll = %v, want %v (DittoCandidateRoll)", boosted, got, roll)
		}
	}
}

// Four real encounters, three from one scanner and one from an unrelated
// second, each observed in game as M where a 24-bit read of the size draw
// gives XS.
//
// Their 24-bit truncation is 486539/2^24 = 0.028999984, just under the 0.029
// XS/M cut; the full 53-bit draw is ~0.02900004, just over it. That single
// draw value is the only one whose 24-bit interval straddles this boundary,
// so encounters that tell the two readings apart are rare.
//
// Production vectors, not constructed inputs: a hand-made roll would pass
// under either reading of the draw.
func TestSizeFromProductionEncounters(t *testing.T) {
	for _, id := range []uint64{
		7442147305026503571,
		3837562679496082718,
		96486674319650176,
		17265459080788604000,
	} {
		for _, boosted := range []bool{false, true} {
			if got := For(id, boosted, 0).Size; got != SizeM {
				t.Errorf("For(%d, boosted=%v).Size = %d, want SizeM (%d)", id, boosted, got, SizeM)
			}
		}
	}
}

// TestSizeRollIsFullDoubleNotTruncatedFloat fails if the size roll is read as
// a 24-bit float. Both readings consume the same number of advances, so no
// later draw can detect the difference -- only the size value itself can.
func TestSizeRollIsFullDoubleNotTruncatedFloat(t *testing.T) {
	const id = 7442147305026503571
	if got := For(id, false, 0).Size; got != SizeM {
		t.Fatalf("Size = %d, want SizeM (%d): a 24-bit read gives 0.028999984 (XS), "+
			"the full draw gives ~0.02900004 (M)", got, SizeM)
	}
}
