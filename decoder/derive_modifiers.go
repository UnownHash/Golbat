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
// encounter checks. It ports tools/eventreplay.py's Detector, with evidence
// kept per timezone and per level-14 cell: an agreement acts only in its own
// cell, and a modifier covers its in-cells, or its whole zone once it is
// zone-wide. The learner is a pure state machine owned by one goroutine.

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
// every strong pokemon (PokemonID and Form 0, Strong true).
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
// Move for move_pin; zero otherwise.
type modifierEffect struct {
	Delta int32
	Slot  string // modSlotQuick | modSlotCharged
	Move  int32
}

// sizeBoundary is one moved size cut's interval on the raw roll; an
// unbounded end has its Has flag false.
type sizeBoundary struct {
	Lo, Hi       float64
	HasLo, HasHi bool
}

// derivationModifier is one detected modifier in one timezone.
type derivationModifier struct {
	Type       modifierType
	Scope      modifierScope
	Effect     modifierEffect
	Boundaries map[string]sizeBoundary // size_cutoffs only
	Zone       string
	Status     modifierStatus
	// Start is the cycle's first matching deviation; End the ended
	// transition's time; LastSeen the latest check or sighting of the scope
	// in the modifier's region.
	Start, End, LastSeen time.Time
	EndReason            string // "agreement" | "contradicted" | "contrary" | "unseen"
	// Observations counts matching deviations (for size, accepted rolls);
	// Contradictions is the lifetime count of in-cell contradictions.
	Observations   int
	Contradictions int
	// InCells holds the cells with a matching deviation; Outside the cells
	// that agreed on the dimension (or contradicted it) and are not in-cells.
	// InCells only grows: add cells with addInCell.
	InCells, Outside map[s2.CellID]struct{}
	// parent10 is the modZoneWideSpreadLevel parent of the first in-cell;
	// spread is set once an in-cell lies under another one.
	parent10 s2.CellID
	spread   bool
	// outsideUnknown: agreements before the candidate overflowed its track's
	// cell list, so an outside cell may be missing from Outside.
	outsideUnknown bool
	// version counts the changes the learner reports through changed (and
	// the confirm), so the runtime re-clones only a modifier that changed
	// since its last publish. LastSeen and the ring are not versioned:
	// neither is published.
	version uint64
}

// modZoneWideSpreadLevel: a zone-wide modifier needs in-cells under at
// least two cells of this level (~10 km), so one venue stays local.
const modZoneWideSpreadLevel = 10

// addInCell makes cell an in-cell, keeping the spread flag.
func (m *derivationModifier) addInCell(cell s2.CellID) {
	m.InCells[cell] = struct{}{}
	p := cell.Parent(modZoneWideSpreadLevel)
	if m.parent10 == 0 {
		m.parent10 = p
	} else if p != m.parent10 {
		m.spread = true
	}
}

// ZoneWide reports whether m covers its whole zone: a known zone, at least
// zoneWideCells in-cells under two or more level-10 cells, and no outside
// cell.
func (m *derivationModifier) ZoneWide(zoneWideCells int) bool {
	return m.Zone != "" && m.spread && len(m.InCells) >= zoneWideCells &&
		len(m.Outside) == 0 && !m.outsideUnknown
}

// Covers reports whether a record in zone and cell is in m's region: its
// zone when zone-wide, else one of its in-cells. Cell 0 (unknown) is only
// ever covered zone-wide.
func (m *derivationModifier) Covers(zone string, cell s2.CellID, zoneWideCells int) bool {
	if zone != m.Zone {
		return false
	}
	if m.ZoneWide(zoneWideCells) {
		return true
	}
	_, ok := m.InCells[cell]
	return ok && cell != 0
}

func (m *derivationModifier) clone() *derivationModifier {
	c := *m
	c.Boundaries = maps.Clone(m.Boundaries)
	c.InCells = maps.Clone(m.InCells)
	c.Outside = maps.Clone(m.Outside)
	return &c
}

// publishedClone is the immutable copy the runtime publishes to the index
// and the API. It leaves Outside nil, folding a non-empty one into
// outsideUnknown, so ZoneWide answers as on m without copying a set that
// grows with every agreeing cell (nothing published reads its members).
func (m *derivationModifier) publishedClone() *derivationModifier {
	c := *m
	c.Boundaries = maps.Clone(m.Boundaries)
	c.InCells = maps.Clone(m.InCells)
	c.Outside = nil
	c.outsideUnknown = m.outsideUnknown || len(m.Outside) > 0
	return &c
}

// modifierParams are the learner's thresholds (design §3, §7).
type modifierParams struct {
	Start, Confirm, End, ZoneWideCells int
	Window, Unseen                     time.Duration
}

func defaultModifierParams() modifierParams {
	return modifierParams{Start: 3, Confirm: 10, End: 8, ZoneWideCells: 3,
		Window: 30 * time.Minute, Unseen: time.Hour}
}

// modifierTransition is one lifecycle change. Mod is a copy as of At (the
// ended modifier itself, which the learner no longer holds, for an end).
type modifierTransition struct {
	At    time.Time
	State modifierStatus
	Mod   *derivationModifier
}

// modDim is the dimension of dimAgreement a track watches.
type modDim uint8

const (
	modDimIVs modDim = iota
	modDimLevel
	modDimSize
	modDimQuick
	modDimCharged
)

func (a dimAgreement) of(d modDim) dimState {
	switch d {
	case modDimIVs:
		return a.IVs
	case modDimLevel:
		return a.Level
	case modDimSize:
		return a.Size
	case modDimQuick:
		return a.Quick
	}
	return a.Charged
}

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

// modPending is a matching deviation of a track with no modifier. Roll and
// Class are its size roll and observed class, the evidence a size_cutoffs
// candidate starts from.
type modPending struct {
	Unix            int64
	Cell            s2.CellID
	Roll            float64
	PokemonID, Form int32
	Class           uint8
}

// modAgreed is a cell where a gated track's dimension agreed before it had
// a modifier, and the latest such agreement.
type modAgreed struct {
	Cell s2.CellID
	Unix int64
}

// modAgreedCap bounds a track's pre-candidate agreeing cells.
const modAgreedCap = 64

// modOutcome is one in-cell outcome on a modifier's dimension.
type modOutcome uint8

const (
	modOutMatch  modOutcome = iota + 1 // its effect
	modOutAgree                        // the base value
	modOutContra                       // another effect of its type, scope and slot
)

const modRingSize = 16

// modRing holds a modifier's last modRingSize in-cell outcomes.
type modRing struct {
	buf     [modRingSize]modOutcome
	head, n uint8
}

func (r *modRing) push(o modOutcome) {
	r.buf[r.head] = o
	r.head = (r.head + 1) % modRingSize
	if r.n < modRingSize {
		r.n++
	}
}

// counts returns the matches and contradictions in the ring and how many
// entries it holds.
func (r *modRing) counts() (match, contra, filled int) {
	for _, o := range r.buf[:r.n] {
		switch o {
		case modOutMatch:
			match++
		case modOutContra:
			contra++
		}
	}
	return match, contra, int(r.n)
}

// modTrack is one (type, scope, effect, zone): its recent deviations and
// agreeing cells while it has no modifier, else the modifier, its outcome
// ring and the in-cell agreements since its last in-cell match.
type modTrack struct {
	key  modTrackKey
	dim  modDim
	gate bool // an agreement in a cell discards that cell's pending deviations
	// twoSpecies: a candidate needs two species among pending (all-strong
	// level offset).
	twoSpecies bool
	pending    []modPending // time order, oldest first
	// agreed: the cells where the dimension agreed within Window, gated
	// tracks without a modifier only; overflowAt the latest agreement left
	// out because agreed was full (0 = none).
	agreed     []modAgreed
	overflowAt int64
	lastDev    int64 // latest matching deviation while without a modifier
	mod        *derivationModifier
	ring       modRing
	endOK      int
	// located counts the modifier's matches with a cell, the observations
	// Confirm is judged on (a cell-0 match is in no ring).
	located int
	size    *sizeEvidence // size_cutoffs tracks only
}

// modPruneInterval is how often, in seconds of event time, feed prunes idle
// tracks.
const modPruneInterval = 60

// modifierLearner is the single-goroutine modifier state machine.
type modifierLearner struct {
	p            modifierParams
	tracks       map[modTrackKey]*modTrack
	byScope      map[modScopeKey][]*modTrack
	activeTracks []*modTrack // tracks with a modifier
	nextPrune    int64
	// dirty records the non-transition changes since the last takeDirty.
	dirty modDirty
}

// modChange is what a non-transition change does to a modifier's index
// lookup (only a confirmed modifier is looked up).
type modChange uint8

const (
	// modEvidence changes no lookup: observations, contradictions, an
	// outside cell of a local modifier, an in-cell of a zone-wide one.
	modEvidence modChange = iota
	// modGrowth makes the lookup cover more: a new in-cell of a local
	// modifier, or zone-wide gained.
	modGrowth
	// modShrink makes it cover less or differently: zone-wide lost, or size
	// boundaries moved (a record's class can change).
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
		p:       p,
		tracks:  make(map[modTrackKey]*modTrack),
		byScope: make(map[modScopeKey][]*modTrack),
	}
}

// feed processes one encounter check or first sighting (ev.Sighting) in
// zone and returns the transitions it causes. It sweeps first; the prune
// part of the sweep runs at most once per modPruneInterval of event time.
func (l *modifierLearner) feed(ev modifierEvent, zone string) []modifierTransition {
	now := time.Unix(ev.Unix, 0).UTC()
	out := l.endUnseen(now, nil)
	if ev.Unix >= l.nextPrune {
		l.prune(ev.Unix)
	}
	species := modifierScope{PokemonID: ev.PokemonID, Form: ev.Form, Strong: ev.Strong}
	out = l.observe(modScopeKey{species, zone}, ev, now, out)
	if ev.Strong {
		out = l.observe(modScopeKey{allStrongScope, zone}, ev, now, out)
	}
	if ev.Sighting {
		return out
	}
	return l.deviations(ev, species, zone, now, out)
}

// sweep ends the active modifiers unseen for longer than Unseen, at the
// time last seen, and drops tracks with no modifier and no pending
// deviation within Window of now.
func (l *modifierLearner) sweep(now time.Time) []modifierTransition {
	out := l.endUnseen(now, nil)
	l.prune(now.Unix())
	return out
}

// active returns full copies (Outside included) of the candidate and
// confirmed modifiers. Test-only: the runtime publishes through
// modifierRuntime.activeCopies.
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
// (wider suppression), or one whose scope and zone hold another confirmed
// modifier it can dispute a value with (a new overlap withholds it).
func (l *modifierLearner) changed(m *derivationModifier, c modChange) {
	m.version++
	l.dirty.changed = true
	if m.Status != modConfirmed {
		return
	}
	if c == modGrowth && (m.Type == modUnexplained || l.hasDisputingPeer(m)) {
		c = modShrink
	}
	switch c {
	case modGrowth:
		l.dirty.growth = true
	case modShrink:
		l.dirty.shrink = true
	}
}

// hasDisputingPeer reports whether m's scope and zone hold another confirmed
// modifier of m's type (for a pin, of its slot): where both cover a record
// the value is disputed and withheld. Size has one track per scope and zone.
func (l *modifierLearner) hasDisputingPeer(m *derivationModifier) bool {
	if m.Type != modLevelOffset && m.Type != modMovePin {
		return false
	}
	for _, o := range l.byScope[modScopeKey{m.Scope, m.Zone}] {
		if o.mod == nil || o.mod == m || o.mod.Status != modConfirmed || o.key.Type != m.Type {
			continue
		}
		if m.Type == modLevelOffset || o.key.Effect.Slot == m.Effect.Slot {
			return true
		}
	}
	return false
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

// markOutside makes cell an outside cell of m. Adding one ends zone-wide
// coverage, so the change shrinks coverage exactly when m was zone-wide.
func (l *modifierLearner) markOutside(m *derivationModifier, cell s2.CellID) {
	if _, ok := m.Outside[cell]; ok {
		return
	}
	c := modEvidence
	if m.ZoneWide(l.p.ZoneWideCells) {
		c = modShrink
	}
	m.Outside[cell] = struct{}{}
	l.changed(m, c)
}

// observe applies a check or sighting to the existing tracks of one scope:
// it marks active modifiers seen, and an agreement on a track's dimension
// gates its pending deviations and records its cell, or (with a modifier)
// is an in-cell agreement outcome counting toward the end, or makes its
// cell outside. A move check agreeing with the pinned move says nothing.
func (l *modifierLearner) observe(sk modScopeKey, ev modifierEvent, now time.Time,
	out []modifierTransition) []modifierTransition {
	for _, t := range l.byScope[sk] {
		if t.mod != nil && now.After(t.mod.LastSeen) && t.mod.Covers(sk.Zone, ev.Cell, l.p.ZoneWideCells) {
			t.mod.LastSeen = now
		}
		if ev.Sighting || !modAgrees(ev.Agree, t.dim) {
			continue
		}
		if t.size != nil {
			out = l.sizeAgreement(t, ev, now, out)
			continue
		}
		if t.pinHeld(ev) {
			continue
		}
		if t.mod == nil {
			if t.gate {
				t.clearCell(ev.Cell, ev.Unix)
				t.noteAgreed(ev.Cell, ev.Unix)
			}
			continue
		}
		if ev.Cell == 0 {
			continue
		}
		if _, in := t.mod.InCells[ev.Cell]; !in {
			l.markOutside(t.mod, ev.Cell)
			continue
		}
		t.ring.push(modOutAgree)
		t.endOK++
		if t.endOK >= l.p.End {
			out = append(out, l.end(t, now, "agreement"))
			continue
		}
		out = l.settle(t, now, out)
	}
	return out
}

// pinHeld reports whether ev's move in t's slot is t's pinned move: the
// check agrees with the base walk and the pin alike, so says nothing.
func (t *modTrack) pinHeld(ev modifierEvent) bool {
	switch {
	case t.key.Type != modMovePin:
		return false
	case t.dim == modDimQuick:
		return ev.ObsQuick == t.key.Effect.Move
	}
	return ev.ObsCharged == t.key.Effect.Move
}

// settle applies t's outcome ring to its modifier (design §7): it ends by
// contradiction with at least Start contradictions and no fewer than
// matches, and a candidate confirms with Confirm located observations, no
// contradiction and matches at least half the ring (size: no majority).
func (l *modifierLearner) settle(t *modTrack, now time.Time, out []modifierTransition) []modifierTransition {
	mod := t.mod
	match, contra, filled := t.ring.counts()
	if contra >= l.p.Start && contra >= match {
		return append(out, l.end(t, now, "contradicted"))
	}
	if mod.Status == modCandidate && t.located >= l.p.Confirm && contra == 0 &&
		(t.size != nil || 2*match >= filled) {
		mod.Status = modConfirmed
		mod.version++
		out = append(out, modifierTransition{At: now, State: modConfirmed, Mod: mod.clone()})
	}
	return out
}

// modAgrees reports whether a check agrees on d. A level or move agreement
// counts only when the IVs agree too: otherwise the walk did not match and
// the agreement is a coincidence.
func modAgrees(a dimAgreement, d modDim) bool {
	return a.of(d) == dimAgree && (d == modDimIVs || a.IVs == dimAgree)
}

// deviations classifies a check's disagreements as Detector._deviation:
// IVs disagreeing are unexplained and say nothing else; with IVs agreeing,
// the level, quick, charged and size deviations each feed their track.
func (l *modifierLearner) deviations(ev modifierEvent, species modifierScope, zone string,
	now time.Time, out []modifierTransition) []modifierTransition {
	switch ev.Agree.IVs {
	case dimDisagree:
		t := l.track(modTrackKey{modUnexplained, species, modifierEffect{}, zone}, modDimIVs, false)
		return l.match(t, ev, now, out)
	case dimAgree:
	default:
		return out
	}
	if ev.Agree.Level == dimDisagree {
		eff := modifierEffect{Delta: int32(ev.LevelDelta)}
		out = l.matchContradicting(l.track(modTrackKey{modLevelOffset, species, eff, zone}, modDimLevel, false), ev, now, out)
		if ev.Strong {
			out = l.matchContradicting(l.track(modTrackKey{modLevelOffset, allStrongScope, eff, zone}, modDimLevel, true), ev, now, out)
		}
	}
	if ev.Agree.Quick == dimDisagree {
		eff := modifierEffect{Slot: modSlotQuick, Move: ev.ObsQuick}
		out = l.matchContradicting(l.track(modTrackKey{modMovePin, species, eff, zone}, modDimQuick, false), ev, now, out)
	}
	if ev.Agree.Charged == dimDisagree {
		eff := modifierEffect{Slot: modSlotCharged, Move: ev.ObsCharged}
		out = l.matchContradicting(l.track(modTrackKey{modMovePin, species, eff, zone}, modDimCharged, false), ev, now, out)
	}
	if ev.Agree.Size == dimDisagree {
		out = l.sizeDeviation(ev, species, zone, now, out)
	}
	return out
}

// track returns the track for key, creating it.
func (l *modifierLearner) track(key modTrackKey, dim modDim, twoSpecies bool) *modTrack {
	if t, ok := l.tracks[key]; ok {
		return t
	}
	t := &modTrack{key: key, dim: dim, gate: true, twoSpecies: twoSpecies}
	l.tracks[key] = t
	sk := modScopeKey{key.Scope, key.Zone}
	l.byScope[sk] = append(l.byScope[sk], t)
	return t
}

// matchContradicting counts a deviation for t after applying it to every
// other active modifier of t's type, scope and dimension (another delta or
// move): a contradiction in one of its in-cells, else its cell is outside.
func (l *modifierLearner) matchContradicting(t *modTrack, ev modifierEvent, now time.Time,
	out []modifierTransition) []modifierTransition {
	if ev.Cell != 0 {
		for _, o := range l.byScope[modScopeKey{t.key.Scope, t.key.Zone}] {
			if o == t || o.mod == nil || o.key.Type != t.key.Type || o.dim != t.dim {
				continue
			}
			out = l.contradict(o, ev.Cell, now, out)
		}
	}
	return l.match(t, ev, now, out)
}

// contradict applies a contradiction in cell to o's modifier: in an in-cell
// it is counted in the ring, otherwise it makes cell outside.
func (l *modifierLearner) contradict(o *modTrack, cell s2.CellID, now time.Time,
	out []modifierTransition) []modifierTransition {
	if _, in := o.mod.InCells[cell]; !in {
		l.markOutside(o.mod, cell)
		return out
	}
	o.mod.Contradictions++
	o.ring.push(modOutContra)
	l.changed(o.mod, modEvidence)
	return l.settle(o, now, out)
}

// match counts one matching deviation for t.
func (l *modifierLearner) match(t *modTrack, ev modifierEvent, now time.Time,
	out []modifierTransition) []modifierTransition {
	p := l.p
	if t.mod == nil {
		t.pending = append(t.pending, modPending{Unix: ev.Unix, Cell: ev.Cell, Roll: ev.SizeRoll,
			PokemonID: ev.PokemonID, Form: ev.Form, Class: ev.ObsSize})
		t.lastDev = max(t.lastDev, ev.Unix)
		t.pruneBefore(ev.Unix - int64(p.Window/time.Second))
		if len(t.pending) < p.Start || (t.twoSpecies && !t.pendingHasTwoSpecies()) {
			return out
		}
		return l.open(t, ev.Unix-int64(p.Window/time.Second), now, out)
	}

	mod := t.mod
	mod.Observations++
	c := modEvidence
	if ev.Cell != 0 {
		// In-cells and removing an outside cell only ever grow coverage; a
		// zone-wide modifier already covers every cell of its zone.
		wasZoneWide := mod.ZoneWide(p.ZoneWideCells)
		_, wasIn := mod.InCells[ev.Cell]
		mod.addInCell(ev.Cell)
		delete(mod.Outside, ev.Cell)
		if !wasZoneWide && (!wasIn || mod.ZoneWide(p.ZoneWideCells)) {
			c = modGrowth
		}
		t.ring.push(modOutMatch)
		t.located++
		t.endOK = 0
	}
	l.changed(mod, c)
	if now.After(mod.LastSeen) {
		mod.LastSeen = now
	}
	return l.settle(t, now, out)
}

// open makes t's pending deviations a candidate. Each is an observation and
// a match in its cell, except a size roll the interval fit refuses: that is
// a contradiction in an in-cell and makes any other cell outside, as does
// an agreement since cut that no later deviation in its cell overrode.
func (l *modifierLearner) open(t *modTrack, cut int64, now time.Time,
	out []modifierTransition) []modifierTransition {
	mod := &derivationModifier{
		Type: t.key.Type, Scope: t.key.Scope, Effect: t.key.Effect, Zone: t.key.Zone,
		Status:   modCandidate,
		Start:    time.Unix(t.pending[0].Unix, 0).UTC(),
		LastSeen: now,
		InCells:  make(map[s2.CellID]struct{}),
		Outside:  make(map[s2.CellID]struct{}),
	}
	t.ring, t.located = modRing{}, 0
	var cuts []sizeCut
	if t.size != nil {
		cuts = sizeCuts(t.key.Scope.Strong)
		*t.size = sizeEvidence{}
	}
	var refused []s2.CellID
	for _, pd := range t.pending {
		if t.size != nil && !t.size.seedRoll(pd, cuts) {
			if pd.Cell != 0 {
				refused = append(refused, pd.Cell)
			}
			continue
		}
		mod.Observations++
		if pd.Cell != 0 {
			mod.addInCell(pd.Cell)
			t.ring.push(modOutMatch)
			t.located++
		}
	}
	// Refused rolls are pushed after every seeded match, whatever their
	// order: the contradictions stay in the ring longest (conservative).
	for _, c := range refused {
		if _, in := mod.InCells[c]; in {
			mod.Contradictions++
			t.ring.push(modOutContra)
		} else {
			mod.Outside[c] = struct{}{}
		}
	}
	for _, a := range t.agreed {
		if _, in := mod.InCells[a.Cell]; !in {
			mod.Outside[a.Cell] = struct{}{}
		}
	}
	mod.outsideUnknown = t.overflowAt != 0 && t.overflowAt >= cut
	if t.size != nil {
		mod.Boundaries = make(map[string]sizeBoundary, len(cuts))
		t.size.fill(mod.Boundaries, cuts)
	}
	t.pending, t.agreed, t.overflowAt = nil, nil, 0
	t.mod, t.endOK = mod, 0
	l.activeTracks = append(l.activeTracks, t)
	out = append(out, modifierTransition{At: now, State: modCandidate, Mod: mod.clone()})
	return l.settle(t, now, out)
}

func (t *modTrack) pendingHasTwoSpecies() bool {
	first := t.pending[0]
	for _, pd := range t.pending[1:] {
		if pd.PokemonID != first.PokemonID || pd.Form != first.Form {
			return true
		}
	}
	return false
}

// noteAgreed records an agreement in cell at unix for a track without a
// modifier; past modAgreedCap cells it only records the overflow.
func (t *modTrack) noteAgreed(cell s2.CellID, unix int64) {
	if cell == 0 {
		return
	}
	for i := range t.agreed {
		if t.agreed[i].Cell == cell {
			t.agreed[i].Unix = max(t.agreed[i].Unix, unix)
			return
		}
	}
	if len(t.agreed) >= modAgreedCap {
		t.overflowAt = max(t.overflowAt, unix)
		return
	}
	t.agreed = append(t.agreed, modAgreed{cell, unix})
}

// pruneBefore drops t's pending deviations and agreeing cells older than
// cut, and an overflow older than cut.
func (t *modTrack) pruneBefore(cut int64) {
	i := 0
	for i < len(t.pending) && t.pending[i].Unix < cut {
		i++
	}
	t.pending = t.pending[i:]
	kept := t.agreed[:0]
	for _, a := range t.agreed {
		if a.Unix >= cut {
			kept = append(kept, a)
		}
	}
	clear(t.agreed[len(kept):])
	t.agreed = kept
	if t.overflowAt < cut {
		t.overflowAt = 0
	}
}

// clearCell drops t's pending deviations from cell at or before unix.
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

// end ends t's modifier at at.
func (l *modifierLearner) end(t *modTrack, at time.Time, reason string) modifierTransition {
	mod := t.mod
	mod.Status, mod.End, mod.EndReason = modEnded, at, reason
	t.mod, t.endOK, t.ring, t.located = nil, 0, modRing{}, 0
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

// prune drops pending deviations and agreeing cells older than Window
// before unix, then the tracks with no modifier and no deviation within
// Window (pending, or gated away by an agreement).
func (l *modifierLearner) prune(unix int64) {
	l.nextPrune = unix + modPruneInterval
	cut := unix - int64(l.p.Window/time.Second)
	for key, t := range l.tracks {
		if t.mod != nil {
			continue
		}
		t.pruneBefore(cut)
		if len(t.pending) > 0 || t.lastDev >= cut {
			continue
		}
		delete(l.tracks, key)
		sk := modScopeKey{key.Scope, key.Zone}
		ts := l.byScope[sk]
		for j, o := range ts {
			if o == t {
				ts[j] = ts[len(ts)-1]
				ts[len(ts)-1] = nil
				ts = ts[:len(ts)-1]
				break
			}
		}
		if len(ts) == 0 {
			delete(l.byScope, sk)
		} else {
			l.byScope[sk] = ts
		}
	}
}
