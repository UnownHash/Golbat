package decoder

import (
	"maps"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/golang/geo/s2"
)

// withSize makes ev a check of a non-strong Rattata (19/0) with size roll
// roll, observed class obs, and the size dimension agreeing when the base
// cuts classify roll as obs.
func withSize(roll float64, obs uint8) func(*modifierEvent) {
	return func(ev *modifierEvent) {
		ev.PokemonID, ev.Form, ev.Strong = 19, 0, false
		ev.SizeRoll, ev.ObsSize = roll, obs
		ev.Agree.Size = dimAgree
		if sizeClassByCuts(roll, baseCutsList) != obs {
			ev.Agree.Size = dimDisagree
		}
	}
}

var baseCutsList = [4]float64{0.004, 0.029, 0.971, 0.996}

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

func TestModifierSizeBaseCuts(t *testing.T) {
	want := map[string]float64{"1/2": 0.004, "2/3": 0.029, "3/4": 0.971, "4/5": 0.996}
	if got := baseSizeCuts(false); !maps.Equal(got, want) {
		t.Fatalf("base cuts %v, want %v", got, want)
	}
	if got := baseSizeCuts(true); !maps.Equal(got, map[string]float64{"4/5": 0.8}) {
		t.Fatalf("strong cuts %v, want 4/5 at 0.8", got)
	}
}
