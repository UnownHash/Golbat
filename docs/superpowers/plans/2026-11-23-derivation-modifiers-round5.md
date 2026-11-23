# Derivation Modifiers Round 5 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement design §10: share hysteresis, pause on agreement, independent size boundaries with resolving readmission, per-pin neutrality, scaled exclusion cap, exact replay bucket metadata.

**Architecture:** Learner (`decoder/derive_modifiers.go`, `_record.go`, `_size.go`), index (`_apply.go`), runtime/API, events writer, `tools/eventreplay.py`.

**Tech Stack:** Go 1.27 (`-tags go_json`), `github.com/golang/geo/s2`, Python 3.

**Spec:** `docs/design/2026-11-17-derivation-modifiers-live-design.md` §10 (binding; amends §9).

## Global Constraints

- Off by default; with it off nothing on the decode path changes beyond one bool branch.
- Decode-path invariant (CLAUDE.md): no blocking send, no new allocation on the decode path; nil-index derive = one atomic load, zero allocations.
- `modifierEvent` pointer-free, ≤ 64 bytes.
- A wrongly applied value is worse than an absent one; a paused dimension is withheld (absent), never reverted to base.
- Thresholds: on ≥ 75% of fresh non-neutral entries; stay > 50%; pause after 2 consecutive non-neutral agreements; stay-on cells `min(start, 8)`; excluded cap max(256, 1% of cells seen in the zone) ≤ 4096.
- Keep the invariant tests (`TestModifierRecordOneVerdictOn`, `TestModifierLearnerOneAppliedPerSlot`) passing; extend them where the rules change.
- Build/test: `go test -tags go_json ./...`, `make lint`, gofmt clean (Docker `golang:1.27`, `golangci/golangci-lint:latest`); Python `python3 -m unittest discover -s tools -p 'test_eventreplay.py'`.

---

### Task 1: Learner and index — hysteresis, pause, per-pin neutrality, per-boundary size, caps

**Files:** `decoder/derive_modifiers.go`, `decoder/derive_modifiers_record.go`, `decoder/derive_modifiers_size.go`, `decoder/derive_modifiers_apply.go`, `decoder/derive_modifiers_runtime.go`, `decoder/api_derivation_modifiers.go`, tests, `api.md`, `config.toml.example`.

- Share hysteresis per §10 (on ≥ 75%, stay > 50%).
- Pause: per applied modifier a consecutive-agreement counter on its record (non-neutral agreements; reset on a match); at 2, the modifier is paused: published with `Paused` (index withholds its dimension: level → Suppress, move → withhold slot, size → withhold size); next match unpauses. Pause/unpause is a shrink/growth publish. API shows `paused`.
- Per-pin neutrality: record entries keep the observed move for agreements; when judging pin P, an agreement whose observed move equals P's move is neutral for P only; competing pins see it as an agreement (verdict and exclusion seeding). Pin-equal agreements must stay in the record.
- Size: one track per (scope, zone, boundary) — each moved cut is its own modifier (type size_cutoffs, effect = boundary key), own record (informative rolls for that boundary), spread, ageing, exclusions. The index merges a scope's applied boundaries into one size adjustment; a boundary not applied in a cell (excluded/paused/not zone-wide) uses its base cut there only if no other boundary's interval depends on it — if any applied boundary's interval would overlap or invert against the base cut, withhold size in that cell. API: one entry per boundary (id includes the boundary).
- Size readmission: excluded (cell, boundary) keeps the disproving roll and its side; readmit only by a later roll in that cell on the far side of the disproving roll classified as the modifier predicts.
- Stay-on cell count `min(start, 8)`; excluded cap max(256, 1% of cells seen in the zone) ≤ 4096 (count distinct cells seen per zone with the learner's existing cell→zone map).

Tests (each from the review; fail on current code): Fable flapping (6000/h, 45 min drain: wrong post-end spawns ≤ 10; start flaps ≤ 12); Fable sampled end (12/h Poisson: post-end applied first sightings drop to ≤ 2 checks' worth — pause within two base checks); live forced-move event at 100% never pauses; Codex size venue boundary (2/3 deviations only in one venue cell never applied outside, even with 1/2 zone-wide); size boundary ages independently (2/3 stops 1 h after its last roll while 1/2 continues); Codex readmission (0.032→M excludes; 0.030→XS does not readmit; 0.034→XS... use a roll on the far side that the published interval classifies as predicted → readmits); Codex neutral-per-pin (268 agreements in D still exclude D from 494); stay cells with start 10; scaled cap on a 30,000-cell zone with 5% noise (no overflow end in 12 h at 6000/h).

Commit: `fix(decoder): share hysteresis, pause on agreement, per-boundary size`.

### Task 2: Replay bucket metadata exact

**Files:** `decoder/derive_events_writer.go`, `tools/eventreplay.py`, golden file, tests.

- Deviation rows carry `bucket_start` (the writer's computed bucket start, unix seconds); summary rows keep their bucket start and carry `flushed_at` when written at shutdown (sort on the bucket end normally, on `flushed_at` for a shutdown summary). Reader uses these when present, today's behaviour otherwise. Tests: Codex 5→7-minute restart (rows 11:55:02, 11:54:58 in one writer bucket; `--at 11:54:59` sees the second row at its own time); Fable 60→5 shrink mid-bucket (pre-restart agreements replay before later short-bucket deviations).

Commit: `fix(tools): replay uses the writer's bucket starts`.
