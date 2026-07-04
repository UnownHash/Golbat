package main

import (
	"runtime"

	"golbat/config"
)

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
	sem <- struct{}{}
	return func() { <-sem }
}
