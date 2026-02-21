package decoder

import (
	"testing"

	"github.com/jmoiron/sqlx"
)

func TestUint64StrWithSqlxNamed(t *testing.T) {
	// Simple struct with Uint64Str field
	type TestRecord struct {
		Id   Uint64Str `db:"id"`
		Name string    `db:"name"`
	}

	records := []TestRecord{
		{Id: Uint64Str(12345678901234567890), Name: "first"},
		{Id: Uint64Str(9876543210987654321), Name: "second"},
		{Id: Uint64Str(1111111111111111111), Name: "third"},
	}

	query := `INSERT INTO test (id, name) VALUES (:id, :name)`

	// Call sqlx.Named with slice of records
	expandedQuery, args, err := sqlx.Named(query, records)
	if err != nil {
		t.Fatalf("sqlx.Named failed: %v", err)
	}

	// Verify query was expanded for 3 records (sqlx uses no space after comma)
	expectedQuery := `INSERT INTO test (id, name) VALUES (?, ?),(?, ?),(?, ?)`
	if expandedQuery != expectedQuery {
		t.Errorf("Query mismatch:\nexpected: %s\ngot:      %s", expectedQuery, expandedQuery)
	}

	// Verify we have 6 args (2 per record * 3 records)
	if len(args) != 6 {
		t.Fatalf("Expected 6 args, got %d", len(args))
	}

	// sqlx.Named extracts the Uint64Str values as-is; the database driver
	// will call Value() when executing to convert to string.
	// Here we verify the Uint64Str values are present and correct.
	expectedIds := []Uint64Str{
		Uint64Str(12345678901234567890),
		Uint64Str(9876543210987654321),
		Uint64Str(1111111111111111111),
	}
	expectedNames := []string{"first", "second", "third"}

	for i := 0; i < 3; i++ {
		idArg, ok := args[i*2].(Uint64Str)
		if !ok {
			t.Errorf("Arg[%d] is not Uint64Str: got %T", i*2, args[i*2])
			continue
		}
		if idArg != expectedIds[i] {
			t.Errorf("Arg[%d] ID mismatch: expected %d, got %d", i*2, expectedIds[i], idArg)
		}

		nameArg, ok := args[i*2+1].(string)
		if !ok {
			t.Errorf("Arg[%d] is not string: got %T", i*2+1, args[i*2+1])
			continue
		}
		if nameArg != expectedNames[i] {
			t.Errorf("Arg[%d] name mismatch: expected %s, got %s", i*2+1, expectedNames[i], nameArg)
		}
	}

	// Verify that Value() produces strings (this is what the DB driver will call)
	for i, id := range expectedIds {
		val, err := id.Value()
		if err != nil {
			t.Errorf("Value() error for record %d: %v", i, err)
			continue
		}
		strVal, ok := val.(string)
		if !ok {
			t.Errorf("Value() for record %d returned %T, expected string", i, val)
			continue
		}
		t.Logf("Record %d: Uint64Str(%d) -> Value() -> %q", i, id, strVal)
	}

	t.Logf("Expanded query: %s", expandedQuery)
	t.Logf("Args types: %T, %T, %T, %T, %T, %T", args[0], args[1], args[2], args[3], args[4], args[5])
}

func TestUint64StrValuer(t *testing.T) {
	tests := []struct {
		input    Uint64Str
		expected string
	}{
		{Uint64Str(0), "0"},
		{Uint64Str(12345), "12345"},
		{Uint64Str(18446744073709551615), "18446744073709551615"}, // max uint64
	}

	for _, tc := range tests {
		val, err := tc.input.Value()
		if err != nil {
			t.Errorf("Value() error for %d: %v", tc.input, err)
			continue
		}
		strVal, ok := val.(string)
		if !ok {
			t.Errorf("Value() returned %T, expected string", val)
			continue
		}
		if strVal != tc.expected {
			t.Errorf("Value() = %q, expected %q", strVal, tc.expected)
		}
	}
}

func TestUint64StrScanner(t *testing.T) {
	tests := []struct {
		input    interface{}
		expected Uint64Str
	}{
		{"12345", Uint64Str(12345)},
		{[]byte("67890"), Uint64Str(67890)},
		{int64(99999), Uint64Str(99999)},
		{uint64(88888), Uint64Str(88888)},
		{nil, Uint64Str(0)},
	}

	for _, tc := range tests {
		var u Uint64Str
		err := u.Scan(tc.input)
		if err != nil {
			t.Errorf("Scan(%v) error: %v", tc.input, err)
			continue
		}
		if u != tc.expected {
			t.Errorf("Scan(%v) = %d, expected %d", tc.input, u, tc.expected)
		}
	}
}
