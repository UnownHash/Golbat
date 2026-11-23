package decoder

import (
	"testing"
	"time"

	"github.com/golang/geo/s2"
)

// Design §10: pause on agreement, neutrality per pin, the stay-on cell
// count, the scaled excluded-cell cap, and how the index withholds a paused
// modifier's dimension and combines size boundaries.

// confirmLevel feeds ten +5 level deviations, one per second ending at end,
// one per cell, and fails unless the offset confirmed.
func confirmLevel(t *testing.T, l *modifierLearner, end time.Time, cells []s2.CellID) {
	t.Helper()
	var trs []modifierTransition
	for i := range 10 {
		trs = append(trs, l.feed(modCheck(end.Add(time.Duration(i-9)*time.Second), cells[i], withLevelDelta(5)), modZone)...)
	}
	if len(trs) != 2 || trs[1].State != modConfirmed {
		t.Fatalf("transitions %v, want candidate then confirmed", states(trs))
	}
}

// Two agreements running pause a confirmed modifier; it stays listed and
// zone-wide, is not applied, and the next match applies it again. One
// agreement, or two with a match between, do not pause.
func TestModifierPausesOnSecondAgreementRunning(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	cells := modWideCells(12)
	confirmLevel(t, l, modT0, cells)
	ts := modT0
	feed := func(cell s2.CellID, opts ...func(*modifierEvent)) *derivationModifier {
		t.Helper()
		ts = ts.Add(time.Second)
		if trs := l.feed(modCheck(ts, cell, opts...), modZone); len(trs) != 0 {
			t.Fatalf("transitions %v, want none", states(trs))
		}
		return onlyActive(t, l)
	}
	if m := feed(cellD); m.Paused || !modifierApplied(m) {
		t.Fatalf("one agreement: paused %v applied %v, want still applied", m.Paused, modifierApplied(m))
	}
	if m := feed(cells[10], withLevelDelta(5)); m.Paused {
		t.Fatal("paused after a match")
	}
	if m := feed(cellE); m.Paused {
		t.Fatal("an agreement, a match, an agreement: paused, want the match to reset the run")
	}
	m := feed(cellF)
	if !m.Paused || modifierApplied(m) || !m.ZoneWide || m.Status != modConfirmed {
		t.Fatalf("two agreements running: paused %v applied %v zone-wide %v %v, want paused, listed, not applied",
			m.Paused, modifierApplied(m), m.ZoneWide, m.Status)
	}
	// Withheld, not base: a covered record derives nothing.
	idx := buildModifierIndex(l.active())
	if adj := idx.lookup(959, 3272, true, cells[0], modZoneOf); !adj.Suppress || adj.LevelDelta != 0 {
		t.Fatalf("paused level offset: adj %+v, want suppress", adj)
	}
	// An excluded cell keeps the base value: it was disproved there.
	if adj := idx.lookup(959, 3272, true, cellF, modZoneOf); adj.Suppress || adj.LevelDelta != 0 {
		t.Fatalf("paused level offset in an excluded cell: adj %+v, want the zero adjustment", adj)
	}
	// A match without a cell is not in the zone record and lifts nothing.
	if m := feed(0, withLevelDelta(5)); !m.Paused {
		t.Fatal("a match without a cell lifted the pause")
	}
	if m := feed(cells[11], withLevelDelta(5)); m.Paused || !modifierApplied(m) {
		t.Fatalf("after the next match: paused %v applied %v, want applied again", m.Paused, modifierApplied(m))
	}
	if adj := buildModifierIndex(l.active()).lookup(959, 3272, true, cells[0], modZoneOf); adj.Suppress || adj.LevelDelta != 5 {
		t.Fatalf("after the next match: adj %+v, want +5", adj)
	}
}

// Checks without a cell are not in the zone record: neither an agreement
// nor another effect without one counts towards the pause.
func TestModifierPauseCountsRecordChecksOnly(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	confirmLevel(t, l, modT0, modWideCells(10))
	for i, ev := range []modifierEvent{
		modCheck(modT0.Add(time.Second), 0),
		modCheck(modT0.Add(2*time.Second), 0),
		modCheck(modT0.Add(3*time.Second), 0, withLevelDelta(3)),
		modCheck(modT0.Add(4*time.Second), 0, withLevelDelta(3)),
		modCheck(modT0.Add(5*time.Second), cellF),
	} {
		l.feed(ev, modZone)
		for _, m := range l.active() {
			if m.Paused {
				t.Fatalf("event %d paused %v", i, m.Effect)
			}
		}
	}
}

// An unexplained modifier never pauses: it already withholds everything.
func TestModifierUnexplainedNeverPauses(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	for i, c := range modWideCells(10) {
		l.feed(modCheck(modT0.Add(time.Duration(i)*time.Second), c, withIVsDisagree), modZone)
	}
	if m := onlyActive(t, l); m.Type != modUnexplained || m.Status != modConfirmed {
		t.Fatalf("%v %v, want a confirmed unexplained modifier", m.Type, m.Status)
	}
	for i, c := range []s2.CellID{cellD, cellE, cellF} {
		l.feed(modCheck(modT0.Add(time.Duration(20+i)*time.Second), c), modZone)
	}
	m := onlyActive(t, l)
	if m.Paused || !modifierApplied(m) {
		t.Fatalf("after three agreements: paused %v applied %v, want it still suppressing", m.Paused, modifierApplied(m))
	}
	if adj := buildModifierIndex(l.active()).lookup(959, 3272, true, cellA, modZoneOf); !adj.Suppress {
		t.Fatal("unexplained no longer suppresses")
	}
}

// A move agreement on the pinned move is neutral for that pin: it does not
// pause it, however many run. An agreement on another move does.
func TestModifierPinNeutralAgreementNeverPauses(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	confirmPin(t, l, modZone, modT0, modWideCells(10)...)
	neutral := func(ev *modifierEvent) { ev.ObsCharged = 494 } // the base walk gave 494
	for i, c := range []s2.CellID{cellD, cellE, cellF, cellA} {
		l.feed(modCheck(modT0.Add(time.Duration(1+i)*time.Second), c, neutral), modZone)
	}
	if m := onlyActive(t, l); m.Paused || len(m.Excluded) != 0 || !m.ZoneWide {
		t.Fatalf("after four pin-equal agreements: paused %v excluded %v zone-wide %v, want untouched",
			m.Paused, m.Excluded, m.ZoneWide)
	}
	l.feed(modCheck(modT0.Add(10*time.Second), cellD), modZone)
	l.feed(modCheck(modT0.Add(11*time.Second), cellE), modZone)
	if m := onlyActive(t, l); !m.Paused {
		t.Fatal("two agreements on another move did not pause the pin")
	}
}

// Codex round 4 (b): neutral is per pin. While a 268 pin forms at a venue,
// two base 268 agreements in cell D are neutral for it, but they stay in
// the zone record: a 494 pin that then forms starts with D excluded, and
// they count against its share. (They were left out of the record, so 494
// later applied in D.)
func TestModifierNeutralAgreementIsPerPin(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	ts := modT0
	feed := func(cell s2.CellID, opts ...func(*modifierEvent)) {
		ts = ts.Add(time.Second)
		l.feed(modCheck(ts, cell, opts...), modZone)
	}
	// A venue where the derived move is 90 and 268 is seen: a 268 pin.
	derived90 := func(ev *modifierEvent) { ev.ObsCharged, ev.Agree.Charged = 268, dimDisagree }
	for range 3 {
		feed(cellA, derived90)
	}
	if m := modifierWith(t, l, 268); m.Status != modCandidate || m.ZoneWide {
		t.Fatalf("268 pin %v zone-wide %v, want a venue candidate", m.Status, m.ZoneWide)
	}
	feed(cellD) // the base walk gives 268, and 268 is seen
	feed(cellD)
	if m := modifierWith(t, l, 268); m.Paused || len(m.Excluded) != 0 {
		t.Fatalf("268 pin paused %v excluded %v after agreements on its own move, want neither", m.Paused, m.Excluded)
	}
	rec := l.scopes[modScopeKey{modifierScope{PokemonID: 959, Form: 3272, Strong: true}, modZone}].rec[modDimCharged]
	if rec.n != 3 || rec.pinEqual == nil || rec.pinEqual.n != 2 {
		t.Fatalf("zone record holds %d entries (pin-equal ring %+v), want the three 268 deviations and, apart, the two agreements",
			rec.n, rec.pinEqual)
	}
	var trs []modifierTransition
	for _, c := range modWideCells(16) {
		ts = ts.Add(time.Second)
		trs = append(trs, l.feed(modCheck(ts, c, withCharged(494)), modZone)...)
	}
	m := modifierWith(t, l, 494)
	if m.Status != modConfirmed || !m.ZoneWide {
		t.Fatalf("494 pin %v zone-wide %v (%v), want confirmed zone-wide", m.Status, m.ZoneWide, states(trs))
	}
	if m.Covers(modZone, cellD) {
		t.Fatal("494 covers cell D, whose last two checks gave the base move 268")
	}
	if !m.Covers(modZone, cellE) {
		t.Fatal("494 does not cover an unchecked cell")
	}
	// The same agreements, judged for each pin, from either ring: neutral
	// for 268, agreements against 494's share.
	spread := modSpreadCells(14)
	for _, pinEqual := range []bool{false, true} {
		var r modRecord
		agree := func(c s2.CellID, unix int64) {
			if pinEqual {
				r.pushPinEqual(c, 268, unix)
			} else {
				r.push(c, 268, true, unix)
			}
		}
		for i, c := range spread[:9] {
			r.push(c, 494, false, int64(100+i))
		}
		for i, c := range spread[9:12] {
			agree(c, int64(110+i))
		}
		need := modJudge{entries: 10, cells: 8}
		if v := r.judge(494, true, need, 0); !v.on {
			t.Fatalf("pin-equal ring %v: 494 with 9 matches and 3 agreements on 268: %+v, want on (three quarters)", pinEqual, v)
		}
		agree(spread[12], 120)
		if v := r.judge(494, true, need, 0); v.on || v.thin || v.byEffect {
			t.Fatalf("pin-equal ring %v: 494 with 9 matches and 4 agreements on 268: %+v, want off by agreement", pinEqual, v)
		}
		// For 268 itself they are neutral: nine 494s are another effect.
		if v := r.judge(268, true, need, 0); v.on || !v.byEffect {
			t.Fatalf("pin-equal ring %v: 268 judged on nine 494s: %+v, want off by another effect", pinEqual, v)
		}
	}
}

// Pin-equal agreements do not push the pin's matches out of the record: a
// pin whose base walk gives the pinned move in most checks keeps its
// sixteen matches, and stays zone-wide.
func TestModifierPinEqualAgreementsDoNotCrowdRecord(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	cells := modWideCells(10)
	confirmPin(t, l, modZone, modT0, cells...)
	for i := range 40 {
		ts := modT0.Add(time.Duration(1+i) * time.Second)
		ev := modCheck(ts, cells[i%10])
		ev.ObsCharged = 494
		if trs := l.feed(ev, modZone); len(trs) != 0 {
			t.Fatalf("pin-equal agreement %d: %v, want no transition", i, states(trs))
		}
	}
	m := onlyActive(t, l)
	rec := l.scopes[modScopeKey{m.Scope, modZone}].rec[modDimCharged]
	if !m.ZoneWide || m.Paused || rec.n != 10 || rec.pinEqual.n != modRecordSize {
		t.Fatalf("zone-wide %v paused %v, record %d + %d: want the ten matches kept and the agreements apart",
			m.ZoneWide, m.Paused, rec.n, rec.pinEqual.n)
	}
}

// The stay-on cell count is min(start, 8): with start 10 a confirmed
// modifier whose record's matches come from eight cells stays zone-wide
// (it needed ten, more than turning on does).
func TestModifierStayCellsCappedAtEight(t *testing.T) {
	p := defaultModifierParams()
	p.Start, p.Confirm = 10, 10
	if got := modJudgeStay(p); got.cells != 8 || got.entries != 10 || !got.stay {
		t.Fatalf("stay rule with start 10: %+v, want 10 entries from 8 cells", got)
	}
	if got := modJudgeStay(defaultModifierParams()); got.cells != 3 || got.entries != 3 {
		t.Fatalf("stay rule with start 3: %+v, want 3 entries from 3 cells", got)
	}
	l := newModifierLearner(p)
	cells := modWideCells(10)
	confirmLevel(t, l, modT0, cells)
	for i := range 32 { // the record's 16 now come from eight cells
		ts := modT0.Add(time.Duration(1+i) * time.Second)
		if trs := l.feed(modCheck(ts, cells[i%8], withLevelDelta(5)), modZone); len(trs) != 0 {
			t.Fatalf("match %d: %v (%s), want the modifier to stay", i, states(trs), trs[0].Mod.EndReason)
		}
	}
	if m := onlyActive(t, l); m.Status != modConfirmed || !m.ZoneWide {
		t.Fatalf("%v zone-wide %v, want confirmed zone-wide on matches from eight cells", m.Status, m.ZoneWide)
	}
}

// The excluded-cell cap scales with the zone: 256, or 1% of the cells seen
// in the zone when that is more, at most 4096.
func TestModifierExcludedCapScalesWithZone(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	seen := 0
	see := func(n int) {
		for ; seen < n; seen++ {
			l.cellSeen(modZone)
		}
	}
	for _, c := range []struct{ seen, want int }{
		{0, 256}, {25_600, 256}, {30_000, 300}, {100_000, 1000}, {409_600, 4096}, {500_000, 4096},
	} {
		see(c.seen)
		if got := l.excludedCap(modZone); got != c.want {
			t.Errorf("%d cells seen: cap %d, want %d", c.seen, got, c.want)
		}
	}
	if got := l.excludedCap("Europe/Paris"); got != 256 {
		t.Errorf("another zone's cap %d, want 256", got)
	}
	// A cleared cell cache recounts: the count reached stays as the floor.
	l = newModifierLearner(defaultModifierParams())
	seen = 0
	see(50_000)
	l.cellsForgotten()
	see(50_100)
	if got := l.excludedCap(modZone); got != 500 {
		t.Errorf("cap %d after the cache was cleared at 50,000 cells, want 500", got)
	}

	// A candidate in a zone of 40,000 cells seen takes 400 disproving cells
	// and overflows at the 401st.
	l = newModifierLearner(defaultModifierParams())
	seen = 0
	see(40_000)
	for i := range 3 {
		l.feed(modCheck(modT0.Add(time.Duration(i)*time.Second), cellA, withLevelDelta(5)), modZone)
	}
	var trs []modifierTransition
	n := 0
	for len(trs) == 0 && n < 500 {
		trs = l.feed(modCheck(modT0.Add(time.Duration(10+n)*time.Second), modTestCell(40.0+0.02*float64(n), -3.0)), modZone)
		n++
	}
	if n != 401 || len(trs) != 1 || trs[0].Mod.EndReason != "unconfirmed" {
		t.Fatalf("after %d disproving cells: %v, want ended unconfirmed at the 401st", n, states(trs))
	}
}

// The runtime counts each cell it resolves for the learner, once.
func TestDerivationModifiersRuntimeCountsZoneCells(t *testing.T) {
	r := newModifierRuntime(defaultModifierParams(), 1)
	for range 2 {
		for _, c := range []s2.CellID{cellA, cellB, cellC} {
			if z := r.zone(c); z != modZone {
				t.Fatalf("zone %q, want %s", z, modZone)
			}
		}
	}
	r.zone(0)
	if zc := r.learner.zoneCells[modZone]; zc == nil || zc.n != 3 {
		t.Fatalf("cells counted in %s: %+v, want 3", modZone, zc)
	}
	if len(r.learner.zoneCells) != 1 {
		t.Fatalf("zones counted %d, want 1 (cell 0 is no cell)", len(r.learner.zoneCells))
	}
}

// A pause is published at once (the index would apply a value it no longer
// should); the lift is growth, published at most once a second.
func TestDerivationModifiersRuntimePublishesPause(t *testing.T) {
	r := newModifierRuntime(defaultModifierParams(), 64)
	clock := modT0
	r.clock = func() time.Time { return clock }
	withModifierRuntime(t, r)
	cells := modWideCells(11)
	for i := range 10 {
		r.handle(modCheck(modT0.Add(time.Duration(i)*time.Second), cells[i], withCharged(494)))
	}
	lookup := func() derivationAdjust {
		return derivationModifierIndex.Load().lookup(959, 3272, true, cells[0], modZoneOf)
	}
	if adj := lookup(); adj.Charged != 494 || adj.NoCharged {
		t.Fatalf("confirmed pin: adj %+v, want charged 494", adj)
	}
	r.handle(modCheck(modT0.Add(20*time.Second), cellD))
	r.handle(modCheck(modT0.Add(21*time.Second), cellE))
	if adj := lookup(); adj.Charged != 0 || !adj.NoCharged || adj.NoQuick || adj.Suppress {
		t.Fatalf("paused pin: adj %+v, want the charged slot withheld and nothing else", adj)
	}
	if snap := activeDerivationModifiers(); len(snap) != 1 || !snap[0].Paused {
		t.Fatalf("snapshot %+v, want the pin paused", snap)
	}
	r.handle(modCheck(modT0.Add(22*time.Second), cells[10], withCharged(494)))
	if adj := lookup(); !adj.NoCharged {
		t.Fatalf("the lift was published at once: adj %+v", adj)
	}
	clock = clock.Add(deriveModifiersEvidenceInterval)
	r.handle(modCheck(modT0.Add(23*time.Second), cellA, withSpecies(1, 0)))
	if adj := lookup(); adj.Charged != 494 || adj.NoCharged {
		t.Fatalf("after the interval: adj %+v, want charged 494 again", adj)
	}
}

// A paused modifier's dimension is withheld through the derive path: a
// paused pin leaves its slot absent (the other derived), a paused size
// boundary the size, a paused level offset everything.
func TestDerivationAdjustPausedWithholds(t *testing.T) {
	withDeriveIv(t, true)
	pinMoveData(t)
	paused := func(m *derivationModifier) *derivationModifier {
		m.Paused = true
		return m
	}

	withModifierIndex(t, paused(pinMod(adjZoneA, modSlotCharged, 494)))
	p := adjRecord(1, adjLatA, adjLonA, false)
	p.deriveIvIfAbsent()
	if p.Move2.Valid || !p.Move1.Valid || !p.AtkIv.Valid || !p.Size.Valid {
		t.Errorf("paused charged pin: move1 %v move2 %v atk %v size %v, want only move2 absent",
			p.Move1, p.Move2, p.AtkIv, p.Size)
	}
	other := adjRecord(1, adjLatB, adjLonB, false) // another zone: base moves
	other.deriveIvIfAbsent()
	if !other.Move2.Valid {
		t.Error("paused pin withheld the charged move outside its zone")
	}

	withModifierIndex(t, paused(pinMod(adjZoneA, modSlotQuick, 300)))
	p = adjRecord(1, adjLatA, adjLonA, false)
	p.deriveIvIfAbsent()
	if p.Move1.Valid || !p.Move2.Valid {
		t.Errorf("paused quick pin: move1 %v move2 %v, want only move1 absent", p.Move1, p.Move2)
	}

	withModifierIndex(t, paused(sizeMod("1/2", sizeBoundary{Lo: 0.0119, Hi: 0.0121, HasLo: true, HasHi: true})))
	p = adjRecord(1, adjLatA, adjLonA, false)
	p.deriveIvIfAbsent()
	if p.Size.Valid || !p.AtkIv.Valid || !p.Move1.Valid {
		t.Errorf("paused size boundary: size %v atk %v move1 %v, want only size absent", p.Size, p.AtkIv, p.Move1)
	}

	withModifierIndex(t, paused(levelMod(modifierScope{PokemonID: 1}, 5, adjZoneA, nil)))
	p = adjRecord(1, adjLatA, adjLonA, false)
	p.deriveIvIfAbsent()
	if p.AtkIv.Valid || p.Level.Valid || p.Move1.Valid || p.Size.Valid {
		t.Errorf("paused level offset: atk %v level %v move1 %v size %v, want nothing derived",
			p.AtkIv, p.Level, p.Move1, p.Size)
	}
	if n := testing.AllocsPerRun(100, func() { _ = p.modifierAdjust() }); n != 0 {
		t.Errorf("%v allocs looking up a paused modifier, want 0", n)
	}
}

// The index combines a scope's size boundaries per cell: a boundary that
// does not cover the cell leaves its cut the base one, and when an applied
// interval reaches past a neighbouring cut the size is withheld there.
func TestDerivationAdjustSizeBoundariesCombine(t *testing.T) {
	low := sizeMod("1/2", sizeBoundary{Lo: 0.0119, Hi: 0.0121, HasLo: true, HasHi: true})
	mid := sizeMod("2/3", sizeBoundary{Lo: 0.0369, Hi: 0.0371, HasLo: true, HasHi: true})
	mid.Excluded = excludedSet(adjCellA)
	idx := buildModifierIndex([]*derivationModifier{low, mid})
	class := func(idx *modifierIndex, cell s2.CellID, roll float64) (int32, bool) {
		adj := idx.lookup(1, 0, false, cell, func(s2.CellID) string { return adjZoneA })
		return adj.sizeClass(roll, false, 0, 3)
	}
	type want struct {
		class int32
		ok    bool
	}
	for _, tc := range []struct {
		cell s2.CellID
		roll float64
		want want
	}{
		{adjCellFar, 0.008, want{1, true}}, {adjCellFar, 0.012, want{0, false}}, {adjCellFar, 0.033, want{2, true}},
		{adjCellFar, 0.037, want{0, false}}, {adjCellFar, 0.5, want{3, true}},
		// 2/3 was disproved in cell A: its base cut (0.029) holds there.
		{adjCellA, 0.008, want{1, true}}, {adjCellA, 0.02, want{2, true}}, {adjCellA, 0.033, want{3, true}},
		{adjCellA, 0.037, want{3, true}},
	} {
		if got, ok := class(idx, tc.cell, tc.roll); got != tc.want.class || ok != tc.want.ok {
			t.Errorf("cell %v roll %v = (%d, %v), want %+v", tc.cell, tc.roll, got, ok, tc.want)
		}
	}

	// 1/2 learned at 0.05–0.06 with 2/3 at its base 0.029: not one set of
	// cuts, so no roll gets a class.
	far := sizeMod("1/2", sizeBoundary{Lo: 0.05, Hi: 0.06, HasLo: true, HasHi: true})
	idx = buildModifierIndex([]*derivationModifier{far})
	for _, roll := range []float64{0.001, 0.02, 0.055, 0.5, 0.999} {
		if got, ok := class(idx, adjCellA, roll); ok {
			t.Errorf("1/2 past the base 2/3 cut: roll %v = (%d, true), want withheld", roll, got)
		}
	}
	// Two intervals that overlap.
	idx = buildModifierIndex([]*derivationModifier{
		sizeMod("1/2", sizeBoundary{Lo: 0.02, Hi: 0.04, HasLo: true, HasHi: true}),
		sizeMod("2/3", sizeBoundary{Lo: 0.03, Hi: 0.05, HasLo: true, HasHi: true})})
	for _, roll := range []float64{0.001, 0.035, 0.5} {
		if got, ok := class(idx, adjCellA, roll); ok {
			t.Errorf("overlapping intervals: roll %v = (%d, true), want withheld", roll, got)
		}
	}
	// A boundary moved down past the cut before it.
	idx = buildModifierIndex([]*derivationModifier{sizeMod("2/3", sizeBoundary{Lo: 0.001, Hi: 0.002, HasLo: true, HasHi: true})})
	if got, ok := class(idx, adjCellA, 0.5); ok {
		t.Errorf("2/3 under the base 1/2 cut: (%d, true), want withheld", got)
	}
	// An unbounded end reaches to the neighbouring cut and withholds nothing
	// beyond its own side.
	idx = buildModifierIndex([]*derivationModifier{sizeMod("1/2", sizeBoundary{Lo: 0.01, HasLo: true})})
	if got, ok := class(idx, adjCellA, 0.008); !ok || got != 1 {
		t.Errorf("under an open interval's lo: (%d, %v), want 1", got, ok)
	}
	if got, ok := class(idx, adjCellA, 0.5); ok {
		t.Errorf("over an open interval's lo: (%d, true), want absent", got)
	}
	// A paused boundary withholds the size wherever it covers.
	mid.Paused = true
	idx = buildModifierIndex([]*derivationModifier{low, mid})
	if got, ok := class(idx, adjCellFar, 0.5); ok {
		t.Errorf("paused 2/3: (%d, true), want withheld", got)
	}
	if got, ok := class(idx, adjCellA, 0.5); !ok || got != 3 {
		t.Errorf("paused 2/3 in its excluded cell: (%d, %v), want 3", got, ok)
	}
	adj := idx.lookup(1, 0, false, adjCellFar, func(s2.CellID) string { return adjZoneA })
	if n := testing.AllocsPerRun(100, func() { _, _ = adj.sizeClass(0.5, false, 0, 3) }); n != 0 {
		t.Errorf("sizeClass allocates %v, want 0", n)
	}
}

func TestApiDerivationModifiersPaused(t *testing.T) {
	t.Cleanup(func() { derivationModifierSnapshot.Store(nil) })
	start := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	pin := pinMod(adjZoneA, modSlotCharged, 494)
	pin.Start, pin.Paused = start, true
	size := sizeMod("2/3", sizeBoundary{Lo: 0.036, Hi: 0.038, HasLo: true, HasHi: true})
	size.Start = start
	storeModifierSnapshot([]*derivationModifier{pin, size})
	res := GetApiDerivationModifiers(start)
	if len(res.Modifiers) != 2 {
		t.Fatalf("got %+v", res.Modifiers)
	}
	p, s := res.Modifiers[0], res.Modifiers[1]
	if !p.Paused || p.Applied || p.Status != "confirmed" || !p.Region.ZoneWide {
		t.Errorf("paused pin: %+v, want paused, listed confirmed zone-wide, not applied", p)
	}
	if s.ID != "2026-11-15-size_cutoffs-1-0-2/3" || s.Paused || !s.Applied {
		t.Errorf("size boundary: %+v, want id ending in its boundary, applied", s)
	}
	if b, ok := s.Effect["boundaries"].(map[string][2]*float64); !ok || len(b) != 1 || *b["2/3"][0] != 0.036 || *b["2/3"][1] != 0.038 {
		t.Errorf("size effect %v, want its one boundary", s.Effect)
	}
}
