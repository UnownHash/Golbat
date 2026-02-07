package writebehind

import (
	"context"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// Flushable represents a queue that can be managed
type Flushable interface {
	ProcessLoop(ctx context.Context)
	Flush(ctx context.Context)
	Size() int
	BatchSize() int
	Name() string
	GetAndResetMetrics() TypedQueueMetrics
}

// QueueManager coordinates all typed queues
type QueueManager struct {
	mu     sync.RWMutex
	queues []Flushable
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	startTime            time.Time
	startupDelayComplete bool
	startupDelaySeconds  int
}

// NewQueueManager creates a new queue manager
func NewQueueManager(startupDelaySeconds int) *QueueManager {
	return &QueueManager{
		queues:              make([]Flushable, 0),
		startTime:           time.Now(),
		startupDelaySeconds: startupDelaySeconds,
	}
}

// Register adds a queue to the manager
func (m *QueueManager) Register(queue Flushable) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queues = append(m.queues, queue)
}

// Start begins processing all registered queues
func (m *QueueManager) Start(ctx context.Context) {
	m.ctx, m.cancel = context.WithCancel(ctx)

	m.mu.RLock()
	queues := m.queues
	m.mu.RUnlock()

	for _, q := range queues {
		m.wg.Add(1)
		go func(queue Flushable) {
			defer m.wg.Done()
			queue.ProcessLoop(m.ctx)
		}(q)
	}

	// Start status logging
	go m.statusLoop()

	log.Infof("Write-behind manager started with %d queues", len(queues))
}

// statusLoop periodically logs queue status
func (m *QueueManager) statusLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.mu.RLock()
			var totalPending, totalBatch int
			var totalBatchCount, totalEntryCount int64
			var totalWriteTime, totalLatency float64
			var latencyCount int64

			for _, q := range m.queues {
				totalPending += q.Size()
				totalBatch += q.BatchSize()

				metrics := q.GetAndResetMetrics()
				totalBatchCount += metrics.BatchCount
				totalEntryCount += metrics.BatchEntryCount
				if metrics.BatchCount > 0 {
					totalWriteTime += metrics.BatchAvgWriteMs * float64(metrics.BatchCount)
				}
				if metrics.BatchEntryCount > 0 {
					totalLatency += metrics.BatchAvgLatencyMs * float64(metrics.BatchEntryCount)
					latencyCount += metrics.BatchEntryCount
				}
			}
			m.mu.RUnlock()

			var avgWriteMs, avgLatencyMs float64
			if totalBatchCount > 0 {
				avgWriteMs = totalWriteTime / float64(totalBatchCount)
			}
			if latencyCount > 0 {
				avgLatencyMs = totalLatency / float64(latencyCount)
			}

			log.Infof("Write-behind: %d pending, %d in batches | %d entries in %d batches (avg write: %.1fms, avg latency: %.1fms)",
				totalPending, totalBatch, totalEntryCount, totalBatchCount, avgWriteMs, avgLatencyMs)
		}
	}
}

// Stop signals all queues to shutdown and waits for completion
func (m *QueueManager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
	log.Info("Write-behind manager stopped")
}

// Flush forces all queues to flush immediately
func (m *QueueManager) Flush() {
	m.mu.RLock()
	queues := m.queues
	m.mu.RUnlock()

	ctx := context.Background()
	for _, q := range queues {
		size := q.Size() + q.BatchSize()
		if size > 0 {
			log.Infof("Write-behind flushing %d %s entries", size, q.Name())
		}
		q.Flush(ctx)
	}
	log.Info("Write-behind flush complete")
}

// TotalSize returns the total number of pending entries across all queues
func (m *QueueManager) TotalSize() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	total := 0
	for _, q := range m.queues {
		total += q.Size() + q.BatchSize()
	}
	return total
}
