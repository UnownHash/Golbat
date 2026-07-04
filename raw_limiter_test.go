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

	r1 := acquireRawProcessingSlot()
	r2 := acquireRawProcessingSlot()

	acquired := make(chan struct{})
	go func() {
		r3 := acquireRawProcessingSlot()
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
	release := acquireRawProcessingSlot()
	release() // must not panic
}
