package decoder

import (
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// TrackedMutex wraps sync.Mutex with contention detection and holder tracking.
// Fast path cost: TryLock() + time.Now() + string pointer store ≈ 25ns.
type TrackedMutex struct {
	mu         sync.Mutex
	holder     string    // caller that holds the lock
	acquiredAt time.Time // when lock was acquired
	entityType string    // "Pokestop", "Gym", etc.
	entityId   string    // the entity ID
}

// Lock attempts to acquire the mutex. If the lock is contended, it logs the
// current holder and the waiting caller, then blocks until acquired.
func (m *TrackedMutex) Lock(caller string) {
	if m.mu.TryLock() {
		m.holder = caller
		m.acquiredAt = time.Now()
		return
	}
	// Contention path
	holder := m.holder
	heldFor := time.Since(m.acquiredAt)
	log.Warnf("[LOCK_CONTENTION] %s id=%s waiter=%s holder=%s held_for=%s",
		m.entityType, m.entityId, caller, holder, heldFor)
	start := time.Now()
	m.mu.Lock()
	log.Warnf("[LOCK_ACQUIRED] %s id=%s caller=%s waited=%s (holder was %s)",
		m.entityType, m.entityId, caller, time.Since(start), holder)
	m.holder = caller
	m.acquiredAt = time.Now()
}

// Unlock releases the mutex. If the lock was held for more than 5 seconds, it
// logs a warning.
func (m *TrackedMutex) Unlock() {
	held := time.Since(m.acquiredAt)
	if held > 5*time.Second {
		log.Warnf("[LOCK_HELD_LONG] %s id=%s holder=%s held_for=%s",
			m.entityType, m.entityId, m.holder, held)
	}
	m.holder = ""
	m.mu.Unlock()
}
