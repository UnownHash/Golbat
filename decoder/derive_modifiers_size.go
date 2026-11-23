package decoder

import (
	"time"

	"github.com/golang/geo/s2"

	"golbat/derive/encounter"
)

// Size cutoff modifiers (design §4, §8–§10). Each moved size cut is its own
// modifier: one track per species scope, zone and boundary, with its own
// zone record, spread, ageing, exclusions and lifecycle, so a boundary seen
// only at a venue never rides on another's coverage. A track has no gate:
// its candidate needs Start deviations across its cut in Window whatever
// agrees. Its evidence is the interval the moved cut lies in: the highest
// roll seen of a class under the cut and the lowest of a class over it —
// the pending deviations at the candidate, then every IV-agreeing located
// roll in the zone. Only rolls the interval accepts are evidence, and only
// accepted deviations across the cut are observations.
//
// A size modifier has no venue cells. Most rolls say nothing about a moved
// cut, so its zone record takes only the informative ones: an accepted
// deviation across the cut (a match), a contrary roll (one the interval
// refuses that the base cut classifies: an agreement), and any other roll
// the interval refuses (another effect). Every other located roll still
// re-judges the record, so its entries age while the scope is seen.
//
// A contrary or refused roll disproves the boundary in its cell, and the
// cell keeps that roll: it is readmitted only by a later roll there that
// the interval accepts and that contradicts the disproving one (design
// §10). A match on the near side of the disproving roll does not: both fit
// a cut lying between them, local to the cell.

// sizeCut is one size class boundary: classes <= below lie under it.
type sizeCut struct {
	name  string
	below uint8
	at    float64
}

// The cuts encounter.size and encounter.strongSize classify a roll with
// (at most modSizeCutsMax).
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

// sizeCutIndex is the index of the boundary name in the strong or ordinary
// cut list, -1 when it has none.
func sizeCutIndex(strong bool, name string) int {
	for i, c := range sizeCuts(strong) {
		if c.name == name {
			return i
		}
	}
	return -1
}

// baseSizeCuts returns the base cut of each boundary, by name, for a strong
// or ordinary encounter. The map is shared and must not be modified.
func baseSizeCuts(strong bool) map[string]float64 {
	if strong {
		return strongSizeCutMap
	}
	return baseSizeCutMap
}

// crosses reports whether a roll of class cls deviates across c: the base
// cut puts it on the other side.
func (c sizeCut) crosses(cls uint8, roll float64) bool {
	return (cls <= c.below) != (roll < c.at)
}

// with returns b narrowed by a roll seen under the cut (lower: the cut lies
// above it) or over it (the cut lies at or below it), and whether that
// changed b.
func (b sizeBoundary) with(lower bool, roll float64) (sizeBoundary, bool) {
	switch {
	case lower && (!b.HasLo || roll > b.Lo):
		b.Lo, b.HasLo = roll, true
	case !lower && (!b.HasHi || roll < b.Hi):
		b.Hi, b.HasHi = roll, true
	default:
		return b, false
	}
	return b, true
}

// empty: no cut fits, a roll under the cut at or above one over it.
func (b sizeBoundary) empty() bool { return b.HasLo && b.HasHi && b.Lo >= b.Hi }

// sizeRollKind is what a roll is to a boundary's interval.
type sizeRollKind uint8

const (
	// sizeRollAccepted: the interval holds with the roll (and may narrow).
	sizeRollAccepted sizeRollKind = iota
	// sizeRollContrary: the interval refuses the roll and the base cut
	// classifies it: the cut has not moved for this roll.
	sizeRollContrary
	// sizeRollRefused: the interval refuses the roll and so does the base
	// cut: the cut moved, but not to where the interval has it.
	sizeRollRefused
)

// fit judges a roll of class cls against c's interval b: the kind, and b
// with the roll when accepted (changed when that narrowed it).
func (b sizeBoundary) fit(c sizeCut, cls uint8, roll float64) (kind sizeRollKind, next sizeBoundary, changed bool) {
	lower := cls <= c.below
	next, changed = b.with(lower, roll)
	switch {
	case !next.empty():
		return sizeRollAccepted, next, changed
	case lower == (roll < c.at):
		return sizeRollContrary, b, false
	}
	return sizeRollRefused, b, false
}

// sizeDisproof is what disproved a boundary in one cell: the highest
// refused roll of a class over the cut (the cell's cut lies at or below
// it), and the lowest refused roll of a class under it (the cell's cut lies
// above it).
type sizeDisproof struct {
	upper, lower       float64
	hasUpper, hasLower bool
}

// sizeState is a size track's cut and, per excluded cell, what disproved
// its modifier there.
type sizeState struct {
	cut      sizeCut
	disproof map[s2.CellID]sizeDisproof
}

// sizeDisprove applies a roll t's interval refuses, seen in cell on the
// side lower: the cell is excluded and keeps the roll, the hardest of its
// side to contradict.
func (l *modifierLearner) sizeDisprove(t *modTrack, cell s2.CellID, lower bool, roll float64) {
	if !l.exclude(t, cell) {
		return
	}
	if t.size.disproof == nil {
		t.size.disproof = make(map[s2.CellID]sizeDisproof)
	}
	d := t.size.disproof[cell]
	if lower {
		if !d.hasLower || roll < d.lower {
			d.lower, d.hasLower = roll, true
		}
	} else if !d.hasUpper || roll > d.upper {
		d.upper, d.hasUpper = roll, true
	}
	t.size.disproof[cell] = d
}

// sizeResolve applies a roll t's interval accepts, seen in cell on the side
// lower, to the cell's disproof: a roll under the cut at or above the
// disproving roll over it (or the reverse) contradicts that roll, and the
// cell is readmitted once nothing disproves it.
func (l *modifierLearner) sizeResolve(t *modTrack, cell s2.CellID, lower bool, roll float64) {
	d, ok := t.size.disproof[cell]
	if !ok {
		return
	}
	if lower && d.hasUpper && roll >= d.upper {
		d.hasUpper = false
	}
	if !lower && d.hasLower && roll <= d.lower {
		d.hasLower = false
	}
	if d.hasUpper || d.hasLower {
		t.size.disproof[cell] = d
		return
	}
	delete(t.size.disproof, cell)
	l.readmit(t, cell)
}

// sizeOpinion reports whether ev says anything about the size roll. The
// strong walk produces only XL and XXL: any other class on a strong pokemon
// is not that walk's and is ignored.
func sizeOpinion(ev modifierEvent) bool {
	if ev.ObsSize < 1 || ev.ObsSize > 5 || encounter.FormFixesSize(ev.Form) {
		return false
	}
	return !ev.Strong || ev.ObsSize >= 4
}

// sizeCheck applies an IV-agreeing check's size roll to each boundary of
// the scope's cut list: a deviation across a cut is pending for that
// boundary's track until Start of them open a candidate; for a boundary
// with a modifier every located roll is evidence.
func (l *modifierLearner) sizeCheck(st *modScopeState, sk modScopeKey, ev modifierEvent, now time.Time,
	out []modifierTransition) []modifierTransition {
	disagree := ev.Agree.Size == dimDisagree
	if (!disagree && ev.Agree.Size != dimAgree) || !sizeOpinion(ev) {
		return out
	}
	for i, c := range sizeCuts(sk.Scope.Strong) {
		d := modDimSize + modDim(i)
		dev := disagree && c.crosses(ev.ObsSize, ev.SizeRoll)
		var t *modTrack
		if dev {
			t = l.track(st, sk, d, modIDSizeMatch)
		} else if t = st.activeTrack(d); t == nil {
			continue
		}
		if t.mod != nil {
			l.sizeRoll(t, st.record(d), ev, dev, now)
		} else if t.addPending(ev, l.p) {
			out = l.sizeOpen(t, st, now, out)
		}
		out = l.judge(st, d, now, out)
	}
	return out
}

// sizeOpen makes t's pending deviations a candidate. Each roll the interval
// accepts is an observation, and with a cell evidence and a match in the
// record; a refused one is a contradiction, in the record after the matches
// whatever their order (it stays there longest, and its cell starts
// excluded: conservative).
func (l *modifierLearner) sizeOpen(t *modTrack, st *modScopeState, now time.Time,
	out []modifierTransition) []modifierTransition {
	c := t.size.cut
	mod := l.open(t, now)
	t.size.disproof = nil
	for i := range t.pending {
		pd := &t.pending[i]
		kind, next, _ := mod.Interval.fit(c, pd.Class, pd.Roll)
		if pd.Void = kind != sizeRollAccepted; pd.Void {
			mod.Contradictions++
			continue
		}
		mod.Observations++
		if pd.Cell == 0 {
			continue
		}
		mod.Interval = next
		t.located++
	}
	rec := st.record(t.dim)
	*rec = modRecord{}
	for _, pd := range t.pending {
		if pd.Cell != 0 && !pd.Void {
			rec.push(pd.Cell, modIDSizeMatch, false, pd.Unix)
		}
	}
	for _, pd := range t.pending {
		if pd.Cell != 0 && pd.Void {
			rec.push(pd.Cell, modIDSizeOther, false, pd.Unix)
			l.sizeDisprove(t, pd.Cell, pd.Class <= c.below, pd.Roll)
		}
	}
	return l.opened(t, rec, now, out)
}

// sizeRoll applies an IV-agreeing roll (dev: a deviation across t's cut) to
// t's modifier. Without a cell a deviation is an observation and nothing
// else. A contrary roll is an agreement in the record, counts towards the
// pause and disproves the boundary in its cell; another roll the interval
// refuses is a contradiction and does the same. Any other is evidence and
// answers the cell's disproof when it contradicts it; a deviation is also a
// match. A roll that enters nothing still marks the record checked.
func (l *modifierLearner) sizeRoll(t *modTrack, rec *modRecord, ev modifierEvent, dev bool, now time.Time) {
	m := t.mod
	c := t.size.cut
	if ev.Cell != 0 {
		lower := ev.ObsSize <= c.below
		kind, next, changed := m.Interval.fit(c, ev.ObsSize, ev.SizeRoll)
		switch kind {
		case sizeRollContrary:
			rec.push(ev.Cell, 0, true, ev.Unix)
			l.sizeDisprove(t, ev.Cell, lower, ev.SizeRoll)
			l.agreed(m)
			return
		case sizeRollRefused:
			m.Contradictions++
			rec.push(ev.Cell, modIDSizeOther, false, ev.Unix)
			l.changed(m, modEvidence)
			l.sizeDisprove(t, ev.Cell, lower, ev.SizeRoll)
			return
		}
		if changed {
			m.Interval = next
			l.changed(m, modShrink) // a moved interval can reclassify a record
		}
		l.sizeResolve(t, ev.Cell, lower, ev.SizeRoll)
		if dev {
			t.located++
			rec.push(ev.Cell, modIDSizeMatch, false, ev.Unix)
			l.matched(m)
		} else {
			rec.touch(ev.Unix)
		}
	}
	if !dev {
		return
	}
	m.Observations++
	l.changed(m, modEvidence)
	if now.After(m.LastSeen) {
		m.LastSeen = now
	}
}
