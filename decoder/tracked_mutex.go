package decoder

import (
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
)

// TrackedMutex wraps sync.Mutex with contention detection and holder tracking.
// Generic over K (the entity ID type) so the fast path stores the native ID
// and string formatting (%v) is deferred to the cold contention/warning paths.
// Fast path cost: TryLock() + time.Now() + atomic store ≈ 25ns.
type TrackedMutex[K any] struct {
	mu         sync.Mutex
	holder     atomic.Value // string - caller that holds the lock
	acquiredAt atomic.Int64 // UnixNano - when lock was acquired
}

func (m *TrackedMutex[K]) loadHolder() string {
	h, _ := m.holder.Load().(string)
	return h
}

// Lock attempts to acquire the mutex. If the lock is contended, it logs the
// current holder and the waiting caller, then blocks until acquired.
// entityType and id are only used in log messages on the contention path.
func (m *TrackedMutex[K]) Lock(caller, entityType string, id K) {
	if m.mu.TryLock() {
		now := time.Now()
		m.holder.Store(caller)
		m.acquiredAt.Store(now.UnixNano())
		return
	}
	// Contention path — exponential backoff TryLock before logging.
	// Delays: 1, 2, 4, 8, 16, 32, 64, 128ms = 255ms total before warning.
	for delay := time.Millisecond; delay <= 128*time.Millisecond; delay *= 2 {
		time.Sleep(delay)
		if m.mu.TryLock() {
			now := time.Now()
			m.holder.Store(caller)
			m.acquiredAt.Store(now.UnixNano())
			return
		}
	}
	// Still contended after ~255ms — log and block.
	holder := m.loadHolder()
	start := time.Now()
	heldFor := start.Sub(time.Unix(0, m.acquiredAt.Load()))
	log.Warnf("[LOCK_CONTENTION] %s id=%v waiter=%s holder=%s held_for=%s",
		entityType, id, caller, holder, heldFor)
	m.mu.Lock()
	now := time.Now()
	log.Warnf("[LOCK_ACQUIRED] %s id=%v caller=%s waited=%s (holder was %s)",
		entityType, id, caller, now.Sub(start), holder)
	m.holder.Store(caller)
	m.acquiredAt.Store(now.UnixNano())
}

// Unlock releases the mutex. If the lock was held for more than 5 seconds, it
// logs a warning. entityType and id are only used when the threshold is exceeded.
func (m *TrackedMutex[K]) Unlock(entityType string, id K) {
	held := time.Since(time.Unix(0, m.acquiredAt.Load()))
	if held > 5*time.Second {
		log.Warnf("[LOCK_HELD_LONG] %s id=%v holder=%s held_for=%s",
			entityType, id, m.loadHolder(), held)
	}
	m.holder.Store("")
	m.mu.Unlock()
}
