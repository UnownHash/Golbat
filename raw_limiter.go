package main

import (
	"runtime"
	"time"

	log "github.com/sirupsen/logrus"

	"golbat/config"
)

// rawSlotWaitWarning is the parked-time threshold above which a raw
// processing goroutine logs how long it waited for a semaphore slot.
const rawSlotWaitWarning = time.Second

// rawProcessingSem bounds concurrent raw-proto processing goroutines
// (HTTP /raw and gRPC). nil means unlimited. Excess submissions park here
// instead of piling into entity-lock convoys during a stall; ingestion
// endpoints still respond immediately.
var rawProcessingSem chan struct{}

func initRawProcessingLimiter() {
	n := config.Config.Tuning.RawProcessingConcurrency
	switch {
	case n < 0:
		rawProcessingSem = nil
		return
	case n == 0:
		n = min(4*runtime.NumCPU(), 96)
	}
	rawProcessingSem = make(chan struct{}, n)
}

// acquireRawProcessingSlot blocks until a processing slot is free and
// returns the release func. Always call the returned func exactly once.
func acquireRawProcessingSlot() func() {
	sem := rawProcessingSem
	if sem == nil {
		return func() {}
	}
	select {
	case sem <- struct{}{}:
	default:
		// Saturated — park, and surface long waits so operators can see
		// backpressure instead of inferring it from throughput.
		start := time.Now()
		sem <- struct{}{}
		if wait := time.Since(start); wait > rawSlotWaitWarning {
			log.Warnf("[RAW_LIMITER] waited %s for a processing slot (limit %d)", wait, cap(sem))
		}
	}
	return func() { <-sem }
}
