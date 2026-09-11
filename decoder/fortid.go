package decoder

import (
	"bytes"
	"cmp"
	"database/sql/driver"
	"encoding/hex"
	"fmt"
	"sync"

	log "github.com/sirupsen/logrus"

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
	var sawDotZero bool
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
		sawDotZero = f.Suffix == 0
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
	if sawDotZero {
		// Only warn once the id is known to actually succeed — an all-zero
		// GUID with ".00" is rejected above as the sentinel, and must not
		// burn the one-shot slot a genuine occurrence needs.
		fortIdDotZeroWarn.Do(func() {
			log.Warnf("[FORTID] fort id %q has a .00 suffix, which has never been observed; "+
				"treating it as the bare form (see decoder/fortid.go)", s)
		})
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
