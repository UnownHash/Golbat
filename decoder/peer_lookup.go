package decoder

import (
	"context"
	"time"

	"golbat/config"
	"golbat/db"
	pb "golbat/grpc"
	"golbat/ottercache"
	"golbat/util"

	log "github.com/sirupsen/logrus"
	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

const (
	// peerLookupQueueSize is generous because the queue absorbs GMO bursts,
	// but it is bounded: CLAUDE.md forbids a blocking send from the decode
	// path to any worker whose throughput can fall below the event rate.
	peerLookupQueueSize = 16384

	// peerLookupBatchWindow is how long the worker waits to accumulate a batch
	// before dispatching. One round-trip per GMO batch, not per pokemon.
	peerLookupBatchWindow = 50 * time.Millisecond

	// peerLookupMaxBatch caps a single request's size.
	peerLookupMaxBatch = 500

	// peerLookupCacheTTL is the maximum life of a pokemon, so an entry can
	// never outlive the thing it describes.
	peerLookupCacheTTL = 60 * time.Minute

	// peerLookupShutdownFlushTimeout bounds the flush issued when
	// RunPeerLookup's context is cancelled. ctx is already Done at that
	// point, so deriving a per-call deadline from it the way the
	// steady-state path does would make every peer call fail before it
	// started (context.WithTimeout on an already-Done parent is itself
	// instantly Done). The shutdown flush therefore runs against
	// context.WithoutCancel(ctx), bounded by this timeout instead, so the
	// final batch — and anything still sitting in the queue — gets a real
	// chance to go out rather than being silently discarded.
	peerLookupShutdownFlushTimeout = 2 * time.Second
)

// peerLookupItem is one question: "what do you know about this sighting?"
type peerLookupItem struct {
	EncounterId uint64
	PokemonId   int32
	Form        int32
	Weather     int32
	SpawnId     int64
}

type peerClient struct {
	client  pb.PokemonClient
	secret  string
	timeout time.Duration
	address string
}

var (
	// peerLookupCache records questions already asked. Existence only: answers
	// are applied on arrival, so there is nothing worth retaining.
	peerLookupCache *ottercache.OtterCache[uint64, struct{}]
	peerLookupQueue chan peerLookupItem
	peerClients     []peerClient
	peerLookupDrops util.DropReporter
)

// peerLookupKey mixes the four fields that make a question distinct.
// SpawnId is deliberately excluded: it is context for answering, not part of
// the question's identity.
//
// A 64-bit mix can collide, and the cost of a collision is one suppressed
// lookup. At ~10^6 live entries the probability of any collision is ~10^-8,
// well below the rate at which peers time out.
func peerLookupKey(item peerLookupItem) uint64 {
	h := item.EncounterId
	h ^= uint64(uint32(item.PokemonId)) * 0x9E3779B97F4A7C15
	h ^= uint64(uint32(item.Form)) * 0xBF58476D1CE4E5B9
	h ^= uint64(uint32(item.Weather)) * 0x94D049BB133111EB

	// splitmix64 finaliser: avalanche the XOR-folded fields.
	h ^= h >> 30
	h *= 0xBF58476D1CE4E5B9
	h ^= h >> 27
	h *= 0x94D049BB133111EB
	h ^= h >> 31
	return h
}

// InitPeerLookup builds the cache, dials configured peers and starts the
// worker. Called from InitDataCache, after config load.
func InitPeerLookup() {
	peerLookupCache = ottercache.NewOtterCache(ottercache.OtterCacheConfig[uint64, struct{}]{
		Name:       "peer_lookup",
		DefaultTTL: peerLookupCacheTTL,
		// The TTL encodes a lifetime; a read must never extend it.
		TouchOnHit: false,
	})

	if len(config.Config.GolbatPeers) == 0 {
		return
	}

	peerLookupQueue = make(chan peerLookupItem, peerLookupQueueSize)

	for _, cfg := range config.Config.GolbatPeers {
		conn, err := googlegrpc.NewClient(cfg.Address,
			googlegrpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			log.Errorf("[PEER] cannot create client for %s: %s", cfg.Address, err)
			continue
		}
		peerClients = append(peerClients, peerClient{
			client:  pb.NewPokemonClient(conn),
			secret:  cfg.ApiSecret,
			timeout: time.Duration(cfg.TimeoutMs) * time.Millisecond,
			address: cfg.Address,
		})
		log.Infof("[PEER] configured golbat peer %s", cfg.Address)
	}
}

// enqueuePeerLookup is called from the decode path under an entity lock. It
// must never block and must never do I/O.
func enqueuePeerLookup(item peerLookupItem) {
	if len(peerClients) == 0 || peerLookupQueue == nil {
		return
	}

	key := peerLookupKey(item)
	if peerLookupCache != nil {
		if _, asked := peerLookupCache.Get(key); asked {
			return
		}
		peerLookupCache.Set(key, struct{}{}, ottercache.DefaultTTL)
	}

	select {
	case peerLookupQueue <- item:
	default:
		peerLookupDrops.Report(func(dropped int64) {
			log.Warnf("[PEER] dropped %d lookup candidates: queue full", dropped)
		})
		statsCollector.IncPeerLookupDropped()
	}
}

// RunPeerLookup batches queued questions and dispatches them. One goroutine.
func RunPeerLookup(ctx context.Context, dbDetails db.DbDetails) {
	if len(peerClients) == 0 || peerLookupQueue == nil {
		return
	}

	batch := make([]peerLookupItem, 0, peerLookupMaxBatch)
	timer := time.NewTimer(peerLookupBatchWindow)
	defer timer.Stop()

	flush := func(flushCtx context.Context) {
		if len(batch) == 0 {
			return
		}
		dispatchPeerBatch(flushCtx, dbDetails, batch)
		batch = batch[:0]
	}

	for {
		select {
		case <-ctx.Done():
			// ctx is Done, so a flush derived from it would fail instantly.
			// Run the shutdown flush against a bounded, uncancelled context,
			// and drain whatever is still queued into it (chunked, like the
			// steady-state path) rather than discarding it.
			shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), peerLookupShutdownFlushTimeout)

		drain:
			for shutdownCtx.Err() == nil {
				select {
				case item := <-peerLookupQueue:
					batch = append(batch, item)
					if len(batch) >= peerLookupMaxBatch {
						flush(shutdownCtx)
					}
				default:
					break drain
				}
			}
			flush(shutdownCtx)
			cancel()
			return
		case item := <-peerLookupQueue:
			batch = append(batch, item)
			if len(batch) >= peerLookupMaxBatch {
				flush(ctx)
			}
		case <-timer.C:
			flush(ctx)
			timer.Reset(peerLookupBatchWindow)
		}
	}
}

func dispatchPeerBatch(ctx context.Context, dbDetails db.DbDetails, batch []peerLookupItem) {
	byEncounter := make(map[uint64]peerLookupItem, len(batch))
	items := make([]*pb.GetPokemonItem, 0, len(batch))
	for _, it := range batch {
		byEncounter[it.EncounterId] = it
		reqItem := &pb.GetPokemonItem{
			EncounterId: it.EncounterId,
			PokemonId:   it.PokemonId,
			Form:        it.Form,
			Weather:     it.Weather,
		}
		if it.SpawnId != 0 {
			spawnId := it.SpawnId
			reqItem.SpawnId = &spawnId
		}
		items = append(items, reqItem)
	}

	for _, peer := range peerClients {
		if len(byEncounter) == 0 {
			return
		}

		callCtx, cancel := context.WithTimeout(ctx, peer.timeout)
		if peer.secret != "" {
			callCtx = metadata.AppendToOutgoingContext(callCtx, "authorization", peer.secret)
		}
		resp, err := peer.client.GetPokemon(callCtx, &pb.GetPokemonRequest{Items: items})
		cancel()

		if err != nil {
			// Timing out costs today's behaviour, not correctness.
			log.Debugf("[PEER] %s lookup failed: %s", peer.address, err)
			continue
		}

		for _, res := range resp.GetResults() {
			if _, wanted := byEncounter[res.GetId()]; !wanted {
				continue
			}
			// TODO(task 9): apply the peer's answer via applyPeerResult, which
			// does not exist yet (Task 9's deliverable). Until then, log what
			// would have been applied so the ask/batch/dedupe/receive path is
			// observable and testable on its own.
			log.Debugf("[PEER] %s answered for encounter %d", peer.address, res.GetId())
			// items (built once above) is never narrowed, so a later peer is
			// still asked about this encounter too; deleting here only stops
			// this answer from being applied twice once Task 9 wires up
			// applyPeerResult.
			delete(byEncounter, res.GetId())
		}
	}
}
