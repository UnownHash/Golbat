package nulltypes

import (
	"encoding/json"
	"math"
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
	n := Uint8From(42) // start valid, so we prove Scan(nil) clears it
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
// decoder/preload.go. Also tests the []byte path for completeness.
func TestUint64FullRange(t *testing.T) {
	t.Run("int64-path", func(t *testing.T) {
		cellID := uint64(0xFFFFFFFFFFFFFFF0)
		cellIDAsInt := int64(cellID) // bit reinterpretation
		var n NullUint64
		if err := n.Scan(cellIDAsInt); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if !n.Valid || n.V != cellID {
			t.Errorf("Scan = {%#x %t}, want {%#x true}", n.V, n.Valid, cellID)
		}
		v, err := n.Value()
		if err != nil {
			t.Fatalf("Value: %v", err)
		}
		if v != cellIDAsInt {
			t.Errorf("Value = %v, want %v", v, cellIDAsInt)
		}
	})

	t.Run("bytes-path", func(t *testing.T) {
		// Test that []byte path also survives the full range via ParseUint fallback.
		bytesVal := []byte("14219788758686216192") // decimal representation of 0xC5A0000000000000
		var n NullUint64
		if err := n.Scan(bytesVal); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if !n.Valid || n.V != 14219788758686216192 {
			t.Errorf("Scan bytes = {%d %t}, want {14219788758686216192 true}", n.V, n.Valid)
		}
	})
}

// TestFloat32JSONIsNarrower verifies that NullFloat32 deliberately produces
// narrower JSON than guregu/null (bitSize 32 vs 64). This is the expected
// behavior — the values come from protobuf float fields promoted through
// float64(), so the extra digits in bitSize 64 are promotion noise with no
// information.
func TestFloat32JSONIsNarrower(t *testing.T) {
	val := float32(6.7)
	mine, err := json.Marshal(Float32From(val))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	theirs, err := json.Marshal(null.FloatFrom(float64(val)))
	if err != nil {
		t.Fatalf("marshal guregu: %v", err)
	}
	// Verify the divergence is intentional: mine should be narrower
	if string(mine) == string(theirs) {
		t.Errorf("marshal = %s, but should differ from guregu = %s (bitSize 32 vs 64)", mine, theirs)
	}
	// Verify mine is the cleaner output
	if string(mine) != "6.7" {
		t.Errorf("marshal = %s, want 6.7 (bitSize 32 produces clean output)", mine)
	}
}

// TestFloat32JSONNaNInf verifies that NaN and Inf values return an error
// rather than emitting invalid JSON tokens.
func TestFloat32JSONNaNInf(t *testing.T) {
	cases := []struct {
		name string
		val  float32
	}{
		{"NaN", float32(math.NaN())},
		{"Inf", float32(math.Inf(1))},
		{"NegInf", float32(math.Inf(-1))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n := Float32From(c.val)
			_, err := json.Marshal(n)
			if err == nil {
				t.Error("MarshalJSON returned nil error, want UnsupportedValueError")
			}
		})
	}
}
