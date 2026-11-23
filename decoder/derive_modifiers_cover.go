package decoder

import (
	"math"
	"time"

	"github.com/golang/geo/s2"
)

// Where a modifier covers and whether it applies there (design §8–§10):
// the cells that disproved it (excluded until answered, capped by the size
// of the zone), the pause on non-matches running, and the venue cells listed
// as evidence.

// A modifier's excluded cells are bounded by excludedCap: modExcludedMin,
// or one in modExcludedShare of the cells the learner has seen in the
// modifier's zone when that is more, at most modExcludedMax. One more and
// it is no longer zone-wide.
const (
	modExcludedMin   = 256
	modExcludedMax   = 4096
	modExcludedShare = 100
)

// modPauseAfter is how many located checks running that are not a
// modifier's effect, with no match between, pause it.
const modPauseAfter = 2

// modVenueCell is a cell's matches since its last agreement or other
// effect, and the latest one's time.
type modVenueCell struct {
	last int64
	n    uint8
}

// modVenueCovered: a venue cell is listed at this many matches.
const modVenueCovered = 2

// modVenueMax bounds the cells a modifier counts venue matches in. A venue
// is a few cells; a global event's matches fall in thousands, which are not
// worth listing.
const modVenueMax = 1024

// modZoneCells counts the distinct level-14 cells seen in one zone: n since
// the counting cache was last cleared, floor the most counted before that.
type modZoneCells struct{ n, floor int }

// cellSeen counts one more distinct cell seen in zone. The owner of the
// cell → zone cache calls it once per cell it adds (modifierRuntime.zone).
func (l *modifierLearner) cellSeen(zone string) {
	zc := l.zoneCells[zone]
	if zc == nil {
		zc = new(modZoneCells)
		l.zoneCells[zone] = zc
	}
	zc.n++
}

// cellsForgotten notes that the cell → zone cache was cleared: its cells
// will be counted again, so each zone's count restarts and the count
// reached stays as its floor.
func (l *modifierLearner) cellsForgotten() {
	for _, zc := range l.zoneCells {
		zc.floor, zc.n = max(zc.floor, zc.n), 0
	}
}

// excludedCap is the most cells a modifier in zone may exclude: one in
// modExcludedShare of the cells seen in the zone, within [modExcludedMin,
// modExcludedMax]. A live event over a large area with a little noise
// (level-capped spawns) disproves a share of its cells at any time.
func (l *modifierLearner) excludedCap(zone string) int {
	n := 0
	if zc := l.zoneCells[zone]; zc != nil {
		n = max(zc.n, zc.floor)
	}
	return min(max(n/modExcludedShare, modExcludedMin), modExcludedMax)
}

// missed counts a located check that says something about m and is not its
// effect: an agreement with the base value or another effect (design
// §10.2: a superseding effect brings no agreement, and the superseded one
// was applied until its share fell to half, about 28 minutes at 12 checks
// an hour). The modPauseAfter-th running pauses m: every event seen applied
// to its whole scope, so other results running mean the event is over (or
// absent here), and what it would derive is withheld rather than risked.
func (l *modifierLearner) missed(m *derivationModifier) {
	if m.Type == modUnexplained {
		return
	}
	if m.missRun < modPauseAfter {
		m.missRun++
	}
	if m.missRun >= modPauseAfter && !m.Paused {
		m.Paused = true
		l.changed(m, modShrink)
	}
}

// matched notes a located match of m: the non-matches no longer run, and a
// paused m applies again.
func (l *modifierLearner) matched(m *derivationModifier) {
	m.missRun = 0
	if m.Paused {
		m.Paused = false
		l.changed(m, modGrowth)
	}
}

// disprove applies an agreement or (byEffect) another effect seen in cell
// to t's modifier: the cell is excluded from it and no longer one of its
// venue cells.
func (l *modifierLearner) disprove(t *modTrack, cell s2.CellID, byEffect bool) {
	m := t.mod
	if byEffect {
		if _, in := t.venue[cell]; in || m.Covers(m.Zone, cell) {
			m.Contradictions++
			l.changed(m, modEvidence)
		}
	}
	l.venueDrop(t, cell)
	l.exclude(t, cell)
}

// exclude records that cell disproved t's modifier and reports whether the
// cell is now excluded: it stays so until readmit. A cell that does not fit
// under excludedCap marks the track overflowed instead: the next judgeZone
// turns zone-wide off, and settle ends the modifier.
func (l *modifierLearner) exclude(t *modTrack, cell s2.CellID) bool {
	m := t.mod
	if _, had := m.Excluded[cell]; had {
		return true
	}
	if len(m.Excluded) >= l.excludedCap(m.Zone) {
		t.overflow = true
		return false
	}
	if m.Excluded == nil {
		m.Excluded = make(map[s2.CellID]struct{})
	}
	m.Excluded[cell] = struct{}{}
	m.excludedVersion++
	if m.ZoneWide { // not looked up, nor listed, until zone-wide
		l.changed(m, modShrink)
	}
	return true
}

// readmit covers cell again for t's modifier: what disproved it there has
// been answered (a match; for size, sizeResolve).
func (l *modifierLearner) readmit(t *modTrack, cell s2.CellID) {
	m := t.mod
	if _, had := m.Excluded[cell]; !had {
		return
	}
	delete(m.Excluded, cell)
	m.excludedVersion++
	if m.ZoneWide {
		l.changed(m, modGrowth)
	}
}

// seedExcluded excludes from t's new modifier the cells whose latest fresh
// entry in rec is not its effect: what the zone showed before the candidate
// formed.
func (l *modifierLearner) seedExcluded(t *modTrack, rec *modRecord, unix int64) {
	if rec == nil {
		return
	}
	cut := unix - int64(l.p.Unseen/time.Second)
	pin := t.key.Type == modMovePin
	rec.eachOldestFirst(func(e modRecEntry) {
		switch {
		case e.unix < cut || (e.agree && pin && e.id == t.id):
		case !e.agree && e.id == t.id:
			l.readmit(t, e.cell)
		default:
			l.exclude(t, e.cell)
		}
	})
}

// venueDrop forgets cell's matches; a listed cell is no longer listed.
func (l *modifierLearner) venueDrop(t *modTrack, cell s2.CellID) {
	v, ok := t.venue[cell]
	if !ok {
		return
	}
	delete(t.venue, cell)
	if v.n < modVenueCovered {
		return
	}
	delete(t.mod.Cells, cell)
	t.mod.cellsVersion++
	l.changed(t.mod, modEvidence)
}

// venueMatch counts a match in cell at unix, listing the cell at
// modVenueCovered matches. A count older than Unseen is forgotten first; a
// new cell past modVenueMax is not counted.
func (l *modifierLearner) venueMatch(t *modTrack, cell s2.CellID, unix int64) {
	v, tracked := t.venue[cell]
	if !tracked && len(t.venue) >= modVenueMax {
		return
	}
	if v.n > 0 && unix-v.last > int64(l.p.Unseen/time.Second) {
		l.venueDrop(t, cell)
		v = modVenueCell{}
	}
	if v.n < math.MaxUint8 {
		v.n++
	}
	v.last = max(v.last, unix)
	t.venue[cell] = v
	if v.n == modVenueCovered {
		t.mod.Cells[cell] = struct{}{}
		t.mod.cellsVersion++
	}
}
