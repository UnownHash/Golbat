package decoder

import (
	"testing"

	"golbat/pogo"

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

	enqueuePeerLookup(peerLookupItem{EncounterId: 1, PokemonId: 1})
	enqueuePeerLookup(peerLookupItem{EncounterId: 2, PokemonId: 1})
	enqueuePeerLookup(peerLookupItem{EncounterId: 3, PokemonId: 1}) // dropped

	if len(peerLookupQueue) != 1 {
		t.Fatalf("queue should hold its single slot, got %d", len(peerLookupQueue))
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
	oldQueue, oldPeers := peerLookupQueue, peerClients
	defer func() { peerLookupQueue, peerClients = oldQueue, oldPeers }()

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
	oldQueue, oldPeers := peerLookupQueue, peerClients
	defer func() { peerLookupQueue, peerClients = oldQueue, oldPeers }()

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

// repopulateIv's weather-flip trigger must ask about the boost state being
// switched TO (the weather parameter), not the state still recorded on the
// pokemon at the moment locateScan fails to find a match.
func TestRepopulateIvEnqueuesNewWeatherNotOld(t *testing.T) {
	oldQueue, oldPeers := peerLookupQueue, peerClients
	defer func() { peerLookupQueue, peerClients = oldQueue, oldPeers }()

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
