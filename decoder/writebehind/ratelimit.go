package writebehind

import (
	"sync"
	"time"
)

// TokenBucket implements a token bucket rate limiter
type TokenBucket struct {
	mu         sync.Mutex
	tokens     float64
	maxTokens  float64
	refillRate float64 // tokens per second
	lastRefill time.Time
	unlimited  bool
}

// NewTokenBucket creates a new token bucket rate limiter
// ratePerSecond is the sustained rate (tokens added per second)
// burstCapacity is the maximum burst size (bucket capacity)
// If ratePerSecond <= 0, the limiter is unlimited
func NewTokenBucket(ratePerSecond int, burstCapacity int) *TokenBucket {
	if ratePerSecond <= 0 {
		return &TokenBucket{
			unlimited: true,
		}
	}

	return &TokenBucket{
		tokens:     float64(burstCapacity), // Start with full bucket
		maxTokens:  float64(burstCapacity),
		refillRate: float64(ratePerSecond),
		lastRefill: time.Now(),
		unlimited:  false,
	}
}

// refill adds tokens based on time elapsed since last refill
func (tb *TokenBucket) refill() {
	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()
	tb.tokens += elapsed * tb.refillRate
	if tb.tokens > tb.maxTokens {
		tb.tokens = tb.maxTokens
	}
	tb.lastRefill = now
}

// TryAcquire attempts to acquire n tokens without blocking
// Returns true if tokens were acquired, false otherwise
func (tb *TokenBucket) TryAcquire(n int) bool {
	if tb.unlimited {
		return true
	}

	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill()

	if tb.tokens >= float64(n) {
		tb.tokens -= float64(n)
		return true
	}
	return false
}

// WaitAcquire blocks until n tokens are available, then acquires them
// Returns the time waited
func (tb *TokenBucket) WaitAcquire(n int) time.Duration {
	if tb.unlimited {
		return 0
	}

	start := time.Now()

	for {
		tb.mu.Lock()
		tb.refill()

		if tb.tokens >= float64(n) {
			tb.tokens -= float64(n)
			tb.mu.Unlock()
			return time.Since(start)
		}

		// Calculate time needed to get enough tokens
		deficit := float64(n) - tb.tokens
		waitTime := time.Duration(deficit / tb.refillRate * float64(time.Second))
		tb.mu.Unlock()

		// Sleep for the needed time (minimum 1ms to avoid busy loop)
		if waitTime < time.Millisecond {
			waitTime = time.Millisecond
		}
		time.Sleep(waitTime)
	}
}

// Available returns the current number of available tokens
func (tb *TokenBucket) Available() float64 {
	if tb.unlimited {
		return float64(^uint(0) >> 1) // Max float64 that fits in an int
	}

	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill()
	return tb.tokens
}

// IsUnlimited returns true if rate limiting is disabled
func (tb *TokenBucket) IsUnlimited() bool {
	return tb.unlimited
}
