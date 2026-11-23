# Derivation Modifiers — Live Learner Design

> **Status:** Agreed 2026-11-17, revised 2026-11-23 (§7–§10); building on branch
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

## 8. Second revision: judge each event at its own scale (2026-11-23)

A second pair of reviews (Fable, Codex) showed the §7 per-cell rules still
fail at scale. Only cells that had deviated counted, so scattered noise over
thousands of cells confirmed (`unexplained` held 41 of 48 simulated hours at
1% noise). Under sampled encounters a modifier stayed applied in its old
cells for 55 h after its event. One shared end counter let a match in cell B
keep a disproven cell A covered, and agreements before a track existed were
lost. Level-14 cells are the right unit for a venue's border but too fine to
count evidence when encounters are sampled. Events are either zone-wide (a
global event in local hours) or ticketed venues (level-14 cells); nothing in
between. This section replaces §7's "Outcomes per modifier" and "Regions"
and §2–§3's in-cell/outside rules; §7's evidence, applying, publishing,
size-evidence and events-log rules stand except where stated. (Amended after
implementation review, 2026-11-23: excluded cells, zone-wide ageing, the
`unconfirmed` end, the venue confirm rule, `end` removed, sightings, and the
index/API shape below.)

**Zone record.** Per (scope, zone, dimension) the learner keeps a ring of the
last 16 located checks (cell ≠ 0): agree, or the effect seen (delta, move,
size match), with its cell. It exists from the first check, before any
modifier, and is pruned when idle for `window`. Only informative checks
enter it: for size, rolls in a disputed band of an active candidate's
boundaries.

**A modifier has two coverages**, judged independently:
- *Zone-wide* (global events): on when its effect is more than half of the
  zone record, the record holds at least `confirm` entries, its matches lie
  under at least two level-10 cells, and the zone is known; off as soon as
  that fails. Under sampling the next hour's first encounters turn it off. It also
  turns off when its record has had no located check for `unseen` (the
  record is emptied, so it needs `confirm` fresh checks to return). Cells
  whose latest entry in the record is anything other than this effect (an
  agreement or another effect) are excluded (at most 16); a later match
  readmits them.
- *Venue cells*: a cell is covered after two matches with no agreement or
  other effect in that cell since; one agreement or other effect there
  removes it at once (the effect applies to every pokemon in scope, so one
  base result disproves the cell). Scattered noise rarely hits one cell
  twice and so rarely covers. A cell with no match for `unseen` is
  forgotten.

**Lifecycle.** Candidate: `start` matches within `window` (as §3); a
candidate not confirmed within `unseen` of its Start ends `unconfirmed` (a
new one forms from fresh deviations). Confirmed: `confirm` located matches
while zone-wide, or `confirm` matches inside covered venue cells (counting
only matches in covered cells: noise covers a cell now and then but does not
accumulate there). Ended: neither coverage on after confirming (reason
`agreement`, or `contradicted` when another effect took its last coverage:
zone-wide lost with another effect as the latest check, or its last covered
venue cell), or `unseen` (coverage lapsed with no check or match, or the
scope not seen where it covers). Sightings no longer extend a modifier's
life: they can only feed the last-seen end, which fires no later than the
check-based ageing. Two confirmed modifiers covering one record with
different values are withheld (§7).

**Applying and publishing.** The index entry carries zone-wide, the excluded
cells (a sorted slice, ≤ 16) and the covered venue cells (the published
copy's set, shared read-only); a record is covered in a covered venue cell,
or in the zone when zone-wide and its cell is not excluded. The lookup stays
allocation-free. Shrink is published at once and growth and evidence at
most once a second (§7); a republished copy whose venue cells did not change
shares the previous copy's set. The API region is `{zone, zone_wide, cells,
excluded}`, cells and excluded sorted and never null.

**All-strong** is not tracked: two or more strong species-scope level
offsets (candidate or confirmed) with the same delta active in one zone are
reported as an all-strong summary in the API, computed at publish (detection
only, `applied: false`, never in the index): confirmed when at least two of
them are, observations summed, the prototype's all-strong id.

**Size** is zone-wide only: its boundaries come from every IV-agreeing
located roll in the zone; a confirmed size modifier needs `start` rolls
before listing a new boundary. Strong size evidence counts only XL and XXL
(the strong walk can produce nothing else).

**Also:** the learner drops a repeated encounter id (kept for `unseen`),
independent of record state; `start ≤ confirm ≤ 16`; `end` and
`zone_wide_cells` are removed (ignored with a warning if set).

## 9. Third revision: apply only what the data supports (2026-11-23)

A third review round (Fable, Codex) confirmed §8 fixed round 2's failures
but found new wrong-value paths, mostly in venue coverage and in size
records that never aged. Decisions with the project owner:

**What applies.** Zone-wide forced moves, level offsets and size cutoffs
(evidence: the 2026-11-15 forced moves and the Rattata size event).
**Venue coverage is detected and listed, never applied**: the Safari +5 was
a venue event, but no venue data has been captured yet. A modifier covered
only by venue cells is listed (`applied: false`); venue cell changes are
evidence, not coverage.

**Zone-wide needs real spread.** Matches in the zone record must come from
at least 8 distinct level-14 cells and span at least 10 km (largest distance
between matching cell centres); different level-10 parents alone are not
spread (two adjacent cells can straddle a level-10 edge). A scan area
smaller than 10 km never applies a zone-wide modifier: withheld, not wrong.

**Record entries age individually.** An entry older than `unseen` does not
count towards a verdict; a record whose entries have all aged turns
zone-wide off. A size record also ages on every IV-agreeing located roll of
the scope, not only informative ones.

**Disproof lasts.** A cell whose latest check of the dimension was not this
modifier's effect stays excluded for the modifier's life until a matching
check in that same cell (not until its entry leaves the 16-entry record). At
most 256 excluded cells; past that, zone-wide turns off. For size, exclusion
is per boundary: a cell is readmitted only when every boundary disproved
there has a fresh match there.

**Form changes are not handled.** A spawn's form does not change after it is
seen; the only display-form change is Minior's core colour, a post-catch
form, which derivation modifiers do not concern. (A review finding premised
on a form change was ruled not applicable.)

**Repeated encounters.** No learner-side id map: the decode site's
first-encounter gate (record state and seen type) is the only dedupe. A
weather downgrade followed by a cache reload can let one spawn vote twice;
accepted (one extra vote).

**Smaller:** a candidate that has not confirmed within 2 × `unseen` of its
start ends `unconfirmed`; candidate and `unconfirmed` transitions log at
Debug; the learner's, the apply path's and the events writer's cell → zone
caches are capped (cleared when they exceed 200,000 cells; recomputed on
demand); every events-log row carries its bucket length so replay orders
rows across a mid-file `bucket_minutes` change.

### 9.1 As implemented (amended after implementation review)

Where the implementation (two review rounds) differs from or adds to the
decisions above:

- **Venue-only never confirms.** A candidate confirms only while zone-wide,
  with `confirm` located matches; there is no venue confirm path. A
  modifier with venue cells alone stays a candidate and ends `unconfirmed`
  (2 × `unseen`) or `unseen`. Venue counts are capped at 1024 cells per
  modifier. A confirmed modifier ends as soon as it is not zone-wide
  (`unseen` with too few fresh entries, `contradicted` when the latest
  fresh entry is another effect, else `agreement`).
- **Stay-on hysteresis.** Turning zone-wide on needs `confirm` fresh
  entries and matches from 8 cells; a confirmed zone-wide modifier stays on
  with `start` fresh entries and matches from `start` cells. Both need the
  majority and a matching pair ≥ 10 km apart. Without this, Poisson-sampled
  encounters at 12/h left fewer than `confirm` fresh entries in about a
  quarter of hours and ended a live event `unseen`.
- **Overflow ends the modifier.** A 257th excluded cell turns zone-wide off
  for the modifier's life: a confirmed one ends (`agreement`); a candidate
  ends `unconfirmed` at once, so a new one (excluded cells seeded from the
  record's fresh entries, ≤ 16) can form when a real event starts after
  noise filled the list.
- **Size exclusion** is a per-cell mask of disproved boundaries: a contrary
  roll sets the boundaries whose band it lies in, a refused roll those
  whose interval it would empty; a match clears only the boundaries it
  crosses.
- **Pin-equal agreements** stay out of the zone record while that pin has
  a modifier (they say nothing about it, and as neutral entries they
  crowded the 16-entry ring below 8 matching cells).
- **Index and API.** The index holds only applied modifiers (confirmed,
  zone-wide, not all-strong) and is nil when nothing is applied or zone-wide;
  the API's `applied` is the same test. Same-type dispute withholding was
  removed: two modifiers of one scope, type and slot cannot both hold one
  zone record's majority, and a record's zone is one, so the case cannot
  arise. The three cell → zone caches are cleared before an insert would
  take them past 200,000 entries.

## 10. Fourth revision: share hysteresis, pause, per-boundary size (2026-11-23)

A fourth review round confirmed §9's fixes and found narrower wrong-value
paths. Decisions with the project owner:

- **Share hysteresis.** Zone-wide turns on only when the effect is at least
  75% of the record's fresh non-neutral entries (with the §9 entry, cell
  and 10 km rules); it stays on while it is more than half. A bare 9-of-16
  majority flapped on and off as an event's pre-end spawns drained (about
  112 wrong +5 spawns per simulated run).
- **Pause on agreement.** Two consecutive non-neutral agreements in the
  modifier's zone record, with no match between, pause application (the
  dimension is withheld, not reverted to base) until the next match. Every
  event seen applies to its whole scope (4,900 of 4,900 forced moves), so a
  live event never pauses; an ended one stops applying within two sampled
  checks instead of about half an hour.
- **Size boundaries are independent.** Each moved cut point is its own
  tracked modifier with its own record, spread, ageing, exclusions and
  lifecycle; the index combines a scope's applied boundaries. A boundary
  seen only at a venue never rides on another boundary's coverage.
- **Size readmission needs a resolving roll.** A cell excluded from a
  boundary keeps its disproving roll; it is readmitted only by a roll there
  that contradicts that disproof (on the far side of the disproving roll),
  not by any match.
- **Neutral is per pin.** A move agreement equal to a pin's move is neutral
  for that pin only; it still counts as an agreement for competing pins'
  verdicts and exclusions.
- **Smaller:** the stay-on cell count is `min(start, 8)`; the excluded-cell
  cap scales with the zone (256 or 1% of the cells the learner has seen in
  that zone, whichever is larger, at most 4096); every events-log row
  carries its bucket start as the writer computed it, and shutdown
  summaries their flush time, so replay never infers alignment.

### 10.1 As implemented (amended after implementation review)

- **Second pin ring.** A move agreement on the move of a pin that has a
  modifier is kept in a second 16-entry ring of the zone record, not the
  main one: in the main ring it pushed the pin's matches out, and a live
  pin whose base walk gives the pinned move half the time ended 6 times in
  6 simulated hours at 1800 checks an hour. Both rings are read for every
  verdict and for exclusion seeding, so the agreement is neutral for its
  own pin (in either ring it does not count as an entry) and an agreement
  for the others. Entries of one second in different rings are ordered
  agreement last (the cell is excluded).
- **Unexplained never pauses.** It already withholds everything; a pause
  would change nothing applied and make `applied` false while it still
  suppresses.
- **The pause counts agreements only.** Another effect seen (another delta
  or move, a refused size roll) and checks without a cell neither count
  towards the pause nor reset it. A paused size boundary withholds the
  whole size where it covers.
- **Cut order.** The index withholds size in a cell only when an applied
  interval passes a neighbouring cut; one ending exactly at the next cut is
  accepted (a single roll can be the upper end of one boundary and the
  lower end of the next).
- **The scaled cap still overflows under heavy noise.** The cells whose
  latest check disproved a live event settle at about the noise share of
  the cells seen, so 1% holds only for noise under about 1% and changes
  nothing below 25,600 cells seen: a 30,000-cell zone with 5% base results
  at 6000 checks an hour still ends on overflow about once an hour (and
  re-confirms within some ten checks).
- **Flapping residual.** With a 45-minute drain at 6000 checks an hour,
  about 19 base spawns a run are still given the event value after its
  share falls under half (121 before), and the modifier confirms 11 to 18
  times as an event starts (36 to 40 before). The 16-entry record's
  sampling noise (about ±12.5 points) is half the 25-point band.

## 11. Not included

Loading a shared modifier file; retroactive correction; detection in the
offline script by cell (the events log carries no per-cell agreements; the
script stays per scope).
