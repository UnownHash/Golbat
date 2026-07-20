package decoder

import (
	"github.com/jmoiron/sqlx"

	golbatdb "golbat/db"
)

// timedDbQuery wraps an entity DB call with [DB_SLOW] logging including
// pool-starvation attribution (see golbat/db.TimedOn). The entity loaders
// run while the entity's lock is held, so slow queries here are prime
// suspects in [LOCK_HELD_LONG] investigations — the caller tag uses the
// loader's function name to line up with the lock instrumentation.
// (Package-local alias: loader signatures shadow the db package with a
// parameter named db.)
func timedDbQuery(caller string, pool *sqlx.DB, fn func() error) error {
	return golbatdb.TimedOn(pool, caller, fn)
}
