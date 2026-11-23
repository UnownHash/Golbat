# Derivation Modifiers — Live Learner Design

> **Status:** Agreed 2026-11-17, revised 2026-11-23 (§7); building on branch
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
  pointer (nil when none, so the normal derive costs one atomic load). The
  worker republishes it, with the API snapshot, on every transition and on
  every change to a confirmed modifier's coverage (in-cells added, zone-wide
  lost or regained, size boundaries moved) at once; evidence-only changes
  (observations, contradictions, a candidate's cells) at most once a second.
  The learner flags such changes itself, so an event that changes nothing
  rebuilds nothing. The
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

## 7. Revision after independent review (2026-11-23)

Two independent reviews (Fable, Codex) and the Dragonite sampled mode
(Dragonite PR #681) found rules above that publish wrong values. These
rules replace the ones they contradict; §2–§6 stand otherwise.

**Evidence.**
- Each spawn counts once: the decode site feeds the learner only the first
  encounter of a record (from the record's own state, no new map), whatever
  the scanner. Repeated encounters of one spawn are not independent evidence.
- Learner events carry the server's clock at submit, never a client or map
  timestamp: one future-stamped sighting ended every modifier.
- A wild update derives with the cell it is placed in, not the previous
  sighting's.

**Outcomes per modifier.** Each active modifier keeps a ring of the last 16
in-cell outcomes on its dimension: match (its effect), agree (base value) or
contra (another effect of the same type, scope and slot).
- *Confirm:* `confirm` observations, no contra in the ring, and matches at
  least half the ring. Noise (a few scattered IV mismatches among many
  agreements) never confirms.
- *End by agreement:* `end` consecutive in-cell agreements; only an in-cell
  match resets the count. Default `end` = **8**, at most Dragonite's hourly
  minimum (12), so an event that ends on the local hour while its species
  keeps spawning ends with the next hour's first sampled encounters. Every
  effect seen applies to the whole scope, so 8 straight agreements in its
  cells is decisive.
- *End by contradiction (new, reason `contradicted`):* at least `start`
  contras in the ring and no fewer contras than matches. A superseded pin
  ends instead of masking its replacement.
- A contra outside the in-cells marks that cell **outside** (it is evidence
  the effect does not apply there), as an agreement does.
- A move agreement whose observed move equals the pinned move is neutral
  (consistent with both): it neither ends nor marks outside.
- A candidate's contradictions age out with the ring, so one odd record
  delays a real event rather than blocking it for its whole run.

**Regions.**
- Agreements seen before a candidate forms are kept per cell (within
  `window`, at most 64 cells per track); at candidate creation those cells
  start as outside unless they also deviated.
- Zone-wide additionally needs in-cells under at least two level-10 cells
  (~10 km). A ticketed venue is a few contiguous level-14 cells and stays
  local until evidence from elsewhere exists; a real global event spans
  more. Zone "" (unknown) is never zone-wide.

**Applying.**
- `all-strong` level offsets are detected and listed but **not applied**:
  a strong species at base levels kept getting +5. Per-species offsets apply
  (with sampling, each strong species has its own hourly minimum).
- When two confirmed modifiers of the same type and slot cover one record,
  neither applies and the dimension is left absent (level: no derived IVs or
  level, as unexplained; move: slot absent; size: absent). A disputed value
  is withheld, not guessed.
- Pinned moves are a server override after the normal walk: draw both slots
  as usual and replace the pinned one. No draw is skipped (the
  derive/encounter comment saying otherwise is corrected).

**Publishing.** Coverage that shrinks (zone-wide lost, a modifier ended) is
published at once; coverage that grows (a new in-cell) and evidence are
published at most once a second. Unchanged modifiers are not re-cloned, and
the index never copies outside-cell sets (it needs only zone-wide or not).

**Size.** Only rolls the interval fit accepts count as observations; a
refused in-cell roll is a contra.

**Events log.** Records stamped in the future are clamped to the writer's
clock; replay orders records within a bucket by time before merging (a
restart inside a bucket appends earlier deviations after its summary).

## 8. Not included

Loading a shared modifier file; retroactive correction; detection in the
offline script by cell (the events log carries no per-cell agreements; the
script stays per scope).
