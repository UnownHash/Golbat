package decoder

import (
	"maps"
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

func TestModifierSizeRattataShift(t *testing.T) {
	// The observed Rattata event: the two lower cuts moved up by 0.008, the
	// upper cuts unchanged; uniform rolls, every one checked.
	game := [4]float64{0.012, 0.037, 0.971, 0.996}
	rng := rand.New(rand.NewPCG(7, 7))
	cells := []s2.CellID{cellA, cellB, cellC}
	l := newModifierLearner(defaultModifierParams())
	var trs []modifierTransition
	const bucket = 15 * time.Minute
	for b := range 8 {
		for i := range 1500 {
			ts := modT0.Add(time.Duration(b)*bucket + time.Duration(i)*bucket/1500)
			r := rng.Float64()
			trs = append(trs, l.feed(modCheck(ts, cells[i%3], withSize(r, sizeClassByCuts(r, game))), modZone)...)
		}
	}
	for _, tr := range trs {
		if tr.State == modEnded {
			t.Fatalf("size modifier ended (%s)", tr.Mod.EndReason)
		}
	}
	m := onlyActive(t, l)
	if m.Type != modSizeCutoffs || m.Status != modConfirmed || m.Contradictions != 0 || !m.ZoneWide {
		t.Fatalf("modifier %v %v contradictions %d zone-wide %v, want confirmed zone-wide size_cutoffs with none",
			m.Type, m.Status, m.Contradictions, m.ZoneWide)
	}
	if len(m.Boundaries) != 2 {
		t.Fatalf("boundaries %v, want 1/2 and 2/3 only", m.Boundaries)
	}
	for name, cut := range map[string]float64{"1/2": 0.012, "2/3": 0.037} {
		b, ok := m.Boundaries[name]
		if !ok || !b.HasLo || !b.HasHi {
			t.Fatalf("boundary %s = %+v (listed %v), want both ends", name, b, ok)
		}
		if !(b.Lo < cut && cut <= b.Hi) || b.Hi-b.Lo >= 0.001 {
			t.Fatalf("boundary %s = [%v, %v], want a < 0.001 interval around %v", name, b.Lo, b.Hi, cut)
		}
	}
}

// sizeEvent returns a learner with a candidate Rattata size modifier, "1/2"
// = [0.011, 0.015], from deviations in cells A, B and C.
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
	want := map[string]sizeBoundary{"1/2": {Lo: 0.011, Hi: 0.015, HasLo: true, HasHi: true}}
	if m.Type != modSizeCutoffs || !maps.Equal(m.Boundaries, want) || m.ZoneWide || len(m.Cells) != 0 {
		t.Fatalf("modifier %v boundaries %v zone-wide %v cells %v", m.Type, m.Boundaries, m.ZoneWide, m.Cells)
	}
	return l
}

// sizeConfirmed is sizeEvent after seven more deviations across "1/2": ten
// matches in the zone record, so zone-wide and confirmed.
func sizeConfirmed(t *testing.T) *modifierLearner {
	t.Helper()
	l := sizeEvent(t)
	var trs []modifierTransition
	for i := range 7 {
		trs = append(trs, l.feed(modCheck(modT0.Add(time.Duration(10+i)*time.Second), cellB, withSize(0.009, 1)), modZone)...)
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
// cut) is an agreement in the zone record: it excludes its cell, and the
// modifier ends once such rolls are half of the record.
func TestModifierSizeContraryRollsEnd(t *testing.T) {
	l := sizeConfirmed(t)
	if trs := l.feed(modCheck(modT0.Add(time.Minute), cellD, withSize(0.006, 2)), modZone); len(trs) != 0 {
		t.Fatalf("one contrary roll: %v, want no transition", states(trs))
	}
	if m := onlyActive(t, l); !m.ZoneWide || m.Covers(modZone, cellD) || !m.Covers(modZone, cellA) {
		t.Fatalf("zone-wide %v excluded %v, want zone-wide less cellD", m.ZoneWide, m.Excluded)
	}
	for i := range 7 {
		trs := l.feed(modCheck(modT0.Add(time.Minute+time.Duration(1+i)*time.Second), cellE, withSize(0.006, 2)), modZone)
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
	if m := onlyActive(t, l); !m.ZoneWide || len(m.Excluded) != 0 {
		t.Fatalf("zone-wide %v excluded %v, want zone-wide with none excluded", m.ZoneWide, m.Excluded)
	}
}

// A size modifier is zone-wide only: matches in one level-10 cell never
// cover a venue cell, so it stays a candidate.
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

// A confirmed size modifier lists a new boundary only after start deviations
// across that cut; a candidate lists it at once, and loses it at the confirm
// if it is still short.
func TestModifierSizeConfirmedNeedsStartRollsForNewBoundary(t *testing.T) {
	l := sizeConfirmed(t)
	across := func(i int) modifierEvent { // XS just above the base 2/3 cut (0.029)
		return modCheck(modT0.Add(time.Minute+time.Duration(i)*time.Second), cellA, withSize(0.031+0.001*float64(i), 2))
	}
	for i := range 2 {
		l.feed(across(i), modZone)
		if _, ok := onlyActive(t, l).Boundaries["2/3"]; ok {
			t.Fatalf("2/3 listed after %d rolls across it, want it after 3", i+1)
		}
	}
	l.feed(across(2), modZone)
	want := sizeBoundary{Lo: 0.033, HasLo: true}
	if b, ok := onlyActive(t, l).Boundaries["2/3"]; !ok || b != want {
		t.Fatalf("2/3 = %+v (listed %v) after 3 rolls across it, want %+v", b, ok, want)
	}

	l = sizeEvent(t)
	l.feed(across(0), modZone)
	if _, ok := onlyActive(t, l).Boundaries["2/3"]; !ok {
		t.Fatal("candidate: 2/3 not listed after a roll across it")
	}
	var trs []modifierTransition
	for i := range 6 {
		trs = append(trs, l.feed(modCheck(modT0.Add(2*time.Minute+time.Duration(i)*time.Second), cellB, withSize(0.009, 1)), modZone)...)
	}
	if len(trs) != 1 || trs[0].State != modConfirmed {
		t.Fatalf("transitions %v, want confirmed", states(trs))
	}
	if b := trs[0].Mod.Boundaries; len(b) != 1 || !b["1/2"].HasLo {
		t.Fatalf("confirmed boundaries %v, want 1/2 only (2/3 has one roll)", b)
	}
}

func TestModifierSizeIgnoresIVDisagree(t *testing.T) {
	l := sizeEvent(t)
	before := onlyActive(t, l).Boundaries
	feedAll(l, modZone,
		modCheck(modT0.Add(time.Minute), cellA, withSize(0.013, 1), withIVsDisagree),
		modCheck(modT0.Add(time.Minute+time.Second), cellA, withSize(0.012, 2), withIVsDisagree),
		modCheck(modT0.Add(time.Minute+2*time.Second), cellA, withSize(0.006, 2), withIVsDisagree))
	for _, m := range l.active() {
		if m.Type == modSizeCutoffs {
			if !maps.Equal(m.Boundaries, before) {
				t.Fatalf("boundaries %v, want unchanged %v", m.Boundaries, before)
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
	want := map[string]sizeBoundary{"1/2": {Lo: 0.010, HasLo: true}}
	if m := trs[0].Mod; !maps.Equal(m.Boundaries, want) || m.Observations != 2 || m.Contradictions != 1 {
		t.Fatalf("seeded boundaries %v observations %d contradictions %d, want %v, 2 and 1",
			m.Boundaries, m.Observations, m.Contradictions, want)
	}
	l.feed(modCheck(modT0.Add(3*time.Second), cellA, withSize(0.002, 2)), modZone)
	m := onlyActive(t, l)
	if m.Contradictions != 2 || m.Observations != 2 || !maps.Equal(m.Boundaries, want) {
		t.Fatalf("contradictions %d observations %d boundaries %v, want 2, 2 and unchanged", m.Contradictions, m.Observations, m.Boundaries)
	}
	rec := l.scopes[modScopeKey{m.Scope, modZone}].rec[modDimSize]
	if v := rec.judge(modIDSizeMatch, false, 4); rec.n != 4 || v.on || !v.byEffect {
		t.Fatalf("record holds %d, verdict %+v: want 2 matches and 2 refused rolls, no majority", rec.n, v)
	}
}

func TestModifierSizeCellZeroNotEvidence(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	trs := feedAll(l, modZone,
		modCheck(modT0, cellA, withSize(0.008, 1)),
		modCheck(modT0.Add(time.Second), 0, withSize(0.012, 1)),
		modCheck(modT0.Add(2*time.Second), cellB, withSize(0.009, 1)))
	want := map[string]sizeBoundary{"1/2": {Lo: 0.009, HasLo: true}}
	if len(trs) != 1 || !maps.Equal(trs[0].Mod.Boundaries, want) {
		t.Fatalf("transitions %v, want a candidate seeded %v without the cell-0 roll", states(trs), want)
	}

	l = sizeEvent(t)
	before := onlyActive(t, l).Boundaries
	l.feed(modCheck(modT0.Add(time.Minute), 0, withSize(0.013, 1)), modZone)
	if m := onlyActive(t, l); !maps.Equal(m.Boundaries, before) || m.Observations != 4 {
		t.Fatalf("boundaries %v observations %d, want unchanged %v and the deviation counted", m.Boundaries, m.Observations, before)
	}
}
