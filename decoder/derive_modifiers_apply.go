package decoder

import (
	"math"
	"sync/atomic"

	"github.com/golang/geo/s2"
	"github.com/puzpuzpuz/xsync/v4"

	"golbat/derive/encounter"
	"golbat/tz"
)

// Applying confirmed derivation modifiers (design §5–6). The learner
// publishes an immutable modifierIndex behind derivationModifierIndex; the
// derive path loads it once per derivation and, only when the record's
// (species, form, strong) or the all-strong key has a confirmed modifier,
// resolves the record's level-14 cell — and its zone only for a zone-wide
// modifier. With no modifier (nil index) a derivation costs one atomic load.

// derivationModifierIndex holds the active modifiers; nil = none.
var derivationModifierIndex atomic.Pointer[modifierIndex]

// modifierEntry is one confirmed modifier as the derive path matches it.
type modifierEntry struct {
	typ      modifierType
	effect   modifierEffect
	size     map[string]sizeBoundary // size_cutoffs; shared, never modified
	zone     string
	zoneWide bool
	inCells  map[s2.CellID]struct{} // shared, never modified
}

// modifierIndex is an immutable snapshot of the learner's active modifiers.
type modifierIndex struct {
	// confirmed is keyed by species scope (PokemonID, Form, Strong).
	confirmed map[modifierScope][]modifierEntry
	allStrong []modifierEntry // confirmed all-strong modifiers
	// active holds the species scopes with a candidate or confirmed
	// modifier; anyAllStrong whether an all-strong one is active.
	active       map[modifierScope]struct{}
	anyAllStrong bool
}

// buildModifierIndex builds the index from the learner's active modifiers
// (copies it may keep: the index shares their maps). It returns nil when no
// modifier is active.
func buildModifierIndex(mods []*derivationModifier, zoneWideCells int) *modifierIndex {
	idx := &modifierIndex{
		confirmed: make(map[modifierScope][]modifierEntry),
		active:    make(map[modifierScope]struct{}),
	}
	n := 0
	for _, m := range mods {
		if m.Status != modCandidate && m.Status != modConfirmed {
			continue
		}
		n++
		if m.Scope.AllStrong {
			idx.anyAllStrong = true
		} else {
			idx.active[m.Scope] = struct{}{}
		}
		if m.Status != modConfirmed {
			continue
		}
		e := modifierEntry{typ: m.Type, effect: m.Effect, size: m.Boundaries, zone: m.Zone,
			zoneWide: m.ZoneWide(zoneWideCells), inCells: m.InCells}
		if m.Scope.AllStrong {
			idx.allStrong = append(idx.allStrong, e)
		} else {
			idx.confirmed[m.Scope] = append(idx.confirmed[m.Scope], e)
		}
	}
	if n == 0 {
		return nil
	}
	return idx
}

// wantsSighting reports whether a first sighting of the scope feeds an
// active modifier: its species scope is active, or it is strong and an
// all-strong modifier is.
func (idx *modifierIndex) wantsSighting(pokemonID, form int32, strong bool) bool {
	if idx == nil {
		return false
	}
	if strong && idx.anyAllStrong {
		return true
	}
	_, ok := idx.active[modifierScope{PokemonID: pokemonID, Form: form, Strong: strong}]
	return ok
}

// derivationAdjust is what confirmed modifiers change in one derivation. The
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

// has reports whether the scope or (strong) the all-strong key has a
// confirmed modifier: the only work done before a key matches.
func (idx *modifierIndex) has(pokemonID, form int32, strong bool) bool {
	if strong && len(idx.allStrong) > 0 {
		return true
	}
	return len(idx.confirmed[modifierScope{PokemonID: pokemonID, Form: form, Strong: strong}]) > 0
}

// lookup combines the confirmed modifiers covering a record of the scope in
// cell. A local modifier matches by in-cell membership alone (never cell 0);
// a zone-wide one calls zoneOf(cell), at most once per lookup. The species
// scope wins over the all-strong key for each effect; unexplained in either
// suppresses everything.
func (idx *modifierIndex) lookup(pokemonID, form int32, strong bool, cell s2.CellID,
	zoneOf func(s2.CellID) string) derivationAdjust {
	species := idx.confirmed[modifierScope{PokemonID: pokemonID, Form: form, Strong: strong}]
	var all []modifierEntry
	if strong {
		all = idx.allStrong
	}
	if len(species) == 0 && len(all) == 0 {
		return derivationAdjust{}
	}
	adj := derivationAdjust{strong: strong}
	var zone string
	zoneKnown, levelSet := false, false
	for _, entries := range [2][]modifierEntry{species, all} {
		for i := range entries {
			e := &entries[i]
			if e.zoneWide {
				if !zoneKnown {
					zone, zoneKnown = zoneOf(cell), true
				}
				if zone != e.zone {
					continue
				}
			} else if _, in := e.inCells[cell]; !in || cell == 0 {
				continue
			}
			switch e.typ {
			case modUnexplained:
				return derivationAdjust{Suppress: true, strong: strong}
			case modLevelOffset:
				if !levelSet {
					adj.LevelDelta, levelSet = e.effect.Delta, true
				}
			case modMovePin:
				if e.effect.Slot == modSlotQuick && adj.Quick == 0 {
					adj.Quick = e.effect.Move
				} else if e.effect.Slot == modSlotCharged && adj.Charged == 0 {
					adj.Charged = e.effect.Move
				}
			case modSizeCutoffs:
				if adj.Size == nil && len(e.size) > 0 {
					adj.Size = e.size
				}
			}
		}
	}
	return adj
}

// modifierZoneCache is the decode path's cell → zone cache, filled only by
// zone-wide matches (the learner keeps its own).
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
