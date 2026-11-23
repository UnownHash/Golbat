package decoder

import (
	"reflect"
	"testing"
	"time"

	"github.com/golang/geo/s2"
)

// Tests of design §9: only spread zone-wide coverage applies, record entries
// age one by one, a disproven cell stays excluded, and venue cells are
// evidence.

// modStraddleVenue is two adjacent level-14 cells on either side of a
// level-10 cell edge: a venue ~600 m across under two level-10 parents.
func modStraddleVenue(t *testing.T) [2]s2.CellID {
	t.Helper()
	p := cellA.Parent(10)
	for c := p.ChildBeginAtLevel(deriveEventsCellLevel); c != p.ChildEndAtLevel(deriveEventsCellLevel); c = c.Next() {
		for _, n := range c.EdgeNeighbors() {
			if n.Parent(10) != p {
				return [2]s2.CellID{c, n}
			}
		}
	}
	t.Fatal("no level-14 cell on the level-10 edge")
	return [2]s2.CellID{}
}

// A two-cell venue straddling a level-10 edge is not spread: it never goes
// zone-wide and never confirms.
func TestModifierVenueAcrossLevel10EdgeNeverZoneWide(t *testing.T) {
	venue := modStraddleVenue(t)
	l := newModifierLearner(defaultModifierParams())
	var trs []modifierTransition
	for i := range 10 {
		ts := modT0.Add(time.Duration(i) * 10 * time.Second)
		trs = append(trs, l.feed(modCheck(ts, venue[i%2], withLevelDelta(5)), modZone)...)
		if i%3 == 0 { // the base level elsewhere in the zone
			trs = append(trs, l.feed(modCheck(ts.Add(time.Second), modSpreadCells(4)[i/3]), modZone)...)
		}
	}
	if len(trs) != 1 || trs[0].State != modCandidate {
		t.Fatalf("transitions %v, want only the candidate", states(trs))
	}
	m := onlyActive(t, l)
	if m.ZoneWide || m.Covers(modZone, unseenCell) || m.Covers(modZone, venue[0]) {
		t.Fatalf("zone-wide %v: a two-cell venue must cover nothing", m.ZoneWide)
	}
	if len(m.Cells) != 2 {
		t.Fatalf("cells %v, want the two venue cells listed", m.Cells)
	}
}

// Zone-wide needs matches from 8 distinct level-14 cells spanning 10 km.
func TestModifierZoneWideNeedsSpread(t *testing.T) {
	far := modSpreadCells(8) // ~55 km apart
	near := make([]s2.CellID, 8)
	for i := range near {
		near[i] = modVenueCell14(cellA, i) // one level-10 cell: under 10 km
	}
	for _, c := range []struct {
		name  string
		cells []s2.CellID
		on    bool
	}{
		{"seven far cells", far[:7], false},
		{"eight cells within 10 km", near, false},
		{"eight far cells", far, true},
	} {
		l := newModifierLearner(defaultModifierParams())
		for i := range 16 {
			l.feed(modCheck(modT0.Add(time.Duration(i)*time.Second), c.cells[i%len(c.cells)], withCharged(494)), modZone)
		}
		if m := onlyActive(t, l); m.ZoneWide != c.on || (m.Status == modConfirmed) != c.on {
			t.Errorf("%s: zone-wide %v status %v, want zone-wide and confirmed %v", c.name, m.ZoneWide, m.Status, c.on)
		}
	}
}

// spreadPin feeds 10 charged-494 deviations ending at end, each in another
// of ten cells ~55 km apart, and fails unless the pin confirmed zone-wide.
func spreadPin(t *testing.T, l *modifierLearner, end time.Time) {
	t.Helper()
	confirmPin(t, l, modZone, end, modSpreadCells(10)...)
	if m := modifierWith(t, l, 494); !m.ZoneWide {
		t.Fatal("spread pin not zone-wide")
	}
}

// A cell whose latest check was not the effect stays excluded after its
// entry has left the 16-entry zone record, until a match in that cell.
func TestModifierDisproofLasts(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	spreadPin(t, l, modT0)
	l.feed(modCheck(modT0.Add(time.Second), cellD), modZone)
	if modifierWith(t, l, 494).Covers(modZone, cellD) {
		t.Fatal("cell D covered after its agreement")
	}
	cells := modSpreadCells(16)
	for i := range 16 {
		l.feed(modCheck(modT0.Add(time.Duration(2+i)*time.Second), cells[i], withCharged(494)), modZone)
	}
	if m := modifierWith(t, l, 494); !m.ZoneWide || m.Covers(modZone, cellD) || !m.Covers(modZone, unseenCell) {
		t.Fatalf("zone-wide %v covers D %v: want zone-wide, cell D still excluded after 16 matches elsewhere",
			m.ZoneWide, m.Covers(modZone, cellD))
	}
	l.feed(modCheck(modT0.Add(20*time.Second), cellD, withCharged(494)), modZone)
	if !modifierWith(t, l, 494).Covers(modZone, cellD) {
		t.Fatal("a match in cell D did not readmit it")
	}
}

// Past 256 excluded cells a modifier is no longer zone-wide, whatever the
// record's majority.
func TestModifierExcludedOverflowEndsZoneWide(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	spreadPin(t, l, modT0)
	spread := modSpreadCells(16)
	ts := modT0
	var ended []string
	fed := 0
	for i := 0; i < 300 && len(ended) == 0; i++ {
		evs := []modifierEvent{
			modCheck(ts.Add(time.Second), modTestCell(40.0+0.02*float64(i), -3.0)),
			modCheck(ts.Add(2*time.Second), spread[(2*i)%16], withCharged(494)),
			modCheck(ts.Add(3*time.Second), spread[(2*i+1)%16], withCharged(494)),
		}
		ts = ts.Add(3 * time.Second)
		for _, tr := range feedAll(l, modZone, evs...) {
			if tr.State == modEnded {
				ended = append(ended, tr.Mod.EndReason)
			}
		}
		fed = i + 1
	}
	if len(ended) != 1 || ended[0] != "agreement" || fed != 257 {
		t.Fatalf("ended %v after %d disproven cells, want by agreement at the 257th", ended, fed)
	}
}

// Record entries age one by one: a zone-wide modifier whose matches are
// older than unseen is no longer zone-wide, though a sparse match kept the
// record in use and nothing agreed.
func TestModifierZoneWideMatchesAge(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	cells := modSpreadCells(12)
	for i := range 10 {
		l.feed(modCheck(modT0.Add(time.Duration(i-9)*time.Second), cells[i], withLevelDelta(5)), modZone)
	}
	if m := onlyActive(t, l); m.Status != modConfirmed || !m.ZoneWide {
		t.Fatalf("status %v zone-wide %v, want a confirmed zone-wide +5", m.Status, m.ZoneWide)
	}
	if trs := l.feed(modCheck(modT0.Add(50*time.Minute), cells[10], withLevelDelta(5)), modZone); len(trs) != 0 {
		t.Fatalf("a match at +50m: %v, want no transition", states(trs))
	}
	trs := l.sweep(modT0.Add(62 * time.Minute))
	if len(trs) != 1 || trs[0].State != modEnded || trs[0].Mod.EndReason != "unseen" || trs[0].Mod.ZoneWide {
		t.Fatalf("sweep at +62m with one fresh match: %v, want ended unseen", states(trs))
	}
}

// sizeSpreadEvent returns a learner with two confirmed zone-wide Rattata
// size modifiers, 1/2 = [0.008, 0.010] and 2/3 = [0.033, ..): XXS seen up
// to 0.008 and XS from 0.010 to 0.033, twelve of each over twelve cells
// ~55 km apart, one roll every two minutes ending at end (the last an XS at
// 0.010, which the base cuts give too).
func sizeSpreadEvent(t *testing.T, end time.Time) *modifierLearner {
	t.Helper()
	l := newModifierLearner(defaultModifierParams())
	cells := modSpreadCells(12)
	for i := range 24 {
		ts := end.Add(time.Duration(i-23) * 2 * time.Minute)
		opt := withSize(0.008, 1)
		if i%2 == 1 {
			opt = withSize(0.033, 2)
		}
		l.feed(modCheck(ts, cells[i/2], opt), modZone)
	}
	l.feed(modCheck(end, cells[0], withSize(0.010, 2)), modZone)
	for cut, want := range map[string]sizeBoundary{
		"1/2": {Lo: 0.008, Hi: 0.010, HasLo: true, HasHi: true},
		"2/3": {Lo: 0.033, HasLo: true},
	} {
		m := sizeActive(l, cut)
		if m == nil || m.Status != modConfirmed || !m.ZoneWide || m.Interval != want {
			t.Fatalf("boundary %s: %+v, want confirmed zone-wide %+v", cut, m, want)
		}
	}
	if n := len(l.active()); n != 2 {
		t.Fatalf("%d active modifiers, want the two boundaries", n)
	}
	return l
}

// A zone-wide size modifier ends within unseen of its event though a sparse
// informative base roll (one per 50 minutes) keeps refreshing its record:
// its old matches no longer hold the majority once aged.
func TestModifierSizeEndsAfterEventWithSparseBaseRolls(t *testing.T) {
	l := sizeSpreadEvent(t, modT0)
	var ended time.Time
	for min := 1; min <= 120; min++ {
		ts := modT0.Add(time.Duration(min) * time.Minute)
		var trs []modifierTransition
		if min%50 == 0 { // a base roll in the disputed band, in another cell
			trs = l.feed(modCheck(ts, modTestCell(45.0+0.01*float64(min), 7.0), withSize(0.006, 2)), modZone)
		}
		// Uninformative rolls and sightings of the scope keep arriving.
		trs = append(trs, l.feed(modCheck(ts.Add(time.Second), modTestCell(44.0+0.01*float64(min), 7.0), withSize(0.5, 3)), modZone)...)
		trs = append(trs, l.feed(modCheck(ts.Add(2*time.Second), unseenCell, withSize(0.5, 3), asSighting), modZone)...)
		trs = append(trs, l.sweep(ts.Add(3*time.Second))...)
		for _, tr := range trs {
			if tr.State == modEnded && tr.Mod.Status == modEnded && tr.Mod.EndReason != "unconfirmed" {
				ended = ts // the later of the two boundaries
			}
		}
	}
	if ended.IsZero() || ended.After(modT0.Add(62*time.Minute)) {
		t.Fatalf("last size modifier ended at %v, want within unseen of its last match at %v", ended, modT0)
	}
	for _, m := range l.active() {
		if m.Status == modConfirmed {
			t.Fatalf("a confirmed size modifier is still published two hours after the event: %s %+v", m.Effect.Cut, m.Interval)
		}
	}
}

// Codex round 4 (c): a cell excluded from a boundary keeps its disproving
// roll and is readmitted only by a roll there that contradicts it. With the
// zone-wide 2/3 cut at 0.033 or above, an M at 0.032 in cell D disproves it
// there. An XS at 0.030 is a match, but both rolls fit a cut at 0.031 local
// to D, so D stays excluded (it was readmitted, and 0.032 there made XS).
// An XS at or above 0.032 contradicts the M and readmits D. A match across
// another boundary says nothing: that is another modifier.
func TestModifierSizeReadmissionNeedsResolvingRoll(t *testing.T) {
	l := sizeSpreadEvent(t, modT0)
	ts := modT0
	feed := func(roll float64, obs uint8) {
		t.Helper()
		ts = ts.Add(time.Second)
		if trs := l.feed(modCheck(ts, cellD, withSize(roll, obs)), modZone); len(trs) != 0 {
			t.Fatalf("roll %v class %d: %v, want no transition", roll, obs, states(trs))
		}
	}
	covers := func() bool { return sizeActive(l, "2/3").Covers(modZone, cellD) }
	feed(0.032, 3) // an M under the moved 2/3 cut: the base cut holds in D
	if covers() {
		t.Fatal("cell D covered after a base roll in the 2/3 band")
	}
	if got, ok := sizeIndexClass(l, cellD, 0.032); !ok || got != 3 {
		t.Fatalf("0.032 in D = (%d, %v), want the base class 3", got, ok)
	}
	feed(0.008, 1) // a match across 1/2 says nothing about 2/3
	if covers() {
		t.Fatal("a match across 1/2 readmitted cell D for 2/3")
	}
	feed(0.030, 2) // a match across 2/3, on the near side of the disproving roll
	if covers() {
		t.Fatal("an XS at 0.030 readmitted cell D: with the M at 0.032 it fits a local cut at 0.031, and 0.032 there would be made XS")
	}
	if got, ok := sizeIndexClass(l, cellD, 0.032); !ok || got != 3 {
		t.Fatalf("0.032 in D after the 0.030 XS = (%d, %v), want the base class 3", got, ok)
	}
	if !sizeActive(l, "1/2").Covers(modZone, cellD) {
		t.Fatal("the 2/3 disproof excluded cell D from 1/2")
	}
	feed(0.0325, 2) // an XS above the disproving M: the M is contradicted
	if !covers() {
		t.Fatal("an XS at 0.0325, above the disproving 0.032, did not readmit cell D")
	}
	if got, ok := sizeIndexClass(l, cellD, 0.032); !ok || got != 2 {
		t.Fatalf("0.032 in D once readmitted = (%d, %v), want 2", got, ok)
	}
}

// Codex round 4 (a): boundaries age apart. With both confirmed zone-wide,
// rolls across 1/2 keep arriving and none across 2/3: 2/3 ends within
// unseen of its last roll (it stayed listed for 90 minutes, on the shared
// record's freshness) and 1/2 goes on.
func TestModifierSizeBoundaryAgesIndependently(t *testing.T) {
	l := sizeSpreadEvent(t, modT0) // the last 2/3 roll is at modT0
	cells := modSpreadCells(12)
	var ended time.Time
	for min := 1; min <= 90; min++ {
		ts := modT0.Add(time.Duration(min) * time.Minute)
		trs := l.feed(modCheck(ts, cells[min%12], withSize(0.008, 1)), modZone)
		for _, tr := range append(trs, l.sweep(ts.Add(time.Second))...) {
			if tr.State != modEnded || tr.Mod.Effect.Cut != "2/3" || tr.Mod.EndReason != "unseen" {
				t.Fatalf("+%dm: %v %s %s, want only 2/3 ending unseen", min, tr.State, tr.Mod.Effect.Cut, tr.Mod.EndReason)
			}
			ended = ts
		}
		if min == 30 && sizeActive(l, "2/3") == nil {
			t.Fatal("2/3 ended half an hour after its last roll")
		}
	}
	if ended.IsZero() || ended.After(modT0.Add(62*time.Minute)) {
		t.Fatalf("2/3 ended at %v, want within unseen of its last roll at %v", ended, modT0)
	}
	if m := sizeActive(l, "1/2"); m == nil || m.Status != modConfirmed || !m.ZoneWide {
		t.Fatalf("1/2 = %+v, want it still confirmed zone-wide", m)
	}
	if got, ok := sizeIndexClass(l, cells[0], 0.031); !ok || got != 3 {
		t.Fatalf("0.031 after 2/3 ended = (%d, %v), want the base class 3", got, ok)
	}
}

// A modifier covered only by venue cells is listed, never confirmed and so
// never in the index's applied entries.
func TestModifierVenueOnlyNeverApplied(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	var trs []modifierTransition
	for i := range 20 {
		ts := modT0.Add(time.Duration(i) * time.Second)
		trs = append(trs, l.feed(modCheck(ts, []s2.CellID{cellA, cellA2}[i%2], withLevelDelta(5)), modZone)...)
	}
	if len(trs) != 1 || trs[0].State != modCandidate {
		t.Fatalf("transitions %v, want only the candidate", states(trs))
	}
	m := onlyActive(t, l)
	if m.Status != modCandidate || len(m.Cells) != 2 || m.Covers(modZone, cellA) {
		t.Fatalf("status %v cells %v covers A %v, want a candidate listing two venue cells and covering nothing",
			m.Status, m.Cells, m.Covers(modZone, cellA))
	}
	if idx := buildModifierIndex(l.active()); idx != nil && len(idx.confirmed) != 0 {
		t.Fatalf("index applies %d scopes, want none", len(idx.confirmed))
	}
}

// A candidate has 2 × unseen from its start to confirm; then it ends
// unconfirmed, with its matches and venue counts, and a new one forms from
// fresh deviations. Scattered noise cannot bank matches indefinitely.
func TestModifierCandidateDeadlineTwiceUnseen(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	var trs []modifierTransition
	for i := range 24 { // a mismatch every five minutes, each in another cell
		trs = append(trs, l.feed(modCheck(modT0.Add(time.Duration(i)*5*time.Minute), modVenueCell14(cellA, i), withIVsDisagree), modZone)...)
	}
	if len(trs) != 1 || trs[0].State != modCandidate {
		t.Fatalf("first 115 minutes: %v, want only the candidate", states(trs))
	}
	trs = l.feed(modCheck(modT0.Add(125*time.Minute), modVenueCell14(cellA, 24), withIVsDisagree), modZone)
	if len(trs) != 1 || trs[0].State != modEnded || trs[0].Mod.EndReason != "unconfirmed" || trs[0].Mod.Observations != 24 {
		t.Fatalf("after 2 × unseen: %v, want the candidate ended unconfirmed with 24 observations", states(trs))
	}
	if len(l.active()) != 0 {
		t.Fatal("the mismatch that found the candidate ended must start a new cycle as pending")
	}
	trs = feedAll(l, modZone,
		modCheck(modT0.Add(130*time.Minute), modVenueCell14(cellA, 25), withIVsDisagree),
		modCheck(modT0.Add(135*time.Minute), modVenueCell14(cellA, 26), withIVsDisagree))
	if len(trs) != 1 || trs[0].State != modCandidate || trs[0].Mod.Observations != 3 {
		t.Fatalf("fresh deviations: %v, want a new candidate with 3 observations", states(trs))
	}
}

// The learner keeps no encounter-id map: the decode site's first-encounter
// gate is the only dedupe (TestAddEncounterPokemonFeedsLearnerOncePerSpawn).
func TestModifierLearnerHasNoIDMap(t *testing.T) {
	if _, ok := reflect.TypeOf(modifierEvent{}).FieldByName("EncounterID"); ok {
		t.Error("modifierEvent carries an encounter id")
	}
	if _, ok := reflect.TypeOf(modifierLearner{}).FieldByName("checked"); ok {
		t.Error("modifierLearner keeps a checked-id map")
	}
}

// A confirmed zone-wide modifier stays on with fewer fresh entries than it
// needed to turn on (start of them, from start cells still 10 km apart); a
// candidate gets no such allowance.
func TestModifierZoneWideHysteresis(t *testing.T) {
	far := modSpreadCells(13)
	sparse := func(l *modifierLearner) {
		for i := range 3 {
			l.feed(modCheck(modT0.Add(time.Duration(30+10*i)*time.Minute), far[10+i], withCharged(494)), modZone)
		}
	}
	l := newModifierLearner(defaultModifierParams())
	spreadPin(t, l, modT0)
	sparse(l)
	if trs := l.sweep(modT0.Add(62 * time.Minute)); len(trs) != 0 || !onlyActive(t, l).ZoneWide {
		t.Fatalf("three fresh matches from three spread cells: %v, want the confirmed pin still zone-wide", states(trs))
	}
	trs := l.sweep(modT0.Add(92 * time.Minute)) // two fresh matches left
	if len(trs) != 1 || trs[0].State != modEnded || trs[0].Mod.EndReason != "unseen" {
		t.Fatalf("two fresh matches: %v, want ended unseen", states(trs))
	}

	// Three fresh matches in neighbouring cells are not spread.
	l = newModifierLearner(defaultModifierParams())
	spreadPin(t, l, modT0)
	for i := range 3 {
		l.feed(modCheck(modT0.Add(time.Duration(30+10*i)*time.Minute), modVenueCell14(cellA, i), withCharged(494)), modZone)
	}
	if trs := l.sweep(modT0.Add(62 * time.Minute)); len(trs) != 1 || trs[0].State != modEnded {
		t.Fatalf("three fresh matches within a venue: %v, want ended", states(trs))
	}

	// A candidate gets no such allowance (here the same state, made a
	// candidate by hand: one that is zone-wide and has its matches confirms).
	l = newModifierLearner(defaultModifierParams())
	spreadPin(t, l, modT0)
	sparse(l)
	l.activeTracks[0].mod.Status = modCandidate
	if trs := l.sweep(modT0.Add(62 * time.Minute)); len(trs) != 0 || onlyActive(t, l).ZoneWide {
		t.Fatalf("a candidate with three fresh matches: %v, want it no longer zone-wide", states(trs))
	}
}

// A candidate whose excluded cells overflow ends unconfirmed at once, and
// the next matches start a new one: it does not sit out its deadline.
func TestModifierOverflowedCandidateRestarts(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	ts := modT0
	feed := func(cell s2.CellID, opts ...func(*modifierEvent)) []modifierTransition {
		ts = ts.Add(time.Second)
		return l.feed(modCheck(ts, cell, opts...), modZone)
	}
	for range 3 {
		feed(cellA, withLevelDelta(5))
	}
	var trs []modifierTransition
	n := 0
	for len(trs) == 0 && n < 300 {
		trs = feed(modTestCell(40.0+0.02*float64(n), -3.0))
		n++
	}
	if n != 257 || len(trs) != 1 || trs[0].State != modEnded || trs[0].Mod.EndReason != "unconfirmed" {
		t.Fatalf("after %d disproving cells: %v, want ended unconfirmed at the 257th", n, states(trs))
	}
	trs = nil
	for _, c := range modSpreadCells(12) { // three quarters of the record's 16
		trs = append(trs, feed(c, withLevelDelta(5))...)
	}
	if len(trs) != 2 || trs[0].State != modCandidate || trs[1].State != modConfirmed {
		t.Fatalf("fresh matches: %v, want a new candidate, then confirmed", states(trs))
	}
	if n := len(trs[0].Mod.Excluded); n == 0 || n > modRecordSize {
		t.Fatalf("new candidate starts with %d excluded cells, want those of the record only", n)
	}
}

// The end reason comes from the latest fresh entry that says anything: a
// pin-equal agreement after another effect leaves it contradicted.
func TestModifierVerdictByEffectSkipsNeutral(t *testing.T) {
	var r modRecord
	cells := modSpreadCells(12)
	for i := range 4 {
		r.push(cells[i], 494, false, 100)
	}
	for i := range 6 {
		r.push(cells[4+i], 300, false, 100)
	}
	r.push(cells[10], 494, true, 100) // the base walk gave the pinned move
	if v := r.judge(494, true, modJudge{entries: 10, cells: 8}, 100); v.on || !v.byEffect || v.lost() != "contradicted" {
		t.Fatalf("verdict %+v, want off by another effect", v)
	}
}

// A boundary's interval accepts a roll it holds with, and tells a roll it
// refuses that the base cut classifies (contrary: an agreement) from one
// the base cut refuses too (another effect).
func TestModifierSizeIntervalFit(t *testing.T) {
	cut := sizeCuts(false)[0] // 1/2 at 0.004
	b := sizeBoundary{Lo: 0.010, Hi: 0.012, HasLo: true, HasHi: true}
	for _, tc := range []struct {
		cls     uint8
		roll    float64
		kind    sizeRollKind
		want    sizeBoundary
		changed bool
	}{
		{1, 0.009, sizeRollAccepted, b, false},
		{1, 0.011, sizeRollAccepted, sizeBoundary{Lo: 0.011, Hi: 0.012, HasLo: true, HasHi: true}, true},
		{2, 0.0115, sizeRollAccepted, sizeBoundary{Lo: 0.010, Hi: 0.0115, HasLo: true, HasHi: true}, true},
		{3, 0.5, sizeRollAccepted, b, false},
		{2, 0.006, sizeRollContrary, b, false}, // the base cut's XS, under the moved cut
		{2, 0.003, sizeRollRefused, b, false},  // an XS under the base cut: moved down
		{1, 0.02, sizeRollRefused, b, false},   // an XXS over the interval: moved further
	} {
		kind, next, changed := b.fit(cut, tc.cls, tc.roll)
		if kind != tc.kind || next != tc.want || changed != tc.changed {
			t.Errorf("class %d at %v: (%v, %+v, %v), want (%v, %+v, %v)", tc.cls, tc.roll,
				kind, next, changed, tc.kind, tc.want, tc.changed)
		}
	}
}
