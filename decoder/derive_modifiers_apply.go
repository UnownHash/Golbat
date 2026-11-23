package decoder

import (
	"math"
	"sync/atomic"

	"github.com/golang/geo/s2"
	"github.com/puzpuzpuz/xsync/v4"

	"golbat/derive/encounter"
	"golbat/tz"
)

// Applying confirmed derivation modifiers (design §5–7, §9). The learner
// publishes an immutable modifierIndex behind derivationModifierIndex; the
// derive path loads it once per derivation and, only when the record's
// (species, form, strong) has an applied modifier, resolves the record's
// level-14 cell and, unless every entry excludes it, the cell's zone. Only
// zone-wide coverage is applied: venue cells are detection evidence, never
// applied. With no modifier (nil index) a derivation costs one atomic load.

// derivationModifierIndex holds the active modifiers; nil = none.
var derivationModifierIndex atomic.Pointer[modifierIndex]

// modifierEntry is one applied modifier as the derive path matches it: it
// covers a record anywhere in its zone except its excluded cells.
type modifierEntry struct {
	typ      modifierType
	effect   modifierEffect
	size     map[string]sizeBoundary // size_cutoffs; shared, never modified
	zone     string
	excluded map[s2.CellID]uint8 // ≤ modExcludedMax; shared, never modified
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
	// modifiers the derive path applies (modifierApplied).
	confirmed map[modifierScope][]modifierEntry
	// active holds the species scopes with a zone-wide candidate or
	// confirmed modifier: their first sightings feed its last-seen time.
	// The learner tracks no all-strong modifier, and all-strong summaries
	// never reach the index.
	active map[modifierScope]struct{}
}

// modifierApplied reports whether the derive path applies m: confirmed,
// zone-wide and not all-strong. A modifier with venue cells only is
// detection evidence (design §9); the learner ends a confirmed one as soon
// as it is not zone-wide. An all-strong level offset is detected and listed
// only: a strong species at base levels kept getting it.
func modifierApplied(m *derivationModifier) bool {
	return m.Status == modConfirmed && m.ZoneWide && !m.Scope.AllStrong
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
		if !modifierApplied(m) {
			continue
		}
		e := modifierEntry{typ: m.Type, effect: m.Effect, size: m.Boundaries, zone: m.Zone,
			excluded: m.Excluded}
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

// derivationAdjust is what applied modifiers change in one derivation. The
// zero value changes nothing.
type derivationAdjust struct {
	Suppress       bool // unexplained: derive nothing
	LevelDelta     int32
	Quick, Charged int32                   // 0 = no pin
	Size           map[string]sizeBoundary // nil = base cuts
	// strong is the walk the adjustment was looked up for, the cut list
	// Size is read against.
	strong bool
}

// has reports whether the scope has an applied modifier: the only work
// done before a key matches.
func (idx *modifierIndex) has(pokemonID, form int32, strong bool) bool {
	return len(idx.confirmed[modifierScope{PokemonID: pokemonID, Form: form, Strong: strong}]) > 0
}

// lookup combines the applied modifiers covering a record of the scope in
// cell: one matches a cell it has not excluded in its zone, calling
// zoneOf(cell) at most once per lookup. Unexplained suppresses everything;
// the other types combine. Two covering modifiers never dispute a value:
// a record's zone is one, and in one zone two modifiers of one scope, type
// and slot cannot both hold the zone record's majority, which zone-wide
// needs (and the runtime publishes one learner state at a time). It does
// not allocate.
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
			adj.LevelDelta = e.effect.Delta
		case modMovePin:
			switch e.effect.Slot {
			case modSlotQuick:
				adj.Quick = e.effect.Move
			case modSlotCharged:
				adj.Charged = e.effect.Move
			}
		case modSizeCutoffs:
			if len(e.size) > 0 {
				adj.Size = e.size
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

// sizeClass classifies roll with the moved cuts: each listed boundary's cut
// is replaced by its interval, below Lo the lower class, at or above Hi the
// upper, inside it no class (false: leave size absent). A form whose size
// is fixed keeps base.
func (a derivationAdjust) sizeClass(roll float64, strong bool, form int32, base int32) (int32, bool) {
	if encounter.FormFixesSize(form) {
		return base, true
	}
	cuts := sizeCuts(strong)
	for _, c := range cuts {
		lo, hi := c.at, c.at
		if b, ok := a.Size[c.name]; ok {
			lo, hi = math.Inf(-1), math.Inf(1)
			if b.HasLo {
				lo = b.Lo
			}
			if b.HasHi {
				hi = b.Hi
			}
		}
		// Learned boundaries always have Lo < Hi (sizeEvidence never holds an
		// empty interval), so the two tests below cannot overlap.
		if roll < lo {
			return int32(c.below), true
		}
		if roll < hi {
			return 0, false
		}
	}
	return int32(cuts[len(cuts)-1].below) + 1, true
}
