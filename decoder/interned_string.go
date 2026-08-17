package decoder

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"sync/atomic"

	"github.com/guregu/null/v6"
	"github.com/puzpuzpuz/xsync/v4"
	log "github.com/sirupsen/logrus"

	"golbat/util"
)

// This file replaces two null.String fields on the cached Pokemon —
// pokestop_id and username — with 4-byte handles into an append-only string
// table.
//
// The motivation is not primarily the 40 bytes of struct width. A pokestop id
// is a ~35-character string, so at the ~3.25M cached pokemon measured in
// production the old representation was 3.25M separately-allocated strings
// naming maybe 500k distinct forts: ~114 MB of duplicated bytes, and 3.25M
// live heap objects for the GC to mark on every cycle. Interning collapses
// that to one copy per distinct string and a uint32 per pokemon, which is not
// a pointer at all and so leaves the GC scan bitmap entirely.
//
// LIFECYCLE — the table is APPEND-ONLY AND DELIBERATELY HAS NO EVICTION.
// A handle is a raw index; evicting an entry while a cached pokemon still
// holds its handle would be a use-after-free in spirit (the index would
// either dangle past the end or, worse, silently resolve to whatever string
// took its place). Append-only is only sound because both key sets are
// genuinely bounded:
//
//   - pokemon.pokestop_id: fort ids, stable identifiers for physical
//     locations. The count converges on the fort density of the scanned
//     region within days of a cold start and then grows only as fast as
//     Niantic adds forts — a few percent a year. ~500k measured, ~1-2M is a
//     realistic lifetime ceiling for a large instance.
//   - pokemon.username: scanner accounts, the faster-churning of the two.
//     A deployment runs hundreds to low tens of thousands of live accounts,
//     but bans mean the set of names ever *seen* grows monotonically. Even an
//     aggressive 500 replacements/day is ~180k/year, so ~1M over an instance's
//     lifetime.
//
// At those ceilings the table costs tens of MB against the hundreds it saves,
// which is why append-only is the right call and eviction is not. But
// "bounded" is an assumption about someone else's deployment, so growth is
// published rather than assumed: both tables export their size as
// golbat_intern_table_size{table=...} every 10s (see StartWorkerBacklogReporter).
// A fort table that keeps climbing after the first week, or a username table
// ramping far faster than the account roster, is the signal that this design
// no longer holds for that instance.

// internTable maps strings to dense uint32 handles and back. Safe for
// concurrent use by every decode goroutine.
//
// Handle 0 is reserved to mean "no value" (SQL NULL / JSON null) and is never
// handed out, so the Go zero value of a handle field is already the null one.
// Real handles start at 1. Note that the empty string is NOT the null handle:
// null.String distinguishes {Valid: true, String: ""} from NULL, that
// distinction reaches the database, and so interning "" yields an ordinary
// handle like any other string.
type internTable struct {
	// name labels this table in metrics and error messages.
	name string

	// handles is the forward direction, string -> handle. Lock-free reads;
	// this is the fast path that every already-seen string takes.
	handles *xsync.Map[string, uint32]

	// values is the reverse direction, handle -> string, as a slice indexed
	// by handle. Held behind an atomic pointer so a lookup is a pointer load,
	// a bounds check and an index — no lock, no hashing — because lookups
	// happen per result row when building API responses.
	//
	// Index 0 is the reserved null handle; the slot exists only so that real
	// handles equal their own index, and its contents are never read.
	values atomic.Pointer[[]string]

	// appendMu serialises the append side so a string interned concurrently
	// by two goroutines still gets exactly one handle.
	appendMu sync.Mutex

	// unresolvable aggregates the report for a handle this table cannot
	// resolve — see reportUnresolvable.
	unresolvable util.DropReporter
}

func newInternTable(name string) *internTable {
	t := &internTable{
		name:    name,
		handles: xsync.NewMap[string, uint32](),
	}
	reserved := []string{""} // slot 0: the null handle, never resolved
	t.values.Store(&reserved)
	return t
}

// intern returns s's handle, adding s to the table if it is new. The returned
// handle is stable for the life of the process.
func (t *internTable) intern(s string) uint32 {
	if h, ok := t.handles.Load(s); ok {
		return h
	}

	t.appendMu.Lock()
	defer t.appendMu.Unlock()

	// Re-check: another goroutine may have interned s between the Load above
	// and the Lock, and one string must map to exactly one handle.
	if h, ok := t.handles.Load(s); ok {
		return h
	}

	current := *t.values.Load()
	if uint64(len(current)) > math.MaxUint32 {
		// Unreachable in practice: 2^32 distinct strings of this shape is
		// ~150 GB of table alone. Return the null handle rather than wrap
		// around into someone else's string.
		log.Errorf("[INTERN] %s: table is full at %d entries; %q cannot be interned", t.name, len(current), s)
		return 0
	}

	handle := uint32(len(current))
	grown := append(current, s)

	// PUBLICATION ORDER IS LOAD-BEARING. The slice that contains index
	// `handle` must be visible before `handle` itself becomes reachable by
	// any other goroutine. Storing the slice first guarantees that: the only
	// ways another goroutine obtains this handle are the map Store below and
	// this function's return value, both of which happen after the atomic
	// Store, so any lookup of this handle observes a slice long enough to
	// contain it. Swapping these two lines would let a reader see the handle
	// against the shorter slice and report it as unresolvable.
	//
	// Growing in place when cap allows is safe alongside concurrent lookups:
	// append only ever writes at index len(current), which is past the end of
	// every slice header a reader can be holding.
	t.values.Store(&grown)
	t.handles.Store(s, handle)
	return handle
}

// lookup resolves a handle to its string. ok is false for the null handle,
// and false-with-a-loud-report for a handle this table cannot resolve.
func (t *internTable) lookup(handle uint32) (string, bool) {
	if handle == 0 {
		return "", false
	}
	values := *t.values.Load()
	if uint64(handle) >= uint64(len(values)) {
		t.reportUnresolvable(handle, len(values))
		return "", false
	}
	return values[handle], true
}

// reportUnresolvable makes a handle this table cannot resolve loud rather
// than silent.
//
// Because the table never evicts, a handle it issued can never stop
// resolving. So reaching here means the handle was never issued by THIS
// table: it was fabricated (a bad cast, a corrupt scan) or it belongs to a
// different intern table. That is a programming bug, and a silent one would
// be nasty — the value would simply read back empty, so pokestop_id would
// quietly become NULL for every affected pokemon.
//
// It is reported three ways and swallowed at none of them: a Prometheus
// counter for alerting, an error log (aggregated to at most one line per
// second, because a bug like this fires once per pokemon and per-event
// logging during that would be its own outage), and — at the write boundary
// only — a hard error from Value() so the bad value can never reach the
// database. See Value's comment for why the read boundaries degrade instead.
func (t *internTable) reportUnresolvable(handle uint32, size int) {
	getStatsCollector().IncInternLookupFailure(t.name)
	t.unresolvable.Report(func(count int64) {
		log.Errorf("[INTERN] %s: %d unresolvable handle(s) in the last second (most recent %d, table holds %d); "+
			"handles are never revoked, so this handle was either fabricated or issued by a different intern table",
			t.name, count, handle, size-1)
	})
}

// size reports the number of interned strings, excluding the reserved slot 0.
func (t *internTable) size() int {
	return len(*t.values.Load()) - 1
}

// The two tables. They are separate rather than shared so that their very
// different growth curves (see the file comment) can be watched separately —
// a fort-id leak summed with ordinary username churn would be invisible.
var (
	pokestopIdInternTable = newInternTable("pokemon_pokestop_id")
	usernameInternTable   = newInternTable("pokemon_username")
)

// internTableRef is InternedString's type parameter: a zero-sized marker
// naming the table a handle belongs to.
//
// It exists purely so the compiler can tell a pokestop-id handle from a
// username handle. Both are uint32 indexes into a table, and nothing about
// the number itself says which table — so with a single handle type,
// assigning a username handle to PokestopId would compile and then silently
// resolve to whatever fort happens to sit at that index. Making the table
// part of the type turns that into a compile error. The parameter carries no
// data and adds no bytes: a handle is still exactly 4 bytes wide.
type internTableRef interface{ table() *internTable }

type pokestopIdInterns struct{}

func (pokestopIdInterns) table() *internTable { return pokestopIdInternTable }

type usernameInterns struct{}

func (usernameInterns) table() *internTable { return usernameInternTable }

// InternedString is a 4-byte handle that stands in for a null.String field.
// It converts back to a string at the database and JSON boundaries, so both
// wire formats are unchanged from the null.String it replaced.
//
// The zero value is null, matching null.String's zero value.
type InternedString[T internTableRef] uint32

// The two instantiations this codebase uses. Only Pokemon's pokestop_id and
// username are interned; the identically-named fields on Incident, Pokestop
// and the API/webhook payload structs are ordinary strings and stay that way.
type (
	InternedPokestopId = InternedString[pokestopIdInterns]
	InternedUsername   = InternedString[usernameInterns]
)

// InternPokestopId returns the handle for a pokemon's pokestop_id.
func InternPokestopId(s string) InternedPokestopId {
	return InternedPokestopId(pokestopIdInternTable.intern(s))
}

// InternUsername returns the handle for a pokemon's username.
func InternUsername(s string) InternedUsername {
	return InternedUsername(usernameInternTable.intern(s))
}

func (h InternedString[T]) resolve() (string, bool) {
	var ref T
	return ref.table().lookup(uint32(h))
}

// Valid reports whether the handle holds a value, mirroring null.String's
// Valid field. It is a plain zero test and does not consult the table: an
// unresolvable handle is non-null but reads back as the empty string, and
// reporting that inconsistency is reportUnresolvable's job, not this one's.
func (h InternedString[T]) Valid() bool { return h != 0 }

// IsZero reports the opposite of Valid, matching guregu/null's method of the
// same name.
func (h InternedString[T]) IsZero() bool { return h == 0 }

// ValueOrZero returns the string, or "" if null.
func (h InternedString[T]) ValueOrZero() string {
	s, _ := h.resolve()
	return s
}

// Ptr returns a pointer to the string, or nil if null. Several API response
// types are *string and stay that way.
func (h InternedString[T]) Ptr() *string {
	s, ok := h.resolve()
	if !ok {
		return nil
	}
	return &s
}

// NullString converts back to a null.String, for the payload structs that
// still declare one.
func (h InternedString[T]) NullString() null.String {
	s, ok := h.resolve()
	return null.NewString(s, ok)
}

// Scan interns the incoming column value. sqlx binds this through the
// unchanged `db:"pokestop_id"` / `db:"username"` tags.
func (h *InternedString[T]) Scan(value any) error {
	var ref T
	switch v := value.(type) {
	case nil:
		*h = 0
	case string:
		*h = InternedString[T](ref.table().intern(v))
	case []byte:
		*h = InternedString[T](ref.table().intern(string(v)))
	default:
		return fmt.Errorf("cannot scan %T into intern handle for %s", value, ref.table().name)
	}
	return nil
}

// Value resolves the handle back to the exact string that was interned, so
// the column is written byte-identically to the null.String this replaced.
//
// An unresolvable handle degrades to NULL rather than returning an error,
// matching the read boundaries. Returning an error here looks like the safer
// choice and is not: the write-behind flush treats a non-MySQL error as
// unretryable, so it abandons the whole batch — 50 pokemon by default, and up
// to preserveBatchSize (1000) during a shutdown preserve. The bad handle
// lives in the cached entity, so every later save re-enqueues it and kills
// another batch for as long as that entry is cached. The trade is one column
// going NULL and self-healing on the next sighting, against dozens of
// unrelated pokemon losing every column, repeatedly.
//
// Nothing is lost by degrading, because resolve() has already reported the
// failure through reportUnresolvable — counter and log both fire regardless
// of what this returns.
//
// This is where the analogy with NullSeenType.Value stops, and the difference
// is worth stating rather than implying: that one rejects an enum code with
// no string form, a value MariaDB would silently store as "" with no
// read-side self-heal. Its error also cannot poison a batch of rows that
// don't share the bad value, because the check is about the value being
// written, not about a lookup that only this row's handle can fail.
func (h InternedString[T]) Value() (driver.Value, error) {
	if h == 0 {
		return nil, nil
	}
	s, ok := h.resolve()
	if !ok {
		return nil, nil
	}
	return s, nil
}

// MarshalJSON produces exactly what null.String produced: null when unset,
// otherwise the quoted string.
func (h InternedString[T]) MarshalJSON() ([]byte, error) {
	s, ok := h.resolve()
	if !ok {
		return []byte("null"), nil
	}
	return json.Marshal(s)
}

func (h *InternedString[T]) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*h = 0
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	var ref T
	*h = InternedString[T](ref.table().intern(s))
	return nil
}
