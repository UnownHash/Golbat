package writebehind

import (
	"sync"
	"time"

	"golbat/db"
	"golbat/stats_collector"

	log "github.com/sirupsen/logrus"
)

// Queue is the write-behind queue that buffers database writes
type Queue struct {
	mu      sync.Mutex
	pending map[string]*QueueEntry // key -> entry for squashing

	workChan chan *QueueEntry // buffered channel for workers

	workerCount    int
	warmupComplete bool
	startTime      time.Time

	config QueueConfig
	db     db.DbDetails
	stats  stats_collector.StatsCollector
}

// NewQueue creates a new write-behind queue
func NewQueue(cfg QueueConfig, dbDetails db.DbDetails, stats stats_collector.StatsCollector) *Queue {
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = 50 // default
	}

	return &Queue{
		pending:        make(map[string]*QueueEntry),
		workChan:       make(chan *QueueEntry, cfg.WorkerCount*10), // buffer 10x worker count
		workerCount:    cfg.WorkerCount,
		warmupComplete: false,
		startTime:      time.Now(),
		config:         cfg,
		db:             dbDetails,
		stats:          stats,
	}
}

// Enqueue adds or updates an entity write
// If an entry already exists for the same key:
// - Entity is replaced with the newer one
// - IsNewRecord is preserved if either is true (INSERT takes priority)
// - Delay is updated to the minimum of existing and new delay (0 means immediate)
// - QueuedAt is preserved (for total time tracking)
func (q *Queue) Enqueue(entity Writeable, isNewRecord bool, delay time.Duration) {
	key := entity.WriteKey()

	q.mu.Lock()
	defer q.mu.Unlock()

	if existing, ok := q.pending[key]; ok {
		// Update existing entry with newer entity
		existing.Entity = entity
		existing.UpdatedAt = time.Now()
		// Preserve INSERT status
		existing.IsNewRecord = existing.IsNewRecord || isNewRecord
		// Use minimum delay (0 means write immediately)
		if delay < existing.Delay {
			existing.Delay = delay
		}
		q.stats.IncWriteBehindSquashed(entity.WriteType())
	} else {
		// New entry
		q.pending[key] = &QueueEntry{
			Key:         key,
			Entity:      entity,
			QueuedAt:    time.Now(),
			UpdatedAt:   time.Now(),
			IsNewRecord: isNewRecord,
			Delay:       delay,
		}
	}

	q.stats.SetWriteBehindQueueDepth(entity.WriteType(), float64(len(q.pending)))
}

// Size returns the current pending queue size
func (q *Queue) Size() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}

// IsWarmupComplete returns true if the warmup period has elapsed
func (q *Queue) IsWarmupComplete() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.warmupComplete
}

// checkWarmup checks if warmup period has elapsed and updates state
func (q *Queue) checkWarmup() bool {
	if q.warmupComplete {
		return true
	}

	elapsed := time.Since(q.startTime)
	if elapsed >= time.Duration(q.config.StartupDelaySeconds)*time.Second {
		q.mu.Lock()
		if !q.warmupComplete {
			q.warmupComplete = true
			queueSize := len(q.pending)
			q.mu.Unlock()
			log.Infof("Write-behind warmup complete, processing %d queued writes with %d workers", queueSize, q.workerCount)
			return true
		}
		q.mu.Unlock()
		return true
	}
	return false
}

// Flush writes all pending entries immediately (used during shutdown)
func (q *Queue) Flush() {
	q.mu.Lock()
	entries := make([]*QueueEntry, 0, len(q.pending))
	for _, entry := range q.pending {
		entries = append(entries, entry)
	}
	q.pending = make(map[string]*QueueEntry)
	q.mu.Unlock()

	if len(entries) == 0 {
		return
	}

	log.Infof("Write-behind flushing %d entries", len(entries))

	// Write all entries directly (bypass channel during shutdown)
	for _, entry := range entries {
		q.writeEntry(entry)
	}

	log.Info("Write-behind flush complete")
}

// writeEntry performs the actual database write for an entry
func (q *Queue) writeEntry(entry *QueueEntry) {
	start := time.Now()

	err := entry.Entity.WriteToDB(q.db, entry.IsNewRecord)
	latency := time.Since(start).Seconds()

	if err != nil {
		q.stats.IncWriteBehindErrors(entry.Entity.WriteType())
		log.Errorf("Write-behind error for %s: %v", entry.Key, err)
	} else {
		q.stats.IncWriteBehindWrites(entry.Entity.WriteType())
	}

	q.stats.ObserveWriteBehindLatency(entry.Entity.WriteType(), latency)
}
