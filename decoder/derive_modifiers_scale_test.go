package decoder

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/golang/geo/s2"
)

// modScaleCells is a 60 × 50 grid of distinct level-14 cells, about 1 km
// apart: a scanned zone ~60 km across.
func modScaleCells(t *testing.T) []s2.CellID {
	t.Helper()
	cells := make([]s2.CellID, 0, 3000)
	seen := make(map[s2.CellID]struct{}, 3000)
	for i := range 60 {
		for j := range 50 {
			c := modTestCell(51.0+0.01*float64(i), -1.0+0.016*float64(j))
			if _, dup := seen[c]; dup {
				t.Fatalf("grid cell %d,%d repeats a level-14 cell", i, j)
			}
			seen[c] = struct{}{}
			cells = append(cells, c)
		}
	}
	return cells
}

// TestModifierScaleNoiseNeverConfirms (design §8): 1% IV-mismatch noise,
// uniformly scattered over 3000 cells at 1800 checks an hour for 48 hours,
// never confirms anything.
func TestModifierScaleNoiseNeverConfirms(t *testing.T) {
	cells := modScaleCells(t)
	for seed := range uint64(4) {
		rng := rand.New(rand.NewPCG(seed, 1))
		l := newModifierLearner(defaultModifierParams())
		for i := range 48 * 1800 {
			ts := modT0.Add(time.Duration(i) * 2 * time.Second)
			ev := modCheck(ts, cells[rng.IntN(len(cells))])
			if rng.Float64() < 0.01 {
				ev.Agree.IVs = dimDisagree
			}
			for _, tr := range l.feed(ev, modZone) {
				if tr.State == modConfirmed {
					t.Fatalf("seed %d: noise confirmed %v after %s", seed, tr.Mod.Type, ts.Sub(modT0))
				}
			}
		}
	}
}

// TestModifierScaleSampledEventEnds (design §8): a zone-wide +5 event seen
// through sampled encounters, 12 an hour scattered over 3000 cells while the
// species keeps spawning (600 first sightings an hour), confirms and then
// ends within an hour of the event ending.
func TestModifierScaleSampledEventEnds(t *testing.T) {
	cells := modScaleCells(t)
	for seed := range uint64(4) {
		rng := rand.New(rand.NewPCG(seed, 2))
		l := newModifierLearner(defaultModifierParams())
		const perHour, eventHours = 12, 6
		eventEnd := modT0.Add(eventHours * time.Hour)
		var confirmed, ended time.Time
		for i := range (eventHours + 6) * perHour {
			ts := modT0.Add(time.Duration(i) * time.Hour / perHour)
			ev := modCheck(ts, cells[rng.IntN(len(cells))])
			if ts.Before(eventEnd) {
				withLevelDelta(5)(&ev)
			}
			trs := l.feed(ev, modZone)
			for k := range 50 {
				trs = append(trs, l.feed(modCheck(ts.Add(time.Duration(1+k)*time.Second),
					cells[rng.IntN(len(cells))], asSighting), modZone)...)
			}
			for _, tr := range trs {
				switch tr.State {
				case modConfirmed:
					confirmed = ts
				case modEnded:
					ended = ts // when the learner stopped applying it, not tr.At
				}
			}
		}
		if confirmed.IsZero() || !confirmed.Before(eventEnd) {
			t.Fatalf("seed %d: confirmed at %v, want during the event", seed, confirmed)
		}
		if ended.IsZero() || ended.After(eventEnd.Add(time.Hour)) {
			t.Fatalf("seed %d: ended at %v, want within an hour of the event's end %v", seed, ended, eventEnd)
		}
	}
}
