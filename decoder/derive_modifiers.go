package decoder

import (
	"maps"
	"math"
	"time"

	"github.com/golang/geo/s2"
)

// The derivation modifier learner (docs/design/2026-11-17-derivation-
// modifiers-live-design.md) detects event modifiers — a level offset, a
// forced move, moved size cuts, or an unexplained walk mismatch — from live
// encounter checks, per timezone. It judges a global event on a zone-level
// record of the last 16 checks (derive_modifiers_record.go, design §8–§10):
// a modifier covers its zone, less the cells that disproved it, while its
// effect holds its share of that record's fresh entries (three quarters to
// turn on, more than half to stay) and its matches are spread. Two
// agreements running pause it until its next match. A ticketed venue is tracked cell by cell (level-14) and listed, but never
// coverage: a venue-only modifier stays a candidate. The learner is a pure
// state machine owned by one goroutine; it does not dedupe encounters (the
// decode site feeds it each spawn's first encounter only).

// modifierEvent is the learner's input: one encounter check or first
// sighting. It is pointer-free (the GC never scans a channel of them) and
// at most 64 bytes; building one on the decode path does not allocate.
type modifierEvent struct {
	Unix                 int64     // seconds
	Cell                 s2.CellID // level-14; 0 = unknown
	SizeRoll             float64
	PokemonID, Form      int32
	ObsQuick, ObsCharged int32 // observed moves; 0 = none
	// LevelDelta is observed − derived level, clamped to int8; meaningful
	// only when Agree.Level is dimDisagree.
	LevelDelta int8
	ObsSize    uint8 // observed size class 1..5; 0 = none
	Agree      dimAgreement
	Strong     bool
	Sighting   bool // a first sighting: no check, Agree has no opinion
}

// newModifierEvent builds the learner's event from the events log's entry.
func newModifierEvent(ev deriveEventCheck) modifierEvent {
	obsSize := uint8(0)
	if s := ev.Check.Observed.Size; s >= 1 && s <= 5 {
		obsSize = uint8(s)
	}
	delta := ev.Check.Observed.Level - ev.Check.Derived.Level
	return modifierEvent{
		Unix:       ev.Time.Unix(),
		Cell:       modifierCell(ev.CellID, ev.Lat, ev.Lon),
		SizeRoll:   ev.Check.SizeRoll,
		PokemonID:  ev.PokemonID,
		Form:       ev.Form,
		ObsQuick:   ev.Check.Move1,
		ObsCharged: ev.Check.Move2,
		LevelDelta: int8(min(max(delta, math.MinInt8), math.MaxInt8)),
		ObsSize:    obsSize,
		Agree:      ev.Agree,
		Strong:     ev.Strong,
		Sighting:   ev.Sighting,
	}
}

// modifierCell is the deriveEventsCellLevel cell of a pokemon: the parent of
// the server's level-15 map cell, else of lat/lon, else 0 (unknown).
func modifierCell(cellID int64, lat, lon float64) s2.CellID {
	cell := s2.CellID(uint64(cellID))
	if cellID == 0 || !cell.IsValid() || cell.Level() < deriveEventsCellLevel {
		if lat == 0 && lon == 0 {
			return 0
		}
		cell = s2.CellIDFromLatLng(s2.LatLngFromDegrees(lat, lon))
	}
	return cell.Parent(deriveEventsCellLevel)
}

type modifierType uint8

const (
	modLevelOffset modifierType = iota
	modMovePin
	modSizeCutoffs
	modUnexplained
)

func (t modifierType) String() string {
	switch t {
	case modLevelOffset:
		return "level_offset"
	case modMovePin:
		return "move_pin"
	case modSizeCutoffs:
		return "size_cutoffs"
	case modUnexplained:
		return "unexplained"
	}
	return "unknown"
}

type modifierStatus uint8

const (
	modCandidate modifierStatus = iota
	modConfirmed
	modEnded
)

func (s modifierStatus) String() string {
	switch s {
	case modCandidate:
		return "candidate"
	case modConfirmed:
		return "confirmed"
	case modEnded:
		return "ended"
	}
	return "unknown"
}

// modifierScope is one species and form (strong or not), or with AllStrong
// every strong pokemon (PokemonID and Form 0, Strong true). The learner
// tracks species scopes only (design §8).
type modifierScope struct {
	PokemonID, Form   int32
	Strong, AllStrong bool
}

var allStrongScope = modifierScope{Strong: true, AllStrong: true}

const (
	modSlotQuick   = "quick"
	modSlotCharged = "charged"
)

// modifierEffect is a modifier's payload: Delta for level_offset, Slot and
// Move for move_pin, Cut for size_cutoffs (the one size boundary the
// modifier moves, "1/2".."4/5"); zero otherwise.
type modifierEffect struct {
	Delta int32
	Slot  string // modSlotQuick | modSlotCharged
	Move  int32
	Cut   string
}

// sizeBoundary is one moved size cut's interval on the raw roll; an
// unbounded end has its Has flag false.
type sizeBoundary struct {
	Lo, Hi       float64
	HasLo, HasHi bool
}

// derivationModifier is one detected modifier in one timezone.
type derivationModifier struct {
	Type   modifierType
	Scope  modifierScope
	Effect modifierEffect
	// Interval is where the moved cut Effect.Cut lies (size_cutoffs only).
	Interval sizeBoundary
	Zone     string
	Status   modifierStatus
	// Start is the cycle's first matching deviation; End the ended
	// transition's time; LastSeen the latest match, or check or sighting of
	// the scope where the modifier covers.
	Start, End, LastSeen time.Time
	EndReason            string // "agreement" | "contradicted" | "unseen" | "unconfirmed"
	// Observations counts matching deviations (for size, accepted rolls);
	// Contradictions the other effects seen where the modifier covered or had
	// a match (for size, the rolls its intervals refuse).
	Observations   int
	Contradictions int
	// ZoneWide: the modifier covers its zone, less the Excluded cells. It is
	// a modifier's only coverage (design §9).
	ZoneWide bool
	// Paused: the zone record's last modPauseAfter located checks that say
	// anything about the modifier were agreements with the base value, with
	// no match since (design §10). While paused the modifier's dimension is
	// withheld where it covers: neither its value nor the base one is
	// derived. The next located match unpauses it. An unexplained modifier
	// never pauses: it already withholds.
	Paused bool
	// agreeRun counts those agreements, up to modPauseAfter.
	agreeRun uint8
	// Excluded holds the cells that disproved the modifier: their latest
	// located check of its dimension was not its effect. A cell stays until
	// a matching check there (for size_cutoffs, a roll there that contradicts
	// the disproving one), whatever ZoneWide is meanwhile; at most the
	// learner's excludedCap cells, nil until the first.
	Excluded map[s2.CellID]struct{}
	// Cells lists the venue cells: two matches and no agreement or other
	// effect there since. They are evidence, listed and never coverage.
	Cells map[s2.CellID]struct{}
	// version counts the changes the learner reports through changed (and
	// the confirm), so the runtime re-clones only a modifier that changed
	// since its last publish. LastSeen is not versioned: it is not published.
	// cellsVersion and excludedVersion count the changes to Cells and to
	// Excluded alone, so a republished copy shares the previous copy's set
	// when it did not change.
	version, cellsVersion, excludedVersion uint64
}

// Covers reports whether a record in zone and cell is covered by m: its
// zone when zone-wide, unless the cell is excluded.
func (m *derivationModifier) Covers(zone string, cell s2.CellID) bool {
	if zone != m.Zone || !m.ZoneWide {
		return false
	}
	_, excluded := m.Excluded[cell]
	return !excluded
}

// clone is an independent copy of m.
func (m *derivationModifier) clone() *derivationModifier {
	return m.cloneSharing(nil)
}

// cloneSharing is clone, sharing prev's Cells and Excluded when prev (an
// earlier, immutable copy of m) has m's version of them: a publish never
// copies a cell set that did not change.
func (m *derivationModifier) cloneSharing(prev *derivationModifier) *derivationModifier {
	c := *m
	if prev != nil && prev.cellsVersion == m.cellsVersion {
		c.Cells = prev.Cells
	} else {
		c.Cells = maps.Clone(m.Cells)
	}
	if prev != nil && prev.excludedVersion == m.excludedVersion {
		c.Excluded = prev.Excluded
	} else {
		c.Excluded = maps.Clone(m.Excluded)
	}
	return &c
}

// modifierParams are the learner's thresholds (design §3, §8).
type modifierParams struct {
	Start, Confirm int
	Window, Unseen time.Duration
}

func defaultModifierParams() modifierParams {
	return modifierParams{Start: 3, Confirm: 10, Window: 30 * time.Minute, Unseen: time.Hour}
}

// modifierTransition is one lifecycle change. Mod is a copy as of At (the
// ended modifier itself, which the learner no longer holds, for an end).
type modifierTransition struct {
	At    time.Time
	State modifierStatus
	Mod   *derivationModifier
}

// modDim is the dimension a track watches: one of dimAgreement's, or for
// size one per cut of the scope's cut list (modDimSize + its index), so
// each size boundary has its own tracks and zone record. Each has one
// modifier type: unexplained, level_offset, move_pin, size_cutoffs.
type modDim uint8

const (
	modDimIVs modDim = iota
	modDimLevel
	modDimQuick
	modDimCharged
	modDimSize  // the first size cut; the others follow
	modDimCount = modDimSize + modSizeCutsMax
)

// modSizeCutsMax is the most cuts a size cut list has.
const modSizeCutsMax = 4

type modTrackKey struct {
	Type   modifierType
	Scope  modifierScope
	Effect modifierEffect
	Zone   string
}

type modScopeKey struct {
	Scope modifierScope
	Zone  string
}

// modTrackKeyOf is the track of effect id on dimension d.
func modTrackKeyOf(sk modScopeKey, d modDim, id int32) modTrackKey {
	k := modTrackKey{Scope: sk.Scope, Zone: sk.Zone}
	switch d {
	case modDimIVs:
		k.Type = modUnexplained
	case modDimLevel:
		k.Type, k.Effect = modLevelOffset, modifierEffect{Delta: id}
	case modDimQuick:
		k.Type, k.Effect = modMovePin, modifierEffect{Slot: modSlotQuick, Move: id}
	case modDimCharged:
		k.Type, k.Effect = modMovePin, modifierEffect{Slot: modSlotCharged, Move: id}
	default:
		k.Type = modSizeCutoffs
		k.Effect = modifierEffect{Cut: sizeCuts(sk.Scope.Strong)[d-modDimSize].name}
	}
	return k
}

// modPending is a matching deviation of a track with no modifier. Roll and
// Class are its size roll and observed class, the evidence a size_cutoffs
// candidate starts from. Void: another effect was seen in its cell since, so
// it counts toward the candidate but not as a venue match (size: the
// interval fit refused it).
type modPending struct {
	Unix  int64
	Cell  s2.CellID
	Roll  float64
	Class uint8
	Void  bool
}

// modTrack is one (type, scope, effect, zone): its recent deviations while
// it has no modifier, else the modifier and its venue cells.
type modTrack struct {
	key     modTrackKey
	dim     modDim
	id      int32        // its effect's id in the zone record
	pending []modPending // time order, oldest first
	lastDev int64        // latest matching deviation while without a modifier
	mod     *derivationModifier
	// venue holds a count for every cell with a match of the modifier and no
	// agreement or other effect since, pruned after Unseen without a match
	// (never used by size, which has no venue cells).
	venue map[s2.CellID]modVenueCell
	// located counts the modifier's matches with a cell.
	located int
	// overflow: a disproving cell did not fit in the modifier's excluded
	// cells, so it is not zone-wide and ends at the next settle.
	overflow bool
	// lost is why the modifier was last found not zone-wide: the end reason
	// of a confirmed one.
	lost string
	size *sizeState // size_cutoffs tracks only
}

// modPruneInterval is how often, in seconds of event time, feed prunes.
const modPruneInterval = 60

// modifierLearner is the single-goroutine modifier state machine.
type modifierLearner struct {
	p            modifierParams
	tracks       map[modTrackKey]*modTrack
	scopes       map[modScopeKey]*modScopeState
	activeTracks []*modTrack // tracks with a modifier
	nextPrune    int64
	// zoneCells counts the cells seen per zone (cellSeen), which scales the
	// excluded-cell cap.
	zoneCells map[string]*modZoneCells
	// dirty records the non-transition changes since the last takeDirty.
	dirty modDirty
}

// modChange is what a non-transition change does to a modifier's index
// lookup (only a confirmed modifier is looked up).
type modChange uint8

const (
	// modEvidence changes no lookup: observations, contradictions, a venue
	// cell listed or removed.
	modEvidence modChange = iota
	// modGrowth makes the lookup cover more: zone-wide gained, an excluded
	// cell readmitted, a pause lifted.
	modGrowth
	// modShrink makes it cover less or differently: zone-wide lost, a cell
	// excluded, paused, or a size interval moved (a record's class can
	// change).
	modShrink
)

// modDirty is what changed without a transition since the last takeDirty:
// changed for any change; growth and shrink when a confirmed modifier's
// lookup coverage grew or shrank.
type modDirty struct {
	changed, growth, shrink bool
}

func newModifierLearner(p modifierParams) *modifierLearner {
	return &modifierLearner{
		p:         p,
		tracks:    make(map[modTrackKey]*modTrack),
		scopes:    make(map[modScopeKey]*modScopeState),
		zoneCells: make(map[string]*modZoneCells),
	}
}

// feed processes one encounter check or first sighting (ev.Sighting) in
// zone and returns the transitions it causes. It ends unseen modifiers
// first, and prunes at most once per modPruneInterval of event time. Every
// check counts: the decode site sends each spawn's first encounter only.
func (l *modifierLearner) feed(ev modifierEvent, zone string) []modifierTransition {
	now := time.Unix(ev.Unix, 0).UTC()
	out := l.endUnseen(now, nil)
	if ev.Unix >= l.nextPrune {
		out = l.prune(now, out)
	}
	sk := modScopeKey{modifierScope{PokemonID: ev.PokemonID, Form: ev.Form, Strong: ev.Strong}, zone}
	st := l.scopes[sk]
	if ev.Sighting {
		if st != nil {
			st.markSeen(zone, ev.Cell, now)
		}
		return out
	}
	ivs := ev.Agree.IVs
	if ivs != dimAgree && ivs != dimDisagree {
		return out
	}
	if st == nil {
		st = new(modScopeState)
		l.scopes[sk] = st
	}
	st.markSeen(zone, ev.Cell, now)
	// IVs disagreeing are unexplained and say nothing else: the walk did not
	// match, so any other agreement is a coincidence.
	if ivs == dimDisagree {
		return l.check(st, sk, modDimIVs, false, modIDMismatch, ev, now, out)
	}
	out = l.check(st, sk, modDimIVs, true, 0, ev, now, out)
	switch ev.Agree.Level {
	case dimAgree:
		out = l.check(st, sk, modDimLevel, true, 0, ev, now, out)
	case dimDisagree:
		out = l.check(st, sk, modDimLevel, false, int32(ev.LevelDelta), ev, now, out)
	}
	if s := ev.Agree.Quick; s == dimAgree || s == dimDisagree {
		out = l.check(st, sk, modDimQuick, s == dimAgree, ev.ObsQuick, ev, now, out)
	}
	if s := ev.Agree.Charged; s == dimAgree || s == dimDisagree {
		out = l.check(st, sk, modDimCharged, s == dimAgree, ev.ObsCharged, ev, now, out)
	}
	return l.sizeCheck(st, sk, ev, now, out)
}

// sweep ends the active modifiers unseen for longer than Unseen, at the
// time last seen, and prunes.
func (l *modifierLearner) sweep(now time.Time) []modifierTransition {
	return l.prune(now, l.endUnseen(now, nil))
}

// active returns copies of the candidate and confirmed modifiers. Test-only:
// the runtime publishes through modifierRuntime.activeCopies.
func (l *modifierLearner) active() []*derivationModifier {
	out := make([]*derivationModifier, 0, len(l.activeTracks))
	for _, t := range l.activeTracks {
		out = append(out, t.mod.clone())
	}
	return out
}

func (l *modifierLearner) trackCount() int { return len(l.tracks) }

// changed records a change to active modifier m that is not a transition,
// and what it does to m's lookup coverage (growth and shrink count only for
// a confirmed modifier). It bumps m's version. Growth that can reduce what
// is applied counts as shrink, published at once: an unexplained modifier's
// (wider suppression). Two modifiers of one type and slot are never both
// confirmed in a zone outside one feed (each needs its share of the record),
// and that feed's transition publishes at once.
func (l *modifierLearner) changed(m *derivationModifier, c modChange) {
	m.version++
	l.dirty.changed = true
	if m.Status != modConfirmed {
		return
	}
	if c == modGrowth && m.Type == modUnexplained {
		c = modShrink
	}
	switch c {
	case modGrowth:
		l.dirty.growth = true
	case modShrink:
		l.dirty.shrink = true
	}
}

// takeDirty reports and clears the changes recorded since the last call.
// The runtime publishes a shrink at once (the published index would apply
// a value where it no longer should) and growth and evidence at most once
// per deriveModifiersEvidenceInterval.
func (l *modifierLearner) takeDirty() modDirty {
	d := l.dirty
	l.dirty = modDirty{}
	return d
}

// check applies one check's outcome on dimension d (not a size one): an
// agreement with the base value, or effect id seen instead. With a cell it
// enters the zone record; a move agreement keeps the observed move as its
// id, and one on the move of a pin that has a modifier goes to the record's
// pin-equal ring, where it does not displace that pin's evidence. It
// disproves the cell for every other effect's modifier (and for
// every modifier, when it agrees): the cell is excluded from it until a
// match there, and an agreement counts towards its pause. It gates or voids
// the pending deviations of tracks without one, counts the match for its
// own track, and re-judges the dimension's modifiers. A move agreeing with a
// pinned move is neutral for that pin alone (the base walk and the pin give
// the same move): the pins of other moves on the slot take it as the
// agreement it is.
func (l *modifierLearner) check(st *modScopeState, sk modScopeKey, d modDim, agree bool, id int32,
	ev modifierEvent, now time.Time, out []modifierTransition) []modifierTransition {
	located := ev.Cell != 0
	pin := d == modDimQuick || d == modDimCharged
	switch {
	case !located:
	case agree && pin && st.hasModifier(d, id):
		st.record(d).pushPinEqual(ev.Cell, id, ev.Unix)
	default:
		st.record(d).push(ev.Cell, id, agree, ev.Unix)
	}
	var own *modTrack
	if !agree {
		own = l.track(st, sk, d, id)
	}
	for _, t := range st.tracks {
		if t.dim != d || t == own || (agree && pin && t.id == id) {
			continue
		}
		switch {
		case t.mod != nil:
			if located {
				l.disprove(t, ev.Cell, !agree)
				if agree {
					l.agreed(t.mod)
				}
			}
		case agree:
			t.clearCell(ev.Cell, ev.Unix)
		default:
			t.voidCell(ev.Cell)
		}
	}
	if own != nil {
		out = l.match(own, st.rec[d], ev, now, out)
	}
	return l.judge(st, d, now, out)
}

// track returns the track of effect id on dimension d, creating it.
func (l *modifierLearner) track(st *modScopeState, sk modScopeKey, d modDim, id int32) *modTrack {
	key := modTrackKeyOf(sk, d, id)
	if t, ok := l.tracks[key]; ok {
		return t
	}
	t := &modTrack{key: key, dim: d, id: id}
	if d >= modDimSize {
		t.size = &sizeState{cut: sizeCuts(sk.Scope.Strong)[d-modDimSize]}
	}
	l.tracks[key] = t
	st.tracks = append(st.tracks, t)
	return t
}

// settle applies the lifecycle to t's modifier (design §8–§9). A candidate
// confirms with Confirm located matches while zone-wide; venue cells never
// confirm. One whose excluded cells overflowed ends unconfirmed at once: it
// could never go zone-wide, and at a global event's start (the spawns from
// before it still agree, in hundreds of cells) it would block the event
// until its deadline. A new candidate forms from the next Start matches. A
// confirmed modifier no longer zone-wide ends, for the reason zone-wide was
// lost.
func (l *modifierLearner) settle(t *modTrack, now time.Time, out []modifierTransition) []modifierTransition {
	m := t.mod
	switch {
	case m.Status == modCandidate:
		if t.overflow {
			return append(out, l.end(t, now, "unconfirmed"))
		}
		if m.ZoneWide && t.located >= l.p.Confirm {
			m.Status = modConfirmed
			m.version++
			out = append(out, modifierTransition{At: now, State: modConfirmed, Mod: m.clone()})
		}
	case !m.ZoneWide:
		out = append(out, l.end(t, now, t.lost))
	}
	return out
}

// match counts one matching deviation for t; rec is its dimension's record.
func (l *modifierLearner) match(t *modTrack, rec *modRecord, ev modifierEvent, now time.Time,
	out []modifierTransition) []modifierTransition {
	if t.mod == nil {
		if !t.addPending(ev, l.p) {
			return out
		}
		mod := l.open(t, now)
		for _, pd := range t.pending {
			mod.Observations++
			if pd.Cell == 0 {
				continue
			}
			t.located++
			if !pd.Void {
				l.venueMatch(t, pd.Cell, pd.Unix)
			}
		}
		l.seedExcluded(t, rec, ev.Unix)
		return l.opened(t, rec, now, out)
	}
	m := t.mod
	m.Observations++
	if ev.Cell != 0 {
		t.located++
		l.venueMatch(t, ev.Cell, ev.Unix)
		l.readmit(t, ev.Cell)
		l.matched(m)
	}
	l.changed(m, modEvidence)
	if now.After(m.LastSeen) {
		m.LastSeen = now
	}
	return out
}

// addPending adds ev to t's pending deviations, drops those older than
// Window, and reports whether Start remain: a candidate.
func (t *modTrack) addPending(ev modifierEvent, p modifierParams) bool {
	t.pending = append(t.pending, modPending{Unix: ev.Unix, Cell: ev.Cell, Roll: ev.SizeRoll, Class: ev.ObsSize})
	t.lastDev = max(t.lastDev, ev.Unix)
	t.pruneBefore(ev.Unix - int64(p.Window/time.Second))
	return len(t.pending) >= p.Start
}

// open gives t a candidate modifier starting at its first pending deviation.
// The caller counts the pending deviations, then calls opened.
func (l *modifierLearner) open(t *modTrack, now time.Time) *derivationModifier {
	mod := &derivationModifier{
		Type: t.key.Type, Scope: t.key.Scope, Effect: t.key.Effect, Zone: t.key.Zone,
		Status:   modCandidate,
		Start:    time.Unix(t.pending[0].Unix, 0).UTC(),
		LastSeen: now,
		Cells:    make(map[s2.CellID]struct{}),
	}
	t.mod, t.located, t.overflow, t.lost = mod, 0, false, modLostAgreement
	if t.size == nil {
		t.venue = make(map[s2.CellID]modVenueCell)
	}
	return mod
}

// opened finishes open: the candidate is active, judged against rec, and
// reported.
func (l *modifierLearner) opened(t *modTrack, rec *modRecord, now time.Time,
	out []modifierTransition) []modifierTransition {
	t.pending = nil
	l.activeTracks = append(l.activeTracks, t)
	l.judgeZone(t, rec, now.Unix())
	return append(out, modifierTransition{At: now, State: modCandidate, Mod: t.mod.clone()})
}

// pruneBefore drops t's pending deviations older than cut.
func (t *modTrack) pruneBefore(cut int64) {
	i := 0
	for i < len(t.pending) && t.pending[i].Unix < cut {
		i++
	}
	t.pending = t.pending[i:]
}

// clearCell drops t's pending deviations from cell at or before unix: the
// base value was seen there since.
func (t *modTrack) clearCell(cell s2.CellID, unix int64) {
	kept := t.pending[:0]
	for _, pd := range t.pending {
		if pd.Cell != cell || pd.Unix > unix {
			kept = append(kept, pd)
		}
	}
	clear(t.pending[len(kept):])
	t.pending = kept
}

// voidCell marks t's pending deviations from cell: another effect was seen
// there since.
func (t *modTrack) voidCell(cell s2.CellID) {
	for i := range t.pending {
		if t.pending[i].Cell == cell {
			t.pending[i].Void = true
		}
	}
}

// end ends t's modifier at at.
func (l *modifierLearner) end(t *modTrack, at time.Time, reason string) modifierTransition {
	mod := t.mod
	mod.Status, mod.End, mod.EndReason = modEnded, at, reason
	t.mod, t.venue, t.located, t.overflow, t.lost = nil, nil, 0, false, ""
	if t.size != nil {
		// The boundary's record and disproofs are relative to the ended
		// interval.
		t.size.disproof = nil
		if st := l.scopes[modScopeKey{t.key.Scope, t.key.Zone}]; st != nil {
			st.rec[t.dim] = nil
		}
	}
	for i, a := range l.activeTracks {
		if a == t {
			l.activeTracks = append(l.activeTracks[:i], l.activeTracks[i+1:]...)
			break
		}
	}
	return modifierTransition{At: at, State: modEnded, Mod: mod}
}

func (l *modifierLearner) endUnseen(now time.Time, out []modifierTransition) []modifierTransition {
	for i := len(l.activeTracks) - 1; i >= 0; i-- {
		t := l.activeTracks[i]
		if now.Sub(t.mod.LastSeen) > l.p.Unseen {
			out = append(out, l.end(t, t.mod.LastSeen, "unseen"))
		}
	}
	return out
}

// prune ages what no check has refreshed and bounds the learner's maps. A
// venue cell with no match for Unseen is forgotten. Each modifier is judged
// again on its zone record as of now: entries older than Unseen no longer
// count, so a modifier whose matches have aged is no longer zone-wide
// (sightings show the species, not the effect) and, confirmed, ends. A
// candidate not confirmed within 2 × Unseen of its start ends unconfirmed (a
// new one can form from fresh deviations). Then pending deviations older
// than Window, tracks with no modifier and no deviation within Window, zone
// records idle for Window with no modifier judged on them, and scopes left
// with nothing.
func (l *modifierLearner) prune(now time.Time, out []modifierTransition) []modifierTransition {
	unix := now.Unix()
	l.nextPrune = unix + modPruneInterval
	stale := unix - int64(l.p.Unseen/time.Second)
	for i := len(l.activeTracks) - 1; i >= 0; i-- {
		t := l.activeTracks[i]
		for cell, v := range t.venue {
			if v.last < stale {
				l.venueDrop(t, cell)
			}
		}
		m := t.mod
		var rec *modRecord
		if st := l.scopes[modScopeKey{t.key.Scope, t.key.Zone}]; st != nil {
			rec = st.rec[t.dim]
		}
		l.judgeZone(t, rec, unix)
		out = l.settle(t, now, out)
		if t.mod == m && m.Status == modCandidate && now.Sub(m.Start) > 2*l.p.Unseen {
			out = append(out, l.end(t, now, "unconfirmed"))
		}
	}
	cut := unix - int64(l.p.Window/time.Second)
	for sk, st := range l.scopes {
		kept := st.tracks[:0]
		for _, t := range st.tracks {
			if t.mod == nil {
				t.pruneBefore(cut)
				if len(t.pending) == 0 && t.lastDev < cut {
					delete(l.tracks, t.key)
					continue
				}
			}
			kept = append(kept, t)
		}
		clear(st.tracks[len(kept):])
		st.tracks = kept
		live := len(kept) > 0
		for d, rec := range st.rec {
			switch {
			case rec == nil:
			case rec.last < cut && !st.active(modDim(d)):
				st.rec[d] = nil
			default:
				live = true
			}
		}
		if !live {
			delete(l.scopes, sk)
		}
	}
	return out
}
