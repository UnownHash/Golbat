// Package despawn answers when a spawnpoint's pokemon disappears.
package despawn

import (
	"time"

	"golbat/derive/javarandom"
)

const halfHour = 30 * time.Minute

// PositionMs returns the position, in milliseconds, of this spawnpoint's
// disappearance within any half-hour window.
//
// The game derives it by seeding java.util.Random from the spawn id and
// taking one 30-bit draw, which is what this replays. The constants are that
// derivation's own; they are not separately meaningful.
//
// Defined for ODD spawn ids, which is every real one. The model diverges for
// even ids, and so do the other implementations of it — there is no agreed
// answer there because the case does not arise.
func PositionMs(spawnID int64) int64 {
	r := javarandom.New((uint64(spawnID) << 20) * 18002419)
	v := int64(r.Int(1 << 30))
	return (((v + 34486400) % (1 << 30)) + 843176) % 1800000
}

// NextOccurrence returns the first moment strictly after now at which this
// spawnpoint's pokemon disappears.
//
// Exact for a spawnpoint whose pokemon live half an hour, and for an hour-long
// one past its midpoint; one half hour early for an hour-long one in its first
// half. Computed from now, so a later call returns the later moment.
func NextOccurrence(spawnID int64, now time.Time) time.Time {
	pos := time.Duration(PositionMs(spawnID)) * time.Millisecond

	// Start of the half-hour window containing now, in UTC.
	utc := now.UTC()
	windowStart := utc.Truncate(halfHour)

	candidate := windowStart.Add(pos)
	if !candidate.After(utc) {
		candidate = candidate.Add(halfHour)
	}
	return candidate
}
