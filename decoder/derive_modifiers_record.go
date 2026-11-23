package decoder

import (
	"math"
	"slices"
	"time"

	"github.com/golang/geo/s2"
)

// The zone record (design §8–§9): per scope, zone and dimension, the last
// modRecordSize located checks, each an agreement or the effect seen, with
// its cell and time. It exists from the scope's first check, before any
// modifier, and zone-wide coverage is judged on its entries no older than
// Unseen: level-14 cells are too fine to count evidence when encounters are
// sampled.

const modRecordSize = 16

// A zone-wide modifier needs matches from at least modSpreadMinCells
// distinct level-14 cells, two of them at least modSpreadMinKm apart
// (centre to centre): a venue stays local, however it lies on the cell
// grid, and so does a scan area smaller than that.
const (
	modSpreadMinCells = 8
	modSpreadMinKm    = 10.0
	modEarthRadiusKm  = 6371.0088
)

// modSpreadMaxCos is the cosine of the angle modSpreadMinKm subtends: two
// unit vectors are that far apart when their dot product is at most this.
var modSpreadMaxCos = math.Cos(modSpreadMinKm / modEarthRadiusKm)

// Effect ids of the dimensions whose tracks have no effect key: an IV
// mismatch, and for size a roll that supports the moved cuts or one the
// interval fit refuses.
const (
	modIDMismatch  int32 = 1
	modIDSizeMatch int32 = 1
	modIDSizeOther int32 = 2
)

// Why a modifier is not zone-wide: an end reason.
const (
	modLostAgreement = "agreement"
	modLostEffect    = "contradicted"
	modLostAged      = "unseen"
)

// modRecEntry is one located check: an agreement with the base value, or
// the effect seen (a level delta, a move, or one of the ids above). An
// agreement on a move slot carries the observed move in id.
type modRecEntry struct {
	cell  s2.CellID
	unix  int64
	id    int32
	agree bool
}

type modRecord struct {
	buf     [modRecordSize]modRecEntry
	head, n uint8
	last    int64 // unix time of the latest check, entered or not
}

func (r *modRecord) push(cell s2.CellID, id int32, agree bool, unix int64) {
	r.buf[r.head] = modRecEntry{cell: cell, unix: unix, id: id, agree: agree}
	r.head = (r.head + 1) % modRecordSize
	if r.n < modRecordSize {
		r.n++
	}
	r.touch(unix)
}

// touch notes a check at unix that adds no entry (a size roll that says
// nothing about the moved cuts).
func (r *modRecord) touch(unix int64) { r.last = max(r.last, unix) }

// at returns the i-th newest entry, 1 ≤ i ≤ r.n.
func (r *modRecord) at(i int) modRecEntry {
	return r.buf[(int(r.head)-i+modRecordSize)%modRecordSize]
}

// modVerdict is a record's judgement of one effect.
type modVerdict struct {
	// on: the record holds at least confirm fresh entries, the effect is
	// more than half of them, and its matches are spread.
	on bool
	// thin: fewer than confirm fresh entries.
	thin bool
	// byEffect: the latest fresh entry is another effect (what displaced
	// this one when on turns false).
	byEffect bool
}

// lost is the reason a modifier with this verdict is not zone-wide.
func (v modVerdict) lost() string {
	switch {
	case v.thin:
		return modLostAged
	case v.byEffect:
		return modLostEffect
	}
	return modLostAgreement
}

// judge evaluates effect id against the record's fresh entries, those at or
// after cut: older ones do not count. For a move pin an agreement whose
// observed move is the pinned one is neutral (the base walk and the pin
// give the same move): it is left out of the majority.
func (r *modRecord) judge(id int32, pin bool, confirm int, cut int64) modVerdict {
	var (
		v                     modVerdict
		cells                 [modRecordSize]s2.CellID
		nCells                int
		fresh, match, neutral int
	)
	for i := 1; i <= int(r.n); i++ { // newest first
		e := r.at(i)
		if e.unix < cut {
			continue
		}
		fresh++
		if e.agree && pin && e.id == id {
			neutral++
			continue
		}
		mine := !e.agree && e.id == id
		if fresh == 1 {
			v.byEffect = !e.agree && !mine
		}
		if mine {
			match++
			if !slices.Contains(cells[:nCells], e.cell) {
				cells[nCells] = e.cell
				nCells++
			}
		}
	}
	v.thin = fresh < confirm
	v.on = !v.thin && 2*match > fresh-neutral && nCells >= modSpreadMinCells && modCellsSpread(cells[:nCells])
	return v
}

// modCellsSpread reports whether two of cells (at most modRecordSize) have
// centres at least modSpreadMinKm apart along the great circle.
func modCellsSpread(cells []s2.CellID) bool {
	var pts [modRecordSize]s2.Point
	for i, c := range cells {
		pts[i] = c.Point()
		for j := range i {
			if pts[i].Dot(pts[j].Vector) <= modSpreadMaxCos {
				return true
			}
		}
	}
	return false
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

// hasModifier reports whether the track of effect id on dimension d has a
// modifier.
func (st *modScopeState) hasModifier(d modDim, id int32) bool {
	for _, t := range st.tracks {
		if t.dim == d && t.id == id && t.mod != nil {
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

// judgeZone applies rec's verdict as of unix to t's modifier: zone-wide
// coverage on or off. It is never on in the unknown zone, without a record,
// or once the modifier's excluded cells overflowed.
func (l *modifierLearner) judgeZone(t *modTrack, rec *modRecord, unix int64) {
	m := t.mod
	v := modVerdict{thin: true}
	if rec != nil {
		v = rec.judge(t.id, t.key.Type == modMovePin, l.p.Confirm, unix-int64(l.p.Unseen/time.Second))
	}
	on := v.on && m.Zone != "" && !t.overflow
	if !on {
		t.lost = v.lost()
		if v.on {
			t.lost = modLostAgreement
		}
	}
	if on == m.ZoneWide {
		return
	}
	m.ZoneWide = on
	if on {
		l.changed(m, modGrowth)
	} else {
		l.changed(m, modShrink)
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
		l.judgeZone(t, rec, now.Unix())
		out = l.settle(t, now, out)
	}
	return out
}
