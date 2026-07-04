package decoder

import (
	"sync"

	log "github.com/sirupsen/logrus"
)

// treeEvictionEntry is one deferred R-tree removal.
type treeEvictionEntry[K comparable] struct {
	id  K
	lat float64
	lon float64
}

// treeEvictor defers R-tree removals out of cache-eviction callbacks.
// ttlcache invokes eviction callbacks synchronously while holding the
// shard's write lock for the whole expiry sweep, so per-item work there
// must be O(1). Enqueue is called from that context; a single worker
// drains the channel and flushes removals in batches so the global tree
// mutex is taken once per batch instead of once per item.
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

// Enqueue queues a removal. Blocks only if the channel is full, which
// restores (batched) backpressure rather than dropping the entry and
// leaking a ghost point in the tree.
func (e *treeEvictor[K]) Enqueue(id K, lat, lon float64) {
	defer func() {
		// Sending on a closed channel panics; Close is only used by
		// tests and shutdown, where losing the entry is acceptable.
		if r := recover(); r != nil {
			log.Debugf("[TREE_EVICTOR] %s enqueue after close dropped: %v", e.name, r)
		}
	}()
	e.ch <- treeEvictionEntry[K]{id: id, lat: lat, lon: lon}
}

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
