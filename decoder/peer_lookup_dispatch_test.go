package decoder

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"golbat/db"
	pb "golbat/grpc"

	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// fakePokemonClient is a minimal pb.PokemonClient for exercising
// dispatchPeerBatch without a real gRPC connection. Only GetPokemon is
// meaningful here: Search/SearchV3 exist solely to satisfy the interface.
type fakePokemonClient struct {
	mu         sync.Mutex
	calls      int
	getPokemon func(ctx context.Context, in *pb.GetPokemonRequest) (*pb.GetPokemonResponse, error)
}

func (f *fakePokemonClient) Search(context.Context, *pb.PokemonScanRequest, ...googlegrpc.CallOption) (*pb.PokemonScanResponse, error) {
	return nil, errors.New("fakePokemonClient: Search not implemented")
}

func (f *fakePokemonClient) SearchV3(context.Context, *pb.PokemonScanRequestV3, ...googlegrpc.CallOption) (*pb.PokemonScanResponseV3, error) {
	return nil, errors.New("fakePokemonClient: SearchV3 not implemented")
}

func (f *fakePokemonClient) GetPokemon(ctx context.Context, in *pb.GetPokemonRequest, _ ...googlegrpc.CallOption) (*pb.GetPokemonResponse, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.getPokemon != nil {
		return f.getPokemon(ctx, in)
	}
	return &pb.GetPokemonResponse{}, nil
}

func (f *fakePokemonClient) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// answerAll builds a GetPokemon handler that answers every requested
// encounter id, unconditionally.
func answerAll() func(context.Context, *pb.GetPokemonRequest) (*pb.GetPokemonResponse, error) {
	return func(_ context.Context, in *pb.GetPokemonRequest) (*pb.GetPokemonResponse, error) {
		results := make([]*pb.PokemonResult, 0, len(in.GetItems()))
		for _, it := range in.GetItems() {
			results = append(results, &pb.PokemonResult{Id: it.GetEncounterId()})
		}
		return &pb.GetPokemonResponse{Results: results}, nil
	}
}

func withPeerClients(t *testing.T, clients []peerClient) {
	t.Helper()
	old := peerClients
	t.Cleanup(func() { peerClients = old })
	peerClients = clients
}

// An encounter peer 1 has no answer for must still be attempted against
// peer 2 - dispatchPeerBatch's per-peer loop must not give up after the
// first miss.
func TestDispatchPeerBatchTriesNextPeerForUnanswered(t *testing.T) {
	peer1 := &fakePokemonClient{getPokemon: func(context.Context, *pb.GetPokemonRequest) (*pb.GetPokemonResponse, error) {
		return &pb.GetPokemonResponse{}, nil // knows nothing
	}}
	var peer2Items []*pb.GetPokemonItem
	peer2 := &fakePokemonClient{getPokemon: func(_ context.Context, in *pb.GetPokemonRequest) (*pb.GetPokemonResponse, error) {
		peer2Items = in.GetItems()
		return &pb.GetPokemonResponse{Results: []*pb.PokemonResult{{Id: 1}}}, nil
	}}
	withPeerClients(t, []peerClient{
		{client: peer1, address: "peer1", timeout: time.Second},
		{client: peer2, address: "peer2", timeout: time.Second},
	})

	dispatchPeerBatch(context.Background(), db.DbDetails{}, []peerLookupItem{{EncounterId: 1, PokemonId: 25}})

	if peer1.callCount() != 1 {
		t.Fatalf("expected peer1 to be called once, got %d", peer1.callCount())
	}
	if peer2.callCount() != 1 {
		t.Fatalf("expected peer2 to be tried after peer1 had no answer, got %d calls", peer2.callCount())
	}
	if len(peer2Items) != 1 || peer2Items[0].GetEncounterId() != 1 {
		t.Fatalf("expected peer2 to be asked about the unanswered encounter, got %+v", peer2Items)
	}
}

// Once every encounter in the batch has been answered, byEncounter is empty
// and the whole-batch short-circuit must skip any remaining peers entirely.
func TestDispatchPeerBatchShortCircuitsWhenAllAnswered(t *testing.T) {
	peer1 := &fakePokemonClient{getPokemon: answerAll()}
	peer2 := &fakePokemonClient{}
	withPeerClients(t, []peerClient{
		{client: peer1, address: "peer1", timeout: time.Second},
		{client: peer2, address: "peer2", timeout: time.Second},
	})

	batch := []peerLookupItem{{EncounterId: 1, PokemonId: 25}, {EncounterId: 2, PokemonId: 6}}
	dispatchPeerBatch(context.Background(), db.DbDetails{}, batch)

	if peer2.callCount() != 0 {
		t.Fatalf("expected peer2 to be skipped once peer1 answered every encounter, got %d calls", peer2.callCount())
	}
}

// One peer erroring (down, timed out) must not abort the batch: the
// remaining peers still get a chance to answer.
func TestDispatchPeerBatchContinuesAfterPeerError(t *testing.T) {
	peer1 := &fakePokemonClient{getPokemon: func(context.Context, *pb.GetPokemonRequest) (*pb.GetPokemonResponse, error) {
		return nil, errors.New("peer1 unreachable")
	}}
	peer2 := &fakePokemonClient{getPokemon: answerAll()}
	withPeerClients(t, []peerClient{
		{client: peer1, address: "peer1", timeout: time.Second},
		{client: peer2, address: "peer2", timeout: time.Second},
	})

	dispatchPeerBatch(context.Background(), db.DbDetails{}, []peerLookupItem{{EncounterId: 1, PokemonId: 1}})

	if peer2.callCount() != 1 {
		t.Fatalf("expected peer2 to still be tried after peer1 errored, got %d calls", peer2.callCount())
	}
}

// A configured secret must ride along as the "authorization" metadata key
// the peer's own handler checks (grpc_server_pokemon.go).
func TestDispatchPeerBatchAttachesAuthorizationWhenSecretConfigured(t *testing.T) {
	var authPresent bool
	var authValues []string
	peer := &fakePokemonClient{getPokemon: func(ctx context.Context, in *pb.GetPokemonRequest) (*pb.GetPokemonResponse, error) {
		if md, ok := metadata.FromOutgoingContext(ctx); ok {
			authPresent = true
			authValues = md.Get("authorization")
		}
		return &pb.GetPokemonResponse{}, nil
	}}
	withPeerClients(t, []peerClient{{client: peer, address: "peer1", timeout: time.Second, secret: "s3cr3t"}})

	dispatchPeerBatch(context.Background(), db.DbDetails{}, []peerLookupItem{{EncounterId: 1, PokemonId: 1}})

	if !authPresent || len(authValues) != 1 || authValues[0] != "s3cr3t" {
		t.Fatalf("expected authorization metadata [s3cr3t], got present=%v values=%v", authPresent, authValues)
	}
}

// No secret configured must mean no authorization metadata at all, not an
// empty-string one.
func TestDispatchPeerBatchOmitsAuthorizationWhenNoSecret(t *testing.T) {
	var authValues []string
	peer := &fakePokemonClient{getPokemon: func(ctx context.Context, in *pb.GetPokemonRequest) (*pb.GetPokemonResponse, error) {
		if md, ok := metadata.FromOutgoingContext(ctx); ok {
			authValues = md.Get("authorization")
		}
		return &pb.GetPokemonResponse{}, nil
	}}
	withPeerClients(t, []peerClient{{client: peer, address: "peer1", timeout: time.Second}})

	dispatchPeerBatch(context.Background(), db.DbDetails{}, []peerLookupItem{{EncounterId: 1, PokemonId: 1}})

	if len(authValues) != 0 {
		t.Fatalf("expected no authorization metadata without a configured secret, got %v", authValues)
	}
}

// SpawnId must only ride along on the wire when the question actually has
// one; a zero SpawnId means "unknown", not "spawn id zero".
func TestDispatchPeerBatchSetsSpawnIdOnlyWhenNonZero(t *testing.T) {
	var gotItems []*pb.GetPokemonItem
	peer := &fakePokemonClient{getPokemon: func(_ context.Context, in *pb.GetPokemonRequest) (*pb.GetPokemonResponse, error) {
		gotItems = in.GetItems()
		return &pb.GetPokemonResponse{}, nil
	}}
	withPeerClients(t, []peerClient{{client: peer, address: "peer1", timeout: time.Second}})

	batch := []peerLookupItem{
		{EncounterId: 1, PokemonId: 1, SpawnId: 0},
		{EncounterId: 2, PokemonId: 1, SpawnId: 555},
	}
	dispatchPeerBatch(context.Background(), db.DbDetails{}, batch)

	byId := make(map[uint64]*pb.GetPokemonItem, len(gotItems))
	for _, it := range gotItems {
		byId[it.GetEncounterId()] = it
	}

	if byId[1] == nil || byId[1].SpawnId != nil {
		t.Fatalf("expected no SpawnId pointer for a zero SpawnId, got %+v", byId[1])
	}
	if byId[2] == nil || byId[2].SpawnId == nil || *byId[2].SpawnId != 555 {
		t.Fatalf("expected SpawnId pointer set to 555, got %+v", byId[2])
	}
}

// A shutdown must actually flush: RunPeerLookup's ctx.Done() path must not
// hand dispatchPeerBatch an already-cancelled context (every peer call
// would fail before it started), and whatever is still sitting in the
// queue at shutdown must still go out rather than being discarded.
func TestRunPeerLookupFlushesQueueOnShutdown(t *testing.T) {
	oldQueue := peerLookupQueue
	t.Cleanup(func() { peerLookupQueue = oldQueue })

	var mu sync.Mutex
	var receivedEncounters []uint64
	sawCancelledCtx := false
	peer := &fakePokemonClient{getPokemon: func(ctx context.Context, in *pb.GetPokemonRequest) (*pb.GetPokemonResponse, error) {
		mu.Lock()
		defer mu.Unlock()
		if ctx.Err() != nil {
			sawCancelledCtx = true
		}
		for _, it := range in.GetItems() {
			receivedEncounters = append(receivedEncounters, it.GetEncounterId())
		}
		return &pb.GetPokemonResponse{}, nil
	}}
	withPeerClients(t, []peerClient{{client: peer, address: "peer1", timeout: 200 * time.Millisecond}})

	peerLookupQueue = make(chan peerLookupItem, 8)
	// Queued before RunPeerLookup ever starts, and ctx is cancelled up
	// front: the batch window (50ms) never gets a chance to fire, so the
	// shutdown path is the only thing that can ever send this.
	peerLookupQueue <- peerLookupItem{EncounterId: 42, PokemonId: 1}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		RunPeerLookup(ctx, db.DbDetails{})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("RunPeerLookup did not return after its context was cancelled")
	}

	mu.Lock()
	defer mu.Unlock()
	if sawCancelledCtx {
		t.Fatal("dispatchPeerBatch's peer call ran under an already-cancelled context during shutdown")
	}
	if len(receivedEncounters) != 1 || receivedEncounters[0] != 42 {
		t.Fatalf("expected the queued item to be flushed on shutdown, got %v", receivedEncounters)
	}
}
