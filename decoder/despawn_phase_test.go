package decoder

import (
	"context"
	"strings"
	"testing"
	"time"

	"golbat/db"
	"golbat/derive/despawn"
	"golbat/pogo"
)

// phaseTestSpawnID and phaseTestPhaseSec are this file's fixture. The phase
// is HARDCODED, not read back out of despawn.PositionMs and never computed
// via despawnPhaseDiverged itself: 1784988 ms is the golden vector
// derive/despawn's own TestPositionMsBoundaryVector pins for this id, and 1784
// is that truncated to seconds, which is what despawnPhaseDiverged compares
// against.
//
// An expectation computed by calling the function under test cannot fail:
// searching for a "divergent" timestamp by calling despawnPhaseDiverged in a
// loop still passes when it is mutated to return a constant. Hardcoding both
// constants is what puts the exact-equality and local/UTC failures within
// reach of these tests.
const (
	phaseTestSpawnID  = int64(0x47b973e0595)
	phaseTestPhaseSec = int64(1784)
)

// phaseTestExpiryAt returns an absolute unix second whose UTC-anchored phase
// (unix mod 1800) is exactly phase. The unix epoch is aligned to the half
// hour, so a multiple of 1800 plus the phase is such an instant by
// construction — the same alignment despawn.NextOccurrence relies on when it
// truncates to the UTC half hour.
func phaseTestExpiryAt(phase int64) int64 {
	const someHalfHour = int64(1755000000) / 1800 * 1800
	return someHalfHour + phase
}

// The derived phase itself must not read as divergence, and the counter must
// tolerate the same ±2s despawnSecUnchanged does on this very quantity —
// PositionMs truncates sub-second phase away and the observed side truncates
// again, so a perfectly agreeing sighting can land a second off. Exact
// equality would give the counter a permanent baseline of ordinary jitter.
func TestDespawnPhaseDivergedToleratesWobble(t *testing.T) {
	for _, c := range []struct {
		offset int64
		want   bool
	}{
		{0, false},
		{1, false},
		{-1, false},
		{2, false},
		{-2, false},
		{3, true},
		{-3, true},
	} {
		expiry := phaseTestExpiryAt(phaseTestPhaseSec + c.offset)
		if got := despawnPhaseDiverged(phaseTestSpawnID, expiry); got != c.want {
			t.Errorf("offset %+ds from the derived phase: diverged=%v want %v", c.offset, got, c.want)
		}
	}
}

// The tolerance wraps at the half-hour boundary, as despawnSecUnchanged
// wraps at the hour: phase 1799 and phase 0 are one second apart, not 1799.
// Without the wrap, a spawnpoint whose derived phase sits within 2s of the
// boundary would report divergence on roughly half its sightings forever —
// and phaseTestSpawnID (phase 1784) cannot show that, because the mod-1800
// reduction alone keeps every case within tolerance for it. This needs a
// spawnpoint whose phase actually straddles the boundary.
func TestDespawnPhaseDivergedWrapsAtTheHalfHourBoundary(t *testing.T) {
	// Chosen by search for a phase of 1799 — one second before the boundary.
	// Odd, because every real spawn id is and the derivation is defined only
	// for those. Its position sits in the final second of the half hour,
	// which is what this test needs.
	const boundarySpawnID = int64(0x47b0000bb3)

	// Fixture guard, not the assertion under test: if PositionMs ever moves,
	// this test must say it has stopped exercising the boundary rather than
	// keep passing on a phase that no longer straddles it.
	if got := despawn.PositionMs(boundarySpawnID) / 1000; got != 1799 {
		t.Fatalf("fixture spawnpoint's derived phase is %d, want 1799 — this test no longer exercises the wrap", got)
	}

	for _, c := range []struct {
		phase int64
		want  bool
	}{
		{1799, false}, // exact
		{0, false},    // +1s, across the boundary
		{1, false},    // +2s, across the boundary
		{2, true},     // +3s: outside tolerance
		{1797, false}, // -2s
		{1796, true},  // -3s
	} {
		if got := despawnPhaseDiverged(boundarySpawnID, phaseTestExpiryAt(c.phase)); got != c.want {
			t.Errorf("observed phase %d against derived 1799: diverged=%v want %v", c.phase, got, c.want)
		}
	}
}

// The mod-1800 invariant, stated on its own: an hour-long spawnpoint's TTH
// legitimately lands a full half hour after the derived phase, and that must
// read as agreement — the counter validates the phase algorithm, not a
// spawnpoint's duration.
func TestDespawnPhaseDivergedIsHalfHourInvariant(t *testing.T) {
	base := phaseTestExpiryAt(phaseTestPhaseSec)
	for _, halfHours := range []int64{0, 1, 2, 47} {
		if despawnPhaseDiverged(phaseTestSpawnID, base+halfHours*1800) {
			t.Errorf("%d half-hours after the derived phase must still match mod 1800", halfHours)
		}
	}
}

// despawnPhaseDiverged takes an ABSOLUTE unix second, so no zone can reach
// it. In a 45-minute zone the local second-of-hour of the same instant is
// shifted by 900s mod 1800 — the maximum possible disagreement — so a local
// reading would flag divergence on every sighting in Nepal, Eucla and the
// Chathams. The two readings of one instant are computed side by side here
// and shown to differ, while the answer stays "agrees".
func TestDespawnPhaseDivergedIgnoresFortyFiveMinuteZones(t *testing.T) {
	expiry := phaseTestExpiryAt(phaseTestPhaseSec)

	for _, c := range []struct {
		zone           *time.Location
		wantLocalShift int64 // how far the local second-of-hour is displaced, mod 1800
	}{
		{time.FixedZone("+0545", 5*3600+45*60), 900},  // Kathmandu
		{time.FixedZone("+0845", 8*3600+45*60), 900},  // Eucla
		{time.FixedZone("+1245", 12*3600+45*60), 900}, // Chatham
		{time.FixedZone("+0530", 5*3600+30*60), 0},    // Kolkata: survives mod 1800
		{time.UTC, 0},
	} {
		local := time.Unix(expiry, 0).In(c.zone)
		localSecondOfHour := int64(local.Second() + local.Minute()*60)
		shift := ((localSecondOfHour-phaseTestPhaseSec)%1800 + 1800) % 1800
		if shift != c.wantLocalShift {
			t.Errorf("zone %s: local second-of-hour is displaced by %ds, expected %ds — fixture no longer exercises what it claims",
				c.zone, shift, c.wantLocalShift)
		}
		if despawnPhaseDiverged(phaseTestSpawnID, expiry) {
			t.Errorf("zone %s: the same instant must still agree (its local second-of-hour reads %d, %ds off the derived phase)",
				c.zone, localSecondOfHour, shift)
		}
	}
}

// The wiring: an actual hasTTH sighting whose despawn instant disagrees with
// its spawnpoint's derived phase must reach IncDespawnPhaseDiverged, and one
// that agrees must not. Both halves are needed — a call site deleted
// entirely satisfies the second on its own, and a check hard-wired to true
// satisfies the first.
//
// The divergent and agreeing timestamps are constructed from
// phaseTestPhaseSec, never found by calling despawnPhaseDiverged.
func TestSpawnpointUpdateFromWildCountsPhaseDivergence(t *testing.T) {
	const spawnHex = "47b973e0595"
	const tth = int32(30000)

	for _, c := range []struct {
		name       string
		phase      int64
		wantCounts int
	}{
		{"agrees exactly", phaseTestPhaseSec, 0},
		{"agrees within tolerance", phaseTestPhaseSec + 2, 0},
		{"diverges", phaseTestPhaseSec + 400, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			fake := withDerivationCountingCollector(t)

			spawnpointCache.Delete(phaseTestSpawnID)
			t.Cleanup(func() { spawnpointCache.Delete(phaseTestSpawnID) })

			// Work backwards from the expiry the check must see.
			timestampMs := phaseTestExpiryAt(c.phase)*1000 - int64(tth)

			wild := &pogo.WildPokemonProto{
				SpawnPointId:     spawnHex,
				TimeTillHiddenMs: tth,
				Latitude:         51.5,
				Longitude:        -0.1,
			}

			func() {
				// db.DbDetails{} wraps a nil *sqlx.DB: spawnpointUpdateFromWild
				// panics once it reaches getOrCreateSpawnpointRecord, well after
				// the divergence check this test cares about.
				defer func() { _ = recover() }()
				spawnpointUpdateFromWild(context.Background(), db.DbDetails{}, wild, timestampMs)
			}()

			if fake.phaseDiverged != c.wantCounts {
				t.Fatalf("IncDespawnPhaseDiverged count = %d, want %d", fake.phaseDiverged, c.wantCounts)
			}
		})
	}
}

// A TTH sighting a minute or more from its derived phase is logged as the
// derivation being wrong; jitter just past the counter's tolerance is not,
// and nor is an hour-long spawnpoint's despawn half an hour after the phase.
func TestSpawnpointUpdateFromWildWarnsWhenDespawnIsFarFromThePhase(t *testing.T) {
	const spawnHex = "47b973e0595"
	const tth = int32(30000)

	for _, c := range []struct {
		name     string
		phase    int64
		wantWarn bool
	}{
		{"jitter past the counter's tolerance", phaseTestPhaseSec + 3, false},
		{"just inside the threshold", phaseTestPhaseSec + despawnPhaseWrongThreshold - 1, false},
		{"hour-long spawnpoint", phaseTestPhaseSec + 1800, false},
		{"at the threshold", phaseTestPhaseSec + despawnPhaseWrongThreshold, true},
		{"far off", phaseTestPhaseSec + 400, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			despawnPhaseWrong.Reset()
			hook := captureStandardLoggerHook(t)

			spawnpointCache.Delete(phaseTestSpawnID)
			t.Cleanup(func() { spawnpointCache.Delete(phaseTestSpawnID) })

			timestampMs := phaseTestExpiryAt(c.phase)*1000 - int64(tth)
			wild := &pogo.WildPokemonProto{
				SpawnPointId:     spawnHex,
				TimeTillHiddenMs: tth,
				Latitude:         51.5,
				Longitude:        -0.1,
			}
			func() {
				// As above: the nil database panics after the phase check.
				defer func() { _ = recover() }()
				spawnpointUpdateFromWild(context.Background(), db.DbDetails{}, wild, timestampMs)
			}()

			warned := false
			for _, e := range hook.Entries {
				if strings.HasPrefix(e.Message, "[DESPAWN]") {
					warned = true
				}
			}
			if warned != c.wantWarn {
				t.Fatalf("[DESPAWN] warning logged = %v, want %v", warned, c.wantWarn)
			}
		})
	}
}
