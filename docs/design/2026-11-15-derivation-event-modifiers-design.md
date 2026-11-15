# Derivation Event Modifiers — Prototype Design

> **Status:** Approved for prototype (2026-11-15). Not yet built.

- **Date:** 2026-11-15
- **Scope:** an opt-in Golbat output plus an offline replay script. Nothing
  here changes published values.
- **Intended audience:** Golbat maintainers and operators; written to gather
  community comment alongside a runnable prototype.

## 1. Problem

Golbat derives a sighting's IVs, level, size and moves from its encounter id
by replaying the game's PRNG (`derive/encounter`). During some events the
game alters the result after that roll. Observed so far:

- **Safari event:** strong (mighty) Pokemon carried +5 levels.
- **Move overrides:** a species' charged move forced to a specific move,
  often one outside its normal pool. Live example (2026-11-15): strong
  Tinkaton (959, form 3272), IVs/level/size/quick move all match the strong
  walk, charged move observed 494 vs derived 268; 494 is not in the pool, so
  `checkDerivation` logs `move_out_of_pool` and counts it as "no opinion" —
  the derived 268 keeps being published.
- **Size overrides:** the XXL boundary for Rattata moved.

These effects are time-bound (events), so they must be detected as they start
and as they end. Today operators either encounter everything (Dragonite) or
nothing (trusting derivation). A model that knows when derivation is being
altered could justify a sensible minimum encounter rate in between.

## 2. Goals, non-goals

**Goals (prototype)**
- Capture, cheaply and opt-in, the data needed to see event effects start
  and end: exact deviations plus summarised agreements.
- An offline replay script that streams that data in time order, proposes
  modifiers as they appear and retires them as they end, printing the
  timeline.
- A sampling simulation answering "what minimum encounter rule would have
  caught each effect within N minutes?".
- A simple JSON description of modifiers in effect, shareable but not
  dependent on sharing.

**Non-goals (prototype)**
- Applying modifiers to published derived values (the long-running learner
  PR, §7).
- Any Dragonite change or Golbat→Dragonite feedback loop.
- A community collection/aggregation service.
- Changing `[DERIVE]` / `derivation_log`, which stays as-is for Ditto and
  model debugging.

**Anti-goals**
- A blocking or per-encounter-expensive hook on the decode path.
- Output sized per encounter (operators see 10^5–10^6+ encounters/day).

## 3. Approaches considered

1. **Replay existing `[DERIVE]` logs only.** No Golbat change, but rows are
   written only on disagreement (no agreements → cannot see an effect end or
   simulate sampling), carry no location/timezone/spawn time/account, and
   most operators leave `derivation_log` off.
2. **New opt-in output (deviations + summarised agreements) + replay
   script.** *Chosen.* Fills exactly the gaps that block end detection and
   sampling analysis, at ~10–30 MB/day uncompressed.
3. **Learner inside Golbat from the start.** Closest to the end state but
   every model change is a rebuild/redeploy; too slow while the model is
   unsettled. Becomes the follow-up.

## 4. Golbat: event analysis log

Config (name is the builder's call): e.g. `derivation_events_log = true`,
independent of `derivation_log`. Writes JSON lines to its own file (daily
rotation, gzip of rotated files), not `golbat.log`, so it works regardless of
log configuration and is the single artefact handed to the script.

Hook point: the same place real encounters are scored,
`decoder/pokemon_decode.go` → `checkDerivation`. Data available there:
position, spawn id, first-seen time, reporting account, observed/derived
values and moves. Writing must not block the decode path: a buffered channel
to a single writer goroutine, drop-and-count on overflow (per the decode-path
worker invariant in `CLAUDE.md`).

**Excluded:** encounters classified as Ditto (their values follow the Ditto's
own boost branch and would masquerade as an effect).

### 4.1 `deviation` record

One per encounter that disagrees on any dimension — IVs, level, size, quick
move, charged move — **including a move outside its pool** (the forced-move
signal `checkDerivation` deliberately treats as "no opinion").

Fields (names indicative):
- `kind: "deviation"`, `ts` (UTC, seconds), `utc_offset` (minutes)
- `encounter_id`, `pokemon_id`, `form`, `costume`, `strong`, `boosted`,
  `weather`
- `observed` / `derived` `{attack, defense, stamina, level, size}`, plus the
  opposite boost branch's derived values
- `observed_move_1/2`, `derived_move_1/2`, `move_1_in_pool`, `move_2_in_pool`
- `size_roll`: the raw `Double()` the size class was cut from
- `cell`: level-10 parent (~10 km; coarse by design) of the server's level-15
  map cell stored on the pokemon (`CellId`), lat/lon only as fallback. The
  level can be raised later if effects prove venue-specific.
- `spawn_id`, `first_seen` (UTC) — to test spawn-time vs encounter-time
  effect boundaries
- `account`: salted hash of the username — to test ticket/account-bound
  effects without recording usernames

### 4.2 `summary` record

One per 15-minute UTC bucket per `(pokemon_id, form, strong, boosted,
utc_offset)` with at least one check. **Every check counts, deviations
included**, so the script never reconciles two sources.

- `kind: "summary"`, `bucket` (UTC start), key fields above
- `checked`
- per-dimension agreement counts: `ivs_ok`, `level_ok`, `size_ok`,
  `quick_ok`, `charged_ok` (per dimension so a move-only effect doesn't make
  level look wrong)
- `size_roll_range`: per observed size class, min and max raw roll. For
  boundary estimation this is lossless: a cutoff lies between one class's
  max and the next class's min.

Size estimate: ~1–3k active keys per bucket × 96 buckets/day ≈ 100–300k short
lines, ~10–30 MB/day before gzip. Bucket length configurable.

### 4.3 Timezone

`utc_offset` from `tz.SearchTimezone` at the level-10 cell → location →
offset at `ts`. Cached per cell so it is not a polygon lookup per encounter.

## 5. Script: `tools/eventreplay.py`

Standard library only (like `derivecheck.py`); reads plain or gzipped files,
any number, merged in time order. Streaming: at any point it knows only what
it has read.

**Modifier types fitted:**
- `level_offset` — consistent observed−derived level delta
- `move_pin` — quick or charged slot consistently a specific move (in or out
  of pool)
- `size_cutoffs` — size classes consistent with moved boundaries on the raw
  roll; reported as an interval
- `unexplained` — consistent disagreement matching none of the above (the
  "stop trusting derivation here" signal)

**Scope:** try broad before narrow — all strong → species → species+form;
boosted/unboosted split only if evidence requires it. A broad rule pools
evidence and confirms faster; narrow when some members contradict.

**Lifecycle:** `candidate` (a few consistent deviations, no contradicting
agreements in the window) → `confirmed` → `ended` (agreements on that
dimension return for the scope). Each transition printed with timestamp (UTC
and local), scope, evidence counts. Thresholds are parameters, tuned on live
data.

**Outputs:**
- Timeline of transitions (the "watch it unfold" view).
- `--emit-modifiers` JSON snapshot of currently active modifiers in the
  shareable format (§6), at end of run or at a given time.
- `--simulate-sampling` re-runs the replay with counts binomially thinned
  under rules such as "x% + minimum k per species per hour (strong counted
  separately)", reporting detection delay for each effect's start and end.

## 6. Modifier JSON (shareable)

```json
{
  "version": 1,
  "generated_at": "2026-11-15T09:00:00Z",
  "modifiers": [{
    "id": "2026-11-15-strong-959-charged-494",
    "scope": { "strong": true, "pokemon": [{"id": 959, "form": 3272}] },
    "window": { "start": "2026-11-15T10:00:00Z", "end": null, "time_basis": "utc" },
    "effect": { "type": "move_pin", "slot": "charged", "move": 494 },
    "status": "confirmed",
    "evidence": { "observations": 412, "contradictions": 0 }
  }]
}
```

`time_basis` is `"local"` or `"utc"`; one operator usually cannot tell which
(single timezone), so the prototype emits `utc` and reports local times
alongside.

## 7. Follow-up: learner in Golbat (long-running PR)

Port the validated detector into Golbat; apply confirmed modifiers to derived
values; suppress derived values for a scope while an effect is a candidate or
`unexplained`; expose modifier state (API / `/api/status`), optionally load a
published modifier file as a prior (as the Ditto pool is: remote + cache +
local override with diff logging). Sampling evidence from §5 informs a
recommended fixed Dragonite encounter rule.

## 8. Open questions for the community

- **Ticketed events:** are effects per-account (ticket holders) rather than
  per-time/place? The `account` hash exists to answer this.
- **Spawn vs encounter time:** does a spawn from before an event start get
  the effect if encountered after?
- **Local vs UTC windows:** do effects start at local 10:00 per timezone?
  Needs data from several regions.
- **Size overrides:** class boundaries, or height/weight scalars?
- **Published values during detection:** keep base values, suppress, or flag
  with confidence? (Prototype publishes unchanged.)
- **Late corrections:** should webhooks be re-sent once an effect is
  confirmed?
- **Minimum encounter rule:** what detection delay is acceptable?
