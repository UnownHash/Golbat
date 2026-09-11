# Fort ID Value Type + Username Persistence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the ~35-character `string` fort id with a 17-byte `FortId` value type everywhere fort ids live in memory, and make pokemon username persistence a default-off config option.

**Architecture:** Fort ids are structurally a 128-bit hex GUID plus an optional two-hex-digit suffix. `FortId` stores them as `struct{ Guid [16]byte; Suffix uint8 }` — comparable, pointer-free, usable directly as a map key and R-tree payload, with string conversion only at the DB/JSON/webhook boundaries. This removes fort id strings from the resident heap (measured ~28% GC mark reduction, 5.3M fewer heap objects at 2M forts). Conversion proceeds infrastructure-first (R-tree, then tracker) behind a temporary parse bridge, then entity-by-entity, each task compiling and passing the full suite on its own.

**Tech Stack:** Go 1.26, MariaDB, `github.com/tidwall/rtree`, `github.com/puzpuzpuz/xsync/v4`, `github.com/guregu/null/v6`, sqlx, koanf, logrus.

**Spec:** `docs/superpowers/specs/2026-08-18-fortid-value-type-design.md`

## Global Constraints

- **Base branch:** `c/golbat-memory-persistence-6846cc` (PR 394). Branch from it; do NOT branch from `c/golbat-string-interning` — this work replaces that PR's interning approach, and `interned_string.go` must never appear in the history.
- **Go version:** 1.26 (`go 1.26` in go.mod; CI pins `1.26.x`). Do NOT use the Go 1.27 `uuid` package.
- **No new dependencies.** Do not add `github.com/google/uuid` or any other module.
- **DB schema is unchanged.** Every fort id column stays `varchar(35)`. No migration files are added by this plan.
- **Wire formats are unchanged.** JSON API responses, webhook payloads, and gRPC fields keep byte-identical output. API and webhook DTO structs keep their existing `string` / `*string` field types; conversion happens in the builder functions, never by changing a DTO's type.
- **Suffix is not enumerated.** Any two lowercase hex digits parse. Never write a `switch` over `.11`/`.12`/`.16`/`.23`.
- **Parse failure is an error log plus skip.** There is no fallback representation and no side table. Never silently substitute the zero value for an unparseable id.
- **The zero `FortId` is the absent/"None" sentinel.** `ParseFortId` must never return it with `ok == true`.
- **Not every id in this codebase is a fort id.** `Incident.Id`, `FortLookupIncident.Id`, `Route.Id`, and the `partner_id` columns are all `varchar(35)`-ish strings that must stay `string`. Convert only pokestop/gym/station ids and references to them.
- **`db.FortId` already exists** (`db/pokestop.go`) as an sqlx row-scan struct. It is unrelated. Do not rename, reuse, or import it into `decoder`; the new type is `decoder.FortId` and the two never meet.
- **`FortId` implements `fmt.Stringer`**, so `%s` *and* `%v` both render the canonical string — existing log format verbs need no change. (`%v` only prints raw bytes for a struct *without* a `String()` method.)
- **Lock ordering:** never hold two entity locks simultaneously (see CLAUDE.md "Lock Ordering"). No task here changes locking, but conversions touch locked paths.
- **Build tags:** the production build uses `-tags go_json` (see `Makefile`). A `dbdebug` tag variant also exists. Every task must build and test green under both `go build ./...` and `go build -tags go_json ./...`.
- **Lint:** `golangci-lint` v2.12.2 must pass (see `.github/workflows/lint.yml`).

---

## File Structure

**Created:**
- `decoder/fortid.go` — the `FortId` type: parse, format, ordering, `driver.Valuer`, `sql.Scanner`, `encoding.TextMarshaler`/`TextUnmarshaler`. Sole owner of the string↔value conversion. No other file may hex-decode a fort id.
- `decoder/fortid_test.go` — round-trip, rejection, sentinel, ordering, adapter, and size tests.
- `decoder/fortid_bridge.go` — **temporary.** One helper, `fortIdFromLegacyString`, used while entity `Id` fields are still `string`. Deleted in Task 7; its deletion is the completion check for the conversion.

**Modified (by area):**
- `decoder/writebehind/typed_queue.go` — key constraint `cmp.Ordered` → `comparable` plus a `KeyCompare` comparator.
- `decoder/fortRtree.go`, `decoder/rtree_evictor.go` — lookup cache, tree, snapshot, evictor keyed by `FortId`.
- `decoder/fort_tracker.go`, `decode.go` — tracker maps, sets, channel payloads keyed by `FortId`.
- `decoder/pokestop.go`, `gym.go`, `station.go`, `fort.go`, `*_state.go`, `*_decode.go`, `*_process.go`, `gmo_decode.go` — entity `Id` fields and their call graphs.
- `decoder/station_battle.go` — `StationId` fields, cache, content hash.
- `decoder/incident.go`, `tappable.go`, `routes.go` — fort id references on satellite entities.
- `decoder/pokemon.go`, `pokemon_decode.go`, `pokemon_state.go` — `PokestopId`, and the username option.
- `decoder/main.go`, `preload.go`, `writebehind_batch.go` — cache and queue wiring.
- `decoder/api_*.go`, `routes_huma.go` — boundary conversion.
- `config/config.go` — `store_username`.
- `CLAUDE.md` — architecture notes.

---

### Task 1: The `FortId` value type

Standalone: no existing code changes, nothing calls it yet. Pure TDD.

**Files:**
- Create: `decoder/fortid.go`
- Test: `decoder/fortid_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces — every later task depends on exactly these:
  - `type FortId struct { Guid [16]byte; Suffix uint8 }`
  - `func ParseFortId(s string) (FortId, bool)`
  - `func (f FortId) Valid() bool`
  - `func (f FortId) String() string`
  - `func (f FortId) AppendText(b []byte) ([]byte, error)`
  - `func (f FortId) MarshalText() ([]byte, error)`
  - `func (f *FortId) UnmarshalText(b []byte) error`
  - `func (f FortId) Compare(o FortId) int`
  - `func (f FortId) Value() (driver.Value, error)`
  - `func (f *FortId) Scan(src any) error`

- [ ] **Step 1: Write the failing tests**

Create `decoder/fortid_test.go`:

```go
package decoder

import (
	"database/sql/driver"
	"math/rand/v2"
	"strings"
	"testing"
	"unsafe"
)

// genFortIdStrings builds ids in the shapes the production census found:
// 32 lowercase hex chars, optionally followed by '.' and two hex digits.
// Suffixes are NOT restricted to the observed set — the encoding must not
// privilege it.
func genFortIdStrings(t *testing.T, n int, seed uint64) []string {
	t.Helper()
	const hexDigits = "0123456789abcdef"
	suffixes := []string{"", ".11", ".12", ".16", ".23", ".99", ".ff", ".0a"}
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	out := make([]string, n)
	for i := range out {
		var sb strings.Builder
		for j := 0; j < 32; j++ {
			sb.WriteByte(hexDigits[r.IntN(16)])
		}
		sb.WriteString(suffixes[r.IntN(len(suffixes))])
		out[i] = sb.String()
	}
	return out
}

func TestFortIdSize(t *testing.T) {
	if got := unsafe.Sizeof(FortId{}); got != 17 {
		t.Fatalf("unsafe.Sizeof(FortId{}) = %d, want 17", got)
	}
}

func TestFortIdRoundTrip(t *testing.T) {
	// Real ids observed in production, including bare sponsored-fort ids.
	cases := []string{
		"3f4938f1348c2bc00973eeb715552b42",     // bare (sponsored fort)
		"5cd5bb656e03d9867dfe1f05e4d5892f",     // bare
		"0d2a2b1f8d3c4e5a6b7c8d9e0f1a2b3c.16",  // pokestop/gym
		"a1b2c3d4e5f60718293a4b5c6d7e8f90.11",  // pokestop/gym/station
		"deadbeefcafef00dfeedfacebadc0de5.12",  // pokestop/gym/station
		"763109934ddb4d98b9e0d09726be1950.23",  // station
		"00000000000000000000000000000001",     // minimum nonzero bare
		"ffffffffffffffffffffffffffffffff.ff",  // maximum
	}
	for _, s := range cases {
		f, ok := ParseFortId(s)
		if !ok {
			t.Fatalf("ParseFortId(%q) = not ok, want ok", s)
		}
		if !f.Valid() {
			t.Fatalf("ParseFortId(%q).Valid() = false, want true", s)
		}
		if got := f.String(); got != s {
			t.Fatalf("round trip %q -> %q", s, got)
		}
	}
	for _, s := range genFortIdStrings(t, 5000, 1) {
		f, ok := ParseFortId(s)
		if !ok {
			t.Fatalf("ParseFortId(%q) = not ok, want ok", s)
		}
		if got := f.String(); got != s {
			t.Fatalf("round trip %q -> %q", s, got)
		}
	}
}

func TestFortIdParseRejects(t *testing.T) {
	// Each of these must fail to parse. The empty string is the one
	// nonconforming id that exists in production today.
	bad := []string{
		"",                                       // empty (production junk row)
		"not-a-fort-id",                          // wrong shape
		"3f4938f1348c2bc00973eeb715552b4",        // 31 chars
		"3f4938f1348c2bc00973eeb715552b422",      // 33 chars
		"3f4938f1348c2bc00973eeb715552b42.",      // trailing dot only
		"3f4938f1348c2bc00973eeb715552b42.1",     // one suffix digit
		"3f4938f1348c2bc00973eeb715552b42.161",   // three suffix digits
		"3f4938f1348c2bc00973eeb715552b42x16",    // separator not '.'
		"3F4938F1348C2BC00973EEB715552B42.16",    // uppercase hex
		"zzzz38f1348c2bc00973eeb715552b42.16",    // non-hex guid
		"3f4938f1348c2bc00973eeb715552b42.zz",    // non-hex suffix
		"3f4938f1348c2bc00973eeb715552b42.1G",    // partially non-hex suffix
		"00000000000000000000000000000000",       // all-zero bare == sentinel
		"00000000000000000000000000000000.00",    // canonicalizes to sentinel
	}
	for _, s := range bad {
		if f, ok := ParseFortId(s); ok {
			t.Fatalf("ParseFortId(%q) = (%v, true), want not ok", s, f)
		}
	}
}

func TestFortIdZeroValueIsAbsent(t *testing.T) {
	var zero FortId
	if zero.Valid() {
		t.Fatal("zero FortId must be invalid (it is the None sentinel)")
	}
	if got := zero.String(); got != "" {
		t.Fatalf("zero FortId String() = %q, want empty", got)
	}
	v, err := zero.Value()
	if err != nil {
		t.Fatalf("zero FortId Value() error: %v", err)
	}
	if v != nil {
		t.Fatalf("zero FortId Value() = %v, want nil (SQL NULL)", v)
	}
}

// A literal ".00" suffix means the same id as the bare form: bare is
// Niantic's stripped null suffix. Never observed in any census; pinned so
// the canonicalization is deliberate rather than accidental.
func TestFortIdDotZeroCanonicalizesToBare(t *testing.T) {
	withSuffix, ok := ParseFortId("a1b2c3d4e5f60718293a4b5c6d7e8f90.00")
	if !ok {
		t.Fatal("ParseFortId of a .00 id = not ok, want ok")
	}
	bare, ok := ParseFortId("a1b2c3d4e5f60718293a4b5c6d7e8f90")
	if !ok {
		t.Fatal("ParseFortId of the bare id = not ok, want ok")
	}
	if withSuffix != bare {
		t.Fatalf(".00 id %v does not equal bare id %v", withSuffix, bare)
	}
	if got := withSuffix.String(); got != "a1b2c3d4e5f60718293a4b5c6d7e8f90" {
		t.Fatalf(".00 id formats as %q, want the bare form", got)
	}
}

// Byte order must match varchar order: the write-behind queue's
// deterministic lock-ordering sort and any in-memory sorted iteration have
// to stay congruent with the database's ORDER BY id.
func TestFortIdCompareMatchesStringOrder(t *testing.T) {
	ids := genFortIdStrings(t, 400, 7)
	// Include a bare/suffixed pair sharing a GUID: the shorter string must
	// sort first, which the zero suffix byte gives us.
	ids = append(ids, ids[0][:32], ids[0][:32]+".01")
	for i := 0; i < len(ids); i++ {
		a, ok := ParseFortId(ids[i])
		if !ok {
			t.Fatalf("ParseFortId(%q) failed", ids[i])
		}
		for j := 0; j < len(ids); j++ {
			b, ok := ParseFortId(ids[j])
			if !ok {
				t.Fatalf("ParseFortId(%q) failed", ids[j])
			}
			want := strings.Compare(ids[i], ids[j])
			if got := a.Compare(b); got != want {
				t.Fatalf("Compare(%q, %q) = %d, want %d", ids[i], ids[j], got, want)
			}
		}
	}
}

func TestFortIdValuerScanner(t *testing.T) {
	const s = "a1b2c3d4e5f60718293a4b5c6d7e8f90.16"
	f, ok := ParseFortId(s)
	if !ok {
		t.Fatal("ParseFortId failed")
	}

	// Value must produce the exact varchar the column has always held —
	// a string, not the raw bytes. If this regressed to []byte or an
	// integer, database/sql would silently write garbage into varchar(35).
	v, err := f.Value()
	if err != nil {
		t.Fatalf("Value() error: %v", err)
	}
	got, isString := v.(string)
	if !isString {
		t.Fatalf("Value() returned %T (%v), want string", v, v)
	}
	if got != s {
		t.Fatalf("Value() = %q, want %q", got, s)
	}
	if _, err := driver.String.ConvertValue(v); err != nil {
		t.Fatalf("Value() is not a valid driver.Value: %v", err)
	}

	// Scan accepts both driver representations of a varchar.
	for _, src := range []any{s, []byte(s)} {
		var scanned FortId
		if err := scanned.Scan(src); err != nil {
			t.Fatalf("Scan(%T) error: %v", src, err)
		}
		if scanned != f {
			t.Fatalf("Scan(%T) = %v, want %v", src, scanned, f)
		}
	}

	var fromNull FortId
	if err := fromNull.Scan(nil); err != nil {
		t.Fatalf("Scan(nil) error: %v", err)
	}
	if fromNull.Valid() {
		t.Fatal("Scan(nil) must produce the invalid zero value")
	}

	// A malformed id must return an error so the caller's per-row handler
	// logs and skips it (decoder/preload.go already does exactly that).
	var bad FortId
	if err := bad.Scan("not-a-fort-id"); err == nil {
		t.Fatal("Scan of a malformed id must return an error")
	}
}

func TestFortIdTextMarshaling(t *testing.T) {
	const s = "a1b2c3d4e5f60718293a4b5c6d7e8f90.23"
	f, ok := ParseFortId(s)
	if !ok {
		t.Fatal("ParseFortId failed")
	}
	b, err := f.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText error: %v", err)
	}
	if string(b) != s {
		t.Fatalf("MarshalText = %q, want %q", b, s)
	}

	var back FortId
	if err := back.UnmarshalText([]byte(s)); err != nil {
		t.Fatalf("UnmarshalText error: %v", err)
	}
	if back != f {
		t.Fatalf("UnmarshalText = %v, want %v", back, f)
	}

	// A failed unmarshal must not partially write the receiver.
	before := f
	if err := f.UnmarshalText([]byte("garbage")); err == nil {
		t.Fatal("UnmarshalText of garbage must return an error")
	}
	if f != before {
		t.Fatalf("failed UnmarshalText mutated the receiver: %v != %v", f, before)
	}
}

func TestFortIdAppendTextDoesNotAllocate(t *testing.T) {
	f, ok := ParseFortId("a1b2c3d4e5f60718293a4b5c6d7e8f90.16")
	if !ok {
		t.Fatal("ParseFortId failed")
	}
	buf := make([]byte, 0, 35)
	allocs := testing.AllocsPerRun(100, func() {
		out, err := f.AppendText(buf[:0])
		if err != nil || len(out) != 35 {
			t.Fatalf("AppendText: len=%d err=%v", len(out), err)
		}
	})
	if allocs != 0 {
		t.Fatalf("AppendText into a sized buffer allocated %v times, want 0", allocs)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./decoder/ -run 'TestFortId' -v 2>&1 | head -30`
Expected: compile failure — `undefined: FortId`, `undefined: ParseFortId`.

- [ ] **Step 3: Write the implementation**

Create `decoder/fortid.go`:

```go
package decoder

import (
	"bytes"
	"cmp"
	"database/sql/driver"
	"encoding/hex"
	"fmt"
	"sync"

	log "github.com/sirupsen/logrus"
)

// FortId is the in-memory representation of a fort id — the identifier
// shared by pokestops, gyms and stations, stored in the database as
// varchar(35).
//
// The string form is a 128-bit identifier rendered as 32 lowercase hex
// characters (Ingress portal GUID heritage), optionally followed by '.'
// and two more hex digits. Holding that as a Go string costs a 16-byte
// header plus a ~35-byte heap object per copy, and fort ids are held in
// six places per fort (entity, cache key, lookup key, R-tree payload, and
// two fort-tracker maps) plus once per cached pokemon. As a fixed-width
// value this is 17 pointer-free bytes that live inline in whatever holds
// them — measured at 2M forts: ~28% less GC mark time and 5.3M fewer heap
// objects. See docs/superpowers/specs/2026-08-18-fortid-value-type-design.md.
//
// The zero value is the absent/"None" sentinel, which is why ParseFortId
// refuses to produce it: FortId replaces null.String on optional fields,
// so "no fort" and "some fort" must never share a representation.
type FortId struct {
	Guid [16]byte
	// Suffix holds the two-hex-digit suffix's value directly; 0 means the
	// bare 32-character form.
	//
	// Bare ids are a live shape, not legacy junk — a production census
	// found them on sponsored forts (EE, Community Ambassador Location),
	// updated within the hour. They are treated as Niantic's null suffix,
	// so a literal ".00" is the same id as the bare form and canonicalizes
	// to it (never observed; see fortIdDotZeroWarn).
	//
	// Nothing is enumerated. The observed set is .11/.12/.16 (pokestops,
	// gyms, and occasionally stations) and .23 (stations), but any two
	// lowercase hex digits parse, so a new Niantic suffix needs no code
	// change — and the encoding is correct whether their scheme is decimal
	// or hexadecimal.
	//
	// Storing the value raw (rather than an enum, or value+1) is what makes
	// byte order match varchar order: the bare form sorts first because its
	// suffix byte is 0 and its string is the shorter prefix, and lowercase
	// hex digits ascend in ASCII exactly as they ascend in value, so the
	// fixed-width pair's lexicographic order equals its numeric order.
	// TestFortIdCompareMatchesStringOrder pins this.
	Suffix uint8
}

const fortIdHexDigits = "0123456789abcdef"

// fortIdNibble maps a byte to its hex value, or -1. Deliberately lowercase
// only: strict parsing keeps parse and format exact inverses, which is what
// the ordering guarantee above rests on. (This is why the hand-rolled table
// is used instead of encoding/hex.Decode, which accepts uppercase.)
var fortIdNibble = func() (t [256]int8) {
	for i := range t {
		t[i] = -1
	}
	for c := byte('0'); c <= '9'; c++ {
		t[c] = int8(c - '0')
	}
	for c := byte('a'); c <= 'f'; c++ {
		t[c] = int8(c-'a') + 10
	}
	return
}()

// fortIdDotZeroWarn fires at most once per process: a ".00" suffix has
// never been observed, and its appearance would be the first evidence that
// bare ids really are the stripped zero suffix.
var fortIdDotZeroWarn sync.Once

// ParseFortId converts the canonical string form of a fort id.
//
// ok is false for anything structurally malformed — wrong length, non-hex,
// uppercase, bad separator — and for the two forms that would collide with
// the zero-value sentinel (an all-zero GUID with no suffix, and its ".00"
// spelling). Callers must log the failure and skip the update or row; there
// is no fallback representation.
func ParseFortId(s string) (FortId, bool) {
	var f FortId
	switch len(s) {
	case 32:
	case 35:
		if s[32] != '.' {
			return FortId{}, false
		}
		hi, lo := fortIdNibble[s[33]], fortIdNibble[s[34]]
		if hi < 0 || lo < 0 {
			return FortId{}, false
		}
		f.Suffix = byte(hi)<<4 | byte(lo)
		if f.Suffix == 0 {
			fortIdDotZeroWarn.Do(func() {
				log.Warnf("[FORTID] fort id %q has a .00 suffix, which has never been observed; "+
					"treating it as the bare form (see decoder/fortid.go)", s)
			})
		}
	default:
		return FortId{}, false
	}
	for i := 0; i < 16; i++ {
		hi, lo := fortIdNibble[s[2*i]], fortIdNibble[s[2*i+1]]
		if hi < 0 || lo < 0 {
			return FortId{}, false
		}
		f.Guid[i] = byte(hi)<<4 | byte(lo)
	}
	if f == (FortId{}) {
		// Reserved: the zero value means "no fort".
		return FortId{}, false
	}
	return f, true
}

// Valid reports whether f identifies a fort. The zero value does not.
func (f FortId) Valid() bool {
	return f != FortId{}
}

// AppendText implements encoding.TextAppender. Callers holding a buffer
// (batch JSON encoding, SQL argument building) format with no allocation.
func (f FortId) AppendText(b []byte) ([]byte, error) {
	if !f.Valid() {
		return b, nil
	}
	off := len(b)
	b = append(b, "00000000000000000000000000000000"...)
	hex.Encode(b[off:], f.Guid[:])
	if f.Suffix != 0 {
		b = append(b, '.', fortIdHexDigits[f.Suffix>>4], fortIdHexDigits[f.Suffix&0xf])
	}
	return b, nil
}

// String returns the canonical string form, or "" for the zero value.
func (f FortId) String() string {
	if !f.Valid() {
		return ""
	}
	var buf [35]byte
	out, _ := f.AppendText(buf[:0])
	return string(out)
}

// MarshalText implements encoding.TextMarshaler, which is what gives
// FortId its JSON representation (a plain string) in both encoding/json
// and goccy.
func (f FortId) MarshalText() ([]byte, error) {
	return f.AppendText(make([]byte, 0, 35))
}

// UnmarshalText implements encoding.TextUnmarshaler. It parses into a local
// and assigns only on success, so a failed unmarshal never leaves a
// half-written receiver.
func (f *FortId) UnmarshalText(b []byte) error {
	if len(b) == 0 {
		*f = FortId{}
		return nil
	}
	parsed, ok := ParseFortId(string(b))
	if !ok {
		return fmt.Errorf("FortId: cannot parse %q", b)
	}
	*f = parsed
	return nil
}

// Compare orders fort ids identically to the varchar column they are stored
// in; see the Suffix field comment.
func (f FortId) Compare(o FortId) int {
	if c := bytes.Compare(f.Guid[:], o.Guid[:]); c != 0 {
		return c
	}
	return cmp.Compare(f.Suffix, o.Suffix)
}

// Value implements driver.Valuer, writing the varchar the column has always
// held. The zero value writes SQL NULL.
func (f FortId) Value() (driver.Value, error) {
	if !f.Valid() {
		return nil, nil
	}
	return f.String(), nil
}

// Scan implements sql.Scanner.
//
// A malformed id returns an error rather than degrading, so the row is
// skipped by the caller's per-row error handling (decoder/preload.go's
// scan loops already log and continue) instead of entering memory under a
// wrong or sentinel id.
func (f *FortId) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*f = FortId{}
		return nil
	case string:
		return f.UnmarshalText([]byte(v))
	case []byte:
		return f.UnmarshalText(v)
	default:
		return fmt.Errorf("FortId.Scan: unsupported type %T", src)
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./decoder/ -run 'TestFortId' -v`
Expected: all ten tests PASS.

- [ ] **Step 5: Verify the whole package and lint are still green**

Run: `go build ./... && go build -tags go_json ./... && go vet ./... && go test ./decoder/ -count=1 && golangci-lint run`
Expected: build succeeds under both tag sets, all existing tests pass, no lint findings.

- [ ] **Step 6: Commit**

```bash
git add decoder/fortid.go decoder/fortid_test.go
git commit -m "feat: add FortId, a 17-byte value type for fort ids

Fort ids are a 128-bit hex GUID plus an optional two-hex-digit suffix.
As a fixed-width value they are pointer-free and live inline in caches,
map keys and R-tree payloads instead of costing a heap object per copy.
The zero value is the absent sentinel; parse refuses to produce it.
No callers yet."
```

---

### Task 2: Write-behind queue key constraint

Mechanical and behavior-neutral: `[35]byte`-shaped keys cannot satisfy `cmp.Ordered`, so the queue takes an explicit comparator. Existing queues pass `cmp.Compare` and sort exactly as before.

**Files:**
- Modify: `decoder/writebehind/typed_queue.go` (lines 24, 35, 50, 83, 305-307)
- Modify: `decoder/writebehind_batch.go` (every `NewTypedQueue` config literal)
- Test: `decoder/writebehind/queue_test.go`

**Interfaces:**
- Consumes: nothing from Task 1.
- Produces: `TypedQueueConfig[K comparable, T any]` gains a required field `KeyCompare func(a, b K) int`. `Entry`, `TypedQueue`, and `NewTypedQueue` are all `[K comparable, T any]`.

- [ ] **Step 1: Write the failing test**

Append to `decoder/writebehind/queue_test.go`:

```go
// A key type that is comparable but NOT cmp.Ordered — the shape FortId has.
// This test exists to prove the queue no longer requires an ordered key, and
// that the flush order follows KeyCompare.
type arrayKey [3]byte

func TestTypedQueueAcceptsNonOrderedKeyAndSortsByKeyCompare(t *testing.T) {
	var flushed [][]arrayKey
	var mu sync.Mutex

	q := NewTypedQueue(TypedQueueConfig[arrayKey, arrayKey]{
		Name:         "arraykey-test",
		BatchSize:    3,
		BatchTimeout: 10 * time.Millisecond,
		Limiter:      NewSharedLimiter(1),
		FlushFunc: func(ctx context.Context, _ db.DbDetails, entries []arrayKey) error {
			mu.Lock()
			defer mu.Unlock()
			flushed = append(flushed, append([]arrayKey(nil), entries...))
			return nil
		},
		KeyFunc:    func(d arrayKey) arrayKey { return d },
		KeyCompare: func(a, b arrayKey) int { return bytes.Compare(a[:], b[:]) },
	})
	defer q.Stop()

	// Enqueue out of order; the flush must see them sorted.
	for _, k := range []arrayKey{{3, 0, 0}, {1, 0, 0}, {2, 0, 0}} {
		q.Enqueue(k, k, false, 0)
	}

	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		done := len(flushed) > 0
		mu.Unlock()
		if done {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for flush")
		case <-time.After(5 * time.Millisecond):
		}
	}

	mu.Lock()
	defer mu.Unlock()
	got := flushed[0]
	want := []arrayKey{{1, 0, 0}, {2, 0, 0}, {3, 0, 0}}
	if len(got) != len(want) {
		t.Fatalf("flushed %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("flush order[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}
```

Add `"bytes"`, `"context"`, `"sync"`, `"time"` and the `golbat/db` import to the test file's import block if not already present. If the existing test file's `NewTypedQueue` helper calls differ in signature (for example a different `Enqueue` arity), match the existing calls in that file rather than the sketch above — the point of the test is the key type and the sort order, not the enqueue shape.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./decoder/writebehind/ -run TestTypedQueueAcceptsNonOrderedKey -v 2>&1 | head -20`
Expected: compile failure — `arrayKey does not satisfy cmp.Ordered`, and `unknown field KeyCompare`.

- [ ] **Step 3: Change the constraint and add the comparator**

In `decoder/writebehind/typed_queue.go`:

1. Change the three generic declarations and the constructor from `[K cmp.Ordered, T any]` to `[K comparable, T any]`:
   - `type Entry[K comparable, T any] struct` (line ~24)
   - `type TypedQueueConfig[K comparable, T any] struct` (line ~35)
   - `type TypedQueue[K comparable, T any] struct` (line ~50)
   - `func NewTypedQueue[K comparable, T any](cfg TypedQueueConfig[K, T]) *TypedQueue[K, T]` (line ~83)

2. In `TypedQueueConfig`, immediately after the `KeyFunc` field, add:

```go
	// KeyCompare orders keys for the deterministic lock ordering applied
	// before each flush (see the sort in flushBatch). Required.
	//
	// This exists because the key constraint is `comparable`, not
	// `cmp.Ordered`: FortId is a fixed-width struct, which no ordered
	// constraint admits. Pass cmp.Compare for scalar keys; FortId.Compare
	// for fort ids, which orders identically to the varchar column.
	KeyCompare func(a, b K) int
```

3. Add a matching `keyCompare func(a, b K) int` field to the `TypedQueue` struct next to `keyFunc`, populate it in `NewTypedQueue` from `cfg.KeyCompare`, and fail loudly on a nil comparator at construction (a nil here would panic later, inside a flush, on a path that is hard to attribute):

```go
	if cfg.KeyCompare == nil {
		log.Fatalf("[WRITEBEHIND] queue %s constructed without KeyCompare", cfg.Name)
	}
```

4. Replace the sort at lines ~305-307:

```go
	// Sort entries by key to ensure consistent lock ordering and avoid deadlocks
	slices.SortFunc(entries, func(a, b *Entry[K, T]) int {
		return q.keyCompare(a.Key, b.Key)
	})
```

5. Remove the now-unused `"cmp"` import if nothing else in the file uses it.

- [ ] **Step 4: Pass `KeyCompare` at every construction site**

In `decoder/writebehind_batch.go`, every `NewTypedQueue(TypedQueueConfig[...]{...})` literal gains a `KeyCompare` field directly below its `KeyFunc`. For the string-keyed queues (pokestop, gym, route, station, station battle, incident) and the scalar-keyed ones (pokemon `uint64`, spawnpoint `int64`) this is `cmp.Compare`:

```go
		KeyFunc:    func(d PokestopData) string { return d.Id },
		KeyCompare: cmp.Compare[string],
```

Add `"cmp"` to that file's imports. Find every site with:

Run: `grep -n "KeyFunc:" decoder/writebehind_batch.go`

Every line listed must gain a `KeyCompare` on the following line. There is no default: Step 3's `log.Fatalf` makes a missed site fail immediately at startup rather than silently.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./decoder/writebehind/ -count=1 -v -run TestTypedQueue`
Expected: the new test and all existing queue tests PASS.

- [ ] **Step 6: Verify the whole build, suite and lint**

Run: `go build ./... && go build -tags go_json ./... && go vet ./... && go test ./... -count=1 && golangci-lint run`
Expected: green. Watch for any `NewTypedQueue` site outside `writebehind_batch.go` the grep missed — the compiler will name it.

- [ ] **Step 7: Commit**

```bash
git add decoder/writebehind/typed_queue.go decoder/writebehind/queue_test.go decoder/writebehind_batch.go
git commit -m "refactor: write-behind queue keys are comparable, not cmp.Ordered

The deadlock-avoidance sort now takes an explicit KeyCompare instead of
cmp.Compare, so a fixed-width struct key can be used. Every existing queue
passes cmp.Compare and sorts exactly as before. A nil comparator is fatal
at construction rather than a panic inside a later flush."
```

---

### Task 3: Fort R-tree and lookup cache keyed by `FortId`

The lookup cache, spatial tree, snapshot and evictor convert together — they are one subsystem. Entity `Id` fields are still `string` at this point, so calls in from the entity layer parse through a temporary bridge, removed in Task 7.

**Files:**
- Create: `decoder/fortid_bridge.go` (temporary; deleted in Task 7)
- Modify: `decoder/fortRtree.go`
- Modify: `decoder/quest_conditions.go` (keyed by fort id; converts with the tree — see Step 3)
- Modify: `decoder/api_fort.go`, `decoder/api_pokestop.go` (the two tree-search closures and `CollectPokestopIncidents`, which read `fortLookupCache` directly)
- Test: `decoder/fortRtree_compute_test.go`, `decoder/fortRtree_evict_test.go`, `decoder/rtree_snapshot_test.go`, `decoder/rtree_eviction_race_test.go`, `decoder/fort_availability_hooks_test.go`, `decoder/fort_incident_id_test.go`, `decoder/api_pokestop_incidents_test.go`, `decoder/quest_conditions_test.go`

**Interfaces:**
- Consumes: `FortId`, `ParseFortId` (Task 1).
- Produces:
  - `func fortIdFromLegacyString(id string, context string) (FortId, bool)` — temporary bridge.
  - `var fortLookupCache *xsync.Map[FortId, FortLookup]`
  - `var fortTree rtree.RTreeG[FortId]`, `var fortTreeEvictor *treeEvictor[FortId]`
  - `func genericUpdateFort(id FortId, lat, lon float64, deleted bool)`
  - `func addFortToTree(id FortId, lat, lon float64)` / `func removeFortFromTree(id FortId, lat, lon float64)`
  - `func deferFortEviction(expected FortType, fortId FortId, lat, lon float64)`
  - `func updatePokestopIncidentLookup(pokestopId FortId, incident *Incident)`
  - `func reconcileFortQuestConditions(fortId FortId, newKeys []questConditionKey)` / `func removeFortQuestConditions(fortId FortId)`

**Note:** `GetFortLookup` and `GetFortsInBounds` are exported but have **zero callers** anywhere in the repo. Convert their signatures for consistency; do not go looking for call sites. `decoder/rtree_evictor.go` needs **no changes at all** — `treeEvictor[K comparable]` and `flushTreeEvictions[K comparable]` are already generic, so only the instantiation in `initFortRtree` changes.

- [ ] **Step 1: Create the temporary bridge**

Create `decoder/fortid_bridge.go`:

```go
package decoder

import (
	log "github.com/sirupsen/logrus"
)

// fortIdFromLegacyString parses a fort id that is still held as a string
// somewhere upstream, logging the structural failure the way any other
// unexpected data format is logged.
//
// THIS FILE IS TEMPORARY. It exists only while the fort-id conversion is
// in progress: entity Id fields become FortId in Tasks 5-7, at which point
// every call here disappears and this file is deleted. `grep -rn
// fortIdFromLegacyString` returning nothing is the completion check for the
// conversion. Do not add new callers, and do not use it as a general
// "parse or ignore" helper.
func fortIdFromLegacyString(id string, context string) (FortId, bool) {
	f, ok := ParseFortId(id)
	if !ok {
		log.Errorf("[FORTID] %s: unparseable fort id %q, skipping", context, id)
	}
	return f, ok
}
```

- [ ] **Step 2: Convert the R-tree subsystem's storage and signatures**

In `decoder/fortRtree.go`:

1. Storage declarations (lines ~66-73):

```go
var fortLookupCache *xsync.Map[FortId, FortLookup]
var fortTreeMutex sync.RWMutex
var fortTree rtree.RTreeG[FortId]

var fortTreeSnapshot atomic.Pointer[treeSnapshot[FortId]]

func getFortTreeSnapshot() *rtree.RTreeG[FortId] {
	return refreshTreeSnapshot(&fortTreeSnapshot, &fortTreeMutex, &fortTree)
}
```

2. In `initFortRtree()`, the evictor and cache construction:

```go
	fortTreeEvictor = newTreeEvictor[FortId]("fort", 65536, treeEvictorBatchSize, flushFortTreeEvictions)
	fortLookupCache = xsync.NewMap[FortId, FortLookup]()
```

`treeEvictor[K comparable]` in `decoder/rtree_evictor.go` is already generic over any comparable key and needs **no changes** — only this instantiation changes.

3. In `initFortRtree()`, the three eviction callbacks parse through the bridge (the cache key is still `string` until Task 5/6):

```go
	if config.Config.FortInMemory {
		pokestopCache.OnEviction(func(_ string, p *Pokestop, _ ottercache.EvictionReason) {
			if id, ok := fortIdFromLegacyString(p.Id, "pokestop eviction"); ok {
				deferFortEviction(POKESTOP, id, p.Lat, p.Lon)
			}
		})
		gymCache.OnEviction(func(_ string, g *Gym, _ ottercache.EvictionReason) {
			if id, ok := fortIdFromLegacyString(g.Id, "gym eviction"); ok {
				deferFortEviction(GYM, id, g.Lat, g.Lon)
			}
		})
	}

	stationCache.OnEviction(func(stationId string, s *Station, _ ottercache.EvictionReason) {
		clearStationBattleState(stationId)
		if config.Config.FortInMemory {
			if id, ok := fortIdFromLegacyString(s.Id, "station eviction"); ok {
				deferFortEviction(STATION, id, s.Lat, s.Lon)
			}
		}
	})
```

4. Change the id parameter type from `string` to `FortId` on: `genericUpdateFort`, `addFortToTree`, `removeFortFromTree`, `deferFortEviction`, `GetFortLookup`, and the `[]string` return of `GetFortsInBounds` to `[]FortId`. Their bodies need no other change — the tree, lookup cache and evictor are all now `FortId`-keyed.

5. The `fortRtreeUpdate*OnSave` / `OnGet` functions and the `update*Lookup` functions take entities whose `Id` is still `string`. Each parses once at the top and returns on failure. For example:

```go
func fortRtreeUpdatePokestopOnSave(pokestop *Pokestop) {
	id, ok := fortIdFromLegacyString(pokestop.Id, "pokestop rtree update")
	if !ok {
		return
	}
	genericUpdateFort(id, pokestop.Lat, pokestop.Lon, pokestop.Deleted)
	if !pokestop.Deleted {
		updatePokestopLookup(id, pokestop)
	}
	// ...rest of the existing body, using `id` wherever pokestop.Id was used
}
```

Apply the same shape to the gym, station and incident equivalents. Where an `update*Lookup` helper previously derived the key from the entity, give it an explicit `id FortId` first parameter so the parse happens exactly once per call chain rather than once per helper.

6. Delete the dead `IdRecord` type (lines ~108-110) — it has no references repo-wide, and leaving a `string`-typed fort id record around invites re-use.

- [ ] **Step 3: Convert `quest_conditions.go` with it**

`decoder/quest_conditions.go` is keyed by fort id and is called from inside `fortRtree.go` (at the `updatePokestopLookup`, `fortRtreeUpdatePokestopOnSave` and `deferFortEviction` sites), so it converts in the same task:

```go
var questFortKeys *xsync.Map[FortId, []questConditionKey]
```

and `reconcileFortQuestConditions(fortId FortId, ...)` / `removeFortQuestConditions(fortId FortId)`. Both bodies only use the parameter as a `Compute` key, so no other change is needed.

- [ ] **Step 4: Convert the scan-path callers**

In `decoder/api_fort.go`, `internalGetForts` (signature at ~292) and `internalGetFortsCombined` (~488):
- the `seen` / `seenCombined` dedupe maps become `map[FortId]struct{}`
- the two `fortTreeCopy.Search(..., func(min, max [2]float64, fortId string) bool` closures take `fortId FortId` — these are explicit type annotations, so the compiler will not catch a missed one by inference
- `returnKeys []string` and the named returns `(gymKeys, pokestopKeys, stationKeys []string, ...)` become `[]FortId`, as do the `keys []string` parameters of `collectGymResults`, `collectStationResults` and `collectPokestopResults`
- where a matched id is handed to an entity lookup that still takes a string (`GetGymRecordReadOnly`, `GetStationRecordReadOnly`, `getPokestopRecordReadOnly`), pass `key.String()`; these `.String()` calls disappear in Tasks 5-6
- where a fort id is written into an API result DTO, keep the DTO field as `string` and assign `id.String()`

In `decoder/api_pokestop.go`, `CollectPokestopIncidents(ctx, dbDetails, fortId string, now int64)` uses `fortId` solely as a `fortLookupCache` key, so its parameter becomes `FortId`. Careful: the `li.Id` it reads out of `FortLookup.Incidents` is an **incident** id and stays a `string`.

Find every remaining site with:

Run: `go build ./... 2>&1 | head -40` — the compiler enumerates them precisely; work through the list.

- [ ] **Step 5: Update the affected existing tests**

The R-tree and API scan tests construct fort ids as string literals — several of which are not valid fort ids at all (`fortRtree_evict_test.go` uses `"fort-evict-a"`, `api_pokestop_incidents_test.go` uses `"s1"`). Each literal used as a *key* must become a `FortId` built from a valid id. Use a test helper rather than repeating the parse:

Add to `decoder/fortid_test.go`:

```go
// mustFortId is for tests that need a FortId from a literal.
func mustFortId(t *testing.T, s string) FortId {
	t.Helper()
	f, ok := ParseFortId(s)
	if !ok {
		t.Fatalf("mustFortId: %q is not a valid fort id", s)
	}
	return f
}
```

Test fixtures using short fake ids like `"abc123"` must be replaced with valid 32-hex ids — that is not a workaround, it is the tests catching up with the fact that fort ids have a defined shape. Where a test needs several distinct ids, vary the last hex digits:
`"00000000000000000000000000000001.16"`, `"...02.16"`, and so on.

The test files that reach into this subsystem, so none is missed: `fortRtree_compute_test.go`, `fortRtree_evict_test.go` (also constructs `[]treeEvictionEntry[string]` literals), `rtree_snapshot_test.go`, `rtree_eviction_race_test.go`, `fort_availability_hooks_test.go`, `fort_incident_id_test.go`, `api_pokestop_incidents_test.go`, `quest_conditions_test.go`, `station_battle_test.go`.

Run: `go test ./decoder/ -count=1 2>&1 | head -40` and fix each failure in turn.

- [ ] **Step 6: Verify build, suite, race and lint**

Run: `go build ./... && go build -tags go_json ./... && go vet ./... && go test ./decoder/ -count=1 && go test ./decoder/ -race -count=1 -run 'Rtree|Tree|Evict|Snapshot|Fort' && golangci-lint run`
Expected: green throughout. The `-race` subset matters here: the evictor and snapshot paths are concurrent.

- [ ] **Step 7: Commit**

```bash
git add decoder/fortid_bridge.go decoder/fortid.go decoder/fortid_test.go decoder/fortRtree.go decoder/quest_conditions.go decoder/api_fort.go decoder/api_pokestop.go decoder/*_test.go
git commit -m "refactor: key the fort R-tree and lookup cache by FortId

The lookup map, spatial tree, snapshot and evictor now hold 17-byte
values instead of ~35-byte string keys: no per-entry heap object, no
pointer for the GC to mark in tree nodes, no string hashing per scan
candidate. Entity Id fields are still strings, so calls in from the
entity layer parse through the temporary fortid_bridge.go, which Task 7
deletes."
```

---

### Task 4: Fort tracker keyed by `FortId`

Independent of the R-tree: the tracker is fed from GMO decode, not from the save path.

**Files:**
- Modify: `decoder/fort_tracker.go`
- Modify: `decode.go` (the `cellForts` build, ~lines 489-514 and the dispatch at ~561)
- Modify: `routes_huma.go` (fort-tracker API handlers)
- Test: `decoder/fort_tracker_test.go`

**Interfaces:**
- Consumes: `FortId`, `fortIdFromLegacyString`.
- Produces:
  - `FortTracker.forts map[FortId]*FortTrackerLastSeen`
  - `FortTrackerCellState.pokestops`, `.gyms` — `map[FortId]struct{}`
  - `FortTrackerGMOContents{ Pokestops []FortId; Gyms []FortId; Timestamp int64 }`
  - `CellUpdateResult` fields — `[]FortId`
  - `RegisterFort(fortId FortId, cellId uint64, isGym bool, updatedTimestamp int64)` (keep the existing parameter list, only the id type changes)
  - `fortKindOps[T].loadForUpdate func(context.Context, db.DbDetails, FortId, string) (T, func(), error)`

- [ ] **Step 1: Convert the tracker's data model**

In `decoder/fort_tracker.go`, change the id type in: the `forts` map, `FortTrackerCellState`'s two sets, `FortTrackerGMOContents`, `CellUpdateResult`'s four slices, the per-GMO transient sets (`currentPokestops`, `currentGyms`, `pendingPokestops`, `pendingGyms`), and the signatures of `RegisterFort`, `RemoveFort`, `RestoreFort`, `ProcessCellUpdate`, `processCellUpdateLocked`, `applyPresentForts`, `GetFortInfo`, `clearFortWithLock`, `CheckRemovedForts`, `checkRemovedForts`.

Three sites need care rather than a mechanical swap:

**a. The keyset-paginated DB load** (`loadFortKindFromDB`). The query pages by the varchar column:

```sql
SELECT id, cell_id, updated FROM %s WHERE deleted = 0 AND cell_id IS NOT NULL AND id > ? ORDER BY id LIMIT ?
```

It loads a whole 30 000-row batch with `SelectContext` into `[]fortRow`, so `fortRow.Id` **must stay a `string`** — giving it a `FortId` type would make one malformed row fail the entire batch, and the cursor is the database's collation-ordered value, not ours. Parse per row inside the existing loop, and advance `lastId` from the raw scanned string exactly as today:

```go
		fortTracker.mu.Lock()
		for _, row := range rows {
			id, ok := ParseFortId(row.Id)
			if !ok {
				log.Errorf("[FORT_TRACKER] unparseable fort id %q in %s, skipping", row.Id, table)
				continue
			}
			cellId := uint64(row.CellId)
			cell := fortTracker.getOrCreateCellLocked(cellId)
			if isGym {
				cell.gyms[id] = struct{}{}
			} else {
				cell.pokestops[id] = struct{}{}
			}
			// ...rest of the existing per-row body, using `id`
		}
		fortTracker.mu.Unlock()
```

Leave `lastId = rows[len(rows)-1].Id` and the initial `var lastId string` untouched — a skipped row must still advance the cursor, which it does automatically here because the cursor comes from the raw slice, not from the parsed ids.

**b. The `fortKindOps` dispatch table.** There is no `clearGymWithLock` / `clearPokestopWithLock` on this branch — they were replaced (commit `af4239d`, 2026-05-05) by one generic `clearFortWithLock[T comparable]` dispatching through a `fortKindOps[T]` struct whose `loadForUpdate` **field type** embeds the fort id:

```go
	loadForUpdate    func(context.Context, db.DbDetails, string, string) (T, func(), error)
```

Change that third parameter to `FortId`. The two instances (`gymClearOps`, `pokestopClearOps`) are populated with `getGymRecordForUpdate` / `getPokestopRecordForUpdate`, which still take `string` until Task 5 — so wrap them for now:

```go
	loadForUpdate: func(ctx context.Context, d db.DbDetails, id FortId, caller string) (*Gym, func(), error) {
		return getGymRecordForUpdate(ctx, d, id.String(), caller)
	},
```

Both wrappers collapse back to the bare function references in Task 5.

**c. `GetCellInfo` / `GetFortInfo`** build the JSON DTOs. Keep `CellFortInfo.Pokestops`/`.Gyms` as `[]string` and `FortTrackerInfo.FortId` as `string`, converting with `.String()` when building — see Step 4.

- [ ] **Step 2: Fix two pre-existing defects in the same struct**

Both are in code this task is already editing, both are one-liners, and neither is caused by the fort-id change.

**The `comparable` constraint is vestigial.** `fortKindOps[T comparable]` never compares two `T` values — the struct carries an explicit `isNil func(T) bool` field precisely because `comparable` does *not* permit comparing `T` against `nil`. The constraint is a leftover that misleads the reader into thinking comparability is load-bearing. Change both declarations to `any`:

```go
type fortKindOps[T any] struct {
```
```go
func clearFortWithLock[T any](ctx context.Context, dbDetails db.DbDetails, fortId FortId, cellId uint64, removeFromTracker bool, ops fortKindOps[T]) {
```

**The lock-contention caller name is not greppable.** The refactor rebuilt the caller string by concatenation:

```go
	rec, unlock, err := ops.loadForUpdate(ctx, dbDetails, fortId, "clear"+ops.kindLabel+"WithLock")
```

With `kindLabel: "gym"` that yields `"cleargymWithLock"` — lowercase `g`, matching neither the old `clearGymWithLock` nor any identifier in the tree. This string reaches `TrackedMutex` and is printed in `[LOCK_CONTENTION]` warnings, so an operator who greps it finds nothing. Name the function that actually holds the lock, and keep the kind as a parenthetical:

```go
	rec, unlock, err := ops.loadForUpdate(ctx, dbDetails, fortId, "clearFortWithLock[T] ("+ops.kindLabel+")")
```

Now `grep clearFortWithLock` from a contention log lands on the real function, and the kind still distinguishes the two paths.

- [ ] **Step 3: Convert the producer in `decode.go`**

`decode.go`'s `decodeGMO` builds `cellForts` from proto fort ids inside the `for _, fort := range mapCell.Fort` loop. The current block is:

```go
			// track fort by type for memory-based cleanup (only if tracker enabled)
			if cf, ok := cellForts[mapCell.S2CellId]; ok {
				switch fort.FortType {
				case pogo.FortType_GYM:
					cf.Gyms = append(cf.Gyms, fort.FortId)
				case pogo.FortType_CHECKPOINT:
					cf.Pokestops = append(cf.Pokestops, fort.FortId)
				}
			}
```

This is a real ingest boundary — `fort.FortId` is untrusted protobuf — so it parses with `ParseFortId` and an error log, not the bridge:

```go
			// track fort by type for memory-based cleanup (only if tracker enabled)
			if cf, ok := cellForts[mapCell.S2CellId]; ok {
				if fortId, parsed := decoder.ParseFortId(fort.FortId); !parsed {
					log.Errorf("[FORT_TRACKER] GMO cell %d carried an unparseable fort id %q, skipping", mapCell.S2CellId, fort.FortId)
				} else {
					switch fort.FortType {
					case pogo.FortType_GYM:
						cf.Gyms = append(cf.Gyms, fortId)
					case pogo.FortType_CHECKPOINT:
						cf.Pokestops = append(cf.Pokestops, fortId)
					}
				}
			}
```

Also update the `&decoder.FortTrackerGMOContents{ Pokestops: make([]string, 0), Gyms: make([]string, 0), ... }` literal earlier in the same loop to `make([]decoder.FortId, 0)`.

Note this only skips the fort's **tracker** registration; the same fort is still appended to `newForts` for the normal decode path, which does its own parse in Task 5. That is intentional — one malformed id must not take out the whole cell.

- [ ] **Step 4: Convert the tracker API handlers**

`CellFortInfo` and `FortTrackerInfo` are JSON DTOs: keep their fields as `[]string` / `string` and convert with `.String()` when building them in `GetCellInfo` / `GetFortInfo`, so the API output is provably unchanged.

`fortTrackerFortInput.FortId` stays a `string` — it is a huma-bound URL path parameter, and keeping it a string means the OpenAPI schema is untouched and no huma resolver or custom schema provider is needed. The handler (`routes_huma.go`, around the `ft.GetFortInfo(in.FortId)` call) parses it:

```go
		fortId, ok := decoder.ParseFortId(in.FortId)
		if !ok {
			return nil, huma.Error404NotFound("fort not found")
		}
		info := ft.GetFortInfo(fortId)
```

A malformed id in a request is a 404 — it cannot identify a fort — never a 500.

- [ ] **Step 5: Add a regression test for the pagination cursor**

Append to `decoder/fort_tracker_test.go`:

```go
// A junk fort id in the middle of a keyset page must not stall the loader:
// the cursor advances on the raw string even when the id is unusable.
func TestFortTrackerParseFailureAdvancesCursor(t *testing.T) {
	// The empty-string id is the one nonconforming row that exists in
	// production; it must parse-fail rather than becoming the zero FortId.
	if _, ok := ParseFortId(""); ok {
		t.Fatal("empty fort id must not parse")
	}
	valid := mustFortId(t, "00000000000000000000000000000001.16")
	if !valid.Valid() {
		t.Fatal("valid id reported invalid")
	}
}
```

If `fort_tracker_test.go` has a fixture that exercises `loadFortKindFromDB` against a real row source, extend that instead — a test driving the real loop is worth more than the type-level assertion above. Check with:

Run: `grep -n "func Test" decoder/fort_tracker_test.go`

- [ ] **Step 6: Run the tests**

Run: `go test ./decoder/ -run 'FortTracker' -count=1 -v && go test ./decoder/ -count=1 && go test ./decoder/ -race -count=1 -run FortTracker`
Expected: PASS.

`fort_tracker_test.go` reaches into unexported internals (`ft.forts`, `ft.cells`, `ft.getOrCreateCellLocked`) and passes ids as bare literals like `"A"` and `[]string{gymId}`, so essentially every test in it needs its fixtures converted to valid 32-hex ids via `mustFortId` (Task 3 Step 5). `huma_routes_test.go`'s fort-tracker case only exercises the nil-tracker 503 path and needs no id changes.

- [ ] **Step 7: Verify build and lint, then commit**

Run: `go build ./... && go build -tags go_json ./... && go vet ./... && golangci-lint run`

```bash
git add decoder/fort_tracker.go decode.go routes_huma.go decoder/fort_tracker_test.go
git commit -m "refactor: key the fort tracker by FortId

Every live fort appears twice in the tracker (the forts map and its cell's
pokestop/gym set), so this removes two more string references per fort.
GMO ingest parses once, at the decode boundary. The keyset-pagination
cursor stays a string — it is the database's ordering — and advances on the
raw value so a junk row cannot stall the loader.

Also fixes two pre-existing warts in fortKindOps while editing it: the
comparable constraint was vestigial (an explicit isNil field exists
precisely because comparable cannot compare T against nil), and the
lock-contention caller name was built by concatenation into
'cleargymWithLock', which matches no identifier in the tree — it now
names clearFortWithLock so a contention log can be grepped."
```

---

### Task 5: Pokestop, gym and shared fort code

The largest task, and cohesive: pokestops and gyms are peers whose conversion path (`pokestop ↔ gym`) couples them through `decoder/fort.go`, so they convert together.

**Files:**
- Modify: `decoder/pokestop.go`, `decoder/gym.go` (the `Id` field, `TrackedMutex[FortId]`, setters)
- Modify: `decoder/pokestop_state.go`, `decoder/gym_state.go` (record accessors, SQL, webhooks)
- Modify: `decoder/pokestop_decode.go`, `decoder/gym_decode.go`, `decoder/pokestop_process.go`
- Modify: `decoder/fort.go` (shared fields, `UpdateFortRecordWithGetMapFortsOutProto`, fort webhooks)
- Modify: `decoder/gmo_decode.go` (fort batch loop and the type-conversion path)
- Modify: `decoder/main.go` (cache declarations), `decoder/writebehind_batch.go` (queue key types + `KeyCompare`)
- Modify: `decoder/preload.go`, `decoder/api_pokestop.go`, `decoder/api_gym.go`, `decoder/api_fort.go`, `routes_huma.go`
- Test: `decoder/api_pokestop_test.go`, `decoder/api_gym_test.go`, `decoder/pokestop_decode_test.go`, `decoder/fort_*_test.go`, `decoder/gym_lobby*_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1-4.
- Produces:
  - `PokestopData.Id FortId`, `GymData.Id FortId`
  - `pokestopCache *ottercache.OtterCache[FortId, *Pokestop]`, `gymCache` likewise
  - `getMapFortsCache *ottercache.OtterCache[FortId, *pogo.GetMapFortsOutProto_FortProto]`
  - `pokestopQueue *writebehind.TypedQueue[FortId, PokestopData]`, `gymQueue` likewise
  - Record accessors taking `FortId`: `getPokestopRecordReadOnly`, `getOrCreatePokestopRecord`, `DoesPokestopExist`, `PeekPokestopRecord`, `clearPokestopWithLock`, and the gym equivalents

- [ ] **Step 1: Establish the green baseline**

Run: `go test ./... -count=1 > /tmp/baseline.txt 2>&1; tail -5 /tmp/baseline.txt`
Expected: all pass. This is the regression suite for the conversion; nothing in it may break.

- [ ] **Step 2: Convert the entity structs and their wiring**

1. `decoder/pokestop.go`: `Id FortId \`db:"id"\`` and `mu TrackedMutex[FortId]`. Same for `decoder/gym.go`.
2. `decoder/main.go`: `pokestopCache`, `gymCache`, `getMapFortsCache` become `OtterCache[FortId, ...]`; update their `NewOtterCache` construction calls (the type parameters are inferred from the declared variable type — check each `initDataCache` assignment).
3. `decoder/writebehind_batch.go`: `pokestopQueue`/`gymQueue` become `*writebehind.TypedQueue[FortId, ...]`; their configs get `KeyCompare: FortId.Compare` (a method expression — this is exactly the shape `KeyCompare func(a, b K) int` wants) and their `KeyFunc` return types change to `FortId`.
4. Any `fmt.Sprintf`/log format verb holding a fort id: `%s` still works (`FortId` has a `String()` method), but `%v` on a struct would print `{[...] 22}`. Check each one:

Run: `grep -n '%v' decoder/pokestop.go decoder/gym.go decoder/pokestop_state.go decoder/gym_state.go | grep -i "id"`

- [ ] **Step 3: Convert the SQL id-list paths by hand**

These four read fort ids **out of** the database into `[]string` and then feed them back into record lookups. The compiler will flag the lookup calls but not the slice types, so they need deliberate conversion — either scan into `[]FortId` (works: `FortId` implements `sql.Scanner`) or scan into `[]string` and parse in the consuming loop, logging and skipping malformed rows:

- `decoder/pokestop_state.go`, `RemoveQuestsWithinGeofence`: `var pokestopIds []string` from `SELECT id FROM pokestop WHERE ... ST_CONTAINS(...)`, consumed by `getOrCreatePokestopRecord` in the loop below it.
- `decoder/pokestop_state.go`, `ExpireQuests`: two `SELECT id FROM pokestop WHERE ..._expiry < ?` queries into `[]string`, plus **three** `map[string]bool` sets built from them (`hasExpiredQuest`, `hasExpiredAltQuest`, `allIds`) that become `map[FortId]bool`.
- `decoder/api_gym.go`, `SearchGymsAPI`: returns `([]string, error)` from `SELECT id FROM gym ... ORDER BY id ASC`. Its caller in `routes_huma.go` feeds each id to `GetGymRecordReadOnly`. The `ORDER BY id ASC` is the DB's ordering and is preserved either way.
- `decoder/pokestop_state.go` / `decoder/gym_state.go`, `updatePokestopGetMapFortCache` / `updateGymGetMapFortCache`: keyed by `getMapFortsCache`, whose key type changed in Step 2.

- [ ] **Step 4: Unwrap the `fortKindOps` shims from Task 4**

`getGymRecordForUpdate` and `getPokestopRecordForUpdate` now take a `FortId`, so the two wrapper closures added to `gymClearOps` / `pokestopClearOps` in Task 4 Step 1(b) collapse back to bare function references:

```go
	loadForUpdate: getGymRecordForUpdate,
```

Leave Task 4 Step 2's two fixes alone — the `any` constraint and the greppable caller name stay as they are.

- [ ] **Step 5: Follow the compiler through the rest of the call graph**

Run: `go build ./... 2>&1 | head -60`

Work through the errors. The rules, applied consistently:
- **In-memory:** the id is a `FortId` end to end. Delete any `fortIdFromLegacyString` call whose input just became a `FortId`.
- **SQL:** pass the `FortId` directly as a query argument — `driver.Valuer` renders the varchar. Scanning into `PokestopData.Id` works through `sql.Scanner`. Do not call `.String()` for SQL.
- **JSON/webhook DTOs:** the struct field stays `string`; assign `id.String()` at the assignment site.
- **Proto ingest** (`fortData.FortId`, `mapFort.Id`, request bodies): `ParseFortId` with an error log and `continue`/`return` on failure.
- **API path params and query-body id lists:** the huma input structs (`gymByIdInput.GymId`, `stationByIdInput.StationId`, `pokestopByIdInput.FortId`, `idsQueryInput.Body.IDs`) stay `string`/`[]string` so the OpenAPI schema is unchanged; the handler parses. `dedupeIDs` becomes `func dedupeIDs(in []string) []FortId`, parsing each id and dropping unparseable ones — which subsumes its existing `id == ""` filter — while keeping the `maxQueryIDs` (500) cap and the order-preserving behavior.

Repeat until `go build ./...` is clean.

- [ ] **Step 6: Check the pokestop↔gym conversion path by hand**

`decoder/gmo_decode.go`'s `UpdateFortBatch` loop (`fortId := fort.Data.FortId` at the top) and `decoder/fort.go`'s `GetSharedFields`/`ApplySharedFields` implement type conversion, and CLAUDE.md's lock ordering rule applies: lock A → copy → unlock A → lock B → apply. Convert `fortId := fort.Data.FortId` to a parse-once-and-skip at the top of the loop, then confirm:
- the parsed value is used for **both** the pokestop and gym sides — no re-parse, no `.String()` round trip between the halves
- `DoesGymExist` / `DoesPokestopExist` receive that same `FortId`
- the release-between-locks structure is untouched
- `getOrCreateIncidentRecord(ctx, db, incidentProto.IncidentId, fortId, ...)` keeps its **third** argument a `string` (the incident id) while its **fourth** becomes the `FortId`

`SharedFortFields`, `GetSharedFields` and `ApplySharedFields` carry **no** fort id and need no change at all — stated explicitly so nobody "fixes" them. `CreateFortWebHooks` / `CreateFortChangeWebhooks` likewise only touch name/description/image/location fields; only the `fort.Id = gym.Id` / `fort.Id = stop.Id` assignments in `InitWebHookFortFromGym` / `InitWebHookFortFromPokestop` change (to `.String()`, since `FortWebhook.Id` stays a `string`).

- [ ] **Step 7: Update the affected tests and run the suite**

Apply the same fixture rule as Task 3 Step 5 (valid 32-hex ids, `mustFortId` where a `FortId` is needed).

Run: `go test ./decoder/ -count=1 2>&1 | tail -30`
Expected: all pass. Compare against `/tmp/baseline.txt` — the set of passing tests must not shrink.

- [ ] **Step 8: Verify build, race and lint**

Run: `go build ./... && go build -tags go_json ./... && go vet ./... && go test ./... -count=1 && go test ./decoder/ -race -count=1 && golangci-lint run`

- [ ] **Step 9: Commit**

```bash
git add -A decoder/ routes_huma.go
git commit -m "refactor: pokestop and gym ids are FortId

Entity fields, cache keys, write-behind queue keys and every record
accessor now carry the 17-byte value. Ids convert to strings only at the
SQL, JSON and webhook boundaries, and proto ingest parses once with an
error log for structurally malformed ids."
```

---

### Task 6: Station and station battles

**Files:**
- Modify: `decoder/station.go`, `decoder/station_state.go`, `decoder/station_decode.go`
- Modify: `decoder/station_battle.go`
- Modify: `decoder/main.go`, `decoder/writebehind_batch.go`, `decoder/preload.go`, `decoder/api_station.go`, `decoder/api_fort.go`, `routes_huma.go`
- Test: `decoder/station_battle_test.go`, `decoder/api_station_test.go`, `decoder/station_lobby*_test.go`

**Interfaces:**
- Consumes: Tasks 1-5.
- Produces: `StationData.Id FortId`, `stationBattleWrite.StationId FortId`, `StationBattleData.StationId FortId`, `stationCache *ottercache.OtterCache[FortId, *Station]`, `stationBattleCache *xsync.Map[FortId, stationBattleState]`.

- [ ] **Step 1: Convert the station entity and wiring**

Same shape as Task 5 Step 2: `StationData.Id FortId`, `mu TrackedMutex[FortId]`, `stationCache` and `stationQueue`/`stationBattleQueue` re-typed, `KeyCompare: FortId.Compare`, and the `stationCache.OnEviction` callback in `fortRtree.go` loses its bridge call (the key is now a `FortId`, so pass it straight to `deferFortEviction` and to `clearStationBattleState`).

- [ ] **Step 2: Convert `station_battle.go`, including the content hash**

`hashStationBattle` currently folds the station id in as a string. Replace:

```go
	writeString(h, battle.StationId)
```

with:

```go
	h.Write(battle.StationId.Guid[:])
	h.Write([]byte{battle.StationId.Suffix})
```

This is safe with no migration: `stationBattleSnapshotSeed` comes from `maphash.MakeSeed()` at process start, so these signatures are per-process ephemera that never persist. `writeString` has other callers — leave it.

- [ ] **Step 3: Replace the empty-string sentinels**

`station_battle.go` uses `stationId == ""` as "absent" in a series of guards. Each becomes `!stationId.Valid()`. The sites: `storeStationBattles`, `clearStationBattleState`, `hasLoadedStationBattles`, `stationBattleFromProto`, `getKnownStationBattles`, `hydrateStationBattlesForStation` (which guards on `station.Id == ""`), and `flattenStationBattleWrites` (`snapshot.StationId == ""`).

In `preloadStationBattles`, the grouped-stream consumer over `ORDER BY sb.station_id, sb.battle_end` uses `currentStationId := ""` plus three `== ""` / `!= ""` comparisons as its group-boundary state. These become `var currentStationId FortId`, `currentStationId.Valid()`, and `currentStationId = FortId{}` for the reset. The grouping itself still works because the id decode is injective — equal strings produce equal `FortId`s — and `FortId`'s byte order matches the varchar collation, so in-memory grouping stays congruent with the SQL ordering.

`finalizePreloadedStationBattles` has an explicit callback annotation, `stationCache.Range(func(stationId string, station *Station) bool`, which must be edited to `FortId` by hand — type inference will not do it.

- [ ] **Step 4: Handle the `sqlx.In` expansion (highest-risk site in this task)**

`buildDeleteObsoleteStationBattlesQuery` passes a slice of station ids straight to `sqlx.In`:

```go
	return sqlx.In("DELETE FROM station_battle WHERE station_id IN (?)", stationIds)
```

`sqlx.In` reflects over the slice and expands each element as a bind argument. `FortId` implements `driver.Valuer` with a **value receiver**, so a `[]FortId` expands to the varchar strings correctly — but this is exactly the kind of reflection-driven path where a mistake produces a silently wrong `DELETE` rather than a compile error. Pin it with a test before trusting it. Append to `decoder/station_battle_test.go`:

```go
// sqlx.In expands []FortId through driver.Valuer. If that ever regressed,
// this DELETE would bind the wrong values and quietly remove the wrong rows.
func TestDeleteObsoleteStationBattlesBindsVarcharIds(t *testing.T) {
	ids := []FortId{
		mustFortId(t, "a1b2c3d4e5f60718293a4b5c6d7e8f90.23"),
		mustFortId(t, "00000000000000000000000000000001.16"),
	}
	query, args, err := buildDeleteObsoleteStationBattlesQuery(ids, nil)
	if err != nil {
		t.Fatalf("buildDeleteObsoleteStationBattlesQuery error: %v", err)
	}
	if len(args) != len(ids) {
		t.Fatalf("got %d bind args, want %d (query: %s)", len(args), len(ids), query)
	}
	for i, arg := range args {
		v, err := driver.DefaultParameterConverter.ConvertValue(arg)
		if err != nil {
			t.Fatalf("arg %d is not a valid driver value: %v", i, err)
		}
		s, ok := v.(string)
		if !ok {
			t.Fatalf("arg %d converted to %T (%v), want the varchar string", i, v, v)
		}
		if s != ids[i].String() {
			t.Fatalf("arg %d = %q, want %q", i, s, ids[i].String())
		}
	}
}
```

Add `"database/sql/driver"` to that file's imports. Run it on its own before continuing:

Run: `go test ./decoder/ -run TestDeleteObsoleteStationBattlesBindsVarcharIds -v`

If it fails, do **not** work around it in the query builder — convert to `[]string` at that one call site with an explicit comment, since a wrong `DELETE` is unrecoverable.

- [ ] **Step 5: Follow the compiler and update tests**

Run: `go build ./... 2>&1 | head -40`, applying the same boundary rules as Task 5 Step 5.

Run: `go test ./decoder/ -count=1 2>&1 | tail -30`

- [ ] **Step 6: Verify and commit**

Run: `go build ./... && go build -tags go_json ./... && go vet ./... && go test ./... -count=1 && go test ./decoder/ -race -count=1 -run 'Station' && golangci-lint run`

```bash
git add -A decoder/ routes_huma.go
git commit -m "refactor: station ids are FortId

Includes the station-battle content hash, which now folds the id's bytes
instead of its string form. Battle snapshot signatures are per-process
(the maphash seed is generated at startup), so nothing persisted changes.
The ORDER BY station_id grouped stream is unaffected: FortId's byte order
matches the varchar collation."
```

---

### Task 7: Satellite fort references, and removing the bridge

Incident, tappable and route rows reference forts. Converting them removes the last `fortIdFromLegacyString` callers.

**Files:**
- Modify: `decoder/incident.go`, `decoder/incident_state.go`, `decoder/incident_decode.go`
- Modify: `decoder/tappable.go`, `decoder/tappable_decode.go`, `decoder/api_tappable.go`
- Modify: `decoder/routes.go`, `decoder/routes_decode.go`, `decoder/routes_state.go`
- Modify: `decoder/fortRtree.go` (`updatePokestopIncidentLookup` drops its bridge call)
- Modify: `decoder/preload.go`
- Delete: `decoder/fortid_bridge.go`
- Test: `decoder/fort_incident_test.go`, `decoder/fort_incident_id_test.go`, `decoder/api_tappable_test.go`, `decoder/incident_battlestate_test.go`

**Interfaces:**
- Consumes: Tasks 1-6.
- Produces: `Incident.PokestopId FortId`, `Tappable.FortId FortId` (zero value = absent, replacing `null.String`), `RouteData.StartFortId FortId`, `RouteData.EndFortId FortId`.

**Three ids in this task's files are NOT fort ids and must stay `string`:**
- `Incident.Id` / `IncidentData.Id` — the incident id, and also the `incidentQueue` key and `incidentCache` key. (It is a signed int64 in decimal; converting it is a separate, already-evidenced follow-up — see the spec's non-goals.)
- `FortLookupIncident.Id` — an incident id used as a lookup handle inside `FortLookup`.
- `Route.Id` / `RouteData.Id` — the route id, and the `routeQueue` key. Only `StartFortId` and `EndFortId` are fort ids.

- [ ] **Step 1: Convert the three satellite entities**

For `Tappable.FortId`, which is currently `null.String`: the zero `FortId` replaces the invalid state. Every `x.FortId.Valid` becomes `x.FortId.Valid()`, `x.FortId.ValueOrZero()` becomes `x.FortId.String()`, and `null.StringFrom(s)` construction becomes a `ParseFortId` at the ingest site. The API DTO field stays `*string`:

```go
	var fortId *string
	if tappable.FortId.Valid() {
		s := tappable.FortId.String()
		fortId = &s
	}
```

This preserves `null` in the JSON exactly as today — do **not** let the zero value serialize as `""`.

- [ ] **Step 2: Delete the bridge and confirm no callers remain**

```bash
rm decoder/fortid_bridge.go
```

Run: `grep -rn "fortIdFromLegacyString" . --include="*.go"`
Expected: **no output.** Any hit is a conversion the plan missed — convert that call site rather than reinstating the file.

- [ ] **Step 3: Follow the compiler and update tests**

Run: `go build ./... 2>&1 | head -40`, then `go test ./decoder/ -count=1 2>&1 | tail -30`.

- [ ] **Step 4: Confirm no stray string conversions remain on hot paths**

Run: `grep -rn "\.String()" decoder/fortRtree.go decoder/fort_tracker.go decoder/api_fort.go`

Every remaining hit must be at a genuine boundary — a JSON DTO assignment, a log line, or an error message. A `.String()` feeding a cache lookup, a map key, or a tree operation is a conversion that was missed; fix it.

- [ ] **Step 5: Verify and commit**

Run: `go build ./... && go build -tags go_json ./... && go vet ./... && go test ./... -count=1 && go test ./decoder/ -race -count=1 && golangci-lint run`

```bash
git add -A decoder/
git commit -m "refactor: incident, tappable and route fort references are FortId

Removes the last callers of the temporary parse bridge, which is deleted
here. Tappable's optional fort reference drops null.String for FortId's
zero value; its API field stays *string so the JSON keeps emitting null.
Incident and route ids themselves are not fort ids and are unchanged."
```

---

### Task 8: `Pokemon.PokestopId`

The pokemon-side win: one fort id per cached pokemon, measured at 3.25M in production.

**Files:**
- Modify: `decoder/pokemon.go` (field + `SetPokestopId`)
- Modify: `decoder/pokemon_decode.go`, `decoder/pokemon_process.go`, `decoder/pokemon_state.go`
- Modify: `decoder/api_pokemon_response.go`
- Test: `decoder/entity_sizes_test.go`, `decoder/pokemon_nullscan_test.go`, `decoder/pokemon_lure_test.go`, `decoder/api_pokemon_response_test.go`

**Interfaces:**
- Consumes: Tasks 1-7.
- Produces: `PokemonData.PokestopId FortId` (zero value = absent), `func (pokemon *Pokemon) SetPokestopId(v FortId)`.

- [ ] **Step 1: Change the field and its setter**

In `decoder/pokemon.go`, move `PokestopId` out of the pointer-carrying group (it no longer carries a pointer) and change its type:

```go
	PokestopId FortId `db:"pokestop_id"`
```

Placement: `FortId` is a 17-byte, 1-byte-aligned struct, so it belongs with the 1-byte group, **after** the existing `int8`/`bool`/`NullSeenType` fields — putting a 17-byte 1-aligned field there fills padding rather than creating it. Do not leave it among the 8-byte-aligned pointer fields, where it would force padding on both sides.

The setter keeps its dirty-tracking shape (`FortId` is comparable, so `!=` works):

```go
func (pokemon *Pokemon) SetPokestopId(v FortId) {
	if pokemon.PokestopId != v {
		if dbDebugEnabled {
			pokemon.debug.recordChange(fmt.Sprintf("PokestopId:%s->%s", pokemon.PokestopId, v))
		}
		pokemon.PokestopId = v
		pokemon.dirty = true
	}
}
```

- [ ] **Step 2: Convert the call sites**

Callers currently pass `null.StringFrom(someProtoFortId)`. Each becomes a parse at the ingest boundary. For example, in `UpdatePokemonRecordWithDiskEncounterProto` (`decoder/pokemon_process.go`):

```go
		if fortId, ok := ParseFortId(request.FortId); ok {
			pokemon.SetPokestopId(fortId)
		} else if request.FortId != "" {
			log.Errorf("[POKEMON] disk encounter %d carried an unparseable fort id %q", encounterId, request.FortId)
		}
```

The `request.FortId != ""` guard matters: an absent fort is normal and expected (the codebase already uses empty string as "no fort"), so only a **non-empty** unparseable id is worth an error log. Apply the same guard at every pokemon fort-id ingest site.

In `createPokemonWebhooks` (`decoder/pokemon_state.go`), the `"None"` sentinel and the pokestop-name lookup become:

```go
		pokestopId := "None"
		if pokemon.PokestopId.Valid() {
			pokestopId = pokemon.PokestopId.String()
		}

		var pokestopName *string
		if pokemon.PokestopId.Valid() {
			pokestop, unlock, _ := getPokestopRecordReadOnly(ctx, db, pokemon.PokestopId, "createPokemonWebhooks")
			name := "Unknown"
			if pokestop != nil {
				name = pokestop.Name.ValueOrZero()
				unlock()
			}
			pokestopName = &name
		}
```

In `decoder/api_pokemon_response.go`, `PokestopId` is a `*string` and must keep emitting `null` when absent:

```go
	var pokestopId *string
	if pokemon.PokestopId.Valid() {
		s := pokemon.PokestopId.String()
		pokestopId = &s
	}
```

- [ ] **Step 3: Update the entity size pin**

`decoder/entity_sizes_test.go` pins `PokemonData` and `Pokemon` sizes, and its file comment explains that a size change is a decision, not a constant bump. Read that comment, then record the new numbers **with the reasoning**, following the existing prose style. State plainly: `PokestopId` moved from a 16-byte string header plus a ~35-byte heap object to 17 inline bytes; the struct grows by 1 byte, and if that crosses `Pokemon` into the 352 allocator class, that is the deliberate trade the design accepted (~104 MB at 3.25M cached) in exchange for eliminating 3.25M heap objects and their GC mark cost.

Get the real numbers rather than guessing:

Run: `go test ./decoder/ -run TestPokemonEntitySizes -v 2>&1 | head -20`

The failure message reports actual vs expected; use the actual values.

- [ ] **Step 4: Verify the DB round trip explicitly**

`pokestop_id` is a nullable `varchar(35)` and `PokemonData` is written by the write-behind batch upsert. Add to `decoder/pokemon_nullscan_test.go`:

```go
// PokestopId must round-trip as a varchar, and an absent fort must write
// SQL NULL rather than an empty string — the column is nullable and
// consumers distinguish the two.
func TestPokemonPokestopIdSqlRoundTrip(t *testing.T) {
	id := mustFortId(t, "a1b2c3d4e5f60718293a4b5c6d7e8f90.16")

	v, err := id.Value()
	if err != nil {
		t.Fatalf("Value() error: %v", err)
	}
	if s, ok := v.(string); !ok || s != "a1b2c3d4e5f60718293a4b5c6d7e8f90.16" {
		t.Fatalf("Value() = %#v, want the varchar string", v)
	}

	var absent FortId
	v, err = absent.Value()
	if err != nil {
		t.Fatalf("Value() on absent error: %v", err)
	}
	if v != nil {
		t.Fatalf("absent PokestopId Value() = %#v, want nil (SQL NULL)", v)
	}

	var scanned FortId
	if err := scanned.Scan("a1b2c3d4e5f60718293a4b5c6d7e8f90.16"); err != nil {
		t.Fatalf("Scan error: %v", err)
	}
	if scanned != id {
		t.Fatalf("Scan = %v, want %v", scanned, id)
	}
}
```

If that test file has an existing MariaDB-backed round-trip harness, add a case there as well: writing a row and reading `pokestop_id` back **as a raw string** is the only check that catches a `Valuer` regression writing a non-varchar into the column.

- [ ] **Step 5: Run the suite and verify**

Run: `go build ./... && go build -tags go_json ./... && go vet ./... && go test ./... -count=1 && go test ./decoder/ -race -count=1 && golangci-lint run`

- [ ] **Step 6: Commit**

```bash
git add -A decoder/
git commit -m "refactor: Pokemon.PokestopId is a FortId

Removes one heap object and a ~35-byte duplicated string per cached
pokemon — about 3.25M of each in production, against maybe 500k distinct
forts. The webhook 'None' sentinel and the API's null pokestop_id are
preserved exactly; an absent fort writes SQL NULL, not an empty string."
```

---

### Task 9: Username persistence option

Independent of the fort work. Default off: the account name is no longer stored on the pokemon entity, but is still threaded to the two consumers that need it.

**Files:**
- Modify: `config/config.go`
- Modify: `decoder/pokemon_decode.go` (the seven `SetUsername` sites)
- Modify: `decoder/pokemon_state.go` (`savePokemonRecordAsAtTime`, `createPokemonWebhooks`)
- Modify: `decoder/stats.go` (`statsSnapshot`, `pokemonStatsEvent`)
- Modify: `decoder/gmo_decode.go`, `decoder/pokemon_process.go`, `decoder/weather_iv.go` (save call sites)
- Test: `decoder/pokemon_username_test.go` (new)

**Interfaces:**
- Consumes: nothing from Tasks 1-8 (fully independent — may be executed before them if convenient).
- Produces:
  - `config.Config.StoreUsername bool` (koanf key `store_username`)
  - `func savePokemonRecordAsAtTime(ctx context.Context, db db.DbDetails, pokemon *Pokemon, isEncounter, writeDB, webhook bool, now int64, username string)`
  - `pokemonStatsEvent` gains a `username string` field
  - `func (pokemon *Pokemon) statsSnapshot(username string) *pokemonStatsSnapshot`

- [ ] **Step 1: Write the failing tests**

Create `decoder/pokemon_username_test.go`:

```go
package decoder

import (
	"testing"

	"golbat/config"

	"github.com/guregu/null/v6"
)

func withStoreUsername(t *testing.T, enabled bool) {
	t.Helper()
	previous := config.Config.StoreUsername
	config.Config.StoreUsername = enabled
	t.Cleanup(func() { config.Config.StoreUsername = previous })
}

// Default off: the account name never reaches the entity, so the column
// writes NULL and the API reports no username.
func TestUsernameNotStoredByDefault(t *testing.T) {
	withStoreUsername(t, false)

	var pokemon Pokemon
	pokemon.setUsernameIfStored("SomeAccount")

	if pokemon.Username.Valid {
		t.Fatalf("username stored with the option off: %q", pokemon.Username.ValueOrZero())
	}
}

func TestUsernameStoredWhenEnabled(t *testing.T) {
	withStoreUsername(t, true)

	var pokemon Pokemon
	pokemon.setUsernameIfStored("SomeAccount")

	if !pokemon.Username.Valid || pokemon.Username.ValueOrZero() != "SomeAccount" {
		t.Fatalf("username = %v, want SomeAccount", pokemon.Username)
	}
}

// With the option on, the first account to see the pokemon keeps ownership
// of the field — the pre-existing behavior the option must not disturb.
func TestUsernameNotOverwrittenWhenEnabled(t *testing.T) {
	withStoreUsername(t, true)

	var pokemon Pokemon
	pokemon.setUsernameIfStored("FirstAccount")
	if !pokemon.Username.Valid {
		t.Fatal("first account was not stored")
	}
	if pokemon.Username.ValueOrZero() != "FirstAccount" {
		t.Fatalf("username = %q, want FirstAccount", pokemon.Username.ValueOrZero())
	}
}

// The shiny/duplicate-encounter dedup needs the account that is reporting
// *now*, which the decode context supplies — not the stored first-seen
// account. It must therefore work identically with the option off.
func TestStatsSnapshotCarriesThreadedUsername(t *testing.T) {
	withStoreUsername(t, false)

	pokemon := &Pokemon{}
	pokemon.Username = null.String{} // nothing stored

	snap := pokemon.statsSnapshot("LiveAccount")
	if snap.Username.ValueOrZero() != "LiveAccount" {
		t.Fatalf("snapshot username = %q, want LiveAccount", snap.Username.ValueOrZero())
	}
}

// With the option on, the stored value still wins for the snapshot so the
// existing per-account dedup semantics are unchanged for those operators.
func TestStatsSnapshotPrefersStoredUsername(t *testing.T) {
	withStoreUsername(t, true)

	pokemon := &Pokemon{}
	pokemon.Username = null.StringFrom("StoredAccount")

	snap := pokemon.statsSnapshot("LiveAccount")
	if snap.Username.ValueOrZero() != "StoredAccount" {
		t.Fatalf("snapshot username = %q, want StoredAccount", snap.Username.ValueOrZero())
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./decoder/ -run 'Username' -v 2>&1 | head -20`
Expected: compile failure — `config.Config.StoreUsername` undefined, `setUsernameIfStored` undefined, `statsSnapshot` takes no arguments.

- [ ] **Step 3: Add the config option**

In `config/config.go`, in `configDefinition`, next to the other pokemon-storage flags:

```go
	StoreUsername           bool           `koanf:"store_username"` // Persist the reporting account on the pokemon row (default off)
```

No entry is needed in `config/reader.go`'s defaults block: the default is `false`, which is the zero value, and the defaults provider cannot express "explicitly false" differently anyway.

- [ ] **Step 4: Gate storage at the setter**

In `decoder/pokemon.go`, add the gated helper next to `SetUsername`:

```go
// setUsernameIfStored records the reporting account only when the operator
// has opted in via store_username.
//
// The field is not load-bearing: its only consumers are the webhook payload
// and the shiny/duplicate-encounter dedup, both of which are supplied the
// account name directly from the decode context now. Persisting it stores a
// caller-supplied identifier on millions of rows for no functional benefit,
// so the default is off.
func (pokemon *Pokemon) setUsernameIfStored(username string) {
	if !config.Config.StoreUsername || username == "" {
		return
	}
	if pokemon.Username.Valid {
		// First account to see the pokemon keeps the field.
		return
	}
	pokemon.SetUsername(null.StringFrom(username))
}
```

Add the `golbat/config` import if the file lacks it.

Replace all seven `SetUsername` call sites in `decoder/pokemon_decode.go` (lines ~187, ~211, ~245-246, ~304, ~1042, ~1179-1180) with `pokemon.setUsernameIfStored(username)`. The existing `if !pokemon.Username.Valid` guards around two of them become redundant — the helper does that check — so delete those guards rather than nesting them.

- [ ] **Step 5: Thread the account name to the webhook**

Add a `username string` parameter as the final argument of `savePokemonRecordAsAtTime` (`decoder/pokemon_state.go:145`) and pass it into `createPokemonWebhooks`. In `createPokemonWebhooks`, the payload prefers the stored value and falls back to the live one:

```go
		webhookUsername := pokemon.Username
		if !webhookUsername.Valid && username != "" {
			webhookUsername = null.StringFrom(username)
		}
```

and use `Username: webhookUsername` in the `PokemonWebhook` literal. With the option on, the payload is unchanged; with it off, the webhook carries the account that triggered this save instead of the first-seen account.

Update the seven call sites:
- `decoder/gmo_decode.go:151, 171, 187` — pass `username` (in scope from `UpdatePokemonBatch`'s parameter)
- `decoder/pokemon_process.go:31, 75, 94` — pass `username` (in scope from each `UpdatePokemonRecordWith*` parameter)
- `decoder/weather_iv.go:120` — pass `""`; the proactive IV re-save has no account context, and an empty username leaves the payload exactly as it is today

- [ ] **Step 6: Thread the account name to the shiny dedup**

In `decoder/stats.go`, `statsSnapshot` takes the live username and prefers the stored one:

```go
func (pokemon *Pokemon) statsSnapshot(username string) *pokemonStatsSnapshot {
	snapUsername := pokemon.Username
	if !snapUsername.Valid && username != "" {
		snapUsername = null.StringFrom(username)
	}
	s := &pokemonStatsSnapshot{
		// ...existing fields...
		Username: snapUsername,
		// ...
	}
	// ...
}
```

`updateEncounterStats` needs no change: it already reads `pokemon.Username` off the snapshot and falls back to `"<NoUsername>"`.

Update the four `statsSnapshot()` call sites:
- `decoder/pokemon_state.go:273` — inside `savePokemonRecordAsAtTime`, pass its new `username` parameter
- `decoder/pokemon_process.go:34, 66, 85` (the three `encounter: true` enqueues) — pass the in-scope `username`

These three encounter sites are the ones that matter: they feed the per-account shiny dedup, and the decode context's account is the semantically correct one regardless of the option.

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./decoder/ -run 'Username' -count=1 -v`
Expected: all five tests PASS.

- [ ] **Step 8: Verify the whole build, suite and lint**

Run: `go build ./... && go build -tags go_json ./... && go vet ./... && go test ./... -count=1 && go test ./decoder/ -race -count=1 -run 'Username|Stats' && golangci-lint run`

- [ ] **Step 9: Commit**

```bash
git add config/config.go decoder/pokemon.go decoder/pokemon_decode.go decoder/pokemon_state.go decoder/stats.go decoder/gmo_decode.go decoder/pokemon_process.go decoder/weather_iv.go decoder/pokemon_username_test.go
git commit -m "feat: make pokemon username persistence optional, default off

The account name is threaded from the decode context to the webhook
payload and the shiny/duplicate-encounter dedup, which are its only
consumers — and the dedup wants the account reporting now, not the
first-seen one, so that path is more correct than before. Operators who
want the column populated set store_username = true."
```

---

### Task 10: Documentation

**Files:**
- Modify: `CLAUDE.md`
- Modify: `docs/` — the config option reference, wherever config keys are documented

- [ ] **Step 1: Locate the config documentation**

Run: `grep -rln "fort_in_memory" --include="*.md" . | grep -v superpowers`

Add `store_username` to whatever files that returns, in their existing style: default `false`, what it does, and the one behavioral consequence — with it off, pokemon webhooks report the account that triggered the save rather than the first account to see the pokemon.

- [ ] **Step 2: Update `CLAUDE.md`**

Three sections need amending:

1. **"Spatial Indexes (R-trees)" → "Fort R-tree"** — replace the "Scaling caveat" paragraph. It currently pre-scopes interning fort ids to dense integers as the lever for scan volume. That lever was evaluated and a different one was taken: fort ids are now a 17-byte `FortId` value type, which removed the string hashing, the per-bucket compares and the GC-visible key pointers it described. Note that interning on top of value ids remains available if fort scans ever become hot, and cite the spec.

2. **"Entity Model" → "Entities" table** — the Cache Key / Queue Key / ID Type columns for Pokestop, Gym and Station change from `string (fort ID)` / `string` to `FortId`.

3. **"Write-Behind Queues" → "Architecture"** — note that queue keys are `comparable` with an explicit `KeyCompare`, and that `FortId.Compare` orders identically to the varchar column so the deadlock-avoidance sort is unchanged.

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md docs/
git commit -m "docs: record the FortId conversion and store_username

CLAUDE.md's fort-scaling caveat pre-scoped interning as the lever; the
benchmarked outcome was the value type instead, which removes the same
costs without a global table. Interning remains available on top if fort
scans ever become hot."
```

---

## Verification checklist (before opening the PR)

- [ ] `grep -rn "fortIdFromLegacyString" . --include="*.go"` returns nothing — the bridge is gone.
- [ ] `grep -rn "interned_string" . --include="*.go"` returns nothing — this branch never carried PR 395's interning.
- [ ] `go build ./... && go build -tags go_json ./...` both succeed.
- [ ] `go test ./... -count=1` passes, and the passing-test set is a superset of the Task 5 Step 1 baseline.
- [ ] `go test ./decoder/ -race -count=1` passes.
- [ ] `golangci-lint run` is clean.
- [ ] `git diff --stat origin/c/golbat-memory-persistence-6846cc -- sql/` is empty — no schema change.
- [ ] A webhook payload and an API response captured before and after the change are byte-identical for the same entities (including a pokemon with no fort, which must still emit `"pokestop_id": "None"` in the webhook and `null` in the API, and a tappable with no fort, which must emit `null`).
- [ ] Against a real MariaDB instance: rows written before the change load unchanged, and rows written after read back byte-identical as raw strings.
- [ ] `grep -rn "map\[string\]" decoder/fortRtree.go decoder/fort_tracker.go decoder/api_fort.go` shows no fort-id-keyed map left behind.
- [ ] Decode-path cost is bounded: this change adds one `ParseFortId` (~218 ns, measured in the spike) per fort per GMO, against a decode that already costs orders of magnitude more. No dedicated perf gate is required on this branch — the `protobench` harness the spec references lives on `perf/proto-thinning` and is not available here. If a regression is suspected, compare `golbat_raw_packets_shed_total` and the decode duration histogram before and after in a staging run.

---

## Notes for the executor

**On "follow the compiler."** Tasks 5-8 deliberately use the type checker as the worklist. This is not vagueness: changing `PokestopData.Id`'s type makes every affected site a compile error, and the four boundary rules (in-memory value, SQL via `Valuer`, JSON/webhook via `.String()` into an unchanged DTO field, ingest via `ParseFortId`) decide each one. If a site does not obviously fall into one of those four, stop and ask rather than inventing a fifth.

**On test fixtures.** Many existing tests use short fake fort ids. These must become valid 32-hex ids. That is not the tests being bent to fit the implementation — fort ids always had this shape, and the fixtures were relying on the absence of validation.

**What must not change.** JSON and webhook bytes, SQL schema, the `"None"` webhook sentinel, `null` in API responses for an absent fort, locking order and structure, and the write-behind flush ordering.
