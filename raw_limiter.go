package main

import (
	"runtime"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"

	"golbat/config"
	db2 "golbat/db"
	"golbat/util"
)

// rawSlotWaitWarning is the parked-time threshold above which a raw
// processing goroutine logs how long it waited for a semaphore slot.
const rawSlotWaitWarning = time.Second

// Limit both the number of pending requests and their aggregate payload size.
const defaultRawQueueFactor = 32
const rawQueueBytes = 64 << 20

type rawJob struct {
	run   func()
	bytes int64
	at    time.Time
}

type rawQueue struct {
	jobs  chan rawJob
	bytes atomic.Int64
}

var rawProcessingQueue *rawQueue

func newRawQueue(workers, capacity int) *rawQueue {
	q := &rawQueue{jobs: make(chan rawJob, capacity)}
	for range workers {
		go func() {
			for job := range q.jobs {
				q.bytes.Add(-job.bytes)
				if waited := time.Since(job.at); waited > rawSlotWaitWarning {
					reportSlowSlotWait(waited, workers)
				}
				job.run()
			}
		}()
	}
	return q
}

func submitRaw(size int, run func()) bool {
	q := rawProcessingQueue
	if q == nil {
		go run()
		return true
	}
	n := int64(size)
	for used := q.bytes.Load(); n <= rawQueueBytes-used; used = q.bytes.Load() {
		if !q.bytes.CompareAndSwap(used, used+n) {
			continue
		}
		select {
		case q.jobs <- rawJob{run: run, bytes: n, at: time.Now()}:
			return true
		default:
			q.bytes.Add(-n)
		}
		break
	}
	statsCollector.IncRawPacketsShed()
	rawShedDrops.Report(func(count int64) {
		log.Warnf("[RAW_LIMITER] rejected %d requests at admission (%d queued, %d queued payload bytes)", count, len(q.jobs), q.bytes.Load())
	})
	return false
}

// Shed logging is aggregated via util.DropReporter: during a sustained
// shed storm one line per dropped packet is hundreds of log lines a
// second — log I/O amplifying the overload it reports.
var rawShedDrops util.DropReporter

// Slow slot waits are aggregated the same way. slowSlotWaitMax holds the
// longest wait since the last report.
var slowSlotWaits util.DropReporter
var slowSlotWaitMax atomic.Int64

// initSlowDbQueryLogging resolves tuning.slow_db_query_ms into the db
// package's [DB_SLOW] threshold (0 = 1s default, negative = disabled).
func initSlowDbQueryLogging() {
	ms := config.Config.Tuning.SlowDbQueryMs
	switch {
	case ms == 0:
		db2.SetSlowQueryLogThreshold(time.Second)
	case ms < 0:
		db2.SetSlowQueryLogThreshold(0)
	default:
		db2.SetSlowQueryLogThreshold(time.Duration(ms) * time.Millisecond)
	}
}

func initRawProcessingLimiter() {
	n := config.Config.Tuning.RawProcessingConcurrency
	switch {
	case n < 0:
		rawProcessingQueue = nil
		return
	case n == 0:
		n = min(4*runtime.NumCPU(), 96)
	}

	factor := config.Config.Tuning.RawProcessingQueueFactor
	if factor <= 0 {
		factor = defaultRawQueueFactor
	}
	q := newRawQueue(n, factor*n)
	rawProcessingQueue = q

	// Publish the parked-queue depth every 10s so dashboards can see
	// decode backpressure building before sheds start.
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		for range ticker.C {
			// statsCollector is never nil (see its doc comment) — no guard
			// needed.
			statsCollector.SetRawProcessingWaiting(float64(len(q.jobs)))
		}
	}()
}

func reportSlowSlotWait(wait time.Duration, limit int) {
	for {
		longest := slowSlotWaitMax.Load()
		if int64(wait) <= longest || slowSlotWaitMax.CompareAndSwap(longest, int64(wait)) {
			break
		}
	}
	slowSlotWaits.Report(func(count int64) {
		longest := time.Duration(slowSlotWaitMax.Swap(0))
		log.Warnf("[RAW_LIMITER] %d packets waited over %s for a processing slot in the last second (longest %s, limit %d)",
			count, rawSlotWaitWarning, longest.Round(time.Millisecond), limit)
	})
}
