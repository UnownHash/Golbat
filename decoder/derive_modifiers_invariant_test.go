package decoder

import (
	"math/rand/v2"
	"testing"
	"time"
)

// The index applies at most one modifier per scope, type and slot in a
// zone, without dispute handling (derive_modifiers_apply.go lookup). These
// tests pin the invariant that rests on: no two effects of one record are
// on together, under any mix of the turn-on and stay-on rules.

// TestModifierRecordOneVerdictOn: random records (fresh and aged entries,
// agreements, neutral pin agreements in either ring and two or three effect
// ids over spread cells) never have two ids on at once, whichever of them
// is judged to turn on (three quarters) and which to stay on (more than
// half).
func TestModifierRecordOneVerdictOn(t *testing.T) {
	p := defaultModifierParams()
	needs := []modJudge{modJudgeOn(p), modJudgeStay(p)}
	cells := modSpreadCells(12)
	rng := rand.New(rand.NewPCG(1, 2))
	const now, cut = int64(10_000), int64(10_000 - 3600)
	onSeen := 0
	for iter := range 20_000 {
		nIDs := 2 + rng.IntN(2)
		var rec modRecord
		for range 1 + rng.IntN(2*modRecordSize) {
			unix := now - int64(rng.IntN(5400)) // a third aged
			id := int32(1 + rng.IntN(nIDs))
			agree := rng.IntN(4) == 0
			if agree && rng.IntN(2) == 0 {
				id = 99 // an agreement on a move no pin holds
			}
			if agree && id != 99 && rng.IntN(2) == 0 {
				// An agreement on the move of a pin that has a modifier.
				rec.pushPinEqual(cells[rng.IntN(len(cells))], id, unix)
				continue
			}
			rec.push(cells[rng.IntN(len(cells))], id, agree, unix)
		}
		for _, pin := range []bool{false, true} {
			// Each id judged under each rule: no assignment puts two on.
			for a := int32(1); a <= int32(nIDs); a++ {
				for b := a + 1; b <= int32(nIDs); b++ {
					for _, na := range needs {
						for _, nb := range needs {
							va := rec.judge(a, pin, na, cut)
							vb := rec.judge(b, pin, nb, cut)
							if va.on {
								onSeen++
							}
							if va.on && vb.on {
								t.Fatalf("iter %d pin %v: ids %d (%v) and %d (%v) both on", iter, pin, a, na, b, nb)
							}
						}
					}
				}
			}
		}
	}
	if onSeen == 0 {
		t.Fatal("no verdict was ever on: the property was not exercised")
	}
}

// TestModifierLearnerOneAppliedPerSlot: over a randomized feed (dominant
// effects that change, noise, neutral pin agreements, gaps that age the
// record, two zones), no published index and no index built from the
// learner's state after any event holds two modifiers of one scope, type
// and slot in one zone.
func TestModifierLearnerOneAppliedPerSlot(t *testing.T) {
	cells := append(modWideCells(12), modSpreadCells(10)...) // London and Paris
	for seed := range uint64(4) {
		r := newModifierRuntime(defaultModifierParams(), 1)
		withModifierRuntime(t, r)
		rng := rand.New(rand.NewPCG(seed, 7))
		ts := modT0
		moves := []int32{494, 300, 301}
		deltas := []int8{5, 3, -2}
		dominant, dominantDelta := moves[0], deltas[0]
		applied := map[modifierEffect]bool{}
		for i := range 6000 {
			if i%400 == 0 {
				dominant, dominantDelta = moves[rng.IntN(len(moves))], deltas[rng.IntN(len(deltas))]
			}
			ts = ts.Add(time.Duration(10+rng.IntN(120)) * time.Second)
			if rng.IntN(300) == 0 {
				ts = ts.Add(time.Duration(rng.IntN(7200)) * time.Second)
			}
			var opts []func(*modifierEvent)
			switch x := rng.IntN(10); {
			case x < 6:
				opts = append(opts, withCharged(dominant))
			case x < 8:
				opts = append(opts, withCharged(moves[rng.IntN(len(moves))]))
			case x < 9:
				// The base walk gives the dominant move: a neutral agreement.
				opts = append(opts, func(ev *modifierEvent) { ev.ObsCharged = dominant })
			}
			switch x := rng.IntN(10); {
			case x < 6:
				opts = append(opts, withLevelDelta(dominantDelta))
			case x < 8:
				opts = append(opts, withLevelDelta(deltas[rng.IntN(len(deltas))]))
			}
			ev := modCheck(ts, cells[rng.IntN(len(cells))], opts...)
			r.handle(ev)
			checkOneAppliedPerSlot(t, seed, i, derivationModifierIndex.Load())
			idx := buildModifierIndex(r.activeCopies())
			checkOneAppliedPerSlot(t, seed, i, idx)
			if idx != nil {
				for _, entries := range idx.confirmed {
					for _, e := range entries {
						applied[e.effect] = true
					}
				}
			}
		}
		// Pins and level offsets taking over from one another.
		if len(applied) < 4 {
			t.Fatalf("seed %d: applied effects %v, want at least 4: the invariant was not exercised", seed, applied)
		}
	}
}

func checkOneAppliedPerSlot(t *testing.T, seed uint64, i int, idx *modifierIndex) {
	t.Helper()
	if idx == nil {
		return
	}
	type key struct {
		typ       modifierType
		slot, cut string
		zone      string
	}
	for scope, entries := range idx.confirmed {
		seen := map[key]modifierEffect{}
		for _, e := range entries {
			k := key{e.typ, e.effect.Slot, e.effect.Cut, e.zone}
			if prev, ok := seen[k]; ok {
				t.Fatalf("seed %d event %d: scope %+v has two applied %v in %s: %+v and %+v",
					seed, i, scope, e.typ, e.zone, prev, e.effect)
			}
			seen[k] = e.effect
		}
	}
}
