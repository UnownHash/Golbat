package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"golbat/config"

	log "github.com/sirupsen/logrus"
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

	// Simulate a full parked queue (cap already waiting).
	rawProcessingWaiting.Store(rawQueueCap)

	if _, ok := acquireRawProcessingSlot(); ok {
		t.Fatal("expected shed when parked queue exceeds cap")
	}
	if got := rawProcessingWaiting.Load(); got != rawQueueCap {
		t.Errorf("shed must not leak the waiting counter: got %d, want %d", got, rawQueueCap)
	}

	rawProcessingWaiting.Store(0)
	release()
}

// Slow slot waits are logged once per second with a count and the longest
// wait, not once per packet.
func TestSlowSlotWaitsAggregate(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	slowSlotWaits.Reset()
	slowSlotWaitMax.Store(0)

	reportSlowSlotWait(2*time.Second, 64)
	reportSlowSlotWait(5*time.Second, 64)
	reportSlowSlotWait(3*time.Second, 64)
	if lines := strings.Count(buf.String(), "[RAW_LIMITER]"); lines != 1 {
		t.Fatalf("got %d log lines within one second, want 1:\n%s", lines, buf.String())
	}

	time.Sleep(1100 * time.Millisecond)
	buf.Reset()
	reportSlowSlotWait(4*time.Second, 64)
	got := buf.String()
	if !strings.Contains(got, "3 packets") || !strings.Contains(got, "longest 5s") {
		t.Fatalf("second window should report the 3 suppressed waits and the longest of them, got:\n%s", got)
	}
}
