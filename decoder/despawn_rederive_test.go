package decoder

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"golbat/db"
	"golbat/derive/despawn"
	"golbat/pogo"

	"github.com/golang/geo/s2"
	"github.com/guregu/null/v6"
	"github.com/jmoiron/sqlx"
)

// derivedDespawnBatchWild builds the wild sighting the two batch-level tests
// below replay. Everything except the timestamp is held identical across the
// two decodes, so the only thing that can re-admit the second one at
// UpdatePokemonBatch's gate is the expiry clause.
func derivedDespawnBatchWild(encId uint64, spawnHex string, timestampMs int64) RawWildPokemonData {
	return RawWildPokemonData{
		Cell:      5000000000000000000,
		Timestamp: timestampMs,
		Data: &pogo.WildPokemonProto{
			EncounterId:      encId,
			SpawnPointId:     spawnHex,
			Latitude:         51.5007,
			Longitude:        -0.1246,
			TimeTillHiddenMs: 0, // no TTH: spawnpointUpdateFromWild takes its lock-free path
			Pokemon: &pogo.PokemonProto{
				PokemonId:      pogo.HoloPokemonId_RATTATA,
				PokemonDisplay: &pogo.PokemonDisplayProto{},
			},
		},
	}
}

// seedCachedSpawnpoint puts a spawnpoint into the cache in the state the
// batch tests need: synced fast mirrors, a fresh LastSeen so
// spawnpointUpdateFromWild's lock-free path returns before it can touch the
// nil database, and whatever despawn_sec the caller asks for.
func seedCachedSpawnpoint(t *testing.T, spawnID int64, despawnSec null.Int) *Spawnpoint {
	t.Helper()
	sp := &Spawnpoint{SpawnpointData: SpawnpointData{
		Id:         spawnID,
		DespawnSec: despawnSec,
		LastSeen:   time.Now().Unix(),
	}}
	sp.syncFastFields()
	spawnpointCache.Set(spawnID, sp, 0)
	t.Cleanup(func() { spawnpointCache.Delete(spawnID) })
	return sp
}

// A derived expiry is unverified, and that is what corrects a 60-minute
// spawnpoint without writing anything to it. The first sighting derives the
// first half hour; the same Pokemon still listed after that expiry is
// re-admitted by wildSignificantUpdate (unverified and passed), and deriving
// again lands on the next half hour. The spawnpoint's despawn_sec stays
// unknown throughout: only a TTH sighting writes it.
//
// Through UpdatePokemonBatch, because the re-admission is the gate's, and a
// test calling the despawn functions directly could not see whether the
// second sighting is processed at all.
func TestUpdatePokemonBatchRederivesPastAnUnverifiedExpiry(t *testing.T) {
	sink := lureTestSetup(t)

	const spawnHex = "2b7c1a9d0e3"
	const spawnID = int64(0x2b7c1a9d0e3)
	const encId = uint64(920201)

	sp := seedCachedSpawnpoint(t, spawnID, null.Int{})
	pokemonCache.Delete(encId)
	t.Cleanup(func() { pokemonCache.Delete(encId) })

	first := time.Date(2026, 8, 23, 12, 17, 33, 0, time.UTC)
	params := ScanParameters{ProcessWild: true}
	UpdatePokemonBatch(context.Background(), db.DbDetails{}, params,
		[]RawWildPokemonData{derivedDespawnBatchWild(encId, spawnHex, first.UnixMilli())},
		nil, nil, nil, "tester")

	p, unlock, _ := peekPokemonRecordReadOnly(encId, "test")
	if p == nil {
		t.Fatal("wild pokemon not in cache after the first batch")
	}
	derived := int64(p.ExpireTimestamp.ValueOrZero())
	verified := p.ExpireTimestampVerified
	unlock()
	if want := despawn.NextOccurrence(spawnID, first).Unix(); derived != want {
		t.Fatalf("first expiry = %d, want the derived %d", derived, want)
	}
	if verified {
		t.Fatal("a derived expiry was written verified; it must not be, or a sighting past it is never re-admitted")
	}

	sink.drain()

	// Still listed five minutes after that expiry: a 60-minute spawnpoint.
	second := time.Unix(derived+300, 0)
	UpdatePokemonBatch(context.Background(), db.DbDetails{}, params,
		[]RawWildPokemonData{derivedDespawnBatchWild(encId, spawnHex, second.UnixMilli())},
		nil, nil, nil, "tester")

	p, unlock, _ = peekPokemonRecordReadOnly(encId, "test")
	if p == nil {
		t.Fatal("wild pokemon left the cache")
	}
	got := int64(p.ExpireTimestamp.ValueOrZero())
	unlock()
	if got != derived+1800 {
		t.Errorf("expiry after a sighting past it = %d, want the next half hour %d", got, derived+1800)
	}
	if len(sink.drain()) == 0 {
		t.Error("no webhook when a Pokemon presumed gone at its derived expiry was re-derived: consumers " +
			"hid it at that expiry and never hear it is still there")
	}
	if sp.DespawnSec.Valid {
		t.Errorf("despawn_sec = %d was written to the spawnpoint; only a TTH sighting may write it", sp.DespawnSec.Int64)
	}
}

// A verified expiry computed an hour late is corrected by a sighting that
// places the Pokemon before its despawn. An encounter received a moment after
// the despawn second, with the receipt time as its clock, wraps forward to the
// next hour; a later GMO whose server time is still before the despawn gives
// the right answer, and applyVerifiedDespawn must accept it — even though
// both expiries sit on the same second-of-hour.
//
// This pins applyVerifiedDespawn only. In production the GMO reaches it when
// the wild gate re-admits the record: a Ditto (whose stored species never
// matches the disguise) or any display change. An unchanged non-Ditto with a
// verified, not-yet-passed expiry is not re-admitted, and keeps the hour-late
// expiry.
func TestApplyVerifiedDespawnCorrectsAnHourLateExpiry(t *testing.T) {
	despawnAt := time.Date(2026, 8, 23, 12, 25, 0, 0, time.UTC)
	despawnSecond := despawnSecondOfHour(despawnAt.Unix())

	p := &Pokemon{}
	p.applyVerifiedDespawn(despawnSecond, despawnAt.Add(time.Second).UnixMilli()) // receipt 12:25:01
	if got := int64(p.ExpireTimestamp.ValueOrZero()); got != despawnAt.Add(time.Hour).Unix() {
		t.Fatalf("fixture assumption violated: the late receipt should give 13:25, got %d", got)
	}
	p.FirstSeenTimestamp = uint32(despawnAt.Add(time.Second).Unix())

	p.applyVerifiedDespawn(despawnSecond, despawnAt.Add(-time.Second).UnixMilli()) // GMO 12:24:59
	if got := int64(p.ExpireTimestamp.ValueOrZero()); got != despawnAt.Unix() {
		t.Errorf("expiry = %d, want %d: a sighting before the despawn must replace an hour-late expiry", got, despawnAt.Unix())
	}
}

// The wrap clamp counts only when it changes the stored expiry. A record
// re-decoded after its despawn — here a weather change, as a Ditto's disguise
// or any display change also causes — recomputes the same second-of-hour and
// clamps back to the value it already holds; counting that would put a
// permanent baseline on golbat_despawn_wrap_clamped_total. Both halves are
// asserted: the expiry is still corrected, and the counter stays at zero.
func TestUpdatePokemonBatchStoredDespawnDoesNotReCountTheWrapClamp(t *testing.T) {
	lureTestSetup(t)
	fake := withDespawnCountingCollector(t)

	const spawnHex = "2b7c1a9d0e4"
	const spawnID = int64(0x2b7c1a9d0e4)
	const encId = uint64(920202)

	seedCachedSpawnpoint(t, spawnID, null.IntFrom(1500))
	pokemonCache.Delete(encId)
	t.Cleanup(func() { pokemonCache.Delete(encId) })

	first := time.Date(2026, 8, 23, 12, 17, 33, 0, time.UTC)
	params := ScanParameters{ProcessWild: true}
	UpdatePokemonBatch(context.Background(), db.DbDetails{}, params,
		[]RawWildPokemonData{derivedDespawnBatchWild(encId, spawnHex, first.UnixMilli())},
		nil, nil, nil, "tester")

	p, unlock, _ := peekPokemonRecordReadOnly(encId, "test")
	if p == nil {
		t.Fatal("wild pokemon not in cache after the first batch")
	}
	storedExpiry := int64(p.ExpireTimestamp.ValueOrZero())
	unlock()
	if fake.wrapClamped != 0 {
		t.Fatalf("first decode clamped %d times, want 0", fake.wrapClamped)
	}

	second := derivedDespawnBatchWild(encId, spawnHex, time.Unix(storedExpiry+60, 0).UnixMilli())
	second.Data.Pokemon.PokemonDisplay = &pogo.PokemonDisplayProto{WeatherBoostedCondition: pogo.GameplayWeatherProto_RAINY}
	UpdatePokemonBatch(context.Background(), db.DbDetails{}, params,
		[]RawWildPokemonData{second}, nil, nil, nil, "tester")

	p, unlock, _ = peekPokemonRecordReadOnly(encId, "test")
	if p == nil {
		t.Fatal("wild pokemon vanished from the cache")
	}
	gotExpiry := int64(p.ExpireTimestamp.ValueOrZero())
	gotWeather := int64OrZero(p.Weather)
	unlock()

	if gotWeather != int64(pogo.GameplayWeatherProto_RAINY) {
		t.Fatal("fixture assumption violated: the second sighting was not processed")
	}
	if gotExpiry != storedExpiry {
		t.Fatalf("re-decode past a stored despawn moved the expiry: got %d want %d (the clamp must still correct the +3600 wrap)", gotExpiry, storedExpiry)
	}
	if fake.wrapClamped != 0 {
		t.Fatalf("re-confirming an unchanged expiry incremented golbat_despawn_wrap_clamped_total %d time(s), want 0", fake.wrapClamped)
	}
}

// A sighting a moment past a verified despawn is a late sighting of that same
// despawn, not a Pokemon with an hour to live. despawn_sec is stored to within
// 2s, so the game can list a Pokemon just past it; recomputing from there
// wraps forward an hour, and the clamp misses a Pokemon first seen within two
// minutes of its despawn. Through UpdatePokemonBatch, with the second
// sighting re-admitted by a weather change, as a Ditto is on every GMO.
func TestUpdatePokemonBatchDoesNotResurrectALateSighting(t *testing.T) {
	lureTestSetup(t)

	const spawnHex = "2b7c1a9d0e5"
	const spawnID = int64(0x2b7c1a9d0e5)
	const encId = uint64(920203)
	despawnAt := time.Date(2026, 8, 23, 12, 25, 0, 0, time.UTC)

	seedCachedSpawnpoint(t, spawnID, null.IntFrom(int64(despawnSecondOfHour(despawnAt.Unix()))))
	pokemonCache.Delete(encId)
	t.Cleanup(func() { pokemonCache.Delete(encId) })

	params := ScanParameters{ProcessWild: true}
	UpdatePokemonBatch(context.Background(), db.DbDetails{}, params,
		[]RawWildPokemonData{derivedDespawnBatchWild(encId, spawnHex, despawnAt.Add(-60*time.Second).UnixMilli())},
		nil, nil, nil, "tester")

	late := derivedDespawnBatchWild(encId, spawnHex, despawnAt.Add(time.Second).UnixMilli())
	late.Data.Pokemon.PokemonDisplay = &pogo.PokemonDisplayProto{WeatherBoostedCondition: pogo.GameplayWeatherProto_RAINY}
	UpdatePokemonBatch(context.Background(), db.DbDetails{}, params, []RawWildPokemonData{late}, nil, nil, nil, "tester")

	p, unlock, _ := peekPokemonRecordReadOnly(encId, "test")
	if p == nil {
		t.Fatal("wild pokemon not in cache")
	}
	got, weather := int64(p.ExpireTimestamp.ValueOrZero()), int64OrZero(p.Weather)
	unlock()
	if weather != int64(pogo.GameplayWeatherProto_RAINY) {
		t.Fatal("fixture assumption violated: the late sighting was not processed")
	}
	if got != despawnAt.Unix() {
		t.Errorf("expiry = %d (%+ds from the despawn): a sighting 1s past a verified despawn moved it", got, got-despawnAt.Unix())
	}
}

// A Pokemon on a half-hour spawnpoint can be listed a moment after its derived
// expiry: the derived second can differ from the game's by a couple of
// seconds. That moment must not move it half an hour on.
func TestUpdatePokemonBatchToleratesASightingJustPastADerivedExpiry(t *testing.T) {
	lureTestSetup(t)

	const spawnHex = "2b7c1a9d0e7"
	const spawnID = int64(0x2b7c1a9d0e7)
	const encId = uint64(920204)

	seedCachedSpawnpoint(t, spawnID, null.Int{})
	pokemonCache.Delete(encId)
	t.Cleanup(func() { pokemonCache.Delete(encId) })

	first := time.Date(2026, 8, 23, 12, 17, 33, 0, time.UTC)
	params := ScanParameters{ProcessWild: true}
	UpdatePokemonBatch(context.Background(), db.DbDetails{}, params,
		[]RawWildPokemonData{derivedDespawnBatchWild(encId, spawnHex, first.UnixMilli())},
		nil, nil, nil, "tester")
	p, unlock, _ := peekPokemonRecordReadOnly(encId, "test")
	derived := int64(p.ExpireTimestamp.ValueOrZero())
	unlock()

	UpdatePokemonBatch(context.Background(), db.DbDetails{}, params,
		[]RawWildPokemonData{derivedDespawnBatchWild(encId, spawnHex, time.Unix(derived+3, 0).UnixMilli())},
		nil, nil, nil, "tester")
	p, unlock, _ = peekPokemonRecordReadOnly(encId, "test")
	got := int64(p.ExpireTimestamp.ValueOrZero())
	unlock()
	if got != derived {
		t.Errorf("expiry = %d, want %d kept: a sighting 3s past a derived expiry moved it %+ds", got, derived, got-derived)
	}
}

// An encounter's timestamp is its receipt time. It must not move an expiry
// already derived for the spawnpoint unless that expiry passed longer ago
// than receipt lag explains — but it does replace a guess, which is not on
// the spawnpoint's phase.
func TestUntrustedTimeKeepsADerivedExpiry(t *testing.T) {
	const spawnID = int64(0x47b973e0595)
	now := time.Date(2026, 8, 23, 12, 17, 33, 0, time.UTC)
	derived := deriveExpiry(spawnID, now.Unix())

	for _, c := range []struct {
		name     string
		existing int64
		receipt  int64
		want     int64
	}{
		{"receipt just after the derived expiry keeps it", derived, derived + 60, derived},
		{"receipt long after it re-derives", derived, derived + 600, deriveExpiry(spawnID, derived+600)},
		{"a guess is replaced", derived + 7, derived - 60, derived},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := &Pokemon{}
			p.SpawnId = null.ValueFrom(spawnID)
			p.SetExpireTimestamp(null.IntFrom(c.existing))
			p.applyDerivedDespawn(spawnID, c.receipt*1000, false)
			if got := int64(p.ExpireTimestamp.ValueOrZero()); got != c.want {
				t.Errorf("expiry = %d, want %d", got, c.want)
			}
		})
	}

	// Through setExpireTimestampFromSpawnpoint, so the flag is known to reach
	// the derivation from the encounter path's call.
	seedCachedSpawnpoint(t, spawnID, null.Int{})
	p := &Pokemon{}
	p.SpawnId = null.ValueFrom(spawnID)
	p.SetExpireTimestamp(null.IntFrom(derived))
	p.setExpireTimestampFromSpawnpoint(context.Background(), db.DbDetails{}, (derived+60)*1000, false)
	if got := int64(p.ExpireTimestamp.ValueOrZero()); got != derived {
		t.Errorf("an encounter received 60s after the derived expiry moved it to %d", got)
	}
}

// A derived expiry keeps the record cached until a short grace past it — long
// enough for a rescan to find it again — rather than for the hour a guess
// gets. A guess (no spawn id) keeps the long lifetime.
func TestCacheLifetimeFollowsADerivedExpiry(t *testing.T) {
	const now = int64(1787486400)
	derived := &Pokemon{}
	derived.SpawnId = null.ValueFrom(int64(0x47b973e0595))
	derived.SetExpireTimestamp(null.IntFrom(now + 600))
	if got, want := derived.remainingDuration(now), time.Duration(600+derivedExpiryCacheGrace)*time.Second; got != want {
		t.Errorf("derived expiry: cache lifetime %v, want %v", got, want)
	}

	guess := &Pokemon{}
	guess.SetExpireTimestamp(null.IntFrom(now + 600))
	if got := guess.remainingDuration(now); got < pokemonUnverifiedTTL {
		t.Errorf("guess: cache lifetime %v, want at least %v", got, pokemonUnverifiedTTL)
	}
}

// What a webhook consumer must hear about the expiry, and what it need not.
// Expiries marked derived sit on the spawnpoint's phase, as a derivation
// does; a guess sits off it.
func TestExpiryNewsForConsumers(t *testing.T) {
	const spawnID = int64(0x47b973e0595)
	const now = uint32(1787486400)
	phase := uint32(deriveExpiry(spawnID, int64(now))) // the next derived expiry
	past := phase - 1800                               // the one just passed
	guess := now + 1200                                // 20 minutes on, off the phase
	if phase == guess || phase == guess+1800 {
		t.Fatal("fixture assumption violated: the guess landed on the phase")
	}
	for _, c := range []struct {
		name                  string
		oldExp, newExp        uint32
		oldVerified, verified bool
		noSpawnId             bool // a guess with nothing to derive from
		want                  bool
	}{
		{"re-derived after the old expiry passed", past, phase, false, false, false, true},
		{"guess replaced by a derived expiry", guess, phase, false, false, false, true},
		{"guess extended after it passed", now - 60, now + 540, false, false, true, false},
		{"derived moved before it arrived", phase, phase + 1800, false, false, false, false},
		{"became verified", phase, phase, false, true, false, true},
		{"verified time moved", phase, phase + 1800, true, true, false, true},
		{"unchanged", phase, phase, true, true, false, false},
	} {
		p := &Pokemon{}
		if !c.noSpawnId {
			p.SpawnId = null.ValueFrom(spawnID)
		}
		p.oldValues = PokemonOldValues{Expiry: c.oldExp, ExpiryVerified: c.oldVerified}
		p.ExpireTimestamp = null.ValueFrom(c.newExp)
		p.ExpireTimestampVerified = c.verified
		if got := p.expiryNewsForConsumers(int64(now)); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// A tappable on a spawnpoint with no stored despawn_sec derives its expiry,
// unverified.
func TestTappableDerivesAnUnverifiedExpiry(t *testing.T) {
	const spawnID = int64(0x2b7c1a9d0e9)
	seedCachedSpawnpoint(t, spawnID, null.Int{})
	now := time.Date(2026, 8, 23, 12, 17, 33, 0, time.UTC)

	ta := &Tappable{}
	ta.SpawnId = null.IntFrom(spawnID)
	ta.setExpireTimestamp(context.Background(), db.DbDetails{}, now.UnixMilli())

	if want := despawn.NextOccurrence(spawnID, now).Unix(); ta.ExpireTimestamp.Int64 != want {
		t.Errorf("tappable expiry = %d, want the derived %d", ta.ExpireTimestamp.Int64, want)
	}
	if ta.ExpireTimestampVerified {
		t.Error("a derived tappable expiry must not be verified")
	}
}

// A sighting carrying the real despawn time confirms an unverified derived
// expiry, even with nothing else about the Pokemon changed and the expiry
// still in the future. The spawnpoint is seeded with the despawn this
// sighting carries — as spawnpointUpdateFromWild has just stored it — so the
// batch sees it unchanged and never reaches the (nil) database.
func TestUpdatePokemonBatchConfirmsADerivedExpiryFromTTH(t *testing.T) {
	sink := lureTestSetup(t)

	const spawnHex = "2b7c1a9d0eb"
	const spawnID = int64(0x2b7c1a9d0eb)
	const encId = uint64(920205)

	sp := seedCachedSpawnpoint(t, spawnID, null.Int{})
	pokemonCache.Delete(encId)
	t.Cleanup(func() { pokemonCache.Delete(encId) })

	first := time.Date(2026, 8, 23, 12, 17, 33, 0, time.UTC)
	params := ScanParameters{ProcessWild: true}
	UpdatePokemonBatch(context.Background(), db.DbDetails{}, params,
		[]RawWildPokemonData{derivedDespawnBatchWild(encId, spawnHex, first.UnixMilli())},
		nil, nil, nil, "tester")
	p, unlock, _ := peekPokemonRecordReadOnly(encId, "test")
	derived := int64(p.ExpireTimestamp.ValueOrZero())
	unlock()
	sink.drain()

	// A minute before the derived expiry, the game gives time-till-hidden.
	seen := time.Unix(derived-60, 0)
	despawnAt := derived
	sp.DespawnSec = null.IntFrom(int64(despawnSecondOfHour(despawnAt)))
	sp.syncFastFields()
	withTTH := derivedDespawnBatchWild(encId, spawnHex, seen.UnixMilli())
	withTTH.Data.TimeTillHiddenMs = 60000
	UpdatePokemonBatch(context.Background(), db.DbDetails{}, params,
		[]RawWildPokemonData{withTTH}, nil, nil, nil, "tester")

	p, unlock, _ = peekPokemonRecordReadOnly(encId, "test")
	verified, got := p.ExpireTimestampVerified, int64(p.ExpireTimestamp.ValueOrZero())
	unlock()
	if !verified {
		t.Fatal("a sighting carrying the real despawn time left the expiry unverified")
	}
	if got != despawnAt {
		t.Errorf("expiry = %d, want the confirmed %d", got, despawnAt)
	}
	if len(sink.drain()) == 0 {
		t.Error("no webhook when the expiry was confirmed")
	}
}

// A derived expiry never moves backward. Once a later sighting has moved a
// 60-minute Pokemon to its next half hour, a delayed GMO from before that —
// re-admitted here by a weather change, as a Ditto is on every GMO — must not
// put back the expiry that has already passed.
func TestUpdatePokemonBatchADelayedGMODoesNotUndoACorrection(t *testing.T) {
	lureTestSetup(t)

	const spawnHex = "2b7c1a9d0ed"
	const spawnID = int64(0x2b7c1a9d0ed)
	const encId = uint64(920206)

	seedCachedSpawnpoint(t, spawnID, null.Int{})
	pokemonCache.Delete(encId)
	t.Cleanup(func() { pokemonCache.Delete(encId) })

	first := time.Date(2026, 8, 23, 12, 17, 33, 0, time.UTC)
	params := ScanParameters{ProcessWild: true}
	UpdatePokemonBatch(context.Background(), db.DbDetails{}, params,
		[]RawWildPokemonData{derivedDespawnBatchWild(encId, spawnHex, first.UnixMilli())},
		nil, nil, nil, "tester")
	p, unlock, _ := peekPokemonRecordReadOnly(encId, "test")
	derived := int64(p.ExpireTimestamp.ValueOrZero())
	unlock()

	UpdatePokemonBatch(context.Background(), db.DbDetails{}, params,
		[]RawWildPokemonData{derivedDespawnBatchWild(encId, spawnHex, time.Unix(derived+300, 0).UnixMilli())},
		nil, nil, nil, "tester")

	delayed := derivedDespawnBatchWild(encId, spawnHex, first.Add(time.Minute).UnixMilli())
	delayed.Data.Pokemon.PokemonDisplay = &pogo.PokemonDisplayProto{WeatherBoostedCondition: pogo.GameplayWeatherProto_RAINY}
	UpdatePokemonBatch(context.Background(), db.DbDetails{}, params, []RawWildPokemonData{delayed}, nil, nil, nil, "tester")

	p, unlock, _ = peekPokemonRecordReadOnly(encId, "test")
	got, weather := int64(p.ExpireTimestamp.ValueOrZero()), int64OrZero(p.Weather)
	unlock()
	if weather != int64(pogo.GameplayWeatherProto_RAINY) {
		t.Fatal("fixture assumption violated: the delayed GMO was not processed")
	}
	if got != derived+1800 {
		t.Errorf("expiry = %d, want the corrected %d kept: a delayed GMO moved it back", got, derived+1800)
	}
}

// A failed spawnpoint lookup is not an unknown despawn. A record whose
// verified expiry came from the spawnpoint's stored despawn time — restored
// after a restart, say, with the spawnpoint cache still cold — must keep it
// when the database errors, not trade it for an unverified derivation that is
// half an hour early for an hour-long spawn. With nothing usable on the
// record, deriving is still the best answer.
//
// The database is real and unreachable, so the lookup returns an error the
// way a timeout does rather than panicking on a nil handle.
func TestFailedSpawnpointLookupKeepsAFutureExpiry(t *testing.T) {
	raw, err := sql.Open("mysql", "u:p@tcp(127.0.0.1:1)/none?timeout=200ms")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	unreachable := db.DbDetails{GeneralDb: sqlx.NewDb(raw, "mysql")}

	const spawnID = int64(0x2b7c1a9d0ef)
	spawnpointCache.Delete(spawnID)
	now := time.Date(2026, 8, 23, 12, 17, 33, 0, time.UTC)
	nowMs := now.UnixMilli()

	t.Run("a verified future expiry is kept", func(t *testing.T) {
		p := &Pokemon{}
		p.SpawnId = null.ValueFrom(spawnID)
		p.SetExpireTimestamp(null.IntFrom(now.Unix() + 2700))
		p.SetExpireTimestampVerified(true)
		p.setExpireTimestampFromSpawnpoint(context.Background(), unreachable, nowMs, true)
		if got := int64(p.ExpireTimestamp.ValueOrZero()); got != now.Unix()+2700 || !p.ExpireTimestampVerified {
			t.Errorf("expiry = %d verified=%v, want %d verified: a lookup failure replaced it", got, p.ExpireTimestampVerified, now.Unix()+2700)
		}
	})

	t.Run("with nothing usable, it derives", func(t *testing.T) {
		for name, existing := range map[string]null.Int{"no expiry": {}, "a passed one": null.IntFrom(now.Unix() - 60)} {
			p := &Pokemon{}
			p.SpawnId = null.ValueFrom(spawnID)
			if existing.Valid {
				p.SetExpireTimestamp(existing)
			}
			p.setExpireTimestampFromSpawnpoint(context.Background(), unreachable, nowMs, true)
			if want := deriveExpiry(spawnID, now.Unix()); int64(p.ExpireTimestamp.ValueOrZero()) != want {
				t.Errorf("%s: expiry = %v, want the derived %d", name, p.ExpireTimestamp, want)
			}
		}
	})
}

// The same failure reached through GMO processing. spawnpointUpdateFromWild
// runs first; its database read fails and leaves an unloaded placeholder in
// the spawnpoint cache, which the expiry lookup then gets back with no error.
// A verified future expiry must still survive. Re-admitted by a weather
// change, as a Ditto is on every GMO.
func TestUpdatePokemonBatchKeepsAFutureExpiryWhenTheSpawnpointFailsToLoad(t *testing.T) {
	lureTestSetup(t)
	raw, err := sql.Open("mysql", "u:p@tcp(127.0.0.1:1)/none?timeout=200ms")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	unreachable := db.DbDetails{GeneralDb: sqlx.NewDb(raw, "mysql")}

	const spawnHex = "2b7c1a9d0f1"
	const spawnID = int64(0x2b7c1a9d0f1)
	const encId = uint64(920207)
	spawnpointCache.Delete(spawnID)
	t.Cleanup(func() { spawnpointCache.Delete(spawnID) })
	pokemonCache.Delete(encId)
	t.Cleanup(func() { pokemonCache.Delete(encId) })

	now := time.Date(2026, 8, 23, 12, 17, 33, 0, time.UTC)
	verified := now.Unix() + 2700
	p, unlock, err := getOrCreatePokemonRecord(context.Background(), db.DbDetails{}, encId, "t")
	if err != nil {
		t.Fatal(err)
	}
	p.SetPokemonId(int16(pogo.HoloPokemonId_RATTATA))
	p.SetSpawnId(null.IntFrom(spawnID))
	p.SetSeenType(SeenTypeCodeWild)
	p.SetExpireTimestamp(null.IntFrom(verified))
	p.SetExpireTimestampVerified(true)
	p.newRecord = false
	unlock()

	sighting := derivedDespawnBatchWild(encId, spawnHex, now.UnixMilli())
	sighting.Data.Pokemon.PokemonDisplay = &pogo.PokemonDisplayProto{WeatherBoostedCondition: pogo.GameplayWeatherProto_RAINY}
	UpdatePokemonBatch(context.Background(), unreachable, ScanParameters{ProcessWild: true},
		[]RawWildPokemonData{sighting}, nil, nil, nil, "tester")

	p, unlock, _ = peekPokemonRecordReadOnly(encId, "t")
	got, isVerified, weather := int64(p.ExpireTimestamp.ValueOrZero()), p.ExpireTimestampVerified, int64OrZero(p.Weather)
	unlock()
	if weather != int64(pogo.GameplayWeatherProto_RAINY) {
		t.Fatal("fixture assumption violated: the sighting was not processed")
	}
	if got != verified || !isVerified {
		t.Errorf("expiry = %d verified=%v, want the verified %d kept: the failed load's placeholder read as an unknown despawn",
			got, isVerified, verified)
	}
}

// A receipt time a little after the despawn phase, with no expiry yet, is a
// Pokemon that has just spawned: the game served it, so it is alive, and its
// expiry is the next occurrence of the phase, not the one just past. The same
// holds for a tappable, which no later sighting would correct.
func TestReceiptTimeJustAfterThePhaseIsAFreshSpawn(t *testing.T) {
	const spawnID = int64(0x47b973e0595)
	phase := deriveExpiry(spawnID, time.Date(2026, 8, 23, 12, 17, 33, 0, time.UTC).Unix())

	for _, after := range []int64{10, 60, 119} {
		p := &Pokemon{}
		p.SpawnId = null.ValueFrom(spawnID)
		p.applyDerivedDespawn(spawnID, (phase+after)*1000, false)
		if got := int64(p.ExpireTimestamp.ValueOrZero()); got != phase+1800 {
			t.Errorf("pokemon received %ds after the phase: expiry = %d, want the next occurrence %d (the phase %d has passed)",
				after, got, phase+1800, phase)
		}

		ta := &Tappable{}
		ta.applyDerivedDespawn(spawnID, (phase+after)*1000)
		if got := ta.ExpireTimestamp.Int64; got != phase+1800 {
			t.Errorf("tappable received %ds after the phase: expiry = %d, want %d", after, got, phase+1800)
		}
	}
}

// A tappable keeps a future expiry, verified flag and all, when its
// spawnpoint lookup fails — by error, or by the unloaded placeholder a failed
// load leaves in the cache — rather than trading it for a derivation that is
// half an hour early for an hour-long spawn. With nothing usable it derives.
func TestTappableKeepsAFutureExpiryWhenTheSpawnpointLookupFails(t *testing.T) {
	raw, err := sql.Open("mysql", "u:p@tcp(127.0.0.1:1)/none?timeout=200ms")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	unreachable := db.DbDetails{GeneralDb: sqlx.NewDb(raw, "mysql")}

	const spawnID = int64(0x2b7c1a9d0f3)
	now := time.Date(2026, 8, 23, 12, 17, 33, 0, time.UTC)
	kept := now.Unix() + 2701 // off the derived phase, as an observed expiry can be

	for name, placeholder := range map[string]bool{"database error": false, "failed-load placeholder": true} {
		t.Run(name, func(t *testing.T) {
			spawnpointCache.Delete(spawnID)
			t.Cleanup(func() { spawnpointCache.Delete(spawnID) })
			if placeholder {
				// What getOrCreateSpawnpointRecord leaves when its read fails:
				// cached, never loaded, never synced.
				spawnpointCache.Set(spawnID, &Spawnpoint{SpawnpointData: SpawnpointData{Id: spawnID}, newRecord: true}, time.Minute)
			}
			ta := &Tappable{}
			ta.SpawnId = null.IntFrom(spawnID)
			ta.SetExpireTimestamp(null.IntFrom(kept))
			ta.SetExpireTimestampVerified(true)
			ta.setExpireTimestamp(context.Background(), unreachable, now.UnixMilli())
			if ta.ExpireTimestamp.Int64 != kept || !ta.ExpireTimestampVerified {
				t.Errorf("expiry = %d verified=%v, want %d verified kept", ta.ExpireTimestamp.Int64, ta.ExpireTimestampVerified, kept)
			}
		})
	}

	t.Run("nothing usable derives", func(t *testing.T) {
		spawnpointCache.Delete(spawnID)
		ta := &Tappable{}
		ta.SpawnId = null.IntFrom(spawnID)
		ta.setExpireTimestamp(context.Background(), unreachable, now.UnixMilli())
		if want := deriveExpiry(spawnID, now.Unix()); ta.ExpireTimestamp.Int64 != want || ta.ExpireTimestampVerified {
			t.Errorf("expiry = %d verified=%v, want the derived %d unverified", ta.ExpireTimestamp.Int64, ta.ExpireTimestampVerified, want)
		}
	})
}

// A pokemon seen nearby first carries the 20-minute guess. Its first wild
// sighting, at a spawnpoint whose despawn has not been observed, replaces
// that with the derived expiry, and nothing else a consumer watches changes —
// the nearby sighting already derived the same IVs. The webhook must still go
// out, or consumers keep a time up to 20 minutes out either way.
func TestUpdatePokemonBatchSendsTheDerivedExpiryThatReplacesANearbyGuess(t *testing.T) {
	sink := lureTestSetup(t)

	const spawnHex = "2b7c1a9d0f7"
	const spawnID = int64(0x2b7c1a9d0f7)
	const encId = uint64(920231)
	freshPokemonRecord(t, encId)
	seedCachedSpawnpoint(t, spawnID, null.Int{})

	fortHex := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	fortId := mustFortId(t, fortHex)
	pokestopCache.Set(fortId, &Pokestop{PokestopData: PokestopData{Id: fortId, Lat: 51.5007, Lon: -0.1246}}, time.Minute)
	t.Cleanup(func() { pokestopCache.Delete(fortId) })

	seen := time.Date(2026, 8, 23, 12, 17, 33, 0, time.UTC)
	cell := int64(s2.CellIDFromLatLng(s2.LatLngFromDegrees(51.5007, -0.1246)).Parent(gmoCellLevel))
	UpdatePokemonBatch(context.Background(), db.DbDetails{}, ScanParameters{ProcessNearby: true},
		nil, []RawNearbyPokemonData{{
			Cell:      uint64(cell),
			Timestamp: seen.UnixMilli(),
			Data: &pogo.NearbyPokemonProto{
				EncounterId:    encId,
				FortId:         fortHex,
				PokedexNumber:  int32(pogo.HoloPokemonId_RATTATA),
				PokemonDisplay: &pogo.PokemonDisplayProto{},
			},
		}}, nil, nil, "tester")

	p, unlock, _ := peekPokemonRecordReadOnly(encId, "test")
	if p == nil {
		t.Fatal("nearby pokemon not in cache")
	}
	guess, hasIv := int64(p.ExpireTimestamp.ValueOrZero()), p.AtkIv.Valid
	unlock()
	derived := deriveExpiry(spawnID, seen.Unix()+60)
	if !hasIv || guess == derived {
		t.Fatalf("fixture assumption violated: nearby sighting must derive IVs and carry a guess off the derived expiry (guess %d, derived %d)", guess, derived)
	}
	sink.drain()

	UpdatePokemonBatch(context.Background(), db.DbDetails{}, ScanParameters{ProcessWild: true},
		[]RawWildPokemonData{derivedDespawnBatchWild(encId, spawnHex, seen.UnixMilli()+60_000)},
		nil, nil, nil, "tester")

	p, unlock, _ = peekPokemonRecordReadOnly(encId, "test")
	got := int64(p.ExpireTimestamp.ValueOrZero())
	unlock()
	if got != derived {
		t.Fatalf("expiry after the wild sighting = %d, want the derived %d", got, derived)
	}
	if len(sink.drain()) == 0 {
		t.Error("no webhook when the derived expiry replaced the nearby guess: consumers keep the guess")
	}
}
