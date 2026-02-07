package writebehind

import (
	"context"
)

// SharedLimiter coordinates concurrency across multiple queues
type SharedLimiter struct {
	sem chan struct{}
}

// NewSharedLimiter creates a limiter with the given max concurrent operations
func NewSharedLimiter(maxConcurrent int) *SharedLimiter {
	return &SharedLimiter{
		sem: make(chan struct{}, maxConcurrent),
	}
}

// Acquire blocks until a slot is available, respecting context cancellation
func (l *SharedLimiter) Acquire(ctx context.Context) error {
	select {
	case l.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Release frees a slot
func (l *SharedLimiter) Release() {
	<-l.sem
}

// TryAcquire attempts to acquire without blocking, returns false if unavailable
func (l *SharedLimiter) TryAcquire() bool {
	select {
	case l.sem <- struct{}{}:
		return true
	default:
		return false
	}
}
