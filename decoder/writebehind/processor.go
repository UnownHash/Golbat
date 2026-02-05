package writebehind

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	// dispatchInterval is how often the dispatcher checks for ready items
	dispatchInterval = 100 * time.Millisecond

	// statusLogInterval is how often to log queue status
	statusLogInterval = 30 * time.Second
)

// ProcessLoop starts the dispatcher and workers
// This should be called in a goroutine
func (q *Queue) ProcessLoop(ctx context.Context) {
	// Start worker goroutines
	for i := 0; i < q.workerCount; i++ {
		go q.worker(ctx, i)
	}

	// Run dispatcher in this goroutine
	q.dispatcher(ctx)
}

// worker reads from the work channel and routes to batch writers
func (q *Queue) worker(ctx context.Context, id int) {
	for {
		select {
		case <-ctx.Done():
			return
		case entry := <-q.workChan:
			// Route to batch writer if registered, otherwise write directly
			tableType := entry.Entity.WriteType()
			if bw := q.getBatchWriter(tableType); bw != nil {
				bw.Add(entry)
			} else {
				q.writeEntry(entry)
			}
		}
	}
}

// dispatcher moves ready entries from pending map to work channel
func (q *Queue) dispatcher(ctx context.Context) {
	ticker := time.NewTicker(dispatchInterval)
	defer ticker.Stop()

	statusTicker := time.NewTicker(statusLogInterval)
	defer statusTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("Write-behind dispatcher shutting down, flushing queue...")
			q.Flush()
			return
		case <-statusTicker.C:
			queueSize := q.Size()
			channelLen := len(q.workChan)
			metrics := q.GetAndResetMetrics()
			log.Infof("Write-behind queue: %d pending, %d in channel | single: %d entries (avg write: %.1fms, avg latency: %.1fms) | batch: %d entries in %d batches (avg write: %.1fms, avg latency: %.1fms)",
				queueSize, channelLen,
				metrics.SingleEntryCount, metrics.SingleAvgWriteMs, metrics.SingleAvgLatencyMs,
				metrics.BatchEntryCount, metrics.BatchCount, metrics.BatchAvgWriteMs, metrics.BatchAvgLatencyMs)
		case <-ticker.C:
			if q.checkWarmup() {
				q.dispatchReady()
			}
		}
	}
}

// dispatchReady moves entries that are ready (delay expired) to the work channel
func (q *Queue) dispatchReady() {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.pending) == 0 {
		return
	}

	now := time.Now()
	dispatched := 0

	for key, entry := range q.pending {
		// Check if delay has elapsed (entry is ready when now >= ReadyAt)
		if now.Before(entry.ReadyAt) {
			continue
		}

		// Try to send to work channel (non-blocking)
		select {
		case q.workChan <- entry:
			delete(q.pending, key)
			dispatched++
		default:
			// Channel full, stop dispatching this tick
			// Workers will drain it and we'll dispatch more next tick
			return
		}
	}
}
