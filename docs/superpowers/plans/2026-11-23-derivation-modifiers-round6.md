# Derivation Modifiers Round 6 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the round-5 review findings (Fable, Codex) in the live derivation-modifier learner, index and replay tool.

**Architecture:** Learner (`decoder/derive_modifiers.go`, `_record.go`, `_cover.go`, `_size.go`), index (`_apply.go`), `tools/eventreplay.py`.

**Tech Stack:** Go 1.27 (`-tags go_json`), Python 3.

**Spec:** `docs/design/2026-11-17-derivation-modifiers-live-design.md` §10 (+ §10.1); this plan adds §10.2.

## Global Constraints

- Off by default; decode path unchanged with it off; nil-index derive one atomic load, zero allocations; lookup allocation-free.
- A wrongly applied value is worse than an absent one: when in doubt, withhold.
- Keep the invariant tests passing.
- Build/test: `go test -tags go_json ./...`, gofmt clean, `make lint` (Docker `golang:1.27`, `golangci/golangci-lint:latest`); Python `python3 -m unittest discover -s tools -p 'test_eventreplay.py'`.

---

### Task 1: Learner and index fixes

**Files:** `decoder/derive_modifiers*.go`, tests, design §10.2.

- Pause counts any located non-neutral non-match (base agreement or another effect) in the modifier's record; a match resets it (Fable: +5 → +3 kept applying +5 ~28 min at 12/h). Test: superseded +5 → +3 at 12/h: stale +5 stops within two checks.
- Size evidence from a cell with an unresolved disproof for that boundary never updates the shared interval, adds a zone match, or clears a pause; evaluate resolution first (Codex: true cut 0.012, interval (0.011, 0.015], excluded venue XS at 0.0115 must not tighten the upper bound; a roll at 0.01175 elsewhere stays XXS; an excluded cell's non-resolving match must not unpause).
- Size combination validates the whole cut sequence against known bounds, including intervals with no upper bound: an open interval's Lo must lie below the next cut's lower bound (base or interval), else withhold size (Codex: 1/2 lower bound 0.050, open; 2/3 at base 0.029 → withhold, never XXS at 0.040).
- Candidate deadline (2 × unseen) is checked before confirmation on feed and sweep paths (Codex: confirm at 2h00m01s after a sweep at 1h59m59s must not happen).
- §10.2 "Round 6": the rules above; city-scale non-ticketed regional effects (≥ 10 km, about half a zone's checks) are out of scope (events are zone-wide or ticketed venues) and can be applied zone-wide; the drain residual stated both ways (≈ 16–19 after the event's share falls under half; 182 / 563 / 1,623 total post-end spawns at 6000/h for 5 / 15 / 45-minute drains).

Commit: `fix(decoder): pause on any non-match; excluded size evidence stays local; full cut validation`.

### Task 2: Replay summary timing and sampling hours

**Files:** `tools/eventreplay.py`, `tools/test_eventreplay.py`.

- A summary's effective end is `flushed_at` when present, else bucket end, used consistently for sorting, transitions and `last_seen` (Codex/Fable: 60-minute bucket flushed at 11:10 must end a modifier at 11:10, not 12:00). Detector-level regression for a 60→5-minute restart.
- Sampling simulation: when a summary's bucket straddles a quota hour, either split counts by hour proportionally or reject the sampling mode for misaligned buckets with a clear error; pick the simpler correct one and test (Codex: 7-minute bucket over 11:00, pct 0, min_per_hour 1).

Commit: `fix(tools): replay uses a summary's effective end; sampling across hours`.
