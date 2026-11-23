package decoder

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/golang/geo/s2"
)

// Simulations of an event's edges and of a live event (design §10), from
// the fourth review round. Each is seeded.

// modLevelApplied reports whether an applied level offset covers cell.
func modLevelApplied(l *modifierLearner, cell s2.CellID) bool {
	for _, tk := range l.activeTracks {
		if m := tk.mod; m.Type == modLevelOffset && modifierApplied(m) && m.Covers(modZone, cell) {
			return true
		}
	}
	return false
}

// TestModifierEndDrainDoesNotFlap: a zone-wide +5 event at 6000 checks an
// hour ends, and the spawns from before its end drain over 45 minutes (the
// event's share of the checks falls linearly to nothing). Once that share
// is under half, few new base spawns are still given +5: a bare majority
// re-confirmed the modifier again and again (121 such spawns a run here, 37
// re-confirms; now 19 and 14). What remains is the 16-entry record's
// sampling noise: three quarters of it now and then, at a true share just
// under half.
func TestModifierEndDrainDoesNotFlap(t *testing.T) {
	grid := modScaleCells(t)
	const (
		perH  = 6000
		drain = 45 * time.Minute
		seeds = 4
	)
	var wrongAfterHalf, reconfirms, applied int
	for seed := range uint64(seeds) {
		rng := rand.New(rand.NewPCG(seed, 31))
		l := newModifierLearner(defaultModifierParams())
		end := modT0.Add(30 * time.Minute)
		step := time.Hour / perH
		for ts := modT0; ts.Before(end.Add(drain + 30*time.Minute)); ts = ts.Add(step) {
			after := ts.Sub(end)
			ev := modCheck(ts, grid[rng.IntN(len(grid))])
			if after < 0 || (after < drain && rng.Float64() > float64(after)/float64(drain)) {
				withLevelDelta(5)(&ev)
			}
			// A new (base) spawn's first sighting just before this check.
			if on := modLevelApplied(l, grid[rng.IntN(len(grid))]); on && after >= drain/2 {
				wrongAfterHalf++
			} else if on && after < 0 {
				applied++
			}
			for _, tr := range append(l.feed(ev, modZone), l.sweep(ts)...) {
				if tr.State == modConfirmed && after >= 0 {
					reconfirms++
				}
			}
		}
	}
	if applied < seeds*perH/4 {
		t.Fatalf("the event was applied to %d spawns before its end: not exercised", applied)
	}
	t.Logf("per run: %d base spawns given +5 after the share fell under half, %d re-confirms after the end",
		wrongAfterHalf/seeds, reconfirms/seeds)
	if wrongAfterHalf > 25*seeds {
		t.Errorf("%d base spawns per run given +5 after the event's share fell under half, want at most 25", wrongAfterHalf/seeds)
	}
}

// TestModifierStartDrainFewFlaps: an event starts among the spawns from
// before it (its share of the checks rises linearly over 45 minutes) at
// 6000 checks an hour. The modifier confirms a few times while the share
// is between one half and three quarters, not at every crossing of one half
// (36 to 40 confirms a run; now 11 to 18).
func TestModifierStartDrainFewFlaps(t *testing.T) {
	grid := modScaleCells(t)
	const (
		perH  = 6000
		drain = 45 * time.Minute
	)
	for seed := range uint64(4) {
		rng := rand.New(rand.NewPCG(seed, 41))
		l := newModifierLearner(defaultModifierParams())
		confirms := 0
		for ts := modT0; ts.Before(modT0.Add(2 * time.Hour)); ts = ts.Add(time.Hour / perH) {
			at := ts.Sub(modT0)
			ev := modCheck(ts, grid[rng.IntN(len(grid))])
			if at >= drain || rng.Float64() < float64(at)/float64(drain) {
				withLevelDelta(5)(&ev)
			}
			for _, tr := range l.feed(ev, modZone) {
				if tr.State == modConfirmed {
					confirms++
				}
			}
		}
		t.Logf("seed %d: %d confirms", seed, confirms)
		if confirms > 20 {
			t.Errorf("seed %d: %d confirms while the event started, want at most 20", seed, confirms)
		}
		applied := false
		for _, m := range l.active() {
			applied = applied || (m.Type == modLevelOffset && modifierApplied(m))
		}
		if !applied {
			t.Errorf("seed %d: the event is not applied an hour after it took over", seed)
		}
	}
}

// TestModifierSampledEndPausesWithinTwoChecks: an event seen through
// sampled encounters (12 an hour, Poisson) ends while the species keeps
// spawning. From the second base check after the end no new spawn is given
// +5 (it took about half an hour, until the base checks were half of the
// record).
func TestModifierSampledEndPausesWithinTwoChecks(t *testing.T) {
	grid := modScaleCells(t)
	const seeds = 20
	appliedAtEnd, wrong := 0, 0
	for seed := range uint64(seeds) {
		rng := rand.New(rand.NewPCG(seed, 77))
		l := newModifierLearner(defaultModifierParams())
		end := modT0.Add(4 * time.Hour)
		next, nextSighting := modT0, end
		baseChecks := 0
		for ts := modT0; ts.Before(end.Add(2 * time.Hour)); ts = ts.Add(10 * time.Second) {
			for ; !next.After(ts); next = next.Add(time.Duration(rng.ExpFloat64() * float64(time.Hour) / 12)) {
				ev := modCheck(next, grid[rng.IntN(len(grid))])
				if next.Before(end) {
					withLevelDelta(5)(&ev)
				} else {
					if baseChecks == 0 && modLevelApplied(l, unseenCell) {
						appliedAtEnd++
					}
					baseChecks++
				}
				l.feed(ev, modZone)
			}
			// First sightings of new (base) spawns, 600 an hour.
			for ; !nextSighting.After(ts); nextSighting = nextSighting.Add(6 * time.Second) {
				c := grid[rng.IntN(len(grid))]
				if baseChecks >= 2 && modLevelApplied(l, c) {
					wrong++
				}
				l.feed(modCheck(nextSighting, c, asSighting), modZone)
			}
			l.sweep(ts)
		}
	}
	if appliedAtEnd < seeds*3/4 {
		t.Fatalf("the event was applied at its end in %d of %d runs: not exercised", appliedAtEnd, seeds)
	}
	if wrong != 0 {
		t.Errorf("%d first sightings given +5 after the second base check since the event ended, want none", wrong)
	}
}

// TestModifierLiveEventNeverPauses: every event seen applied to its whole
// scope. A live forced move, whose base walk gives the pinned move half the
// time (neutral agreements), with a +5 on every check, confirms and is then
// never paused, excluded anywhere or ended, sampled or not.
func TestModifierLiveEventNeverPauses(t *testing.T) {
	grid := modScaleCells(t)
	for _, perH := range []int{60, 1800} {
		rng := rand.New(rand.NewPCG(5, uint64(perH)))
		l := newModifierLearner(defaultModifierParams())
		confirmed := map[modifierType]time.Duration{}
		for i := range perH * 6 {
			ts := modT0.Add(time.Duration(i) * time.Hour / time.Duration(perH))
			ev := modCheck(ts, grid[rng.IntN(len(grid))], withLevelDelta(5))
			if rng.IntN(2) == 0 {
				ev.ObsCharged = 494 // the base walk gives the pinned move
			} else {
				withCharged(494)(&ev)
			}
			for _, tr := range append(l.feed(ev, modZone), l.sweep(ts)...) {
				switch tr.State {
				case modConfirmed:
					confirmed[tr.Mod.Type] = ts.Sub(modT0)
				case modEnded:
					t.Fatalf("%d/h: %v ended (%s) at +%v during the event", perH, tr.Mod.Type, tr.Mod.EndReason, ts.Sub(modT0))
				}
			}
			for _, tk := range l.activeTracks {
				if m := tk.mod; m.Paused || len(m.Excluded) != 0 {
					t.Fatalf("%d/h: %v paused %v with %d excluded cells at +%v of a live event",
						perH, m.Type, m.Paused, len(m.Excluded), ts.Sub(modT0))
				}
			}
		}
		for _, typ := range []modifierType{modLevelOffset, modMovePin} {
			if at, ok := confirmed[typ]; !ok || at > time.Hour {
				t.Errorf("%d/h: %v confirmed at +%v (%v), want within the first hour", perH, typ, at, ok)
			}
		}
	}
}

// modBigZone returns n distinct level-14 cells in a block ~200 km across.
func modBigZone(n int) []s2.CellID {
	cells := make([]s2.CellID, 0, n)
	seen := make(map[s2.CellID]struct{}, n)
	for i := 0; len(cells) < n; i++ {
		c := modTestCell(50.0+0.008*float64(i%250), -3.0+0.012*float64(i/250))
		if _, dup := seen[c]; !dup {
			seen[c] = struct{}{}
			cells = append(cells, c)
		}
	}
	return cells
}

// modLiveNoise runs a live zone-wide +5 event over cells for checks checks,
// perH an hour, a share noise of them base results (level-capped spawns),
// counting each new cell for the learner as the runtime does, and returns
// the ends by reason and the most cells a modifier excluded.
func modLiveNoise(cells []s2.CellID, checks, perH int, noise float64) (ends map[string]int, maxExcluded int) {
	rng := rand.New(rand.NewPCG(4, 4))
	l := newModifierLearner(defaultModifierParams())
	seen := make(map[s2.CellID]struct{}, len(cells))
	ends = map[string]int{}
	for i := range checks {
		ts := modT0.Add(time.Duration(i) * time.Hour / time.Duration(perH))
		c := cells[rng.IntN(len(cells))]
		if _, ok := seen[c]; !ok {
			seen[c] = struct{}{}
			l.cellSeen(modZone)
		}
		ev := modCheck(ts, c, withLevelDelta(5))
		if rng.Float64() < noise {
			ev = modCheck(ts, c)
		}
		for _, tr := range l.feed(ev, modZone) {
			if tr.State == modEnded {
				ends[tr.Mod.EndReason]++
			}
		}
		for _, tk := range l.activeTracks {
			maxExcluded = max(maxExcluded, len(tk.mod.Excluded))
		}
	}
	return ends, maxExcluded
}

// TestModifierScaledCapLargeZone: a live event over a 150,000-cell zone at
// 30,000 checks an hour with 0.8% base results has, at any time, more than
// 256 cells whose latest check disproved it. The cap grows with the cells
// seen (1%), so the event is not ended for it (at 256 it was).
func TestModifierScaledCapLargeZone(t *testing.T) {
	ends, maxExcluded := modLiveNoise(modBigZone(150_000), 4*30_000, 30_000, 0.008)
	if len(ends) != 0 {
		t.Errorf("a live event ended: %v (most cells excluded %d)", ends, maxExcluded)
	}
	if maxExcluded <= modExcludedMin {
		t.Fatalf("at most %d cells excluded: the scaled cap was not exercised", maxExcluded)
	}
}
