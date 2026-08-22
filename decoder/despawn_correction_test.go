package decoder

import (
	"context"
	"testing"
	"time"

	"golbat/db"

	"github.com/guregu/null/v6"
)

// A full queue must drop rather than block: queueDespawnClear runs from the
// decode path under the pokemon lock, and a blocking send here reproduces
// the fill-drain limit cycle CLAUDE.md warns about for decode-path workers.
func TestQueueDespawnClearDropsWhenQueueFull(t *testing.T) {
	old := despawnClearQueue
	t.Cleanup(func() { despawnClearQueue = old })

	despawnClearQueue = make(chan int64, 1)

	queueDespawnClear(100)
	queueDespawnClear(200) // dropped: queue already full

	if len(despawnClearQueue) != 1 {
		t.Fatalf("queue should hold its single slot, got %d", len(despawnClearQueue))
	}
	if got := <-despawnClearQueue; got != 100 {
		t.Fatalf("expected the first enqueued id to survive, got %d", got)
	}
}

// The positive case (rule 2, applied by clearContradictedDespawn): a
// spawnpoint with a known despawn_sec, contradicted by a live sighting, must
// have its despawn_sec cleared - and written through, not just mutated
// in-memory - so the next sighting re-derives it.
func TestClearContradictedDespawnClearsKnownDespawnSec(t *testing.T) {
	stubSpawnpointQueue(t)

	const spawnId = int64(920301)
	sp := &Spawnpoint{SpawnpointData: SpawnpointData{Id: spawnId, DespawnSec: null.IntFrom(1234)}}
	sp.syncDespawnFast()
	spawnpointCache.Set(spawnId, sp, time.Minute)

	clearContradictedDespawn(context.Background(), db.DbDetails{}, spawnId)

	if sp.DespawnSec.Valid {
		t.Fatalf("expected despawn_sec to be cleared, still %v", sp.DespawnSec)
	}
	if _, known, synced := sp.DespawnSecFast(); !synced || known {
		t.Fatalf("lock-free mirror must also report unknown after the clear: known=%v synced=%v", known, synced)
	}
}

// A spawnpoint with no despawn_sec (already null, or gone from the cache
// entirely) must not panic and must not write anything: SetDespawnSec's
// no-change short-circuit means clearContradictedDespawn's Valid guard keeps
// this a no-op, not a spurious write.
func TestClearContradictedDespawnNoopsWhenAlreadyNull(t *testing.T) {
	stubSpawnpointQueue(t)

	const spawnId = int64(920302)
	sp := &Spawnpoint{SpawnpointData: SpawnpointData{Id: spawnId}}
	spawnpointCache.Set(spawnId, sp, time.Minute)

	clearContradictedDespawn(context.Background(), db.DbDetails{}, spawnId)

	if sp.DespawnSec.Valid {
		t.Fatal("expected despawn_sec to remain null")
	}
}

// RunDespawnCorrection must drain the queue end to end (not just delegate to
// a helper this test never exercises) and return promptly once its context
// is cancelled - the shape shared with every other decode-path worker.
func TestRunDespawnCorrectionDrainsQueueAndStopsOnCancel(t *testing.T) {
	stubSpawnpointQueue(t)
	oldQueue := despawnClearQueue
	t.Cleanup(func() { despawnClearQueue = oldQueue })
	despawnClearQueue = make(chan int64, 8)

	const spawnId = int64(920304)
	sp := &Spawnpoint{SpawnpointData: SpawnpointData{Id: spawnId, DespawnSec: null.IntFrom(500)}}
	sp.syncDespawnFast()
	spawnpointCache.Set(spawnId, sp, time.Minute)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		RunDespawnCorrection(ctx, db.DbDetails{})
		close(done)
	}()

	despawnClearQueue <- spawnId

	// Poll for the async clear rather than sleeping a fixed duration. Read
	// through the lock-free atomic mirror, not sp.DespawnSec directly: the
	// worker goroutine mutates that field under the entity lock, so a raw
	// field read here would itself be a data race (caught by -race against
	// this test, not against production code, which never reads the field
	// unlocked).
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, known, _ := sp.DespawnSecFast(); !known {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("RunDespawnCorrection did not clear the queued spawnpoint in time")
		}
		time.Sleep(time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("RunDespawnCorrection did not return after its context was cancelled")
	}
}
