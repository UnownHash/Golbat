package decoder

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/guregu/null/v6"

	"golbat/stats_collector"
)

// internFailureCollector counts unresolvable-handle reports, so the test
// below can prove that path is loud rather than silent.
type internFailureCollector struct {
	stats_collector.StatsCollector
	mu       sync.Mutex
	failures map[string]int
	rejected map[string]int
}

func withInternFailureCollector(t *testing.T) *internFailureCollector {
	t.Helper()
	fake := &internFailureCollector{
		StatsCollector: stats_collector.NewNoopStatsCollector(),
		failures:       make(map[string]int),
		rejected:       make(map[string]int),
	}
	setStatsCollectorForTest(t, fake)
	return fake
}

func (c *internFailureCollector) IncInternLookupFailure(table string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures[table]++
}

func (c *internFailureCollector) IncInternRejected(table string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rejected[table]++
}

func (c *internFailureCollector) count(table string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failures[table]
}

func (c *internFailureCollector) rejectedCount(table string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rejected[table]
}

// TestInternedStringMatchesNullString is the contract this change has to
// keep: every boundary an interned handle crosses must produce exactly what
// the null.String it replaced produced. The database values and the API and
// webhook JSON all ride on this.
func TestInternedStringMatchesNullString(t *testing.T) {
	cases := []struct {
		name string
		s    null.String
		h    InternedPokestopId
	}{
		{"unset", null.NewString("", false), InternedPokestopId(0)},
		// The empty string is a VALUE, not NULL: null.String draws that
		// distinction, the pokestop_id column stores it, so the intern table
		// must keep the two apart rather than folding "" into the null handle.
		{"empty but valid", null.StringFrom(""), InternPokestopId("")},
		{"ordinary fort id", null.StringFrom("2eb4a5b1e1e94b2ab0b3c8f0d0000000.16"), InternPokestopId("2eb4a5b1e1e94b2ab0b3c8f0d0000000.16")},
		// json.Marshal HTML-escapes these; null.String does too, since both
		// end up in the same encoding/json call. Pinned so a hand-rolled
		// "faster" marshaller can't silently change the wire bytes.
		{"html-escapable", null.StringFrom(`a<b>&"c"`), InternPokestopId(`a<b>&"c"`)},
		{"unicode", null.StringFrom("Pokéstop ☃"), InternPokestopId("Pokéstop ☃")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, want := tc.h.Valid(), tc.s.Valid; got != want {
				t.Errorf("Valid() = %v, want %v", got, want)
			}
			if got, want := tc.h.ValueOrZero(), tc.s.ValueOrZero(); got != want {
				t.Errorf("ValueOrZero() = %q, want %q", got, want)
			}

			gotPtr, wantPtr := tc.h.Ptr(), tc.s.Ptr()
			switch {
			case (gotPtr == nil) != (wantPtr == nil):
				t.Errorf("Ptr() = %v, want nil-ness %v", gotPtr, wantPtr == nil)
			case gotPtr != nil && *gotPtr != *wantPtr:
				t.Errorf("*Ptr() = %q, want %q", *gotPtr, *wantPtr)
			}

			gotValue, err := tc.h.Value()
			if err != nil {
				t.Fatalf("Value(): %v", err)
			}
			wantValue, err := tc.s.Value()
			if err != nil {
				t.Fatalf("null.String.Value(): %v", err)
			}
			if gotValue != wantValue {
				t.Errorf("Value() = %#v, want %#v", gotValue, wantValue)
			}

			gotJSON, err := json.Marshal(tc.h)
			if err != nil {
				t.Fatalf("MarshalJSON: %v", err)
			}
			wantJSON, err := json.Marshal(tc.s)
			if err != nil {
				t.Fatalf("null.String MarshalJSON: %v", err)
			}
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("MarshalJSON = %s, want %s", gotJSON, wantJSON)
			}

			if got := tc.h.NullString(); got != tc.s {
				t.Errorf("NullString() = %+v, want %+v", got, tc.s)
			}
		})
	}
}

// TestInternIsCanonical pins the property the setters depend on: SetPokestopId
// skips the dirty flag when the handle is unchanged, so two interns of the
// same string MUST produce the same handle or every re-sighting would look
// like an edit.
func TestInternIsCanonical(t *testing.T) {
	const id = "canonical-fort-1"
	first := InternPokestopId(id)
	second := InternPokestopId(string([]byte(id))) // distinct backing array

	if first != second {
		t.Errorf("intern(%q) returned %d then %d; handles must be canonical", id, first, second)
	}
	if other := InternPokestopId("canonical-fort-2"); other == first {
		t.Errorf("distinct strings shared handle %d", other)
	}
	if !first.Valid() || first == 0 {
		t.Errorf("handle for %q is the null handle", id)
	}
}

// TestInternTablesAreSeparate checks the two tables really are independent,
// which is what makes the per-table size gauge meaningful. The phantom type
// parameter is what stops a handle from one being resolved against the
// other; that part is enforced by the compiler, not by this test.
func TestInternTablesAreSeparate(t *testing.T) {
	const shared = "same-text-in-both-tables"
	fort := InternPokestopId(shared)
	user := InternUsername(shared)

	if fort.ValueOrZero() != shared {
		t.Errorf("pokestop handle resolved to %q, want %q", fort.ValueOrZero(), shared)
	}
	if user.ValueOrZero() != shared {
		t.Errorf("username handle resolved to %q, want %q", user.ValueOrZero(), shared)
	}
	if pokestopIdInternTable == usernameInternTable {
		t.Error("the two intern tables are the same table")
	}
}

// TestInternedStringScan covers the database read boundary sqlx binds
// through the unchanged db tags: MySQL hands strings over as []byte, and a
// NULL column arrives as an untyped nil.
func TestInternedStringScan(t *testing.T) {
	var h InternedUsername

	if err := h.Scan([]byte("scanner-account-01")); err != nil {
		t.Fatalf("Scan([]byte): %v", err)
	}
	if got := h.ValueOrZero(); got != "scanner-account-01" {
		t.Errorf("after Scan([]byte): %q, want scanner-account-01", got)
	}

	if err := h.Scan("scanner-account-02"); err != nil {
		t.Fatalf("Scan(string): %v", err)
	}
	if got := h.ValueOrZero(); got != "scanner-account-02" {
		t.Errorf("after Scan(string): %q, want scanner-account-02", got)
	}

	if err := h.Scan(nil); err != nil {
		t.Fatalf("Scan(nil): %v", err)
	}
	if h.Valid() {
		t.Errorf("after Scan(nil): Valid() = true, want false")
	}

	if err := h.Scan(42); err == nil {
		t.Error("Scan(int) succeeded, want an error")
	}
}

// TestInternedStringUnresolvableHandle covers the failure the table can only
// reach through a bug — a handle it never issued. Since nothing is ever
// evicted, a handle that stops resolving means it was fabricated or came
// from another table.
//
// EVERY boundary degrades, including Value(). That is deliberate and easy to
// "fix" backwards, so it is pinned here: the write-behind flush treats a
// non-MySQL error as unretryable and abandons the entire batch, 50 pokemon by
// default and up to preserveBatchSize (1000) during a shutdown preserve, and
// the bad handle stays in the cached entity so every later save poisons
// another batch. Erasing one column that refills on the next sighting is the
// cheaper failure by orders of magnitude. Nothing is lost by degrading
// because the report fires from resolve() either way, which is what the
// counter assertion below guards.
func TestInternedStringUnresolvableHandle(t *testing.T) {
	collector := withInternFailureCollector(t)

	forged := InternedPokestopId(1 << 30)

	got, err := forged.Value()
	if err != nil {
		t.Errorf("Value() = error %v; an unresolvable handle must not fail the write, it takes 50-1000 unrelated rows with it", err)
	}
	if got != nil {
		t.Errorf("Value() = %#v, want nil", got)
	}
	if got, err := json.Marshal(forged); err != nil || string(got) != "null" {
		t.Errorf("MarshalJSON = %s (err %v), want null with no error: an API response should degrade, not fail", got, err)
	}
	if forged.Ptr() != nil {
		t.Error("Ptr() on an unresolvable handle should be nil")
	}
	// The visibility that makes degrading acceptable.
	if n := collector.count(pokestopIdInternTable.name); n == 0 {
		t.Errorf("no intern lookup failures counted for %s; degrading is only safe because the counter still fires", pokestopIdInternTable.name)
	}
}

// TestInternRejectsOverlongStrings covers the guard that makes append-only
// defensible against a caller rather than only against Niantic: username
// arrives unvalidated from the request body and pokestop_id straight off
// proto FortId, and nothing evicts, so a string too long for its column must
// never be retained.
func TestInternRejectsOverlongStrings(t *testing.T) {
	collector := withInternFailureCollector(t)
	table := newInternTable("test_overlong", 8)

	atLimit := "12345678"
	if h := table.intern(atLimit); h == 0 {
		t.Errorf("intern(%q) rejected a string exactly at the limit", atLimit)
	}

	overLimit := "123456789"
	before := table.size()
	if h := table.intern(overLimit); h != 0 {
		t.Errorf("intern(%q) = %d, want the null handle: 9 characters must not fit an 8-character column", overLimit, h)
	}
	if got := table.size(); got != before {
		t.Errorf("table grew from %d to %d; a rejected string must not be retained", before, got)
	}
	if n := collector.rejectedCount("test_overlong"); n != 1 {
		t.Errorf("rejected counter = %d, want 1; a rejection must be visible in metrics", n)
	}

	// Rejected, not truncated. A truncated fort id would be a
	// plausible-looking key that later code hands to a pokestop lookup, and a
	// truncated username silently attributes one account's scans to another.
	if h, ok := table.handles.Load(overLimit[:8]); ok && h != 1 {
		t.Errorf("an over-length string was stored under a truncated key (handle %d)", h)
	}

	// A string far past the byte bound is refused by the O(1) check that runs
	// before the map probe, so it is never hashed and never retained.
	huge := strings.Repeat("x", 1<<20)
	if h := table.intern(huge); h != 0 {
		t.Errorf("intern of a 1 MiB string = %d, want the null handle", h)
	}
	if got := table.size(); got != before {
		t.Errorf("table grew to %d entries after a 1 MiB string; nothing over-length may be retained", got)
	}

	// The column widths are utf8mb4 characters, not bytes, so a short string
	// of wide characters must still be accepted.
	wide := "☃☃☃☃☃☃☃☃" // 8 characters, 24 bytes
	if h := table.intern(wide); h == 0 {
		t.Errorf("intern(%q) rejected 8 characters because they are 24 bytes; the column counts characters", wide)
	}
	if h := table.intern("☃☃☃☃☃☃☃☃☃"); h != 0 {
		t.Errorf("intern of 9 wide characters = %d, want the null handle", h)
	}
}

// TestInternProductionTablesMatchColumnWidths ties the caps to the schema, so
// a migration that widens a column and forgets this file fails here rather
// than silently dropping values the column would now accept.
func TestInternProductionTablesMatchColumnWidths(t *testing.T) {
	// pokemon.pokestop_id varchar(35), sql/1_rdmdb_tables.up.sql
	if got := pokestopIdInternTable.maxChars; got != 35 {
		t.Errorf("pokestop_id intern cap = %d, want 35", got)
	}
	// pokemon.username varchar(64), sql/36_pokemon_username.up.sql
	if got := usernameInternTable.maxChars; got != 64 {
		t.Errorf("username intern cap = %d, want 64", got)
	}
}

// TestInternTableConcurrent runs the table the way production does: many
// goroutines interning overlapping strings at once, including a heavy
// overlap on the same few strings, while others resolve handles. It is worth
// running with -race. What it proves is the publication order in intern():
// a handle must never become reachable before the slice that can resolve it,
// or a lookup would report a live handle as unresolvable.
func TestInternTableConcurrent(t *testing.T) {
	table := newInternTable("test_concurrent", 64)

	const (
		goroutines = 16
		perRoutine = 400
	)

	handles := make([][]uint32, goroutines)
	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mine := make([]uint32, perRoutine)
			for i := range perRoutine {
				// Half the strings are shared across all goroutines (so they
				// race to intern the same value) and half are unique.
				var s string
				if i%2 == 0 {
					s = fmt.Sprintf("shared-%d", i)
				} else {
					s = fmt.Sprintf("unique-%d-%d", g, i)
				}
				h := table.intern(s)
				// Resolve immediately: this is the read that would fail if
				// the handle were published before the slice holding it.
				if got, ok := table.lookup(h); !ok || got != s {
					t.Errorf("lookup(%d) = %q, %v; want %q, true", h, got, ok, s)
				}
				mine[i] = h
			}
			handles[g] = mine
		}()
	}
	wg.Wait()

	// One handle per distinct string, and every handle still resolves.
	byString := make(map[string]uint32)
	for g := range goroutines {
		for i, h := range handles[g] {
			s, ok := table.lookup(h)
			if !ok {
				t.Fatalf("handle %d stopped resolving", h)
			}
			if seen, dup := byString[s]; dup && seen != h {
				t.Errorf("%q has two handles, %d and %d", s, seen, h)
			}
			byString[s] = h
			if i%2 == 0 && s != fmt.Sprintf("shared-%d", i) {
				t.Errorf("handle %d resolved to %q, want shared-%d", h, s, i)
			}
		}
	}

	wantSize := len(byString)
	if got := table.size(); got != wantSize {
		t.Errorf("size() = %d, want %d (one entry per distinct string, excluding the reserved null slot)", got, wantSize)
	}
}

// TestInternTableNullSlotReserved pins that handle 0 stays reserved, which is
// what lets the Go zero value of a handle field mean NULL.
func TestInternTableNullSlotReserved(t *testing.T) {
	table := newInternTable("test_null_slot", 64)
	if got := table.size(); got != 0 {
		t.Errorf("fresh table size() = %d, want 0", got)
	}
	if _, ok := table.lookup(0); ok {
		t.Error("handle 0 resolved; it is the null handle and must not")
	}
	// Even the empty string, which is the value most likely to be confused
	// with null, gets an ordinary non-zero handle.
	if h := table.intern(""); h == 0 {
		t.Error(`intern("") returned the null handle`)
	}
}
