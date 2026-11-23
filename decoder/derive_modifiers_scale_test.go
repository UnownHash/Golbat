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

// TestModifierScaleVenueNeverApplies (design §9): a two-cell venue giving
// half of a zone's checks at 1800 an hour for six hours, lying across a
// level-10 cell edge or inside one, never goes zone-wide and never confirms:
// no cell is ever covered.
func TestModifierScaleVenueNeverApplies(t *testing.T) {
	grid := modScaleCells(t)
	straddle := modStraddleVenue(t)
	for name, venue := range map[string][]s2.CellID{"straddle": straddle[:], "inside": {cellA, cellA2}} {
		rng := rand.New(rand.NewPCG(7, 7))
		l := newModifierLearner(defaultModifierParams())
		for i := range 6 * 1800 {
			ts := modT0.Add(time.Duration(i) * 2 * time.Second)
			ev := modCheck(ts, grid[rng.IntN(len(grid))])
			if rng.IntN(2) == 0 {
				ev = modCheck(ts, venue[rng.IntN(len(venue))], withLevelDelta(5))
			}
			for _, tr := range l.feed(ev, modZone) {
				if tr.State == modConfirmed {
					t.Fatalf("%s: venue confirmed after %s", name, ts.Sub(modT0))
				}
			}
			for _, tk := range l.activeTracks {
				if tk.mod.ZoneWide {
					t.Fatalf("%s: venue zone-wide after %s", name, ts.Sub(modT0))
				}
			}
		}
		if m := onlyActive(t, l); len(m.Cells) != 2 {
			t.Fatalf("%s: cells %v, want the two venue cells listed", name, m.Cells)
		}
	}
}

// TestModifierScaleEventEndStopsApplying (design §9): a zone-wide +5 at 6000
// checks an hour over 3000 cells leaves no cell covered once its event has
// ended and eight base checks have come in, however slowly (30 an hour
// here); the venue cells it listed meanwhile cover nothing and are bounded.
func TestModifierScaleEventEndStopsApplying(t *testing.T) {
	grid := modScaleCells(t)
	rng := rand.New(rand.NewPCG(3, 3))
	l := newModifierLearner(defaultModifierParams())
	ts := modT0
	for range 6000 {
		ts = ts.Add(time.Hour / 6000)
		for _, tr := range l.feed(modCheck(ts, grid[rng.IntN(len(grid))], withLevelDelta(5)), modZone) {
			if tr.State == modEnded {
				t.Fatalf("ended (%s) during the event, after %s", tr.Mod.EndReason, ts.Sub(modT0))
			}
		}
	}
	tk := l.activeTracks[0]
	if m := tk.mod; m.Status != modConfirmed || !m.ZoneWide || len(m.Cells) == 0 || len(tk.venue) != modVenueMax {
		t.Fatalf("at the event's end: status %v zone-wide %v cells %d venue %d, want confirmed zone-wide with venue cells, %d tracked",
			m.Status, m.ZoneWide, len(m.Cells), len(tk.venue), modVenueMax)
	}
	for i := range 60 {
		ts = ts.Add(2 * time.Minute)
		cell := grid[rng.IntN(len(grid))]
		if i >= 8 {
			for _, m := range l.active() {
				if m.Status == modConfirmed || m.Covers(modZone, cell) {
					t.Fatalf("base check %d: a modifier still applies: status %v zone-wide %v", i+1, m.Status, m.ZoneWide)
				}
			}
		}
		l.feed(modCheck(ts, cell), modZone)
		l.sweep(ts)
	}
}
