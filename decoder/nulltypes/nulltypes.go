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
	"database/sql"
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
