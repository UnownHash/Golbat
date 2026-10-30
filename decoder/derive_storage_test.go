package decoder

import (
	"context"
	"testing"
	"time"

	"golbat/db"
	"golbat/pogo"

	"github.com/guregu/null/v6"
)

// A cached spawnpoint reached by a wild sighting must be maintained, right
// down to the TTH despawn_sec write. Proof by panic: the fixture is seeded
// with LastSeen left at its zero value, so LastSeenFast() == 0 and the
// lock-free fast path's "last > 0" guard is false — that path can never take
// its early return here, so it always falls through to the locked path
// below. A cached-but-never-loaded spawnpoint has newRecord == false, so
// getOrCreateSpawnpointRecord does not touch the database either; the write
// lands in spawnpointUpdate, and with no write-behind queue running in this
// test binary (spawnpointQueue is nil unless InitWriteBehindQueue was
// called, which no test in this package does) that falls to the direct
// spawnpointWriteDB path, which calls db.DbDetails{}'s nil *sqlx.DB and
// panics.
func TestCachedSpawnpointReachesTheDespawnWrite(t *testing.T) {
	// This test's proof structure depends on the direct-write path being
	// taken. If a write-behind queue exists, spawnpointUpdate enqueues
	// instead of writing, no panic occurs, and this test would silently
	// stop proving anything.
	if spawnpointQueue != nil {
		t.Fatal("a write-behind queue was initialised; this test's proof-by-panic no longer holds")
	}

	const spawnHex = "47b973e0595"
	const spawnID = int64(0x47b973e0595)
	sp := &Spawnpoint{SpawnpointData: SpawnpointData{Id: spawnID}}
	sp.syncFastFields()
	spawnpointCache.Set(spawnID, sp, 0)
	t.Cleanup(func() { spawnpointCache.Delete(spawnID) })

	wild := &pogo.WildPokemonProto{
		SpawnPointId:     spawnHex,
		TimeTillHiddenMs: 30000,
		Latitude:         51.5,
		Longitude:        -0.1,
	}

	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		spawnpointUpdateFromWild(context.Background(), db.DbDetails{}, wild, 1786955853000)
	}()

	if !panicked {
		t.Fatal("cached spawnpoint did not reach the database path")
	}
}

// TestSetExpireTimestampFromSpawnpointReadsTheRecord covers the record-read
// path in setExpireTimestampFromSpawnpoint, taken when the lock-free cache
// check cannot answer: a stored despawn_sec is read and used. Without it, a known 60-minute spawnpoint would silently
// fall through to applyDerivedDespawn and get an expiry half an hour early.
//
// Coverage limit, stated plainly: the block's headline case is a cache MISS
// followed by a database read, and this package has no database in tests
// (db.DbDetails{} wraps a nil *sqlx.DB and there is no sql mock dependency).
// This drives the block's other entry instead — an entry that is cached but
// not yet SYNCED, the "mid-load" case the block's own comment names, which a
// second goroutine really does observe while getOrCreateSpawnpointRecord is
// between GetOrSetFunc and syncFastFields. It runs the same statements:
// getSpawnpointRecord, spawnPoint.DespawnSec.Valid, applyVerifiedDespawn.
// The database call itself is exercised by no test.
func TestSetExpireTimestampFromSpawnpointReadsTheRecord(t *testing.T) {
	const spawnID = int64(0x47b973e0596)
	const despawnSec = 1500

	// A 12:00:00 UTC wall clock, so secondOfHour (unix % 3600) is 0 and the
	// stored despawn is exactly despawnSec seconds away.
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	wantSec := now.Unix() + despawnSec

	// Cached but NOT synced: syncFastFields is deliberately not called, so
	// DespawnSecFast reports synced=false and the lock-free fast path above
	// the block declines to answer, exactly as it does mid-load.
	sp := &Spawnpoint{SpawnpointData: SpawnpointData{Id: spawnID, DespawnSec: null.IntFrom(despawnSec)}}
	spawnpointCache.Set(spawnID, sp, 0)
	t.Cleanup(func() { spawnpointCache.Delete(spawnID) })
	if _, _, synced := sp.DespawnSecFast(); synced {
		t.Fatal("fixture broke: the entry must be unsynced, or the fast path answers before the block runs")
	}

	p := &Pokemon{}
	p.SetSpawnId(null.IntFrom(spawnID))
	p.setExpireTimestampFromSpawnpoint(context.Background(), db.DbDetails{}, now.UnixMilli(), true)

	if got := int64(p.ExpireTimestamp.ValueOrZero()); got != wantSec {
		t.Fatalf("expiry = %d (%+ds), want %d (%+ds)",
			got, got-now.Unix(), wantSec, wantSec-now.Unix())
	}
	if !p.ExpireTimestampVerified {
		t.Error("expiry not verified")
	}
}
