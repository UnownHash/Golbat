package decoder

import (
	"context"

	"golbat/db"
	"golbat/util"

	"github.com/guregu/null/v6"
	log "github.com/sirupsen/logrus"
)

// despawnClearQueue carries spawnpoints whose despawn_sec was contradicted by
// a live sighting. Detection happens under the pokemon lock, so the clear is
// deferred rather than taken inline: CLAUDE.md forbids holding two entity
// locks simultaneously. Loss-tolerant - a dropped clear is retried by the
// next contradicted sighting, so a non-blocking send is correct (see the
// decode-path worker table in CLAUDE.md: this follows the drop+count shape,
// not the tree writer's must-not-lose shape) - drops are aggregated and
// reported via despawnClearDrops/IncDespawnClearDropped, per CLAUDE.md's
// requirement that loss-tolerant decode-path queues drop *and count*.
var despawnClearQueue = make(chan int64, 4096)

// despawnClearDrops aggregates queueDespawnClear's drops into at most one
// log line per second (the same pattern peerLookupDrops uses in
// peer_lookup.go), so a drop storm doesn't itself become a logging storm.
var despawnClearDrops util.DropReporter

// queueDespawnClear enqueues a spawnpoint for its despawn_sec to be cleared.
// Never blocks: the decode path holds the pokemon lock when this is called,
// and CLAUDE.md's fill-drain limit cycle warning applies here exactly as it
// does to the other decode-path workers.
func queueDespawnClear(spawnId int64) {
	select {
	case despawnClearQueue <- spawnId:
	default:
		// Dropped: the next contradicted sighting queues it again.
		despawnClearDrops.Report(func(dropped int64) {
			log.Warnf("[DESPAWN] dropped %d despawn_sec clears: queue full", dropped)
		})
		statsCollector.IncDespawnClearDropped()
	}
}

// RunDespawnCorrection retires despawn_sec values proven wrong by a live
// sighting (spec rule 2). It runs as a single dedicated worker so the clear
// can take the spawnpoint lock without the caller ever holding the pokemon
// lock at the same time.
//
// Rule 2 is applied to every despawn_sec reaching this worker, not only
// peer-written ones: a TTH-derived value can only trigger a contradiction
// under real clock skew beyond despawnSkewMargin, and the phantom-hour wrap
// clamp (applyVerifiedDespawn) already absorbs the legitimate +3600 wrap
// case, so anything reaching here is genuinely anomalous. There is
// deliberately no provenance tracking of which despawn_secs came from a
// peer: the cost of clearing a value that turns out to have been fine is
// that it is re-learned from the next TTH sighting - cheap and bounded -
// while a set of peer-sourced spawn ids would grow unboundedly with no
// natural eviction point.
func RunDespawnCorrection(ctx context.Context, dbDetails db.DbDetails) {
	for {
		select {
		case <-ctx.Done():
			return
		case spawnId := <-despawnClearQueue:
			clearContradictedDespawn(ctx, dbDetails, spawnId)
		}
	}
}

// clearContradictedDespawn locks the spawnpoint, clears despawn_sec if still
// set, and unlocks. Isolated from RunDespawnCorrection's loop so tests can
// drive it directly without a running goroutine.
func clearContradictedDespawn(ctx context.Context, dbDetails db.DbDetails, spawnId int64) {
	spawnpoint, unlock, err := getSpawnpointRecord(ctx, dbDetails, spawnId, "RunDespawnCorrection")
	if err != nil || spawnpoint == nil {
		if unlock != nil {
			unlock()
		}
		return
	}
	defer unlock()

	if spawnpoint.DespawnSec.Valid {
		spawnpoint.SetDespawnSec(null.NewInt(0, false))
		spawnpointUpdate(ctx, dbDetails, spawnpoint)
		statsCollector.IncDespawnRetired()
		log.Debugf("[DESPAWN] cleared contradicted despawn_sec for spawnpoint %d", spawnId)
	}
}
