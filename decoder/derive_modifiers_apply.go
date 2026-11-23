package decoder

import (
	"math"
	"slices"
	"sync/atomic"

	"github.com/golang/geo/s2"
	"github.com/puzpuzpuz/xsync/v4"

	"golbat/derive/encounter"
	"golbat/tz"
)

// Applying confirmed derivation modifiers (design §5–7). The learner
// publishes an immutable modifierIndex behind derivationModifierIndex; the
// derive path loads it once per derivation and, only when the record's
// (species, form, strong) has a confirmed modifier, resolves the record's
// level-14 cell — and its zone only for a zone-wide modifier that does not
// cover the cell as a venue and has not excluded it (design §8). With no
// modifier (nil index) a derivation costs one atomic load.

// derivationModifierIndex holds the active modifiers; nil = none.
var derivationModifierIndex atomic.Pointer[modifierIndex]

// modifierEntry is one confirmed modifier as the derive path matches it: a
// record is covered in one of its venue cells, or anywhere in its zone when
// zone-wide and the cell is not excluded.
type modifierEntry struct {
	typ      modifierType
	effect   modifierEffect
	size     map[string]sizeBoundary // size_cutoffs; shared, never modified
	zone     string
	zoneWide bool
	excluded []s2.CellID            // sorted, ≤ 16; shared, never modified
	cells    map[s2.CellID]struct{} // covered venue cells; shared, never modified
}

// covers reports whether e covers a record in cell, resolving the cell's
// zone through zoneOf into *zone only when needed, at most once across the
// entries of one lookup.
func (e *modifierEntry) covers(cell s2.CellID, zone *string, zoneKnown *bool,
	zoneOf func(s2.CellID) string) bool {
	if _, in := e.cells[cell]; in && cell != 0 {
		return true
	}
	if !e.zoneWide {
		return false
	}
	if _, found := slices.BinarySearch(e.excluded, cell); found {
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
	// confirmed modifiers the derive path applies. All-strong modifiers are
	// never applied (design §7) and have no entry.
	confirmed map[modifierScope][]modifierEntry
	// active holds the species scopes with a candidate or confirmed
	// modifier that covers something; anyAllStrong whether an all-strong one
	// is active.
	active       map[modifierScope]struct{}
	anyAllStrong bool
}

// modifierApplied reports whether the derive path applies m: confirmed and
// not all-strong. An all-strong level offset is detected and listed only: a
// strong species at base levels kept getting it.
func modifierApplied(m *derivationModifier) bool {
	return m.Status == modConfirmed && !m.Scope.AllStrong
}

// buildModifierIndex builds the index from the learner's active modifiers
// (published copies, never modified: the index shares their maps). It
// returns nil when no modifier is active.
func buildModifierIndex(mods []*derivationModifier) *modifierIndex {
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
		// A sighting only marks a modifier seen where it covers: one with no
		// coverage (a candidate from scattered noise) wants none.
		switch {
		case m.Scope.AllStrong:
			idx.anyAllStrong = true
		case m.ZoneWide || len(m.Cells) > 0:
			idx.active[m.Scope] = struct{}{}
		}
		if !modifierApplied(m) {
			continue
		}
		e := modifierEntry{typ: m.Type, effect: m.Effect, size: m.Boundaries, zone: m.Zone,
			zoneWide: m.ZoneWide, cells: m.Cells}
		if m.ZoneWide {
			e.excluded = m.Excluded
		}
		idx.confirmed[m.Scope] = append(idx.confirmed[m.Scope], e)
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
	Suppress       bool // unexplained or a disputed level: derive nothing
	LevelDelta     int32
	Quick, Charged int32                   // 0 = no pin
	Size           map[string]sizeBoundary // nil = base cuts
	// WithholdQuick, WithholdCharged and WithholdSize leave that value
	// absent, neither pinned nor derived: two confirmed modifiers dispute
	// it, and the base value is likely wrong too.
	WithholdQuick, WithholdCharged, WithholdSize bool
	// strong is the walk the adjustment was looked up for, the cut list
	// Size is read against.
	strong bool
}

// has reports whether the scope has a confirmed modifier: the only work
// done before a key matches.
func (idx *modifierIndex) has(pokemonID, form int32, strong bool) bool {
	return len(idx.confirmed[modifierScope{PokemonID: pokemonID, Form: form, Strong: strong}]) > 0
}

// lookup combines the confirmed modifiers covering a record of the scope in
// cell. A covered venue cell matches by membership alone (never cell 0); a
// zone-wide modifier also matches a cell it has not excluded in its zone,
// calling zoneOf(cell) at most once per lookup. Unexplained
// suppresses everything. Two covering modifiers that dispute a value
// withhold it rather than pick one (design §7): two level deltas suppress
// (no IVs or level, as unexplained), two pins of one slot with different
// moves withhold that slot, two size modifiers withhold size. It does not
// allocate.
func (idx *modifierIndex) lookup(pokemonID, form int32, strong bool, cell s2.CellID,
	zoneOf func(s2.CellID) string) derivationAdjust {
	entries := idx.confirmed[modifierScope{PokemonID: pokemonID, Form: form, Strong: strong}]
	if len(entries) == 0 {
		return derivationAdjust{}
	}
	adj := derivationAdjust{strong: strong}
	var zone string
	zoneKnown, levelSet, sizeSet := false, false, false
	for i := range entries {
		e := &entries[i]
		if !e.covers(cell, &zone, &zoneKnown, zoneOf) {
			continue
		}
		switch e.typ {
		case modUnexplained:
			return derivationAdjust{Suppress: true, strong: strong}
		case modLevelOffset:
			if levelSet && adj.LevelDelta != e.effect.Delta {
				return derivationAdjust{Suppress: true, strong: strong}
			}
			adj.LevelDelta, levelSet = e.effect.Delta, true
		case modMovePin:
			switch e.effect.Slot {
			case modSlotQuick:
				adj.Quick, adj.WithholdQuick = pinSlot(adj.Quick, adj.WithholdQuick, e.effect.Move)
			case modSlotCharged:
				adj.Charged, adj.WithholdCharged = pinSlot(adj.Charged, adj.WithholdCharged, e.effect.Move)
			}
		case modSizeCutoffs:
			if sizeSet {
				adj.Size, adj.WithholdSize = nil, true
			} else if sizeSet = true; len(e.size) > 0 {
				adj.Size = e.size
			}
		}
	}
	return adj
}

// pinSlot folds one more covering pin of move into a slot holding pinned
// (0 = none) and withheld: a different move disputes the slot.
func pinSlot(pinned int32, withheld bool, move int32) (int32, bool) {
	switch {
	case withheld:
		return 0, true
	case pinned != 0 && pinned != move:
		return 0, true
	}
	return move, false
}

// modifierZoneCache is the decode path's cell → zone cache, filled only by
// zone-wide matches (the learner keeps its own). Never pruned: it holds only
// the cells where an affected species was derived under a zone-wide
// modifier, bounded by the scanned area.
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
