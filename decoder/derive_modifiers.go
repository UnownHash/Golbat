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
	EndReason            string // "agreement" | "contrary" | "unseen"
	Observations         int
	Contradictions       int
	// InCells holds the cells with a matching deviation; Outside the cells
	// that agreed on the dimension while the modifier was active and are not
	// in-cells.
	InCells, Outside map[s2.CellID]struct{}
}

// ZoneWide reports whether m covers its whole zone: at least zoneWideCells
// in-cells and no outside cell.
func (m *derivationModifier) ZoneWide(zoneWideCells int) bool {
	return len(m.InCells) >= zoneWideCells && len(m.Outside) == 0
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

// modifierParams are the learner's thresholds (design §3).
type modifierParams struct {
	Start, Confirm, End, ZoneWideCells int
	Window, Unseen                     time.Duration
}

func defaultModifierParams() modifierParams {
	return modifierParams{Start: 3, Confirm: 10, End: 20, ZoneWideCells: 3,
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

// modTrack is one (type, scope, effect, zone): its recent deviations while
// it has no modifier, else the modifier and the in-cell agreements since its
// last matching deviation.
type modTrack struct {
	key  modTrackKey
	dim  modDim
	gate bool // an agreement in a cell discards that cell's pending deviations
	// twoSpecies: a candidate needs two species among pending (all-strong
	// level offset).
	twoSpecies bool
	pending    []modPending // time order, oldest first
	mod        *derivationModifier
	endOK      int
	size       *sizeEvidence // size_cutoffs tracks only
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

// active returns copies of the candidate and confirmed modifiers.
func (l *modifierLearner) active() []*derivationModifier {
	out := make([]*derivationModifier, 0, len(l.activeTracks))
	for _, t := range l.activeTracks {
		out = append(out, t.mod.clone())
	}
	return out
}

func (l *modifierLearner) trackCount() int { return len(l.tracks) }

// observe applies a check or sighting to the existing tracks of one scope:
// it marks active modifiers seen, and an agreement on a track's dimension
// gates its pending deviations or counts toward its end.
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
		if t.mod == nil {
			if t.gate {
				t.clearCell(ev.Cell, ev.Unix)
			}
			continue
		}
		if ev.Cell == 0 {
			continue
		}
		if _, in := t.mod.InCells[ev.Cell]; !in {
			t.mod.Outside[ev.Cell] = struct{}{}
			continue
		}
		t.endOK++
		if t.endOK >= l.p.End {
			out = append(out, l.end(t, now, "agreement"))
		}
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

// matchContradicting counts a deviation for t after contradicting every
// other active modifier of t's type, scope and dimension (another delta or
// move) that has the deviation's cell as an in-cell.
func (l *modifierLearner) matchContradicting(t *modTrack, ev modifierEvent, now time.Time,
	out []modifierTransition) []modifierTransition {
	if ev.Cell != 0 {
		for _, o := range l.byScope[modScopeKey{t.key.Scope, t.key.Zone}] {
			if o == t || o.mod == nil || o.key.Type != t.key.Type || o.dim != t.dim {
				continue
			}
			if _, in := o.mod.InCells[ev.Cell]; in {
				o.mod.Contradictions++
			}
		}
	}
	return l.match(t, ev, now, out)
}

// match counts one matching deviation for t.
func (l *modifierLearner) match(t *modTrack, ev modifierEvent, now time.Time,
	out []modifierTransition) []modifierTransition {
	p := l.p
	if t.mod == nil {
		t.pending = append(t.pending, modPending{Unix: ev.Unix, Cell: ev.Cell, Roll: ev.SizeRoll,
			PokemonID: ev.PokemonID, Form: ev.Form, Class: ev.ObsSize})
		cut := ev.Unix - int64(p.Window/time.Second)
		i := 0
		for i < len(t.pending) && t.pending[i].Unix < cut {
			i++
		}
		t.pending = t.pending[i:]
		if len(t.pending) < p.Start || (t.twoSpecies && !t.pendingHasTwoSpecies()) {
			return out
		}
		mod := &derivationModifier{
			Type: t.key.Type, Scope: t.key.Scope, Effect: t.key.Effect, Zone: t.key.Zone,
			Status:       modCandidate,
			Start:        time.Unix(t.pending[0].Unix, 0).UTC(),
			LastSeen:     now,
			Observations: len(t.pending),
			InCells:      make(map[s2.CellID]struct{}),
			Outside:      make(map[s2.CellID]struct{}),
		}
		for _, pd := range t.pending {
			if pd.Cell != 0 {
				mod.InCells[pd.Cell] = struct{}{}
			}
		}
		if t.size != nil {
			mod.Boundaries = t.size.seed(t.pending, sizeCuts(t.key.Scope.Strong))
		}
		t.pending, t.mod, t.endOK = nil, mod, 0
		l.activeTracks = append(l.activeTracks, t)
		out = append(out, modifierTransition{At: now, State: modCandidate, Mod: mod.clone()})
		if mod.Observations >= p.Confirm {
			mod.Status = modConfirmed
			out = append(out, modifierTransition{At: now, State: modConfirmed, Mod: mod.clone()})
		}
		return out
	}

	mod := t.mod
	mod.Observations++
	t.endOK = 0
	if ev.Cell != 0 {
		mod.InCells[ev.Cell] = struct{}{}
		delete(mod.Outside, ev.Cell)
	}
	if now.After(mod.LastSeen) {
		mod.LastSeen = now
	}
	if mod.Status == modCandidate && mod.Observations >= p.Confirm && mod.Contradictions == 0 {
		mod.Status = modConfirmed
		out = append(out, modifierTransition{At: now, State: modConfirmed, Mod: mod.clone()})
	}
	return out
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
	t.mod, t.endOK = nil, 0
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

// prune drops pending deviations older than Window before unix, then the
// tracks left with neither a modifier nor a pending deviation.
func (l *modifierLearner) prune(unix int64) {
	l.nextPrune = unix + modPruneInterval
	cut := unix - int64(l.p.Window/time.Second)
	for key, t := range l.tracks {
		if t.mod != nil {
			continue
		}
		i := 0
		for i < len(t.pending) && t.pending[i].Unix < cut {
			i++
		}
		t.pending = t.pending[i:]
		if len(t.pending) > 0 {
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
