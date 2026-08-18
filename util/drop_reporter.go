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

// Reset discards the accumulated count and reopens the reporting window, so
// the next Report is guaranteed to deliver rather than being suppressed by a
// report some earlier caller already made this second.
//
// Its caller is tests: a package-level reporter is shared by every test in
// the binary, so a test asserting on its own log line needs the window to
// start fresh. Doing that by reassigning the package variable is a write to
// a global that production reads unsynchronised on hot paths — the same data
// race decoder/init_test.go documents for statsCollector. Both fields here
// are already atomic, so resetting through them is race-free against a
// concurrent Report, and the package variable is never written at all.
func (d *DropReporter) Reset() {
	d.count.Store(0)
	d.lastLog.Store(0)
}
