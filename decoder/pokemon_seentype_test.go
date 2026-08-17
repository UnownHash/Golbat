package decoder

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golbat/db"
	"golbat/jsonenc"
	"golbat/pogo"
	"golbat/util"

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
	//
	// Marshals through jsonenc rather than encoding/json directly, so
	// building this test under -tags go_json (as CI now does) round-trips
	// through goccy/go-json — the codec huma_api.go uses to serve every API
	// response — instead of pinning stdlib's output regardless of which
	// codec ships.
	var n NullSeenType
	if err := n.Scan(SeenTypeCodeEncounter.String()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	b, err := jsonenc.Marshal(n)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != `"encounter"` {
		t.Errorf("marshal = %s, want \"encounter\"", b)
	}

	b, err = jsonenc.Marshal(NullSeenType{})
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
	resetSeenTypeScanWarnThrottle(t)

	n := SeenTypeFrom(SeenTypeCodeWild)
	if err := n.Scan("teleported"); err != nil {
		t.Fatalf("Scan of unknown seen type returned an error, want nil (degrade-and-warn): %v", err)
	}
	if n.Valid {
		t.Error("Scan of unknown seen type left Valid = true, want false")
	}
	// Not Unset: Unset means "never set", and two decode switches treat that
	// as licence to rewrite the record (see
	// TestScanUnknownSeenTypeIsInertInTheDecodeSwitches). Unknown is the
	// distinct fact — "set to something this binary does not recognise" —
	// and matches no case anywhere.
	if n.Code != SeenTypeCodeUnknown {
		t.Errorf("Scan of unknown seen type left Code = %d, want SeenTypeCodeUnknown (%d)", n.Code, SeenTypeCodeUnknown)
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

// TestScanUnknownSeenTypeIsInertInTheDecodeSwitches is the regression for
// the degrade landing on SeenTypeCodeUnset. Unset is not an inert value: it
// means "never set", and two decode switches treat that as licence to fill
// the record in. Reaching them with a seen_type this binary merely doesn't
// recognise — the exact mixed-deployment case the degrade exists for —
// turned a read failure into active damage:
//
//   - updateFromWild's `case Unset, Cell, NearbyStop` rewrote the newer
//     binary's value to "wild" and persisted the downgrade over it.
//   - updateFromNearby's `case Unset, Cell` set overrideLatLon, replacing
//     the record's precise coordinates with the pokestop's.
//
// Pre-PR main held the unknown value as an opaque string, so every switch
// fell through to default and the record round-tripped unharmed. Unknown
// restores that: it equals none of the eight real codes, so it reaches no
// case in either switch.
func TestScanUnknownSeenTypeIsInertInTheDecodeSwitches(t *testing.T) {
	// Scanned, not constructed: the degrade is the thing under test, and a
	// hand-built SeenTypeFrom(SeenTypeCodeUnknown) would assert nothing
	// about what a real row load produces. The code itself is checked in
	// TestNullSeenTypeScanUnknownValueDegrades; the subtests below assert
	// only on the damage, so they stay meaningful against a build that
	// degrades to Unset.
	scanUnknown := func(t *testing.T) NullSeenType {
		t.Helper()
		var n NullSeenType
		if err := n.Scan("teleported"); err != nil {
			t.Fatalf("Scan of unknown seen type: %v", err)
		}
		return n
	}

	t.Run("updateFromWild does not downgrade to wild", func(t *testing.T) {
		p := &Pokemon{}
		p.Id = 42
		p.SeenType = scanUnknown(t)

		// SpawnPointId "0" keeps setExpireTimestampFromSpawnpoint off the
		// spawnpoint cache and the database (it returns early on spawn id 0).
		wild := &pogo.WildPokemonProto{
			EncounterId:  42,
			SpawnPointId: "0",
			Latitude:     10.5,
			Longitude:    20.5,
			Pokemon: &pogo.PokemonProto{
				PokemonId:      25,
				PokemonDisplay: &pogo.PokemonDisplayProto{},
			},
		}
		p.updateFromWild(context.Background(), db.DbDetails{}, wild, 99, nil, 1700000000000, "tester")

		if p.SeenType.Code == SeenTypeCodeWild || p.SeenType.Valid {
			t.Errorf("SeenType after updateFromWild = {Code: %d, Valid: %t}, want the unrecognised value left alone (Code %d, invalid) — the record was downgraded to wild and will be persisted that way over the newer binary's value",
				p.SeenType.Code, p.SeenType.Valid, SeenTypeCodeUnknown)
		}
	})

	t.Run("updateFromNearby keeps precise coordinates", func(t *testing.T) {
		const fortId = "seentype-unknown-stop"
		pokestopCache.Set(fortId, &Pokestop{PokestopData: PokestopData{
			Id:  fortId,
			Lat: 51.5,
			Lon: -0.12,
		}}, time.Minute)
		t.Cleanup(func() { pokestopCache.Delete(fortId) })

		p := &Pokemon{}
		p.Id = 43
		p.SeenType = scanUnknown(t)
		p.SetLat(10.5)
		p.SetLon(20.5)

		nearby := &pogo.NearbyPokemonProto{
			FortId:         fortId,
			PokedexNumber:  25,
			PokemonDisplay: &pogo.PokemonDisplayProto{},
		}
		p.updateFromNearby(context.Background(), db.DbDetails{}, nearby, 99, nil, 1700000000000, "tester")

		if p.Lat != 10.5 || p.Lon != 20.5 {
			t.Errorf("coordinates after updateFromNearby = (%v, %v), want (10.5, 20.5) — precise location replaced by the pokestop's",
				p.Lat, p.Lon)
		}
		if p.SeenType.Code == SeenTypeCodeNearbyStop || p.SeenType.Valid {
			t.Errorf("SeenType after updateFromNearby = {Code: %d, Valid: %t}, want the unrecognised value left alone (Code %d, invalid)",
				p.SeenType.Code, p.SeenType.Valid, SeenTypeCodeUnknown)
		}
		if p.PokestopId.Valid {
			t.Errorf("PokestopId after updateFromNearby = %q, want unset", p.PokestopId.ValueOrZero())
		}
	})
}

// TestScanUnknownSeenTypeWarnIsThrottled pins the aggregation. Scan runs
// once per row loaded — millions during preload, and under the entity lock
// at runtime — so one line per unrecognised row is log I/O proportional to
// the table. util.DropReporter is the codebase's aggregator for exactly
// this shape (see raw_limiter.go, fort_tracker.go).
func TestScanUnknownSeenTypeWarnIsThrottled(t *testing.T) {
	resetSeenTypeScanWarnThrottle(t)

	var buf bytes.Buffer
	restore := log.StandardLogger().Out
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(restore) })

	const rows = 500
	for range rows {
		var n NullSeenType
		if err := n.Scan("teleported"); err != nil {
			t.Fatalf("Scan of unknown seen type: %v", err)
		}
	}

	if got := strings.Count(buf.String(), "teleported"); got != 1 {
		t.Errorf("%d rows with an unrecognised seen_type logged %d warnings, want 1 (throttled to one per second)", rows, got)
	}
}

// resetSeenTypeScanWarnThrottle clears the shared throttle so a test's own
// Scan is guaranteed to log rather than being suppressed by a warning an
// earlier test emitted in the same second.
func resetSeenTypeScanWarnThrottle(t *testing.T) {
	t.Helper()
	previous := seenTypeScanWarns
	seenTypeScanWarns = &util.DropReporter{}
	t.Cleanup(func() { seenTypeScanWarns = previous })
}

// TestSetSeenTypeRefusesCodesWithNoStringForm pins the setter's guard. Taking
// a SeenTypeCode directly means the setter is the only gate left on what
// reaches the column, and the two output boundaries disagree about an invalid
// code: Value() errors, which fails a whole multi-row upsert at bind time
// with no retry, while MarshalJSON emits "" into the webhook. The setter
// keeps both out of that state by leaving the previous value alone.
func TestSetSeenTypeRefusesCodesWithNoStringForm(t *testing.T) {
	p := &Pokemon{}
	p.SetSeenType(SeenTypeCodeWild)
	p.dirty = false

	// Unset (the "never set" zero value), Unknown (Scan's degrade code, which
	// is always paired with Valid = false) and anything past the end of
	// seenTypeStrings all have no string form.
	for _, c := range []SeenTypeCode{SeenTypeCodeUnset, SeenTypeCodeUnknown, SeenTypeCode(len(seenTypeStrings)), 200} {
		p.SetSeenType(c)
		if p.SeenType != SeenTypeFrom(SeenTypeCodeWild) {
			t.Fatalf("SetSeenType(%d) replaced the stored value with %+v", c, p.SeenType)
		}
		if p.dirty {
			t.Fatalf("SetSeenType(%d) marked the record dirty", c)
		}
		if _, err := p.SeenType.Value(); err != nil {
			t.Fatalf("SetSeenType(%d) left a value Value() rejects: %v", c, err)
		}
	}

	// Every real code still stores.
	for _, c := range []SeenTypeCode{
		SeenTypeCodeWild, SeenTypeCodeEncounter, SeenTypeCodeNearbyStop, SeenTypeCodeCell,
		SeenTypeCodeLureWild, SeenTypeCodeLureEncounter, SeenTypeCodeTappableEncounter,
		SeenTypeCodeTappableLureEncounter,
	} {
		p.SetSeenType(c)
		if p.SeenType != SeenTypeFrom(c) {
			t.Errorf("SetSeenType(%d) stored %+v, want code %d", c, p.SeenType, c)
		}
	}
}
