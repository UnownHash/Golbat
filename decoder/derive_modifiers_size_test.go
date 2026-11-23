package decoder

import (
	"math/rand/v2"
	"strconv"
	"testing"
	"time"

	"github.com/golang/geo/s2"

	"golbat/derive/encounter"
)

// withSize makes ev a check of a non-strong Rattata (19/0) with size roll
// roll, observed class obs, and the size dimension agreeing when the base
// cuts (encounter's own classifier) classify roll as obs.
func withSize(roll float64, obs uint8) func(*modifierEvent) {
	return func(ev *modifierEvent) {
		ev.PokemonID, ev.Form, ev.Strong = 19, 0, false
		ev.SizeRoll, ev.ObsSize = roll, obs
		ev.Agree.Size = dimAgree
		if encounter.ClassifySize(roll, false) != int32(obs) {
			ev.Agree.Size = dimDisagree
		}
	}
}

// sizeClassByCuts classifies roll with an event's cuts, independent of the
// production base cuts.
func sizeClassByCuts(roll float64, cuts [4]float64) uint8 {
	c := uint8(1)
	for _, cut := range cuts {
		if roll >= cut {
			c++
		}
	}
	return c
}

// sizeActive returns the active size modifier of boundary cut, nil when
// there is none.
func sizeActive(l *modifierLearner, cut string) *derivationModifier {
	for _, m := range l.active() {
		if m.Type == modSizeCutoffs && m.Effect.Cut == cut {
			return m
		}
	}
	return nil
}

// sizeIndexClass classifies roll for a Rattata in cell with the index built
// from l's active modifiers.
func sizeIndexClass(l *modifierLearner, cell s2.CellID, roll float64) (int32, bool) {
	idx := buildModifierIndex(l.active())
	base := encounter.ClassifySize(roll, false)
	if idx == nil {
		return base, true
	}
	adj := idx.lookup(19, 0, false, cell, func(s2.CellID) string { return modZone })
	if !adj.adjustsSize() {
		return base, true
	}
	return adj.sizeClass(roll, false, 0, base)
}

func TestModifierSizeRattataShift(t *testing.T) {
	// The observed Rattata event: the two lower cuts moved up by 0.008, the
	// upper cuts unchanged; uniform rolls, every one checked.
	game := [4]float64{0.012, 0.037, 0.971, 0.996}
	rng := rand.New(rand.NewPCG(7, 7))
	cells := modScaleCells(t)
	l := newModifierLearner(defaultModifierParams())
	var trs []modifierTransition
	const bucket = 15 * time.Minute
	for b := range 8 {
		for i := range 1500 {
			ts := modT0.Add(time.Duration(b)*bucket + time.Duration(i)*bucket/1500)
			r := rng.Float64()
			trs = append(trs, l.feed(modCheck(ts, cells[rng.IntN(len(cells))], withSize(r, sizeClassByCuts(r, game))), modZone)...)
		}
	}
	for _, tr := range trs {
		if tr.State == modEnded {
			t.Fatalf("size modifier ended (%s)", tr.Mod.EndReason)
		}
	}
	// Each moved cut is its own modifier; the unmoved ones have none.
	if act := l.active(); len(act) != 2 {
		t.Fatalf("%d active modifiers, want the 1/2 and 2/3 boundaries", len(act))
	}
	for name, cut := range map[string]float64{"1/2": 0.012, "2/3": 0.037} {
		m := sizeActive(l, name)
		if m == nil || m.Status != modConfirmed || m.Contradictions != 0 || !m.ZoneWide || m.Paused {
			t.Fatalf("boundary %s: %+v, want confirmed zone-wide with no contradiction", name, m)
		}
		b := m.Interval
		if !b.HasLo || !b.HasHi {
			t.Fatalf("boundary %s = %+v, want both ends", name, b)
		}
		if !(b.Lo < cut && cut <= b.Hi) || b.Hi-b.Lo >= 0.001 {
			t.Fatalf("boundary %s = [%v, %v], want a < 0.001 interval around %v", name, b.Lo, b.Hi, cut)
		}
	}
	// The index combines the two boundaries into the event's cuts.
	for roll, want := range map[float64]int32{0.008: 1, 0.02: 2, 0.033: 2, 0.05: 3, 0.99: 4} {
		if got, ok := sizeIndexClass(l, cells[0], roll); !ok || got != want {
			t.Errorf("class of %v = (%d, %v), want %d", roll, got, ok, want)
		}
	}
}

// sizeEvent returns a learner with a candidate Rattata size modifier of
// boundary 1/2, [0.011, 0.015], from deviations in cells A, B and C.
func sizeEvent(t *testing.T) *modifierLearner {
	t.Helper()
	l := newModifierLearner(defaultModifierParams())
	trs := feedAll(l, modZone,
		modCheck(modT0, cellA, withSize(0.008, 1)),
		modCheck(modT0.Add(time.Second), cellB, withSize(0.009, 1)),
		modCheck(modT0.Add(2*time.Second), cellC, withSize(0.011, 1)),
		modCheck(modT0.Add(3*time.Second), cellA, withSize(0.015, 2)))
	if len(trs) != 1 || trs[0].State != modCandidate {
		t.Fatalf("transitions %v, want one candidate", states(trs))
	}
	m := onlyActive(t, l)
	want := sizeBoundary{Lo: 0.011, Hi: 0.015, HasLo: true, HasHi: true}
	if m.Type != modSizeCutoffs || m.Effect.Cut != "1/2" || m.Interval != want || m.ZoneWide || len(m.Cells) != 0 {
		t.Fatalf("modifier %v %q interval %+v zone-wide %v cells %v", m.Type, m.Effect.Cut, m.Interval, m.ZoneWide, m.Cells)
	}
	return l
}

// sizeConfirmed is sizeEvent after seven more deviations across "1/2" over
// the zone: ten matches from ten cells in the zone record, so zone-wide and
// confirmed.
func sizeConfirmed(t *testing.T) *modifierLearner {
	t.Helper()
	l := sizeEvent(t)
	var trs []modifierTransition
	for i, c := range modWideCells(7) {
		trs = append(trs, l.feed(modCheck(modT0.Add(time.Duration(10+i)*time.Second), c, withSize(0.009, 1)), modZone)...)
	}
	if len(trs) != 1 || trs[0].State != modConfirmed {
		t.Fatalf("transitions %v, want confirmed at the tenth deviation", states(trs))
	}
	if m := onlyActive(t, l); !m.ZoneWide || len(m.Cells) != 0 || m.Observations != 10 {
		t.Fatalf("zone-wide %v cells %v observations %d, want zone-wide only with 10", m.ZoneWide, m.Cells, m.Observations)
	}
	return l
}

// A contrary roll (in the moved cut's disputed band, classified by the base
// cut) is an agreement in the zone record: it excludes its cell, the second
// running pauses the modifier, and it ends once such rolls are half of the
// record.
func TestModifierSizeContraryRollsEnd(t *testing.T) {
	l := sizeConfirmed(t)
	if trs := l.feed(modCheck(modT0.Add(time.Minute), cellD, withSize(0.006, 2)), modZone); len(trs) != 0 {
		t.Fatalf("one contrary roll: %v, want no transition", states(trs))
	}
	if m := onlyActive(t, l); !m.ZoneWide || m.Paused || m.Covers(modZone, cellD) || !m.Covers(modZone, cellA) {
		t.Fatalf("zone-wide %v paused %v excluded %v, want zone-wide less cellD, not paused", m.ZoneWide, m.Paused, m.Excluded)
	}
	for i := range 7 {
		trs := l.feed(modCheck(modT0.Add(time.Minute+time.Duration(1+i)*time.Second), cellE, withSize(0.006, 2)), modZone)
		if i < 6 && !onlyActive(t, l).Paused {
			t.Fatalf("contrary roll %d: not paused after two running", i+2)
		}
		if i < 6 && len(trs) != 0 {
			t.Fatalf("contrary roll %d: %v, want no transition", i+2, states(trs))
		}
		if i == 6 && (len(trs) != 1 || trs[0].State != modEnded || trs[0].Mod.EndReason != "agreement") {
			t.Fatalf("8th contrary roll: %v, want ended by agreement", states(trs))
		}
	}
	if len(l.active()) != 0 {
		t.Fatal("ended modifier still active")
	}
}

// Rolls away from the moved cut say nothing about it: they never enter the
// zone record, so however many agree the modifier stays.
func TestModifierSizeAgreementsNeverEnd(t *testing.T) {
	l := sizeConfirmed(t)
	var trs []modifierTransition
	for i := range 40 {
		ts := modT0.Add(time.Minute + time.Duration(i)*time.Second)
		trs = append(trs, l.feed(modCheck(ts, cellA, withSize(0.5, 3)), modZone)...)
		trs = append(trs, l.feed(modCheck(ts, cellD, withSize(0.5, 3)), modZone)...)
	}
	if len(trs) != 0 {
		t.Fatalf("transitions %v, want none", states(trs))
	}
	if m := onlyActive(t, l); !m.ZoneWide || m.Paused || len(m.Excluded) != 0 {
		t.Fatalf("zone-wide %v paused %v excluded %v, want zone-wide with none excluded", m.ZoneWide, m.Paused, m.Excluded)
	}
}

// A size modifier has no venue cells: matches in one cell list none, and it
// stays a candidate.
func TestModifierSizeNoVenueCells(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	var trs []modifierTransition
	for i := range 20 {
		trs = append(trs, l.feed(modCheck(modT0.Add(time.Duration(i)*time.Second), cellA, withSize(0.009, 1)), modZone)...)
	}
	if len(trs) != 1 || trs[0].State != modCandidate {
		t.Fatalf("transitions %v, want only the candidate", states(trs))
	}
	if m := onlyActive(t, l); m.ZoneWide || len(m.Cells) != 0 || m.Covers(modZone, cellA) {
		t.Fatalf("zone-wide %v cells %v, want no coverage", m.ZoneWide, m.Cells)
	}
}

// Strong size evidence counts only XL and XXL, the classes the strong walk
// can produce: strong pokemon observed as class 3 at rolls 0.900–0.909
// (which would read as the 4/5 cut moved) are ignored.
func TestModifierSizeStrongIgnoresOtherClasses(t *testing.T) {
	strongSize := func(roll float64, obs uint8) func(*modifierEvent) {
		return func(ev *modifierEvent) { ev.SizeRoll, ev.ObsSize, ev.Agree.Size = roll, obs, dimDisagree }
	}
	l := newModifierLearner(defaultModifierParams())
	cells := []s2.CellID{cellA, cellB, cellC}
	var trs []modifierTransition
	for i := range 20 {
		ts := modT0.Add(time.Duration(i) * time.Second)
		trs = append(trs, l.feed(modCheck(ts, cells[i%3], strongSize(0.900+0.001*float64(i%10), 3)), modZone)...)
	}
	if len(trs) != 0 || l.trackCount() != 0 {
		t.Fatalf("transitions %v tracks %d, want nothing from class 3 on a strong pokemon", states(trs), l.trackCount())
	}
	// An XL observed above the strong cut is still a deviation.
	for i := range 3 {
		ts := modT0.Add(time.Minute + time.Duration(i)*time.Second)
		trs = append(trs, l.feed(modCheck(ts, cells[i], strongSize(0.85, 4)), modZone)...)
	}
	if len(trs) != 1 || trs[0].State != modCandidate || trs[0].Mod.Type != modSizeCutoffs {
		t.Fatalf("transitions %v, want a size candidate from XL rolls above the strong cut", states(trs))
	}
}

// Codex round 4 (a): each boundary is its own modifier. Deviations across
// 2/3 seen in one venue cell while 1/2 is confirmed zone-wide make a 2/3
// candidate that never applies, in the venue or outside it: it does not
// ride on the other boundary's coverage (it was listed on the one size
// modifier, and 0.0305 was made XS across the zone).
func TestModifierSizeVenueBoundaryNeverApplied(t *testing.T) {
	l := sizeConfirmed(t)
	wideCells := modWideCells(7)
	var trs []modifierTransition
	for i := range 20 {
		ts := modT0.Add(time.Minute + time.Duration(2*i)*time.Second)
		// The zone-wide event goes on: an XXS across 1/2 somewhere in the zone.
		trs = append(trs, l.feed(modCheck(ts, wideCells[i%7], withSize(0.009, 1)), modZone)...)
		// And at a venue, cell A, an XS just above the base 2/3 cut (0.029).
		trs = append(trs, l.feed(modCheck(ts.Add(time.Second), cellA, withSize(0.030+0.0001*float64(i), 2)), modZone)...)
	}
	if len(trs) != 1 || trs[0].State != modCandidate || trs[0].Mod.Effect.Cut != "2/3" {
		t.Fatalf("transitions %v, want only a 2/3 candidate", states(trs))
	}
	venue, wide := sizeActive(l, "2/3"), sizeActive(l, "1/2")
	if venue == nil || venue.Status != modCandidate || venue.ZoneWide || venue.Observations != 20 {
		t.Fatalf("2/3 = %+v, want a candidate with 20 observations and no coverage", venue)
	}
	if wide == nil || wide.Status != modConfirmed || !wide.ZoneWide || wide.Observations != 30 {
		t.Fatalf("1/2 = %+v, want it confirmed zone-wide and untouched by the 2/3 rolls", wide)
	}
	for _, cell := range []s2.CellID{cellA, cellD} {
		if got, ok := sizeIndexClass(l, cell, 0.0305); !ok || got != 3 {
			t.Errorf("0.0305 in %v = (%d, %v), want the base class 3: 2/3 was seen at a venue only", cell, got, ok)
		}
		if got, ok := sizeIndexClass(l, cell, 0.009); !ok || got != 1 {
			t.Errorf("0.009 in %v = (%d, %v), want 1 by the zone-wide 1/2", cell, got, ok)
		}
	}
}

func TestModifierSizeIgnoresIVDisagree(t *testing.T) {
	l := sizeEvent(t)
	before := onlyActive(t, l).Interval
	feedAll(l, modZone,
		modCheck(modT0.Add(time.Minute), cellA, withSize(0.013, 1), withIVsDisagree),
		modCheck(modT0.Add(time.Minute+time.Second), cellA, withSize(0.012, 2), withIVsDisagree),
		modCheck(modT0.Add(time.Minute+2*time.Second), cellA, withSize(0.006, 2), withIVsDisagree))
	for _, m := range l.active() {
		if m.Type == modSizeCutoffs {
			if m.Interval != before {
				t.Fatalf("interval %+v, want unchanged %+v", m.Interval, before)
			}
			return
		}
	}
	t.Fatal("size modifier no longer active")
}

// TestModifierSizeCutsMatchEncounter fails when the learner's cuts drift
// from the ones encounter classifies a roll with.
func TestModifierSizeCutsMatchEncounter(t *testing.T) {
	for _, strong := range []bool{false, true} {
		cuts := baseSizeCuts(strong)
		if want := map[bool]int{false: 4, true: 1}[strong]; len(cuts) != want {
			t.Fatalf("strong=%v: %d cuts, want %d", strong, len(cuts), want)
		}
		for name, cut := range cuts {
			below, err := strconv.Atoi(name[:1])
			if err != nil || name != name[:1]+"/"+strconv.Itoa(below+1) {
				t.Fatalf("strong=%v: bad boundary name %q", strong, name)
			}
			if got := encounter.ClassifySize(cut-1e-9, strong); got != int32(below) {
				t.Errorf("strong=%v %s: class just under %v = %d, want %d", strong, name, cut, got, below)
			}
			if got := encounter.ClassifySize(cut+1e-9, strong); got != int32(below+1) {
				t.Errorf("strong=%v %s: class just over %v = %d, want %d", strong, name, cut, got, below+1)
			}
		}
	}
}

func TestModifierSizeNeverInverted(t *testing.T) {
	// The second pending deviation, XS at 0.003 under an XXS at 0.010, would
	// empty "1/2": the candidate leaves it out instead of listing [0.010,
	// 0.003]. A refused roll is a contradiction, not an observation, and
	// another effect in the zone record.
	l := newModifierLearner(defaultModifierParams())
	trs := feedAll(l, modZone,
		modCheck(modT0, cellA, withSize(0.010, 1)),
		modCheck(modT0.Add(time.Second), cellB, withSize(0.003, 2)),
		modCheck(modT0.Add(2*time.Second), cellC, withSize(0.009, 1)))
	if len(trs) != 1 || trs[0].State != modCandidate {
		t.Fatalf("transitions %v, want one candidate", states(trs))
	}
	want := sizeBoundary{Lo: 0.010, HasLo: true}
	if m := trs[0].Mod; m.Interval != want || m.Observations != 2 || m.Contradictions != 1 {
		t.Fatalf("seeded interval %+v observations %d contradictions %d, want %+v, 2 and 1",
			m.Interval, m.Observations, m.Contradictions, want)
	}
	l.feed(modCheck(modT0.Add(3*time.Second), cellA, withSize(0.002, 2)), modZone)
	m := onlyActive(t, l)
	if m.Contradictions != 2 || m.Observations != 2 || m.Interval != want {
		t.Fatalf("contradictions %d observations %d interval %+v, want 2, 2 and unchanged", m.Contradictions, m.Observations, m.Interval)
	}
	rec := l.scopes[modScopeKey{m.Scope, modZone}].rec[modDimSize]
	if v := rec.judge(modIDSizeMatch, false, modJudge{entries: 4, cells: 8}, 0); rec.n != 4 || v.on || !v.byEffect {
		t.Fatalf("record holds %d, verdict %+v: want 2 matches and 2 refused rolls, no majority", rec.n, v)
	}
	// Each refused roll disproved the boundary in its cell.
	_, a := m.Excluded[cellA]
	_, b := m.Excluded[cellB]
	if !a || !b || len(m.Excluded) != 2 {
		t.Fatalf("excluded %v, want cells A and B", m.Excluded)
	}
}

func TestModifierSizeCellZeroNotEvidence(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	trs := feedAll(l, modZone,
		modCheck(modT0, cellA, withSize(0.008, 1)),
		modCheck(modT0.Add(time.Second), 0, withSize(0.012, 1)),
		modCheck(modT0.Add(2*time.Second), cellB, withSize(0.009, 1)))
	want := sizeBoundary{Lo: 0.009, HasLo: true}
	if len(trs) != 1 || trs[0].Mod.Interval != want {
		t.Fatalf("transitions %v, want a candidate seeded %v without the cell-0 roll", states(trs), want)
	}

	l = sizeEvent(t)
	before := onlyActive(t, l).Interval
	l.feed(modCheck(modT0.Add(time.Minute), 0, withSize(0.013, 1)), modZone)
	if m := onlyActive(t, l); m.Interval != before || m.Observations != 4 {
		t.Fatalf("interval %+v observations %d, want unchanged %+v and the deviation counted", m.Interval, m.Observations, before)
	}
}
