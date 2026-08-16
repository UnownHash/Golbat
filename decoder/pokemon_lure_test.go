package decoder

import (
	"context"
	"sync"
	"testing"
	"time"

	"golbat/config"
	"golbat/db"
	"golbat/geo"
	"golbat/pogo"
	"golbat/webhooks"

	"github.com/guregu/null/v6"
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

// #390 regression: placement must come from the captured fort fields, with
// no pokestop record anywhere.
func TestUpdateFromMapPlacesNewRecordFromCapturedFort(t *testing.T) {
	lureTestSetup(t)
	const encId = uint64(910101)
	expireMs := time.Now().UnixMilli() + 120_000
	raw := testRawMapPokemon(encId, "lure-fort-910101", 51.5007, -0.1246, expireMs)

	pokemon, unlock, err := getOrCreatePokemonRecord(context.Background(), db.DbDetails{}, encId, "test")
	if err != nil {
		t.Fatalf("getOrCreatePokemonRecord: %v", err)
	}
	saveNeeded := pokemon.updateFromMap(context.Background(), db.DbDetails{}, raw, nil, "tester")

	if !saveNeeded {
		t.Errorf("updateFromMap on new record = false, want true")
	}
	if got := pokemon.PokestopId.ValueOrZero(); got != "lure-fort-910101" {
		t.Errorf("PokestopId = %q, want lure-fort-910101", got)
	}
	if pokemon.Lat != 51.5007 || pokemon.Lon != -0.1246 {
		t.Errorf("Lat/Lon = %v/%v, want 51.5007/-0.1246", pokemon.Lat, pokemon.Lon)
	}
	if got := pokemon.SeenType.ValueOrZero(); got != SeenType_LureWild {
		t.Errorf("SeenType = %q, want %q", got, SeenType_LureWild)
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

	// First sighting carries no expiry -> unverified record.
	noExpiry := testRawMapPokemon(encId, "lure-fort-910102", 51.5, -0.12, 0)
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
	withExpiry := testRawMapPokemon(encId, "lure-fort-910102", 51.5, -0.12, expireMs)
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
	pokemon, unlock, err := getOrCreatePokemonRecord(context.Background(), db.DbDetails{}, encId, "test")
	if err != nil {
		t.Fatalf("getOrCreatePokemonRecord: %v", err)
	}
	pokemon.SetSeenType(null.StringFrom(SeenType_Wild))
	pokemon.newRecord = false

	raw := testRawMapPokemon(encId, "lure-fort-910103", 51.5, -0.12, time.Now().UnixMilli()+90_000)
	if pokemon.updateFromMap(context.Background(), db.DbDetails{}, raw, nil, "tester") {
		t.Errorf("updateFromMap on wild record = true, want false")
	}
	if pokemon.PokestopId.Valid {
		t.Errorf("PokestopId set on wild record, want untouched")
	}
	unlock()
}

// Batch-level #390 regression: full pipeline with an unknown fort — the
// record is placed and a webhook fires with real coordinates.
func TestUpdatePokemonBatchPlacesLureWithUnknownFort(t *testing.T) {
	sink := lureTestSetup(t)
	const encId = uint64(910104)
	expireMs := time.Now().UnixMilli() + 120_000
	raw := testRawMapPokemon(encId, "lure-fort-910104", 48.8584, 2.2945, expireMs)

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
	request, response := testDiskEncounterProtos(encId, "lure-fort-910105", 40.7580, -73.9855)

	before := time.Now().Unix()
	UpdatePokemonRecordWithDiskEncounterProto(context.Background(), db.DbDetails{}, request, response, "tester")
	after := time.Now().Unix()

	pokemon, unlock, _ := peekPokemonRecordReadOnly(encId, "test")
	if pokemon == nil {
		t.Fatalf("pokemon %d not in cache after disk encounter", encId)
	}
	if got := pokemon.PokestopId.ValueOrZero(); got != "lure-fort-910105" {
		t.Errorf("PokestopId = %q, want lure-fort-910105", got)
	}
	if pokemon.Lat != 40.7580 || pokemon.Lon != -73.9855 {
		t.Errorf("Lat/Lon = %v/%v, want request coords 40.7580/-73.9855", pokemon.Lat, pokemon.Lon)
	}
	if got := pokemon.SeenType.ValueOrZero(); got != SeenType_LureEncounter {
		t.Errorf("SeenType = %q, want %q", got, SeenType_LureEncounter)
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
		t.Errorf("webhook coords = %v/%v, want 40.7580/-73.9855 (the #390 symptom was 0/0)", hook.Latitude, hook.Longitude)
	}
	if hook.DisappearTime <= 0 {
		t.Errorf("webhook DisappearTime = %d, want > 0 (the #390 symptom was 0)", hook.DisappearTime)
	}
}

// A GMO arriving after the encounter tightens the estimate to a verified
// despawn without downgrading the seen type or touching IVs.
func TestGmoAfterDiskEncounterContributesVerifiedExpiry(t *testing.T) {
	lureTestSetup(t)
	const encId = uint64(910106)
	request, response := testDiskEncounterProtos(encId, "lure-fort-910106", 40.0, -73.0)
	UpdatePokemonRecordWithDiskEncounterProto(context.Background(), db.DbDetails{}, request, response, "tester")

	expireMs := time.Now().UnixMilli() + 100_000
	raw := testRawMapPokemon(encId, "lure-fort-910106", 40.0, -73.0, expireMs)
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
	if got := pokemon.SeenType.ValueOrZero(); got != SeenType_LureEncounter {
		t.Errorf("SeenType = %q, want %q (must not downgrade)", got, SeenType_LureEncounter)
	}
	if got := pokemon.AtkIv.ValueOrZero(); got != 15 {
		t.Errorf("AtkIv = %d, want 15 (encounter data must survive the GMO merge)", got)
	}
	unlock()
}

// The classic order still works with no cache hop: GMO creates lure_wild,
// the encounter upgrades it in place.
func TestDiskEncounterAfterGmoUpgradesRecord(t *testing.T) {
	lureTestSetup(t)
	const encId = uint64(910107)
	expireMs := time.Now().UnixMilli() + 110_000
	raw := testRawMapPokemon(encId, "lure-fort-910107", 35.6595, 139.7005, expireMs)
	UpdatePokemonBatch(context.Background(), db.DbDetails{}, ScanParameters{}, nil, nil,
		[]RawMapPokemonData{raw}, nil, "tester")

	request, response := testDiskEncounterProtos(encId, "lure-fort-910107", 35.6595, 139.7005)
	UpdatePokemonRecordWithDiskEncounterProto(context.Background(), db.DbDetails{}, request, response, "tester")

	pokemon, unlock, _ := peekPokemonRecordReadOnly(encId, "test")
	if pokemon == nil {
		t.Fatalf("pokemon %d missing", encId)
	}
	if got := pokemon.SeenType.ValueOrZero(); got != SeenType_LureEncounter {
		t.Errorf("SeenType = %q, want %q", got, SeenType_LureEncounter)
	}
	if got := pokemon.AtkIv.ValueOrZero(); got != 15 {
		t.Errorf("AtkIv = %d, want 15", got)
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
	request, response := testDiskEncounterProtos(encId, "lure-fort-910108", 40.1, -73.1)
	UpdatePokemonRecordWithDiskEncounterProto(context.Background(), db.DbDetails{}, request, response, "tester")

	results := collectApiPokemonResults([]uint64{encId}, "test")
	if len(results) != 1 {
		t.Fatalf("collectApiPokemonResults returned %d results, want 1", len(results))
	}
}
