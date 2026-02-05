package writebehind

import (
	"context"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"

	"golbat/db"
	"golbat/stats_collector"
)

// BatchWriter handles batched writes for a specific table type
type BatchWriter struct {
	mu        sync.Mutex
	entries   []*QueueEntry
	timer     *time.Timer
	batchSize int
	timeout   time.Duration
	flushFunc func(ctx context.Context, db db.DbDetails, entries []*QueueEntry) error
	db        db.DbDetails
	stats     stats_collector.StatsCollector
	tableType string
	queue     *Queue // Reference to parent queue for metrics
}

// BatchWriterConfig holds configuration for a batch writer
type BatchWriterConfig struct {
	BatchSize int
	Timeout   time.Duration
	TableType string
	FlushFunc func(ctx context.Context, db db.DbDetails, entries []*QueueEntry) error
	Db        db.DbDetails
	Stats     stats_collector.StatsCollector
	Queue     *Queue
}

// NewBatchWriter creates a new batch writer for a table type
func NewBatchWriter(cfg BatchWriterConfig) *BatchWriter {
	return &BatchWriter{
		entries:   make([]*QueueEntry, 0, cfg.BatchSize),
		batchSize: cfg.BatchSize,
		timeout:   cfg.Timeout,
		flushFunc: cfg.FlushFunc,
		db:        cfg.Db,
		stats:     cfg.Stats,
		tableType: cfg.TableType,
		queue:     cfg.Queue,
	}
}

// Add adds an entry to the batch, flushing if batch is full
func (bw *BatchWriter) Add(entry *QueueEntry) {
	bw.mu.Lock()
	defer bw.mu.Unlock()

	bw.entries = append(bw.entries, entry)

	if len(bw.entries) >= bw.batchSize {
		bw.flushLocked()
	} else if bw.timer == nil {
		// Start timeout for partial batch
		bw.timer = time.AfterFunc(bw.timeout, func() {
			bw.mu.Lock()
			defer bw.mu.Unlock()
			if len(bw.entries) > 0 {
				bw.flushLocked()
			}
		})
	}
}

// flushLocked flushes the current batch (must be called with lock held)
func (bw *BatchWriter) flushLocked() {
	if bw.timer != nil {
		bw.timer.Stop()
		bw.timer = nil
	}

	if len(bw.entries) == 0 {
		return
	}

	// Take ownership of entries slice
	entries := bw.entries
	bw.entries = make([]*QueueEntry, 0, bw.batchSize)

	// Release lock before doing I/O
	bw.mu.Unlock()

	// Execute batch write
	start := time.Now()
	ctx := context.Background()
	err := bw.flushFunc(ctx, bw.db, entries)
	batchTime := time.Since(start).Seconds()
	entryCount := len(entries)

	if err != nil {
		bw.stats.IncWriteBehindErrors(bw.tableType)
		log.Errorf("Write-behind batch error for %s (%d entries): %v", bw.tableType, entryCount, err)
	} else {
		// Increment write count by number of entries in batch
		for range entries {
			bw.stats.IncWriteBehindWrites(bw.tableType)
		}
		// Record batch metrics in Prometheus
		bw.stats.IncWriteBehindBatches(bw.tableType)
		bw.stats.ObserveWriteBehindBatchSize(bw.tableType, float64(entryCount))
		bw.stats.ObserveWriteBehindBatchTime(bw.tableType, batchTime)

		log.Debugf("Write-behind batch wrote %d %s entries in %.1fms", entryCount, bw.tableType, batchTime*1000)
	}

	// Track batch metrics on the parent queue
	bw.queue.metricsMu.Lock()
	bw.queue.batchCount++
	bw.queue.batchEntryCount += int64(entryCount)
	bw.queue.batchWriteTime += batchTime
	for _, entry := range entries {
		latency := time.Since(entry.ReadyAt).Seconds()
		bw.queue.batchLatency += latency
		bw.queue.batchLatencyCount++
		// Also record per-entry latency in Prometheus
		bw.stats.ObserveWriteBehindLatency(bw.tableType, latency)
	}
	bw.queue.metricsMu.Unlock()

	// Re-acquire lock (caller expects it held)
	bw.mu.Lock()
}

// Flush forces a flush of any pending entries
func (bw *BatchWriter) Flush() {
	bw.mu.Lock()
	defer bw.mu.Unlock()
	if len(bw.entries) > 0 {
		bw.flushLocked()
	}
}

// Size returns number of pending entries
func (bw *BatchWriter) Size() int {
	bw.mu.Lock()
	defer bw.mu.Unlock()
	return len(bw.entries)
}

// ExecuteBatchUpsert builds and executes a batch upsert using sqlx.Named
// lockFunc should lock all entities and return an unlock function
// The query should use :field placeholders matching the struct's db tags
func ExecuteBatchUpsert(
	ctx context.Context,
	dbConn *sqlx.DB,
	query string,
	entities interface{},
	lockFunc func() func(),
) error {
	// Lock all entities to read their values
	unlock := lockFunc()

	// Generate SQL and args while holding locks
	expandedQuery, args, err := sqlx.Named(query, entities)

	// Release locks - args now contains the values
	unlock()

	if err != nil {
		return err
	}

	expandedQuery = dbConn.Rebind(expandedQuery)

	// Execute without locks held
	_, err = dbConn.ExecContext(ctx, expandedQuery, args...)
	return err
}
