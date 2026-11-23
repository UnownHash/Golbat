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
	if m.Type != modSizeCutoffs || m.Status != modConfirmed || m.Contradictions != 0 {
		t.Fatalf("modifier %v %v contradictions %d, want confirmed size_cutoffs with none",
			m.Type, m.Status, m.Contradictions)
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

// sizeEvent returns a learner with an active Rattata size modifier, "1/2" =
// [0.011, 0.015], in-cells A, B and C.
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
	if m.Type != modSizeCutoffs || !maps.Equal(m.Boundaries, want) || !m.ZoneWide(3) {
		t.Fatalf("modifier %v boundaries %v zone-wide %v", m.Type, m.Boundaries, m.ZoneWide(3))
	}
	return l
}

func TestModifierSizeContraryEndsInCell(t *testing.T) {
	l := sizeEvent(t)
	trs := l.feed(modCheck(modT0.Add(time.Minute), cellA, withSize(0.006, 2)), modZone)
	if len(trs) != 1 || trs[0].State != modEnded || trs[0].Mod.EndReason != "contrary" {
		t.Fatalf("transitions %v, want ended by contrary", states(trs))
	}
	if len(l.active()) != 0 {
		t.Fatal("ended modifier still active")
	}
}

func TestModifierSizeContraryOutsideMarksCell(t *testing.T) {
	l := sizeEvent(t)
	if trs := l.feed(modCheck(modT0.Add(time.Minute), cellD, withSize(0.006, 2)), modZone); len(trs) != 0 {
		t.Fatalf("transitions %v, want none", states(trs))
	}
	m := onlyActive(t, l)
	if _, ok := m.Outside[cellD]; !ok || m.ZoneWide(3) {
		t.Fatalf("outside %v zone-wide %v, want cellD outside, local", m.Outside, m.ZoneWide(3))
	}
}

func TestModifierSizeAgreementsNeverEnd(t *testing.T) {
	l := sizeEvent(t)
	var trs []modifierTransition
	for i := range 40 {
		ts := modT0.Add(time.Minute + time.Duration(i)*time.Second)
		trs = append(trs, l.feed(modCheck(ts, cellA, withSize(0.5, 3)), modZone)...)
		trs = append(trs, l.feed(modCheck(ts, cellD, withSize(0.5, 3)), modZone)...)
	}
	if len(trs) != 0 {
		t.Fatalf("transitions %v, want none", states(trs))
	}
	if m := onlyActive(t, l); len(m.Outside) != 0 {
		t.Fatalf("outside %v, want none", m.Outside)
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
	// 0.003], and a later such roll in an in-cell is a contradiction.
	l := newModifierLearner(defaultModifierParams())
	trs := feedAll(l, modZone,
		modCheck(modT0, cellA, withSize(0.010, 1)),
		modCheck(modT0.Add(time.Second), cellB, withSize(0.003, 2)),
		modCheck(modT0.Add(2*time.Second), cellC, withSize(0.009, 1)))
	if len(trs) != 1 || trs[0].State != modCandidate {
		t.Fatalf("transitions %v, want one candidate", states(trs))
	}
	want := map[string]sizeBoundary{"1/2": {Lo: 0.010, HasLo: true}}
	if b := trs[0].Mod.Boundaries; !maps.Equal(b, want) {
		t.Fatalf("seeded boundaries %v, want %v", b, want)
	}
	l.feed(modCheck(modT0.Add(3*time.Second), cellA, withSize(0.002, 2)), modZone)
	m := onlyActive(t, l)
	if m.Contradictions != 1 || !maps.Equal(m.Boundaries, want) {
		t.Fatalf("contradictions %d boundaries %v, want 1 and unchanged", m.Contradictions, m.Boundaries)
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
