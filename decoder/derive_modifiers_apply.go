package decoder

import (
	"math"
	"sync/atomic"

	"github.com/golang/geo/s2"
	"github.com/puzpuzpuz/xsync/v4"

	"golbat/derive/encounter"
	"golbat/tz"
)

// Applying confirmed derivation modifiers (design §5–7, §9–§10). The learner
// publishes an immutable modifierIndex behind derivationModifierIndex; the
// derive path loads it once per derivation and, only when the record's
// (species, form, strong) has an applied modifier, resolves the record's
// level-14 cell and, unless every entry excludes it, the cell's zone. Only
// zone-wide coverage is applied: venue cells are detection evidence, never
// applied. A paused modifier withholds its dimension where it covers. With
// no modifier (nil index) a derivation costs one atomic load.

// derivationModifierIndex holds the active modifiers; nil = none.
var derivationModifierIndex atomic.Pointer[modifierIndex]

// modifierEntry is one confirmed zone-wide modifier as the derive path
// matches it: it covers a record anywhere in its zone except its excluded
// cells. Paused, it withholds its dimension there instead of applying.
type modifierEntry struct {
	typ      modifierType
	effect   modifierEffect
	interval sizeBoundary // size_cutoffs: where the moved cut lies
	cut      int8         // size_cutoffs: its index in the scope's cut list
	paused   bool
	zone     string
	excluded map[s2.CellID]struct{} // ≤ modExcludedMax; shared, never modified
}

// covers reports whether e covers a record in cell, resolving the cell's
// zone through zoneOf into *zone only when needed, at most once across the
// entries of one lookup.
func (e *modifierEntry) covers(cell s2.CellID, zone *string, zoneKnown *bool,
	zoneOf func(s2.CellID) string) bool {
	if _, found := e.excluded[cell]; found {
		return false
	}
	if !*zoneKnown {
		*zone, *zoneKnown = zoneOf(cell), true
	}
	return *zone == e.zone
}

// modifierIndex is an immutable snapshot of the learner's active modifiers.
type modifierIndex struct {
	// confirmed is keyed by species scope (PokemonID, Form, Strong): the
	// modifiers the derive path applies or, paused, withholds for
	// (modifierIndexed).
	confirmed map[modifierScope][]modifierEntry
	// active holds the species scopes with a zone-wide candidate or
	// confirmed modifier: their first sightings feed its last-seen time.
	// The learner tracks no all-strong modifier, and all-strong summaries
	// never reach the index.
	active map[modifierScope]struct{}
}

// modifierIndexed reports whether the derive path looks m up: confirmed,
// zone-wide and not all-strong. A modifier with venue cells only is
// detection evidence (design §9); the learner ends a confirmed one as soon
// as it is not zone-wide. An all-strong level offset is detected and listed
// only: a strong species at base levels kept getting it.
func modifierIndexed(m *derivationModifier) bool {
	return m.Status == modConfirmed && m.ZoneWide && !m.Scope.AllStrong
}

// modifierApplied reports whether the derive path applies m's value: it is
// indexed and not paused. A paused one is still looked up, to withhold its
// dimension (design §10).
func modifierApplied(m *derivationModifier) bool {
	return modifierIndexed(m) && !m.Paused
}

// buildModifierIndex builds the index from the learner's active modifiers
// (published copies, never modified: the index shares their maps). It
// returns nil when nothing is applied or wants sightings.
func buildModifierIndex(mods []*derivationModifier) *modifierIndex {
	idx := &modifierIndex{
		confirmed: make(map[modifierScope][]modifierEntry),
		active:    make(map[modifierScope]struct{}),
	}
	for _, m := range mods {
		if m.Status != modCandidate && m.Status != modConfirmed {
			continue
		}
		// A sighting only marks a modifier seen where it covers: one with no
		// coverage (a candidate from scattered noise, a venue) wants none.
		if m.ZoneWide {
			idx.active[m.Scope] = struct{}{}
		}
		if !modifierIndexed(m) {
			continue
		}
		e := modifierEntry{typ: m.Type, effect: m.Effect, interval: m.Interval, paused: m.Paused,
			zone: m.Zone, excluded: m.Excluded}
		if m.Type == modSizeCutoffs {
			// A boundary the scope's cut list does not have classifies nothing.
			i := sizeCutIndex(m.Scope.Strong, m.Effect.Cut)
			if i < 0 {
				continue
			}
			e.cut = int8(i)
		}
		idx.confirmed[m.Scope] = append(idx.confirmed[m.Scope], e)
	}
	if len(idx.confirmed) == 0 && len(idx.active) == 0 {
		return nil
	}
	return idx
}

// wantsSighting reports whether a first sighting of the scope feeds an
// active modifier: its species scope is active.
func (idx *modifierIndex) wantsSighting(pokemonID, form int32, strong bool) bool {
	if idx == nil {
		return false
	}
	_, ok := idx.active[modifierScope{PokemonID: pokemonID, Form: form, Strong: strong}]
	return ok
}

// derivationAdjust is what the modifiers covering a record change in one
// derivation. The zero value changes nothing.
type derivationAdjust struct {
	// Suppress: derive nothing (unexplained, or a paused level offset: the
	// level is neither the event's nor the base one, and nothing is derived
	// without a level).
	Suppress       bool
	LevelDelta     int32
	Quick, Charged int32 // 0 = no pin
	// NoQuick, NoCharged, NoSize: a paused modifier covers the record, so
	// the move slot or the size is left absent: its value is not applied,
	// and the base value is not derived in its place.
	NoQuick, NoCharged, NoSize bool
	// size holds the applied boundaries' intervals by index in the walk's
	// cut list; bit i of sizeSet is set for those present. The other cuts
	// are the base ones.
	size    [modSizeCutsMax]sizeBoundary
	sizeSet uint8
	// strong is the walk the adjustment was looked up for, the cut list
	// size is read against.
	strong bool
}

// adjustsSize reports whether a changes the size: a moved cut or a withheld
// size.
func (a *derivationAdjust) adjustsSize() bool { return a.NoSize || a.sizeSet != 0 }

// has reports whether the scope has an applied modifier: the only work
// done before a key matches.
func (idx *modifierIndex) has(pokemonID, form int32, strong bool) bool {
	return len(idx.confirmed[modifierScope{PokemonID: pokemonID, Form: form, Strong: strong}]) > 0
}

// lookup combines the indexed modifiers covering a record of the scope in
// cell: one matches a cell it has not excluded in its zone, calling
// zoneOf(cell) at most once per lookup. Unexplained suppresses everything;
// the other types combine, each size boundary on its own cut (a boundary
// that does not cover the cell leaves its cut the base one; sizeClass
// withholds the size when the two do not fit together). A paused modifier
// withholds its dimension instead: a level offset everything, a pin its
// slot, a size boundary the size. Two covering modifiers never dispute a
// value: a record's zone is one, and in one zone two modifiers of one
// scope, type and slot (or size boundary) cannot both hold their zone
// record's share, which zone-wide needs (and the runtime publishes one
// learner state at a time). It does not allocate.
func (idx *modifierIndex) lookup(pokemonID, form int32, strong bool, cell s2.CellID,
	zoneOf func(s2.CellID) string) derivationAdjust {
	entries := idx.confirmed[modifierScope{PokemonID: pokemonID, Form: form, Strong: strong}]
	if len(entries) == 0 {
		return derivationAdjust{}
	}
	adj := derivationAdjust{strong: strong}
	var zone string
	zoneKnown := false
	for i := range entries {
		e := &entries[i]
		if !e.covers(cell, &zone, &zoneKnown, zoneOf) {
			continue
		}
		switch e.typ {
		case modUnexplained:
			return derivationAdjust{Suppress: true, strong: strong}
		case modLevelOffset:
			if e.paused {
				return derivationAdjust{Suppress: true, strong: strong}
			}
			adj.LevelDelta = e.effect.Delta
		case modMovePin:
			switch {
			case e.effect.Slot == modSlotQuick && e.paused:
				adj.NoQuick = true
			case e.effect.Slot == modSlotQuick:
				adj.Quick = e.effect.Move
			case e.effect.Slot == modSlotCharged && e.paused:
				adj.NoCharged = true
			case e.effect.Slot == modSlotCharged:
				adj.Charged = e.effect.Move
			}
		case modSizeCutoffs:
			if e.paused {
				adj.NoSize = true
			} else {
				adj.size[e.cut] = e.interval
				adj.sizeSet |= 1 << e.cut
			}
		}
	}
	return adj
}

// deriveZoneCacheMax bounds each cell → zone cache (the learner's, the
// decode path's and the events writer's): an insert that would take one
// past it clears the cache first. Each entry costs a timezone search to
// refill; 200,000 level-14 cells is ~200,000 km² of scanned area.
var deriveZoneCacheMax = 200_000

// modifierZoneCache is the decode path's cell → zone cache, filled only by
// lookups that reach an applied modifier's zone check (the learner keeps
// its own), at most deriveZoneCacheMax entries.
var modifierZoneCache = xsync.NewMap[s2.CellID, string]()

// modifierZoneOf is the IANA zone at cell's centre, "" when unknown or for
// cell 0.
func modifierZoneOf(cell s2.CellID) string {
	if cell == 0 || !cell.IsValid() {
		return ""
	}
	if z, ok := modifierZoneCache.Load(cell); ok {
		return z
	}
	ll := cell.LatLng()
	z := tz.SearchTimezone(ll.Lat.Degrees(), ll.Lng.Degrees())
	// Concurrent misses may overshoot the cap by the number of decoders.
	// Clear allocates a fresh table on the decode path, once per ~200,000
	// misses, each already paying a timezone search.
	if modifierZoneCache.Size() >= deriveZoneCacheMax {
		modifierZoneCache.Clear()
	}
	modifierZoneCache.Store(cell, z)
	return z
}

// pokemonModifierCell is the record's level-14 cell: the parent of its
// level-15 map cell, else of its lat/lon, else 0.
func pokemonModifierCell(p *Pokemon) s2.CellID {
	return modifierCell(p.CellId.ValueOrZero(), p.Lat, p.Lon)
}

// modifierAdjust is the adjustment confirmed modifiers make to this record's
// derivation on its stored strong flag; zero when there is no index, the
// record is a Ditto, or nothing matches.
func (p *Pokemon) modifierAdjust() derivationAdjust {
	return p.modifierAdjustAs(p.IsStrong.ValueOrZero())
}

// modifierAdjustAs is modifierAdjust for the given walk, for a sighting that
// is about to change the stored strong flag (repopulateIv).
func (p *Pokemon) modifierAdjustAs(strong bool) derivationAdjust {
	idx := derivationModifierIndex.Load()
	if idx == nil || p.IsDitto {
		return derivationAdjust{}
	}
	pokemonID, form := int32(p.PokemonId), int32(int64OrZero(p.Form))
	if !idx.has(pokemonID, form, strong) {
		return derivationAdjust{}
	}
	return idx.lookup(pokemonID, form, strong, pokemonModifierCell(p), modifierZoneOf)
}

// level is the derived level with the delta applied, clamped to [1, 50].
func (a derivationAdjust) level(derived int32) int32 {
	if a.LevelDelta == 0 {
		return derived
	}
	return min(max(derived+a.LevelDelta, 1), 50)
}

// sizeClass classifies roll with the moved cuts: each applied boundary's
// cut is replaced by its interval, below Lo the lower class, at or above Hi
// the upper, inside it no class (false: leave size absent). An interval
// with no Lo starts at the cut before it; one with no Hi leaves every roll
// from its Lo up absent. The cuts must still be in order: when an applied
// interval passes a neighbouring cut (a base one where that boundary does
// not cover the record, or another interval), the two were learned apart
// and do not describe one set of cuts, so no roll gets a class. A withheld
// size is absent; a form whose size is fixed keeps base. It does not
// allocate.
func (a derivationAdjust) sizeClass(roll float64, strong bool, form int32, base int32) (int32, bool) {
	if encounter.FormFixesSize(form) {
		return base, true
	}
	if a.NoSize {
		return 0, false
	}
	cuts := sizeCuts(strong)
	class, ok, done := int32(cuts[len(cuts)-1].below)+1, true, false
	floor := math.Inf(-1) // the end of the cut before
	for i, c := range cuts {
		lo, hi := c.at, c.at
		if a.sizeSet&(1<<i) != 0 {
			b := a.size[i]
			lo, hi = floor, math.Inf(1)
			if b.HasLo {
				if lo = b.Lo; lo < floor {
					return 0, false
				}
			}
			if b.HasHi {
				if hi = b.Hi; hi < floor {
					return 0, false
				}
			}
		} else if lo < floor {
			return 0, false
		}
		// Learned intervals always have Lo < Hi (fit refuses the roll that
		// would empty one), so the two tests below cannot overlap.
		if !done && roll < lo {
			class, ok, done = int32(c.below), true, true
		} else if !done && roll < hi {
			class, ok, done = 0, false, true
		}
		if math.IsInf(hi, 1) {
			break // nothing is known above lo: every roll there is absent
		}
		floor = hi
	}
	return class, ok
}
