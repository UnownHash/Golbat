package decoder

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unsafe"

	log "github.com/sirupsen/logrus"
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
		SeenTypeCodeWild.String(), SeenTypeCodeEncounter.String(), SeenTypeCodeNearbyStop.String(), SeenTypeCodeCell.String(),
		SeenTypeCodeLureWild.String(), SeenTypeCodeLureEncounter.String(), SeenTypeCodeTappableEncounter.String(),
		SeenTypeCodeTappableLureEncounter.String(),
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
	if err := n.Scan(SeenTypeCodeEncounter.String()); err != nil {
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

// TestNullSeenTypeScanUnknownValueDegrades pins the deliberate asymmetry
// between the read path (Scan, this test) and the write path
// (TestNullSeenTypeValueOutOfRange) for a seen_type value this binary
// doesn't recognise.
//
// The enum has been widened three times (migrations 3, 43, 45), so a newer
// binary can write a value a rollback or a lagging replica on this binary
// doesn't know yet — routine, not exceptional, for a mixed deployment. An
// earlier round of this PR made Scan error here, reasoning that silently
// storing a wrong code would corrupt scan statistics — correct for the
// write side, wrong for this one: an error here fails the row load, which
// leaves getOrCreatePokemonRecord's cache entry stuck marked newRecord, so
// every subsequent sighting retries and fails the same load and that
// pokemon silently stops being processed for as long as the deployment is
// mixed. Scan must degrade to invalid (as if the column were NULL) and warn
// instead.
//
// Value stays strict on purpose: writing an out-of-enum string to the
// seen_type ENUM column is silently stored as "" by MariaDB, unrecoverable
// data loss with no read-side self-heal to fall back on. Read permissive,
// write strict — this is not an oversight, don't "fix" it back to
// symmetric. See NullSeenType.Scan's doc comment for the full reasoning.
func TestNullSeenTypeScanUnknownValueDegrades(t *testing.T) {
	var buf bytes.Buffer
	restore := log.StandardLogger().Out
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(restore) })

	// Start from a real value so a successful degrade is visibly resetting
	// it, not just leaving an already-zero value alone.
	n := SeenTypeFrom(SeenTypeCodeWild)
	if err := n.Scan("teleported"); err != nil {
		t.Fatalf("Scan of unknown seen type returned an error, want nil (degrade-and-warn): %v", err)
	}
	if n.Valid {
		t.Error("Scan of unknown seen type left Valid = true, want false")
	}
	if n.Code != SeenTypeCodeUnset {
		t.Errorf("Scan of unknown seen type left Code = %d, want SeenTypeCodeUnset", n.Code)
	}
	if !strings.Contains(buf.String(), "teleported") {
		t.Errorf("Scan of unknown seen type did not log a warning naming the value, got: %s", buf.String())
	}
}

// TestNullSeenTypeZeroValueIsUnset is the point of reserving code 0: without
// it, NullSeenType{}'s zero value would equal SeenTypeCodeWild, and any code
// that read .Code without checking .Valid would silently treat a NULL
// seen_type as wild. This asserts the sentinel is in place and that it can
// never reach the seen_type column as a real enum member.
func TestNullSeenTypeZeroValueIsUnset(t *testing.T) {
	var zero NullSeenType
	if zero.Code == SeenTypeCodeWild {
		t.Fatal("NullSeenType{}.Code == SeenTypeCodeWild — the zero-value hazard is back")
	}
	if zero.Code != SeenTypeCodeUnset {
		t.Errorf("NullSeenType{}.Code = %d, want SeenTypeCodeUnset (0)", zero.Code)
	}

	// Value() must refuse Unset even when explicitly marked Valid — a
	// caller-constructed NullSeenType, not just the Go zero value (which is
	// already caught by !n.Valid).
	explicit := SeenTypeFrom(SeenTypeCodeUnset)
	v, err := explicit.Value()
	if err == nil {
		t.Errorf("Value() for SeenTypeCodeUnset = %v, nil error; want a non-nil error", v)
	}
	if v != nil {
		t.Errorf("Value() for SeenTypeCodeUnset returned %v, want nil", v)
	}
}

func TestNullSeenTypeValueOutOfRange(t *testing.T) {
	// Code and SeenTypeFrom are both exported, so nothing at the type level
	// stops a caller from building a NullSeenType whose Code has no matching
	// string. Value() must refuse to hand such a code to the driver — writing
	// '' to the seen_type ENUM column would be silently accepted by MariaDB
	// and would corrupt scan statistics with no error anywhere in the path.
	n := SeenTypeFrom(SeenTypeCode(len(seenTypeStrings)))
	v, err := n.Value()
	if err == nil {
		t.Errorf("Value() for out-of-range code = %v, nil error; want a non-nil error", v)
	}
	if v != nil {
		t.Errorf("Value() for out-of-range code returned %v, want nil", v)
	}
}
