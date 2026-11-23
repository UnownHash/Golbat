# Derivation Modifiers — Live Learner Design

> **Status:** Agreed 2026-11-17; building on branch
> t3/encounter-override-heuristics (PR #441).

- **Date:** 2026-11-17
- **Follows:** `2026-11-15-derivation-event-modifiers-design.md` (§7, the
  "learner in Golbat" follow-up). Read that first: modifier types, lifecycle
  and evidence come from it and from the prototype `tools/eventreplay.py`.

## 1. What changes

The prototype only observes. This adds an opt-in learner inside Golbat that
detects modifiers from live encounters and applies confirmed ones to derived
values, so a Pokemon never encountered during an event is published with the
event's values instead of the base derivation.

Evidence gathered since the prototype:

- **Forced moves:** 2026-11-15, strong 635/2297 → 367 and strong 959/3272 →
  494, every one of ~4,900 encounters, until 18:00 UTC; the forms then
  stopped spawning.
- **Size cuts:** Rattata, both lower cuts +0.008 (XXS < ~0.012, XS < ~0.037),
  upper cuts unchanged; every mismatch in the two shifted bands.
- **Level offset:** Safari, strong +5 (reports).
- Events start on the hour, local time. Ticketed events are bounded by S2
  level-14 cells.

## 2. Scope: timezone and level-14 cells

Every modifier belongs to one **timezone** (IANA zone name from
`tz.SearchTimezone` at the level-14 cell centre; "" when unknown). Within
it, evidence is kept per **level-14 cell**, and an agreement acts only in
its own cell:

- Before a modifier exists, an agreement in cell c discards pending
  deviations from cell c only.
- A cell joins the modifier's **in-cells** on its first matching deviation.
- A cell where the dimension agrees while the modifier is active, and which
  is not an in-cell, becomes an **outside cell**. For size, where most rolls
  agree under any cut, a cell becomes outside on a contrary roll (§4)
  instead.

**Region:** a modifier is **zone-wide** when it has at least
`zone_wide_cells` (3) in-cells and no outside cell; otherwise it is
**local** to its in-cells. Zone-wide applies to every cell of the timezone,
including cells not yet seen. A ticketed venue: its cells deviate, cells
elsewhere agree and become outside, so the modifier stays local to the
venue. A global event: deviations everywhere, nobody agrees, so it goes
zone-wide.

## 3. Lifecycle (per modifier, judged in its region)

Thresholds as in the prototype: `start` 3, `confirm` 10, `end` 20, `window`
1800 s, `unseen` 3600 s.

- **candidate:** `start` matching deviations within `window` (all-strong
  level offset: from at least two species).
- **confirmed:** `confirm` matching deviations and no contradiction.
  A contradiction is a deviation for another effect of the same type, scope
  and slot in one of the modifier's in-cells (for size, a roll that would
  empty an interval).
- **ended:**
  - `end` agreements in in-cells since the modifier's last matching
    deviation (not size); or
  - size: a contrary roll in an in-cell; or
  - unseen: no check or sighting of the scope in its region for `unseen`
    seconds, ended at the time last seen.

## 4. Modifier types

As the prototype: `level_offset` (species scope; and all-strong, from two
or more species), `move_pin` (quick or charged), `size_cutoffs` (an interval
per moved cut on the raw roll, base cuts 0.004/0.029/0.971/0.996, strong
0.8), `unexplained` (IVs disagree). Move pin and size need the IVs to agree.
Size evidence uses every IV-agreeing roll in an in-cell, deviations and
agreements alike. A contrary roll: one in a listed boundary's disputed
region classified by the base cut.

## 5. Applying

Only **confirmed** modifiers apply, only to a record in their region, never
to a Ditto, never over a value an encounter reported (derived values fill
absent fields only, as today).

| Type | Effect on derived values |
|---|---|
| level_offset | level + delta (both scopes add if both are confirmed for the same delta; one delta per record: species scope wins) |
| move_pin | the pinned slot gets the pinned move |
| size_cutoffs | size reclassified from `SizeRoll` with each listed boundary's interval: below lo → lower class, at or above hi → upper class, inside → size left absent; forms with fixed size untouched |
| unexplained | no derived IVs, level, size or moves for the scope (as derive_iv off) |

Records derived before a modifier confirms keep their values (no
retroactive correction, no webhook re-send).

## 6. Runtime

- Config `[derivation_modifiers]`, `enabled = false`; needs `derive_iv`.
  Thresholds configurable.
- One learner goroutine fed by a channel from the encounter check and the
  first-sighting hook already used by the events log; drop and count on a
  full channel (decode-path invariant). The channel carries a compact,
  pointer-free event (~56 bytes, capacity 16384, ~1 MB), not the events
  log's 288-byte record (65536 of those would be ~19 MB allocated up front
  and scanned by the GC). A first sighting is sent only when its scope has
  an active modifier: outside events there is no sighting traffic at all.
- Active modifiers are published as an immutable index behind an atomic
  pointer (nil when none, so the normal derive costs one atomic load); the
  derive path does a lock-free lookup by (species, form, strong) plus the
  all-strong key, and only on a hit computes the level-14 cell (local:
  in-cell membership) or, for a zone-wide modifier, the zone. The decode
  path's zone cache therefore holds only cells where an affected species was
  derived; the learner keeps its own cell → zone-number map.
- Transitions logged as `[DERIVE_MODIFIER]`. State is in memory only; a
  restart relearns.
- `GET /api/derivation/modifiers` returns the active modifiers in the
  prototype's shareable JSON plus `region` (`zone`, `cells` or null for
  zone-wide); `/api/status` gains `features.derivation_modifiers`.

## 7. Not included

Loading a shared modifier file; retroactive correction; detection in the
offline script by cell (the events log carries no per-cell agreements; the
script stays per scope).
