package decoder

import (
	"context"
	"strconv"
	"testing"
	"time"

	"golbat/db"
	"golbat/pogo"

	"github.com/golang/geo/s2"
	"github.com/guregu/null/v6"
)

// The key must separate the four fields that make a question distinct.
// Encounter ids are reused across spawn mutations (so pokemon_id/form matter),
// and a weather flip is a genuinely different question about the same pokemon.
func TestPeerLookupKeyDistinguishesEachField(t *testing.T) {
	base := peerLookupItem{EncounterId: 42, PokemonId: 25, Form: 1, Weather: 0}

	variants := map[string]peerLookupItem{
		"encounter": {EncounterId: 43, PokemonId: 25, Form: 1, Weather: 0},
		"pokemon":   {EncounterId: 42, PokemonId: 26, Form: 1, Weather: 0},
		"form":      {EncounterId: 42, PokemonId: 25, Form: 2, Weather: 0},
		"weather":   {EncounterId: 42, PokemonId: 25, Form: 1, Weather: 3},
	}

	baseKey := peerLookupKey(base)
	for name, v := range variants {
		if peerLookupKey(v) == baseKey {
			t.Errorf("%s change must produce a different key", name)
		}
	}
}

// A golden value rather than a self-comparison: the point is that the mixer
// stays stable across builds and versions, which comparing a call to itself
// cannot show.
func TestPeerLookupKeyIsStable(t *testing.T) {
	it := peerLookupItem{EncounterId: 7, PokemonId: 1, Form: 2, Weather: 3}

	if got := peerLookupKey(it); got != 0xAE09E87BED5D1D2B {
		t.Fatalf("key drifted: got %#016x want 0xAE09E87BED5D1D2B", got)
	}

	// SpawnId is context for answering, not part of the question's identity.
	withSpawn := it
	withSpawn.SpawnId = 999
	if peerLookupKey(withSpawn) != peerLookupKey(it) {
		t.Fatal("spawn_id must not change the key")
	}
}

// A full queue must drop rather than block: the decode path holds entity locks
// and a blocking send here reproduces the fill-drain limit cycle CLAUDE.md
// warns about.
func TestEnqueuePeerLookupDropsWhenQueueFull(t *testing.T) {
	oldQueue, oldPeers := peerLookupQueue, peerClients
	defer func() { peerLookupQueue, peerClients = oldQueue, oldPeers }()

	peerLookupQueue = make(chan peerLookupItem, 1)
	peerClients = []peerClient{{}} // non-empty so enqueue is active

	before := testStatsCollector.peerLookupDropped.Load()

	enqueuePeerLookup(peerLookupItem{EncounterId: 1, PokemonId: 1}) // fills the slot
	enqueuePeerLookup(peerLookupItem{EncounterId: 2, PokemonId: 1}) // dropped
	enqueuePeerLookup(peerLookupItem{EncounterId: 3, PokemonId: 1}) // dropped

	if len(peerLookupQueue) != 1 {
		t.Fatalf("queue should hold its single slot, got %d", len(peerLookupQueue))
	}
	// CLAUDE.md requires a loss-tolerant decode-path queue to drop *and
	// count*: silent loss is the failure mode an operator cannot diagnose.
	// Queue length alone cannot tell a counted drop from an uncounted one.
	if got := testStatsCollector.peerLookupDropped.Load() - before; got != 2 {
		t.Fatalf("IncPeerLookupDropped fired %d times, want 2 (both rejected sends)", got)
	}
}

// Asking twice about the same question must produce one call.
func TestEnqueuePeerLookupDedupes(t *testing.T) {
	oldQueue, oldPeers := peerLookupQueue, peerClients
	defer func() { peerLookupQueue, peerClients = oldQueue, oldPeers }()

	peerLookupQueue = make(chan peerLookupItem, 8)
	peerClients = []peerClient{{}}

	it := peerLookupItem{EncounterId: 99, PokemonId: 25, Form: 1, Weather: 0}
	enqueuePeerLookup(it)
	enqueuePeerLookup(it)

	if len(peerLookupQueue) != 1 {
		t.Fatalf("expected one queued item after a repeat, got %d", len(peerLookupQueue))
	}
}

// A pokemon that is both stats-less and unverified must produce one queued
// item, not two: a single lookup answers both questions at once.
func TestConsiderPeerLookupOneItemNotTwo(t *testing.T) {
	oldQueue, oldPeers, oldCache := peerLookupQueue, peerClients, peerLookupCache
	defer func() { peerLookupQueue, peerClients, peerLookupCache = oldQueue, oldPeers, oldCache }()

	// Nil the dedup cache: with it live, a regression that enqueues once per
	// need (needsStats, then needsExpiry) would build a byte-identical item
	// both times, so the second call would be silently swallowed by dedup and
	// the queue length could not tell the two implementations apart.
	peerLookupCache = nil
	peerLookupQueue = make(chan peerLookupItem, 8)
	peerClients = []peerClient{{}}

	pokemon := &Pokemon{PokemonData: PokemonData{
		Id:                      Uint64Str(12345),
		PokemonId:               25,
		AtkIv:                   null.NewInt(0, false), // stats-less
		ExpireTimestampVerified: false,                 // unverified
		SpawnId:                 null.IntFrom(777),     // on a known spawnpoint
	}}

	pokemon.considerPeerLookup()

	if len(peerLookupQueue) != 1 {
		t.Fatalf("stats-less AND unverified must enqueue exactly one item, got %d", len(peerLookupQueue))
	}

	got := <-peerLookupQueue
	if got.EncounterId != 12345 || got.SpawnId != 777 {
		t.Fatalf("unexpected item: %+v", got)
	}
}

// A pokemon with neither question outstanding — stats present and expiry
// verified — has nothing worth asking.
func TestConsiderPeerLookupNoQuestionNoEnqueue(t *testing.T) {
	oldQueue, oldPeers, oldCache := peerLookupQueue, peerClients, peerLookupCache
	defer func() { peerLookupQueue, peerClients, peerLookupCache = oldQueue, oldPeers, oldCache }()

	peerLookupCache = nil
	peerLookupQueue = make(chan peerLookupItem, 8)
	peerClients = []peerClient{{}}

	pokemon := &Pokemon{PokemonData: PokemonData{
		Id:                      Uint64Str(54321),
		PokemonId:               1,
		AtkIv:                   null.IntFrom(10), // has stats
		ExpireTimestampVerified: true,             // verified
		SpawnId:                 null.IntFrom(777),
	}}

	pokemon.considerPeerLookup()

	if len(peerLookupQueue) != 0 {
		t.Fatalf("nothing worth asking should not enqueue, got %d items", len(peerLookupQueue))
	}
}

// peerLookupTestQueue isolates the lookup globals for one test and hands back
// the queue. The dedup cache is nilled deliberately: with it live, a
// regression that enqueues more than once builds a byte-identical item each
// time, so dedup would swallow the extra and the queue length could not tell
// the implementations apart. enqueuePeerLookup nil-guards the cache.
func peerLookupTestQueue(t *testing.T) chan peerLookupItem {
	t.Helper()
	oldQueue, oldPeers, oldCache := peerLookupQueue, peerClients, peerLookupCache
	t.Cleanup(func() { peerLookupQueue, peerClients, peerLookupCache = oldQueue, oldPeers, oldCache })

	peerLookupCache = nil
	peerLookupQueue = make(chan peerLookupItem, 8)
	peerClients = []peerClient{{}} // non-empty so enqueue is active
	return peerLookupQueue
}

// The wild trigger, at its call site rather than through considerPeerLookup
// directly: deleting the call from updateFromWild left the suite green.
// The lookup fires after addWildPokemon, which is what supplies the spawn id
// and the provisional expiry, so the enqueued question carries both.
func TestUpdateFromWildEnqueuesLookup(t *testing.T) {
	queue := peerLookupTestQueue(t)

	const encId = uint64(920501)
	const spawnId = int64(0x920502)

	// A known spawnpoint with no despawn second yet: the fast path in
	// setExpireTimestampFromSpawnpoint reads the atomic mirror and never
	// touches the database.
	sp := &Spawnpoint{SpawnpointData: SpawnpointData{Id: spawnId}}
	sp.syncDespawnFast()
	spawnpointCache.Set(spawnId, sp, time.Minute)

	pokemon := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(encId)}}
	wild := &pogo.WildPokemonProto{
		EncounterId:  encId,
		SpawnPointId: strconv.FormatInt(spawnId, 16),
		Latitude:     51.5,
		Longitude:    -0.12,
		Pokemon: &pogo.PokemonProto{
			PokemonId:      pogo.HoloPokemonId(25),
			PokemonDisplay: &pogo.PokemonDisplayProto{},
		},
	}

	pokemon.updateFromWild(context.Background(), db.DbDetails{}, wild, 1234, nil, time.Now().UnixMilli(), "tester")

	if len(queue) != 1 {
		t.Fatalf("a wild sighting with no IVs must enqueue one lookup, got %d", len(queue))
	}
	got := <-queue
	if got.EncounterId != encId {
		t.Fatalf("encounter id: got %d want %d", got.EncounterId, encId)
	}
	if got.SpawnId != spawnId {
		t.Fatalf("spawn id: got %d want %d - the question must carry it so a peer can answer expiry", got.SpawnId, spawnId)
	}
	if got.PokemonId != 25 {
		t.Fatalf("pokemon id: got %d want 25", got.PokemonId)
	}
}

// The nearby trigger, at its call site. A nearby/cell sighting has no spawn
// id, so only the stats half of the question applies - which is exactly why
// the trigger is not conditional on having one.
func TestUpdateFromNearbyEnqueuesLookup(t *testing.T) {
	queue := peerLookupTestQueue(t)

	const encId = uint64(920503)
	cellId := int64(s2.CellIDFromLatLng(s2.LatLngFromDegrees(51.5, -0.12)).Parent(15))

	// Already seen in a cell: updateFromNearby returns early rather than
	// downgrading a better sighting, and that early return is before the
	// trigger.
	pokemon := &Pokemon{PokemonData: PokemonData{
		Id:       Uint64Str(encId),
		SeenType: null.StringFrom(SeenType_Cell),
	}}
	nearby := &pogo.NearbyPokemonProto{
		PokedexNumber:  25,
		PokemonDisplay: &pogo.PokemonDisplayProto{},
	}

	pokemon.updateFromNearby(context.Background(), db.DbDetails{}, nearby, cellId, nil, time.Now().UnixMilli(), "tester")

	if len(queue) != 1 {
		t.Fatalf("a nearby sighting with no IVs must enqueue one lookup, got %d", len(queue))
	}
	got := <-queue
	if got.EncounterId != encId {
		t.Fatalf("encounter id: got %d want %d", got.EncounterId, encId)
	}
	if got.SpawnId != 0 {
		t.Fatalf("a nearby sighting has no spawn id, got %d", got.SpawnId)
	}
}

// repopulateIv's weather-flip trigger must ask about the boost state being
// switched TO (the weather parameter), not the state still recorded on the
// pokemon at the moment locateScan fails to find a match.
func TestRepopulateIvEnqueuesNewWeatherNotOld(t *testing.T) {
	oldQueue, oldPeers, oldCache := peerLookupQueue, peerClients, peerLookupCache
	defer func() { peerLookupQueue, peerClients, peerLookupCache = oldQueue, oldPeers, oldCache }()

	peerLookupCache = nil
	peerLookupQueue = make(chan peerLookupItem, 8)
	peerClients = []peerClient{{}}

	pokemon := &Pokemon{PokemonData: PokemonData{
		Id:        Uint64Str(999),
		PokemonId: 25,
		Weather:   null.IntFrom(int64(pogo.GameplayWeatherProto_NONE)), // old: unboosted
	}}

	newWeather := int64(pogo.GameplayWeatherProto_RAINY) // new: boosted
	pokemon.repopulateIv(newWeather, false)

	if len(peerLookupQueue) != 1 {
		t.Fatalf("expected one enqueued item on the weather-flip/no-match path, got %d", len(peerLookupQueue))
	}

	got := <-peerLookupQueue
	if got.Weather != int32(newWeather) {
		t.Fatalf("enqueued Weather = %d, want the NEW boost state %d (not pokemon.Weather = %d)",
			got.Weather, newWeather, pokemon.Weather.ValueOrZero())
	}
}
