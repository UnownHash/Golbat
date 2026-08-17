package decoder

import (
	"context"
	"os"
	"testing"

	"github.com/guregu/null/v6"
	"github.com/jmoiron/sqlx"

	_ "github.com/go-sql-driver/mysql"
)

// connectNullscanTestDB connects to GOLBAT_TEST_DSN for a pokemon round-trip
// test, skipping the test if it isn't set.
//
// Registers db.Close() via t.Cleanup, not a bare defer: t.Cleanup functions
// run after the test function's own defers have already fired, so a bare
// `defer db.Close()` would close the connection before a delete cleanup the
// caller registers afterward runs, silently skipping it. Cleanups run LIFO,
// so registering the close here — before the caller registers its own
// delete cleanup — makes it run last. That ordering was a real bug once
// already: a bare defer ran before the delete cleanup and silently left
// test rows in the shared dev database on every run.
func connectNullscanTestDB(t *testing.T) (*sqlx.DB, context.Context) {
	t.Helper()
	dsn := os.Getenv("GOLBAT_TEST_DSN")
	if dsn == "" {
		t.Skip("GOLBAT_TEST_DSN not set; skipping database round-trip test")
	}

	db, err := sqlx.Connect("mysql", dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	})

	return db, context.Background()
}

// TestPokemonNullColumnRoundTrip is the test the packing change exists to pass.
//
// Every nullable pokemon column is narrowed from guregu/null's 16-byte
// null.Int/null.Float wrappers to null.Value[T] instantiated at the narrow
// width (uint8/uint16/uint32/float32/bool). Those implement sql.Scanner via
// the embedded sql.Null[T], so a NULL should land as Valid=false — but that
// is a runtime property, not a compile-time one, and getting it wrong means
// the first production row with a null gender panics inside database/sql's
// convertAssign.
//
// Requires a MariaDB with the golbat schema. Set GOLBAT_TEST_DSN, e.g.
//
//	GOLBAT_TEST_DSN='user:pass@tcp(127.0.0.1:3306)/golbat_test?parseTime=true'
func TestPokemonNullColumnRoundTrip(t *testing.T) {
	db, ctx := connectNullscanTestDB(t)
	const testID = 999999999999999999

	t.Cleanup(func() {
		if _, err := db.ExecContext(ctx, "DELETE FROM pokemon WHERE id = ?", testID); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})

	// Insert with every nullable column explicitly NULL. Only the NOT NULL
	// columns get values.
	//
	// shiny is called out by name (rather than just omitted like the other
	// nullable columns) because it is the one nullable column in this table
	// with a non-NULL schema DEFAULT ('0', verified live via
	// information_schema.COLUMNS). Omitting it from the column list would
	// insert 0/Valid, not NULL, silently defeating the one check this test
	// exists to make for that field.
	_, err := db.ExecContext(ctx, `
		INSERT INTO pokemon (id, pokemon_id, lat, lon, first_seen_timestamp,
			changed, expire_timestamp_verified, is_event, shiny)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
		testID, 25, 51.5, -0.1, 1000, 2000, 0, 0)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	var got Pokemon
	err = db.GetContext(ctx, &got,
		"SELECT "+pokemonSelectColumns+" FROM pokemon WHERE id = ?", testID)
	if err != nil {
		t.Fatalf("select: %v", err)
	}

	// Every nullable field must come back invalid, not zero-and-valid.
	checks := []struct {
		name  string
		valid bool
	}{
		{"SpawnId", got.SpawnId.Valid},
		{"CellId", got.CellId.Valid},
		{"ExpireTimestamp", got.ExpireTimestamp.Valid},
		{"Updated", got.Updated.Valid},
		{"Weight", got.Weight.Valid},
		{"Height", got.Height.Valid},
		{"Iv", got.Iv.Valid},
		{"Move1", got.Move1.Valid},
		{"Move2", got.Move2.Valid},
		{"Cp", got.Cp.Valid},
		{"Form", got.Form.Valid},
		{"DisplayPokemonId", got.DisplayPokemonId.Valid},
		{"DisplayPokemonForm", got.DisplayPokemonForm.Valid},
		{"Gender", got.Gender.Valid},
		{"AtkIv", got.AtkIv.Valid},
		{"DefIv", got.DefIv.Valid},
		{"StaIv", got.StaIv.Valid},
		{"Level", got.Level.Valid},
		{"Weather", got.Weather.Valid},
		{"Costume", got.Costume.Valid},
		{"Size", got.Size.Valid},
		{"IsStrong", got.IsStrong.Valid},
		{"Shiny", got.Shiny.Valid},
		// The two interned columns: a NULL must land on the reserved null
		// handle, not on whatever string happens to sit at index 0.
		{"PokestopId", got.PokestopId.Valid()},
		{"Username", got.Username.Valid()},
	}
	for _, c := range checks {
		if c.valid {
			t.Errorf("%s.Valid = true after scanning NULL, want false", c.name)
		}
	}

	// And the NOT NULL columns must have survived intact.
	if got.PokemonId != 25 {
		t.Errorf("PokemonId = %d, want 25", got.PokemonId)
	}
	if got.Lat != 51.5 {
		t.Errorf("Lat = %v, want 51.5", got.Lat)
	}
}

// TestPokemonFullRowRoundTrip writes a fully-populated row through the same
// upsert the write-behind queue uses, then reads it back, proving driver.Valuer
// binds correctly for every narrowed type.
// The two interned values used by TestPokemonFullRowRoundTrip. The pokestop
// id is shaped like a real fort id (32 hex characters and a suffix); the
// username is shaped like a scanner account.
const (
	roundTripPokestopId = "2eb4a5b1e1e94b2ab0b3c8f0d0000000.16"
	roundTripUsername   = "RoundTripScanner01"
)

func TestPokemonFullRowRoundTrip(t *testing.T) {
	db, ctx := connectNullscanTestDB(t)
	const testID = 999999999999999998

	t.Cleanup(func() {
		if _, err := db.ExecContext(ctx, "DELETE FROM pokemon WHERE id = ?", testID); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})

	want := PokemonData{
		Id:                      testID,
		PokemonId:               149,
		Lat:                     51.50000000000001,
		Lon:                     -0.10000000000001,
		FirstSeenTimestamp:      1000,
		Changed:                 2000,
		ExpireTimestampVerified: true,
		// -16, chosen as the signed equivalent of the old test's
		// 0xFFFFFFFFFFFFFFF0 bit pattern: cell_id is a signed bigint and real
		// S2 cell ids are frequently negative (see decoder/pokemon.go's
		// SetCellId), so this exercises that a negative value round-trips.
		CellId: null.ValueFrom(int64(-16)),
		// SpawnId is null.Value[int64] (not [uint64]) specifically because
		// sql.Null[uint64]'s Value() refuses any value with the high bit
		// set, even though spawn_id is bigint unsigned — see SetSpawnId's
		// doc comment. A plain positive value like this one round-trips
		// under either type, so it doesn't exercise that boundary, but it
		// does prove the chosen type binds and scans correctly end to end
		// against the real column, which nothing here previously did.
		SpawnId: null.ValueFrom(int64(424242)),
		// The interned columns. Their Valuer has to hand the driver the
		// original string, and their Scanner has to intern whatever comes
		// back to the same handle — the whole round trip lives in a pair of
		// methods that no unit test can exercise against a real driver.
		PokestopId: InternPokestopId(roundTripPokestopId),
		Username:   InternUsername(roundTripUsername),
		AtkIv:      null.ValueFrom(uint8(15)),
		DefIv:      null.ValueFrom(uint8(14)),
		StaIv:      null.ValueFrom(uint8(13)),
		Level:      null.ValueFrom(uint8(35)),
		Cp:         null.ValueFrom(uint16(3500)),
		Move1:      null.ValueFrom(uint16(216)),
		Gender:     null.ValueFrom(uint8(1)),
		Size:       null.ValueFrom(uint8(5)),
		Shiny:      null.ValueFrom(true),
		Weight:     null.ValueFrom(float32(3.5)),
		Iv:         null.ValueFrom(float32(93.33)),
		SeenType:   SeenTypeFrom(SeenTypeCodeLureEncounter),
	}

	if _, err := db.NamedExecContext(ctx, pokemonBatchUpsertQuery, []PokemonData{want}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	var got Pokemon
	if err := db.GetContext(ctx, &got,
		"SELECT "+pokemonSelectColumns+" FROM pokemon WHERE id = ?", testID); err != nil {
		t.Fatalf("select: %v", err)
	}

	if got.CellId != want.CellId {
		t.Errorf("CellId = %#x, want %#x (negative signed values must survive)", got.CellId.V, want.CellId.V)
	}
	if got.SpawnId != want.SpawnId {
		t.Errorf("SpawnId = %v, want %v", got.SpawnId, want.SpawnId)
	}
	if got.Cp != want.Cp {
		t.Errorf("Cp = %v, want %v", got.Cp, want.Cp)
	}
	if got.AtkIv != want.AtkIv {
		t.Errorf("AtkIv = %v, want %v", got.AtkIv, want.AtkIv)
	}
	if got.Shiny != want.Shiny {
		t.Errorf("Shiny = %v, want %v", got.Shiny, want.Shiny)
	}
	// Weight and Iv are the pinning case for NullFloat32: Value() widens the
	// stored float32 to float64(n.V) on the way in, and MariaDB rounds it to
	// the column's own precision (double(18,14) for weight, float(5,2) for
	// iv) rather than the driver. Fixture values are chosen to already sit on
	// exact boundaries of both precisions so this only checks the narrowed
	// type's round-trip, not MariaDB's rounding behavior.
	if got.Weight != want.Weight {
		t.Errorf("Weight = %v, want %v", got.Weight, want.Weight)
	}
	if got.Iv != want.Iv {
		t.Errorf("Iv = %v, want %v", got.Iv, want.Iv)
	}
	if got.Lat != want.Lat {
		t.Errorf("Lat = %.14f, want %.14f (float64 precision must survive)", got.Lat, want.Lat)
	}
	if got.SeenType != want.SeenType {
		t.Errorf("SeenType = %+v, want %+v", got.SeenType, want.SeenType)
	}
	if got.SeenType.ValueOrZero() != SeenTypeCodeLureEncounter.String() {
		t.Errorf("SeenType.ValueOrZero() = %q, want %q (the enum column must store this exact string)", got.SeenType.ValueOrZero(), SeenTypeCodeLureEncounter.String())
	}

	// Interning is canonical, so a correct round trip returns the very same
	// handle — a different handle would mean the string came back altered.
	if got.PokestopId != want.PokestopId {
		t.Errorf("PokestopId handle = %d, want %d (resolved %q vs %q)",
			got.PokestopId, want.PokestopId, got.PokestopId.ValueOrZero(), want.PokestopId.ValueOrZero())
	}
	if got.Username != want.Username {
		t.Errorf("Username handle = %d, want %d (resolved %q vs %q)",
			got.Username, want.Username, got.Username.ValueOrZero(), want.Username.ValueOrZero())
	}

	// And read the two columns as plain strings, bypassing the Scanner
	// entirely: this is the check that the bytes sitting in the database are
	// the ones the interned Valuer was supposed to write, rather than a
	// Valuer/Scanner pair that agrees with itself while storing something
	// else.
	var rawPokestopId, rawUsername string
	if err := db.QueryRowContext(ctx,
		"SELECT pokestop_id, username FROM pokemon WHERE id = ?", testID,
	).Scan(&rawPokestopId, &rawUsername); err != nil {
		t.Fatalf("raw select: %v", err)
	}
	if rawPokestopId != roundTripPokestopId {
		t.Errorf("stored pokestop_id = %q, want %q (the column must be byte-identical)", rawPokestopId, roundTripPokestopId)
	}
	if rawUsername != roundTripUsername {
		t.Errorf("stored username = %q, want %q (the column must be byte-identical)", rawUsername, roundTripUsername)
	}
}

// TestPokemonInternedColumnsLoadPreExistingRow proves rows written before the
// interning change still load: the values go in through raw SQL, never
// touching driver.Valuer, and must come back as ordinary interned handles.
func TestPokemonInternedColumnsLoadPreExistingRow(t *testing.T) {
	db, ctx := connectNullscanTestDB(t)
	const testID = 999999999999999997
	const legacyPokestopId = "0f1e2d3c4b5a69788796a5b4c3d2e1f0.16"
	const legacyUsername = "LegacyRowScanner"

	t.Cleanup(func() {
		if _, err := db.ExecContext(ctx, "DELETE FROM pokemon WHERE id = ?", testID); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})

	if _, err := db.ExecContext(ctx, `
		INSERT INTO pokemon (id, pokemon_id, lat, lon, first_seen_timestamp,
			changed, expire_timestamp_verified, is_event, pokestop_id, username)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		testID, 25, 51.5, -0.1, 1000, 2000, 0, 0, legacyPokestopId, legacyUsername); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var got Pokemon
	if err := db.GetContext(ctx, &got,
		"SELECT "+pokemonSelectColumns+" FROM pokemon WHERE id = ?", testID); err != nil {
		t.Fatalf("select: %v", err)
	}

	if !got.PokestopId.Valid() || got.PokestopId.ValueOrZero() != legacyPokestopId {
		t.Errorf("PokestopId = %q (valid %v), want %q", got.PokestopId.ValueOrZero(), got.PokestopId.Valid(), legacyPokestopId)
	}
	if !got.Username.Valid() || got.Username.ValueOrZero() != legacyUsername {
		t.Errorf("Username = %q (valid %v), want %q", got.Username.ValueOrZero(), got.Username.Valid(), legacyUsername)
	}
}
