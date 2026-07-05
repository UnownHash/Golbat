package db

import (
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
)

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
// caller name, when it exceeds the configured threshold. The companion of
// the TrackedMutex [LOCK_*] instrumentation: several entity loaders run
// while an entity lock is held, so a slow query here surfaces in lock-wait
// investigations under the same identifiable name. The callback's error is
// returned unchanged (sql.ErrNoRows passes through for errors.Is checks).
func Timed(caller string, fn func() error) error {
	threshold := slowQueryLogThreshold.Load()
	if threshold <= 0 {
		return fn()
	}
	start := time.Now()
	err := fn()
	if took := time.Since(start); took > time.Duration(threshold) {
		log.Warnf("[DB_SLOW] %s took %s (err: %v)", caller, took.Round(time.Millisecond), err)
	}
	return err
}
