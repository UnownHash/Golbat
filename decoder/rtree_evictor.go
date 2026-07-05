package decoder

import (
	"sync"

	log "github.com/sirupsen/logrus"
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
// snapshot refresh remain as lock parties. Blocks only if the channel is
// full (bounded backpressure) rather than dropping entries and leaking
// ghost/missing points in the tree.
func (e *treeEvictor[K]) Enqueue(id K, lat, lon float64) {
	e.enqueue(treeEvictionEntry[K]{op: treeOpDelete, id: id, lat: lat, lon: lon})
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

func (e *treeEvictor[K]) run() {
	defer close(e.done)
	buf := make([]treeEvictionEntry[K], 0, e.batchSize)
	for entry := range e.ch {
		buf = append(buf[:0], entry)
	drain:
		for len(buf) < e.batchSize {
			select {
			case next, ok := <-e.ch:
				if !ok {
					break drain
				}
				buf = append(buf, next)
			default:
				break drain
			}
		}
		e.flush(buf)
		if pending := len(e.ch); pending > cap(e.ch)/2 {
			log.Warnf("[TREE_EVICTOR] %s backlog %d/%d", e.name, pending, cap(e.ch))
		}
	}
}
