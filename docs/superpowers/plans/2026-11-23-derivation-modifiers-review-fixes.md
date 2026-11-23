# Derivation Modifiers Review Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the rules and defects two independent reviews found in the live derivation-modifier learner, so it never publishes a value the game did not apply.

**Architecture:** Same components as `2026-11-17-derivation-modifiers-live.md`: decode-side submit hooks, the single-goroutine learner (`decoder/derive_modifiers.go`, `_size.go`), the published index and derive-path lookup (`_apply.go`), the runtime/publisher (`_runtime.go`), the API (`api_derivation_modifiers.go`), the events writer and `tools/eventreplay.py`. Rules change; structure does not.

**Tech Stack:** Go 1.27 (`-tags go_json`), `github.com/golang/geo/s2`, Python 3 (tools).

**Spec:** `docs/design/2026-11-17-derivation-modifiers-live-design.md` §7 (the revision; binding where it contradicts §2–§6).

## Global Constraints

- `[derivation_modifiers]` stays off by default; with it off nothing on the decode path changes (one bool branch at most).
- Decode-path invariant (CLAUDE.md): no blocking send; no new allocation or map on the decode path; the nil-index derive path stays one atomic load, zero allocations (keep `TestDerivationAdjustNoAllocs` passing).
- `modifierEvent` stays pointer-free and ≤ 64 bytes.
- Pinned moves are a server override after the normal walk: never skip or shift a draw for a pin.
- A wrongly applied value is worse than an absent one: when in doubt, withhold.
- Defaults after this plan: `start` 3, `confirm` 10, `end` **8**, `window_seconds` 1800, `unseen_seconds` 3600, `zone_wide_cells` 3; outcome ring 16; pre-candidate agreeing cells ≤ 64 per track; zone-wide spread: in-cells under ≥ 2 distinct level-10 cells.
- Build/test: `go test -tags go_json ./...`, `make lint` (Docker `golang:1.27`, `golangci/golangci-lint:latest`); Python: `python3 -m unittest discover -s tools -p 'test_eventreplay.py'`.

---

### Task 1: Inputs — one count per spawn, server clock, placed cell, comment, writer clamp

**Files:** Modify `decoder/pokemon_decode.go` (encounter check submit ~2228–2236; wild update ~327), `decoder/pokemon_state.go` (sighting submit ~153–160), `decoder/derive_modifiers_runtime.go` (submit helpers), `decoder/derive_events_writer.go` (bucket time), `derive/encounter/encounter.go` (ForStrong comment ~117–121). Tests in the matching `_test.go` files.

- **First encounter only:** the learner gets a check only when the record held no encounter-reported scan before this encounter (decide from record state captured at the top of `addEncounterPokemon`, e.g. `scanHistory` empty / no encounter values; no new map). The events log keeps receiving every check (unchanged).
- **Server clock:** `submitDerivationModifierEvent` and the sighting submit set `modifierEvent.Unix = time.Now().Unix()` at submit, ignoring the record's or map's timestamp.
- **Placed cell:** in the wild update, derivation and `repopulateIv` must use the cell the record is being placed in (assign the incoming `CellId` before any derive call on that path, or pass it through). Trace `updateFromWild` and every derive call it can reach.
- **Comment:** ForStrong's comment must say a pinned move is a server override applied after the normal walk; the walk is unchanged and no draw is skipped (evidence: 2026-11-15 forced charged moves with every other dimension matching).
- **Writer clamp:** a check or sighting timestamped after the writer's clock is bucketed at the writer's clock.

Tests: a second encounter of the same record sends nothing to the learner but still reaches the events writer; a sighting saved with a timestamp 3 h in the future produces a learner event with `Unix` within a second of now; a wild update moving a record from cell A (local modifier) to cell B derives B's values (no modifier); a future-stamped check lands in the current bucket.

Commit: `fix(decoder): learner counts each spawn once on the server clock`.

### Task 2: Learner rules — outcome ring, contradiction end, regions, end 8, size evidence

**Files:** Modify `decoder/derive_modifiers.go`, `decoder/derive_modifiers_size.go`, `config/reader.go` (default `end` 8), `config.toml.example`; tests in `decoder/derive_modifiers_test.go`, `decoder/derive_modifiers_size_test.go`.

Rules (spec §7):
- Per active modifier, a ring of the last 16 in-cell outcomes on its dimension: match / agree / contra (fixed-size array + index; no allocation per event).
- Confirm: `Observations >= Confirm`, no contra in the ring, matches ≥ half the ring's filled entries.
- End by agreement: `End` consecutive in-cell agreements; only an in-cell match resets.
- End by contradiction (reason `"contradicted"`): contras in ring ≥ `Start` and contras ≥ matches in ring. Applies to candidates and confirmed.
- A contra in a non-in-cell marks the cell outside (`markOutside`), never counts as an in-cell contra. Contradiction no longer increments only in-cell modifiers: classify by cell.
- Move-pin agreement with observed move == pinned move: neutral.
- Pre-candidate agreeing cells: a gated track without a modifier keeps the cells where its dimension agreed within `Window` (≤ 64; past the cap set an overflow flag treated as "has outside evidence"); at candidate creation those cells (minus cells that deviated after agreeing) seed `Outside`.
- `ZoneWide`: also needs in-cells under ≥ 2 distinct level-10 parents; never true for zone "".
- Size: only accepted rolls count as observations; a refused in-cell roll is a contra in the ring (size is exempt from the majority-of-ring confirm rule and from agreement end; its contrary-roll end stays).
- `Contradictions` stays as the lifetime count for the API.
- Default `End` 8 in `defaultModifierParams` and config; update `config.toml.example` text.

Tests (each fails on current code): Codex superseded pin (494 confirmed, then 300 deviations in its cells → 494 ends `contradicted`, 300 confirms); disjoint venues (494 in A/B/C, 777 in D/E/F) → both local, neither covers the other's cells; pre-candidate agreement in D then deviations A/B/C → D outside, not zone-wide; zone-wide needs two level-10 parents (three adjacent level-14 cells → local; cells in two level-10 parents with no outside → zone-wide); zone "" never zone-wide; noise (1 IV mismatch per 5 agreements across cells) never confirms `unexplained`; candidate with one early contra still confirms once the contra leaves the ring; 8 consecutive in-cell agreements end; an out-of-cell deviation does not reset the end count; pin-equal agreement neutral; size rejected roll is a contra and not an observation (Codex XXS 0.010 / XS 0.003 case does not confirm).

Commit: `fix(decoder): learner confirms and ends on in-cell outcome ratios`.

### Task 3: Index, apply and publish

**Files:** Modify `decoder/derive_modifiers_apply.go`, `decoder/derive_modifiers_runtime.go`, `decoder/api_derivation_modifiers.go`; tests in the matching files.

- `buildModifierIndex` skips all-strong modifiers for application (they stay in the API snapshot and the sighting scope set). API: add `"applied": bool` per modifier (false for all-strong and candidates).
- Conflict: when two confirmed entries of the same type and slot (level: any two deltas; move: same slot; size: two size modifiers) both cover a record, neither applies and the dimension is withheld: level conflict → `Suppress`; move conflict → that slot not pinned and not derived (leave absent); size conflict → size absent. Keep the lookup allocation-free.
- Publish: a coverage shrink (a confirmed modifier losing zone-wide, or ended) publishes at once (transitions already do); coverage growth and evidence publish at most once a second. The learner reports shrink vs growth separately (`takeDirty`).
- Cloning: keep a per-modifier version; the runtime reuses the previous clone for unchanged modifiers. The index stores a zone-wide flag and in-cells only, never a copy of `Outside`.

Tests: all-strong +5 confirmed → strong record unchanged, API lists it with `applied:false`; two confirmed charged pins covering one cell → Move2 absent (not either pin); two level deltas → no derived IVs/level; zone-wide → local publishes immediately, new in-cell on a zone-wide modifier does not rebuild immediately; publishing N new in-cells on 30 zone-wide modifiers clones each modifier at most once per publish (benchmark or counter assertion), no Outside copy.

Commit: `fix(decoder): withhold disputed modifier values; cheaper publishing`.

### Task 4: Replay ordering and docs

**Files:** Modify `tools/eventreplay.py`, `tools/test_eventreplay.py`, `api.md`, `docs/design/README.md` (row status unchanged), `CLAUDE.md` only if a layout line changes.

- Replay: records within a bucket are ordered by time before merging (bounded buffer per file: a summary row's bucket); add a test with a shutdown summary followed by earlier-timestamped deviations in the same bucket and `--at` inside it.
- `api.md`: `applied`, end reason `contradicted`, all-strong is detection-only, conflict withholding, `end` default 8.

Commit: `fix(tools): replay orders records within a bucket; docs for review fixes`.
