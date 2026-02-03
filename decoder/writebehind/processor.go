package writebehind

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	// processingInterval is how often the processor checks for work
	processingInterval = 100 * time.Millisecond

	// batchSize is the maximum number of entries to process per tick
	batchSize = 50

	// statusLogInterval is how often to log queue status
	statusLogInterval = 30 * time.Second
)

// ProcessLoop runs the main processing loop for the queue
// This should be called in a goroutine
func (q *Queue) ProcessLoop(ctx context.Context) {
	ticker := time.NewTicker(processingInterval)
	defer ticker.Stop()

	statusTicker := time.NewTicker(statusLogInterval)
	defer statusTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("Write-behind processor shutting down, flushing queue...")
			q.Flush()
			return
		case <-statusTicker.C:
			queueSize := q.Size()
			log.Infof("Write-behind queue length: %d", queueSize)
		case <-ticker.C:
			if q.checkWarmup() {
				q.processBatch(ctx)
			}
		}
	}
}

// processBatch processes a batch of entries from the queue
func (q *Queue) processBatch(ctx context.Context) {
	entries := q.getReadyEntries(batchSize)
	if len(entries) == 0 {
		return
	}

	for _, entry := range entries {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Apply rate limiting
		if !q.rateLimiter.TryAcquire(1) {
			q.stats.IncWriteBehindRateLimited(entry.Entity.WriteType())
			log.Debugf("Write-behind rate limited for %s", entry.Key)
			waitTime := q.rateLimiter.WaitAcquire(1)
			if waitTime > time.Second {
				log.Warnf("Write-behind rate limited for %s, waited %v", entry.Key, waitTime)
			}
		}

		q.writeEntry(entry)
	}
}
