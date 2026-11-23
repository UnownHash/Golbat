package decoder

import (
	"time"

	"golbat/derive/encounter"
)

// Size cutoff modifiers (design §4, eventreplay's size handling). A
// size_cutoffs track is one per species scope and zone with no effect key,
// and no gate: its candidate needs Start size deviations in Window whatever
// agrees. Its evidence is, per observed size class, the lowest and highest
// roll seen — the pending deviations at the candidate, then every
// IV-agreeing roll in an in-cell — so a boundary k/k+1's interval is [the
// highest roll of a class <= k, the lowest of a class > k]. A modifier lists
// the boundaries whose interval excludes the base cut. Agreements neither
// count toward the end nor mark a cell outside: a contrary roll (one in a
// listed boundary's disputed region that the base cut classifies) ends the
// modifier in an in-cell and marks any other cell outside.

// sizeCut is one size class boundary: classes <= below lie under it.
type sizeCut struct {
	name  string
	below uint8
	at    float64
}

// The cuts encounter.size and encounter.strongSize classify a roll with.
var (
	baseSizeCutList = []sizeCut{
		{"1/2", 1, 0.004}, {"2/3", 2, 0.029}, {"3/4", 3, 0.971}, {"4/5", 4, 0.996},
	}
	strongSizeCutList = []sizeCut{{"4/5", 4, 0.8}}
	baseSizeCutMap    = sizeCutMap(baseSizeCutList)
	strongSizeCutMap  = sizeCutMap(strongSizeCutList)
)

func sizeCutMap(cuts []sizeCut) map[string]float64 {
	m := make(map[string]float64, len(cuts))
	for _, c := range cuts {
		m[c.name] = c.at
	}
	return m
}

func sizeCuts(strong bool) []sizeCut {
	if strong {
		return strongSizeCutList
	}
	return baseSizeCutList
}

// baseSizeCuts returns the base cut of each boundary, by name, for a strong
// or ordinary encounter. The map is shared and must not be modified.
func baseSizeCuts(strong bool) map[string]float64 {
	if strong {
		return strongSizeCutMap
	}
	return baseSizeCutMap
}

// sizeClassRolls is the lowest and highest roll seen of one class.
type sizeClassRolls struct {
	lo, hi float64
	seen   bool
}

// sizeEvidence is a size track's rolls, by observed class 1..5. It never
// holds an empty interval (try refuses the roll that would make one), so
// every boundary it gives has Lo < Hi.
type sizeEvidence [5]sizeClassRolls

// with returns e with roll added to class cls, and whether that changed it.
func (e *sizeEvidence) with(cls uint8, roll float64) (sizeEvidence, bool) {
	out := *e
	r := &out[cls-1]
	switch {
	case !r.seen:
		*r = sizeClassRolls{lo: roll, hi: roll, seen: true}
	case roll < r.lo:
		r.lo = roll
	case roll > r.hi:
		r.hi = roll
	default:
		return out, false
	}
	return out, true
}

// boundary is c's interval: the highest roll of a class under c, the lowest
// of a class over it.
func (e *sizeEvidence) boundary(c sizeCut) sizeBoundary {
	var b sizeBoundary
	for i, r := range e {
		if !r.seen {
			continue
		}
		if uint8(i+1) <= c.below {
			if !b.HasLo || r.hi > b.Lo {
				b.Lo, b.HasLo = r.hi, true
			}
		} else if !b.HasHi || r.lo < b.Hi {
			b.Hi, b.HasHi = r.lo, true
		}
	}
	return b
}

func (b sizeBoundary) empty() bool { return b.HasLo && b.HasHi && b.Lo >= b.Hi }

// try returns e with roll added to class cls and whether that changed it,
// or refused when the roll would empty an interval: a roll of a class under
// a cut at or above one of a class over it.
func (e *sizeEvidence) try(cls uint8, roll float64, cuts []sizeCut) (next sizeEvidence, changed, refused bool) {
	next, changed = e.with(cls, roll)
	if !changed {
		return next, false, false
	}
	for _, c := range cuts {
		if next.boundary(c).empty() {
			return *e, false, true
		}
	}
	return next, true, false
}

// fill sets dst to the boundaries whose interval excludes their base cut.
func (e *sizeEvidence) fill(dst map[string]sizeBoundary, cuts []sizeCut) {
	clear(dst)
	for _, c := range cuts {
		b := e.boundary(c)
		if !b.empty() && ((b.HasLo && b.Lo >= c.at) || (b.HasHi && b.Hi < c.at)) {
			dst[c.name] = b
		}
	}
}

// seed resets e to the rolls of the pending deviations with a cell, in
// time order, leaving out one that would empty an interval, and returns the
// listed boundaries.
func (e *sizeEvidence) seed(pending []modPending, cuts []sizeCut) map[string]sizeBoundary {
	*e = sizeEvidence{}
	for _, pd := range pending {
		if pd.Cell != 0 && pd.Class >= 1 && pd.Class <= 5 {
			*e, _, _ = e.try(pd.Class, pd.Roll, cuts)
		}
	}
	out := make(map[string]sizeBoundary, len(cuts))
	e.fill(out, cuts)
	return out
}

// contrary reports whether a roll of class cls lies in a listed boundary's
// disputed region (between its base cut and its interval) on the side the
// base cut classifies it: the moved cut is gone.
func contraryRoll(listed map[string]sizeBoundary, cuts []sizeCut, cls uint8, roll float64) bool {
	for _, c := range cuts {
		b, ok := listed[c.name]
		if !ok {
			continue
		}
		if cls > c.below && b.HasLo && b.Lo >= c.at && c.at <= roll && roll < b.Lo {
			return true // cut moved up, a roll under it seen above
		}
		if cls <= c.below && b.HasHi && b.Hi < c.at && b.Hi <= roll && roll < c.at {
			return true // cut moved down, a roll over it seen below
		}
	}
	return false
}

// sizeOpinion reports whether ev says anything about the size roll.
func sizeOpinion(ev modifierEvent) bool {
	return ev.ObsSize >= 1 && ev.ObsSize <= 5 && !encounter.FormFixesSize(ev.Form)
}

// sizeTrack returns the size_cutoffs track of scope in zone, creating it.
func (l *modifierLearner) sizeTrack(scope modifierScope, zone string) *modTrack {
	t := l.track(modTrackKey{modSizeCutoffs, scope, modifierEffect{}, zone}, modDimSize, false)
	if t.size == nil {
		t.gate, t.size = false, new(sizeEvidence)
	}
	return t
}

// sizeDeviation counts an IV-agreeing size deviation for its species'
// track. With a modifier active, a roll that would empty an interval is a
// contradiction (in an in-cell) and otherwise left out, unmatched; any other
// roll with a cell is evidence (its cell becomes an in-cell).
func (l *modifierLearner) sizeDeviation(ev modifierEvent, species modifierScope, zone string,
	now time.Time, out []modifierTransition) []modifierTransition {
	if !sizeOpinion(ev) {
		return out
	}
	t := l.sizeTrack(species, zone)
	if mod := t.mod; mod != nil {
		cuts := sizeCuts(t.key.Scope.Strong)
		next, changed, refused := t.size.try(ev.ObsSize, ev.SizeRoll, cuts)
		if refused {
			if _, in := mod.InCells[ev.Cell]; in && ev.Cell != 0 {
				mod.Contradictions++
			}
			return out
		}
		if changed && ev.Cell != 0 {
			*t.size = next
			t.size.fill(mod.Boundaries, cuts)
		}
	}
	return l.match(t, ev, now, out)
}

// sizeAgreement applies an IV- and size-agreeing check to t's modifier: a
// contrary roll ends it in an in-cell and marks any other cell outside; any
// other roll in an in-cell is evidence unless it would empty an interval.
func (l *modifierLearner) sizeAgreement(t *modTrack, ev modifierEvent, now time.Time,
	out []modifierTransition) []modifierTransition {
	mod := t.mod
	if mod == nil || ev.Cell == 0 || !sizeOpinion(ev) {
		return out
	}
	cuts := sizeCuts(t.key.Scope.Strong)
	_, in := mod.InCells[ev.Cell]
	if contraryRoll(mod.Boundaries, cuts, ev.ObsSize, ev.SizeRoll) {
		if in {
			return append(out, l.end(t, now, "contrary"))
		}
		mod.Outside[ev.Cell] = struct{}{}
		return out
	}
	if !in {
		return out
	}
	if next, changed, _ := t.size.try(ev.ObsSize, ev.SizeRoll, cuts); changed {
		*t.size = next
		t.size.fill(mod.Boundaries, cuts)
	}
	return out
}
