package writebehind

import (
	"context"
	"sync"
	"time"

	"golbat/db"
	"golbat/stats_collector"

	log "github.com/sirupsen/logrus"
)

// S2CellData holds the data needed for an S2Cell write
type S2CellData struct {
	Id        uint64
	Latitude  float64
	Longitude float64
	Level     int64
	Updated   int64
}

// S2CellAccumulator collects S2Cell updates and writes them in batches
type S2CellAccumulator struct {
	mu      sync.Mutex
	pending map[uint64]*S2CellData // Dedupe by cell ID

	warmupComplete bool
	startTime      time.Time

	config QueueConfig
	db     db.DbDetails
	stats  stats_collector.StatsCollector

	// Batch write function - injected to avoid circular dependency
	batchWriter func(db.DbDetails, []*S2CellData) error
}

// NewS2CellAccumulator creates a new S2Cell accumulator
func NewS2CellAccumulator(cfg QueueConfig, dbDetails db.DbDetails, stats stats_collector.StatsCollector, batchWriter func(db.DbDetails, []*S2CellData) error) *S2CellAccumulator {
	return &S2CellAccumulator{
		pending:        make(map[uint64]*S2CellData),
		warmupComplete: false,
		startTime:      time.Now(),
		config:         cfg,
		db:             dbDetails,
		stats:          stats,
		batchWriter:    batchWriter,
	}
}

// Add adds S2Cell data to the accumulator (deduplicates by ID)
func (a *S2CellAccumulator) Add(cells []*S2CellData) {
	a.mu.Lock()
	defer a.mu.Unlock()

	for _, cell := range cells {
		if existing, ok := a.pending[cell.Id]; ok {
			// Update existing - latest timestamp wins
			if cell.Updated > existing.Updated {
				a.pending[cell.Id] = cell
			}
			a.stats.IncWriteBehindSquashed("s2cell")
		} else {
			a.pending[cell.Id] = cell
		}
	}

	a.stats.SetWriteBehindQueueDepth("s2cell", float64(len(a.pending)))
}

// Size returns the current number of pending cells
func (a *S2CellAccumulator) Size() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.pending)
}

// IsWarmupComplete returns true if the warmup period has elapsed
func (a *S2CellAccumulator) IsWarmupComplete() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.warmupComplete
}

// checkWarmup checks if warmup period has elapsed and updates state
func (a *S2CellAccumulator) checkWarmup() bool {
	if a.warmupComplete {
		return true
	}

	elapsed := time.Since(a.startTime)
	if elapsed >= time.Duration(a.config.StartupDelaySeconds)*time.Second {
		a.mu.Lock()
		if !a.warmupComplete {
			a.warmupComplete = true
			queueSize := len(a.pending)
			a.mu.Unlock()
			log.Infof("S2Cell accumulator warmup complete, processing %d queued cells", queueSize)
			return true
		}
		a.mu.Unlock()
		return true
	}
	return false
}

// Start begins the background processing loop
func (a *S2CellAccumulator) Start(ctx context.Context) {
	go a.processLoop(ctx)
}

// processLoop runs the background flush loop
func (a *S2CellAccumulator) processLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second) // Flush every 5 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			a.Flush()
			log.Info("S2Cell accumulator stopped")
			return
		case <-ticker.C:
			if a.checkWarmup() {
				a.flushBatch()
			}
		}
	}
}

// flushBatch writes pending cells
func (a *S2CellAccumulator) flushBatch() {
	a.mu.Lock()
	if len(a.pending) == 0 {
		a.mu.Unlock()
		return
	}

	// Take all pending cells
	cells := make([]*S2CellData, 0, len(a.pending))
	for _, cell := range a.pending {
		cells = append(cells, cell)
	}
	a.pending = make(map[uint64]*S2CellData)
	a.mu.Unlock()

	batchSize := len(cells)
	log.Debugf("S2Cell accumulator flushing batch of %d cells", batchSize)

	// Write batch
	start := time.Now()
	err := a.batchWriter(a.db, cells)
	latency := time.Since(start).Seconds()

	if err != nil {
		a.stats.IncWriteBehindErrors("s2cell")
		log.Errorf("S2Cell batch write error: %v", err)
	} else {
		a.stats.IncWriteBehindWrites("s2cell")
	}

	a.stats.ObserveWriteBehindLatency("s2cell", latency)
	a.stats.SetWriteBehindQueueDepth("s2cell", float64(len(a.pending)))
	a.stats.SetS2CellBatchSize(batchSize)
}

// Flush writes all pending cells immediately (used during shutdown)
func (a *S2CellAccumulator) Flush() {
	a.mu.Lock()
	if len(a.pending) == 0 {
		a.mu.Unlock()
		return
	}

	cells := make([]*S2CellData, 0, len(a.pending))
	for _, cell := range a.pending {
		cells = append(cells, cell)
	}
	a.pending = make(map[uint64]*S2CellData)
	a.mu.Unlock()

	log.Infof("S2Cell accumulator flushing %d cells", len(cells))

	// Write without rate limiting during shutdown
	err := a.batchWriter(a.db, cells)
	if err != nil {
		log.Errorf("S2Cell flush error: %v", err)
	}

	log.Info("S2Cell accumulator flush complete")
}
