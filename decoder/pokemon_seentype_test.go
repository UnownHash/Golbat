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
