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
		"3f4938f1348c2bc00973eeb715552b42",    // bare (sponsored fort)
		"5cd5bb656e03d9867dfe1f05e4d5892f",    // bare
		"0d2a2b1f8d3c4e5a6b7c8d9e0f1a2b3c.16", // pokestop/gym
		"a1b2c3d4e5f60718293a4b5c6d7e8f90.11", // pokestop/gym/station
		"deadbeefcafef00dfeedfacebadc0de5.12", // pokestop/gym/station
		"763109934ddb4d98b9e0d09726be1950.23", // station
		"00000000000000000000000000000001",    // minimum nonzero bare
		"ffffffffffffffffffffffffffffffff.ff", // maximum
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
		"",                                     // empty (production junk row)
		"not-a-fort-id",                        // wrong shape
		"3f4938f1348c2bc00973eeb715552b4",      // 31 chars
		"3f4938f1348c2bc00973eeb715552b422",    // 33 chars
		"3f4938f1348c2bc00973eeb715552b42.",    // trailing dot only
		"3f4938f1348c2bc00973eeb715552b42.1",   // one suffix digit
		"3f4938f1348c2bc00973eeb715552b42.161", // three suffix digits
		"3f4938f1348c2bc00973eeb715552b42x16",  // separator not '.'
		"3F4938F1348C2BC00973EEB715552B42.16",  // uppercase hex
		"zzzz38f1348c2bc00973eeb715552b42.16",  // non-hex guid
		"3f4938f1348c2bc00973eeb715552b42.zz",  // non-hex suffix
		"3f4938f1348c2bc00973eeb715552b42.1G",  // partially non-hex suffix
		"00000000000000000000000000000000",     // all-zero bare == sentinel
		"00000000000000000000000000000000.00",  // canonicalizes to sentinel
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
