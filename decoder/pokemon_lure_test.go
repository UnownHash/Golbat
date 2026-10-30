package decoder

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/guregu/null/v6"

	"golbat/config"
	"golbat/db"
	"golbat/derive/encounter"
	"golbat/geo"
	"golbat/pogo"
	"golbat/webhooks"
)

type recordedWebhook struct {
	whType  webhooks.WebhookType
	message any
}

type testWebhookSink struct {
	mu   sync.Mutex
	msgs []recordedWebhook
}

func (s *testWebhookSink) AddMessage(whType webhooks.WebhookType, message any, areas []geo.AreaName) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, recordedWebhook{whType, message})
}

func (s *testWebhookSink) drain() []recordedWebhook {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.msgs
	s.msgs = nil
	return out
}

// lureTestSetup puts the decoder package into memory-only mode with a
// recording webhook sink. The package caches are shared across the test
// binary, so every test must use unique encounter/fort IDs.
func lureTestSetup(t *testing.T) *testWebhookSink {
	t.Helper()
	prevMemOnly := config.Config.PokemonMemoryOnly
	config.Config.PokemonMemoryOnly = true
	sink := &testWebhookSink{}
	SetWebhooksSender(sink)
	t.Cleanup(func() {
		config.Config.PokemonMemoryOnly = prevMemOnly
		SetWebhooksSender(nil)
	})
	return sink
}

// freshPokemonRecord removes any cached record for id now and after the test,
// so a record left by an earlier test or repeat — possibly still locked, if
// that run failed before unlocking — cannot leak into this one.
func freshPokemonRecord(t *testing.T, id uint64) {
	t.Helper()
	pokemonCache.Delete(id)
	t.Cleanup(func() { pokemonCache.Delete(id) })
}

// testFortIdStr returns a deterministic, valid FortId string derived from n
// (the canonical 32-hex-digit form), so lure tests that thread a fort id
// string through the raw proto ingest boundary can name distinct forts
// cheaply without tripping ParseFortId's structural validation.
func testFortIdStr(n uint64) string {
	return fmt.Sprintf("%032x", n)
}

func testRawMapPokemon(encId uint64, fortId string, lat, lon float64, expireMs int64) RawMapPokemonData {
	return RawMapPokemonData{
		Cell:      5000000000000000000,
		Timestamp: time.Now().UnixMilli(),
		FortId:    fortId,
		Lat:       lat,
		Lon:       lon,
		Data: &pogo.MapPokemonProto{
			SpawnpointId:     fortId,
			EncounterId:      encId,
			ExpirationTimeMs: expireMs,
			PokedexTypeId:    25,
			PokemonDisplay:   &pogo.PokemonDisplayProto{},
		},
	}
}

// Placement must come from the captured fort fields, with no pokestop record
// anywhere.
func TestUpdateFromMapPlacesNewRecordFromCapturedFort(t *testing.T) {
	lureTestSetup(t)
	const encId = uint64(910101)
	freshPokemonRecord(t, encId)
	expireMs := time.Now().UnixMilli() + 120_000
	raw := testRawMapPokemon(encId, testFortIdStr(910101), 51.5007, -0.1246, expireMs)

	pokemon, unlock, err := getOrCreatePokemonRecord(context.Background(), db.DbDetails{}, encId, "test")
	if err != nil {
		t.Fatalf("getOrCreatePokemonRecord: %v", err)
	}
	saveNeeded := pokemon.updateFromMap(context.Background(), db.DbDetails{}, raw, nil, "tester")

	if !saveNeeded {
		t.Errorf("updateFromMap on new record = false, want true")
	}
	if got := pokemon.PokestopId.String(); got != testFortIdStr(910101) {
		t.Errorf("PokestopId = %q, want %q", got, testFortIdStr(910101))
	}
	if pokemon.Lat != 51.5007 || pokemon.Lon != -0.1246 {
		t.Errorf("Lat/Lon = %v/%v, want 51.5007/-0.1246", pokemon.Lat, pokemon.Lon)
	}
	if got := pokemon.SeenType.ValueOrZero(); got != SeenTypeCodeLureWild.String() {
		t.Errorf("SeenType = %q, want %q", got, SeenTypeCodeLureWild.String())
	}
	if !pokemon.ExpireTimestampVerified {
		t.Errorf("ExpireTimestampVerified = false, want true (GMO supplied ExpirationTimeMs)")
	}
	if got := int64(pokemon.ExpireTimestamp.ValueOrZero()); got != expireMs/1000 {
		t.Errorf("ExpireTimestamp = %d, want %d", got, expireMs/1000)
	}
	unlock()
}

// The merge path: an existing lure record without verified expiry gains it
// from a later GMO; a repeat identical GMO changes nothing.
func TestUpdateFromMapMergeAddsVerifiedExpiryOnce(t *testing.T) {
	lureTestSetup(t)
	const encId = uint64(910102)
	freshPokemonRecord(t, encId)

	// First sighting carries no expiry -> unverified record.
	noExpiry := testRawMapPokemon(encId, testFortIdStr(910102), 51.5, -0.12, 0)
	pokemon, unlock, err := getOrCreatePokemonRecord(context.Background(), db.DbDetails{}, encId, "test")
	if err != nil {
		t.Fatalf("getOrCreatePokemonRecord: %v", err)
	}
	if !pokemon.updateFromMap(context.Background(), db.DbDetails{}, noExpiry, nil, "tester") {
		t.Fatalf("first updateFromMap = false, want true")
	}
	if pokemon.ExpireTimestampVerified {
		t.Fatalf("ExpireTimestampVerified = true after no-expiry GMO, want false")
	}

	// Simulate the commit the batch loop performs after a true return —
	// in production savePokemonRecordAsAtTime clears newRecord.
	pokemon.newRecord = false

	// Second sighting supplies the despawn time.
	expireMs := time.Now().UnixMilli() + 90_000
	withExpiry := testRawMapPokemon(encId, testFortIdStr(910102), 51.5, -0.12, expireMs)
	if !pokemon.updateFromMap(context.Background(), db.DbDetails{}, withExpiry, nil, "tester") {
		t.Errorf("merge updateFromMap = false, want true (expiry contributed)")
	}
	if !pokemon.ExpireTimestampVerified || int64(pokemon.ExpireTimestamp.ValueOrZero()) != expireMs/1000 {
		t.Errorf("expiry = %d verified=%v, want %d verified=true",
			int64(pokemon.ExpireTimestamp.ValueOrZero()), pokemon.ExpireTimestampVerified, expireMs/1000)
	}

	// Identical replay: nothing left to contribute.
	if pokemon.updateFromMap(context.Background(), db.DbDetails{}, withExpiry, nil, "tester") {
		t.Errorf("no-change replay updateFromMap = true, want false")
	}
	unlock()
}

// Non-lure records must be left untouched (old code was a no-op for every
// non-new record; the merge must not widen that).
func TestUpdateFromMapLeavesNonLureRecordsAlone(t *testing.T) {
	lureTestSetup(t)
	const encId = uint64(910103)
	freshPokemonRecord(t, encId)
	pokemon, unlock, err := getOrCreatePokemonRecord(context.Background(), db.DbDetails{}, encId, "test")
	if err != nil {
		t.Fatalf("getOrCreatePokemonRecord: %v", err)
	}
	pokemon.SetSeenType(SeenTypeCodeWild)
	pokemon.newRecord = false

	raw := testRawMapPokemon(encId, testFortIdStr(910103), 51.5, -0.12, time.Now().UnixMilli()+90_000)
	if pokemon.updateFromMap(context.Background(), db.DbDetails{}, raw, nil, "tester") {
		t.Errorf("updateFromMap on wild record = true, want false")
	}
	if pokemon.PokestopId.Valid() {
		t.Errorf("PokestopId set on wild record, want untouched")
	}
	unlock()
}

// Full pipeline with an unknown fort: the record is placed and a webhook
// fires with real coordinates.
func TestUpdatePokemonBatchPlacesLureWithUnknownFort(t *testing.T) {
	sink := lureTestSetup(t)
	const encId = uint64(910104)
	freshPokemonRecord(t, encId)
	expireMs := time.Now().UnixMilli() + 120_000
	raw := testRawMapPokemon(encId, testFortIdStr(910104), 48.8584, 2.2945, expireMs)

	UpdatePokemonBatch(context.Background(), db.DbDetails{}, ScanParameters{}, nil, nil,
		[]RawMapPokemonData{raw}, nil, "tester")

	pokemon, unlock, _ := peekPokemonRecordReadOnly(encId, "test")
	if pokemon == nil {
		t.Fatalf("pokemon %d not in cache after batch", encId)
	}
	if pokemon.Lat != 48.8584 || pokemon.Lon != 2.2945 {
		t.Errorf("Lat/Lon = %v/%v, want 48.8584/2.2945", pokemon.Lat, pokemon.Lon)
	}
	if pokemon.isNewRecord() {
		t.Errorf("record still new after batch save, want committed")
	}
	unlock()

	hooks := sink.drain()
	if len(hooks) == 0 {
		t.Fatalf("no webhook emitted for placed lure pokemon")
	}
	hook, ok := hooks[0].message.(PokemonWebhook)
	if !ok {
		t.Fatalf("webhook message type %T, want PokemonWebhook", hooks[0].message)
	}
	if hook.Latitude != 48.8584 || hook.Longitude != 2.2945 {
		t.Errorf("webhook coords = %v/%v, want 48.8584/2.2945", hook.Latitude, hook.Longitude)
	}
	if hook.DisappearTime != expireMs/1000 {
		t.Errorf("webhook DisappearTime = %d, want %d", hook.DisappearTime, expireMs/1000)
	}
	// The fort is real (parses) but has no cached/DB pokestop record, so the
	// webhook must carry the canonical id string with a placeholder name —
	// not the "None"/nil absent-fort shape TestCreatePokemonWebhooksPokestopIdSentinelForAbsentFort covers.
	if want := testFortIdStr(910104); hook.PokestopId != want {
		t.Errorf("webhook PokestopId = %q, want %q", hook.PokestopId, want)
	}
	if hook.PokestopName == nil || *hook.PokestopName != "Unknown" {
		t.Errorf("webhook PokestopName = %v, want \"Unknown\"", hook.PokestopName)
	}
}

func testDiskEncounterProtos(encId uint64, fortId string, lat, lon float64) (*pogo.DiskEncounterProto, *pogo.DiskEncounterOutProto) {
	request := &pogo.DiskEncounterProto{
		EncounterId:   int64(encId),
		FortId:        fortId,
		GymLatDegrees: lat,
		GymLngDegrees: lon,
	}
	response := &pogo.DiskEncounterOutProto{
		Result: pogo.DiskEncounterOutProto_SUCCESS,
		Pokemon: &pogo.PokemonProto{
			PokemonId:         pogo.HoloPokemonId(25),
			Cp:                500,
			CpMultiplier:      0.5,
			IndividualAttack:  15,
			IndividualDefense: 14,
			IndividualStamina: 13,
			PokemonDisplay:    &pogo.PokemonDisplayProto{DisplayId: int64(encId)},
		},
	}
	return request, response
}

// #283: an encounter with no prior GMO creates a fully placed record from
// the request proto — real coords, estimated expiry, IVs, webhook.
func TestDiskEncounterFirstCreatesPlacedRecord(t *testing.T) {
	sink := lureTestSetup(t)
	const encId = uint64(910105)
	freshPokemonRecord(t, encId)
	request, response := testDiskEncounterProtos(encId, testFortIdStr(910105), 40.7580, -73.9855)

	before := time.Now().Unix()
	UpdatePokemonRecordWithDiskEncounterProto(context.Background(), db.DbDetails{}, request, response, "tester")
	after := time.Now().Unix()

	pokemon, unlock, _ := peekPokemonRecordReadOnly(encId, "test")
	if pokemon == nil {
		t.Fatalf("pokemon %d not in cache after disk encounter", encId)
	}
	if got := pokemon.PokestopId.String(); got != testFortIdStr(910105) {
		t.Errorf("PokestopId = %q, want %q", got, testFortIdStr(910105))
	}
	if pokemon.Lat != 40.7580 || pokemon.Lon != -73.9855 {
		t.Errorf("Lat/Lon = %v/%v, want request coords 40.7580/-73.9855", pokemon.Lat, pokemon.Lon)
	}
	if got := pokemon.SeenType.ValueOrZero(); got != SeenTypeCodeLureEncounter.String() {
		t.Errorf("SeenType = %q, want %q", got, SeenTypeCodeLureEncounter.String())
	}
	if pokemon.ExpireTimestampVerified {
		t.Errorf("ExpireTimestampVerified = true, want false (estimate)")
	}
	exp := int64(pokemon.ExpireTimestamp.ValueOrZero())
	if exp < before+lureSpawnLifetimeSeconds || exp > after+lureSpawnLifetimeSeconds {
		t.Errorf("ExpireTimestamp = %d, want now+%ds (in [%d, %d])",
			exp, lureSpawnLifetimeSeconds, before+lureSpawnLifetimeSeconds, after+lureSpawnLifetimeSeconds)
	}
	if got := pokemon.AtkIv.ValueOrZero(); got != 15 {
		t.Errorf("AtkIv = %d, want 15", got)
	}
	unlock()

	hooks := sink.drain()
	if len(hooks) == 0 {
		t.Fatalf("no webhook emitted for encounter-created lure pokemon")
	}
	hook, ok := hooks[0].message.(PokemonWebhook)
	if !ok {
		t.Fatalf("webhook message type %T, want PokemonWebhook", hooks[0].message)
	}
	if hook.Latitude != 40.7580 || hook.Longitude != -73.9855 {
		t.Errorf("webhook coords = %v/%v, want 40.7580/-73.9855 (0/0 means the record was never placed)", hook.Latitude, hook.Longitude)
	}
	if hook.DisappearTime <= 0 {
		t.Errorf("webhook DisappearTime = %d, want > 0", hook.DisappearTime)
	}
}

// A GMO arriving after the encounter tightens the estimate to a verified
// despawn without downgrading the seen type or touching IVs.
func TestGmoAfterDiskEncounterContributesVerifiedExpiry(t *testing.T) {
	lureTestSetup(t)
	const encId = uint64(910106)
	freshPokemonRecord(t, encId)
	request, response := testDiskEncounterProtos(encId, testFortIdStr(910106), 40.0, -73.0)
	UpdatePokemonRecordWithDiskEncounterProto(context.Background(), db.DbDetails{}, request, response, "tester")

	expireMs := time.Now().UnixMilli() + 100_000
	raw := testRawMapPokemon(encId, testFortIdStr(910106), 40.0, -73.0, expireMs)
	UpdatePokemonBatch(context.Background(), db.DbDetails{}, ScanParameters{}, nil, nil,
		[]RawMapPokemonData{raw}, nil, "tester")

	pokemon, unlock, _ := peekPokemonRecordReadOnly(encId, "test")
	if pokemon == nil {
		t.Fatalf("pokemon %d missing", encId)
	}
	if !pokemon.ExpireTimestampVerified || int64(pokemon.ExpireTimestamp.ValueOrZero()) != expireMs/1000 {
		t.Errorf("expiry = %d verified=%v, want %d verified=true",
			int64(pokemon.ExpireTimestamp.ValueOrZero()), pokemon.ExpireTimestampVerified, expireMs/1000)
	}
	if got := pokemon.SeenType.ValueOrZero(); got != SeenTypeCodeLureEncounter.String() {
		t.Errorf("SeenType = %q, want %q (must not downgrade)", got, SeenTypeCodeLureEncounter.String())
	}
	if got := pokemon.AtkIv.ValueOrZero(); got != 15 {
		t.Errorf("AtkIv = %d, want 15 (encounter data must survive the GMO merge)", got)
	}
	unlock()
}

// The classic order still works with no cache hop: GMO creates lure_wild,
// the encounter upgrades it in place.
//
// This test does not use testDiskEncounterProtos' shared IVs (15/14/13):
// encounter id 910107 derives to atk 15 / def 5 / sta 14 unboosted, so the
// GMO pass alone (deriveIvIfAbsent) already leaves AtkIv at 15 before the
// encounter runs, and an atk-only assertion would pass with the encounter
// path dead. Real IVs that differ from the derived ones on all three stats,
// all three checked, mean the test passes only if the encounter ran.
func TestDiskEncounterAfterGmoUpgradesRecord(t *testing.T) {
	lureTestSetup(t)
	const encId = uint64(910107)
	freshPokemonRecord(t, encId)
	expireMs := time.Now().UnixMilli() + 110_000
	raw := testRawMapPokemon(encId, testFortIdStr(910107), 35.6595, 139.7005, expireMs)
	UpdatePokemonBatch(context.Background(), db.DbDetails{}, ScanParameters{}, nil, nil,
		[]RawMapPokemonData{raw}, nil, "tester")

	request := &pogo.DiskEncounterProto{
		EncounterId:   int64(encId),
		FortId:        testFortIdStr(910107),
		GymLatDegrees: 35.6595,
		GymLngDegrees: 139.7005,
	}
	response := &pogo.DiskEncounterOutProto{
		Result: pogo.DiskEncounterOutProto_SUCCESS,
		Pokemon: &pogo.PokemonProto{
			PokemonId:         pogo.HoloPokemonId(25),
			Cp:                500,
			CpMultiplier:      0.5,
			IndividualAttack:  2,
			IndividualDefense: 9,
			IndividualStamina: 4,
			PokemonDisplay:    &pogo.PokemonDisplayProto{DisplayId: int64(encId)},
		},
	}
	UpdatePokemonRecordWithDiskEncounterProto(context.Background(), db.DbDetails{}, request, response, "tester")

	pokemon, unlock, _ := peekPokemonRecordReadOnly(encId, "test")
	if pokemon == nil {
		t.Fatalf("pokemon %d missing", encId)
	}
	if got := pokemon.SeenType.ValueOrZero(); got != SeenTypeCodeLureEncounter.String() {
		t.Errorf("SeenType = %q, want %q", got, SeenTypeCodeLureEncounter.String())
	}
	if got := pokemon.AtkIv.ValueOrZero(); got != 2 {
		t.Errorf("AtkIv = %d, want 2 (the real encounter value; derivation on the GMO pass alone would have left 15)", got)
	}
	if got := pokemon.DefIv.ValueOrZero(); got != 9 {
		t.Errorf("DefIv = %d, want 9 (the real encounter value; derivation on the GMO pass alone would have left 5)", got)
	}
	if got := pokemon.StaIv.ValueOrZero(); got != 4 {
		t.Errorf("StaIv = %d, want 4 (the real encounter value; derivation on the GMO pass alone would have left 14)", got)
	}
	// GMO-verified expiry must survive: the estimate is only for new records.
	if !pokemon.ExpireTimestampVerified || int64(pokemon.ExpireTimestamp.ValueOrZero()) != expireMs/1000 {
		t.Errorf("expiry = %d verified=%v, want %d verified=true (estimate must not overwrite)",
			int64(pokemon.ExpireTimestamp.ValueOrZero()), pokemon.ExpireTimestampVerified, expireMs/1000)
	}
	unlock()
}

// The issue's visibility symptom: the v2/v3 result collector must include a
// record whose only expiry is the unverified estimate.
func TestCollectApiPokemonResultsIncludesEstimatedExpiry(t *testing.T) {
	lureTestSetup(t)
	const encId = uint64(910108)
	freshPokemonRecord(t, encId)
	request, response := testDiskEncounterProtos(encId, testFortIdStr(910108), 40.1, -73.1)
	UpdatePokemonRecordWithDiskEncounterProto(context.Background(), db.DbDetails{}, request, response, "tester")

	results := collectApiPokemonResults([]uint64{encId}, "test", 0)
	if len(results) != 1 {
		t.Fatalf("collectApiPokemonResults returned %d results, want 1", len(results))
	}
}

// createPokemonWebhooks must keep emitting the literal "None" sentinel (not
// "", not the FortId zero value's own empty String()) for a pokemon with no
// associated fort, and leave PokestopName nil rather than "Unknown" — that
// wire format predates the FortId conversion and is a hard constraint on it.
func TestCreatePokemonWebhooksPokestopIdSentinelForAbsentFort(t *testing.T) {
	sink := lureTestSetup(t)
	const encId = uint64(910109)
	freshPokemonRecord(t, encId)

	pokemon, unlock, err := getOrCreatePokemonRecord(context.Background(), db.DbDetails{}, encId, "test")
	if err != nil {
		t.Fatalf("getOrCreatePokemonRecord: %v", err)
	}
	pokemon.SetPokemonId(25)
	pokemon.SetLat(10)
	pokemon.SetLon(20)
	pokemon.SetSeenType(SeenTypeCodeWild)

	// Matches production usage in savePokemonRecordAsAtTime: the entity
	// lock is held across the call, since createPokemonWebhooks reads
	// pokemon fields without locking internally.
	createPokemonWebhooks(context.Background(), db.DbDetails{}, pokemon, nil, "", time.Now().Unix())
	unlock()

	hooks := sink.drain()
	if len(hooks) == 0 {
		t.Fatalf("no webhook emitted for new pokemon record")
	}
	hook, ok := hooks[0].message.(PokemonWebhook)
	if !ok {
		t.Fatalf("webhook message type %T, want PokemonWebhook", hooks[0].message)
	}
	if hook.PokestopId != "None" {
		t.Errorf("PokestopId = %q, want %q (no fort associated)", hook.PokestopId, "None")
	}
	if hook.PokestopName != nil {
		t.Errorf("PokestopName = %q, want nil", *hook.PokestopName)
	}
}

// A lure pokemon that has never been encountered carries DERIVED stats, and
// those belong to one boost branch. Apart from the new-record branch,
// updateFromMap reads the display only in its lure_wild refresh, so without
// that refresh a weather flip leaves stats that are now wrong — and a derived value is
// indistinguishable from a scanned one, so nothing downstream can tell.
//
// ProactiveIVSwitch covers the same flip when proactive_iv_switching is on
// (the default); this is the path that keeps the record correct when it is off.
func TestUpdateFromMapRefreshesDerivedLureStatsOnBoostChange(t *testing.T) {
	lureTestSetup(t)
	withDeriveIv(t, true)

	// A unique id: getOrCreatePokemonRecord shares one cache across the whole
	// package, so reusing another test's id hands this one that test's record.
	const encId = uint64(910110)
	freshPokemonRecord(t, encId)
	expireMs := time.Now().UnixMilli() + 120_000
	fortId := testFortIdStr(910110)

	pokemon, unlock, err := getOrCreatePokemonRecord(context.Background(), db.DbDetails{}, encId, "test")
	if err != nil {
		t.Fatalf("getOrCreatePokemonRecord: %v", err)
	}
	defer unlock()

	// First sighting: species 25 is Electric, so an unboosted display.
	unboosted := testRawMapPokemon(encId, fortId, 35.6595, 139.7005, expireMs)
	pokemon.updateFromMap(context.Background(), db.DbDetails{}, unboosted, nil, "tester")

	if !pokemon.AtkIv.Valid {
		t.Fatal("no IVs derived on the new-record path; this test cannot say anything about refreshing them")
	}
	gotUnboosted := [4]uint8{
		pokemon.AtkIv.ValueOrZero(), pokemon.DefIv.ValueOrZero(),
		pokemon.StaIv.ValueOrZero(), pokemon.Level.ValueOrZero(),
	}

	// Simulate the commit the batch loop performs after a true return —
	// in production savePokemonRecordAsAtTime clears newRecord.
	pokemon.newRecord = false

	// Second sighting of the SAME record, now reporting a weather boost.
	boosted := testRawMapPokemon(encId, fortId, 35.6595, 139.7005, expireMs)
	boosted.Data.PokemonDisplay = &pogo.PokemonDisplayProto{
		WeatherBoostedCondition: pogo.GameplayWeatherProto_RAINY,
	}
	pokemon.updateFromMap(context.Background(), db.DbDetails{}, boosted, nil, "tester")

	gotBoosted := [4]uint8{
		pokemon.AtkIv.ValueOrZero(), pokemon.DefIv.ValueOrZero(),
		pokemon.StaIv.ValueOrZero(), pokemon.Level.ValueOrZero(),
	}
	if gotBoosted == gotUnboosted {
		t.Fatalf("stats unchanged across a boost flip (%v): the derived values still describe the "+
			"unboosted branch and are now wrong", gotUnboosted)
	}

	// And they must be the right branch, not merely different.
	want := encounter.For(encId, true, 0).Values
	if gotBoosted[0] != uint8(want.Attack) || gotBoosted[1] != uint8(want.Defense) ||
		gotBoosted[2] != uint8(want.Stamina) || gotBoosted[3] != uint8(want.Level) {
		t.Errorf("after the flip got atk/def/sta/level %v, want %d/%d/%d/%d (the boosted derivation)",
			gotBoosted, want.Attack, want.Defense, want.Stamina, want.Level)
	}

	// A repeat of the same sighting must stay a genuine no-op.
	if pokemon.updateFromMap(context.Background(), db.DbDetails{}, boosted, nil, "tester") {
		t.Error("a repeated identical sighting reported a change; the refresh must be idempotent")
	}
}

// With derive_iv off a lure_wild record carries no stats to keep fresh, so
// its display is read once, at creation, and a later lure GMO's weather does
// not reach it.
func TestUpdateFromMapReadsTheDisplayOnlyAtCreationWithDerivationOff(t *testing.T) {
	lureTestSetup(t)
	withDeriveIv(t, false)

	const encId = uint64(910111)
	freshPokemonRecord(t, encId)
	expireMs := time.Now().UnixMilli() + 120_000
	fortId := testFortIdStr(910111)

	pokemon, unlock, err := getOrCreatePokemonRecord(context.Background(), db.DbDetails{}, encId, "test")
	if err != nil {
		t.Fatalf("getOrCreatePokemonRecord: %v", err)
	}
	defer unlock()

	pokemon.updateFromMap(context.Background(), db.DbDetails{}, testRawMapPokemon(encId, fortId, 35.6595, 139.7005, expireMs), nil, "tester")
	pokemon.newRecord = false

	boosted := testRawMapPokemon(encId, fortId, 35.6595, 139.7005, expireMs)
	boosted.Data.PokemonDisplay = &pogo.PokemonDisplayProto{
		WeatherBoostedCondition: pogo.GameplayWeatherProto_RAINY,
	}
	pokemon.updateFromMap(context.Background(), db.DbDetails{}, boosted, nil, "tester")

	if got := int64OrZero(pokemon.Weather); got != int64(pogo.GameplayWeatherProto_NONE) {
		t.Errorf("weather = %d after a later lure GMO; with derivation off the display is read only at creation", got)
	}
}

// seen_type tells a consumer how a record was obtained, and so how much of it
// to trust: a cell sighting carries no stats, a wild one carries derived ones,
// an encounter carries observed ones. A record climbing that ladder changes
// meaning without necessarily changing species, weather or CP — and with
// derivation on it often does exactly that, because the IVs were filled in on
// the first sighting, so an upgrade to encounter confirms them without moving
// any other value the webhook condition looks at.
func TestWebhookResendsWhenOnlySeenTypeChanges(t *testing.T) {
	sink := lureTestSetup(t)

	freshPokemonRecord(t, 980001)
	p, unlock, err := getOrCreatePokemonRecord(context.Background(), db.DbDetails{}, 980001, "t")
	if err != nil {
		t.Fatalf("getOrCreatePokemonRecord: %v", err)
	}
	defer unlock()
	p.SetPokemonId(220)
	p.SetSeenType(SeenTypeCodeNearbyStop)
	savePokemonRecordAsAtTime(context.Background(), db.DbDetails{}, p, false, false, true, time.Now().Unix(), "t")
	if len(sink.drain()) == 0 {
		t.Fatal("fixture assumption violated: a new record must produce a webhook")
	}

	// nearby to wild changes nothing about whether the stats were observed.
	p.snapshotOldValues()
	p.SetSeenType(SeenTypeCodeWild)
	savePokemonRecordAsAtTime(context.Background(), db.DbDetails{}, p, false, false, true, time.Now().Unix(), "t")
	if got := len(sink.drain()); got != 0 {
		t.Errorf("nearby to wild produced %d webhook(s); only a move into or out of an encounter is news", got)
	}

	// Control: save again with nothing changed. The entry gate alone should
	// stop this, and if it does not the assertion below proves nothing.
	p.snapshotOldValues()
	savePokemonRecordAsAtTime(context.Background(), db.DbDetails{}, p, false, false, true, time.Now().Unix(), "t")
	if got := len(sink.drain()); got != 0 {
		t.Fatalf("an unchanged save produced %d webhook(s); this test cannot then show that "+
			"seen_type specifically is what triggers one", got)
	}

	// Only seen_type moves: same species, same weather, same CP.
	p.snapshotOldValues()
	p.SetSeenType(SeenTypeCodeEncounter)
	savePokemonRecordAsAtTime(context.Background(), db.DbDetails{}, p, false, false, true, time.Now().Unix(), "t")

	msgs := sink.drain()
	if len(msgs) == 0 {
		t.Error("no webhook after seen_type changed from wild to encounter — the consumer keeps " +
			"showing the weaker provenance for the rest of the pokemon's life")
	}
}

// A verified expiry that moves is a correction consumers must hear about. A
// consumer hides the pokemon when its time arrives, so a correction that
// stays inside golbat hides a catchable pokemon while the API shows the right
// time. See expiryNewsForConsumers for the full rule.
func TestWebhookResendsWhenAVerifiedExpiryMoves(t *testing.T) {
	sink := lureTestSetup(t)

	freshPokemonRecord(t, 980002)
	p, unlock, err := getOrCreatePokemonRecord(context.Background(), db.DbDetails{}, 980002, "t")
	if err != nil {
		t.Fatalf("getOrCreatePokemonRecord: %v", err)
	}
	defer unlock()
	p.SetPokemonId(220)
	p.SetSeenType(SeenTypeCodeWild)
	base := time.Now().Unix() + 600
	p.SetExpireTimestamp(null.IntFrom(base))
	p.SetExpireTimestampVerified(true)
	savePokemonRecordAsAtTime(context.Background(), db.DbDetails{}, p, false, false, true, time.Now().Unix(), "t")
	if len(sink.drain()) == 0 {
		t.Fatal("fixture assumption violated: a new record must produce a webhook")
	}

	// Control: nothing changed, nothing sent.
	p.snapshotOldValues()
	savePokemonRecordAsAtTime(context.Background(), db.DbDetails{}, p, false, false, true, time.Now().Unix(), "t")
	if got := len(sink.drain()); got != 0 {
		t.Fatalf("an unchanged save produced %d webhook(s); this test cannot then show that "+
			"the expiry specifically is what triggers one", got)
	}

	// The same verified expiry, half an hour later. Species, weather, CP and
	// seen_type all unchanged.
	p.snapshotOldValues()
	p.SetExpireTimestamp(null.IntFrom(base + 1800))
	savePokemonRecordAsAtTime(context.Background(), db.DbDetails{}, p, false, false, true, time.Now().Unix(), "t")
	if len(sink.drain()) == 0 {
		t.Error("no webhook after a verified expiry moved by 30 minutes — the consumer keeps the " +
			"early time and hides a catchable pokemon until it really despawns")
	}

	// An unverified expiry is a guess and consumers treat it as one; moving
	// it is not news.
	p.snapshotOldValues()
	p.SetExpireTimestampVerified(false)
	savePokemonRecordAsAtTime(context.Background(), db.DbDetails{}, p, false, false, true, time.Now().Unix(), "t")
	sink.drain() // the flag flipping is itself a change; not what this step tests
	p.snapshotOldValues()
	p.SetExpireTimestamp(null.IntFrom(base + 3600))
	savePokemonRecordAsAtTime(context.Background(), db.DbDetails{}, p, false, false, true, time.Now().Unix(), "t")
	if got := len(sink.drain()); got != 0 {
		t.Errorf("an UNVERIFIED expiry moving produced %d webhook(s); a guess changing is not a correction", got)
	}

	// Becoming verified is: a guess replaced by a time consumers can act on.
	p.snapshotOldValues()
	p.SetExpireTimestampVerified(true)
	savePokemonRecordAsAtTime(context.Background(), db.DbDetails{}, p, false, false, true, time.Now().Unix(), "t")
	if len(sink.drain()) == 0 {
		t.Error("no webhook when an expiry became verified — the consumer keeps treating it as a guess")
	}
}
