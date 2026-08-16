# PokemonData Struct Packing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Shrink the cached `Pokemon` entity from 800 bytes to roughly 410 so it falls under Go's 512-byte GC threshold, cutting about 480 bytes of resident memory per cached pokemon.

**Architecture:** Replace `guregu/null`'s 16-byte numeric wrappers with narrow equivalents sized to the actual database columns, in a new `decoder/nulltypes` package. Each type implements `sql.Scanner` and `driver.Valuer`, so sqlx keeps binding them with no shim, and mirrors `guregu/null`'s `Valid` / `ValueOrZero()` / `Ptr()` / JSON surface so most call sites keep compiling. Reorder `PokemonData`'s fields by descending alignment, and delete three fields that cost memory without earning it.

**Tech Stack:** Go 1.26, `sqlx`, `guregu/null/v6` (retained for string fields and setter signatures), MariaDB, standard library `testing`.

**Design doc:** `docs/superpowers/specs/2026-08-16-pokemon-struct-packing-design.md`

## Global Constraints

- **Build tag is mandatory:** every build and test command needs `-tags go_json`. A bare `go build ./...` does not reflect what ships.
- **Never run a bare `go test ./...` sweep.** Use targeted packages: `go test -tags go_json ./decoder/ ./decoder/nulltypes/`.
- **`golangci-lint run` must be clean** before every commit. CI enforces it, staticcheck included.
- **Do not change the database schema.** Every narrowed Go type matches a column width that already exists. If you find one that does not, stop and report it rather than adding a migration.
- **Verify column types against `sql/*.up.sql`, never against the block comment at `decoder/pokemon.go:88-140`.** That comment has three known-stale claims: `iv` is not a generated column (`sql/11_ivchanges.up.sql` replaced it), `seen_type` has eight values not four (`sql/45_tappables_seen_type_lure.up.sql`), and `size` is `tinyint unsigned` not `double` (`sql/7_add_height_size.up.sql` renamed the old double to `height`).
- **JSON output is a public contract.** The API responses (`api.md`) and webhooks (`webhooks.md`) are consumed by other people's software. A nullable field must marshal to `null`, never to `0`.
- **One deliberate JSON divergence, ruled on 2026-08-16.** `NullFloat32` formats at bitSize 32, where `guregu/null.Float` formats at 64. A weight of 6.7 emits `6.7` where it used to emit `6.699999809265137`. The values originate as protobuf `float` fields promoted through `float64()` (`decoder/pokemon_decode.go:749,751`), so the extra digits were promotion noise that never carried information. Every other type stays byte-identical to `guregu/null`.
- **Commit messages** end with `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>` only if an agent authored the commit.

---

## File Structure

**New files:**

| path | responsibility |
| --- | --- |
| `decoder/nulltypes/nulltypes.go` | The narrow nullable types and the shared driver-value normaliser. No Golbat dependencies. |
| `decoder/nulltypes/nulltypes_test.go` | Scan/Value/JSON round-trip tests, including comparison against `guregu/null`'s JSON output. |
| `decoder/entity_sizes_test.go` | Pins `unsafe.Sizeof` for `PokemonData` and `Pokemon`, and enforces the 512-byte ceiling. |
| `decoder/pokemon_nullscan_test.go` | Integration test: a row with every nullable column NULL survives a write-read cycle against a real MariaDB. Skipped when no test database is configured. |

**Modified files:**

| path | change |
| --- | --- |
| `decoder/pokemon.go` | `PokemonData` field types and ordering; 33 setters convert and range-check; drop `Capture1/2/3` and their setters; drop `changedFields`. |
| `decoder/pokemon_decode.go` | Direct field assignments become setter calls so range checking is not bypassed. |
| `decoder/pokemon_state.go` | Compiler-flagged width mismatches; `seenType` comparisons move from strings to codes; webhook payload sources `capture_*` from `0`. |
| `decoder/api_pokemon_response.go` | Compiler-flagged pointer-width mismatches; `SeenType` produces a `*string`. |
| `decoder/pokemonRtree.go` | Compiler-flagged width mismatches in `updatePokemonLookup`. |
| `decoder/stats.go`, `decoder/weather_iv.go`, `decoder/pokemon_process.go`, `decoder/pokemon_preserve.go`, `decoder/api_pokemon_scan_v2.go`, `decoder/api_pokemon_scan_v3.go` | Compiler-flagged width mismatches only. |
| `stats_collector/stats_collector.go`, `stats_collector/noop.go`, `stats_collector/prometheus.go` | New `IncFieldClamped(field string)` counter. |

---

## Task 0: Decide whether to do this at all

The design doc gates the whole effort on one measurement, and it takes about
thirty minutes. Do it first. If GC turns out to be a small share of CPU and the
live heap is well under RSS, then most of the resident memory is collector
headroom, `tuning.go_mem_limit_mib` is a one-line lever that beats this entire
plan on memory, and the packing is worth doing for CPU and headroom rather than
as a memory fix — which is a different priority.

**Files:** none. This task produces a decision, not a diff.

- [ ] **Step 1: Enable the profiling routes on a production instance**

Set `profile_routes = true` in `config.toml` and restart. The routes sit behind
`AuthRequired()` (`main.go:362-374`), so the API secret is still needed.

- [ ] **Step 2: Capture a CPU profile under normal load**

```bash
curl -s -H "X-Golbat-Secret: $SECRET" "http://localhost:9001/debug/pprof/profile?seconds=30" > before.cpu
go tool pprof -top -cum before.cpu | grep -E "gcDrain|scanobject|gcBgMarkWorker"
```

Record the cumulative percentage attributable to GC.

- [ ] **Step 3: Capture a heap profile and compare against RSS**

```bash
curl -s -H "X-Golbat-Secret: $SECRET" "http://localhost:9001/debug/pprof/heap" > before.heap
go tool pprof -top -sample_index=inuse_space before.heap | head -25
ps -o rss= -p "$(pgrep -f golbat)"
```

Record `inuse_space` total against RSS, and note how much of `inuse_space` is
`decoder.Pokemon` / `decoder.PokemonData`.

- [ ] **Step 4: Decide, and write the numbers down**

- **GC around 5% and live heap well under RSS** — the memory is mostly collector
  headroom. Set `tuning.go_mem_limit_mib` first and re-measure. Proceed with the
  packing for CPU and headroom, knowing the RSS claim will underdeliver.
- **Live heap close to RSS, pokemon a large share of it** — the packing is the
  right tool and the design's estimate holds. Proceed.
- **GC well above 5%** — proceed, and note that the arena option the design
  ruled out may deserve reopening afterwards.

Keep `before.cpu` and `before.heap`. Task 7 compares against them.

---

## Task 1: Pin the current entity sizes

This task builds the measuring instrument the rest of the plan uses. Every later task updates the expected numbers, which makes each task's memory effect explicit and reviewable in its own diff.

**Files:**
- Create: `decoder/entity_sizes_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `TestPokemonEntitySizes` — later tasks update the `wantPokemonData` and `wantPokemon` constants inside it.

- [ ] **Step 1: Write the test pinning today's sizes**

Create `decoder/entity_sizes_test.go`:

```go
package decoder

import (
	"testing"
	"unsafe"
)

// gcSizeThreshold is the object size above which Go's garbage collector gets
// materially more expensive. Measured across 5M live entries: a 512-byte object
// carrying one pointer marks in 62.6ms, a 520-byte one in 194.4ms.
//
// Pokemon is cached in the millions, so staying under this is the entire point
// of the packing work in
// docs/superpowers/specs/2026-08-16-pokemon-struct-packing-design.md.
const gcSizeThreshold = 512

// TestPokemonEntitySizes pins the in-memory footprint of the cached pokemon
// entity so that growth is a deliberate decision rather than an accident.
//
// If this fails because you added a field: do not simply update the constant.
// Go rounds every allocation up to a size class, so the cost of your field is
// not its own width but the distance to the next class boundary. The classes
// either side of Pokemon are 416, 448, 480 and 512 bytes. Check which one you
// landed in, and whether the field could be narrower or live somewhere else.
func TestPokemonEntitySizes(t *testing.T) {
	const (
		wantPokemonData = 592
		wantPokemon     = 800
	)

	if got := unsafe.Sizeof(PokemonData{}); got != wantPokemonData {
		t.Errorf("unsafe.Sizeof(PokemonData{}) = %d, want %d", got, wantPokemonData)
	}
	if got := unsafe.Sizeof(Pokemon{}); got != wantPokemon {
		t.Errorf("unsafe.Sizeof(Pokemon{}) = %d, want %d", got, wantPokemon)
	}
}
```

- [ ] **Step 2: Run the test and confirm it passes**

```bash
go test -tags go_json -run TestPokemonEntitySizes -v ./decoder/
```

Expected: PASS. If it fails, the struct has already changed since this plan was written — report the actual numbers before continuing, because every size in this plan derives from these two.

- [ ] **Step 3: Commit**

```bash
git add decoder/entity_sizes_test.go
git commit -m "test: pin Pokemon and PokemonData in-memory sizes"
```

---

## Task 2: The `nulltypes` package

This package is the compatibility contract for the entire change. It has no Golbat dependencies and lands alone so it can be reviewed and tested in isolation.

**Files:**
- Create: `decoder/nulltypes/nulltypes.go`
- Create: `decoder/nulltypes/nulltypes_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `nulltypes.NullUint8` / `NullUint16` / `NullUint32` (aliases of `NullUint[T]`), `NullUint64`, `NullFloat32`, `NullBool`
  - Constructors: `Uint8From(uint8) NullUint8`, `Uint16From(uint16) NullUint16`, `Uint32From(uint32) NullUint32`, `Uint64From(uint64) NullUint64`, `Float32From(float32) NullFloat32`, `BoolFrom(bool) NullBool`
  - Methods on every type: `ValueOrZero() T`, `Ptr() *T`, `IsZero() bool`, `Scan(any) error`, `Value() (driver.Value, error)`, `MarshalJSON() ([]byte, error)`, `UnmarshalJSON([]byte) error`
  - Exported field on every type: `V` (the value) and `Valid bool`

- [ ] **Step 1: Write the failing tests**

Create `decoder/nulltypes/nulltypes_test.go`:

```go
package nulltypes

import (
	"encoding/json"
	"testing"
	"unsafe"

	"github.com/guregu/null/v6"
)

func TestSizes(t *testing.T) {
	// The whole reason this package exists. guregu/null is 16 bytes for every
	// numeric type because it embeds sql.NullInt64.
	cases := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"NullUint8", unsafe.Sizeof(NullUint8{}), 2},
		{"NullUint16", unsafe.Sizeof(NullUint16{}), 4},
		{"NullUint32", unsafe.Sizeof(NullUint32{}), 8},
		{"NullUint64", unsafe.Sizeof(NullUint64{}), 16},
		{"NullFloat32", unsafe.Sizeof(NullFloat32{}), 8},
		{"NullBool", unsafe.Sizeof(NullBool{}), 2},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("sizeof(%s) = %d, want %d", c.name, c.got, c.want)
		}
	}
}

func TestScanNull(t *testing.T) {
	var n NullUint8
	n = Uint8From(42) // start valid, so we prove Scan(nil) clears it
	if err := n.Scan(nil); err != nil {
		t.Fatalf("Scan(nil) returned error: %v", err)
	}
	if n.Valid {
		t.Error("Scan(nil) left Valid = true")
	}
	if n.V != 0 {
		t.Errorf("Scan(nil) left V = %d, want 0", n.V)
	}
}

func TestScanDriverTypes(t *testing.T) {
	// go-sql-driver/mysql returns int64 for integer columns, float64 for
	// double columns, and []byte when a value arrives over the text protocol.
	cases := []struct {
		name  string
		input any
		want  uint8
	}{
		{"int64", int64(7), 7},
		{"float64", float64(7), 7},
		{"bytes", []byte("7"), 7},
		{"string", "7", 7},
	}
	for _, c := range cases {
		var n NullUint8
		if err := n.Scan(c.input); err != nil {
			t.Errorf("%s: Scan(%v) returned error: %v", c.name, c.input, err)
			continue
		}
		if !n.Valid || n.V != c.want {
			t.Errorf("%s: Scan(%v) = {%d %t}, want {%d true}", c.name, c.input, n.V, n.Valid, c.want)
		}
	}
}

func TestScanOutOfRange(t *testing.T) {
	// Narrowing must fail loudly at the database boundary rather than
	// silently truncating. (The setters clamp instead — different boundary,
	// different policy, see clampUint8 in decoder/pokemon.go.)
	var n NullUint8
	if err := n.Scan(int64(256)); err == nil {
		t.Error("Scan(256) into NullUint8 returned nil error, want out-of-range")
	}
	if err := n.Scan(int64(-1)); err == nil {
		t.Error("Scan(-1) into NullUint8 returned nil error, want out-of-range")
	}
}

func TestValueRoundTrip(t *testing.T) {
	valid := Uint16From(1234)
	v, err := valid.Value()
	if err != nil {
		t.Fatalf("Value() returned error: %v", err)
	}
	if v != int64(1234) {
		t.Errorf("Value() = %v (%T), want int64(1234)", v, v)
	}

	var invalid NullUint16
	v, err = invalid.Value()
	if err != nil {
		t.Fatalf("Value() on invalid returned error: %v", err)
	}
	if v != nil {
		t.Errorf("Value() on invalid = %v, want nil", v)
	}
}

func TestJSONMatchesGuregu(t *testing.T) {
	// The API responses and webhook payloads are public contracts. A nullable
	// field must marshal to `null`, never to a zero value. Compare byte-for-byte
	// against the library we are replacing.
	t.Run("valid", func(t *testing.T) {
		mine, err := json.Marshal(Uint8From(15))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		theirs, err := json.Marshal(null.IntFrom(15))
		if err != nil {
			t.Fatalf("marshal guregu: %v", err)
		}
		if string(mine) != string(theirs) {
			t.Errorf("marshal = %s, guregu = %s", mine, theirs)
		}
	})

	t.Run("null", func(t *testing.T) {
		mine, err := json.Marshal(NullUint8{})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(mine) != "null" {
			t.Errorf("marshal of invalid = %s, want null", mine)
		}
	})
}

func TestUnmarshalJSON(t *testing.T) {
	var n NullUint16
	if err := json.Unmarshal([]byte("null"), &n); err != nil {
		t.Fatalf("unmarshal null: %v", err)
	}
	if n.Valid {
		t.Error("unmarshal null left Valid = true")
	}

	if err := json.Unmarshal([]byte("500"), &n); err != nil {
		t.Fatalf("unmarshal 500: %v", err)
	}
	if !n.Valid || n.V != 500 {
		t.Errorf("unmarshal 500 = {%d %t}, want {500 true}", n.V, n.Valid)
	}
}

func TestPtr(t *testing.T) {
	if p := (NullUint8{}).Ptr(); p != nil {
		t.Errorf("Ptr() on invalid = %v, want nil", p)
	}
	p := Uint8From(9).Ptr()
	if p == nil || *p != 9 {
		t.Errorf("Ptr() on valid = %v, want pointer to 9", p)
	}
}

func TestFloat32(t *testing.T) {
	var n NullFloat32
	if err := n.Scan(float64(2.5)); err != nil {
		t.Fatalf("Scan(2.5): %v", err)
	}
	if !n.Valid || n.V != 2.5 {
		t.Errorf("Scan(2.5) = {%v %t}, want {2.5 true}", n.V, n.Valid)
	}
}

func TestBool(t *testing.T) {
	var n NullBool
	if err := n.Scan(int64(1)); err != nil {
		t.Fatalf("Scan(1): %v", err)
	}
	if !n.Valid || !n.V {
		t.Errorf("Scan(1) = {%t %t}, want {true true}", n.V, n.Valid)
	}
	if err := n.Scan(nil); err != nil {
		t.Fatalf("Scan(nil): %v", err)
	}
	if n.Valid {
		t.Error("Scan(nil) left Valid = true")
	}
}

// TestUint64FullRange covers the S2 cell id case: cell_id is `bigint unsigned`
// and uses the full 64-bit range, but the driver hands it back as a signed
// int64. The bits must survive, matching the existing uint64(x.Int64) casts at
// decoder/preload.go.
func TestUint64FullRange(t *testing.T) {
	const cellID = uint64(0xFFFFFFFFFFFFFFF0)
	var n NullUint64
	if err := n.Scan(int64(cellID)); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if !n.Valid || n.V != cellID {
		t.Errorf("Scan = {%#x %t}, want {%#x true}", n.V, n.Valid, cellID)
	}
	v, err := n.Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	if v != int64(cellID) {
		t.Errorf("Value = %v, want %v", v, int64(cellID))
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test -tags go_json ./decoder/nulltypes/
```

Expected: FAIL — the package does not exist yet (`no Go files in .../decoder/nulltypes`).

- [ ] **Step 3: Write the implementation**

Create `decoder/nulltypes/nulltypes.go`:

```go
// Package nulltypes provides nullable scalar types sized to the database
// columns Golbat actually uses.
//
// github.com/guregu/null/v6 is 16 bytes for every numeric type because it
// embeds sql.NullInt64 or sql.NullFloat64. Most of the nullable pokemon columns
// are tinyint or smallint, so those 16 bytes carry one or two bytes of payload.
// Pokemon is cached in the millions, and the waste pushes the entity over Go's
// 512-byte GC threshold. See
// docs/superpowers/specs/2026-08-16-pokemon-struct-packing-design.md.
//
// These types mirror guregu/null's API surface — Valid, ValueOrZero, Ptr,
// IsZero, and JSON marshalling that produces `null` rather than a zero value —
// so existing call sites keep compiling, and they implement sql.Scanner and
// driver.Valuer so sqlx keeps binding them without a shim struct.
package nulltypes

import (
	"database/sql/driver"
	"fmt"
	"strconv"
)

// asInt64 normalises the values a SQL driver produces for integer columns.
// go-sql-driver/mysql returns int64 for integer types, float64 for double
// types, and []byte for values that arrive over the text protocol.
func asInt64(value any) (int64, error) {
	switch v := value.(type) {
	case int64:
		return v, nil
	case uint64:
		return int64(v), nil
	case float64:
		return int64(v), nil
	case bool:
		if v {
			return 1, nil
		}
		return 0, nil
	case []byte:
		return strconv.ParseInt(string(v), 10, 64)
	case string:
		return strconv.ParseInt(v, 10, 64)
	default:
		return 0, fmt.Errorf("cannot convert %T to int64", value)
	}
}

// asFloat64 is the float equivalent of asInt64.
func asFloat64(value any) (float64, error) {
	switch v := value.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case []byte:
		return strconv.ParseFloat(string(v), 64)
	case string:
		return strconv.ParseFloat(v, 64)
	default:
		return 0, fmt.Errorf("cannot convert %T to float64", value)
	}
}

// unsignedNarrow is the set of widths where a bool flag is cheaper than
// guregu/null's embedded sql.NullInt64. uint64 is excluded: at 16 bytes with
// the flag it saves nothing, and it needs the sign-reinterpreting Scan that
// NullUint64 implements separately.
type unsignedNarrow interface {
	~uint8 | ~uint16 | ~uint32
}

// NullUint is a nullable unsigned integer narrowed to T.
type NullUint[T unsignedNarrow] struct {
	V     T
	Valid bool
}

type (
	NullUint8  = NullUint[uint8]
	NullUint16 = NullUint[uint16]
	NullUint32 = NullUint[uint32]
)

func Uint8From(v uint8) NullUint8    { return NullUint8{V: v, Valid: true} }
func Uint16From(v uint16) NullUint16 { return NullUint16{V: v, Valid: true} }
func Uint32From(v uint32) NullUint32 { return NullUint32{V: v, Valid: true} }

// ValueOrZero returns the inner value if valid, otherwise the zero value.
func (n NullUint[T]) ValueOrZero() T {
	if !n.Valid {
		var zero T
		return zero
	}
	return n.V
}

// Ptr returns a pointer to the inner value, or nil if invalid.
func (n NullUint[T]) Ptr() *T {
	if !n.Valid {
		return nil
	}
	v := n.V
	return &v
}

// IsZero reports whether the value is null, matching guregu/null's semantics
// (a valid zero is not "zero" for this purpose).
func (n NullUint[T]) IsZero() bool { return !n.Valid }

// Scan implements sql.Scanner.
func (n *NullUint[T]) Scan(value any) error {
	var zero T
	if value == nil {
		n.V, n.Valid = zero, false
		return nil
	}
	i, err := asInt64(value)
	if err != nil {
		return fmt.Errorf("nulltypes: scanning %T: %w", zero, err)
	}
	narrowed := T(i)
	if int64(narrowed) != i {
		return fmt.Errorf("nulltypes: value %d out of range for %T", i, zero)
	}
	n.V, n.Valid = narrowed, true
	return nil
}

// Value implements driver.Valuer.
func (n NullUint[T]) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}
	return int64(n.V), nil
}

// MarshalJSON implements json.Marshaler, producing `null` when invalid.
func (n NullUint[T]) MarshalJSON() ([]byte, error) {
	if !n.Valid {
		return []byte("null"), nil
	}
	return strconv.AppendUint(nil, uint64(n.V), 10), nil
}

// UnmarshalJSON implements json.Unmarshaler, accepting a number or null.
func (n *NullUint[T]) UnmarshalJSON(data []byte) error {
	var zero T
	if string(data) == "null" {
		n.V, n.Valid = zero, false
		return nil
	}
	u, err := strconv.ParseUint(string(data), 10, 64)
	if err != nil {
		return fmt.Errorf("nulltypes: unmarshalling %q into %T: %w", data, zero, err)
	}
	narrowed := T(u)
	if uint64(narrowed) != u {
		return fmt.Errorf("nulltypes: value %d out of range for %T", u, zero)
	}
	n.V, n.Valid = narrowed, true
	return nil
}

// NullUint64 is a nullable uint64.
//
// At 16 bytes it is the same size as guregu/null's Int and saves nothing; it
// exists for type clarity and because cell_id needs the full 64-bit range.
// MariaDB's `bigint unsigned` round-trips through the driver as a signed
// int64, so Scan and Value reinterpret the bits rather than range-checking.
// This matches the existing uint64(x.Int64) casts at decoder/preload.go.
type NullUint64 struct {
	V     uint64
	Valid bool
}

func Uint64From(v uint64) NullUint64 { return NullUint64{V: v, Valid: true} }

func (n NullUint64) ValueOrZero() uint64 {
	if !n.Valid {
		return 0
	}
	return n.V
}

func (n NullUint64) Ptr() *uint64 {
	if !n.Valid {
		return nil
	}
	v := n.V
	return &v
}

func (n NullUint64) IsZero() bool { return !n.Valid }

func (n *NullUint64) Scan(value any) error {
	if value == nil {
		n.V, n.Valid = 0, false
		return nil
	}
	if u, ok := value.(uint64); ok {
		n.V, n.Valid = u, true
		return nil
	}
	i, err := asInt64(value)
	if err != nil {
		return fmt.Errorf("nulltypes: scanning uint64: %w", err)
	}
	n.V, n.Valid = uint64(i), true
	return nil
}

func (n NullUint64) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}
	return int64(n.V), nil
}

func (n NullUint64) MarshalJSON() ([]byte, error) {
	if !n.Valid {
		return []byte("null"), nil
	}
	return strconv.AppendUint(nil, n.V, 10), nil
}

func (n *NullUint64) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		n.V, n.Valid = 0, false
		return nil
	}
	u, err := strconv.ParseUint(string(data), 10, 64)
	if err != nil {
		return fmt.Errorf("nulltypes: unmarshalling %q into uint64: %w", data, err)
	}
	n.V, n.Valid = u, true
	return nil
}

// NullFloat32 is a nullable float32.
//
// Used for weight, height and iv, which are approximate game-supplied values.
// Latitude and longitude deliberately stay float64: the columns are
// double(18,14) and the precision is load-bearing for spatial matching.
type NullFloat32 struct {
	V     float32
	Valid bool
}

func Float32From(v float32) NullFloat32 { return NullFloat32{V: v, Valid: true} }

func (n NullFloat32) ValueOrZero() float32 {
	if !n.Valid {
		return 0
	}
	return n.V
}

func (n NullFloat32) Ptr() *float32 {
	if !n.Valid {
		return nil
	}
	v := n.V
	return &v
}

func (n NullFloat32) IsZero() bool { return !n.Valid }

func (n *NullFloat32) Scan(value any) error {
	if value == nil {
		n.V, n.Valid = 0, false
		return nil
	}
	f, err := asFloat64(value)
	if err != nil {
		return fmt.Errorf("nulltypes: scanning float32: %w", err)
	}
	n.V, n.Valid = float32(f), true
	return nil
}

func (n NullFloat32) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}
	return float64(n.V), nil
}

func (n NullFloat32) MarshalJSON() ([]byte, error) {
	if !n.Valid {
		return []byte("null"), nil
	}
	return strconv.AppendFloat(nil, float64(n.V), 'f', -1, 32), nil
}

func (n *NullFloat32) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		n.V, n.Valid = 0, false
		return nil
	}
	f, err := strconv.ParseFloat(string(data), 32)
	if err != nil {
		return fmt.Errorf("nulltypes: unmarshalling %q into float32: %w", data, err)
	}
	n.V, n.Valid = float32(f), true
	return nil
}

// NullBool is a nullable bool. guregu/null's Bool is already only 2 bytes;
// this exists so the whole struct uses one vocabulary.
type NullBool struct {
	V     bool
	Valid bool
}

func BoolFrom(v bool) NullBool { return NullBool{V: v, Valid: true} }

func (n NullBool) ValueOrZero() bool {
	if !n.Valid {
		return false
	}
	return n.V
}

func (n NullBool) Ptr() *bool {
	if !n.Valid {
		return nil
	}
	v := n.V
	return &v
}

func (n NullBool) IsZero() bool { return !n.Valid }

func (n *NullBool) Scan(value any) error {
	if value == nil {
		n.V, n.Valid = false, false
		return nil
	}
	if b, ok := value.(bool); ok {
		n.V, n.Valid = b, true
		return nil
	}
	i, err := asInt64(value)
	if err != nil {
		return fmt.Errorf("nulltypes: scanning bool: %w", err)
	}
	n.V, n.Valid = i != 0, true
	return nil
}

func (n NullBool) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}
	return n.V, nil
}

func (n NullBool) MarshalJSON() ([]byte, error) {
	if !n.Valid {
		return []byte("null"), nil
	}
	return strconv.AppendBool(nil, n.V), nil
}

func (n *NullBool) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		n.V, n.Valid = false, false
		return nil
	}
	b, err := strconv.ParseBool(string(data))
	if err != nil {
		return fmt.Errorf("nulltypes: unmarshalling %q into bool: %w", data, err)
	}
	n.V, n.Valid = b, true
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go test -tags go_json -v ./decoder/nulltypes/
```

Expected: PASS, all of them. If `TestSizes` fails, the field order inside a type is wrong — the value must come before `Valid`.

- [ ] **Step 5: Verify the interfaces are actually satisfied**

Compile-time assertions catch a missing pointer receiver, which is otherwise a runtime surprise inside sqlx. Add to `nulltypes.go`:

```go
var (
	_ sql.Scanner   = (*NullUint8)(nil)
	_ sql.Scanner   = (*NullUint16)(nil)
	_ sql.Scanner   = (*NullUint32)(nil)
	_ sql.Scanner   = (*NullUint64)(nil)
	_ sql.Scanner   = (*NullFloat32)(nil)
	_ sql.Scanner   = (*NullBool)(nil)
	_ driver.Valuer = NullUint8{}
	_ driver.Valuer = NullUint16{}
	_ driver.Valuer = NullUint32{}
	_ driver.Valuer = NullUint64{}
	_ driver.Valuer = NullFloat32{}
	_ driver.Valuer = NullBool{}
)
```

with `"database/sql"` added to the imports. Then:

```bash
go build -tags go_json ./decoder/nulltypes/
golangci-lint run ./decoder/nulltypes/
```

Expected: both clean.

- [ ] **Step 6: Commit**

```bash
git add decoder/nulltypes/
git commit -m "feat: add nulltypes package with column-width nullable types"
```

---

## Task 3: Repack the numeric fields

The largest task. The type change and the call-site fixes are one atomic unit — the tree will not compile partway through, so do not try to split this into two commits.

**Files:**
- Modify: `decoder/pokemon.go` (`PokemonData` definition, 33 setters, delete `Capture1/2/3` and their setters)
- Modify: `decoder/pokemon_decode.go` (direct field assignments become setter calls)
- Modify: `stats_collector/stats_collector.go`, `stats_collector/noop.go`, `stats_collector/prometheus.go` (clamp counter)
- Modify: `decoder/pokemon_state.go` (webhook payload sources `capture_*` from `0`)
- Modify: whatever else the compiler flags
- Modify: `decoder/entity_sizes_test.go` (update expected sizes)

**Interfaces:**
- Consumes: everything from Task 2.
- Produces:
  - `PokemonData` with narrowed field types (see the table in the design doc)
  - `clampUint8(v null.Int, field string) nulltypes.NullUint8` and siblings `clampUint16(v null.Int, field string) nulltypes.NullUint16`, `clampUint32(v null.Int, field string) nulltypes.NullUint32`, `clampFloat32(v null.Float) nulltypes.NullFloat32` (no field argument — no range check needed)
  - `statsCollector.IncFieldClamped(field string)`

- [ ] **Step 1: Add the clamp counter to the stats collector**

Three files, mirroring the existing `IncPokemons` pattern.

In `stats_collector/stats_collector.go`, add to the interface:

```go
	// IncFieldClamped counts a value that arrived outside the range of the
	// column it is stored in, and was clamped to the boundary. Non-zero means
	// either the game protocol changed or a decode path is wrong.
	IncFieldClamped(field string)
```

In `stats_collector/noop.go`:

```go
func (c *noopStatsCollector) IncFieldClamped(string) {}
```

In `stats_collector/prometheus.go`, declare the counter alongside the existing ones and implement:

```go
	fieldClamped: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "golbat",
		Name:      "field_clamped_total",
		Help:      "Values clamped to the range of their database column during decode",
	}, []string{"field"}),
```

```go
func (c *promCollector) IncFieldClamped(field string) {
	c.fieldClamped.WithLabelValues(field).Inc()
}
```

Register `fieldClamped` wherever the other collectors in that file are registered.

- [ ] **Step 2: Write the clamp helpers**

Add to `decoder/pokemon.go`:

```go
// clampUint8 narrows a null.Int for storage in a tinyint-backed field.
//
// Values arrive from decoded game protos and are bounded in practice, so
// out-of-range means the protocol changed rather than a normal case. Clamping
// keeps the value at the boundary and counts the event; truncating would
// silently produce a plausible-looking wrong number, which is worse.
//
// Note this is the opposite policy to nulltypes' Scan, which rejects
// out-of-range values outright. That is deliberate: a bad value from our own
// database is a bug worth failing on, a bad value from a game server is a fact
// worth recording.
func clampUint8(v null.Int, field string) nulltypes.NullUint8 {
	if !v.Valid {
		return nulltypes.NullUint8{}
	}
	i := v.Int64
	switch {
	case i < 0:
		statsCollector.IncFieldClamped(field)
		i = 0
	case i > math.MaxUint8:
		statsCollector.IncFieldClamped(field)
		i = math.MaxUint8
	}
	return nulltypes.Uint8From(uint8(i))
}

func clampUint16(v null.Int, field string) nulltypes.NullUint16 {
	if !v.Valid {
		return nulltypes.NullUint16{}
	}
	i := v.Int64
	switch {
	case i < 0:
		statsCollector.IncFieldClamped(field)
		i = 0
	case i > math.MaxUint16:
		statsCollector.IncFieldClamped(field)
		i = math.MaxUint16
	}
	return nulltypes.Uint16From(uint16(i))
}

func clampUint32(v null.Int, field string) nulltypes.NullUint32 {
	if !v.Valid {
		return nulltypes.NullUint32{}
	}
	i := v.Int64
	switch {
	case i < 0:
		statsCollector.IncFieldClamped(field)
		i = 0
	case i > math.MaxUint32:
		statsCollector.IncFieldClamped(field)
		i = math.MaxUint32
	}
	return nulltypes.Uint32From(uint32(i))
}

// clampFloat32 narrows a null.Float. Range is not checked: the values are
// weight, height and iv, all far inside float32's range.
func clampFloat32(v null.Float) nulltypes.NullFloat32 {
	if !v.Valid {
		return nulltypes.NullFloat32{}
	}
	return nulltypes.Float32From(float32(v.Float64))
}
```

Add `"math"` and `"golbat/decoder/nulltypes"` to the file's imports.

- [ ] **Step 3: Rewrite the `PokemonData` struct**

Replace the struct at `decoder/pokemon.go:13-54`. Every `db` tag must be preserved exactly — sqlx binds by tag, and a typo is a runtime error, not a compile error.

```go
// PokemonData contains all database-persisted fields for Pokemon.
// This struct is embedded in Pokemon and can be safely copied for write-behind
// queueing.
//
// FIELD ORDER IS LOAD-BEARING. Fields are declared in descending alignment
// order to minimise padding; the same types in arbitrary order measured 264
// bytes against 232 ordered. TestPokemonEntitySizes guards the result — if it
// fails after you add a field, read its doc comment before touching the
// constant.
//
// Types are narrowed to the actual column widths. Verify any change against
// sql/*.up.sql, NOT against the schema comment further down this file, which
// has three known-stale claims.
type PokemonData struct {
	// --- 8-byte aligned ---
	Id      Uint64Str            `db:"id"`
	SpawnId nulltypes.NullUint64 `db:"spawn_id"`
	CellId  nulltypes.NullUint64 `db:"cell_id"`
	Lat     float64              `db:"lat"`
	Lon     float64              `db:"lon"`

	// --- 4-byte aligned ---
	FirstSeenTimestamp uint32                `db:"first_seen_timestamp"`
	Changed            uint32                `db:"changed"`
	ExpireTimestamp    nulltypes.NullUint32  `db:"expire_timestamp"`
	Updated            nulltypes.NullUint32  `db:"updated"`
	Weight             nulltypes.NullFloat32 `db:"weight"`
	Height             nulltypes.NullFloat32 `db:"height"`
	Iv                 nulltypes.NullFloat32 `db:"iv"`

	// --- 2-byte aligned ---
	PokemonId          int16                `db:"pokemon_id"`
	Move1              nulltypes.NullUint16 `db:"move_1"`
	Move2              nulltypes.NullUint16 `db:"move_2"`
	Cp                 nulltypes.NullUint16 `db:"cp"`
	Form               nulltypes.NullUint16 `db:"form"`
	DisplayPokemonId   nulltypes.NullUint16 `db:"display_pokemon_id"`
	DisplayPokemonForm nulltypes.NullUint16 `db:"display_pokemon_form"`

	// --- 1-byte ---
	Gender                  nulltypes.NullUint8 `db:"gender"`
	AtkIv                   nulltypes.NullUint8 `db:"atk_iv"`
	DefIv                   nulltypes.NullUint8 `db:"def_iv"`
	StaIv                   nulltypes.NullUint8 `db:"sta_iv"`
	Level                   nulltypes.NullUint8 `db:"level"`
	Weather                 nulltypes.NullUint8 `db:"weather"`
	Costume                 nulltypes.NullUint8 `db:"costume"`
	Size                    nulltypes.NullUint8 `db:"size"`
	IsStrong                nulltypes.NullBool  `db:"strong"`
	Shiny                   nulltypes.NullBool  `db:"shiny"`
	ExpireTimestampVerified bool                `db:"expire_timestamp_verified"`
	IsDitto                 bool                `db:"is_ditto"`
	IsEvent                 int8                `db:"is_event"`

	// --- pointer-carrying, last ---
	PokestopId     null.String `db:"pokestop_id"`
	SeenType       null.String `db:"seen_type"`
	Username       null.String `db:"username"`
	Pvp            null.String `db:"pvp"`
	GolbatInternal []byte      `db:"golbat_internal"`
}
```

`Capture1`, `Capture2` and `Capture3` are gone. `SeenType` stays a `null.String` here and is narrowed in Task 5.

- [ ] **Step 4: Rewrite the affected setters**

Each narrowed field's setter keeps its `null.X` signature and converts inside. `Gender` is the pattern; apply it to `AtkIv`, `DefIv`, `StaIv`, `Level`, `Weather`, `Costume` and `Size` with the matching field name string:

```go
func (pokemon *Pokemon) SetGender(v null.Int) {
	next := clampUint8(v, "gender")
	if pokemon.Gender != next {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields,
				fmt.Sprintf("Gender:%s->%s", FormatNull(pokemon.Gender), FormatNull(next)))
		}
		pokemon.Gender = next
		pokemon.dirty = true
	}
}
```

`FormatNull` needs no change: it is already generic over `Ptrable[T] interface{ Ptr() *T }` (`decoder/main.go:310-322`), which every `nulltypes` type satisfies.

Use `clampUint16` for `Move1`, `Move2`, `Cp`, `Form`, `DisplayPokemonId` and `DisplayPokemonForm`; `clampUint32` for `ExpireTimestamp` and `Updated`; `clampFloat32` for `Weight`, `Height` and `Iv`.

For `SpawnId` and `CellId`, convert without clamping — the full 64-bit range is legitimate:

```go
func (pokemon *Pokemon) SetCellId(v null.Int) {
	var next nulltypes.NullUint64
	if v.Valid {
		next = nulltypes.Uint64From(uint64(v.Int64))
	}
	if pokemon.CellId != next {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields,
				fmt.Sprintf("CellId:%s->%s", FormatNull(pokemon.CellId), FormatNull(next)))
		}
		pokemon.CellId = next
		pokemon.dirty = true
	}
}
```

For `IsStrong` and `Shiny`, convert `null.Bool` to `nulltypes.NullBool`:

```go
func nullBoolFrom(v null.Bool) nulltypes.NullBool {
	if !v.Valid {
		return nulltypes.NullBool{}
	}
	return nulltypes.BoolFrom(v.Bool)
}
```

Delete `SetCapture1`, `SetCapture2` and `SetCapture3` entirely.

- [ ] **Step 5: Route the direct assignments in `pokemon_decode.go` through setters**

`decoder/pokemon_decode.go:247` assigns `pokemon.Iv` directly, and nearby code assigns `AtkIv`/`DefIv`/`StaIv`. Direct assignment now skips clamping and dirty tracking. Convert each to its setter, for example:

```go
// was: pokemon.Iv = null.FloatFrom(float64(a+d+s) / .45)
pokemon.SetIv(null.FloatFrom(float64(a+d+s) / .45))
```

Do the same at `decoder/pokemon_decode.go:729` (`pokemon.Iv = null.NewFloat(0, false)` becomes `pokemon.SetIv(null.Float{})`).

- [ ] **Step 6: Fix the webhook payload's capture fields**

At `decoder/pokemon_state.go:475-477`, the payload struct reads the now-deleted fields. The payload keeps its fields so external consumers see no change; they were always zero because nothing ever set them:

```go
	// capture_1/2/3 have never been populated by any code path — the setters
	// existed but had no callers, and the columns were in neither
	// pokemonSelectColumns nor pokemonBatchUpsertQuery. The payload keeps the
	// fields at their long-standing value so consumers see no change.
	Capture1: 0,
	Capture2: 0,
	Capture3: 0,
```

- [ ] **Step 7: Build and let the compiler enumerate the rest**

```bash
go build -tags go_json ./... 2>&1 | head -60
```

Expected: a list of type mismatches. These are the read sites where `ValueOrZero()` now returns a narrower type, or where `Ptr()` returns a narrower pointer. Work through them adding explicit conversions. Expect them in `decoder/pokemon_state.go`, `decoder/stats.go`, `decoder/api_pokemon_response.go`, `decoder/pokemonRtree.go`, `decoder/weather_iv.go`, `decoder/api_pokemon_scan_v2.go`, `decoder/api_pokemon_scan_v3.go`, `decoder/pokemon_process.go` and `decoder/pokemon_preserve.go`.

Two traps while doing this pass:

- `api_pokemon_scan_v2.go` and `_v3.go` each contain a second, unrelated loop variable also named `pokemon`, of an `ApiPokemonDnfId` shape. Its fields are compared against literal `nil`, which `Uint64Str` and the `nulltypes` types never can be — that is how to tell them apart. Only the `*Pokemon` returned by `peekPokemonRecordReadOnly` is affected.
- `decoder/station_decode.go` has a `pokemon` that is a `*pogo.PlayerClientStationedPokemonProto`. Nothing here touches it.

For API response fields typed `*int64` that now receive a `*uint8`, prefer widening at the response boundary over changing the response type — `api.md` is a public contract:

```go
	AtkIv: widenPtr[uint8, int64](pokemon.AtkIv.Ptr()),
```

with a small helper in `decoder/api_pokemon_response.go`:

```go
// widenPtr converts a pointer to a narrow numeric into a pointer to a wider
// one, preserving nil. The API response types stay as they are: api.md is a
// public contract and the storage width is an internal detail.
func widenPtr[N, W ~int8 | ~int16 | ~int32 | ~int64 | ~uint8 | ~uint16 | ~uint32 | ~uint64](p *N) *W {
	if p == nil {
		return nil
	}
	w := W(*p)
	return &w
}
```

- [ ] **Step 8: Update the expected sizes**

In `decoder/entity_sizes_test.go`, change the constants:

```go
	const (
		wantPokemonData = 232
		wantPokemon     = 424
	)
```

`wantPokemon` is an estimate at this point — `changedFields` is still present and is removed in Task 6. Run the test, and if the actual number differs, put the actual number in and note it in the commit message. The value of this test is that the number is deliberate, not that it was predicted correctly.

- [ ] **Step 9: Refresh the stale schema comment**

The block comment at `decoder/pokemon.go:88-140` reproduces a `CREATE TABLE`
that no longer matches the database. Three of its claims are wrong and two of
them nearly sent this design the wrong way. Regenerate it from the migrations,
or replace it with a pointer to them:

```go
// The pokemon table's current shape is the composition of sql/1_rdmdb_tables.up.sql
// and every migration after it. Notable divergences from the original CREATE TABLE
// that used to be reproduced here:
//   - iv is a plain nullable float(5,2), not a generated column (sql/11_ivchanges.up.sql)
//   - seen_type is an eight-value enum (sql/45_tappables_seen_type_lure.up.sql)
//   - size is tinyint unsigned; the original double column was renamed to height
//     (sql/7_add_height_size.up.sql)
// Check sql/*.up.sql before relying on a column's type.
```

- [ ] **Step 10: Run the full decoder test suite**

```bash
go test -tags go_json ./decoder/ ./decoder/nulltypes/
golangci-lint run
```

Expected: PASS and clean. `decoder/api_pokemon_response_test.go` constructs a `PokemonData` literal with `null.IntFrom(...)` values and asserts an exact JSON string — it will need its literals updated to the new types, and its expected JSON should come out **unchanged**. If the expected JSON has to change, stop: that is a public API regression, not a test to update.

- [ ] **Step 11: Commit**

```bash
git add -A
git commit -m "perf: narrow PokemonData numeric fields to column widths

PokemonData 592 -> 232 bytes. Nullable numerics move from guregu/null's
16-byte wrappers to nulltypes equivalents sized to the actual columns, and
fields are reordered by descending alignment.

Setters keep their null.X signatures and clamp out-of-range values to the
column boundary, counted by golbat_field_clamped_total. Direct field
assignments in pokemon_decode.go move to setter calls so they cannot skip
the clamp.

Drops Capture1/2/3, which had setters but no callers and appeared in
neither pokemonSelectColumns nor pokemonBatchUpsertQuery. The webhook
payload keeps its capture_* fields at 0, which is what consumers have
always received."
```

---

## Task 4: Prove the narrowed types survive a real database

Everything so far is unit-tested against a mock. This is the first test that puts a NULL from MariaDB into a `nulltypes` field, which is the risk the whole approach was chosen to remove.

**Files:**
- Create: `decoder/pokemon_nullscan_test.go`

**Interfaces:**
- Consumes: `PokemonData` from Task 3, `nulltypes` from Task 2.
- Produces: nothing other tasks depend on.

- [ ] **Step 1: Write the integration test**

Create `decoder/pokemon_nullscan_test.go`:

```go
package decoder

import (
	"context"
	"os"
	"testing"

	"github.com/jmoiron/sqlx"

	_ "github.com/go-sql-driver/mysql"
)

// TestPokemonNullColumnRoundTrip is the test the packing change exists to pass.
//
// Every nullable pokemon column is narrowed from guregu/null's 16-byte wrappers
// to nulltypes equivalents. Those implement sql.Scanner, so a NULL should land
// as Valid=false — but that is a runtime property, not a compile-time one, and
// getting it wrong means the first production row with a null gender panics
// inside database/sql's convertAssign.
//
// Requires a MariaDB with the golbat schema. Set GOLBAT_TEST_DSN, e.g.
//   GOLBAT_TEST_DSN='user:pass@tcp(127.0.0.1:3306)/golbat_test?parseTime=true'
func TestPokemonNullColumnRoundTrip(t *testing.T) {
	dsn := os.Getenv("GOLBAT_TEST_DSN")
	if dsn == "" {
		t.Skip("GOLBAT_TEST_DSN not set; skipping database round-trip test")
	}

	db, err := sqlx.Connect("mysql", dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	const testID = 999999999999999999

	t.Cleanup(func() {
		if _, err := db.ExecContext(ctx, "DELETE FROM pokemon WHERE id = ?", testID); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})

	// Insert with every nullable column explicitly NULL. Only the NOT NULL
	// columns get values.
	_, err = db.ExecContext(ctx, `
		INSERT INTO pokemon (id, pokemon_id, lat, lon, first_seen_timestamp,
			changed, expire_timestamp_verified, is_event)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		testID, 25, 51.5, -0.1, 1000, 2000, 0, 0)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	var got Pokemon
	err = db.GetContext(ctx, &got,
		"SELECT "+pokemonSelectColumns+" FROM pokemon WHERE id = ?", testID)
	if err != nil {
		t.Fatalf("select: %v", err)
	}

	// Every nullable field must come back invalid, not zero-and-valid.
	checks := []struct {
		name  string
		valid bool
	}{
		{"SpawnId", got.SpawnId.Valid},
		{"CellId", got.CellId.Valid},
		{"ExpireTimestamp", got.ExpireTimestamp.Valid},
		{"Updated", got.Updated.Valid},
		{"Weight", got.Weight.Valid},
		{"Height", got.Height.Valid},
		{"Iv", got.Iv.Valid},
		{"Move1", got.Move1.Valid},
		{"Move2", got.Move2.Valid},
		{"Cp", got.Cp.Valid},
		{"Form", got.Form.Valid},
		{"DisplayPokemonId", got.DisplayPokemonId.Valid},
		{"DisplayPokemonForm", got.DisplayPokemonForm.Valid},
		{"Gender", got.Gender.Valid},
		{"AtkIv", got.AtkIv.Valid},
		{"DefIv", got.DefIv.Valid},
		{"StaIv", got.StaIv.Valid},
		{"Level", got.Level.Valid},
		{"Weather", got.Weather.Valid},
		{"Costume", got.Costume.Valid},
		{"Size", got.Size.Valid},
		{"IsStrong", got.IsStrong.Valid},
		{"Shiny", got.Shiny.Valid},
	}
	for _, c := range checks {
		if c.valid {
			t.Errorf("%s.Valid = true after scanning NULL, want false", c.name)
		}
	}

	// And the NOT NULL columns must have survived intact.
	if got.PokemonId != 25 {
		t.Errorf("PokemonId = %d, want 25", got.PokemonId)
	}
	if got.Lat != 51.5 {
		t.Errorf("Lat = %v, want 51.5", got.Lat)
	}
}

// TestPokemonFullRowRoundTrip writes a fully-populated row through the same
// upsert the write-behind queue uses, then reads it back, proving driver.Valuer
// binds correctly for every narrowed type.
func TestPokemonFullRowRoundTrip(t *testing.T) {
	dsn := os.Getenv("GOLBAT_TEST_DSN")
	if dsn == "" {
		t.Skip("GOLBAT_TEST_DSN not set; skipping database round-trip test")
	}

	db, err := sqlx.Connect("mysql", dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	const testID = 999999999999999998

	t.Cleanup(func() {
		if _, err := db.ExecContext(ctx, "DELETE FROM pokemon WHERE id = ?", testID); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})

	want := PokemonData{
		Id:                      testID,
		PokemonId:               149,
		Lat:                     51.50000000000001,
		Lon:                     -0.10000000000001,
		FirstSeenTimestamp:      1000,
		Changed:                 2000,
		ExpireTimestampVerified: true,
		CellId:                  nulltypes.Uint64From(0xFFFFFFFFFFFFFFF0),
		AtkIv:                   nulltypes.Uint8From(15),
		DefIv:                   nulltypes.Uint8From(14),
		StaIv:                   nulltypes.Uint8From(13),
		Level:                   nulltypes.Uint8From(35),
		Cp:                      nulltypes.Uint16From(3500),
		Move1:                   nulltypes.Uint16From(216),
		Gender:                  nulltypes.Uint8From(1),
		Size:                    nulltypes.Uint8From(5),
		Shiny:                   nulltypes.BoolFrom(true),
		Weight:                  nulltypes.Float32From(3.5),
	}

	if _, err := db.NamedExecContext(ctx, pokemonBatchUpsertQuery, []PokemonData{want}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	var got Pokemon
	if err := db.GetContext(ctx, &got,
		"SELECT "+pokemonSelectColumns+" FROM pokemon WHERE id = ?", testID); err != nil {
		t.Fatalf("select: %v", err)
	}

	if got.CellId != want.CellId {
		t.Errorf("CellId = %#x, want %#x (full 64-bit range must survive)", got.CellId.V, want.CellId.V)
	}
	if got.Cp != want.Cp {
		t.Errorf("Cp = %v, want %v", got.Cp, want.Cp)
	}
	if got.AtkIv != want.AtkIv {
		t.Errorf("AtkIv = %v, want %v", got.AtkIv, want.AtkIv)
	}
	if got.Shiny != want.Shiny {
		t.Errorf("Shiny = %v, want %v", got.Shiny, want.Shiny)
	}
	if got.Lat != want.Lat {
		t.Errorf("Lat = %.14f, want %.14f (float64 precision must survive)", got.Lat, want.Lat)
	}
}
```

Add `"golbat/decoder/nulltypes"` to the imports.

- [ ] **Step 2: Run against a real database**

Create a scratch database with the golbat schema, then:

```bash
GOLBAT_TEST_DSN='user:pass@tcp(127.0.0.1:3306)/golbat_test' go test -tags go_json -run 'TestPokemonNullColumnRoundTrip|TestPokemonFullRowRoundTrip' -v ./decoder/
```

Expected: PASS. A failure here is the change's central risk materialising — fix `nulltypes`, not the test.

- [ ] **Step 3: Confirm the test skips cleanly without a database**

```bash
go test -tags go_json -run 'TestPokemonNullColumnRoundTrip' -v ./decoder/
```

Expected: SKIP with "GOLBAT_TEST_DSN not set". CI defines no database service today, so it must not fail there.

- [ ] **Step 4: Commit**

```bash
git add decoder/pokemon_nullscan_test.go
git commit -m "test: verify narrowed pokemon columns round-trip through MariaDB

Covers the failure mode the narrow-wrapper approach was chosen to avoid: a
NULL column landing in a narrowed field. Also pins the two precision cases
that must not regress, the full 64-bit cell_id and float64 lat/lon.

Skips when GOLBAT_TEST_DSN is unset, since CI defines no database service."
```

---

## Task 5: Narrow `SeenType`

`SeenType` is a `null.String` holding one of eight enum values — 24 bytes and a heap pointer for three bits of information. It needs its own type rather than a plain `NullUint8`, because it must present as a string at both the database and the JSON boundaries.

**Files:**
- Modify: `decoder/pokemon_decode.go` (add the code type alongside the existing string constants)
- Modify: `decoder/pokemon.go` (`SeenType` field type, `SetSeenType`)
- Modify: `decoder/pokemon_state.go` (comparisons at lines ~189-196 and ~234-236)
- Modify: `decoder/api_pokemon_response.go` (`SeenType: pokemon.SeenType.Ptr()` at line 123)
- Modify: `decoder/entity_sizes_test.go`
- Create: test cases in `decoder/pokemon_seentype_test.go`

**Interfaces:**
- Consumes: Task 3's `PokemonData`.
- Produces:
  - `SeenTypeCode uint8` with constants `SeenTypeCodeWild`, `SeenTypeCodeEncounter`, `SeenTypeCodeNearbyStop`, `SeenTypeCodeCell`, `SeenTypeCodeLureWild`, `SeenTypeCodeLureEncounter`, `SeenTypeCodeTappableEncounter`, `SeenTypeCodeTappableLureEncounter`
  - `NullSeenType` — exported fields `Code SeenTypeCode` and `Valid bool`; methods `Scan`, `Value`, `MarshalJSON`, `UnmarshalJSON`, `Ptr() *string`, `ValueOrZero() string`, `IsZero() bool`
  - `SeenTypeFrom(SeenTypeCode) NullSeenType`, `ParseSeenType(string) (NullSeenType, error)`

- [ ] **Step 1: Write the failing test**

Create `decoder/pokemon_seentype_test.go`:

```go
package decoder

import (
	"encoding/json"
	"testing"
	"unsafe"
)

func TestNullSeenTypeSize(t *testing.T) {
	if got := unsafe.Sizeof(NullSeenType{}); got != 2 {
		t.Errorf("unsafe.Sizeof(NullSeenType{}) = %d, want 2", got)
	}
}

func TestNullSeenTypeRoundTrip(t *testing.T) {
	// Every value the decode path can produce must survive string -> code ->
	// string unchanged. The database column is an enum of these exact strings.
	all := []string{
		SeenType_Wild, SeenType_Encounter, SeenType_NearbyStop, SeenType_Cell,
		SeenType_LureWild, SeenType_LureEncounter, SeenType_TappableEncounter,
		SeenType_TappableLureEncounter,
	}
	for _, s := range all {
		var n NullSeenType
		if err := n.Scan(s); err != nil {
			t.Errorf("Scan(%q): %v", s, err)
			continue
		}
		if !n.Valid {
			t.Errorf("Scan(%q) left Valid = false", s)
			continue
		}
		v, err := n.Value()
		if err != nil {
			t.Errorf("Value() after Scan(%q): %v", s, err)
			continue
		}
		if v != s {
			t.Errorf("round trip of %q produced %v", s, v)
		}
	}
}

func TestNullSeenTypeNull(t *testing.T) {
	var n NullSeenType
	if err := n.Scan(nil); err != nil {
		t.Fatalf("Scan(nil): %v", err)
	}
	if n.Valid {
		t.Error("Scan(nil) left Valid = true")
	}
	v, err := n.Value()
	if err != nil {
		t.Fatalf("Value(): %v", err)
	}
	if v != nil {
		t.Errorf("Value() on invalid = %v, want nil", v)
	}
}

func TestNullSeenTypeJSON(t *testing.T) {
	// The API response marshals seen_type as a string. That must not change.
	var n NullSeenType
	if err := n.Scan(SeenType_Encounter); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	b, err := json.Marshal(n)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != `"encounter"` {
		t.Errorf("marshal = %s, want \"encounter\"", b)
	}

	b, err = json.Marshal(NullSeenType{})
	if err != nil {
		t.Fatalf("marshal invalid: %v", err)
	}
	if string(b) != "null" {
		t.Errorf("marshal of invalid = %s, want null", b)
	}
}

func TestNullSeenTypeUnknownValue(t *testing.T) {
	// An unrecognised enum value means the game added a seen type and the
	// migrations have not caught up. Fail loudly rather than storing a wrong
	// code.
	var n NullSeenType
	if err := n.Scan("teleported"); err == nil {
		t.Error("Scan of unknown seen type returned nil error, want failure")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
go test -tags go_json -run TestNullSeenType ./decoder/
```

Expected: FAIL — `undefined: NullSeenType`.

- [ ] **Step 3: Implement the type**

Add to `decoder/pokemon_decode.go`, below the existing string constants at lines 308-315:

```go
// SeenTypeCode is the in-memory representation of the seen_type enum column.
//
// The column holds one of eight strings; storing it as a null.String costs a
// 24-byte header plus a heap pointer per cached pokemon. The code is one byte.
// NullSeenType converts at the database and JSON boundaries so both wire
// formats are unchanged.
type SeenTypeCode uint8

const (
	SeenTypeCodeWild SeenTypeCode = iota
	SeenTypeCodeEncounter
	SeenTypeCodeNearbyStop
	SeenTypeCodeCell
	SeenTypeCodeLureWild
	SeenTypeCodeLureEncounter
	SeenTypeCodeTappableEncounter
	SeenTypeCodeTappableLureEncounter
)

// seenTypeStrings maps codes to the exact strings in the enum column. The order
// must match the constants above. Adding a value here requires a migration
// widening the enum — see sql/45_tappables_seen_type_lure.up.sql.
var seenTypeStrings = [...]string{
	SeenType_Wild,
	SeenType_Encounter,
	SeenType_NearbyStop,
	SeenType_Cell,
	SeenType_LureWild,
	SeenType_LureEncounter,
	SeenType_TappableEncounter,
	SeenType_TappableLureEncounter,
}

var seenTypeCodes = func() map[string]SeenTypeCode {
	m := make(map[string]SeenTypeCode, len(seenTypeStrings))
	for i, s := range seenTypeStrings {
		m[s] = SeenTypeCode(i)
	}
	return m
}()

// String returns the database representation of the code.
func (c SeenTypeCode) String() string {
	if int(c) >= len(seenTypeStrings) {
		return ""
	}
	return seenTypeStrings[c]
}

// NullSeenType is a nullable seen_type, stored as a code and presented as a
// string at every boundary.
type NullSeenType struct {
	Code  SeenTypeCode
	Valid bool
}

// SeenTypeFrom builds a valid NullSeenType from a known code.
func SeenTypeFrom(c SeenTypeCode) NullSeenType {
	return NullSeenType{Code: c, Valid: true}
}

// ParseSeenType converts a database or proto string into a NullSeenType.
// An empty string is treated as NULL; an unrecognised value is an error,
// because silently mapping it to a valid code would corrupt scan statistics.
func ParseSeenType(s string) (NullSeenType, error) {
	if s == "" {
		return NullSeenType{}, nil
	}
	c, ok := seenTypeCodes[s]
	if !ok {
		return NullSeenType{}, fmt.Errorf("unknown seen_type %q", s)
	}
	return SeenTypeFrom(c), nil
}

// ValueOrZero returns the string form, or "" if null.
func (n NullSeenType) ValueOrZero() string {
	if !n.Valid {
		return ""
	}
	return n.Code.String()
}

// Ptr returns a pointer to the string form, or nil if null. The API response
// type is *string and stays that way.
func (n NullSeenType) Ptr() *string {
	if !n.Valid {
		return nil
	}
	s := n.Code.String()
	return &s
}

func (n NullSeenType) IsZero() bool { return !n.Valid }

func (n *NullSeenType) Scan(value any) error {
	if value == nil {
		n.Code, n.Valid = 0, false
		return nil
	}
	var s string
	switch v := value.(type) {
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return fmt.Errorf("cannot scan %T into NullSeenType", value)
	}
	parsed, err := ParseSeenType(s)
	if err != nil {
		return err
	}
	*n = parsed
	return nil
}

func (n NullSeenType) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}
	return n.Code.String(), nil
}

func (n NullSeenType) MarshalJSON() ([]byte, error) {
	if !n.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(n.Code.String())
}

func (n *NullSeenType) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		n.Code, n.Valid = 0, false
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	parsed, err := ParseSeenType(s)
	if err != nil {
		return err
	}
	*n = parsed
	return nil
}
```

Add `"database/sql/driver"`, `"encoding/json"` and `"fmt"` to that file's imports as needed.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go test -tags go_json -run TestNullSeenType -v ./decoder/
```

Expected: PASS.

- [ ] **Step 5: Switch the field over**

In `decoder/pokemon.go`, change the field. Note `NullSeenType` lives in package
`decoder`, not `nulltypes`, because it depends on the `SeenType_*` string
constants:

```go
	SeenType NullSeenType `db:"seen_type"`
```

Move it from the pointer-carrying block up into the 1-byte block, next to `IsEvent`.

Update `SetSeenType` to take a `NullSeenType` and update its callers, or keep it taking a `string` and parse inside — prefer the latter, since the decode path already produces the string constants:

```go
func (pokemon *Pokemon) SetSeenType(s string) {
	next, err := ParseSeenType(s)
	if err != nil {
		log.Warnf("SetSeenType: %s", err)
		return
	}
	if pokemon.SeenType != next {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields,
				fmt.Sprintf("SeenType:%s->%s", pokemon.SeenType.ValueOrZero(), next.ValueOrZero()))
		}
		pokemon.SeenType = next
		pokemon.dirty = true
	}
}
```

- [ ] **Step 6: Convert the comparison sites**

At `decoder/pokemon_state.go:234-236` the comparison chain is against string constants. Convert to codes — faster and it removes the string materialisation:

```go
	switch pokemon.SeenType.Code {
	case SeenTypeCodeWild, SeenTypeCodeLureWild, SeenTypeCodeCell, SeenTypeCodeNearbyStop:
		// ... existing body
	}
```

At `decoder/pokemon_state.go:189-196` the debug log builds `oldSeenType`. `ValueOrZero()` still returns a string, so that code needs only its `.Valid` check adjusting if the type changed shape.

At `decoder/api_pokemon_response.go:123`, `pokemon.SeenType.Ptr()` already returns `*string`, so this line is unchanged.

- [ ] **Step 7: Build, test and update the size**

```bash
go build -tags go_json ./... && go test -tags go_json ./decoder/ ./decoder/nulltypes/ && golangci-lint run
```

Update `wantPokemonData` and `wantPokemon` in `decoder/entity_sizes_test.go` to the values the test reports. `PokemonData` should drop by 22 bytes.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "perf: store seen_type as a one-byte code

seen_type is an eight-value enum held in a 24-byte null.String with a heap
pointer. NullSeenType stores the code and converts at the database and JSON
boundaries, so both wire formats are unchanged.

An unrecognised enum value is now an error rather than a silently stored
string, since the alternative is corrupting scan statistics when the game
adds a seen type ahead of the migrations."
```

---

## Task 6: Drop `changedFields` and gate the threshold

**Files:**
- Modify: `decoder/pokemon.go` (remove `changedFields`)
- Modify: `decoder/db_debug.go` / `decoder/db_debug_off.go` if the debug path needs restructuring
- Modify: `decoder/entity_sizes_test.go` (add the threshold assertion)

**Interfaces:**
- Consumes: everything prior.
- Produces: `TestPokemonUnderGCThreshold`.

- [ ] **Step 1: Write the failing threshold test**

Add to `decoder/entity_sizes_test.go`:

```go
// TestPokemonUnderGCThreshold is the acceptance test for the packing work.
//
// Above 512 bytes Go's GC gets materially more expensive: measured across 5M
// live entries, 512 bytes with one pointer marks in 62.6ms and 520 bytes marks
// in 194.4ms. Pokemon is cached in the millions.
//
// If this fails, the entity has grown back over the line. Do not delete the
// test — find the field that pushed it over.
func TestPokemonUnderGCThreshold(t *testing.T) {
	if got := unsafe.Sizeof(Pokemon{}); got > gcSizeThreshold {
		t.Errorf("unsafe.Sizeof(Pokemon{}) = %d, want <= %d", got, gcSizeThreshold)
	}
}
```

- [ ] **Step 2: Run it**

```bash
go test -tags go_json -run TestPokemonUnderGCThreshold -v ./decoder/
```

Expected: PASS already, if Tasks 3 and 5 landed as designed. If it fails, `Pokemon` is still over 512 and the remaining bytes must be found before continuing — report the actual size rather than proceeding.

- [ ] **Step 3: Remove `changedFields`**

`dbDebugEnabled` is a build-tag `const` that is false in production, so every `append` is already compiled away — but the 24-byte slice header stays in the struct and one of its words sits in the GC pointer bitmap.

Remove the field from `Pokemon` and have the debug path build a local slice instead. In each setter, the debug block becomes:

```go
		if dbDebugEnabled {
			logFieldChange(pokemon.Id, "Gender", FormatNull(pokemon.Gender), FormatNull(next))
		}
```

with `logFieldChange` defined in `decoder/db_debug.go` (real implementation) and `decoder/db_debug_off.go` (empty, const-folded away):

```go
// db_debug.go, built with -tags dbdebug
func logFieldChange(id Uint64Str, field, from, to string) {
	log.Debugf("pokemon[%d] %s: %s -> %s", id, field, from, to)
}
```

```go
// db_debug_off.go
func logFieldChange(Uint64Str, string, string, string) {}
```

Check where `changedFields` is read before deleting it — if something aggregates it into a single log line per save, preserve that behaviour in the debug build rather than switching to per-field logging.

- [ ] **Step 4: Verify both build tags compile**

```bash
go build -tags go_json ./...
go build -tags "go_json dbdebug" ./...
go test -tags go_json ./decoder/ ./decoder/nulltypes/
go test -tags "go_json dbdebug" ./decoder/
golangci-lint run
```

Expected: all clean. The `dbdebug` build is easy to break and nothing else exercises it.

- [ ] **Step 5: Update the pinned sizes to their final values**

Run the size test, take the reported numbers, and put them in `TestPokemonEntitySizes`. Record both in the commit message.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "perf: drop changedFields from the Pokemon struct

The slice was never allocated in production builds — dbDebugEnabled is a
build-tag const, so every append was const-folded away — but the 24-byte
header stayed in the struct and one of its words in the GC pointer bitmap.
The debug build now logs each change directly.

Adds TestPokemonUnderGCThreshold, the acceptance test for the packing work:
Pokemon must stay under 512 bytes, above which GC mark cost jumps 3.1x."
```

---

## Task 7: Measure the result

Not code. This is the step that tells you whether the previous six were worth it, and it is easy to skip.

- [ ] **Step 1: Confirm the size change**

```bash
go test -tags go_json -run 'TestPokemonEntitySizes|TestPokemonUnderGCThreshold' -v ./decoder/
```

Record the before and after: `PokemonData` 592 to roughly 210, `Pokemon` 800 to roughly 400. What matters is the size class — 800 falls in Go's 896-byte class and roughly 400 falls in the 416-byte class, so the allocator returns about 480 fewer bytes per cached pokemon.

- [ ] **Step 2: Confirm it on a real instance**

Set `profile_routes = true`, restart, let the cache fill, then:

```bash
curl -s -H "X-Golbat-Secret: $SECRET" "http://localhost:9001/debug/pprof/heap" > after.heap
go tool pprof -top -sample_index=inuse_space after.heap | head -30
```

Compare `decoder.PokemonData` and `decoder.Pokemon` against a capture from before the change. Also compare RSS.

- [ ] **Step 3: Check the clamp counter is silent**

```bash
curl -s -H "X-Golbat-Secret: $SECRET" http://localhost:9001/metrics | grep golbat_field_clamped_total
```

Expected: absent or zero. A non-zero value means a decode path is producing values outside its column's range — investigate before assuming the packing is at fault, since the clamp is new but the values are not.

- [ ] **Step 4: Record the outcome in the design doc**

Append a "Results" section to `docs/superpowers/specs/2026-08-16-pokemon-struct-packing-design.md` with the measured before and after sizes, the RSS delta, and the GC CPU share from a profile. The spec's own estimate was low single-digit percent CPU and roughly 2.4 GB at 5M cached pokemon — write down whether that held.

```bash
git add docs/superpowers/specs/2026-08-16-pokemon-struct-packing-design.md
git commit -m "docs: record measured results of the pokemon struct packing"
```

---

## Notes for whoever executes this

**The riskiest moment is Task 3 Step 7.** The compiler produces a wall of type errors and the temptation is to silence each one with the smallest possible cast. Resist widening a value and then narrowing it again somewhere else — if a call site wants an `int64`, ask whether it actually needs one, or whether it is passing through to something that would be happier with the narrow type.

**Do not update `decoder/api_pokemon_response_test.go`'s expected JSON string.** It asserts an exact serialisation of the API response. If your change makes that string need editing, you have changed a public contract — stop and reconsider. The one legitimate edit is to the input literals, which construct a `PokemonData` using `null.IntFrom(...)` and now need `nulltypes` constructors.

**The schema comment at `decoder/pokemon.go:88-140` lies.** Three of its claims are stale. Refresh it against `sql/*.up.sql` as part of Task 3 — it has already cost this design one wrong turn.

**If `TestPokemonUnderGCThreshold` fails at Task 6**, the fallback is to check `PokemonOldValues` (80 bytes, holds three nullable fields that could take the same treatment) and the embedded `grpc.PokemonInternal` (64 bytes). Both were left out of scope deliberately and either would provide the headroom.
