package writebehind

import (
	"context"
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

	workChan chan *QueueEntry // buffered channel for dispatcher

	// Batch writers per table type
	batchWriters   map[string]*BatchWriter
	batchWritersMu sync.RWMutex

	workerCount    int
	warmupComplete bool
	startTime      time.Time

	// Metrics tracking (protected by metricsMu)
	metricsMu sync.Mutex
	// Single write metrics
	singleWriteCount   int64   // number of single entries written
	singleWriteTime    float64 // sum of single write durations in seconds
	singleLatency      float64 // sum of single write latencies in seconds
	singleLatencyCount int64   // number of single latency samples
	// Batch write metrics
	batchCount        int64   // number of batches executed
	batchEntryCount   int64   // total entries written via batches
	batchWriteTime    float64 // sum of batch execution times in seconds
	batchLatency      float64 // sum of batch entry latencies in seconds
	batchLatencyCount int64   // number of batch latency samples

	config QueueConfig
	db     db.DbDetails
	stats  stats_collector.StatsCollector
}

// NewQueue creates a new write-behind queue
func NewQueue(cfg QueueConfig, dbDetails db.DbDetails, stats stats_collector.StatsCollector) *Queue {
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = 50 // default
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 50 // default
	}
	if cfg.BatchTimeout <= 0 {
		cfg.BatchTimeout = 100 * time.Millisecond // default
	}

	return &Queue{
		pending:        make(map[string]*QueueEntry),
		workChan:       make(chan *QueueEntry, cfg.WorkerCount*10), // buffer 10x worker count
		batchWriters:   make(map[string]*BatchWriter),
		workerCount:    cfg.WorkerCount,
		warmupComplete: false,
		startTime:      time.Now(),
		config:         cfg,
		db:             dbDetails,
		stats:          stats,
	}
}

// RegisterBatchWriter registers a batch writer for a specific table type
func (q *Queue) RegisterBatchWriter(tableType string, flushFunc func(ctx context.Context, db db.DbDetails, entries []*QueueEntry) error) {
	q.batchWritersMu.Lock()
	defer q.batchWritersMu.Unlock()

	q.batchWriters[tableType] = NewBatchWriter(BatchWriterConfig{
		BatchSize: q.config.BatchSize,
		Timeout:   q.config.BatchTimeout,
		TableType: tableType,
		FlushFunc: flushFunc,
		Db:        q.db,
		Stats:     q.stats,
		Queue:     q, // Pass queue reference for metrics
	})
}

// getBatchWriter returns the batch writer for a table type, or nil if not registered
func (q *Queue) getBatchWriter(tableType string) *BatchWriter {
	q.batchWritersMu.RLock()
	defer q.batchWritersMu.RUnlock()
	return q.batchWriters[tableType]
}

// Enqueue adds or updates an entity write
// If an entry already exists for the same key:
// - Entity is replaced with the newer one
// - IsNewRecord is preserved if either is true (INSERT takes priority)
// - Delay is updated to the minimum of existing and new delay (0 means immediate)
// - QueuedAt is preserved (for total time tracking)
// - ReadyAt is updated if the new delay makes the entry ready earlier
func (q *Queue) Enqueue(entity Writeable, isNewRecord bool, delay time.Duration) {
	key := entity.WriteKey()

	q.mu.Lock()
	defer q.mu.Unlock()

	now := time.Now()

	if existing, ok := q.pending[key]; ok {
		// Update existing entry with newer entity
		existing.Entity = entity
		existing.UpdatedAt = now
		// Preserve INSERT status
		existing.IsNewRecord = existing.IsNewRecord || isNewRecord
		// Use minimum delay (0 means write immediately)
		if delay < existing.Delay {
			existing.Delay = delay
			// Update ReadyAt if this squash makes it ready earlier
			newReadyAt := now.Add(delay)
			if newReadyAt.Before(existing.ReadyAt) {
				existing.ReadyAt = newReadyAt
			}
		}
		q.stats.IncWriteBehindSquashed(entity.WriteType())
	} else {
		// New entry - ReadyAt is when the entry becomes eligible for dispatch
		readyAt := now.Add(delay)
		q.pending[key] = &QueueEntry{
			Key:         key,
			Entity:      entity,
			QueuedAt:    now,
			UpdatedAt:   now,
			ReadyAt:     readyAt,
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

// WriteMetrics holds the metrics returned by GetAndResetMetrics
type WriteMetrics struct {
	// Single write metrics
	SingleEntryCount   int64   // number of entries written via single writes
	SingleAvgWriteMs   float64 // average write time per single entry in milliseconds
	SingleAvgLatencyMs float64 // average latency per single entry in milliseconds
	// Batch write metrics
	BatchCount        int64   // number of batches executed
	BatchEntryCount   int64   // total entries written via batches
	BatchAvgWriteMs   float64 // average time per batch in milliseconds
	BatchAvgLatencyMs float64 // average latency per batch entry in milliseconds
}

// GetAndResetMetrics returns write metrics then resets counters
func (q *Queue) GetAndResetMetrics() WriteMetrics {
	q.metricsMu.Lock()
	defer q.metricsMu.Unlock()

	var singleAvgWrite, singleAvgLatency float64
	var batchAvgWrite, batchAvgLatency float64

	if q.singleWriteCount > 0 {
		singleAvgWrite = (q.singleWriteTime / float64(q.singleWriteCount)) * 1000
	}
	if q.singleLatencyCount > 0 {
		singleAvgLatency = (q.singleLatency / float64(q.singleLatencyCount)) * 1000
	}
	if q.batchCount > 0 {
		batchAvgWrite = (q.batchWriteTime / float64(q.batchCount)) * 1000
	}
	if q.batchLatencyCount > 0 {
		batchAvgLatency = (q.batchLatency / float64(q.batchLatencyCount)) * 1000
	}

	metrics := WriteMetrics{
		SingleEntryCount:   q.singleWriteCount,
		SingleAvgWriteMs:   singleAvgWrite,
		SingleAvgLatencyMs: singleAvgLatency,
		BatchCount:         q.batchCount,
		BatchEntryCount:    q.batchEntryCount,
		BatchAvgWriteMs:    batchAvgWrite,
		BatchAvgLatencyMs:  batchAvgLatency,
	}

	// Reset counters
	q.singleWriteCount = 0
	q.singleWriteTime = 0
	q.singleLatency = 0
	q.singleLatencyCount = 0
	q.batchCount = 0
	q.batchEntryCount = 0
	q.batchWriteTime = 0
	q.batchLatency = 0
	q.batchLatencyCount = 0

	return metrics
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
		log.Info("Write-behind flush: no pending entries")
	} else {
		log.Infof("Write-behind flushing %d pending entries", len(entries))

		// Route entries to batch writers or write directly
		for _, entry := range entries {
			tableType := entry.Entity.WriteType()
			if bw := q.getBatchWriter(tableType); bw != nil {
				bw.Add(entry)
			} else {
				q.writeEntry(entry)
			}
		}
	}

	// Flush all batch writers
	q.batchWritersMu.RLock()
	for tableType, bw := range q.batchWriters {
		size := bw.Size()
		if size > 0 {
			log.Infof("Write-behind flushing %d %s batch entries", size, tableType)
		}
		bw.Flush()
	}
	q.batchWritersMu.RUnlock()

	log.Info("Write-behind flush complete")
}

// writeEntry performs the actual database write for an entry (single write path)
func (q *Queue) writeEntry(entry *QueueEntry) {
	start := time.Now()

	err := entry.Entity.WriteToDB(q.db, entry.IsNewRecord)
	writeTime := time.Since(start).Seconds()

	entityType := entry.Entity.WriteType()
	if err != nil {
		q.stats.IncWriteBehindErrors(entityType)
		log.Errorf("Write-behind error for %s: %v", entry.Key, err)
	} else {
		q.stats.IncWriteBehindWrites(entityType)
	}

	// Latency is from when entry became ready (ReadyAt) to write completion
	latency := time.Since(entry.ReadyAt).Seconds()
	q.stats.ObserveWriteBehindLatency(entityType, latency)

	// Track single write metrics for status logging
	q.metricsMu.Lock()
	q.singleWriteCount++
	q.singleWriteTime += writeTime
	q.singleLatency += latency
	q.singleLatencyCount++
	q.metricsMu.Unlock()
}
