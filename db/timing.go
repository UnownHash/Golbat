package db

import (
	"strings"
	"sync/atomic"
	"time"

	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"
)

// callerTag strips per-call detail ("flush(512 entries)") from a caller
// string so the prometheus label keeps bounded cardinality.
func callerTag(caller string) string {
	if i := strings.IndexAny(caller, " ("); i > 0 {
		return caller[:i]
	}
	return caller
}

// slowQueryLogThreshold holds the duration (nanoseconds) above which Timed
// logs a [DB_SLOW] warning. <= 0 disables logging.
var slowQueryLogThreshold atomic.Int64

func init() {
	slowQueryLogThreshold.Store(int64(time.Second))
}

// SetSlowQueryLogThreshold configures the [DB_SLOW] warning threshold.
// d <= 0 disables the logging entirely.
func SetSlowQueryLogThreshold(d time.Duration) {
	slowQueryLogThreshold.Store(int64(d))
}

// Timed runs a database call and logs a [DB_SLOW] warning, tagged with the
// caller name, when it exceeds the configured threshold. See TimedOn.
func Timed(caller string, fn func() error) error {
	return TimedOn(nil, caller, fn)
}

// TimedOn is Timed with connection-pool attribution: when the call is slow,
// the warning includes the pool's state at completion plus the WaitCount/
// WaitDuration deltas across the call window, so operators can immediately
// tell pool starvation (inUse==max, idle=0, poolWait growing — connections
// are the bottleneck) from a genuinely slow database (idle available,
// poolWait ~0 — the query itself took the time). The companion of the
// TrackedMutex [LOCK_*] instrumentation: several entity loaders run while
// an entity lock is held, so a slow query here surfaces in lock-wait
// investigations under the same identifiable name. The callback's error is
// returned unchanged (sql.ErrNoRows passes through for errors.Is checks).
// Note: the wait deltas are pool-wide, so concurrent queries inflate them —
// read them as "the pool was starving during this window", not as exact
// attribution to this call.
func TimedOn(pool *sqlx.DB, caller string, fn func() error) error {
	threshold := slowQueryLogThreshold.Load()
	if threshold <= 0 {
		return fn()
	}

	var beforeWaitCount int64
	var beforeWaitDuration time.Duration
	if pool != nil {
		s := pool.Stats()
		beforeWaitCount = s.WaitCount
		beforeWaitDuration = s.WaitDuration
	}

	start := time.Now()
	err := fn()
	took := time.Since(start)
	if took <= time.Duration(threshold) {
		return err
	}

	if statsCollector != nil {
		statsCollector.IncSlowDbQuery(callerTag(caller))
	}
	if pool != nil {
		s := pool.Stats()
		log.Warnf("[DB_SLOW] %s took %s (err: %v) pool[inUse=%d idle=%d max=%d waits+%d poolWait+%s]",
			caller, took.Round(time.Millisecond), err,
			s.InUse, s.Idle, s.MaxOpenConnections,
			s.WaitCount-beforeWaitCount,
			(s.WaitDuration - beforeWaitDuration).Round(time.Millisecond))
	} else {
		log.Warnf("[DB_SLOW] %s took %s (err: %v)", caller, took.Round(time.Millisecond), err)
	}
	return err
}
