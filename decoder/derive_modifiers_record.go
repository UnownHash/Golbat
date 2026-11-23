package decoder

import (
	"slices"
	"time"

	"github.com/golang/geo/s2"
)

// The zone record (design §8): per scope, zone and dimension, the last
// modRecordSize located checks, each an agreement or the effect seen, with
// its cell. It exists from the scope's first check, before any modifier, and
// zone-wide coverage is judged on it: level-14 cells are too fine to count
// evidence when encounters are sampled.

const modRecordSize = 16

// modZoneWideSpreadLevel: a zone-wide modifier needs matches under at least
// two cells of this level (~10 km), so one venue stays local.
const modZoneWideSpreadLevel = 10

// Effect ids of the dimensions whose tracks have no effect key: an IV
// mismatch, and for size a roll that supports the moved cuts or one the
// interval fit refuses.
const (
	modIDMismatch  int32 = 1
	modIDSizeMatch int32 = 1
	modIDSizeOther int32 = 2
)

// modRecEntry is one located check: an agreement with the base value, or
// the effect seen (a level delta, a move, or one of the ids above). An
// agreement on a move slot carries the observed move in id.
type modRecEntry struct {
	cell  s2.CellID
	id    int32
	agree bool
}

type modRecord struct {
	buf     [modRecordSize]modRecEntry
	head, n uint8
	last    int64 // unix time of the latest entry
}

func (r *modRecord) push(cell s2.CellID, id int32, agree bool, unix int64) {
	r.buf[r.head] = modRecEntry{cell: cell, id: id, agree: agree}
	r.head = (r.head + 1) % modRecordSize
	if r.n < modRecordSize {
		r.n++
	}
	r.last = max(r.last, unix)
}

// modVerdict is a record's judgement of one effect.
type modVerdict struct {
	// on: the effect is more than half of the record, the record holds at
	// least confirm entries, and its matches lie under two level-10 cells.
	on bool
	// byEffect: the latest entry is another effect (what displaced this one
	// when on turns false).
	byEffect bool
	// excluded holds, sorted, the nExcluded cells whose latest entry is an
	// agreement.
	excluded  [modRecordSize]s2.CellID
	nExcluded int
}

func (v *modVerdict) cells() []s2.CellID { return v.excluded[:v.nExcluded] }

// judge evaluates effect id against the record. For a move pin an agreement
// whose observed move is the pinned one is neutral (the base walk and the
// pin give the same move): it is left out of the majority and excludes no
// cell.
func (r *modRecord) judge(id int32, pin bool, confirm int) modVerdict {
	var (
		v              modVerdict
		seen           [modRecordSize]s2.CellID
		nSeen          int
		match, neutral int
		parent         s2.CellID
		spread         bool
	)
	for i := 1; i <= int(r.n); i++ { // newest first
		e := r.buf[(int(r.head)-i+modRecordSize)%modRecordSize]
		if e.agree && pin && e.id == id {
			neutral++
			continue
		}
		mine := !e.agree && e.id == id
		if i == 1 {
			v.byEffect = !e.agree && !mine
		}
		if mine {
			match++
			if p := e.cell.Parent(modZoneWideSpreadLevel); parent == 0 {
				parent = p
			} else if p != parent {
				spread = true
			}
		}
		if slices.Contains(seen[:nSeen], e.cell) {
			continue
		}
		seen[nSeen] = e.cell
		nSeen++
		if e.agree {
			v.excluded[v.nExcluded] = e.cell
			v.nExcluded++
		}
	}
	slices.Sort(v.excluded[:v.nExcluded])
	v.on = int(r.n) >= confirm && 2*match > int(r.n)-neutral && spread
	return v
}

// modScopeState is what the learner holds for one scope in one zone: its
// tracks and its zone record per dimension (nil until the first check).
type modScopeState struct {
	tracks []*modTrack
	rec    [modDimCount]*modRecord
}

func (st *modScopeState) record(d modDim) *modRecord {
	if st.rec[d] == nil {
		st.rec[d] = new(modRecord)
	}
	return st.rec[d]
}

// active reports whether a track of dimension d has a modifier.
func (st *modScopeState) active(d modDim) bool {
	for _, t := range st.tracks {
		if t.dim == d && t.mod != nil {
			return true
		}
	}
	return false
}

// markSeen notes a check or sighting of the scope in cell for the modifiers
// covering it.
func (st *modScopeState) markSeen(zone string, cell s2.CellID, now time.Time) {
	for _, t := range st.tracks {
		if m := t.mod; m != nil && now.After(m.LastSeen) && m.Covers(zone, cell) {
			m.LastSeen = now
		}
	}
}

// judgeZone applies rec's verdict to t's modifier: zone-wide coverage on or
// off (never on in the unknown zone), and its excluded cells.
func (l *modifierLearner) judgeZone(t *modTrack, rec *modRecord) {
	m := t.mod
	v := rec.judge(t.id, t.key.Type == modMovePin, l.p.Confirm)
	switch {
	case !v.on || m.Zone == "":
		if m.ZoneWide {
			m.ZoneWide, m.Excluded = false, nil
			t.lostToEffect = v.byEffect
			l.changed(m, modShrink)
		}
	case !m.ZoneWide:
		m.ZoneWide, m.Excluded = true, slices.Clone(v.cells())
		l.changed(m, modGrowth)
	case !slices.Equal(m.Excluded, v.cells()):
		c := modGrowth
		for _, cell := range v.cells() {
			if !slices.Contains(m.Excluded, cell) {
				c = modShrink // a cell newly excluded
			}
		}
		m.Excluded = slices.Clone(v.cells())
		l.changed(m, c)
	}
}

// judge re-evaluates the modifiers of dimension d after a check: zone-wide
// coverage from the record, then confirm or end.
func (l *modifierLearner) judge(st *modScopeState, d modDim, now time.Time,
	out []modifierTransition) []modifierTransition {
	rec := st.rec[d]
	for _, t := range st.tracks {
		if t.dim != d || t.mod == nil {
			continue
		}
		if rec != nil {
			l.judgeZone(t, rec)
		}
		out = l.settle(t, now, out)
	}
	return out
}
