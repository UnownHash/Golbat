package decoder

import (
	"bytes"
	"cmp"
	"database/sql/driver"
	"encoding/hex"
	"fmt"

	"golbat/util"
)

// FortIdParseDrops aggregates fort-id parse failures on the decode path
// (GMO fort/station batches, fort-tracker cell tracking, and pokemon
// lure/nearby/tappable fort references) into at most one log line per
// second, via util.DropReporter — the codebase's pattern for exactly this
// (see stats.go, rtree_evictor.go, raw_limiter.go). Parse failure only
// happens if Niantic changes the id structure (§2.3 of the design doc),
// but when it does, every affected fort/pokemon hits this on every GMO
// across up to tuning.raw_processing_concurrency (default 96) concurrent
// decoders — five figures of log.Errorf/s, a second incident stacked on
// top of the ingest outage, burying the one line an operator needs.
//
// Exported (unlike the codebase's other DropReporters, which are
// package-private to their single call site) because decode.go — package
// main — is one of the routed call sites; sharing one counter across all
// of them means one aggregated line per second regardless of which site
// is producing the failures, which is the useful signal here ("fort id
// parsing is failing"), not which specific call site noticed first.
var FortIdParseDrops util.DropReporter

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
	// Suffix holds the id's numeric suffix as its decimal value; 0 means
	// the bare 32-character form.
	//
	// Niantic's suffix is an unpadded decimal number, and 0 is rendered by
	// omitting the '.' and the digits entirely. A 7,065,029-row production
	// census (gym + pokestop + station, 2026-09-22) found exactly six
	// suffixes — bare, .2, .11, .12, .16, .23 — with **no hex letter in any
	// row** and **no leading zero in any row**. Both absences are the
	// evidence: a hexadecimal suffix would put a-f in ~37% of two-digit
	// values, and a zero-padded one would spell .2 as .02. Bare ids are not
	// legacy junk (they are live on sponsored forts); they are suffix 0.
	//
	// So .2 and .02 are not two spellings of one id — .02 is not a string
	// Niantic emits, and parsing it would be guessing. Only the canonical
	// spelling parses: a leading zero, a lone ".0", and any hex letter are
	// rejected like any other unexpected format (logged via
	// FortIdParseDrops, row skipped) rather than silently rewritten into a
	// different primary key.
	//
	// Storing the decimal value keeps the type 17 bytes and makes
	// parse/format exact inverses over every id the scheme can produce, so
	// a FortId never rewrites the varchar it came from. Only ParseFortId,
	// Scan and UnmarshalText construct values; they never yield a Suffix
	// above 99, which is the largest the varchar(35) column can hold.
	// Compare renders the suffix back to its digits so byte order still
	// matches varchar order (TestFortIdCompareMatchesStringOrder).
	Suffix uint8
}

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

// fortIdDigit maps a byte to its decimal value, or -1. The suffix is
// decimal (see the Suffix field comment); the GUID stays hex.
var fortIdDigit = func() (t [256]int8) {
	for i := range t {
		t[i] = -1
	}
	for c := byte('0'); c <= '9'; c++ {
		t[c] = int8(c - '0')
	}
	return
}()

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
		// Bare: suffix 0.
	case 34:
		if s[32] != '.' {
			return FortId{}, false
		}
		// A lone ".0" is suffix 0, which Niantic spells as the bare form;
		// accepting it would let one fort hold two keys.
		if d := fortIdDigit[s[33]]; d > 0 {
			f.Suffix = uint8(d)
		} else {
			return FortId{}, false
		}
	case 35:
		if s[32] != '.' {
			return FortId{}, false
		}
		// hi > 0: a leading zero is not a spelling Niantic emits.
		hi, lo := fortIdDigit[s[33]], fortIdDigit[s[34]]
		if hi < 1 || lo < 0 {
			return FortId{}, false
		}
		f.Suffix = uint8(hi)*10 + uint8(lo)
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
//
// Asymmetric with UnmarshalText at the zero value: the zero value appends
// nothing (renders as ""), but UnmarshalText("") fails (Parse("") is
// rejected — see ParseFortId). This path is write-only for the zero value;
// no current DTO marshals a bare FortId (every JSON boundary holds
// string/*string, converted via Ptr/String at the edge), so the asymmetry
// is latent. A future FortId-typed JSON field must handle absence itself
// (e.g. Ptr(), or an explicit Valid() check) rather than round-tripping
// the zero value through this encoding.
func (f FortId) AppendText(b []byte) ([]byte, error) {
	if !f.Valid() {
		return b, nil
	}
	off := len(b)
	b = append(b, "00000000000000000000000000000000"...)
	hex.Encode(b[off:], f.Guid[:])
	if f.Suffix != 0 {
		b = append(b, '.')
		if f.Suffix >= 10 {
			b = append(b, '0'+f.Suffix/10)
		}
		b = append(b, '0'+f.Suffix%10)
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
//
// Shares AppendText's zero-value asymmetry: marshals the zero value as ""
// rather than failing, but UnmarshalText("") rejects it. See AppendText.
func (f FortId) MarshalText() ([]byte, error) {
	return f.AppendText(make([]byte, 0, 35))
}

// Ptr returns nil for the zero value (absent fort) and a pointer to the
// canonical string form otherwise. Named to mirror null.String.Ptr(),
// which every FortId field replaced — callers that used to write
// pokemon.PokestopId.Ptr() keep the same call shape. Centralizing this
// (rather than each site writing `if f.Valid() { s := f.String(); ... }`)
// makes "absent fort serializes as JSON null, never \"\"" a property of
// the type: a call site can no longer accidentally write an unconditional
// `s := f.String(); p = &s`, which would emit "" instead of null for an
// absent fort.
func (f FortId) Ptr() *string {
	if !f.Valid() {
		return nil
	}
	s := f.String()
	return &s
}

// UnmarshalText implements encoding.TextUnmarshaler. It parses into a local
// and assigns only on success, so a failed unmarshal never leaves a
// half-written receiver.
//
// The error text carries "unparseable" — the same word every decode-path
// ingest site uses for this failure — so it stays grep-compatible with them
// once a DB-scan loader wraps it (e.g. "Preload: pokestop scan error - %s").
// One shared token makes "did Niantic change the id format?" a single grep
// across both ingest logs and DB-scan logs, without needing every loader's
// wrapper message rewritten individually.
func (f *FortId) UnmarshalText(b []byte) error {
	parsed, ok := ParseFortId(string(b))
	if !ok {
		return fmt.Errorf("FortId.UnmarshalText: unparseable fort id, cannot parse %q", b)
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
	a, b := fortIdSuffixKey(f.Suffix), fortIdSuffixKey(o.Suffix)
	if a[0] != b[0] {
		return cmp.Compare(a[0], b[0])
	}
	return cmp.Compare(a[1], b[1])
}

// fortIdSuffixKey renders a suffix as the two bytes it occupies in the
// string form, zero-padded on the right for the absent/one-digit cases.
// Comparing these instead of the raw values is what keeps Compare equal to
// varchar order: ".16" sorts before ".2" as a string, while 16 > 2 as a
// number.
func fortIdSuffixKey(s uint8) [2]byte {
	switch {
	case s == 0:
		return [2]byte{}
	case s < 10:
		return [2]byte{'0' + s, 0}
	default:
		return [2]byte{'0' + s/10, '0' + s%10}
	}
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
