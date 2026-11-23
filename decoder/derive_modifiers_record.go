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

// A modifier goes zone-wide with matches from at least modSpreadMinCells
// distinct level-14 cells, two of them at least modSpreadMinKm apart
// (centre to centre): a venue stays local, however it lies on the cell
// grid, and so does a scan area smaller than that. A confirmed one stays
// zone-wide on less (modJudgeStay).
const (
	modSpreadMinCells = 8
	modSpreadMinKm    = 10.0
	modEarthRadiusKm  = 6371.0088
)

// modSpreadMaxCos is the cosine of the angle modSpreadMinKm subtends: two
// unit vectors are that far apart when their dot product is at most this.
var modSpreadMaxCos = math.Cos(modSpreadMinKm / modEarthRadiusKm)

// Effect ids of the dimensions whose tracks have no effect id: an IV
// mismatch, and for a size boundary a roll that supports the moved cut or
// one its interval refuses.
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

// modRing is the last modRecordSize entries pushed, in push order.
type modRing struct {
	buf     [modRecordSize]modRecEntry
	head, n uint8
}

func (r *modRing) push(e modRecEntry) {
	r.buf[r.head] = e
	r.head = (r.head + 1) % modRecordSize
	if r.n < modRecordSize {
		r.n++
	}
}

// at returns the i-th newest entry, 1 ≤ i ≤ r.n.
func (r *modRing) at(i int) modRecEntry {
	return r.buf[(int(r.head)-i+modRecordSize)%modRecordSize]
}

type modRecord struct {
	modRing
	last int64 // unix time of the latest check, entered or not
	// pinEqual holds the move agreements that were neutral for a pin with a
	// modifier when seen (pushPinEqual), nil until the first. They say
	// nothing about that pin, so they must not push its evidence out of the
	// record: a species whose base walk gives the pinned move half the time
	// would else be left, now and then, with too few matching cells in the
	// 16 and end mid-event. To every other pin of the slot they are
	// agreements like any other (design §10): judge and seedExcluded read
	// both rings.
	pinEqual *modRing
}

func (r *modRecord) push(cell s2.CellID, id int32, agree bool, unix int64) {
	r.modRing.push(modRecEntry{cell: cell, unix: unix, id: id, agree: agree})
	r.touch(unix)
}

// pushPinEqual enters a move agreement on move id, the move of a pin that
// has a modifier.
func (r *modRecord) pushPinEqual(cell s2.CellID, id int32, unix int64) {
	if r.pinEqual == nil {
		r.pinEqual = new(modRing)
	}
	r.pinEqual.push(modRecEntry{cell: cell, unix: unix, id: id, agree: true})
	r.touch(unix)
}

// touch notes a check at unix that adds no entry (a size roll that says
// nothing about the moved cuts).
func (r *modRecord) touch(unix int64) { r.last = max(r.last, unix) }

// eachOldestFirst calls fn with the entries of both rings in time order,
// oldest first; at one time, a pin-equal agreement comes after the others
// (the order within a second is not kept across the rings, and taking the
// agreement as the later one excludes its cell: conservative).
func (r *modRecord) eachOldestFirst(fn func(modRecEntry)) {
	i, j := int(r.n), 0
	if r.pinEqual != nil {
		j = int(r.pinEqual.n)
	}
	for i >= 1 || j >= 1 {
		if j < 1 || (i >= 1 && r.at(i).unix <= r.pinEqual.at(j).unix) {
			fn(r.at(i))
			i--
		} else {
			fn(r.pinEqual.at(j))
			j--
		}
	}
}

// modJudge is what a record must hold for a verdict to be on: entries
// fresh entries, matches from cells distinct cells, and the effect's share
// of the fresh entries that say anything: at least three quarters, or with
// stay more than half.
type modJudge struct {
	entries, cells int
	stay           bool
}

// modJudgeOn is what turns zone-wide on; modJudgeStay what keeps a confirmed
// modifier zone-wide (hysteresis, design §9.1 and §10): Start fresh entries
// instead of Confirm, matches from min(Start, modSpreadMinCells) cells
// instead of modSpreadMinCells, more than half instead of three quarters,
// and still two matching cells modSpreadMinKm apart. Sampled encounters
// arrive at random, and an hour with fewer than Confirm of them is common
// at 12 an hour; it must not end an event that every one of them still
// shows. And as an event's last spawns drain, or its first arrive among the
// spawns from before it, the share crosses one half many times: turning on
// at a bare majority confirmed and ended it at each crossing. An event that
// ended is paused at its second base result running and out within Unseen
// (its matches age) or once its share is no more than half.
func modJudgeOn(p modifierParams) modJudge {
	return modJudge{entries: p.Confirm, cells: modSpreadMinCells}
}

func modJudgeStay(p modifierParams) modJudge {
	return modJudge{entries: p.Start, cells: min(p.Start, modSpreadMinCells), stay: true}
}

// modVerdict is a record's judgement of one effect.
type modVerdict struct {
	// on: the record holds enough fresh entries, the effect has its share
	// of those that say anything, and its matches are spread.
	on bool
	// thin: too few fresh entries.
	thin bool
	// byEffect: the latest fresh entry that says anything is another effect
	// (what displaced this one when on turns false).
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
// observed move is the pinned one is neutral for that pin (the base walk
// and the pin give the same move): it is not counted at all, neither in the
// share nor towards the entries a verdict needs. Judged for
// another pin of the slot it is an agreement like any other, from either
// ring.
func (r *modRecord) judge(id int32, pin bool, need modJudge, cut int64) modVerdict {
	var (
		v            modVerdict
		cells        [modRecordSize]s2.CellID
		nCells       int
		fresh, match int   // fresh: the fresh entries that say anything
		saidUnix     int64 // the latest of them
	)
	for i := 1; i <= int(r.n); i++ { // newest first
		e := r.at(i)
		if e.unix < cut {
			continue
		}
		if e.agree && pin && e.id == id {
			continue // neutral, as in the pin-equal ring: it says nothing
		}
		fresh++
		mine := !e.agree && e.id == id
		if fresh == 1 {
			v.byEffect, saidUnix = !e.agree && !mine, e.unix
		}
		if mine {
			match++
			if !slices.Contains(cells[:nCells], e.cell) {
				cells[nCells] = e.cell
				nCells++
			}
		}
	}
	if r.pinEqual != nil {
		for i := 1; i <= int(r.pinEqual.n); i++ {
			e := r.pinEqual.at(i)
			if e.unix < cut || e.id == id {
				continue
			}
			fresh++ // an agreement on another pin's move
			if e.unix >= saidUnix {
				v.byEffect = false
			}
		}
	}
	v.thin = fresh < need.entries
	share := 4*match >= 3*fresh
	if need.stay {
		share = 2*match > fresh
	}
	v.on = !v.thin && match > 0 && share && nCells >= need.cells && modCellsSpread(cells[:nCells])
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

// activeTrack returns the track of dimension d that has a modifier, nil
// when none has (a size dimension has one track).
func (st *modScopeState) activeTrack(d modDim) *modTrack {
	for _, t := range st.tracks {
		if t.dim == d && t.mod != nil {
			return t
		}
	}
	return nil
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
// coverage on or off, with hysteresis for a confirmed modifier that is on.
// It is never on in the unknown zone, without a record, or once the
// modifier's excluded cells overflowed.
func (l *modifierLearner) judgeZone(t *modTrack, rec *modRecord, unix int64) {
	m := t.mod
	v := modVerdict{thin: true}
	if rec != nil {
		need := modJudgeOn(l.p)
		if m.ZoneWide && m.Status == modConfirmed {
			need = modJudgeStay(l.p)
		}
		v = rec.judge(t.id, t.key.Type == modMovePin, need, unix-int64(l.p.Unseen/time.Second))
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
