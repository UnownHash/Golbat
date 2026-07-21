# Phase 0: Payload Capture Hook + protobench Harness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the Phase 0 deliverables of the proto-decoding GC spec: a debug-gated, size-stratified raw-payload capture hook in Golbat, and a standalone `protobench/` module that decodes captured payloads at volume under three configurations (open, opaque+lazy, opaque−lazy) and reports GC/allocation metrics.

**Architecture:** The capture hook samples payloads on the decode path into `capture/<METHOD>/<ts>_<size>.bin` via an async worker (never blocks decode). `protobench/` is a **separate Go module** (a second generated copy of `vbase.proto` in the same binary as `golbat/pogo` panics the proto registry), containing its own pogo package generated at `API_HYBRID` with `[lazy = true]` annotations, a corpus loader, per-method reader functions simulating Golbat's field accesses, `testing.B` microbenchmarks, and a sustained-volume runner reporting `runtime/metrics` deltas.

**Tech Stack:** Go 1.26, `google.golang.org/protobuf` v1.36.11, `protoc-gen-go` v1.36.11 (pinned), protoc (brew), Python 3 (lazy-annotation script from `origin/len-optimize`).

## Global Constraints

- Spec: `docs/superpowers/specs/2026-07-05-proto-decoding-gc-design.md`. Read it before starting.
- Working branch: `worktree-proto-opaque-gc` (worktree at `.claude/worktrees/proto-opaque-gc`). Never touch other branches.
- `protobench/` must NOT import any `golbat/...` package, and nothing in the root module may import `protobench/...`.
- Never add a blocking send from the decode path to any worker (CLAUDE.md invariant). The capture hook drops when its channel is full.
- Corpus arrives incrementally: every harness component must work with whatever payload directories exist (including none — skip, don't fail).
- Capture defaults: 200 payloads per size bucket × 5 buckets = max 1,000/method. Buckets: <4 KiB, <16 KiB, <64 KiB, <256 KiB, ≥256 KiB.
- Metadata is path-encoded (`<METHOD>/<unixMilli>_<sizeBytes>.bin`), not a JSON sidecar. Task 2 amends the spec wording to match.
- Generated code in `protobench/pogo/`, `protobench/bin/`, `protobench/build/`, and the root `capture/` directory are gitignored — never commit them.
- `vbase.proto` source: `$PROTO_SRC`, default `~/dev/ProtoMirror/vbase.proto`.
- Run `gofmt -l` on changed Go files before every commit; it must output nothing.

---

### Task 1: Capture core (bucketing, naming, quota tracking, async worker)

**Files:**
- Create: `raw_capture.go` (package `main`, repo root)
- Test: `raw_capture_test.go`

**Interfaces:**
- Produces: `CaptureRawPayload(method string, data []byte)` — non-blocking, safe when capture disabled (nil worker).
- Produces: `startRawCapture(dir string, perBucketLimit int) error` — creates dir, seeds quota counts from existing files, starts the worker goroutine, publishes the worker pointer.
- Produces (internal, tested): `captureSizeBucket(size int) int`, `captureFilename(method string, tsMs int64, size int) string`, `seedCaptureCounts(dir string) (map[string]*[5]int, error)`.

- [ ] **Step 1: Write the failing tests**

```go
// raw_capture_test.go
package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCaptureSizeBucket(t *testing.T) {
	cases := []struct {
		size int
		want int
	}{
		{0, 0}, {4095, 0}, {4096, 1}, {16383, 1}, {16384, 2},
		{65535, 2}, {65536, 3}, {262143, 3}, {262144, 4}, {5 << 20, 4},
	}
	for _, c := range cases {
		if got := captureSizeBucket(c.size); got != c.want {
			t.Errorf("captureSizeBucket(%d) = %d, want %d", c.size, got, c.want)
		}
	}
}

func TestCaptureFilename(t *testing.T) {
	got := captureFilename("GET_MAP_OBJECTS", 1751800000000, 12345)
	want := filepath.Join("GET_MAP_OBJECTS", "1751800000000_12345.bin")
	if got != want {
		t.Errorf("captureFilename = %q, want %q", got, want)
	}
}

func TestSeedCaptureCounts(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "ENCOUNTER")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// two files in bucket 0 (<4KiB), one in bucket 2 (16-64KiB), one junk file
	for _, name := range []string{"1_100.bin", "2_200.bin", "3_20000.bin", "junk.txt"} {
		if err := os.WriteFile(filepath.Join(sub, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	counts, err := seedCaptureCounts(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := counts["ENCOUNTER"]
	if got == nil {
		t.Fatal("no counts for ENCOUNTER")
	}
	if got[0] != 2 || got[1] != 0 || got[2] != 1 {
		t.Errorf("counts = %v, want [2 0 1 0 0]", *got)
	}
}

func TestCaptureWorkerWritesAndEnforcesQuota(t *testing.T) {
	dir := t.TempDir()
	if err := startRawCapture(dir, 1); err != nil { // 1 per bucket
		t.Fatal(err)
	}
	defer stopRawCaptureForTest()

	// two tiny payloads, same bucket -> only the first is kept
	CaptureRawPayload("FORT_DETAILS", []byte("aaaa"))
	CaptureRawPayload("FORT_DETAILS", []byte("bbbb"))

	deadline := time.Now().Add(2 * time.Second)
	var files []os.DirEntry
	for time.Now().Before(deadline) {
		files, _ = os.ReadDir(filepath.Join(dir, "FORT_DETAILS"))
		if len(files) >= 1 {
			// give the worker a moment to (incorrectly) write a second file
			time.Sleep(50 * time.Millisecond)
			files, _ = os.ReadDir(filepath.Join(dir, "FORT_DETAILS"))
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1 (quota)", len(files))
	}
}

func TestCaptureDisabledIsNoop(t *testing.T) {
	rawCaptureWorkerPtr.Store(nil)
	CaptureRawPayload("ENCOUNTER", []byte("x")) // must not panic
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run 'TestCapture|TestSeed' ./ -v`
Expected: FAIL — `undefined: captureSizeBucket` (compile error).

- [ ] **Step 3: Write the implementation**

```go
// raw_capture.go
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
)

// Raw payload capture for the protobench decode harness (Phase 0 of
// docs/superpowers/specs/2026-07-05-proto-decoding-gc-design.md).
// Samples are size-stratified per method so the corpus includes rare complex
// shapes. Disabled by default; when disabled the decode-path cost is one
// atomic load. The worker owns all quota state; the producer never blocks
// (channel full => drop + count, per the decode-path invariant in CLAUDE.md).

const captureBuckets = 5

type captureItem struct {
	method string
	data   []byte
}

type rawCaptureWorker struct {
	dir            string
	perBucketLimit int
	ch             chan captureItem
	drops          atomic.Int64
	counts         map[string]*[captureBuckets]int // worker-goroutine only
	stop           chan struct{}
}

var rawCaptureWorkerPtr atomic.Pointer[rawCaptureWorker]

// captureSizeBucket: <4KiB, <16KiB, <64KiB, <256KiB, >=256KiB.
func captureSizeBucket(size int) int {
	switch {
	case size < 4<<10:
		return 0
	case size < 16<<10:
		return 1
	case size < 64<<10:
		return 2
	case size < 256<<10:
		return 3
	default:
		return 4
	}
}

func captureFilename(method string, tsMs int64, size int) string {
	return filepath.Join(method, fmt.Sprintf("%d_%d.bin", tsMs, size))
}

// seedCaptureCounts restores per-bucket counts from files already on disk so
// capture can resume across restarts without exceeding quotas.
func seedCaptureCounts(dir string) (map[string]*[captureBuckets]int, error) {
	counts := make(map[string]*[captureBuckets]int)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		method := e.Name()
		files, err := os.ReadDir(filepath.Join(dir, method))
		if err != nil {
			return nil, err
		}
		c := &[captureBuckets]int{}
		for _, f := range files {
			name := f.Name()
			if !strings.HasSuffix(name, ".bin") {
				continue
			}
			// <unixMilli>_<size>.bin
			base := strings.TrimSuffix(name, ".bin")
			_, sizeStr, ok := strings.Cut(base, "_")
			if !ok {
				continue
			}
			size, err := strconv.Atoi(sizeStr)
			if err != nil {
				continue
			}
			c[captureSizeBucket(size)]++
		}
		counts[method] = c
	}
	return counts, nil
}

func startRawCapture(dir string, perBucketLimit int) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("raw capture dir: %w", err)
	}
	counts, err := seedCaptureCounts(dir)
	if err != nil {
		return fmt.Errorf("raw capture seed: %w", err)
	}
	w := &rawCaptureWorker{
		dir:            dir,
		perBucketLimit: perBucketLimit,
		ch:             make(chan captureItem, 1024),
		counts:         counts,
		stop:           make(chan struct{}),
	}
	go w.run()
	rawCaptureWorkerPtr.Store(w)
	log.Infof("[RAW_CAPTURE] enabled, dir=%s limit=%d/bucket", dir, perBucketLimit)
	return nil
}

// CaptureRawPayload samples a raw proto payload. Cheap no-op when disabled.
func CaptureRawPayload(method string, data []byte) {
	w := rawCaptureWorkerPtr.Load()
	if w == nil {
		return
	}
	select {
	case w.ch <- captureItem{method: method, data: data}:
	default:
		if n := w.drops.Add(1); n%1000 == 1 {
			log.Warnf("[RAW_CAPTURE] channel full, dropped %d samples so far", n)
		}
	}
}

func (w *rawCaptureWorker) run() {
	for {
		select {
		case <-w.stop:
			return
		case item := <-w.ch:
			w.write(item)
		}
	}
}

func (w *rawCaptureWorker) write(item captureItem) {
	c := w.counts[item.method]
	if c == nil {
		c = &[captureBuckets]int{}
		w.counts[item.method] = c
	}
	bucket := captureSizeBucket(len(item.data))
	if c[bucket] >= w.perBucketLimit {
		return
	}
	rel := captureFilename(item.method, time.Now().UnixMilli(), len(item.data))
	path := filepath.Join(w.dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Warnf("[RAW_CAPTURE] mkdir: %v", err)
		return
	}
	if err := os.WriteFile(path, item.data, 0o644); err != nil {
		log.Warnf("[RAW_CAPTURE] write: %v", err)
		return
	}
	c[bucket]++
}

// stopRawCaptureForTest disables capture and stops the worker (tests only).
func stopRawCaptureForTest() {
	if w := rawCaptureWorkerPtr.Swap(nil); w != nil {
		close(w.stop)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -run 'TestCapture|TestSeed' ./ -v`
Expected: PASS (5 tests). Note: `TestCaptureWorkerWritesAndEnforcesQuota` writes two same-bucket payloads with limit 1 and asserts exactly one file lands.

- [ ] **Step 5: Run the whole root-module suite**

Run: `go test ./...`
Expected: all packages PASS (no regressions).

- [ ] **Step 6: Commit**

```bash
gofmt -l raw_capture.go raw_capture_test.go   # must print nothing
git add raw_capture.go raw_capture_test.go
git commit -m "feat: size-stratified raw payload capture worker (Phase 0)"
```

---

### Task 2: Capture config + decode-path wiring

**Files:**
- Modify: `config/config.go` (add `rawCapture` struct + field on `configDefinition`)
- Modify: `config/reader.go` (defaults in the `structs.Provider(configDefinition{...})` block starting at line 20)
- Modify: `decode.go` (hook call in `decode()`, after the level gate at line 27-31)
- Modify: `main.go` (start worker after config load)
- Modify: `.gitignore` (add `/capture/`)
- Modify: `docs/superpowers/specs/2026-07-05-proto-decoding-gc-design.md` (metadata wording)

**Interfaces:**
- Consumes: `startRawCapture(dir, perBucketLimit)`, `CaptureRawPayload(method, data)` from Task 1.
- Produces: `config.Config.RawCapture.{Enabled,Dir,PerBucketLimit}` (koanf keys `raw_capture.enabled`, `raw_capture.dir`, `raw_capture.per_bucket_limit`).

- [ ] **Step 1: Add the config struct**

In `config/config.go`, add to `configDefinition` (after the `Tuning` field):

```go
	RawCapture              rawCapture     `koanf:"raw_capture"`
```

and define next to the other config structs:

```go
// rawCapture samples raw proto payloads to disk for the protobench decode
// harness. See docs/superpowers/specs/2026-07-05-proto-decoding-gc-design.md.
type rawCapture struct {
	Enabled        bool   `koanf:"enabled"`
	Dir            string `koanf:"dir"`
	PerBucketLimit int    `koanf:"per_bucket_limit"`
}
```

- [ ] **Step 2: Add defaults**

In `config/reader.go` inside the `structs.Provider(configDefinition{...})` default block (alongside `Tuning:`), add:

```go
		RawCapture: rawCapture{
			Dir:            "capture",
			PerBucketLimit: 200, // x5 buckets = max 1000 payloads/method
		},
```

- [ ] **Step 3: Wire into decode() and main()**

In `decode.go`, immediately after the level-gate `if` block (currently lines 27-31), add:

```go
	CaptureRawPayload(getMethodName(method, true), protoData.Data)
```

(`CaptureRawPayload` is a single atomic-load no-op unless enabled; `getMethodName(method, true)` yields directory names like `GET_MAP_OBJECTS`.)

In `main.go`, find the line calling `decoder.InitDataCache()` (config is loaded by then) and add right after it:

```go
	if config.Config.RawCapture.Enabled {
		if err := startRawCapture(config.Config.RawCapture.Dir, config.Config.RawCapture.PerBucketLimit); err != nil {
			log.Errorf("raw capture disabled: %v", err)
		}
	}
```

- [ ] **Step 4: Gitignore + spec wording**

Append to `.gitignore`:

```
/capture/
```

In `docs/superpowers/specs/2026-07-05-proto-decoding-gc-design.md`, replace the corpus bullet beginning `- **Metadata sidecar** per payload (method, byte size, capture` and its continuation lines with:

```markdown
   - **Path-encoded metadata** per payload (`<METHOD>/<unixMilli>_<sizeBytes>.bin`)
     so benchmarks can select subsets: microbenchmarks run a small
     representative slice per bucket; sustained volume runs replay the full
     corpus.
```

- [ ] **Step 5: Build and test**

Run: `go build ./... && go test ./...`
Expected: builds clean, all tests PASS.

- [ ] **Step 6: Manual smoke check (config parse)**

Run: `go vet ./config/`
Expected: no output. (Full end-to-end capture is verified on prod by the user setting `[raw_capture] enabled = true` in `config.toml`.)

- [ ] **Step 7: Commit**

```bash
gofmt -l config/config.go config/reader.go decode.go main.go   # must print nothing
git add config/config.go config/reader.go decode.go main.go .gitignore docs/superpowers/specs/2026-07-05-proto-decoding-gc-design.md
git commit -m "feat: config-gated raw payload capture on the decode path"
```

---

### Task 3: protobench module scaffold + codegen pipeline

**Files:**
- Create: `protobench/go.mod`
- Create: `protobench/.gitignore`
- Create: `protobench/scripts/add_lazy_proto.py` (ported from `origin/len-optimize`)
- Create: `protobench/scripts/gen.sh`
- Create: `protobench/roundtrip_test.go` (package `protobench_test` — smoke test that generation worked, both build modes)
- Generated (not committed): `protobench/pogo/vbase.pb.go`, `protobench/pogo/vbase_protoopaque.pb.go`

**Interfaces:**
- Produces: Go package `protobench/pogo` generated at `API_HYBRID` with lazy annotations — getters/setters/builders compile identically with and without `-tags protoopaque`.
- Produces: `protobench/scripts/gen.sh` — the one-command regen (env `PROTO_SRC` overrides proto path).

- [ ] **Step 1: Module scaffold**

```bash
mkdir -p protobench/scripts
cat > protobench/go.mod <<'EOF'
module protobench

go 1.26

require google.golang.org/protobuf v1.36.11
EOF
cat > protobench/.gitignore <<'EOF'
pogo/
bin/
build/
EOF
```

Note: `protobench` is intentionally NOT in a go.work file and NOT referenced by the root module — root-level `go build ./...` / CI never touches it, so the gitignored generated code cannot break the main build.

- [ ] **Step 2: Port the lazy-annotation script from the old branch**

```bash
git fetch origin len-optimize
git show origin/len-optimize:scripts/add_lazy_proto.py > protobench/scripts/add_lazy_proto.py
```

Then edit `protobench/scripts/add_lazy_proto.py`: its `__main__` entry currently derives paths from `get_project_root()` (assumes it lives in `<golbat>/scripts/`). Replace the entry point with explicit arguments, threading them into the existing functions (`get_used_getters(project_root)` and the proto parse/annotate calls — keep those functions unchanged):

```python
if __name__ == "__main__":
    import argparse
    ap = argparse.ArgumentParser()
    ap.add_argument("--proto", required=True, help="vbase.proto to annotate in place")
    ap.add_argument("--go-src", required=True, help="Go source root to scan for used getters")
    args = ap.parse_args()
    run(proto_path=args.proto, go_src_root=args.go_src)
```

(Adapt the script's existing top-level flow into a `run(proto_path, go_src_root)` function; do not change its analysis or annotation logic. Known, accepted limitation recorded in the spec: it only counts getter-style accesses, so Golbat's direct field reads make it over-annotate — the harness readers keep measurements honest because they exercise the true access set.)

- [ ] **Step 3: Write gen.sh**

```bash
cat > protobench/scripts/gen.sh <<'EOF'
#!/bin/bash
# Regenerates protobench/pogo from vbase.proto at API_HYBRID with lazy
# annotations. One command; rerun whenever vbase.proto updates.
set -euo pipefail
cd "$(dirname "$0")/.."

PROTO_SRC="${PROTO_SRC:-$HOME/dev/ProtoMirror/vbase.proto}"
GOLBAT_ROOT="$(cd .. && pwd)"

command -v protoc >/dev/null || { echo "protoc not found (brew install protobuf)" >&2; exit 1; }
[ -f "$PROTO_SRC" ] || { echo "proto source not found: $PROTO_SRC (set PROTO_SRC)" >&2; exit 1; }

mkdir -p bin build pogo
GOBIN="$(pwd)/bin" go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11

cp "$PROTO_SRC" build/vbase.proto
python3 scripts/add_lazy_proto.py --proto build/vbase.proto --go-src "$GOLBAT_ROOT"
echo "lazy annotations: $(grep -c 'lazy = true' build/vbase.proto)"

PATH="$(pwd)/bin:$PATH" protoc -I build \
  --go_out=pogo --go_opt=paths=source_relative \
  --go_opt=default_api_level=API_HYBRID \
  --go_opt=Mvbase.proto=protobench/pogo \
  vbase.proto

go mod tidy
echo "generated: $(ls -la pogo/)"
EOF
chmod +x protobench/scripts/gen.sh
```

- [ ] **Step 4: Run generation**

Run: `protobench/scripts/gen.sh`
Expected: prints a nonzero lazy-annotation count, then generates `pogo/vbase.pb.go` (`//go:build !protoopaque`) and `pogo/vbase_protoopaque.pb.go` (`//go:build protoopaque`). Verify:

```bash
head -3 protobench/pogo/vbase_protoopaque.pb.go   # shows //go:build protoopaque
grep -c "func (x \*GetMapObjectsOutProto) GetMapCell" protobench/pogo/*.pb.go   # getter in both files
```

- [ ] **Step 5: Write the round-trip smoke test (fails before generation, passes after)**

```go
// protobench/roundtrip_test.go
package protobench_test

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"protobench/pogo"
)

// Builders + getters compile in BOTH build modes (default and
// -tags protoopaque); this is the hybrid-API contract the harness relies on.
func TestRoundTrip(t *testing.T) {
	wild := pogo.WildPokemonProto_builder{
		EncounterId:  7,
		SpawnPointId: "ABCD",
		Pokemon:      pogo.PokemonProto_builder{Cp: 500}.Build(),
	}.Build()
	raw, err := proto.Marshal(wild)
	if err != nil {
		t.Fatal(err)
	}
	var back pogo.WildPokemonProto
	if err := proto.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.GetEncounterId() != 7 || back.GetPokemon().GetCp() != 500 {
		t.Fatalf("round trip mismatch: id=%d cp=%d", back.GetEncounterId(), back.GetPokemon().GetCp())
	}
}
```

If builder field types disagree with the compiler (implicit- vs explicit-presence fields take values vs pointers), fix the test to match the generated builder struct — the generated code is the source of truth.

- [ ] **Step 6: Test both build modes**

Run: `cd protobench && go test ./... && go test -tags protoopaque ./... && cd ..`
Expected: PASS twice.

- [ ] **Step 7: Commit (scripts and scaffold only — generated code is ignored)**

```bash
git add protobench/go.mod protobench/go.sum protobench/.gitignore protobench/scripts/ protobench/roundtrip_test.go
git commit -m "feat: protobench module with hybrid+lazy codegen pipeline"
```

---

### Task 4: Corpus loader

**Files:**
- Create: `protobench/corpus/corpus.go`
- Test: `protobench/corpus/corpus_test.go`

**Interfaces:**
- Produces: `corpus.Payload{Method string; Path string; Data []byte}` and `corpus.Load(dir string) (map[string][]corpus.Payload, error)` — method name (= subdirectory name) → payloads. Missing dir returns an error; empty dir returns an empty map; non-`.bin` files are skipped.

- [ ] **Step 1: Write the failing test**

```go
// protobench/corpus/corpus_test.go
package corpus

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPartialCorpus(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "ENCOUNTER"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ENCOUNTER", "1_4.bin"), []byte{1, 2, 3, 4}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ENCOUNTER", "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stray.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err) // top-level file, not in a method dir: ignored
	}

	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got["ENCOUNTER"]) != 1 {
		t.Fatalf("got %v", got)
	}
	p := got["ENCOUNTER"][0]
	if p.Method != "ENCOUNTER" || len(p.Data) != 4 {
		t.Fatalf("payload = %+v", p)
	}
}

func TestLoadEmptyAndMissing(t *testing.T) {
	empty := t.TempDir()
	got, err := Load(empty)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty dir: got %v, err %v", got, err)
	}
	if _, err := Load(filepath.Join(empty, "nope")); err == nil {
		t.Fatal("missing dir: want error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd protobench && go test ./corpus/ -v && cd ..`
Expected: FAIL — `undefined: Load`.

- [ ] **Step 3: Write the implementation**

```go
// protobench/corpus/corpus.go

// Package corpus loads captured raw proto payloads written by Golbat's
// raw_capture worker: <dir>/<METHOD>/<unixMilli>_<sizeBytes>.bin.
// The corpus grows incrementally; whatever exists is what gets benchmarked.
package corpus

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Payload struct {
	Method string
	Path   string
	Data   []byte
}

func Load(dir string) (map[string][]Payload, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("corpus dir: %w", err)
	}
	out := make(map[string][]Payload)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		method := e.Name()
		files, err := os.ReadDir(filepath.Join(dir, method))
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".bin") {
				continue
			}
			path := filepath.Join(dir, method, f.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			out[method] = append(out[method], Payload{Method: method, Path: path, Data: data})
		}
	}
	return out, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd protobench && go test ./corpus/ -v && go test -tags protoopaque ./corpus/ && cd ..`
Expected: PASS in both modes.

- [ ] **Step 5: Commit**

```bash
cd protobench && gofmt -l corpus/ && cd ..   # must print nothing
git add protobench/corpus/
git commit -m "feat: protobench corpus loader (partial corpus tolerant)"
```

---

### Task 5: Reader functions (access simulation)

**Files:**
- Create: `protobench/readers/readers.go`
- Test: `protobench/readers/readers_test.go`

**Interfaces:**
- Consumes: `protobench/pogo` (generated, Task 3).
- Produces: `readers.Reader = func(payload []byte, o proto.UnmarshalOptions) error`; `readers.Registry map[string]Reader` keyed by capture method dir name (`"GET_MAP_OBJECTS"`, `"ENCOUNTER"`); `readers.Sink atomic.Int64` (dead-code-elimination defeat).

Readers mirror the fields Golbat's decode path actually touches (verified against `decoder/gmo_decode.go`, `decoder/pokemon_decode.go`, fort/weather decode). Extending to more methods later = add a function + registry entry.

- [ ] **Step 1: Write the failing test**

```go
// protobench/readers/readers_test.go
package readers

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"protobench/pogo"
)

func TestReadGMO(t *testing.T) {
	wild := pogo.WildPokemonProto_builder{
		EncounterId:  7,
		SpawnPointId: "ABCD",
		Pokemon:      pogo.PokemonProto_builder{Cp: 500, IndividualAttack: 15}.Build(),
	}.Build()
	fort := pogo.PokemonFortProto_builder{FortId: "fort.1", Latitude: 51.5, Longitude: -0.1}.Build()
	cell := pogo.ClientMapCellProto_builder{
		S2CellId:    123,
		Fort:        []*pogo.PokemonFortProto{fort},
		WildPokemon: []*pogo.WildPokemonProto{wild},
	}.Build()
	gmo := pogo.GetMapObjectsOutProto_builder{MapCell: []*pogo.ClientMapCellProto{cell}}.Build()
	raw, err := proto.Marshal(gmo)
	if err != nil {
		t.Fatal(err)
	}

	before := Sink.Load()
	if err := Registry["GET_MAP_OBJECTS"](raw, proto.UnmarshalOptions{}); err != nil {
		t.Fatal(err)
	}
	if Sink.Load() == before {
		t.Fatal("sink unchanged — reader accessed nothing")
	}
}

func TestReadEncounter(t *testing.T) {
	enc := pogo.EncounterOutProto_builder{
		Pokemon: pogo.WildPokemonProto_builder{EncounterId: 9}.Build(),
	}.Build()
	raw, err := proto.Marshal(enc)
	if err != nil {
		t.Fatal(err)
	}
	before := Sink.Load()
	if err := Registry["ENCOUNTER"](raw, proto.UnmarshalOptions{}); err != nil {
		t.Fatal(err)
	}
	if Sink.Load() == before {
		t.Fatal("sink unchanged")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd protobench && go test ./readers/ -v && cd ..`
Expected: FAIL — `undefined: Sink` / `undefined: Registry`.

- [ ] **Step 3: Write the implementation**

```go
// protobench/readers/readers.go

// Package readers simulates Golbat's per-method field accesses: decode a raw
// payload, read the same subtrees Golbat's decode path reads, drop the
// message. Every read folds into Sink so the compiler cannot eliminate the
// accesses. Access sets mirror decoder/gmo_decode.go and friends; keep them
// in sync when Golbat starts reading new fields.
package readers

import (
	"sync/atomic"

	"google.golang.org/protobuf/proto"

	"protobench/pogo"
)

var Sink atomic.Int64

type Reader func(payload []byte, o proto.UnmarshalOptions) error

var Registry = map[string]Reader{
	"GET_MAP_OBJECTS": ReadGMO,
	"ENCOUNTER":       ReadEncounter,
}

func readDisplay(d *pogo.PokemonDisplayProto) int64 {
	if d == nil {
		return 0
	}
	acc := int64(d.GetForm()) + int64(d.GetCostume()) + int64(d.GetGender()) +
		int64(d.GetWeatherBoostedCondition()) + int64(d.GetAlignment())
	if d.GetShiny() {
		acc++
	}
	return acc
}

func readPokemon(p *pogo.PokemonProto) int64 {
	if p == nil {
		return 0
	}
	acc := int64(p.GetPokemonId()) + int64(p.GetCp()) + int64(p.GetStamina()) +
		int64(p.GetMaxStamina()) + int64(p.GetMove1()) + int64(p.GetMove2()) +
		int64(p.GetIndividualAttack()) + int64(p.GetIndividualDefense()) +
		int64(p.GetIndividualStamina()) + int64(p.GetSize()) +
		int64(p.GetHeightM()*100) + int64(p.GetWeightKg()*100) +
		int64(p.GetCpMultiplier()*1000)
	return acc + readDisplay(p.GetPokemonDisplay())
}

func readWild(w *pogo.WildPokemonProto) int64 {
	if w == nil {
		return 0
	}
	return int64(w.GetEncounterId()) + int64(len(w.GetSpawnPointId())) +
		int64(w.GetTimeTillHiddenMs()) + w.GetLastModifiedMs() +
		int64(w.GetLatitude()*1e5) + int64(w.GetLongitude()*1e5) +
		readPokemon(w.GetPokemon())
}

func readFort(f *pogo.PokemonFortProto) int64 {
	if f == nil {
		return 0
	}
	acc := int64(len(f.GetFortId())) + int64(f.GetLatitude()*1e5) +
		int64(f.GetLongitude()*1e5) + f.GetLastModifiedMs() +
		int64(f.GetTeam()) + int64(f.GetGuardPokemonId()) +
		int64(f.GetFortType()) + f.GetCooldownCompleteMs() +
		int64(f.GetPowerUpProgressPoints()) + f.GetPowerUpLevelExpirationMs() +
		int64(len(f.GetActiveFortModifier())) + int64(len(f.GetPartnerId()))
	if f.GetEnabled() {
		acc++
	}
	if ri := f.GetRaidInfo(); ri != nil {
		acc += ri.GetRaidSpawnMs() + ri.GetRaidBattleMs() + ri.GetRaidEndMs() +
			int64(ri.GetRaidLevel()) + readPokemon(ri.GetRaidPokemon())
	}
	if gd := f.GetGymDisplay(); gd != nil {
		acc += int64(gd.GetTotalGymCp()) + int64(gd.GetSlotsAvailable()) + gd.GetOccupiedMillis()
	}
	for _, pd := range f.GetPokestopDisplays() {
		acc += int64(len(pd.GetIncidentId())) + pd.GetIncidentStartMs() +
			pd.GetIncidentExpirationMs() + int64(pd.GetIncidentDisplayType())
	}
	if pd := f.GetPokestopDisplay(); pd != nil {
		acc += int64(len(pd.GetIncidentId()))
	}
	return acc
}

func readWeather(cw *pogo.ClientWeatherProto) int64 {
	if cw == nil {
		return 0
	}
	acc := cw.GetS2CellId()
	if gw := cw.GetGameplayWeather(); gw != nil {
		acc += int64(gw.GetGameplayCondition())
	}
	if dw := cw.GetDisplayWeather(); dw != nil {
		acc += int64(dw.GetCloudLevel()) + int64(dw.GetRainLevel()) +
			int64(dw.GetWindLevel()) + int64(dw.GetSnowLevel()) +
			int64(dw.GetFogLevel()) + int64(dw.GetWindDirection())
	}
	return acc + int64(len(cw.GetAlerts()))
}

func readStation(s *pogo.StationProto) int64 {
	if s == nil {
		return 0
	}
	acc := int64(len(s.GetId())) + int64(len(s.GetName())) +
		int64(s.GetLat()*1e5) + int64(s.GetLng()*1e5) +
		s.GetStartTimeMs() + s.GetEndTimeMs() + s.GetCooldownCompleteMs()
	if s.GetIsBreadBattleAvailable() {
		acc++
	}
	return acc
}

func ReadGMO(payload []byte, o proto.UnmarshalOptions) error {
	var gmo pogo.GetMapObjectsOutProto
	if err := o.Unmarshal(payload, &gmo); err != nil {
		return err
	}
	var acc int64
	for _, cell := range gmo.GetMapCell() {
		acc += int64(cell.GetS2CellId()) + cell.GetAsOfTimeMs()
		for _, f := range cell.GetFort() {
			acc += readFort(f)
		}
		for _, w := range cell.GetWildPokemon() {
			acc += readWild(w)
		}
		for _, n := range cell.GetNearbyPokemon() {
			acc += int64(n.GetPokedexNumber()) + int64(n.GetEncounterId()) +
				int64(len(n.GetFortId())) + readDisplay(n.GetPokemonDisplay())
		}
		for _, m := range cell.GetCatchablePokemon() {
			acc += int64(m.GetEncounterId()) + int64(m.GetPokedexTypeId()) +
				m.GetExpirationTimeMs() + int64(len(m.GetSpawnpointId())) +
				readDisplay(m.GetPokemonDisplay())
		}
		for _, s := range cell.GetStations() {
			acc += readStation(s)
		}
	}
	for _, cw := range gmo.GetClientWeather() {
		acc += readWeather(cw)
	}
	Sink.Add(acc)
	return nil
}

func ReadEncounter(payload []byte, o proto.UnmarshalOptions) error {
	var e pogo.EncounterOutProto
	if err := o.Unmarshal(payload, &e); err != nil {
		return err
	}
	acc := int64(e.GetStatus()) + readWild(e.GetPokemon())
	if cp := e.GetCaptureProbability(); cp != nil {
		for _, p := range cp.GetCaptureProbability() {
			acc += int64(p * 1000)
		}
	}
	Sink.Add(acc)
	return nil
}
```

(If any getter name fails to compile, check the generated `protobench/pogo/vbase.pb.go` for the correct name — the generated code is the source of truth; field names were verified against Golbat's `pogo/vbase.pb.go` at plan time.)

- [ ] **Step 4: Run tests in both build modes**

Run: `cd protobench && go test ./readers/ -v && go test -tags protoopaque ./readers/ -v && cd ..`
Expected: PASS in both modes. The `-tags protoopaque` run exercises lazy decoding (annotations are active by default in opaque mode).

- [ ] **Step 5: Commit**

```bash
cd protobench && gofmt -l readers/ && cd ..   # must print nothing
git add protobench/readers/
git commit -m "feat: protobench GMO/Encounter access-simulation readers"
```

---

### Task 6: Microbenchmarks

**Files:**
- Create: `protobench/bench/bench_test.go`

**Interfaces:**
- Consumes: `corpus.Load`, `readers.Registry`, `readers.Sink`.
- Configuration: env `PROTOBENCH_CORPUS` (default `../../capture`), env `PROTOBENCH_NOLAZY=1` → `proto.UnmarshalOptions{NoLazyDecoding: true}`.

- [ ] **Step 1: Write the benchmark**

```go
// protobench/bench/bench_test.go
package bench

import (
	"os"
	"sort"
	"testing"

	"google.golang.org/protobuf/proto"

	"protobench/corpus"
	"protobench/readers"
)

func unmarshalOpts() proto.UnmarshalOptions {
	if os.Getenv("PROTOBENCH_NOLAZY") != "" {
		return proto.UnmarshalOptions{NoLazyDecoding: true}
	}
	return proto.UnmarshalOptions{}
}

func corpusDir() string {
	if d := os.Getenv("PROTOBENCH_CORPUS"); d != "" {
		return d
	}
	return "../../capture"
}

func BenchmarkDecode(b *testing.B) {
	byMethod, err := corpus.Load(corpusDir())
	if err != nil {
		b.Skipf("no corpus at %s: %v (set PROTOBENCH_CORPUS)", corpusDir(), err)
	}
	o := unmarshalOpts()
	methods := make([]string, 0, len(byMethod))
	for m := range byMethod {
		if _, ok := readers.Registry[m]; ok {
			methods = append(methods, m)
		}
	}
	sort.Strings(methods)
	if len(methods) == 0 {
		b.Skip("corpus has no methods with readers yet")
	}
	for _, method := range methods {
		payloads := byMethod[method]
		reader := readers.Registry[method]
		var totalBytes int64
		for _, p := range payloads {
			totalBytes += int64(len(p.Data))
		}
		b.Run(method, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(totalBytes / int64(len(payloads)))
			for i := 0; i < b.N; i++ {
				p := payloads[i%len(payloads)]
				if err := reader(p.Data, o); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	b.Log("sink:", readers.Sink.Load())
}
```

- [ ] **Step 2: Verify skip-behavior without corpus**

Run: `cd protobench && go test ./bench/ -bench=Decode -benchmem && cd ..`
Expected: `--- SKIP: BenchmarkDecode` mentioning the corpus path (or sub-benchmarks running, if a corpus already exists locally). No failure either way.

- [ ] **Step 3: Verify against a synthetic corpus**

```bash
cd protobench
mkdir -p /tmp/pbcorpus/GET_MAP_OBJECTS
go test ./readers/ -run TestReadGMO -v   # sanity: readers still pass
cat > /tmp/gen_corpus_test_payload.go <<'EOF'
//go:build ignore
package main

import (
	"os"

	"google.golang.org/protobuf/proto"

	"protobench/pogo"
)

func main() {
	wild := pogo.WildPokemonProto_builder{EncounterId: 7,
		Pokemon: pogo.PokemonProto_builder{Cp: 500}.Build()}.Build()
	cell := pogo.ClientMapCellProto_builder{S2CellId: 123,
		WildPokemon: []*pogo.WildPokemonProto{wild}}.Build()
	gmo := pogo.GetMapObjectsOutProto_builder{MapCell: []*pogo.ClientMapCellProto{cell}}.Build()
	raw, err := proto.Marshal(gmo)
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile("/tmp/pbcorpus/GET_MAP_OBJECTS/1_100.bin", raw, 0o644); err != nil {
		panic(err)
	}
}
EOF
go run /tmp/gen_corpus_test_payload.go
PROTOBENCH_CORPUS=/tmp/pbcorpus go test ./bench/ -bench=Decode -benchmem
PROTOBENCH_CORPUS=/tmp/pbcorpus go test -tags protoopaque ./bench/ -bench=Decode -benchmem
PROTOBENCH_CORPUS=/tmp/pbcorpus PROTOBENCH_NOLAZY=1 go test -tags protoopaque ./bench/ -bench=Decode -benchmem
rm /tmp/gen_corpus_test_payload.go
cd ..
```

Expected: `BenchmarkDecode/GET_MAP_OBJECTS` reports ns/op, B/op, allocs/op in all three configurations.

- [ ] **Step 4: Commit**

```bash
cd protobench && gofmt -l bench/ && cd ..   # must print nothing
git add protobench/bench/
git commit -m "feat: protobench per-method decode microbenchmarks"
```

---

### Task 7: Volume runner (cmd/bench)

**Files:**
- Create: `protobench/cmd/bench/main.go`
- Create: `protobench/cmd/bench/buildmode_open.go`
- Create: `protobench/cmd/bench/buildmode_opaque.go`
- Test: `protobench/cmd/bench/main_test.go`

**Interfaces:**
- Consumes: `corpus.Load`, `readers.Registry`.
- Produces: binary flags `-corpus` (default `../capture`), `-workers` (default 96), `-duration` (default 60s), `-nolazy`, `-ballast-mb` (default 0), `-methods` (CSV filter). Prints a metrics report; exits nonzero on empty/usable corpus.
- Produces (internal, tested): `run(cfg runConfig) (report, error)`.

- [ ] **Step 1: Build-mode marker files**

```go
// protobench/cmd/bench/buildmode_open.go
//go:build !protoopaque

package main

const buildMode = "open"
```

```go
// protobench/cmd/bench/buildmode_opaque.go
//go:build protoopaque

package main

const buildMode = "opaque"
```

- [ ] **Step 2: Write the failing test**

```go
// protobench/cmd/bench/main_test.go
package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"protobench/pogo"
)

func TestRunSmoke(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "ENCOUNTER"), 0o755); err != nil {
		t.Fatal(err)
	}
	enc := pogo.EncounterOutProto_builder{
		Pokemon: pogo.WildPokemonProto_builder{EncounterId: 9}.Build(),
	}.Build()
	raw, err := proto.Marshal(enc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ENCOUNTER", "1_10.bin"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := run(runConfig{
		corpusDir: dir,
		workers:   4,
		duration:  200 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.decodes == 0 {
		t.Fatal("no decodes performed")
	}
	if rep.gcCPUShare < 0 || rep.gcCPUShare > 1 {
		t.Fatalf("gcCPUShare out of range: %v", rep.gcCPUShare)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd protobench && go test ./cmd/bench/ -v && cd ..`
Expected: FAIL — `undefined: run` / `undefined: runConfig`.

- [ ] **Step 4: Write the implementation**

```go
// protobench/cmd/bench/main.go

// Sustained decode-at-volume runner: N workers decode corpus payloads in a
// loop (decode -> read Golbat's field set -> drop), reporting throughput and
// GC metrics. Run once per configuration and compare:
//   open:         go run ./cmd/bench
//   opaque+lazy:  go run -tags protoopaque ./cmd/bench
//   opaque-lazy:  go run -tags protoopaque ./cmd/bench -nolazy
package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"runtime/metrics"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	"protobench/corpus"
	"protobench/readers"
)

type runConfig struct {
	corpusDir string
	workers   int
	duration  time.Duration
	nolazy    bool
	ballastMB int
	methods   string // CSV filter; empty = all with readers
}

type report struct {
	decodes    uint64
	bytes      uint64
	perMethod  map[string]uint64
	elapsed    time.Duration
	gcCPUShare float64
	allocBytes uint64
	allocObjs  uint64
	gcCycles   uint64
	pauseP50   time.Duration
	pauseP99   time.Duration
}

type ballastNode struct {
	next *ballastNode
	_    [48]byte
}

// buildBallast allocates a pointer-dense linked structure approximating
// Golbat's live caches (GC mark cost scales with live pointerful heap).
func buildBallast(mb int) *ballastNode {
	if mb <= 0 {
		return nil
	}
	n := mb * (1 << 20) / 64
	var head *ballastNode
	for i := 0; i < n; i++ {
		head = &ballastNode{next: head}
	}
	return head
}

var metricNames = []string{
	"/cpu/classes/gc/total:cpu-seconds",
	"/cpu/classes/total:cpu-seconds",
	"/gc/heap/allocs:bytes",
	"/gc/heap/allocs:objects",
	"/gc/cycles/total:gc-cycles",
	"/sched/pauses/total/gc:seconds",
}

// histPercentile computes a percentile from the delta of two cumulative
// pause histograms (runtime/metrics Float64Histogram).
func histPercentile(before, after *metrics.Float64Histogram, q float64) time.Duration {
	if before == nil || after == nil {
		return 0
	}
	var total uint64
	deltas := make([]uint64, len(after.Counts))
	for i := range after.Counts {
		d := after.Counts[i]
		if i < len(before.Counts) {
			d -= before.Counts[i]
		}
		deltas[i] = d
		total += d
	}
	if total == 0 {
		return 0
	}
	target := uint64(q * float64(total))
	var cum uint64
	for i, d := range deltas {
		cum += d
		if cum >= target {
			// Buckets has len(Counts)+1 boundaries; use the bucket's upper edge.
			return time.Duration(after.Buckets[i+1] * float64(time.Second))
		}
	}
	return time.Duration(after.Buckets[len(after.Buckets)-1] * float64(time.Second))
}

func readMetrics() map[string]metrics.Value {
	samples := make([]metrics.Sample, len(metricNames))
	for i, n := range metricNames {
		samples[i].Name = n
	}
	metrics.Read(samples)
	out := make(map[string]metrics.Value, len(samples))
	for _, s := range samples {
		out[s.Name] = s.Value
	}
	return out
}

func run(cfg runConfig) (report, error) {
	byMethod, err := corpus.Load(cfg.corpusDir)
	if err != nil {
		return report{}, err
	}
	filter := map[string]bool{}
	for _, m := range strings.Split(cfg.methods, ",") {
		if m = strings.TrimSpace(m); m != "" {
			filter[m] = true
		}
	}
	type item struct {
		data   []byte
		reader readers.Reader
		method string
	}
	var items []item
	perMethodIdx := map[string]int{}
	var methodNames []string
	for method, payloads := range byMethod {
		reader, ok := readers.Registry[method]
		if !ok || (len(filter) > 0 && !filter[method]) {
			continue
		}
		if _, seen := perMethodIdx[method]; !seen {
			perMethodIdx[method] = len(methodNames)
			methodNames = append(methodNames, method)
		}
		for _, p := range payloads {
			items = append(items, item{data: p.Data, reader: reader, method: method})
		}
	}
	if len(items) == 0 {
		return report{}, fmt.Errorf("corpus at %s has no payloads with readers (have readers: GET_MAP_OBJECTS, ENCOUNTER)", cfg.corpusDir)
	}

	o := proto.UnmarshalOptions{NoLazyDecoding: cfg.nolazy}
	ballast := buildBallast(cfg.ballastMB)

	perMethod := make([]atomic.Uint64, len(methodNames))
	var decodes, bytes atomic.Uint64
	deadline := time.Now().Add(cfg.duration)
	before := readMetrics()
	start := time.Now()

	var wg sync.WaitGroup
	for w := 0; w < cfg.workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for time.Now().Before(deadline) {
				it := items[rng.Intn(len(items))]
				if err := it.reader(it.data, o); err != nil {
					panic(fmt.Sprintf("decode %s: %v", it.method, err))
				}
				decodes.Add(1)
				bytes.Add(uint64(len(it.data)))
				perMethod[perMethodIdx[it.method]].Add(1)
			}
		}(int64(w) + 1)
	}
	wg.Wait()
	elapsed := time.Since(start)
	after := readMetrics()
	runtime.KeepAlive(ballast)

	f64 := func(m map[string]metrics.Value, k string) float64 { return m[k].Float64() }
	u64 := func(m map[string]metrics.Value, k string) uint64 { return m[k].Uint64() }
	gcCPU := f64(after, "/cpu/classes/gc/total:cpu-seconds") - f64(before, "/cpu/classes/gc/total:cpu-seconds")
	totCPU := f64(after, "/cpu/classes/total:cpu-seconds") - f64(before, "/cpu/classes/total:cpu-seconds")
	rep := report{
		decodes:    decodes.Load(),
		bytes:      bytes.Load(),
		perMethod:  map[string]uint64{},
		elapsed:    elapsed,
		allocBytes: u64(after, "/gc/heap/allocs:bytes") - u64(before, "/gc/heap/allocs:bytes"),
		allocObjs:  u64(after, "/gc/heap/allocs:objects") - u64(before, "/gc/heap/allocs:objects"),
		gcCycles:   u64(after, "/gc/cycles/total:gc-cycles") - u64(before, "/gc/cycles/total:gc-cycles"),
	}
	if totCPU > 0 {
		rep.gcCPUShare = gcCPU / totCPU
	}
	beforeH := before["/sched/pauses/total/gc:seconds"].Float64Histogram()
	afterH := after["/sched/pauses/total/gc:seconds"].Float64Histogram()
	rep.pauseP50 = histPercentile(beforeH, afterH, 0.50)
	rep.pauseP99 = histPercentile(beforeH, afterH, 0.99)
	for i, m := range methodNames {
		rep.perMethod[m] = perMethod[i].Load()
	}
	return rep, nil
}

func main() {
	cfg := runConfig{}
	flag.StringVar(&cfg.corpusDir, "corpus", "../capture", "corpus directory")
	flag.IntVar(&cfg.workers, "workers", 96, "concurrent decode workers (matches raw_processing_concurrency)")
	flag.DurationVar(&cfg.duration, "duration", 60*time.Second, "run duration")
	flag.BoolVar(&cfg.nolazy, "nolazy", false, "NoLazyDecoding (only meaningful with -tags protoopaque)")
	flag.IntVar(&cfg.ballastMB, "ballast-mb", 0, "pointer-dense live-heap ballast")
	flag.StringVar(&cfg.methods, "methods", "", "CSV method filter")
	flag.Parse()

	rep, err := run(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	lazyState := "n/a (open build)"
	if buildMode == "opaque" {
		if cfg.nolazy {
			lazyState = "disabled"
		} else {
			lazyState = "enabled"
		}
	}
	fmt.Printf("build=%s lazy=%s workers=%d duration=%s ballast=%dMB\n",
		buildMode, lazyState, cfg.workers, rep.elapsed.Round(time.Millisecond), cfg.ballastMB)
	fmt.Printf("decodes:      %d (%.0f/s)\n", rep.decodes, float64(rep.decodes)/rep.elapsed.Seconds())
	fmt.Printf("throughput:   %.1f MB/s\n", float64(rep.bytes)/1e6/rep.elapsed.Seconds())
	fmt.Printf("alloc/decode: %.0f B, %.1f objects\n",
		float64(rep.allocBytes)/float64(rep.decodes), float64(rep.allocObjs)/float64(rep.decodes))
	fmt.Printf("GC:           cpu-share=%.1f%% cycles=%d pause-p50=%s pause-p99=%s\n",
		rep.gcCPUShare*100, rep.gcCycles, rep.pauseP50, rep.pauseP99)
	methods := make([]string, 0, len(rep.perMethod))
	for m := range rep.perMethod {
		methods = append(methods, m)
	}
	sort.Strings(methods)
	for _, m := range methods {
		fmt.Printf("  %-24s %d\n", m, rep.perMethod[m])
	}
}
```

- [ ] **Step 5: Run tests in both build modes**

Run: `cd protobench && go test ./cmd/bench/ -v && go test -tags protoopaque ./cmd/bench/ -v && cd ..`
Expected: PASS in both modes (`TestRunSmoke` decodes for 200ms with 4 workers).

- [ ] **Step 6: Manual run against the synthetic corpus from Task 6**

Run: `cd protobench && go run ./cmd/bench -corpus /tmp/pbcorpus -workers 8 -duration 2s && go run -tags protoopaque ./cmd/bench -corpus /tmp/pbcorpus -workers 8 -duration 2s && cd ..`
Expected: two reports; first line shows `build=open lazy=n/a` then `build=opaque lazy=enabled`; nonzero decodes/s in both.

- [ ] **Step 7: Commit**

```bash
cd protobench && gofmt -l cmd/ && cd ..   # must print nothing
git add protobench/cmd/
git commit -m "feat: protobench sustained decode-at-volume runner with GC metrics"
```

---

### Task 8: README + full verification sweep

**Files:**
- Create: `protobench/README.md`
- Modify: `CLAUDE.md` (one paragraph pointing at capture + protobench)

- [ ] **Step 1: Write the README**

```markdown
# protobench — decode-at-volume harness (Phase 0)

Standalone module proving the opaque/lazy proto decode methodology before any
Golbat migration. See docs/superpowers/specs/2026-07-05-proto-decoding-gc-design.md.
Separate module on purpose: a second registration of vbase.proto inside the
Golbat binary would panic the proto registry.

## Corpus

Enable capture on a production Golbat (`config.toml`):

    [raw_capture]
    enabled = true            # payloads land in capture/<METHOD>/<ts>_<size>.bin

Copy payloads here as they accumulate (the harness uses whatever exists):

    rsync -a prod:/path/to/golbat/capture/ ../capture/

## Generate the proto package (once, and after each vbase.proto update)

    PROTO_SRC=~/dev/ProtoMirror/vbase.proto scripts/gen.sh

## The three configurations

| Configuration | Command |
|---------------|---------|
| (a) open (current Golbat) | `go run ./cmd/bench` |
| (b) opaque + lazy         | `go run -tags protoopaque ./cmd/bench` |
| (c) opaque, no lazy       | `go run -tags protoopaque ./cmd/bench -nolazy` |

Useful flags: `-corpus ../capture -workers 96 -duration 60s -ballast-mb 2048`.
Run all three back-to-back on an idle machine; compare `alloc/decode`,
`decodes/s`, and `GC cpu-share`.

Microbenchmarks (per-method ns/op, B/op, allocs/op):

    go test ./bench/ -bench=Decode -benchmem
    go test -tags protoopaque ./bench/ -bench=Decode -benchmem
    PROTOBENCH_NOLAZY=1 go test -tags protoopaque ./bench/ -bench=Decode -benchmem

## Phase 0 exit gate (from the spec)

(b) or (c) must beat (a) on allocation rate and GC CPU share at volume —
provisional target: ≥20% allocs/op reduction on GET_MAP_OBJECTS. If the gate
fails, the migration (Phase 1) does not happen.

## Extending

New method = capture dir appears automatically; add a reader in
readers/readers.go mirroring the fields Golbat's decoder reads and register
it in readers.Registry.
```

- [ ] **Step 2: CLAUDE.md pointer**

Add to `CLAUDE.md` at the end of the Raw Message Processing section:

```markdown
Raw payloads can be sampled to disk (`raw_capture` config, size-stratified
per method) to feed `protobench/` — a standalone decode benchmarking module
(own go.mod; see protobench/README.md and
docs/superpowers/specs/2026-07-05-proto-decoding-gc-design.md).
```

- [ ] **Step 3: Full verification sweep**

```bash
go build ./... && go test ./...                     # root module
cd protobench
go vet ./... && go vet -tags protoopaque ./...
go test ./... && go test -tags protoopaque ./...
cd ..
```

Expected: everything passes; root module untouched by protobench.

- [ ] **Step 4: Commit**

```bash
git add protobench/README.md CLAUDE.md
git commit -m "docs: protobench README and capture pointers"
```

---

## Execution Notes

- Tasks 1-2 (Golbat capture) and Tasks 3-7 (protobench) are independent
  streams; Task 8 needs both. Within each stream, order is strict.
- Task 3 requires network access (`go install protoc-gen-go`), a protoc
  binary, and `~/dev/ProtoMirror/vbase.proto` on this machine.
- Generated `protobench/pogo/` is required by Tasks 4-8 — they cannot run
  before Task 3's generation step has succeeded locally.
- Builder/getter names in Tasks 5-7 were verified against Golbat's current
  `pogo/vbase.pb.go`; if the freshly generated package differs (newer proto
  version), trust the compiler and adjust reads accordingly.
- The spec's Phase 0 item 3 (prod baseline: pprof allocs/heap/CPU profiles at
  peak, via the existing authed `/debug/pprof/` endpoints) is an operational
  step the user runs on production — it is intentionally not a task in this
  plan.
