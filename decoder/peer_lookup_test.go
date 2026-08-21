package decoder

import "testing"

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

func TestPeerLookupKeyIsStable(t *testing.T) {
	it := peerLookupItem{EncounterId: 7, PokemonId: 1, Form: 2, Weather: 3}
	if peerLookupKey(it) != peerLookupKey(it) { //nolint:staticcheck // SA4000: intentional x==x, asserting the key is deterministic across calls
		t.Fatal("key must be deterministic")
	}
	// SpawnId is context for the answer, not part of the question's identity.
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
