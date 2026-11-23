# Derivation Modifiers Scale Rework Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rework the live derivation-modifier learner to judge zone-wide events on a zone-level record and venues cell by cell, and fix the remaining review findings, so it never publishes a value the game did not apply.

**Architecture:** Same components (`decoder/derive_modifiers.go`, `_size.go`, `_apply.go`, `_runtime.go`, `api_derivation_modifiers.go`, decode hooks, events writer, `tools/eventreplay.py`). The learner's per-track in-cell/outside/ring machinery is replaced by a per-(scope, zone, dimension) zone record plus per-modifier venue cells.

**Tech Stack:** Go 1.27 (`-tags go_json`), `github.com/golang/geo/s2`, Python 3.

**Spec:** `docs/design/2026-11-17-derivation-modifiers-live-design.md` §8 (binding; supersedes §7's outcome/region rules and §2–§3's in-cell/outside rules; §7's evidence, applying, publishing, size-evidence and events-log rules stand except where §8 says).

## Global Constraints

- Off by default; with it off nothing on the decode path changes beyond one bool branch.
- Decode-path invariant (CLAUDE.md): no blocking send, no new allocation or map on the decode path; nil-index derive = one atomic load, zero allocations (`TestDerivationAdjustNoAllocs`).
- `modifierEvent` pointer-free, ≤ 64 bytes (adding `EncounterID uint64` fits exactly).
- Pinned moves: server override after the normal walk; never skip or shift a draw.
- A wrongly applied value is worse than an absent one: when in doubt, withhold.
- Defaults: `start` 3, `confirm` 10, `end` 8 (now unused by the rules? see Task 1), `window_seconds` 1800, `unseen_seconds` 3600; zone record ring 16; venue cell covered after 2 matches; zone-wide spread ≥ 2 level-10 cells; `zone_wide_cells` removed (config key ignored if present, with a warning).
- Learner per-event work O(1) amortised; every learner map bounded (pruned by `window`/`unseen`).
- Build/test: `go test -tags go_json ./...`, `make lint` (Docker `golang:1.27`, `golangci/golangci-lint:latest`); Python `python3 -m unittest discover -s tools -p 'test_eventreplay.py'`.

---

### Task 1: Learner rework — zone record, venue cells, lifecycle, size, dedupe

**Files:** Rewrite the rule parts of `decoder/derive_modifiers.go` and `decoder/derive_modifiers_size.go`; modify `decoder/derive_modifiers_runtime.go` (params validation, log line), `config/config.go`, `config/reader.go`, `config.toml.example`; rewrite tests in `decoder/derive_modifiers_test.go`, `decoder/derive_modifiers_size_test.go` (keep tests whose behaviour §8 keeps; delete tests of removed rules, listing each in the report).

Rules: spec §8 exactly. Notes:
- Zone record: `[16]` entries {cell, outcome} + head/filled per (scope, zone, dim); outcome = agree or an effect id (delta / move / for size: match vs contrary in a disputed band). Fed by every located check of the scope's dimension (IV-agreeing for level/move/size; IV-disagree feeds `unexplained`'s record as its effect and IV-agree as agree).
- Candidate: as now (`start` matching deviations within `window`, gating by same-cell agreement as now). Keep the pending list but it no longer needs agreed-cell tracking (the zone record replaces it).
- Zone-wide on/off evaluated after each record change for active modifiers of that record; excluded cells = cells whose latest entry is agree (≤ 16).
- Venue cells: per active modifier map cell → match count (uint8); covered at ≥ 2; an agreement or another effect in the cell deletes the entry (shrink if it was covered). Bounded: entries only for cells with matches; prune cells with no match for `unseen`.
- Confirm: `confirm` located matches and (zone-wide on or ≥ 1 covered cell). End: after confirm, zone-wide off and no covered cells → end (`contradicted` if the last removal was by another effect, else `agreement`); unseen as now. A candidate whose coverage never comes on simply ages out by unseen/window.
- `End` param: no longer used by the rules — remove it from params and config (ignored with a warning if set) unless you find a rule that needs it; say which in the report.
- All-strong: no tracks. (Task 2 computes the report.)
- Size: zone-wide only; evidence from every IV-agreeing located roll in the zone; a confirmed size modifier lists a new boundary only after `start` rolls support it; strong evidence counts only classes 4–5 (anything else is ignored).
- Dedupe: `modifierEvent.EncounterID` (0 for sightings); the learner keeps id → expiry (`unseen`) and drops a repeated check id. Set it in `newModifierEvent`.
- Validation: `start`, `confirm` ≤ 16, `start` ≥ 1, `confirm` ≥ `start`.
- Keep `changed`/`takeDirty` reporting shrink vs growth (Task 2 consumes it): shrink = zone-wide turned off, a covered cell removed, size boundaries changed, an `unexplained` gaining coverage, growth on a scope with a disputing peer (as now); growth = zone-wide on, a cell newly covered; evidence otherwise. Version counter as now.

Tests (from the reviews; each must fail on the current code or be a kept behaviour): Fable noise — 1% IV-mismatch noise over 3000 cells at 1800/h for 48 h never confirms; Fable sampled end — a zone-wide +5 event sampled at 12/h over 3000 cells ends within 1 h of the event ending; Codex pre-deviation agreement in D then 10 matches in A/B/C (same level-10 cell) → D not covered; per-cell end — pin covered in A and B, base agreement in A removes A at once while B stays; disjoint venues with different pins → each covers only its own cells; superseded pin (494 then 300 in the same cells) → 494 ends `contradicted`, 300 confirms; zone-wide needs two level-10 cells; zone "" never zone-wide; strong size class 3 evidence ignored (Codex .900–.909 case does not confirm); confirmed size needs `start` rolls before a new boundary; repeated encounter id counted once; config rejects start 17.

Commit: `refactor(decoder): judge zone-wide events by a zone record, venues by cell`.

### Task 2: Index, apply, publish and API for the new coverages

**Files:** `decoder/derive_modifiers_apply.go`, `decoder/derive_modifiers_runtime.go`, `decoder/api_derivation_modifiers.go`, `api.md`; tests.

- Index entry: type, effect, zone, zone-wide bool, excluded cells (≤ 16, small slice), covered cells (map or sorted slice), boundaries. Lookup: zone-wide and cell not excluded, or cell covered. Conflict withholding and all-strong exclusion as now. Allocation-free.
- Publishing as now (shrink at once; growth/evidence ≤ 1 s; version-reused clones); never clone venue cells for a modifier whose cells didn't change.
- API: `region` = {zone, zone_wide bool, cells: covered cells sorted, excluded: cells} (document; keep `applied`); all-strong summary entries computed at publish: same delta, ≥ 2 species-scope level offsets active in one zone → listed with `applied:false`, scope all-strong, id as the prototype's all-strong id.
- `api.md` and `config.toml.example` describe §8.

Tests: lookup for zone-wide with an excluded cell; covered vs uncovered cell; conflict still withheld; no-alloc test incl. zone-wide with exclusions; API region shape; all-strong summary appears with two species at +5 and disappears when one ends.

Commit: `fix(decoder): index and API for zone-wide and venue coverage`.

### Task 3: Remaining fixes outside the learner

**Files:** `decoder/pokemon_decode.go` (`repopulateIv` ~2507/2563; encounter check build ~2260), `decoder/derive_events_writer.go` (~301), `tools/eventreplay.py` (~172), `api.md` lifecycle wording (size: no majority rule) if still present; tests.

- `repopulateIv`: when the adjustment suppresses (unexplained or level conflict), never fall through to the legacy weather estimate; leave level absent unless a usable encounter scan exists for the new state (Codex case: +5 and +10 confirmed, old unboosted level-20 scan, no boosted scan → level absent, not 25).
- Build the check record only when something uses it: `derivationEventsWriter != nil || (derivationModifierLearnerOn && firstEncounter)`.
- Events log: each deviation row's replay ordering time lies inside its assigned bucket (keep the original time as a separate field if the format needs it; update the golden file and the Python reader); replay test for the Codex sequence (899, summary 900, 901, delayed 898) emitting in non-decreasing order.

Commit: `fix: repopulate never estimates withheld levels; replay order across buckets`.
