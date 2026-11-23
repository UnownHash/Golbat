package decoder

import (
	"math"
	"time"

	"golbat/derive/encounter"
)

// Size cutoff modifiers (design §4, §8). A size_cutoffs track is one per
// species scope and zone with no effect key and no gate: its candidate needs
// Start size deviations in Window whatever agrees. Its evidence is, per
// observed size class, the lowest and highest roll seen — the pending
// deviations at the candidate, then every IV-agreeing located roll in the
// zone — so a boundary k/k+1's interval is [the highest roll of a class <= k,
// the lowest of a class > k]. A modifier lists the boundaries whose interval
// excludes the base cut; once confirmed, a new one only after Start
// deviations across that cut. Only deviations the interval fit accepts are
// observations.
//
// A size modifier is zone-wide only. Most rolls say nothing about a moved
// cut, so its zone record takes only the informative ones: a deviation
// across a listed boundary (a match), a contrary roll (one in a listed
// boundary's disputed region that the base cut classifies: an agreement),
// and a roll the fit refuses (another effect).

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

// sizeState is a size track's evidence and, per cut of its cut list, the
// accepted located deviations across it.
type sizeState struct {
	ev      sizeEvidence
	support [4]uint8
}

// crosses reports whether a roll of class cls deviates across c: the base
// cut puts it on the other side.
func (c sizeCut) crosses(cls uint8, roll float64) bool {
	return (cls <= c.below) != (roll < c.at)
}

// note counts an accepted located deviation for each cut it crosses.
func (s *sizeState) note(cuts []sizeCut, cls uint8, roll float64) {
	for i, c := range cuts {
		if c.crosses(cls, roll) && s.support[i] < math.MaxUint8 {
			s.support[i]++
		}
	}
}

// fill sets dst to the boundaries whose interval excludes their base cut
// and reports whether that changed dst. A boundary not yet in dst is listed
// only with need deviations across its cut. dst holds only cuts' names.
func (s *sizeState) fill(dst map[string]sizeBoundary, cuts []sizeCut, need int) bool {
	changed := false
	for i, c := range cuts {
		b := s.ev.boundary(c)
		old, had := dst[c.name]
		moved := !b.empty() && ((b.HasLo && b.Lo >= c.at) || (b.HasHi && b.Hi < c.at))
		switch {
		case !moved:
			if had {
				delete(dst, c.name)
				changed = true
			}
		case had:
			if old != b {
				dst[c.name], changed = b, true
			}
		case int(s.support[i]) >= need:
			dst[c.name], changed = b, true
		}
	}
	return changed
}

// dropUnsupported removes from dst the boundaries with fewer than need
// deviations across their cut: a confirmed modifier lists none (they return
// through fill once supported).
func (s *sizeState) dropUnsupported(dst map[string]sizeBoundary, cuts []sizeCut, need int) {
	for i, c := range cuts {
		if int(s.support[i]) < need {
			delete(dst, c.name)
		}
	}
}

// crossesListed reports whether a roll of class cls deviates across a
// listed boundary: it supports that moved cut.
func crossesListed(listed map[string]sizeBoundary, cuts []sizeCut, cls uint8, roll float64) bool {
	for _, c := range cuts {
		if _, ok := listed[c.name]; ok && c.crosses(cls, roll) {
			return true
		}
	}
	return false
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

// sizeOpinion reports whether ev says anything about the size roll. The
// strong walk produces only XL and XXL: any other class on a strong pokemon
// is not that walk's and is ignored.
func sizeOpinion(ev modifierEvent) bool {
	if ev.ObsSize < 1 || ev.ObsSize > 5 || encounter.FormFixesSize(ev.Form) {
		return false
	}
	return !ev.Strong || ev.ObsSize >= 4
}

// sizeCheck applies an IV-agreeing check's size roll: a deviation is pending
// for the scope's size track until Start of them open a candidate; with a
// modifier every located roll is evidence.
func (l *modifierLearner) sizeCheck(st *modScopeState, sk modScopeKey, ev modifierEvent, now time.Time,
	out []modifierTransition) []modifierTransition {
	dev := ev.Agree.Size == dimDisagree
	if (!dev && ev.Agree.Size != dimAgree) || !sizeOpinion(ev) {
		return out
	}
	var t *modTrack
	if dev {
		t = l.track(st, sk, modDimSize, modIDSizeMatch)
	} else {
		for _, o := range st.tracks {
			if o.dim == modDimSize && o.mod != nil {
				t = o
			}
		}
		if t == nil {
			return out
		}
	}
	if t.mod != nil {
		l.sizeRoll(t, st.record(modDimSize), ev, dev, now)
	} else if t.addPending(ev, l.p) {
		out = l.sizeOpen(t, st, now, out)
	}
	return l.judge(st, modDimSize, now, out)
}

// sizeOpen makes t's pending deviations a candidate. Each roll the interval
// fit accepts is an observation, and with a cell evidence and a match in the
// record; a refused one is a contradiction, in the record after the matches
// whatever their order (it stays there longest: conservative).
func (l *modifierLearner) sizeOpen(t *modTrack, st *modScopeState, now time.Time,
	out []modifierTransition) []modifierTransition {
	cuts := sizeCuts(t.key.Scope.Strong)
	mod := l.open(t, now)
	*t.size = sizeState{}
	for i := range t.pending {
		pd := &t.pending[i]
		next, changed, refused := t.size.ev.try(pd.Class, pd.Roll, cuts)
		if pd.Void = refused; refused {
			mod.Contradictions++
			continue
		}
		mod.Observations++
		if pd.Cell == 0 {
			continue
		}
		if changed {
			t.size.ev = next
		}
		t.size.note(cuts, pd.Class, pd.Roll)
		t.located++
	}
	mod.Boundaries = make(map[string]sizeBoundary, len(cuts))
	t.size.fill(mod.Boundaries, cuts, 0)
	rec := st.record(modDimSize)
	*rec = modRecord{}
	for _, pd := range t.pending {
		if pd.Cell != 0 && !pd.Void && crossesListed(mod.Boundaries, cuts, pd.Class, pd.Roll) {
			rec.push(pd.Cell, modIDSizeMatch, false, pd.Unix)
		}
	}
	for _, pd := range t.pending {
		if pd.Cell != 0 && pd.Void {
			rec.push(pd.Cell, modIDSizeOther, false, pd.Unix)
		}
	}
	return l.opened(t, rec, now, out)
}

// sizeRoll applies an IV-agreeing roll (dev: a size deviation) to t's
// modifier. Without a cell a deviation is an observation and nothing else. A
// contrary roll is an agreement in the record; one the fit refuses a
// contradiction; any other is evidence, and a deviation across a listed
// boundary a match.
func (l *modifierLearner) sizeRoll(t *modTrack, rec *modRecord, ev modifierEvent, dev bool, now time.Time) {
	m := t.mod
	cuts := sizeCuts(t.key.Scope.Strong)
	if ev.Cell != 0 {
		if contraryRoll(m.Boundaries, cuts, ev.ObsSize, ev.SizeRoll) {
			rec.push(ev.Cell, 0, true, ev.Unix)
			return
		}
		next, changed, refused := t.size.ev.try(ev.ObsSize, ev.SizeRoll, cuts)
		if refused {
			m.Contradictions++
			rec.push(ev.Cell, modIDSizeOther, false, ev.Unix)
			l.changed(m, modEvidence)
			return
		}
		if changed {
			t.size.ev = next
		}
		if dev {
			t.size.note(cuts, ev.ObsSize, ev.SizeRoll)
			t.located++
		}
		need := 0
		if m.Status == modConfirmed {
			need = l.p.Start
		}
		if (changed || dev) && t.size.fill(m.Boundaries, cuts, need) {
			l.changed(m, modShrink) // moved cuts can reclassify a record
		}
		if dev && crossesListed(m.Boundaries, cuts, ev.ObsSize, ev.SizeRoll) {
			rec.push(ev.Cell, modIDSizeMatch, false, ev.Unix)
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
