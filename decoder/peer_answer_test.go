package decoder

import (
	"testing"
	"time"

	pb "golbat/grpc"

	"github.com/guregu/null/v6"
)

func int64p(v int64) *int64 { return &v }

// The answering side's whole correctness obligation: a record only answers a
// question it actually describes. Encounter ids are reused across spawn
// mutations, and IVs are rolled per boost state, so species, form and weather
// all have to agree.
func TestPeerRecordMatches(t *testing.T) {
	base := func() *ApiPokemonResult {
		return &ApiPokemonResult{PokemonId: 25, Form: int64p(1), Weather: int64p(3)}
	}

	tests := []struct {
		name                     string
		record                   *ApiPokemonResult
		pokemonId, form, weather int32
		want                     bool
	}{
		{"everything agrees", base(), 25, 1, 3, true},
		{"species moved on", base(), 26, 1, 3, false},
		{"form moved on", base(), 25, 2, 3, false},
		{"boost state differs", base(), 25, 1, 0, false},
		{
			// A record that never learned its form makes no claim about it.
			"unknown form is not a mismatch",
			&ApiPokemonResult{PokemonId: 25, Weather: int64p(3)},
			25, 7, 3, true,
		},
		{
			// Likewise weather: an unset value is absence of a claim, not a
			// claim of NONE. This is what keeps the check usable by a peer
			// that computes an answer rather than holding one.
			"unknown weather is not a mismatch",
			&ApiPokemonResult{PokemonId: 25, Form: int64p(1)},
			25, 1, 7, true,
		},
		{"a miss is never a match", nil, 25, 1, 3, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := peerRecordMatches(tt.record, tt.pokemonId, tt.form, tt.weather); got != tt.want {
				t.Fatalf("peerRecordMatches = %v, want %v", got, tt.want)
			}
		})
	}
}

// Spelled out separately because it is the case the suite could not previously
// see: weather NONE (0) is the zero value of the request field, so a check
// comparing only species and form treats a record held under a boosted weather
// as a valid answer to an unboosted question.
func TestPeerRecordMatchesRejectsStatsRolledUnderAnotherWeather(t *testing.T) {
	boosted := &ApiPokemonResult{
		PokemonId: 25,
		Form:      int64p(0),
		Weather:   int64p(3), // still holds the pre-flip, boosted roll
		AtkIv:     int64p(15),
		DefIv:     int64p(15),
		StaIv:     int64p(15),
	}

	if peerRecordMatches(boosted, 25, 0, 0) {
		t.Fatal("a record rolled under a different boost state must not answer this question")
	}
}

// --- The spawn_id capability: answering expiry for a pokemon never seen. ---

// The despawn second is a local second-of-hour, so the answer is the next
// moment that second comes round. The asker recovers the second-of-hour from
// the timestamp (localSecondOfHour), so a round trip through the answer must
// give back exactly the value stored here.
func TestPeerSpawnpointExpiryReturnsNextOccurrenceOfDespawnSecond(t *testing.T) {
	const spawnId = int64(920401)
	const despawnSecond = 1234

	sp := &Spawnpoint{SpawnpointData: SpawnpointData{Id: spawnId, DespawnSec: null.IntFrom(despawnSecond)}}
	sp.syncDespawnFast()
	spawnpointCache.Set(spawnId, sp, time.Minute)

	now := time.Now()
	expiry, ok := peerSpawnpointExpiry(spawnId, now)
	if !ok {
		t.Fatal("a spawnpoint with a known despawn second must be answerable")
	}
	if expiry <= now.Unix() {
		t.Fatalf("expiry must be in the future: got %d, now %d", expiry, now.Unix())
	}
	if expiry > now.Unix()+3600 {
		t.Fatalf("the next occurrence is at most an hour away: got %d, now %d", expiry, now.Unix())
	}
	if got := localSecondOfHour(expiry, time.Local); got != despawnSecond {
		t.Fatalf("the asker must recover the stored despawn second: got %d want %d", got, despawnSecond)
	}
}

// A spawnpoint whose despawn second is still NULL, or one this instance has
// never seen, is a miss like any other - never an invented answer.
func TestPeerSpawnpointExpiryMisses(t *testing.T) {
	const knownButNull = int64(920402)
	sp := &Spawnpoint{SpawnpointData: SpawnpointData{Id: knownButNull}}
	sp.syncDespawnFast()
	spawnpointCache.Set(knownButNull, sp, time.Minute)

	if _, ok := peerSpawnpointExpiry(knownButNull, time.Now()); ok {
		t.Fatal("a NULL despawn_sec must not produce an answer")
	}
	if _, ok := peerSpawnpointExpiry(920403, time.Now()); ok {
		t.Fatal("an unknown spawnpoint must not produce an answer")
	}
	if _, ok := peerSpawnpointExpiry(0, time.Now()); ok {
		t.Fatal("spawn id 0 (nearby/cell pokemon) must not produce an answer")
	}
}

// The capability the request's spawn_id exists for: this instance never saw
// the pokemon, but knows the spawnpoint, so it answers the expiry alone.
func TestAnswerPeerLookupAnswersExpiryFromSpawnpointForUnseenPokemon(t *testing.T) {
	const spawnId = int64(920404)
	const encId = uint64(920405) // deliberately never in the pokemon cache

	sp := &Spawnpoint{SpawnpointData: SpawnpointData{Id: spawnId, DespawnSec: null.IntFrom(600)}}
	sp.syncDespawnFast()
	spawnpointCache.Set(spawnId, sp, time.Minute)

	now := time.Now()
	askedSpawnId := spawnId
	item := &pb.GetPokemonItem{EncounterId: encId, PokemonId: 25, SpawnId: &askedSpawnId}

	got := AnswerPeerLookup(item, now)
	if got == nil {
		t.Fatal("a known spawnpoint must let a peer answer expiry for a pokemon it never saw")
	}
	if got.GetId() != encId {
		t.Fatalf("id must match the question: got %d want %d", got.GetId(), encId)
	}
	if got.GetPokemonId() != 25 {
		t.Fatalf("pokemon_id must be echoed: got %d want 25", got.GetPokemonId())
	}
	if got.GetSpawnId() != spawnId {
		t.Fatalf("spawn_id must name the spawnpoint the expiry came from: got %d want %d", got.GetSpawnId(), spawnId)
	}
	if !got.GetExpireTimestampVerified() {
		t.Fatal("a spawnpoint despawn second is TTH-grade, so the answer is verified")
	}
	if got.ExpireTimestamp == nil {
		t.Fatal("the answer must carry an expiry")
	}
	if second := localSecondOfHour(got.GetExpireTimestamp(), time.Local); second != 600 {
		t.Fatalf("expiry second-of-hour: got %d want 600", second)
	}
	// Stats are genuinely absent, not zeroed: this instance holds none.
	if got.AtkIv != nil || got.Level != nil || got.Cp != nil {
		t.Fatal("an expiry-only answer must not claim stats")
	}
}

// --- The hit path: a pokemon this instance does hold. ---

// A cached record that describes the sighting being asked about is returned in
// full. This is the path the whole feature exists for and the one that had no
// test: removing the verification below it left the suite green.
func TestAnswerPeerLookupReturnsMatchingCachedRecord(t *testing.T) {
	const encId = uint64(920410)
	p := &Pokemon{PokemonData: PokemonData{
		Id: Uint64Str(encId), PokemonId: 25,
		Form:    null.IntFrom(1),
		Weather: null.IntFrom(3),
		AtkIv:   null.IntFrom(15), DefIv: null.IntFrom(14), StaIv: null.IntFrom(13),
		Level: null.IntFrom(20),
	}}
	pokemonCache.Set(encId, p, time.Minute)

	got := AnswerPeerLookup(&pb.GetPokemonItem{
		EncounterId: encId, PokemonId: 25, Form: 1, Weather: 3,
	}, time.Now())

	if got == nil {
		t.Fatal("a matching cached record must be answered with")
	}
	if got.GetId() != encId {
		t.Fatalf("id: got %d want %d", got.GetId(), encId)
	}
	if got.GetAtkIv() != 15 || got.GetDefIv() != 14 || got.GetStaIv() != 13 {
		t.Fatalf("stats not carried: %d/%d/%d", got.GetAtkIv(), got.GetDefIv(), got.GetStaIv())
	}
}

// Each verification field on its own must be able to turn a hit into a miss.
// Answering a mismatched question is worse than not answering: the asker
// adopts what it is told.
func TestAnswerPeerLookupRefusesRecordThatMovedOn(t *testing.T) {
	const encId = uint64(920411)
	p := &Pokemon{PokemonData: PokemonData{
		Id: Uint64Str(encId), PokemonId: 25,
		Form:    null.IntFrom(1),
		Weather: null.IntFrom(3),
		AtkIv:   null.IntFrom(15), DefIv: null.IntFrom(14), StaIv: null.IntFrom(13),
	}}
	pokemonCache.Set(encId, p, time.Minute)

	mismatches := map[string]*pb.GetPokemonItem{
		"species": {EncounterId: encId, PokemonId: 26, Form: 1, Weather: 3},
		"form":    {EncounterId: encId, PokemonId: 25, Form: 2, Weather: 3},
		"weather": {EncounterId: encId, PokemonId: 25, Form: 1, Weather: 0},
	}
	for name, item := range mismatches {
		t.Run(name, func(t *testing.T) {
			if got := AnswerPeerLookup(item, time.Now()); got != nil {
				t.Fatalf("a %s mismatch must not be answered, got %+v", name, got)
			}
		})
	}
}

// Without spawn_id there is nothing to look the spawnpoint up by, so a pokemon
// this instance never saw stays a miss - the shape a nearby/cell sighting
// produces.
func TestAnswerPeerLookupMissesWithoutSpawnId(t *testing.T) {
	const encId = uint64(920406) // never cached

	if got := AnswerPeerLookup(&pb.GetPokemonItem{EncounterId: encId, PokemonId: 25}, time.Now()); got != nil {
		t.Fatalf("no spawn_id and no pokemon means no answer, got %+v", got)
	}
}

// A preloaded spawnpoint must be answerable. preloadSpawnpoints StructScans
// rows straight into a Spawnpoint and caches it, so DespawnSec is populated
// while the lock-free mirrors have never been published - a state every other
// test here creates artificially by calling syncDespawnFast, and therefore the
// one state none of them exercises.
//
// This matters far beyond a corner case: it is the state of the entire 48-hour
// preload set immediately after every restart. peerSpawnpointExpiry reads the
// mirror with no locked fallback (deliberately - it runs inline in the gRPC
// handler, where blocking behind a spawnpoint lock held across a DB load would
// be exactly what the mirror exists to prevent), so an unpublished mirror
// makes the spawnpoint answer nothing at all. The spawnpoints worth the most
// to a peer - long-known, no longer densely scanned - are the ones least
// likely to get a fresh TTH sighting to republish it.
func TestPreloadedSpawnpointIsAnswerable(t *testing.T) {
	const spawnId = int64(920420)
	const encId = uint64(920421) // never in the pokemon cache

	// Exactly what rows.StructScan produces: fields set, mirrors untouched.
	sp := &Spawnpoint{SpawnpointData: SpawnpointData{
		Id:         spawnId,
		Lat:        51.5,
		Lon:        -0.12,
		DespawnSec: null.IntFrom(900),
		LastSeen:   time.Now().Unix(),
	}}
	if _, _, synced := sp.DespawnSecFast(); synced {
		t.Fatal("test setup is wrong: a freshly scanned row must start unsynced")
	}

	cachePreloadedSpawnpoint(sp)

	askedSpawnId := spawnId
	got := AnswerPeerLookup(&pb.GetPokemonItem{
		EncounterId: encId, PokemonId: 25, SpawnId: &askedSpawnId,
	}, time.Now())

	if got == nil {
		t.Fatal("a preloaded spawnpoint must be answerable - otherwise the capability answers nothing across the whole preload set after a restart")
	}
	if second := localSecondOfHour(got.GetExpireTimestamp(), time.Local); second != 900 {
		t.Fatalf("expiry second-of-hour: got %d want 900", second)
	}
}

// A record that describes a different sighting must not suppress the expiry
// answer. The expiry belongs to the spawnpoint the asker named; a
// species/form/weather mismatch on our pokemon record is no evidence about
// it, and the two halves of a lookup are independent.
//
// The weather case is the one that bites. A flip is precisely when a peer's
// record mismatches - it has not processed the same flip yet - so returning
// nothing there would drop the despawn half at exactly the moment the asker
// is most likely to be asking.
func TestAnswerPeerLookupStillAnswersExpiryWhenRecordMismatches(t *testing.T) {
	const spawnId = int64(920430)
	const encId = uint64(920431)

	sp := &Spawnpoint{SpawnpointData: SpawnpointData{Id: spawnId, DespawnSec: null.IntFrom(1500)}}
	sp.syncDespawnFast()
	spawnpointCache.Set(spawnId, sp, time.Minute)

	// Held under the pre-flip boosted state, with stats for that roll.
	p := &Pokemon{PokemonData: PokemonData{
		Id: Uint64Str(encId), PokemonId: 25,
		Form:    null.IntFrom(0),
		Weather: null.IntFrom(3),
		AtkIv:   null.IntFrom(15), DefIv: null.IntFrom(14), StaIv: null.IntFrom(13),
		Level: null.IntFrom(20),
	}}
	pokemonCache.Set(encId, p, time.Minute)

	askedSpawnId := spawnId
	got := AnswerPeerLookup(&pb.GetPokemonItem{
		EncounterId: encId, PokemonId: 25, Form: 0, Weather: 0, SpawnId: &askedSpawnId,
	}, time.Now())

	if got == nil {
		t.Fatal("a boost-state mismatch must not suppress the expiry, which does not depend on boost state")
	}
	if second := localSecondOfHour(got.GetExpireTimestamp(), time.Local); second != 1500 {
		t.Fatalf("expiry second-of-hour: got %d want 1500", second)
	}
	if !got.GetExpireTimestampVerified() {
		t.Fatal("the spawnpoint's despawn second is still TTH-grade")
	}
	// The stats half must still be withheld: that is what the mismatch proves.
	if got.AtkIv != nil || got.DefIv != nil || got.StaIv != nil || got.Level != nil {
		t.Fatalf("stats rolled under another boost state must not be answered with: got atk=%v level=%v", got.AtkIv, got.Level)
	}
}

// Without a spawn id there is nothing to fall through to, so a mismatched
// record is still a plain miss - the fall-through adds an expiry answer, it
// does not weaken the verification.
func TestAnswerPeerLookupMismatchWithoutSpawnIdIsStillAMiss(t *testing.T) {
	const encId = uint64(920432)
	p := &Pokemon{PokemonData: PokemonData{
		Id: Uint64Str(encId), PokemonId: 25, Form: null.IntFrom(1),
		AtkIv: null.IntFrom(15), DefIv: null.IntFrom(14), StaIv: null.IntFrom(13),
	}}
	pokemonCache.Set(encId, p, time.Minute)

	got := AnswerPeerLookup(&pb.GetPokemonItem{EncounterId: encId, PokemonId: 99}, time.Now())

	if got != nil {
		t.Fatalf("a mismatched record with no spawnpoint to fall back on must answer nothing, got %+v", got)
	}
}
