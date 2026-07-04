package main

import (
	"testing"
	"time"

	"golbat/config"
)

func TestRawLimiterBoundsConcurrency(t *testing.T) {
	old := config.Config.Tuning.RawProcessingConcurrency
	defer func() {
		config.Config.Tuning.RawProcessingConcurrency = old
		rawProcessingSem = nil
	}()

	config.Config.Tuning.RawProcessingConcurrency = 2
	initRawProcessingLimiter()

	r1, ok1 := acquireRawProcessingSlot()
	r2, ok2 := acquireRawProcessingSlot()
	if !ok1 || !ok2 {
		t.Fatal("slots within the limit must not be shed")
	}

	acquired := make(chan struct{})
	go func() {
		r3, ok := acquireRawProcessingSlot()
		if !ok {
			t.Error("third slot shed despite empty parked queue")
			close(acquired)
			return
		}
		close(acquired)
		r3()
	}()

	select {
	case <-acquired:
		t.Fatal("third slot acquired despite limit of 2")
	case <-time.After(50 * time.Millisecond):
	}

	r1()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("third slot not acquired after release")
	}
	r2()
}

func TestRawLimiterUnlimited(t *testing.T) {
	old := config.Config.Tuning.RawProcessingConcurrency
	defer func() {
		config.Config.Tuning.RawProcessingConcurrency = old
		rawProcessingSem = nil
	}()

	config.Config.Tuning.RawProcessingConcurrency = -1
	initRawProcessingLimiter()
	if rawProcessingSem != nil {
		t.Fatal("expected nil semaphore for unlimited config")
	}
	release, ok := acquireRawProcessingSlot()
	if !ok {
		t.Fatal("unlimited mode must never shed")
	}
	release() // must not panic
}

func TestRawLimiterShedsWhenParkedQueueFull(t *testing.T) {
	old := config.Config.Tuning.RawProcessingConcurrency
	defer func() {
		config.Config.Tuning.RawProcessingConcurrency = old
		rawProcessingSem = nil
		rawProcessingWaiting.Store(0)
	}()

	config.Config.Tuning.RawProcessingConcurrency = 1
	initRawProcessingLimiter()

	release, ok := acquireRawProcessingSlot()
	if !ok {
		t.Fatal("first slot must not be shed")
	}

	// Simulate a full parked queue (rawQueueFactor × limit already waiting).
	rawProcessingWaiting.Store(int64(rawQueueFactor * 1))

	if _, ok := acquireRawProcessingSlot(); ok {
		t.Fatal("expected shed when parked queue exceeds cap")
	}
	if got := rawProcessingWaiting.Load(); got != int64(rawQueueFactor) {
		t.Errorf("shed must not leak the waiting counter: got %d, want %d", got, rawQueueFactor)
	}

	rawProcessingWaiting.Store(0)
	release()
}
