# Derivation Event Modifiers Prototype — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An opt-in Golbat "derivation events" log (exact deviations + 15-minute agreement summaries) and a streaming replay script that proposes, confirms and ends event modifiers (level offset, move pin, size cutoffs, unexplained) as the data unfolds.

**Architecture:** `checkDerivation` returns what it computed; the encounter path turns that into a `deriveEventCheck` and does a non-blocking send to a single writer goroutine. The writer aggregates summaries per UTC bucket, writes deviation rows immediately, resolves UTC offset per S2 L10 cell, and writes one JSON-lines file per UTC day (gzipped on rollover). `tools/eventreplay.py` (stdlib only) merges files in time order and runs a detector state machine, printing transitions, emitting modifier JSON, and optionally simulating thinned sampling.

**Tech Stack:** Go 1.26 (`go_json` tag), `github.com/golang/geo/s2`, `golbat/tz`; Python 3.12 stdlib + `unittest`.

**Spec:** `docs/design/2026-11-15-derivation-event-modifiers-design.md`

## Global Constraints

- Nothing in this plan changes published/derived values, webhooks, `[DERIVE]` rows or `derivation_*` metrics.
- Never block the decode path: the send to the writer is non-blocking; full channel → drop, aggregated via `util.DropReporter` (one log line/second).
- Encounters where `pokemon.IsDitto` or the Ditto branch is `dittoBranchOpposite` are excluded from both record kinds.
- A move outside its pool is a **deviation** in this log (unlike `checkDerivation`'s "no opinion").
- Every check counts in the summary, deviations included.
- Default bucket 15 minutes; default retention 14 days; feature off by default.
- Location in records is an S2 **level-14** cell id only; accounts only as a salted hash; never raw username or lat/lon.
- Script: Python standard library only; must read plain and gzip files and tolerate a truncated final line / truncated gzip stream.
- Go verification runs in Docker if Go isn't on PATH:
  `docker run --rm -v "$PWD":/src -w /src -v gomod:/go/pkg/mod -v gobuild:/root/.cache/go-build golang:1.26 go test -tags go_json ./derive/... ./decoder/ ./config/`
- Python tests: `docker run --rm -v "$PWD":/src -w /src python:3.12 python -m unittest discover -s tools -p 'test_*.py' -v`
- `make lint` must pass before the branch is finished.

## Review Focus

1. **Unwritable directory / disk error** — feature logs an error and disables itself; Golbat keeps running. Test: `TestDeriveEventsWriterInitBadDir` (Task 4).
2. **Restart within the same UTC day, and leftover plain files from earlier days** — today's file is appended to, not truncated; an earlier day's plain file is gzipped at startup. Test: `TestDeriveEventsWriterAppendsAndCompressesLeftovers` (Task 4).
3. **No cell id, no position, or timezone lookup returns ""** — offset 0, no panic. Test: `TestDeriveEventsUTCOffsetUnknownLocation` (Task 4).
4. **Fixed-size forms (Pumpkaboo/Gourgeist) and unset size (0)** — size dimension is "no opinion", never a deviation, so size cutoffs aren't learned from them. Test: `TestClassifyDimensionsSizeNoOpinion` (Task 3).
5. **Log cut mid-write (truncated gzip or partial last line)** — script processes everything before the break and warns once. Test: `test_reads_truncated_gzip_and_partial_line` (Task 6).

---

### Task 1: Expose the raw size roll from the derivation

**Files:**
- Modify: `derive/encounter/encounter.go` (`Draw`, `For`, `ForStrong`, `size`, `strongSize`)
- Test: `derive/encounter/encounter_test.go`

**Interfaces:**
- Produces: `Draw.SizeRoll float64` — the raw `Double()` the size class was cut from (set for both walks, including fixed-size forms). `func FormFixesSize(form int32) bool` — reports whether `formSizes` overrides the class.

- [ ] **Step 1: Write failing tests**

```go
func TestSizeRollClassifies(t *testing.T) {
	for id := uint64(1); id < 5000; id++ {
		d := For(id*7919, false, 0)
		if d.SizeRoll < 0 || d.SizeRoll >= 1 { t.Fatalf("roll %v out of range", d.SizeRoll) }
		if classifySize(d.SizeRoll) != d.Size { t.Fatalf("id %d: class %d != Size %d", id, classifySize(d.SizeRoll), d.Size) }
		s := ForStrong(id*7919, false, 0)
		want := SizeXXL
		if s.SizeRoll < strongSizeCut { want = SizeXL }
		if s.Size != want { t.Fatalf("strong id %d mismatch", id) }
	}
}

func TestFormFixesSize(t *testing.T) {
	if !FormFixesSize(2641) || FormFixesSize(0) { t.Fatal("FormFixesSize") }
	d := For(12345, false, 2644)
	if d.SizeRoll == 0 { t.Fatal("fixed-size form must still report its roll") }
}
```

- [ ] **Step 2: Run, expect FAIL** (`d.SizeRoll undefined`): `go test -tags go_json ./derive/encounter/ -run 'SizeRoll|FormFixesSize'`
- [ ] **Step 3: Implement.** `size`/`strongSize` return `(class int32, roll float64)`; `For`/`ForStrong` store the roll in `Draw.SizeRoll`. Keep the Draw-comparable property (float64 is). Doc comment on `SizeRoll` references its use by the derivation events log.
- [ ] **Step 4: Run whole package, expect PASS:** `go test -tags go_json ./derive/...`
- [ ] **Step 5: Commit** `feat(derive): expose raw size roll on Draw`

---

### Task 2: Config section

**Files:**
- Modify: `config/config.go`, `config/reader.go` (defaults), `config.toml.example`, `main.go:191-197` (`needsMasterfile`)
- Test: `config/` existing default-loading test file if present, else `config/derivation_events_test.go`

**Interfaces:**
- Produces: `config.Config.DerivationEvents` of type

```go
type derivationEvents struct {
	Enabled       bool   `koanf:"enabled"`
	Dir           string `koanf:"dir"`            // default "logs"
	BucketMinutes int    `koanf:"bucket_minutes"` // default 15
	RetainDays    int    `koanf:"retain_days"`    // default 14
}
// field on configDefinition:
DerivationEvents derivationEvents `koanf:"derivation_events"`
```

- [ ] **Step 1: Failing test** `TestDerivationEventsDefaults`: load config from an empty TOML; assert `Enabled == false`, `Dir == "logs"`, `BucketMinutes == 15`, `RetainDays == 14`. Follow the pattern of any existing reader test; if none, call the reader with a temp empty file.
- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement** struct + defaults in `reader.go`; `needsMasterfile` also true when `cfg.DerivationEvents.Enabled` (move pools need it). Add a `[derivation_events]` block to `config.toml.example` with a comment: opt-in, one JSON-lines file per UTC day in `dir`, deviations + per-bucket agreement summaries, read with `tools/eventreplay.py`, contains coarse (~10 km) cells and salted account hashes only.
- [ ] **Step 4: Run, expect PASS.**
- [ ] **Step 5: Commit** `feat(config): derivation_events section`

---

### Task 3: Per-dimension classification and the event check

**Files:**
- Create: `decoder/derive_events.go`
- Modify: `decoder/derive_validation.go` (`checkDerivation` returns its computation)
- Test: `decoder/derive_events_test.go`

**Interfaces:**
- Consumes: `encounter.Draw.SizeRoll`, `encounter.FormFixesSize` (Task 1); existing `derivedMoveResult`, `moveInPool`, `dittoBranch`.
- Produces:

```go
type dimState int8
const (dimNoOpinion dimState = iota; dimAgree; dimDisagree)

type dimAgreement struct{ IVs, Level, Size, Quick, Charged dimState }
func (a dimAgreement) Deviates() bool // any dimension == dimDisagree

// classifyDimensions: IVs = atk/def/sta all equal; Level equal;
// Size no opinion when observed.Size == 0 or FormFixesSize(form);
// Quick/Charged no opinion when !moves.Resolved or observed move == 0;
// out-of-pool observed move → dimDisagree; else equal → agree.
func classifyDimensions(observed, derived encounter.Values, form int32,
	moves derivedMoveResult, move1, move2 int32) dimAgreement

// returned by checkDerivation (callers that ignore it are unchanged)
type derivationCheck struct {
	Observed, Derived, Opposite encounter.Values
	SizeRoll  float64
	Moves     derivedMoveResult
	Move1, Move2 int32
	Branch    dittoBranch
	Boosted   bool
}

type deriveEventCheck struct {
	Time        time.Time
	EncounterID uint64
	PokemonID, Form, Costume, Weather int32
	Strong, Boosted bool
	CellID      int64   // the server's level-15 map cell (Pokemon.CellId); 0 = none
	Lat, Lon    float64 // fallback when CellID is 0; never written
	SpawnID     int64  // 0 = none
	FirstSeen   uint32 // 0 = unknown (set only in savePokemonRecord)
	Account     string // raw; hashed by the writer, never written raw
	Check       derivationCheck
	Agree       dimAgreement
}

// newDeriveEventCheck returns ok=false for an excluded encounter
// (pokemon.IsDitto or chk.Branch == dittoBranchOpposite).
func newDeriveEventCheck(p *Pokemon, scan *pokemonScan, chk derivationCheck,
	username string, now time.Time) (deriveEventCheck, bool)
```

- [ ] **Step 1: Failing tests**
  - `TestClassifyDimensionsAgreeAll` — identical values/moves → all `dimAgree`, `Deviates()==false`.
  - `TestClassifyDimensionsLevelOnly` — level +5, rest equal → `Level==dimDisagree`, IVs/Size agree.
  - `TestClassifyDimensionsMoveOutOfPool` — charged 494 not in `ChargedPool{268, 300}` → `Charged==dimDisagree`.
  - `TestClassifyDimensionsSizeNoOpinion` — observed size 0 → `Size==dimNoOpinion`; form 2641 with differing size → `dimNoOpinion`.
  - `TestClassifyDimensionsUnresolvedMoves` — `Resolved=false` → both move dims `dimNoOpinion`.
  - `TestNewDeriveEventCheckExcludesDitto` — `IsDitto=true` → ok false; `Branch=dittoBranchOpposite` → ok false.
  - `TestCheckDerivationReturnsComputation` — using an existing `checkDerivation` test fixture, assert returned `Derived` equals `encounter.Derive(...)` values and `SizeRoll` equals the Draw's.
- [ ] **Step 2: Run, expect FAIL:** `go test -tags go_json ./decoder/ -run 'ClassifyDimensions|DeriveEventCheck|CheckDerivationReturns'`
- [ ] **Step 3: Implement.** `checkDerivation` gains return type `derivationCheck` (fill from values it already computes; no behaviour change). `newDeriveEventCheck` fills location from `p.CellId` (and `p.Lat/p.Lon` as fallback), `p.SpawnId`, `p.FirstSeenTimestamp`, and computes `Agree` via `classifyDimensions`.
- [ ] **Step 4: Run whole decoder package, expect PASS** (existing `checkDerivation` tests compile unchanged).
- [ ] **Step 5: Commit** `feat(decoder): per-dimension derivation check for events log`

---

### Task 4: Writer — files, summaries, timezone, salt

**Files:**
- Create: `decoder/derive_events_writer.go`
- Test: `decoder/derive_events_writer_test.go`

**Interfaces:**
- Consumes: `deriveEventCheck`, `dimAgreement` (Task 3); `config.Config.DerivationEvents` (Task 2).
- Produces:

```go
func newDeriveEventsWriter(dir string, bucket time.Duration, retainDays int,
	now func() time.Time) (*deriveEventsWriter, error)
func (w *deriveEventsWriter) handle(ev deriveEventCheck) // synchronous; used by run() and tests
func (w *deriveEventsWriter) tick()                      // flush bucket if now >= bucketEnd
func (w *deriveEventsWriter) close() error               // flush current bucket, close file
func (w *deriveEventsWriter) run()                       // drains channel + 1-minute ticker until channel closed
```

**Record formats** (one JSON object per line; field names fixed — the script depends on them):

```jsonc
{"kind":"deviation","ts":1763200000,"utc_offset":0,"encounter_id":"158…","pokemon_id":959,"form":3272,"costume":0,
 "strong":true,"boosted":false,"weather":5,"cell":"<s2 L10 token>","spawn_id":0,"first_seen":0,"account":"a1b2c3d4e5f6",
 "observed":{"attack":13,"defense":13,"stamina":15,"level":38,"size":4},
 "derived":{...},"opposite":{...},"size_roll":0.9734,
 "observed_move_1":350,"observed_move_2":494,"derived_move_1":350,"derived_move_2":268,
 "move_1_in_pool":true,"move_2_in_pool":false,
 "agree":{"ivs":"agree","level":"agree","size":"agree","quick":"agree","charged":"disagree"}}
{"kind":"summary","bucket":1763199900,"bucket_seconds":900,"pokemon_id":959,"form":3272,"strong":true,"boosted":false,
 "utc_offset":0,"checked":41,
 "ivs":{"ok":41,"n":41},"level":{"ok":41,"n":41},"size":{"ok":40,"n":40},"quick":{"ok":41,"n":41},"charged":{"ok":5,"n":41},
 "size_rolls":{"4":[0.9712,0.9958],"5":[0.9963,0.9991]}}
```

`n` = checks where that dimension had an opinion; `size_rolls` keyed by **observed** class (only when Size had an opinion). `agree` values are `"agree"|"disagree"|"none"`. Summary key: `(pokemon_id, form, strong, boosted, utc_offset)`.

Behaviour decisions:
- File `dir/derive-events-YYYY-MM-DD.jsonl` (UTC date of the write), opened `O_APPEND|O_CREATE`, `bufio.Writer` flushed after each bucket flush and on close.
- Day change: close, gzip the previous file to `.jsonl.gz` in a goroutine, remove the plain file after a successful gzip; delete `derive-events-*` files older than `retainDays`. At construction, gzip any plain file whose date is before today.
- Bucket = `ev.Time.UTC().Truncate(bucket)`; an event for a later bucket flushes the current one first; an event for an earlier bucket is counted in the current one (cross-goroutine skew is milliseconds).
- Cell: `s2.CellID(ev.CellID).Parent(14)` — the parent of the server's level-15 map cell (level 14 ≈ 1 km, the level regional spawn differences are partitioned at); only when `CellID` is 0 fall back to `s2.CellIDFromLatLng(...).Parent(14)`. Written as the cell token. Level 14 is a named constant (`deriveEventsCellLevel`) so it can change later.
- UTC offset: cached per level-10 cell, `*time.Location` from `tz.SearchTimezone` at the cell centre (`""` or load error → UTC); offset = `ev.Time.In(loc)` zone offset in minutes. No cell id and lat/lon both 0 → offset 0, cell `""`.
- Salt: `dir/derive-events.salt`, 16 random bytes hex, created if missing; `account` = first 12 hex chars of sha256(salt ‖ username); empty username → `""`.

- [ ] **Step 1: Failing tests** (all use `t.TempDir()` and an injected clock):
  - `TestDeriveEventsWriterDeviationRow` — one disagreeing check → after `close()`, file has one `deviation` line with `move_2_in_pool:false`, `agree.charged=="disagree"`, no `lat`/`lon`/raw username anywhere in the file.
  - `TestDeriveEventsWriterSummaryCounts` — 3 agreeing + 1 level-deviating check in one bucket, then a check in the next bucket → first summary has `checked:4`, `level:{ok:3,n:4}`, `size_rolls` min/max correct.
  - `TestDeriveEventsWriterTickFlushesIdleBucket` — one check, advance clock past bucket end, `tick()` → summary written without another event.
  - `TestDeriveEventsWriterDayRollover` — checks either side of UTC midnight → yesterday's file becomes `.jsonl.gz` (wait for the gzip goroutine via a test hook/WaitGroup), today's plain file exists.
  - `TestDeriveEventsWriterAppendsAndCompressesLeftovers` — pre-create today's file with one line and a plain file dated 3 days ago → after construction + one write, today's file has 2+ lines; old one is gzipped.
  - `TestDeriveEventsWriterRetention` — a file dated `retainDays+1` ago is deleted on rollover.
  - `TestDeriveEventsUTCOffsetUnknownLocation` — `CellID` 0 and lat/lon 0 → `utc_offset:0`, `cell:""`.
  - `TestDeriveEventsCellFromServerCell` — a level-15 `CellID` → `cell` is the token of its level-10 parent, even when lat/lon point elsewhere.
  - `TestDeriveEventsUTCOffsetLondonSummer` — lat 51.5, lon −0.12, time in July → `utc_offset:60`.
  - `TestDeriveEventsAccountHashStable` — same username twice → same 12-char hash; salt file reused by a second writer in the same dir.
  - `TestDeriveEventsWriterInitBadDir` — dir is a regular file → constructor returns error.
- [ ] **Step 2: Run, expect FAIL:** `go test -tags go_json ./decoder/ -run DeriveEvents`
- [ ] **Step 3: Implement** `decoder/derive_events_writer.go` per the decisions above.
- [ ] **Step 4: Run, expect PASS** (also with `-race`).
- [ ] **Step 5: Commit** `feat(decoder): derivation events writer`

---

### Task 5: Wire into the encounter path and lifecycle

**Files:**
- Modify: `decoder/derive_events_writer.go` (package-level init/submit/flush), `decoder/pokemon_decode.go:2186`, `main.go` (init after config + masterfile; flush after `decoder.FlushWriteBehindQueue()`), `CLAUDE.md` (Project Layout lines for the two new decoder files and `tools/eventreplay.py`)
- Test: `decoder/derive_events_writer_test.go`

**Interfaces:**
- Consumes: Tasks 3–4.
- Produces:

```go
func InitDerivationEvents() error // reads config.Config.DerivationEvents; no-op when disabled; on error logs and stays disabled
func FlushDerivationEvents()      // closes the channel, waits for run() to drain, closes the file; no-op when disabled
func submitDerivationEvent(ev deriveEventCheck) // non-blocking; channel cap 65536; drop → util.DropReporter log "[DERIVE_EVENTS] dropped N"
```

- [ ] **Step 1: Failing tests**
  - `TestSubmitDerivationEventDisabledIsNoop` — writer nil → returns immediately, no panic.
  - `TestSubmitDerivationEventDropsWhenFull` — channel of cap 1 with no reader, submit 3 → returns without blocking (run under a 1s deadline); drop reporter count ≥ 2 (`Reset()` first).
- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement.** In `pokemon_decode.go`, capture the `checkDerivation` return; when the writer is enabled, `newDeriveEventCheck(pokemon, &observedScan, chk, username, time.Now())` and submit if ok. Update `CLAUDE.md` layout (`derive_events.go`, `derive_events_writer.go`, `tools/eventreplay.py`).
- [ ] **Step 4: Run** `go test -tags go_json ./decoder/ ./derive/... ./config/` and `go build -tags go_json ./...`, expect PASS.
- [ ] **Step 5: Commit** `feat: wire derivation events log into encounter path`

---

### Task 6: Replay script — reader and detector

**Files:**
- Create: `tools/eventreplay.py`, `tools/test_eventreplay.py`

**Interfaces:**
- Consumes: record formats from Task 4.
- Produces (module-level, importable by tests):

```python
def read_records(paths: list[str]) -> Iterator[dict]
    # plain/.gz; merged by order key: deviation -> ts, summary -> bucket + bucket_seconds;
    # truncated gzip / undecodable last line -> warn once to stderr per file, continue
@dataclass
class Params: start: int = 3; confirm: int = 10; end: int = 20; window: int = 1800
@dataclass
class Transition: ts: int; utc_offset: int; state: str  # "candidate"|"confirmed"|"ended"
    modifier: "Modifier"
@dataclass
class Modifier: type: str; scope: dict; effect: dict; status: str
    start: int | None; end: int | None; observations: int; contradictions: int
class Detector:
    def __init__(self, params: Params): ...
    def feed(self, rec: dict) -> list[Transition]
    def active(self) -> list[Modifier]   # candidate + confirmed
    def all(self) -> list[Modifier]
```

Detection rules (scope dicts: `{"strong": bool}` for all-strong, `{"strong": bool, "pokemon_id": int, "form": int}` for species):
- **level_offset** — deviation with `agree.level=="disagree"` and IVs agree; key `(scope, delta = observed.level - derived.level)`. Candidate when `start` such deviations within `window` seconds and no level agreement in scope since the first of them. Tracked at species scope; for `strong` deviations also at the all-strong scope, which becomes a candidate only once ≥2 species contribute.
- **move_pin** — `agree.quick|charged=="disagree"`; key `(species scope, slot, observed move)`. A deviation on that slot with a different observed move is a contradiction.
- **size_cutoffs** — `agree.size=="disagree"`; species scope. Effect `{"type":"size_cutoffs","boundaries":{"<lo>/<hi>":[max_roll_of_lo, min_roll_of_hi], …}}` built from deviation rolls plus summary `size_rolls` observed since the effect's start, listing only boundaries whose interval excludes the base cut (base: 0.004/0.029/0.971/0.996; strong: 0.8 XL/XXL).
- **unexplained** — `agree.ivs=="disagree"`; species scope; no effect payload.
- **Lifecycle:** candidate at `start` matching deviations; confirmed at `confirm` matching with 0 contradictions; ended when, after the last matching deviation, summaries for the scope whose bucket starts after that deviation contribute ≥ `end` `ok` on the dimension with no further matching deviation (the transition's `ts` is that summary's bucket end). A matching deviation after `ended` opens a new candidate.

- [ ] **Step 1: Failing tests** (helpers `dev(...)`/`summ(...)` build synthetic records):
  - `test_level_offset_lifecycle` — 3 strong 959 deviations level +5 → candidate; 10 → confirmed; then summaries with 20 level `ok` and no deviations → ended, end ts = summary bucket end.
  - `test_level_offset_broad_strong_scope` — +5 deviations across species 959 and 19 → an all-strong `level_offset` candidate appears.
  - `test_move_pin_out_of_pool` — 3 charged deviations observed 494 → `move_pin` `{"slot":"charged","move":494}` candidate; a deviation observing 300 counts as a contradiction and blocks confirm.
  - `test_size_cutoff_interval` — Rattata deviations with rolls 0.008–0.011 (observed XXS, derived XS; the cut moved up from 0.004) → boundary `"1/2"` interval lower bound ≥ 0.011 and excludes 0.004.
  - `test_unexplained` — 3 IV deviations → `unexplained` candidate.
  - `test_merge_order` — two files with interleaved timestamps yield records in order key order.
  - `test_reads_truncated_gzip_and_partial_line` — gzip file cut mid-stream and a plain file whose last line is half-written → all complete records yielded, one warning per file.
- [ ] **Step 2: Run, expect FAIL** (Python test command in Global Constraints).
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run, expect PASS.**
- [ ] **Step 5: Commit** `feat(tools): eventreplay detector`

---

### Task 7: Replay script — CLI, timeline, modifier JSON, sampling simulation

**Files:**
- Modify: `tools/eventreplay.py`, `tools/test_eventreplay.py`

**Interfaces:**
- Consumes: Task 6.
- Produces:

```python
def format_transition(t: Transition) -> str
    # "2026-11-15T10:03:12Z (local 10:03 +00:00) CANDIDATE move_pin charged=494 scope=strong 959/3272 obs=3 contra=0"
def modifiers_json(mods: list[Modifier], generated_at: int) -> dict   # design §6 shape; time_basis "utc"
@dataclass
class SamplingRule: pct: float; min_per_hour: int; seed: int = 1
def thin(records: Iterator[dict], rule: SamplingRule) -> Iterator[dict]
def compare_runs(full: list[Transition], sampled: list[Transition]) -> list[dict]
    # per (type, scope, effect): start/confirm/end delay seconds, or "missed"
def main(argv: list[str]) -> int
```

CLI: `eventreplay.py FILES… [--start N --confirm N --end N --window S] [--emit-modifiers PATH] [--at ISO8601] [--simulate-sampling pct=5,min_per_hour=1[,seed=1]]`. Default prints the timeline. `--at` stops the replay at that time before emitting. With `--simulate-sampling`, run full and thinned replays and print a per-effect table of start/confirm/end delays.

Thinning approximation (state it in `--help`): per `(pokemon_id, form, strong)` per UTC hour, the first `min_per_hour` deviation rows are kept, later ones with probability `pct/100`; summary `ok`/`n` per dimension and `checked` are thinned as `kept = min(n, remaining_min) + Binomial(n - that, pct/100)` with `ok` scaled hypergeometrically (stdlib `random`); `size_rolls` kept as-is.

- [ ] **Step 1: Failing tests**
  - `test_format_transition` — exact string above for a fixed transition.
  - `test_modifiers_json_shape` — keys `version, generated_at, modifiers[].{id, scope, window{start,end,time_basis}, effect, status, evidence{observations,contradictions}}`; ended modifiers are omitted.
  - `test_thin_full_rate_is_identity` — `pct=100` yields equal records.
  - `test_sampling_reports_delay_or_missed` — synthetic event, `pct=0,min_per_hour=1` → start delay > 0 or `"missed"`; `pct=100` → zero delays.
  - `test_main_emit_modifiers` — `main([file, "--emit-modifiers", out])` returns 0 and writes valid JSON.
- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement.** Module docstring explains inputs, outputs and the thinning approximation, like `derivecheck.py`'s.
- [ ] **Step 4: Run, expect PASS.**
- [ ] **Step 5: Run `make lint`** (in Docker if needed), fix findings; run the full Go and Python test commands.
- [ ] **Step 6: Commit** `feat(tools): eventreplay CLI, modifier JSON and sampling simulation`
