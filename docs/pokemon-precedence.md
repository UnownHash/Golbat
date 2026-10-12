# Pokémon data precedence

Three quantities on a Pokémon record can come from more than one source.
This document states, for each, the complete list of sources, their strict
precedence, and the guard that enforces it.

It exists because the defect this design invites is a **precedence failure** —
not a wrong calculation, but the wrong source winning.
Those failures are unusually expensive here for one reason:

> **Derived values are indistinguishable from observed, by design.** That is what
> lets every existing consumer benefit with no changes. It also means a
> precedence error can never be identified after the fact. Wrong IVs look exactly
> like right ones. There is no provenance column and no way back.

Each section names the tests that check its rules; what is not checked is
listed at the end.

Guards are cited by **symbol**, never by line number. A line number in a document
is a claim that goes stale silently, which is the failure this document exists to
reduce rather than add to.

---

## A distinction that must not be collapsed

For each source, "no answer" splits into two cases that must not be conflated:

| | meaning | correct response |
|---|---|---|
| **absent** | the source has no data at all | fall through to the next source |
| **cannot conclude** | the source has data but it does not settle the question | **not** a negative — do not treat as refutation |

`detectDitto` is the source where this bites. Given a single boost state it
cannot determine Ditto-ness, and its "missing past scans" branch means *I cannot
verify this*, not *this is not a Ditto*. Treating that as refutation reverts a
correct roll-mark at the first encounter. Separately,
`is_ditto: false` read as a negative is the same error at the consumer end — it
conflates "not a Ditto" with "never resolved".

---

## 1. IVs, level, size

**Precedence, strongest first:**

| # | source | wins when |
|---|---|---|
| 1 | encounter proto | always — `addEncounterPokemon` writes what the encounter reports |
| 2 | stored cross-boost scan | a weather flip finds a matching scan (`repopulateIv`) |
| 3 | derivation from `encounter_id` | nothing above has written IVs |

**A Ditto is the exception to what source 1 contributes, not to its rank.** The
record carries the Ditto you would CATCH, which in `0P` is not what the
encounter reports: there the Ditto under the disguise is Normal-typed, so the
encounter reports the boosted set while a caught Ditto is boosted only in `PP`.
Publishing what was reported would put IVs and a level five too high into
storage, the APIs and the webhooks.

`addEncounterPokemon` takes the caught set from `detectDitto`'s `caughtIv`
whenever it exists: the Ditto you would catch, recovered from real
observations (for anything that is not a Ditto, this encounter's own scan).
It does not always exist: in `0P` when nothing has observed the caught set —
a roll-marked record with no history, for one — `caughtIv` is nil. There,
with derivation on, the caught set comes from the encounter id
(`encounter.For` on `dittoCaughtBoosted`'s branch); with derivation off, the
level is adjusted down by five and the IVs are cleared.

`detectDitto` owns the identity, the scan history, and the cross-boost
reasoning that can refute a Ditto the derivation believed in, and
`checkDerivation` compares every encounter against the derivation. With
derivation off `detectDitto` is the only source.

**Guard** — `deriveIvIfAbsent` (`decoder/pokemon_decode.go`):

```go
if !config.Config.DeriveIv {
    return
}
strong := pokemon.IsStrong.ValueOrZero()
if !deriveStrongAllowed(strong) { // strong needs derive_strong as well
    return
}
if pokemon.AtkIv.Valid {
    if !pokemon.Size.Valid || !pokemon.Move1.Valid || !pokemon.Move2.Valid {
        pokemon.deriveSizeAndMovesIfAbsent()
    }
    return
}
```

`AtkIv.Valid` is what makes the encounter win: stored IVs are kept, and only
an absent size or move is filled (a display change clears those but not the
IVs). `IsStrong` picks the walk, not the precedence: a strong (mighty) Pokémon
is derived with `encounter.ForStrong` — IVs 13–15 that do not move with
weather, level 36–40 or 41–45 boosted, size XL or XXL only — and only while
`derive_strong` is on (default). Its IVs being weather-independent is what
`repopulateIv`'s `isBoostedMatches || isStrong` and `detectDitto` already
assumed. Its **moves are not derived**: an event can fix a strong species'
move (often outside its pool), and a fixed slot takes no draw, so the drawn
indices cannot be trusted without a list of the fixed moves. `checkDerivation`
logs every strong encounter, moves included, without scoring the moves.

A known Ditto is derived, on its own boost branch rather than its disguise's
(`statsBoosted`): a caught Ditto is boosted only in `PP`.

**Tests:** `TestDeriveIvLeavesExistingIvsAlone`,
`TestDeriveIvIfAbsentSkipsStrongPokemon`,
`TestDeriveIvIfAbsentDerivesAKnownDittoOnItsOwnBranch`,
`TestRepopulateIvDerivesInsteadOfClearing`,
`TestRepopulateIvClearsStrongPokemonInsteadOfDeriving`,
`TestEncounterPublishesTheCaughtDittoNotWhatItReported`,
`TestEncounterKeepsTheCaughtSetForARollMarkedDittoIn0P`.

---

## 2. Despawn time

**Precedence, strongest first** — `setExpireTimestampFromSpawnpoint`
(`decoder/pokemon_decode.go`):

| # | source | wins when |
|---|---|---|
| 0 | existing verified expiry | the sighting's timestamp is untrustworthy |
| 1 | stored `despawn_sec` (cache) | cache hit, synced, value known |
| 2 | stored `despawn_sec` (database) | cache miss or mid-load, row has a value |
| 3 | derivation from `spawn_id` | no stored value anywhere |

Rule 0 is an early return: an untrustworthy timestamp — an encounter's or a
tappable's receipt time — may not overwrite a verified expiry. Within rule 3
it also may not replace an expiry already derived for the spawnpoint unless
that expiry passed more than `despawnClockSkewTolerance` ago; it does replace
a guess, which is not on the spawnpoint's phase.

`spawnId == 0` returns without writing — no source applies.

A failed spawnpoint lookup is not "no stored value": the row may hold the
despawn second a verified expiry came from, so an expiry still in the future
is kept rather than replaced by a derived one (`spawnpointLookupFailed`).

A derived expiry never moves backward: an earlier one can only come from a
delayed sighting, and would undo a 60-minute Pokémon's correction.

**Derivation is gated by `derive_despawn`** (default on), inside
`applyDerivedDespawn`; off, the guess takes its place. It is exact for every
spawnpoint whose Pokémon live half an hour, and half an hour early for the
rest. A derived expiry is written unverified, so a Pokémon still listed after
it is re-admitted and derived again from the later sighting, landing on the
next half hour. Nothing derived is written to the spawnpoint.

**Tests:** `TestSetExpireTimestampFromSpawnpointReadsTheRecord`,
`TestApplyDerivedDespawnUsesNextOccurrence`,
`TestUntrustedTimeKeepsADerivedExpiry`,
`TestFailedSpawnpointLookupKeepsAFutureExpiry`,
`TestUpdatePokemonBatchRederivesPastAnUnverifiedExpiry`,
`TestUpdatePokemonBatchADelayedGMODoesNotUndoACorrection`.

---

## 3. Ditto identity

**Precedence, strongest first:**

| # | source | wins when |
|---|---|---|
| 1 | `detectDitto` reaching a conclusion | it has evidence — cross-boost history, or a single scan whose level or IVs are impossible for its reported boost state — authoritative in both directions |
| 2 | Ditto derivation (`ditto_derivation`) | species and form in `ditto_pool`, roll below threshold, no scan history |
| 3 | nothing | record stays as displayed |

The *roll* is the encounter id's third generator draw, read as `nextFloat()`;
the game makes a spawn a Ditto candidate when it is strictly below `0.02f`
(`dittoCandidateThreshold`). It is unrelated to weather rerolls.

**`detectDitto` without evidence does not beat the roll.** Its three outcomes for
a record already flagged `IsDitto`:

| state | outcome |
|---|---|
| a boosted or unboosted scan exists | normal Ditto logic — may confirm or clear |
| no history, **roll-marked** | **keep the mark**, unless this scan's level or IVs are impossible for a Ditto in the cell's weather, or its stats match the disguise's boost branch where that differs from the Ditto's (`rollMarkRefutedByStats`) — then reset |
| no history, not roll-marked | reset, return error |

**Guards on the roll** — `maybeMarkDittoFromRoll`:

- `!config.Config.DittoDerivation` — feature off
- `pokemon.IsDitto` — already concluded, by either source; never overridden
- `oldValues.SeenType` an encounter type — the record has been encountered,
  so `detectDitto` has already concluded about it. This holds after a reload
  that lost the scan history, which is persisted only with
  `pokemon_internal_to_db`; the snapshot is taken before an encounter in
  progress writes its own `seen_type`, so a first encounter still marks.
- `pokemon.IsStrong` — whether a strong Pokémon can be a Ditto is not known
  either way, and the Ditto rule reads the ordinary walk's advance 3; there is
  no evidence it describes a strong one. Marking would also be
  **terminal** — `detectDitto` returns on `scan.Strong` before any
  Ditto logic runs, so no later encounter takes the identity back
- not in `ditto_pool` — the game only substitutes for eligible species, so the
  roll fires on every encounter but means nothing outside the pool
- `len(pokemon.scanHistory) > 0` — `detectDitto` has real data; a prediction has
  no business competing with observation. Hydrates `GolbatInternal` first, since
  a freshly loaded record's history is not yet materialised.

**The roll cannot discover new pool members.** Membership is a gate, so a
non-pool species is never marked however the roll falls. Only the boost-branch
channel (`dittoBranchFor`) can identify an unknown disguise.

**The boost-branch comparison.** For an encounter id,
`encounter.For` derives two branches — the one `weather_boosted_condition`
implies, and the other. `dittoBranchFor` reports which the observed
IVs/level/size match:

| verdict | meaning |
|---|---|
| `expected` | matches the branch the proto implies — the ordinary case |
| `opposite` | matches the other branch — a Ditto indicator |
| `neither` | matches no branch — a genuine derivation failure |
| `unknown` | not comparable — a strong Pokemon, outside the Ditto comparison |

`opposite` arises because the IVs an encounter REPORTS follow the Ditto's own
boost state, not its disguise's: in `PARTLY_CLOUDY` the Ditto is boosted while
a disguise that Normal-typing does not cover is not, so the proto reports
unboosted and the reported IVs are boosted. That is the `0P` state. Note this
is about what is reported — the Ditto you *catch* there is unboosted, because a
caught Ditto is boosted only in `PP`.

Because the comparison never consults `ditto_pool`, it is the only channel that
can find a disguise the pool does not know about.

**That channel logs; it never marks — on two deliberately independent paths.**
An `opposite` row on a species already in the pool goes to the `[DERIVE]` log
when the roll did not fire for it, as a Ditto the roll missed. An `opposite` row
on a species *not* in the pool — the discovery case — emits a standalone `[DITTO]`
warning (`logUnknownDittoDisguise`) that does not depend on `[DERIVE]` being
enabled. `[DERIVE]` is validation apparatus with a finite life and is off by
default (`derivation_log`); an undocumented disguise is permanently worth
knowing about, so the warning must survive the log being switched off.

Golbat never adds to the pool itself. The pool is maintained by the publishers
of `https://uxie.unownhash.com/`; an operator who needs a different pool sets
`ditto_pool` in config, which replaces it. A wrongly added species is marked a
Ditto on roughly 2% of its encounters indefinitely, and a marking is
indistinguishable from a real one downstream, so amending it from live
evidence is left until there is enough evidence to show an automatic rule is
safe.

**`dittoRollMarked` records provenance** and must move with `IsDitto` in both
directions: set together when the roll marks, cleared at `SetIsDitto(false)`'s
single choke point.

**A caught Ditto is boosted only in the PP state**: partly cloudy weather AND a
disguise that partly cloudy also boosts. That is exactly what the game reports
as `weather_boosted_condition`, so the `weather` column answers it and nothing
else is needed — `repopulateIv`'s Ditto arm compares the stored condition
against the incoming one.

The arm keys on the reported condition, not the cell's weather. In `0P` the
cell is partly cloudy, so the encounter reports boosted stats for the
Normal-typed Ditto underneath, while the Ditto you catch is unboosted; only
the caught set belongs on the record.

**Tests:** `TestRepopulateIvDerivesForADittoOnItsOwnBoostRule` asserts into
PP, out of PP, and neither, each with a different outcome; keying the arm on
the cell instead fails the third.

The roll's guards: `TestMaybeMarkDittoFromRollSuppressedWhenDisabled`,
`TestMaybeMarkDittoFromRollExcludesStrongPokemon`,
`TestMaybeMarkDittoFromRollDoesNotFireForNonPoolSpeciesEvenIfRollFires`,
`TestMaybeMarkDittoFromRollDoesNotFireForPoolSpeciesAboveThreshold`,
`TestMaybeMarkDittoFromRollScanHistoryGate`,
`TestMaybeMarkDittoFromRollScanHistoryGateHydratesFromGolbatInternal` and
`TestSightingDoesNotRemarkAnEncounteredRecordReloadedWithoutHistory`. The
three `detectDitto` outcomes: `TestDetectDittoRollMarkedRecordWithHistoryUsesNormalDittoLogic`,
`TestDetectDittoKeepsRollMarkedRecordOnMissingHistory` (with
`TestDetectDittoDropsARollMarkTheFirstEncounterContradicts` and
`TestFirstEncounterRefutesARollMarkWhoseStatsFollowTheDisguise` for the reset)
and `TestDetectDittoStillReturnsErrorForGenuineMissingHistory`.

---

## What is not checked here

- **Whether the pool in force still matches the game's.** The pool rotates on
  Niantic's schedule, and a stale entry is a pure false-positive source. The
  published pool is refreshed every 6 hours, which bounds how stale it can be
  but not whether its source is right. The signals are the two `[DITTO]`
  warnings — an unknown disguise from the derivation side, and a disguise
  `detectDitto` confirmed that the pool does not list — and the rate of
  roll-marks later contradicted by `detectDitto` (a species the pool should
  not contain).
- **Whether a spawnpoint's stored `despawn_sec` is still correct.** It is only
  ever written from observation, never from derivation, so a wrong stored value
  outranks a correct derivation until something contradicts it.
