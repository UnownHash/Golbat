package writebehind

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
	log "github.com/sirupsen/logrus"

	"golbat/db"
	"golbat/stats_collector"
)

const (
	mysqlDeadlock   = 1213
	deadlockRetries = 3
)

// Entry represents a pending write in a typed queue
type Entry[K cmp.Ordered, T any] struct {
	Key         K
	Data        T
	QueuedAt    time.Time
	UpdatedAt   time.Time
	ReadyAt     time.Time     // When the entry becomes eligible for dispatch
	IsNewRecord bool          // Track if this needs INSERT (preserved across updates)
	Delay       time.Duration // Minimum delay before writing (0 = immediate)
}

// TypedQueueConfig holds configuration for a typed queue
type TypedQueueConfig[K cmp.Ordered, T any] struct {
	Name                string
	BatchSize           int
	BatchTimeout        time.Duration
	StartupDelaySeconds int
	Limiter             *SharedLimiter
	Db                  db.DbDetails
	Stats               stats_collector.StatsCollector
	// FlushFunc is called to write a batch of entries to the database
	FlushFunc func(ctx context.Context, db db.DbDetails, entries []T) error
	// KeyFunc extracts the unique key from an entry's data
	KeyFunc func(data T) K
}

// TypedQueue is a type-safe write-behind queue for a specific entity type
type TypedQueue[K cmp.Ordered, T any] struct {
	mu      sync.Mutex
	pending map[K]*Entry[K, T] // key -> entry for squashing

	// Batch accumulation
	batchMu      sync.Mutex
	batchPending map[K]*Entry[K, T]
	batchTimer   *time.Timer

	name         string
	batchSize    int
	batchTimeout time.Duration
	limiter      *SharedLimiter
	flushFunc    func(ctx context.Context, db db.DbDetails, entries []T) error
	keyFunc      func(data T) K
	db           db.DbDetails
	stats        stats_collector.StatsCollector

	// Warmup tracking
	warmupComplete bool
	startTime      time.Time
	startupDelay   time.Duration

	// Metrics (protected by metricsMu)
	metricsMu         sync.Mutex
	batchCount        int64
	batchEntryCount   int64
	batchWriteTime    float64
	batchLatency      float64
	batchLatencyCount int64
}

// NewTypedQueue creates a new type-safe write-behind queue
func NewTypedQueue[K cmp.Ordered, T any](cfg TypedQueueConfig[K, T]) *TypedQueue[K, T] {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 50
	}
	if cfg.BatchTimeout <= 0 {
		cfg.BatchTimeout = 100 * time.Millisecond
	}

	return &TypedQueue[K, T]{
		pending:        make(map[K]*Entry[K, T]),
		batchPending:   make(map[K]*Entry[K, T]),
		name:           cfg.Name,
		batchSize:      cfg.BatchSize,
		batchTimeout:   cfg.BatchTimeout,
		limiter:        cfg.Limiter,
		flushFunc:      cfg.FlushFunc,
		keyFunc:        cfg.KeyFunc,
		db:             cfg.Db,
		stats:          cfg.Stats,
		warmupComplete: false,
		startTime:      time.Now(),
		startupDelay:   time.Duration(cfg.StartupDelaySeconds) * time.Second,
	}
}

// Enqueue adds or updates an entry in the queue
// Takes data snapshot directly - caller is responsible for calling while holding entity lock
func (q *TypedQueue[K, T]) Enqueue(data T, isNewRecord bool, delay time.Duration) {
	key := q.keyFunc(data)

	q.mu.Lock()
	defer q.mu.Unlock()

	now := time.Now()

	if existing, ok := q.pending[key]; ok {
		// Update existing entry with newer data
		existing.Data = data
		existing.UpdatedAt = now
		// Preserve INSERT status
		existing.IsNewRecord = existing.IsNewRecord || isNewRecord
		// Use minimum delay
		if delay < existing.Delay {
			existing.Delay = delay
			newReadyAt := now.Add(delay)
			if newReadyAt.Before(existing.ReadyAt) {
				existing.ReadyAt = newReadyAt
			}
		}
		q.stats.IncWriteBehindSquashed(q.name)
	} else {
		readyAt := now.Add(delay)
		q.pending[key] = &Entry[K, T]{
			Key:         key,
			Data:        data,
			QueuedAt:    now,
			UpdatedAt:   now,
			ReadyAt:     readyAt,
			IsNewRecord: isNewRecord,
			Delay:       delay,
		}
	}

	q.stats.SetWriteBehindQueueDepth(q.name, float64(len(q.pending)))
}

// Size returns the current pending queue size
func (q *TypedQueue[K, T]) Size() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}

// BatchSize returns the current batch accumulation size
func (q *TypedQueue[K, T]) BatchSize() int {
	q.batchMu.Lock()
	defer q.batchMu.Unlock()
	return len(q.batchPending)
}

// IsWarmupComplete returns true if the warmup period has elapsed
func (q *TypedQueue[K, T]) IsWarmupComplete() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.warmupComplete
}

// checkWarmup checks if warmup period has elapsed and updates state
func (q *TypedQueue[K, T]) checkWarmup() bool {
	if q.warmupComplete {
		return true
	}

	elapsed := time.Since(q.startTime)
	if elapsed >= q.startupDelay {
		q.mu.Lock()
		if !q.warmupComplete {
			q.warmupComplete = true
			queueSize := len(q.pending)
			q.mu.Unlock()
			log.Infof("Write-behind [%s] warmup complete, processing %d queued writes", q.name, queueSize)
			return true
		}
		q.mu.Unlock()
		return true
	}
	return false
}

// ProcessLoop starts the dispatch loop for this queue
// Should be called in a goroutine
func (q *TypedQueue[K, T]) ProcessLoop(ctx context.Context) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Infof("Write-behind [%s] shutting down, flushing...", q.name)
			q.Flush(context.Background())
			return
		case <-ticker.C:
			if q.checkWarmup() {
				q.dispatchReady(ctx)
			}
		}
	}
}

// dispatchReady moves entries that are ready to the batch accumulator
func (q *TypedQueue[K, T]) dispatchReady(ctx context.Context) {
	q.mu.Lock()

	if len(q.pending) == 0 {
		q.mu.Unlock()
		return
	}

	now := time.Now()
	toDispatch := make([]*Entry[K, T], 0)

	for key, entry := range q.pending {
		if now.Before(entry.ReadyAt) {
			continue
		}
		toDispatch = append(toDispatch, entry)
		delete(q.pending, key)
	}
	q.mu.Unlock()

	// Add to batch accumulator
	for _, entry := range toDispatch {
		q.addToBatch(ctx, entry)
	}
}

// addToBatch adds an entry to the batch accumulator, flushing if batch is full
func (q *TypedQueue[K, T]) addToBatch(ctx context.Context, entry *Entry[K, T]) {
	q.batchMu.Lock()
	defer q.batchMu.Unlock()

	// Deduplicate within batch (squash)
	if existing, ok := q.batchPending[entry.Key]; ok {
		entry.IsNewRecord = entry.IsNewRecord || existing.IsNewRecord
		if existing.QueuedAt.Before(entry.QueuedAt) {
			entry.QueuedAt = existing.QueuedAt
		}
		if existing.ReadyAt.Before(entry.ReadyAt) {
			entry.ReadyAt = existing.ReadyAt
		}
		q.stats.IncWriteBehindSquashed(q.name)
	}
	q.batchPending[entry.Key] = entry

	if len(q.batchPending) >= q.batchSize {
		q.flushBatchLocked(ctx)
	} else if q.batchTimer == nil {
		q.batchTimer = time.AfterFunc(q.batchTimeout, func() {
			q.batchMu.Lock()
			defer q.batchMu.Unlock()
			if len(q.batchPending) > 0 {
				q.flushBatchLocked(context.Background())
			}
		})
	}
}

// flushBatchLocked flushes the current batch (must be called with batchMu held)
func (q *TypedQueue[K, T]) flushBatchLocked(ctx context.Context) {
	if q.batchTimer != nil {
		q.batchTimer.Stop()
		q.batchTimer = nil
	}

	if len(q.batchPending) == 0 {
		return
	}

	// Convert map to slice (order doesn't matter for upserts)
	entries := make([]*Entry[K, T], 0, len(q.batchPending))
	for _, entry := range q.batchPending {
		entries = append(entries, entry)
	}
	q.batchPending = make(map[K]*Entry[K, T])

	// Release batch lock before doing I/O
	q.batchMu.Unlock()

	// Acquire concurrency slot from shared limiter
	if q.limiter != nil {
		if err := q.limiter.Acquire(ctx); err != nil {
			// Context cancelled, re-queue entries
			q.batchMu.Lock()
			for _, entry := range entries {
				q.batchPending[entry.Key] = entry
			}
			return
		}
		defer q.limiter.Release()
	}

	// Sort entries by key to ensure consistent lock ordering and avoid deadlocks
	slices.SortFunc(entries, func(a, b *Entry[K, T]) int {
		return cmp.Compare(a.Key, b.Key)
	})

	// Execute batch write
	start := time.Now()
	data := make([]T, len(entries))
	for i, entry := range entries {
		data[i] = entry.Data
	}

	var err error
	for attempt := 0; attempt <= deadlockRetries; attempt++ {
		err = q.flushFunc(ctx, q.db, data)
		if err == nil {
			break
		}
		if mysqlErr, ok := err.(*mysql.MySQLError); ok && mysqlErr.Number == mysqlDeadlock && attempt < deadlockRetries {
			log.Warnf("Write-behind [%s] deadlock on attempt %d/%d (%d entries), retrying...", q.name, attempt+1, deadlockRetries, len(entries))
			time.Sleep(time.Duration(50*(attempt+1)) * time.Millisecond)
			continue
		}
		break
	}
	batchTime := time.Since(start).Seconds()
	entryCount := len(entries)

	if err != nil {
		q.stats.IncWriteBehindErrors(q.name)
		log.Errorf("Write-behind [%s] batch error (%d entries): %v", q.name, entryCount, err)
	} else {
		for range entries {
			q.stats.IncWriteBehindWrites(q.name)
		}
		q.stats.IncWriteBehindBatches(q.name)
		q.stats.ObserveWriteBehindBatchSize(q.name, float64(entryCount))
		q.stats.ObserveWriteBehindBatchTime(q.name, batchTime)
	}

	// Track metrics
	q.metricsMu.Lock()
	q.batchCount++
	q.batchEntryCount += int64(entryCount)
	q.batchWriteTime += batchTime
	for _, entry := range entries {
		latency := time.Since(entry.ReadyAt).Seconds()
		q.batchLatency += latency
		q.batchLatencyCount++
		q.stats.ObserveWriteBehindLatency(q.name, latency)
	}
	q.metricsMu.Unlock()

	// Re-acquire lock (caller expects it held)
	q.batchMu.Lock()
}

// Flush writes all pending entries immediately
func (q *TypedQueue[K, T]) Flush(ctx context.Context) {
	// Move all pending to batch
	q.mu.Lock()
	entries := make([]*Entry[K, T], 0, len(q.pending))
	for _, entry := range q.pending {
		entries = append(entries, entry)
	}
	q.pending = make(map[K]*Entry[K, T])
	q.mu.Unlock()

	// Add all to batch
	for _, entry := range entries {
		q.addToBatch(ctx, entry)
	}

	// Force flush the batch
	q.batchMu.Lock()
	if len(q.batchPending) > 0 {
		q.flushBatchLocked(ctx)
	}
	q.batchMu.Unlock()
}

// TypedQueueMetrics holds the metrics for a typed queue
type TypedQueueMetrics struct {
	BatchCount        int64
	BatchEntryCount   int64
	BatchAvgWriteMs   float64
	BatchAvgLatencyMs float64
}

// GetAndResetMetrics returns metrics then resets counters
func (q *TypedQueue[K, T]) GetAndResetMetrics() TypedQueueMetrics {
	q.metricsMu.Lock()
	defer q.metricsMu.Unlock()

	var batchAvgWrite, batchAvgLatency float64
	if q.batchCount > 0 {
		batchAvgWrite = (q.batchWriteTime / float64(q.batchCount)) * 1000
	}
	if q.batchLatencyCount > 0 {
		batchAvgLatency = (q.batchLatency / float64(q.batchLatencyCount)) * 1000
	}

	metrics := TypedQueueMetrics{
		BatchCount:        q.batchCount,
		BatchEntryCount:   q.batchEntryCount,
		BatchAvgWriteMs:   batchAvgWrite,
		BatchAvgLatencyMs: batchAvgLatency,
	}

	// Reset
	q.batchCount = 0
	q.batchEntryCount = 0
	q.batchWriteTime = 0
	q.batchLatency = 0
	q.batchLatencyCount = 0

	return metrics
}

// Name returns the queue name
func (q *TypedQueue[K, T]) Name() string {
	return q.name
}
