# Derivation Modifiers Apply-Scope Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Apply only zone-wide forced moves, level offsets and size cutoffs, with real spread, ageing entries and lasting disproof; venue coverage becomes detection-only; fix the remaining round-3 findings.

**Architecture:** Same components. Learner (`decoder/derive_modifiers.go`, `_record.go`, `_size.go`), index/lookup (`_apply.go`), runtime (`_runtime.go`), API, decode hooks (`pokemon_decode.go`), events writer, `tools/eventreplay.py`.

**Tech Stack:** Go 1.27 (`-tags go_json`), `github.com/golang/geo/s2`, Python 3.

**Spec:** `docs/design/2026-11-17-derivation-modifiers-live-design.md` §9 (binding; amends §8).

## Global Constraints

- Off by default; with it off nothing on the decode path changes beyond one bool branch.
- Decode-path invariant (CLAUDE.md): no blocking send, no new allocation or map on the decode path; nil-index derive = one atomic load, zero allocations.
- `modifierEvent` pointer-free, ≤ 64 bytes.
- Pinned moves: server override after the normal walk; never skip or shift a draw.
- A wrongly applied value is worse than an absent one: when in doubt, withhold. Forms are independent scopes.
- Constants: zone-wide spread = matches from ≥ 8 distinct level-14 cells spanning ≥ 10 km; excluded cells cap 256 (overflow → zone-wide off); candidate deadline 2 × `unseen`; cell→zone caches cleared above 200,000 entries.
- Learner per-event work O(1) amortised; every learner map bounded.
- Build/test: `go test -tags go_json ./...`, `make lint` (Docker `golang:1.27`, `golangci/golangci-lint:latest`); Python `python3 -m unittest discover -s tools -p 'test_eventreplay.py'`.

---

### Task 1: Learner — spread, ageing, lasting disproof, venue as evidence, no id map

**Files:** `decoder/derive_modifiers.go`, `decoder/derive_modifiers_record.go`, `decoder/derive_modifiers_size.go`, `decoder/derive_modifiers_runtime.go` (log levels), tests.

- Venue cells: still tracked and listed, but never coverage: changes to them are evidence (no shrink/growth), and confirmation/ending depend only on zone-wide (a venue-only modifier stays a candidate, listed; it ends `unconfirmed`/`unseen` as candidates do). Remove the venue confirm path.
- Spread: replace the level-10 parent test with: matching entries from ≥ 8 distinct level-14 cells and max pairwise distance between their cell centres ≥ 10 km (≤ 16 entries; compute on change).
- Entry ageing: record entries carry their unix time; entries older than `unseen` don't count (filled and majority use only fresh entries); all aged → zone-wide off. Size records also take a non-informative "aged-refresh" for every IV-agreeing located roll (it refreshes the record's last-check time without adding an entry).
- Lasting disproof: per modifier, excluded cells = cells whose latest located check of the dimension was not this effect, kept until a matching check in that cell; map ≤ 256 (overflow → zone-wide off, shrink). For size: per excluded cell a 4-bit mask of disproved boundaries; readmitted when each has a fresh match in that cell; any disproof excludes the whole size modifier in that cell.
- Candidate deadline 2 × `unseen`; candidate and `unconfirmed` transitions logged at Debug (others stay Info).
- Remove the learner's repeated-id map (`checked`); keep `EncounterID` in the event only if still used, else remove it.

Tests (each fails on current code, from the reviews): venue across a level-10 edge (two adjacent cells, 10 matches, base elsewhere) never zone-wide; zone-wide size modifier ends after its event with sparse informative base rolls (Codex: 12/h, one informative base roll per 50 min → no XXS published 2 h later); disproven cell D stays excluded after 16 matches elsewhere, readmitted by a match in D; size per-boundary readmission (Codex 0.032 case → not reclassified in D); zone-wide modifier whose matches are older than `unseen` turns off even with fresh agreements absent; a venue-only modifier is never applied (index has no entry); candidate deadline 2 × unseen; no `checked` map (repeated ids now reach the learner — the decode gate's test covers dedupe).

Commit: `fix(decoder): apply only spread zone-wide modifiers; disproof lasts`.

### Task 2: Index, API and decode-path fixes

**Files:** `decoder/derive_modifiers_apply.go`, `decoder/api_derivation_modifiers.go`, `decoder/pokemon_decode.go` (display form change), `decoder/derive_modifiers_runtime.go` and `decoder/derive_events_writer.go` (zone cache caps), `api.md`, `config.toml.example`; tests.

- Index: entries only for applied modifiers (confirmed, zone-wide, not all-strong); lookup covers zone-wide minus excluded cells (map or sorted slice, alloc-free). No venue entries.
- API: `applied` false for venue-only; region unchanged in shape.
- Display form change: when an unencountered record's display form (scope) changes, re-derive the values derived under the old scope (level, CP, moves, size, IVs as applicable) for the new scope; encounter-reported values kept. Find where the display/form changes (e.g. `setPokemonDisplay`) and every derive path it should trigger; test Codex case (strong record derived at 42 under form 0's +5, display changes to form 1 with base 37 → 37).
- Zone caches (learner map, `modifierZoneOf` xsync map, events writer `zones`): cleared when above 200,000 entries.
- Docs: api.md and config.toml.example describe §9 (venue detection-only, spread rule, ageing, lasting disproof).

Commit: `fix(decoder): index applies zone-wide only; forms re-derive; capped caches`.

### Task 3: Replay bucket metadata

**Files:** `decoder/derive_events_writer.go`, `tools/eventreplay.py`, `tools/testdata/derive-events-golden.jsonl`, tests (Go writer test, Python).

- Every row (deviation and summary) carries its bucket length in seconds (`bucket_s`); the reader groups and orders by each row's own bucket, so a mid-file `bucket_minutes` change (60 → 5) and a file starting with a 60-minute bucket replay in non-decreasing order without rewriting valid times (Codex: deviation at 899 under 60-minute buckets stays 899). Old files without `bucket_s` keep today's behaviour.

Commit: `fix(tools): replay orders rows by their own bucket`.
