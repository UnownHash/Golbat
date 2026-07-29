package util

import (
	"sync/atomic"
	"time"
)

// DropReporter aggregates high-frequency drop events into at most one
// report per second — per-event logging during a drop storm is log I/O
// amplifying the overload being reported.
type DropReporter struct {
	count   atomic.Int64
	lastLog atomic.Int64 // unix nanos of last report
}

// Report records one drop. When at least a second has passed since the
// last report, exactly one caller receives the accumulated count via
// report(); all others return without logging.
func (d *DropReporter) Report(report func(dropped int64)) {
	d.count.Add(1)
	now := time.Now().UnixNano()
	if last := d.lastLog.Load(); now-last >= int64(time.Second) && d.lastLog.CompareAndSwap(last, now) {
		report(d.count.Swap(0))
	}
}
