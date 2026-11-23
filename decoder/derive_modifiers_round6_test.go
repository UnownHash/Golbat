package decoder

import (
	"testing"
	"time"

	"github.com/golang/geo/s2"
)

// Design §10.2: the pause counts every located non-match, a size roll from
// a cell with an unresolved disproof stays local, the index validates the
// whole cut sequence, and a candidate never confirms past its deadline.

// levelWith returns the active level offset of delta, nil when there is
// none.
func levelWith(l *modifierLearner, delta int32) *derivationModifier {
	for _, m := range l.active() {
		if m.Type == modLevelOffset && m.Effect.Delta == delta {
			return m
		}
	}
	return nil
}

// A superseded effect (+5 replaced by +3 at once, no base result between)
// is paused at the second check running that is not its own, at any
// sampling rate: here 12 an hour, one check every five minutes. A match
// applies it again.
func TestModifierPausesOnOtherEffect(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	cells := modWideCells(12)
	confirmLevel(t, l, modT0, cells)
	l.feed(modCheck(modT0.Add(5*time.Minute), cellD, withLevelDelta(3)), modZone)
	if m := levelWith(l, 5); m == nil || m.Paused || !modifierApplied(m) {
		t.Fatalf("after one +3: %+v, want +5 still applied", m)
	}
	l.feed(modCheck(modT0.Add(10*time.Minute), cellE, withLevelDelta(3)), modZone)
	m := levelWith(l, 5)
	if m == nil || !m.Paused || modifierApplied(m) || !m.ZoneWide || m.Status != modConfirmed {
		t.Fatalf("after two +3 running: %+v, want +5 paused, listed, not applied", m)
	}
	adj := buildModifierIndex(l.active()).lookup(959, 3272, true, cells[0], modZoneOf)
	if !adj.Suppress || adj.LevelDelta != 0 {
		t.Fatalf("stale +5 after two checks: adj %+v, want withheld", adj)
	}
	l.feed(modCheck(modT0.Add(15*time.Minute), cells[10], withLevelDelta(5)), modZone)
	if m := levelWith(l, 5); m == nil || m.Paused {
		t.Fatalf("after a +5 match: %+v, want +5 applied again", m)
	}
	// One of each running: another effect, then a base result.
	l.feed(modCheck(modT0.Add(20*time.Minute), cellF, withLevelDelta(3)), modZone)
	if m := levelWith(l, 5); m == nil || m.Paused {
		t.Fatalf("one +3 after the match: %+v, want not paused", m)
	}
	l.feed(modCheck(modT0.Add(25*time.Minute), unseenCell), modZone)
	if m := levelWith(l, 5); m == nil || !m.Paused {
		t.Fatalf("a +3 then an agreement: %+v, want paused", m)
	}
}

// A size roll the interval refuses that the base cut refuses too (another
// effect) counts towards the pause like a contrary one.
func TestModifierSizeRefusedRollsPause(t *testing.T) {
	l := sizeConfirmed(t)
	// XXS at 0.016: over the interval's upper end, and not base either.
	l.feed(modCheck(modT0.Add(time.Minute), cellD, withSize(0.016, 1)), modZone)
	if m := sizeActive(l, "1/2"); m == nil || m.Paused || m.Contradictions != 1 {
		t.Fatalf("one refused roll: %+v, want a contradiction and no pause", m)
	}
	l.feed(modCheck(modT0.Add(time.Minute+time.Second), cellE, withSize(0.016, 1)), modZone)
	if m := sizeActive(l, "1/2"); m == nil || !m.Paused {
		t.Fatalf("two refused rolls running: %+v, want paused", m)
	}
}

// A roll from a cell with an unresolved disproof says where that cell's cut
// lies, not the zone's: it never narrows the shared interval. True cut
// 0.012, interval (0.011, 0.015]; a venue whose own cut is lower shows XS
// at 0.0115. A roll at 0.01175 elsewhere must not become XS.
func TestModifierSizeExcludedCellDoesNotNarrowInterval(t *testing.T) {
	l := sizeConfirmed(t)
	want := sizeBoundary{Lo: 0.011, Hi: 0.015, HasLo: true, HasHi: true}
	// The venue: XS at 0.0105, under the interval (a contrary roll).
	l.feed(modCheck(modT0.Add(time.Minute), cellD, withSize(0.0105, 2)), modZone)
	if m := onlyActive(t, l); m.Covers(modZone, cellD) || m.Interval != want {
		t.Fatalf("after the venue's contrary roll: excluded %v interval %+v", m.Excluded, m.Interval)
	}
	// XS at 0.0115 there fits the interval and does not answer the disproof.
	l.feed(modCheck(modT0.Add(time.Minute+time.Second), cellD, withSize(0.0115, 2)), modZone)
	m := onlyActive(t, l)
	if m.Interval != want {
		t.Fatalf("interval %+v after an excluded cell's roll, want %+v unchanged", m.Interval, want)
	}
	if m.Covers(modZone, cellD) {
		t.Fatal("the venue cell was readmitted by a roll that does not contradict its disproof")
	}
	if got, ok := sizeIndexClass(l, modWideCells(1)[0], 0.01175); ok {
		t.Fatalf("roll 0.01175 elsewhere = (%d, true), want no class (inside the interval)", got)
	}
	// The same roll from a cell that never disproved the boundary is the
	// zone's evidence.
	l.feed(modCheck(modT0.Add(time.Minute+2*time.Second), cellE, withSize(0.0115, 2)), modZone)
	if m := onlyActive(t, l); m.Interval.Hi != 0.0115 {
		t.Fatalf("interval %+v after the roll in a covered cell, want Hi 0.0115", m.Interval)
	}
}

// A match in a cell with an unresolved disproof is not a zone match: it
// does not lift a pause. One that answers the disproof readmits the cell
// and is.
func TestModifierSizeExcludedCellMatchDoesNotUnpause(t *testing.T) {
	l := sizeConfirmed(t)
	l.feed(modCheck(modT0.Add(time.Minute), cellD, withSize(0.010, 2)), modZone)
	l.feed(modCheck(modT0.Add(time.Minute+time.Second), cellE, withSize(0.010, 2)), modZone)
	m := onlyActive(t, l)
	if !m.Paused || m.Covers(modZone, cellD) {
		t.Fatalf("after two contrary rolls: paused %v excluded %v, want paused less cellD and cellE", m.Paused, m.Excluded)
	}
	obs := m.Observations
	// XXS at 0.008 in cellD: across the base cut, but under the disproving
	// XS at 0.010 — both fit a cut local to the cell.
	l.feed(modCheck(modT0.Add(time.Minute+2*time.Second), cellD, withSize(0.008, 1)), modZone)
	m = onlyActive(t, l)
	if !m.Paused || m.Covers(modZone, cellD) || m.Observations != obs {
		t.Fatalf("after a non-resolving match in an excluded cell: paused %v covers %v observations %d (was %d), want still paused, excluded, not counted",
			m.Paused, m.Covers(modZone, cellD), m.Observations, obs)
	}
	// XXS at 0.0105 there contradicts the XS at 0.010: readmitted, a match.
	l.feed(modCheck(modT0.Add(time.Minute+3*time.Second), cellD, withSize(0.0105, 1)), modZone)
	m = onlyActive(t, l)
	if m.Paused || !m.Covers(modZone, cellD) || m.Observations != obs+1 {
		t.Fatalf("after the resolving match: paused %v covers %v observations %d, want applied, readmitted, counted",
			m.Paused, m.Covers(modZone, cellD), m.Observations)
	}
}

// The index validates the whole cut sequence, an interval with no upper end
// included: its lower end must not pass a later cut's lower bound.
func TestDerivationAdjustSizeOpenIntervalCutOrder(t *testing.T) {
	class := func(roll float64, mods ...*derivationModifier) (int32, bool) {
		adj := buildModifierIndex(mods).lookup(1, 0, false, adjCellA, func(s2.CellID) string { return adjZoneA })
		return adj.sizeClass(roll, false, 0, 3)
	}
	// 1/2 known only to lie above 0.050, 2/3 at its base 0.029.
	open := sizeMod("1/2", sizeBoundary{Lo: 0.050, HasLo: true})
	for _, roll := range []float64{0.001, 0.020, 0.040, 0.5, 0.999} {
		if got, ok := class(roll, open); ok {
			t.Errorf("open 1/2 above the base 2/3 cut: roll %v = (%d, true), want withheld", roll, got)
		}
	}
	// The same against a learned 2/3 interval under it.
	for _, roll := range []float64{0.001, 0.040} {
		if got, ok := class(roll, open, sizeMod("2/3", sizeBoundary{Lo: 0.036, Hi: 0.038, HasLo: true, HasHi: true})); ok {
			t.Errorf("open 1/2 above the 2/3 interval: roll %v = (%d, true), want withheld", roll, got)
		}
	}
	// And against a cut two further on: 2/3 open too, 3/4 at its base.
	if got, ok := class(0.5, sizeMod("2/3", sizeBoundary{Lo: 0.98, HasLo: true})); ok {
		t.Errorf("open 2/3 above the base 3/4 cut: (%d, true), want withheld", got)
	}
	if got, ok := class(0.001, sizeMod("1/2", sizeBoundary{Lo: 0.02, HasLo: true}),
		sizeMod("2/3", sizeBoundary{Lo: 0.98, HasLo: true})); ok {
		t.Errorf("open 1/2 and an open 2/3 above the base 3/4 cut: (%d, true), want withheld", got)
	}
	// In order, an open interval classes the rolls under its lower end and
	// nothing from there up.
	lowOpen := sizeMod("1/2", sizeBoundary{Lo: 0.02, HasLo: true})
	mid := sizeMod("2/3", sizeBoundary{Lo: 0.036, Hi: 0.038, HasLo: true, HasHi: true})
	if got, ok := class(0.01, lowOpen, mid); !ok || got != 1 {
		t.Errorf("under an open 1/2 in order: (%d, %v), want 1", got, ok)
	}
	for _, roll := range []float64{0.02, 0.03, 0.5} {
		if got, ok := class(roll, lowOpen, mid); ok {
			t.Errorf("from an open 1/2's lower end up: roll %v = (%d, true), want absent", roll, got)
		}
	}
	// A lower end shared with the next interval's is in order (one roll can
	// be under both cuts).
	if got, ok := class(0.01, sizeMod("1/2", sizeBoundary{Lo: 0.036, HasLo: true}), mid); !ok || got != 1 {
		t.Errorf("open 1/2 sharing the 2/3 interval's lower end: (%d, %v), want 1", got, ok)
	}
	adj := buildModifierIndex([]*derivationModifier{lowOpen, mid}).lookup(1, 0, false, adjCellA,
		func(s2.CellID) string { return adjZoneA })
	if n := testing.AllocsPerRun(100, func() { _, _ = adj.sizeClass(0.01, false, 0, 3) }); n != 0 {
		t.Errorf("sizeClass allocates %v, want 0", n)
	}
}

// A candidate is ended at its deadline (2 × Unseen from its start) before
// it can confirm, on a check between prunes too: here the sweep at 1h59m59s
// sets the next prune a minute on, and the checks at 2h00m01s complete what
// zone-wide and confirmation need.
func TestModifierCandidateDeadlineBeforeConfirmOnFeed(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	at := func(d time.Duration, cell s2.CellID) []modifierTransition {
		return l.feed(modCheck(modT0.Add(d), cell, withLevelDelta(5)), modZone)
	}
	trs := append(at(0, cellA), at(time.Second, cellA)...)
	trs = append(trs, at(2*time.Second, cellA)...)
	trs = append(trs, at(50*time.Minute, cellA)...)
	trs = append(trs, at(100*time.Minute, cellA)...)
	cells := modWideCells(9)
	for i := range 6 {
		trs = append(trs, at(2*time.Hour-time.Duration(10-i)*time.Second, cells[i])...)
	}
	trs = append(trs, l.sweep(modT0.Add(2*time.Hour-time.Second))...)
	if len(trs) != 1 || trs[0].State != modCandidate {
		t.Fatalf("before the deadline: %v, want one candidate", states(trs))
	}
	trs = nil
	for i := 6; i < 9; i++ {
		trs = append(trs, at(2*time.Hour+time.Second, cells[i])...)
	}
	if len(trs) != 1 || trs[0].State != modEnded || trs[0].Mod.EndReason != "unconfirmed" {
		t.Fatalf("past the deadline: %v, want ended unconfirmed and never confirmed", states(trs))
	}
	if len(l.active()) != 0 {
		t.Fatalf("%d active modifiers, want none", len(l.active()))
	}
}

// The same on the sweep path: a sweep past the deadline finds the record's
// agreements aged and the matches left enough to go zone-wide.
func TestModifierCandidateDeadlineBeforeConfirmOnSweep(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	at := func(d time.Duration, cell s2.CellID, opts ...func(*modifierEvent)) []modifierTransition {
		return l.feed(modCheck(modT0.Add(d), cell, opts...), modZone)
	}
	up := withLevelDelta(5)
	trs := append(at(0, cellA, up), at(time.Second, cellA, up)...)
	trs = append(trs, at(2*time.Second, cellA, up)...)
	trs = append(trs, at(40*time.Minute, cellA, up)...)
	for _, c := range []s2.CellID{cellD, cellE, cellF, unseenCell} {
		trs = append(trs, at(time.Hour+30*time.Second, c)...)
	}
	for i, c := range modWideCells(10) {
		trs = append(trs, at(time.Hour+time.Minute+time.Duration(i)*6*time.Minute, c, up)...)
	}
	if len(trs) != 1 || trs[0].State != modCandidate {
		t.Fatalf("before the deadline: %v, want one candidate", states(trs))
	}
	trs = l.sweep(modT0.Add(2*time.Hour + 31*time.Second))
	if len(trs) != 1 || trs[0].State != modEnded || trs[0].Mod.EndReason != "unconfirmed" {
		t.Fatalf("sweep past the deadline: %v, want ended unconfirmed and never confirmed", states(trs))
	}
}

// A cell already excluded from a modifier says nothing new about the zone:
// a densely scanned venue with another effect must not keep pausing a
// zone-wide event. A non-match from a cell not yet excluded still counts.
func TestModifierExcludedCellDoesNotPause(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	cells := modWideCells(12)
	confirmPin(t, l, modZone, modT0, cells[:10]...)
	ts := modT0
	feed := func(cell s2.CellID, move int32) *derivationModifier {
		t.Helper()
		ts = ts.Add(time.Second)
		l.feed(modCheck(ts, cell, withCharged(move)), modZone)
		return modifierWith(t, l, 494)
	}
	for i := range 4 {
		if m := feed(cellA, 300); m.Paused || m.Covers(modZone, cellA) {
			t.Fatalf("venue check %d: paused %v covers the venue %v, want applied less the venue cell", i+1, m.Paused, m.Covers(modZone, cellA))
		}
	}
	// The venue's first check counted once: one more from a fresh cell pauses.
	if m := feed(cellD, 300); !m.Paused {
		t.Fatal("a non-match from a fresh cell after the venue's first: not paused")
	}
	if m := feed(cells[10], 494); m.Paused {
		t.Fatal("paused after a match")
	}
	// Both cells are excluded now: neither counts again.
	feed(cellA, 300)
	if m := feed(cellD, 300); m.Paused {
		t.Fatal("paused by checks from cells already excluded")
	}
}

// The same for a size boundary: contrary rolls from one excluded cell do
// not pause it; the next from another cell does.
func TestModifierSizeExcludedCellDoesNotPause(t *testing.T) {
	l := sizeConfirmed(t)
	for i := range 4 {
		l.feed(modCheck(modT0.Add(time.Minute+time.Duration(i)*time.Second), cellD, withSize(0.006, 2)), modZone)
		if m := onlyActive(t, l); m.Paused {
			t.Fatalf("contrary roll %d from one cell: paused", i+1)
		}
	}
	l.feed(modCheck(modT0.Add(2*time.Minute), cellE, withSize(0.016, 1)), modZone)
	if m := onlyActive(t, l); !m.Paused {
		t.Fatal("a refused roll from a fresh cell after the first: not paused")
	}
}
