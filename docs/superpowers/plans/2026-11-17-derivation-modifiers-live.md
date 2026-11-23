# Derivation Modifiers Live Learner Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Golbat detects event modifiers from live encounters, per timezone and level-14 cell, and applies confirmed ones to derived values.

**Architecture:** A pure, single-goroutine state machine (`modifierLearner`) consumes a compact, pointer-free `modifierEvent` (~56 bytes) built from the `deriveEventCheck` the events log already builds (encounter checks and, only for scopes with an active modifier, first sightings), each tagged with its zone and level-14 cell. Active modifiers are published as an immutable index behind an `atomic.Pointer`; the derive path looks a record up lock-free (species key first, cell and zone only on a hit) and adjusts the draw. A Huma endpoint lists active modifiers.

**Tech Stack:** Go 1.26 (`-tags go_json`), `github.com/golang/geo/s2`, `golbat/tz`, `xsync`, Huma.

**Spec:** `docs/design/2026-11-17-derivation-modifiers-live-design.md` (read with `docs/design/2026-11-15-derivation-event-modifiers-design.md` and `tools/eventreplay.py`, whose rules this ports).

## Global Constraints

- Opt-in: `[derivation_modifiers] enabled = false` by default; inert unless `derive_iv` is on.
- Defaults: `start` 3, `confirm` 10, `end` 20, `window_seconds` 1800, `unseen_seconds` 3600, `zone_wide_cells` 3.
- Base size cuts 0.004/0.029/0.971/0.996 (boundaries "1/2","2/3","3/4","4/5"); strong: "4/5" at 0.8 only.
- Cell level 14 (`deriveEventsCellLevel`); zone = IANA name from `tz.SearchTimezone` at the cell centre, "" when unknown.
- Never a blocking send from the decode path: non-blocking send, drop and count with `util.DropReporter` (CLAUDE.md decode-path invariant).
- Only confirmed modifiers apply; never to a Ditto (`IsDitto`); derived values only fill absent fields, as today.
- Log prefix `[DERIVE_MODIFIER]`.
- Performance (Golbat is tuned for throughput and memory):
  - Learner off, or on with no active modifier: the derive path costs one atomic load (nil index), no allocation, no cell or zone work; no sighting is sent.
  - The learner's channel carries `modifierEvent` (no pointers, so the GC never scans the buffer), capacity 16384 (~1 MB). Never send the 288-byte `deriveEventCheck`.
  - A first sighting is sent only when its scope (species/form/strong, or any strong when an all-strong modifier is active) is in the index's active-scope set.
  - The derive path resolves a zone (`tz.SearchTimezone`) only when a matching confirmed modifier is zone-wide; local modifiers are matched by cell alone.
  - The learner keeps its own plain `map[s2.CellID]uint16` (index into a zone-name table); it shares no cache with the decode path.
- Build/test: `go test -tags go_json ./...`; lint `make lint` (via Docker `golangci/golangci-lint:latest` and `golang:1.26` if not installed).

## Review Focus

- A record with no cell and no lat/lon (zone "", cell 0): lookups and learning must not panic; a local modifier never covers cell 0; a zone-wide one in zone "" covers it. Test in Task 3.
- A Ditto record in a region with a confirmed modifier: derived values unchanged. Test in Task 3.
- Noise: thousands of one-off deviations must not grow learner memory without bound; `sweep` prunes tracks with no modifier and no pending deviation in `window`. Test in Task 1.
- A modifier that ends: records derived afterwards get base values again (index rebuilt on every transition). Test in Task 4.
- Two confirmed level offsets for one record (species scope +5, all-strong +3): species scope wins, never summed. Test in Task 3.

---

### Task 1: Learner core — level offset, move pin, unexplained; zones, cells, regions, lifecycle

**Files:**
- Create: `decoder/derive_modifiers.go`
- Test: `decoder/derive_modifiers_test.go`

**Interfaces:**
- Consumes: `deriveEventCheck` (`decoder/derive_events.go`; `Sighting`, `Agree dimAgreement`, `Check derivationCheck` with `Observed`, `Derived` `encounter.Values`, `Move1`, `Move2`, `SizeRoll`) — only to build a `modifierEvent`; `dimState` (`dimAgree`/`dimDisagree`/`dimNoOpinion`).
- Produces:
  ```go
  // modifierEvent is the learner's input: pointer-free, ~56 bytes (assert
  // unsafe.Sizeof <= 64 in a test). Built on the decode path, so building it
  // must not allocate.
  type modifierEvent struct {
      Unix        int64     // seconds
      Cell        s2.CellID // level-14; 0 = unknown
      SizeRoll    float64
      PokemonID, Form int32
      ObsQuick, ObsCharged int32 // observed moves; 0 = none
      LevelDelta  int8      // observed − derived level (only meaningful when Agree.Level == dimDisagree)
      ObsSize     uint8     // observed size class 1..5; 0 = none
      Agree       dimAgreement // per-dimension dimState (5 bytes)
      Strong, Sighting bool
  }
  func newModifierEvent(ev deriveEventCheck) modifierEvent // cell = level-14 parent of ev.CellID (bit op), lat/lon fallback, else 0
  type modifierType uint8 // modLevelOffset, modMovePin, modSizeCutoffs, modUnexplained; String(): "level_offset","move_pin","size_cutoffs","unexplained"
  type modifierStatus uint8 // modCandidate, modConfirmed, modEnded; String(): "candidate","confirmed","ended"
  type modifierScope struct{ PokemonID, Form int32; Strong, AllStrong bool } // AllStrong: PokemonID/Form 0
  type modifierEffect struct{ Delta int32; Slot string /* "quick"|"charged" */; Move int32 }
  type sizeBoundary struct{ Lo, Hi float64; HasLo, HasHi bool } // Task 2 fills
  type derivationModifier struct {
      Type modifierType; Scope modifierScope; Effect modifierEffect
      Boundaries map[string]sizeBoundary // size_cutoffs only (Task 2)
      Zone string; Status modifierStatus
      Start, End, LastSeen time.Time; EndReason string // "agreement"|"contrary"|"unseen"
      Observations, Contradictions int
      InCells, Outside map[s2.CellID]struct{}
  }
  func (m *derivationModifier) ZoneWide(zoneWideCells int) bool // len(InCells) >= n && len(Outside) == 0
  func (m *derivationModifier) Covers(zone string, cell s2.CellID, zoneWideCells int) bool
  func (m *derivationModifier) clone() *derivationModifier // deep copy (maps)
  type modifierParams struct{ Start, Confirm, End, ZoneWideCells int; Window, Unseen time.Duration }
  func defaultModifierParams() modifierParams
  type modifierTransition struct{ At time.Time; State modifierStatus; Mod *derivationModifier } // Mod is a clone at that moment
  type modifierLearner struct{ /* unexported */ }
  func newModifierLearner(p modifierParams) *modifierLearner
  func (l *modifierLearner) feed(ev modifierEvent, zone string) []modifierTransition // check or sighting (ev.Sighting); cell is ev.Cell
  func (l *modifierLearner) sweep(now time.Time) []modifierTransition // unseen ends + prune
  func (l *modifierLearner) active() []*derivationModifier // clones of candidate+confirmed
  func (l *modifierLearner) trackCount() int // for the prune test
  ```

Rules (port of `tools/eventreplay.py` `Detector`, changed per spec §2–3): a track is keyed (type, scope, effect, zone). Deviation classification as `Detector._deviation` (IVs disagree → `unexplained` only; IVs agree → level/quick/charged; all-strong level track needs ≥2 species among pending). Gate: an agreement on the track's dimension in cell c removes pending deviations from cell c with time ≤ the agreement. Candidate/confirm/contradiction as spec §3 (contradiction only from in-cells). While active: a matching deviation adds its cell to `InCells` (and removes it from `Outside`); an agreement in an in-cell adds to the end count (reset by any matching deviation); an agreement in a non-in-cell adds the cell to `Outside`. `LastSeen` updates on any check or sighting of the scope in the region (`Covers`). `sweep(now)` ends active modifiers with `now - LastSeen > Unseen` at `LastSeen` (reason "unseen") and deletes tracks with no modifier and no pending deviation newer than `now - Window`. `feed` calls `sweep(ev.Time)` first.

- [ ] **Step 1: Write the failing tests** in `decoder/derive_modifiers_test.go`, with a helper `modCheck(ts time.Time, cell s2.CellID, opts ...func(*modifierEvent)) modifierEvent` (strong 959/3272, everything agreeing, Move2 268 derived) and cells `cellA, cellB, cellC, cellD` (distinct level-14 cells), zone `"Europe/London"`:
  - `TestModifierMovePinLifecycle`: 3 charged deviations (observed 494) in cellA within a minute → one `candidate` transition, effect `{Slot:"charged", Move:494}`; 7 more → `confirmed`, `Observations == 10`; then 20 charged agreements in cellA → `ended`, `EndReason == "agreement"`.
  - `TestModifierAgreementOtherCellDoesNotGate`: interleave agreements in cellB with 3 deviations in cellA → candidate still forms; agreements in cellA between them → no candidate.
  - `TestModifierVenueStaysLocal`: confirmed pin from cellA+cellB+cellC deviations while cellD agrees → `ZoneWide(3) == false`, `Covers(zone, cellA) == true`, `Covers(zone, cellD) == false`, `Covers(zone, unseenCell) == false`.
  - `TestModifierGlobalGoesZoneWide`: deviations from cellA, cellB, cellC, no agreements → `ZoneWide(3) == true`, `Covers(zone, unseenCell) == true`, `Covers("America/New_York", cellA) == false`.
  - `TestModifierZonesSeparate`: deviations in two zones make two candidates; an agreement in zone 2 does not touch zone 1's pending deviations.
  - `TestModifierAllStrongLevelNeedsTwoSpecies`: 3 level +5 deviations of one species → species-scope candidate only; add one of a second species → all-strong candidate too.
  - `TestModifierContradictionBlocksConfirm`: candidate charged=494 in cellA; one charged deviation observed 300 in cellA → `Contradictions == 1`; 20 more 494 → stays `candidate`.
  - `TestModifierUnexplainedOnIVDisagree`: IV-disagreeing deviations → `unexplained` track only (no level/move tracks).
  - `TestModifierUnseenEndsAtLastSeen`: confirmed pin, last check at t; `sweep(t+59m)` → nothing; `sweep(t+61m)` → `ended` with `At == t`, `EndReason == "unseen"`; a sighting (`Sighting: true`) of the scope in an in-cell at t+30m moves it to t+30m.
  - `TestModifierSweepPrunesNoise`: 5000 deviations, each a different (species, move) once, then `sweep(t+31m)` → `trackCount() == 0`.
  - `TestModifierEventCompact`: `unsafe.Sizeof(modifierEvent{}) <= 64`; `newModifierEvent` of a level-15 `CellId` gives its level-14 parent; `testing.AllocsPerRun(100, ...) == 0` for `newModifierEvent`.
- [ ] **Step 2: Run** `go test -tags go_json -run 'TestModifier' ./decoder/` — expect FAIL (undefined).
- [ ] **Step 3: Implement** the interfaces above in `decoder/derive_modifiers.go`.
- [ ] **Step 4: Run** the same command — expect PASS; then `go test -tags go_json ./decoder/`.
- [ ] **Step 5: Commit** `feat(decoder): derivation modifier learner core (zones, cells, regions)`.

### Task 2: Learner — size cutoffs

**Files:**
- Create: `decoder/derive_modifiers_size.go`
- Modify: `decoder/derive_modifiers.go` (dispatch size deviations/agreements to the size track)
- Test: `decoder/derive_modifiers_size_test.go`

**Interfaces:**
- Consumes: Task 1 types; `encounter.FormFixesSize(form int32) bool`; size classes 1..5 (`encounter.SizeXXS`.. `SizeXXL`).
- Produces: `derivationModifier.Boundaries` filled for `modSizeCutoffs` (keys "1/2".."4/5"; only boundaries whose interval excludes the base cut, as `eventreplay._size_effect`); `func baseSizeCuts(strong bool) map[string]float64`.

Rules (port of `eventreplay` size handling, per cell): the size track is per species scope and zone, gate off (`start` size deviations in `window` make a candidate regardless of agreements). Evidence rolls: every check with `Agree.IVs == dimAgree` and `Agree.Size != dimNoOpinion` in an in-cell, keyed by observed class, kept as min/max per class (the deviation itself included); a roll that would empty an interval is a contradiction (deviation) or left out (agreement). Contrary roll (spec §4): in an in-cell → end, reason "contrary"; in a non-in-cell → that cell joins `Outside`. Agreements never end size and never mark outside.

- [ ] **Step 1: Write the failing tests:**
  - `TestModifierSizeRattataShift`: Rattata 19/0 not strong, 8×1500 uniform rolls from `rand.New(rand.NewPCG(7, 7))` across cellA–cellC over 2 h, observed class by cuts (0.012, 0.037, 0.971, 0.996), derived by base; `Agree.Size` disagree where they differ → one confirmed `size_cutoffs`, `Contradictions == 0`, `Boundaries` keys exactly {"1/2","2/3"}, each `Lo < cut <= Hi` and `Hi-Lo < 0.001` for cuts 0.012 and 0.037.
  - `TestModifierSizeContraryEndsInCell`: active size modifier with "1/2" = [0.011, 0.015]; an XS (class 2) check with roll 0.006 in an in-cell → `ended`, reason "contrary".
  - `TestModifierSizeContraryOutsideMarksCell`: same roll in a non-in-cell → still active, that cell in `Outside`, `ZoneWide(3) == false`.
  - `TestModifierSizeIgnoresIVDisagree`: IV-disagreeing checks add no rolls (Boundaries unchanged).
- [ ] **Step 2: Run** `go test -tags go_json -run 'TestModifierSize' ./decoder/` — FAIL.
- [ ] **Step 3: Implement** in `decoder/derive_modifiers_size.go`.
- [ ] **Step 4: Run** — PASS; then `go test -tags go_json ./decoder/`.
- [ ] **Step 5: Commit** `feat(decoder): size cutoff modifiers in the learner`.

### Task 3: Applying confirmed modifiers to derived values

**Files:**
- Create: `decoder/derive_modifiers_apply.go`
- Modify: `decoder/pokemon_decode.go` (`deriveIvIfAbsent`, `deriveSizeAndMovesIfAbsent`, `applyDerivedValues`, `applyDerivedMoves`)
- Test: `decoder/derive_modifiers_apply_test.go`

**Interfaces:**
- Consumes: Task 1–2 `derivationModifier`, `Covers`, `baseSizeCuts`.
- Produces:
  ```go
  type modifierIndex struct{ /* confirmed: by (PokemonID, Form, Strong) plus all-strong, each entry with its zone, ZoneWide flag and in-cell set; active scopes (candidate+confirmed) for sighting gating */ }
  func buildModifierIndex(mods []*derivationModifier, zoneWideCells int) *modifierIndex // nil when mods is empty
  func (idx *modifierIndex) wantsSighting(pokemonID, form int32, strong bool) bool // scope active (or strong && an all-strong modifier active); false on nil idx
  var derivationModifierIndex atomic.Pointer[modifierIndex] // nil = no modifiers
  type derivationAdjust struct {
      Suppress   bool  // unexplained: derive nothing
      LevelDelta int32
      Quick, Charged int32 // 0 = no pin
      Size       map[string]sizeBoundary // nil = base cuts
  }
  // lookup: probe the species key and the all-strong key first (no match → zero value, no cell/zone work);
  // a local entry matches when cell is in its in-cells; a zone-wide entry calls zoneOf(cell) once and compares.
  func (idx *modifierIndex) lookup(pokemonID, form int32, strong bool, cell s2.CellID, zoneOf func(s2.CellID) string) derivationAdjust
  func modifierZoneOf(cell s2.CellID) string // decode-path cache (xsync.Map[s2.CellID]string) over tz.SearchTimezone at the cell centre; filled only by zone-wide matches
  func pokemonModifierCell(p *Pokemon) s2.CellID // level-14 parent of CellId (level-15 map cell), else of lat/lon, else 0
  func (p *Pokemon) modifierAdjust() derivationAdjust // zero value when the index is nil, p.IsDitto, or no match
  func (a derivationAdjust) sizeClass(roll float64, strong bool, form int32, base int32) (int32, bool) // false = leave size absent
  ```

Rules (spec §5): `modifierAdjust` loads the index first and returns the zero value on nil before touching `p` further; the cell is computed only after a key matches, the zone only for a zone-wide match. Species scope level delta wins over all-strong; level = derived + delta, clamped to [1, 50]; move pin sets the slot only when it is absent (as derived moves do); size: for a form with `encounter.FormFixesSize(form)` return (base, true); otherwise classify `roll` by the cuts, replacing each listed boundary's cut with its interval: roll < Lo → below, roll ≥ Hi → above, else return false. `Suppress` makes `deriveIvIfAbsent` and `deriveSizeAndMovesIfAbsent` return without writing.

- [ ] **Step 1: Write the failing tests** (set the index with `derivationModifierIndex.Store(buildModifierIndex(...))`, `t.Cleanup` stores nil; config `DeriveIv`/`DeriveStrong` on as `derive_iv_test.go` does):
  - `TestDerivationAdjustLevelOffset`: confirmed zone-wide all-strong +5 → a strong record in the zone derives level base+5 (compare with `encounter.Derive` for the same id); a record in another zone gets base.
  - `TestDerivationAdjustSpeciesWinsOverAllStrong`: species +5 and all-strong +3 confirmed → +5.
  - `TestDerivationAdjustMovePin`: charged pin 494 → `Move2 == 494`, `Move1` as derived; a record with an observed `Move2` keeps it.
  - `TestDerivationAdjustSizeShift`: "1/2" [0.0119, 0.0121], "2/3" [0.0369, 0.0371]: `sizeClass(0.008)` → (1,true), `(0.03)` → (2,true), `(0.012)` → (0,false), `(0.5)` → (3,true).
  - `TestDerivationAdjustUnexplainedSuppresses`: confirmed unexplained → no IVs, level, size or moves written.
  - `TestDerivationAdjustCandidateIgnored`: a candidate modifier is not in the index.
  - `TestDerivationAdjustLocalRegion`: local modifier with InCells {cellA}: record in cellA adjusted, in cellB not; record with CellId 0 and lat/lon 0,0 (zone "", cell 0) not adjusted and no panic.
  - `TestDerivationAdjustSkipsDitto`: `IsDitto` record in region → base values.
  - `TestDerivationAdjustLazyZone`: index with only a local modifier for species X → `lookup` for X in an in-cell and for species Y never calls `zoneOf` (counting stub); a zone-wide modifier calls it once per lookup.
  - `TestDerivationAdjustNoAllocs`: `testing.AllocsPerRun` of `p.modifierAdjust()` is 0 with a nil index and with a non-matching index.
  - `TestModifierIndexWantsSighting`: candidate pin for 959/3272 strong → wants that scope, not 959/3272 non-strong nor 635; an all-strong candidate → wants any strong scope; nil index → false.
- [ ] **Step 2: Run** `go test -tags go_json -run 'TestDerivationAdjust' ./decoder/` — FAIL.
- [ ] **Step 3: Implement** `decoder/derive_modifiers_apply.go` and the four hooks in `decoder/pokemon_decode.go` (read `p.modifierAdjust()` once per derive call).
- [ ] **Step 4: Run** — PASS; then `go test -tags go_json ./...`.
- [ ] **Step 5: Commit** `feat(decoder): apply confirmed derivation modifiers to derived values`.

### Task 4: Runtime — config, learner goroutine, wiring, logging

**Files:**
- Create: `decoder/derive_modifiers_runtime.go`, `decoder/derive_modifiers_runtime_test.go`
- Modify: `config/config.go`, `config/reader.go`, `config.toml.example`, `main.go`, `decoder/pokemon_decode.go` (~2186, the encounter check submit), `decoder/pokemon_state.go` (sighting submit in `savePokemonRecordAsAtTime`), `CLAUDE.md` (Project Layout)

**Interfaces:**
- Consumes: Tasks 1–3.
- Produces:
  ```go
  // config
  type derivationModifiers struct {
      Enabled bool `koanf:"enabled"`
      Start, Confirm, End, ZoneWideCells int `koanf:"start" / "confirm" / "end" / "zone_wide_cells"` // one tag each
      WindowSeconds, UnseenSeconds int `koanf:"window_seconds" / "unseen_seconds"`
  } // config.Config.DerivationModifiers, koanf "derivation_modifiers"; defaults from Global Constraints
  // decoder
  func InitDerivationModifiers() error // no-op unless Enabled && DeriveIv; warns and stays off if DeriveIv is false
  func FlushDerivationModifiers()      // idempotent shutdown
  func submitDerivationModifierEvent(ev modifierEvent) // non-blocking, DropReporter
  func activeDerivationModifiers() []*derivationModifier // snapshot for Task 5 (copy taken by the worker after each transition, behind a mutex or atomic)
  var derivationModifierLearnerOn bool // set by Init; read by the submit sites
  ```

Worker: channel `chan modifierEvent`, capacity 16384; one goroutine owns the learner; resolves `zone` from its own `map[s2.CellID]uint16` plus name table (tz lookup at the cell centre on a miss; zone "" for cell 0); feeds; a 1-minute ticker calls `sweep(time.Now())`; after any transitions it logs each (`[DERIVE_MODIFIER] <state> <type> <effect> scope=<...> zone=<zone> region=zone-wide|<n> cells obs=<n> contra=<n>`), rebuilds the index (`derivationModifierIndex.Store(buildModifierIndex(active, ZoneWideCells))`; nil when none) and refreshes the snapshot. Submit sites: encounter check — build the `deriveEventCheck` when `derivationEventsWriter != nil || derivationModifierLearnerOn`, send it to the events writer if on and `newModifierEvent(ev)` to the learner if on; first sighting — send to the learner only when `derivationModifierIndex.Load().wantsSighting(...)`. `main.go`: Init after `InitDerivationEvents` (log the error, continue), Flush next to `FlushDerivationEvents`; include `DerivationModifiers.Enabled` in the masterfile condition.

- [ ] **Step 1: Write the failing tests:**
  - `TestDerivationModifiersRuntimeConfirmAndEnd`: start a runtime with defaults over a test channel; submit 10 charged-494 deviations then 20 agreements in one cell; drain; the index after the 10th has the pin for that cell, after the 20 agreements is empty (records derive base values again).
  - `TestDerivationModifiersInitDisabledWithoutDeriveIv`: `Enabled` true, `DeriveIv` false → no worker, `derivationModifierLearnerOn == false`.
  - `TestSubmitDerivationModifierEventDropsWhenFull`: tiny channel, full → returns without blocking, drop counted.
  - `TestDerivationModifierSightingGated`: learner on, nil index → saving a new pokemon record sends nothing; with an active scope for it → one sighting sent.
  - `TestDerivationModifiersConfigDefaults` in `config/` (or the existing reader test file): defaults 3/10/20/1800/3600/3.
- [ ] **Step 2: Run** `go test -tags go_json -run 'TestDerivationModifiers|TestSubmitDerivationModifier' ./decoder/ ./config/` — FAIL.
- [ ] **Step 3: Implement** config, runtime, wiring, `config.toml.example` section (commented like `[derivation_events]`), CLAUDE.md layout lines for the four new `derive_modifiers*.go` files.
- [ ] **Step 4: Run** `go build -tags go_json ./... && go test -tags go_json ./...` — PASS; `make lint` — 0 issues.
- [ ] **Step 5: Commit** `feat: derivation modifiers learner runtime and config`.

### Task 5: API — list active modifiers

**Files:**
- Modify: `routes_huma.go` (register in `registerStatusRoutes`), `decoder/api_status.go`, `api.md`, `docs/design/README.md` (index row for the live design), `decoder/api_status_test.go` or the root huma route tests
- Create: `decoder/api_derivation_modifiers.go`

**Interfaces:**
- Consumes: `activeDerivationModifiers()` (Task 4), `derivationModifier` (Task 1).
- Produces:
  ```go
  type ApiDerivationModifiers struct {
      Version int `json:"version"`              // 1
      GeneratedAt string `json:"generated_at"`  // RFC 3339 UTC
      Modifiers []ApiDerivationModifier `json:"modifiers"`
  }
  type ApiDerivationModifier struct { // shareable format of the 2026-11-15 design §6 plus region
      ID string `json:"id"`; Scope ApiModifierScope `json:"scope"`; Window ApiModifierWindow `json:"window"`
      Effect map[string]any `json:"effect"`; Status string `json:"status"`
      Evidence ApiModifierEvidence `json:"evidence"`; Region ApiModifierRegion `json:"region"`
  }
  type ApiModifierRegion struct{ Zone string `json:"zone"`; Cells []string `json:"cells"` } // cells: level-14 tokens, sorted; null when zone-wide
  func GetApiDerivationModifiers(now time.Time) *ApiDerivationModifiers
  ```
  `ApiStatusResult.Features.DerivationModifiers bool` (`json:"derivation_modifiers"`). Route `GET /api/derivation/modifiers`, OperationID `get-derivation-modifiers`, tag `Status`, `api_secret` security. IDs and effect payloads exactly as `eventreplay._modifier_id` / `modifiers_json` (e.g. `2026-11-15-move_pin-strong-959-3272-charged-494`; size effect `{"type":"size_cutoffs","boundaries":{"1/2":[lo,hi]}}` with null for an unbounded end).

- [ ] **Step 1: Write the failing tests:** `TestApiDerivationModifiersShape` (one confirmed zone-wide pin and one local size modifier → ids, `region.cells == nil` for the first, sorted tokens for the second, JSON round-trips) and a route test that `GET /api/derivation/modifiers` returns 200 with `version == 1` and `/api/status` has `features.derivation_modifiers`.
- [ ] **Step 2: Run** `go test -tags go_json -run 'DerivationModifiers' ./decoder/ .` — FAIL.
- [ ] **Step 3: Implement**; document the endpoint and the status flag in `api.md`; add the design index row.
- [ ] **Step 4: Run** `go test -tags go_json ./...` and `make lint` — PASS / 0 issues.
- [ ] **Step 5: Commit** `feat: /api/derivation/modifiers lists active modifiers`.
