package decoder

import (
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"

	"golbat/util"
)

type treeOp uint8

const (
	treeOpDelete treeOp = iota
	treeOpInsert
)

// treeEvictionEntry is one deferred R-tree mutation (insert or delete).
type treeEvictionEntry[K comparable] struct {
	op  treeOp
	id  K
	lat float64
	lon float64
}

// treeEvictor defers R-tree removals out of cache-eviction callbacks.
// ttlcache runs each eviction callback on its own goroutine (Cache.OnEviction
// wraps the registered fn in `go fn(...)`), so a mass-expiry sweep used to
// spawn thousands of goroutines all contending for the global tree write
// lock — the convoy that froze savers holding entity locks. Enqueue collapses
// that to a channel send; a single worker drains the channel and flushes
// removals in batches so the tree mutex is taken once per ~batchSize items
// by one goroutine instead of once per item by thousands.
type treeEvictor[K comparable] struct {
	name      string
	ch        chan treeEvictionEntry[K]
	flush     func([]treeEvictionEntry[K])
	batchSize int

	closeOnce sync.Once
	done      chan struct{}

	lastBacklogWarn atomic.Int64 // unix nanos; backlog warnings at most 1/s
	drops           util.DropReporter
}

// The flush callback must not retain the slice after returning; the buffer is reused.
func newTreeEvictor[K comparable](name string, capacity, batchSize int, flush func([]treeEvictionEntry[K])) *treeEvictor[K] {
	e := &treeEvictor[K]{
		name:      name,
		ch:        make(chan treeEvictionEntry[K], capacity),
		flush:     flush,
		batchSize: batchSize,
		done:      make(chan struct{}),
	}
	go e.run()
	return e
}

// Enqueue queues a removal; EnqueueInsert queues an insertion. All tree
// mutations flow through one ordered channel and one worker: production
// goroutine dumps showed 90+ savers convoyed on the tree write mutex
// behind eviction drains and COW clone chains — with a single writer,
// savers never touch the mutex at all, and only the worker and the ~1/s
// snapshot refresh remain as lock parties.
//
// Blocking sends are safe ONLY for callers whose concurrency is bounded
// (savers: capped by the raw-processing limiter). Eviction callbacks run
// on one goroutine per evicted item — with a full channel during a mass
// expiry, blocking there parked millions of goroutines, each holding an
// entity lock and 8KB of stack, strangling the scheduler until the drain
// rate collapsed (overnight production incident: channel pegged at cap
// for >1h, entity locks held for minutes, worker at ~500 ops/s). Those
// callers use TryEnqueue and drop instead: a dropped delete leaves a
// ghost tree point, which scans already tolerate (candidates are verified
// against the lookup cache, cleaned inline before the enqueue).
func (e *treeEvictor[K]) Enqueue(id K, lat, lon float64) {
	e.enqueue(treeEvictionEntry[K]{op: treeOpDelete, id: id, lat: lat, lon: lon})
}

// TryEnqueue queues a removal without blocking. Returns false (entry
// dropped) if the channel is full. For unbounded-concurrency callers
// (eviction callbacks); see Enqueue for why they must never block.
func (e *treeEvictor[K]) TryEnqueue(id K, lat, lon float64) bool {
	defer func() {
		if r := recover(); r != nil {
			log.Debugf("[TREE_EVICTOR] %s try-enqueue after close dropped: %v", e.name, r)
		}
	}()
	select {
	case e.ch <- treeEvictionEntry[K]{op: treeOpDelete, id: id, lat: lat, lon: lon}:
		return true
	default:
		e.drops.Report(func(dropped int64) {
			log.Warnf("[TREE_EVICTOR] %s dropped %d evictions in the last second (queue full %d/%d; ghost points until restart)",
				e.name, dropped, len(e.ch), cap(e.ch))
		})
		return false
	}
}

func (e *treeEvictor[K]) EnqueueInsert(id K, lat, lon float64) {
	e.enqueue(treeEvictionEntry[K]{op: treeOpInsert, id: id, lat: lat, lon: lon})
}

func (e *treeEvictor[K]) enqueue(entry treeEvictionEntry[K]) {
	defer func() {
		// Sending on a closed channel panics; Close is only used by
		// tests and shutdown, where losing the entry is acceptable.
		if r := recover(); r != nil {
			log.Debugf("[TREE_EVICTOR] %s enqueue after close dropped: %v", e.name, r)
		}
	}()
	e.ch <- entry
}

// QueueLen reports the current backlog for metrics.
func (e *treeEvictor[K]) QueueLen() int { return len(e.ch) }

// Close stops the worker after draining queued entries. Test/shutdown use.
func (e *treeEvictor[K]) Close() {
	e.closeOnce.Do(func() { close(e.ch) })
	<-e.done
}

// drainBatch appends immediately-available items from ch to buf, up to max
// entries, without blocking. Shared by the tree writer and the stats
// aggregation worker.
func drainBatch[T any](ch <-chan T, buf []T, max int) []T {
	for len(buf) < max {
		select {
		case v, ok := <-ch:
			if !ok {
				return buf
			}
			buf = append(buf, v)
		default:
			return buf
		}
	}
	return buf
}

func (e *treeEvictor[K]) run() {
	defer close(e.done)
	buf := make([]treeEvictionEntry[K], 0, e.batchSize)
	for entry := range e.ch {
		buf = drainBatch(e.ch, append(buf[:0], entry), e.batchSize)
		e.flush(buf)
		if pending := len(e.ch); pending > cap(e.ch)/2 {
			now := time.Now().UnixNano()
			if last := e.lastBacklogWarn.Load(); now-last >= int64(time.Second) && e.lastBacklogWarn.CompareAndSwap(last, now) {
				log.Warnf("[TREE_EVICTOR] %s backlog %d/%d", e.name, pending, cap(e.ch))
			}
		}
	}
}
