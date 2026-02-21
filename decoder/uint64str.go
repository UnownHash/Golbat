package decoder

import (
	"database/sql/driver"
	"fmt"
	"strconv"
)

// Uint64Str is a uint64 that serializes to/from database as a string.
// This is useful for columns that store large integers as VARCHAR.
type Uint64Str uint64

// Value implements driver.Valuer - outputs as string for database writes
func (u Uint64Str) Value() (driver.Value, error) {
	return strconv.FormatUint(uint64(u), 10), nil
}

// Scan implements sql.Scanner - reads string from database into uint64
func (u *Uint64Str) Scan(src interface{}) error {
	if src == nil {
		*u = 0
		return nil
	}

	switch v := src.(type) {
	case string:
		parsed, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return fmt.Errorf("Uint64Str.Scan: cannot parse %q: %w", v, err)
		}
		*u = Uint64Str(parsed)
	case []byte:
		parsed, err := strconv.ParseUint(string(v), 10, 64)
		if err != nil {
			return fmt.Errorf("Uint64Str.Scan: cannot parse %q: %w", string(v), err)
		}
		*u = Uint64Str(parsed)
	case int64:
		*u = Uint64Str(v)
	case uint64:
		*u = Uint64Str(v)
	default:
		return fmt.Errorf("Uint64Str.Scan: unsupported type %T", src)
	}
	return nil
}

// Uint64 returns the underlying uint64 value
func (u Uint64Str) Uint64() uint64 {
	return uint64(u)
}

// String returns the string representation
func (u Uint64Str) String() string {
	return strconv.FormatUint(uint64(u), 10)
}
