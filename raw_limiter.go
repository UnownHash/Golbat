package main

import (
	"runtime"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"

	"golbat/config"
)

// rawSlotWaitWarning is the parked-time threshold above which a raw
// processing goroutine logs how long it waited for a semaphore slot.
const rawSlotWaitWarning = time.Second

// rawQueueFactor bounds how many goroutines may park waiting for a slot
// (rawQueueFactor × the concurrency limit). Beyond that the packet is shed:
// the ingest endpoints have already replied success, so each parked
// goroutine pins its decoded payload in memory — during a sustained stall
// an unbounded queue turns into an OOM.
const rawQueueFactor = 8

// rawProcessingSem bounds concurrent raw-proto processing goroutines
// (HTTP /raw and gRPC). nil means unlimited. Excess submissions park here
// instead of piling into entity-lock convoys during a stall; ingestion
// endpoints still respond immediately.
var rawProcessingSem chan struct{}

// rawProcessingWaiting counts goroutines parked on rawProcessingSem.
var rawProcessingWaiting atomic.Int64

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
// returns (release, true); the caller must call release exactly once. If
// the parked queue already exceeds rawQueueFactor× the limit it returns
// (nil, false) and the caller must drop the packet.
func acquireRawProcessingSlot() (func(), bool) {
	sem := rawProcessingSem
	if sem == nil {
		return func() {}, true
	}
	select {
	case sem <- struct{}{}:
	default:
		// Saturated — park (bounded), and surface long waits so operators
		// can see backpressure instead of inferring it from throughput.
		if waiting := rawProcessingWaiting.Add(1); waiting > int64(rawQueueFactor*cap(sem)) {
			rawProcessingWaiting.Add(-1)
			log.Warnf("[RAW_LIMITER] shedding packet: %d goroutines already waiting for %d slots", waiting-1, cap(sem))
			return nil, false
		}
		start := time.Now()
		sem <- struct{}{}
		rawProcessingWaiting.Add(-1)
		if wait := time.Since(start); wait > rawSlotWaitWarning {
			log.Warnf("[RAW_LIMITER] waited %s for a processing slot (limit %d)", wait, cap(sem))
		}
	}
	return func() { <-sem }, true
}
