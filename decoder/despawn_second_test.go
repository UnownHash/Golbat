package decoder

import (
	"context"
	"testing"
	"time"

	"golbat/db"

	"github.com/guregu/null/v6"
)

// localSecondOfHour is the local wall-clock second-of-hour of a unix instant
// in the given zone: the conversion older Golbat versions wrote despawn_sec
// with, so existing rows may hold it. It exists only to compare against
// despawnSecondOfHour.
func localSecondOfHour(unixSec int64, zone *time.Location) int {
	t := time.Unix(unixSec, 0).In(zone)
	return t.Second() + t.Minute()*60
}

// despawnSecondOfHour is `unix % 3600`, with no process timezone read at
// all. despawn_sec rows written by older Golbat versions use a local conversion
// (time.Unix(ts,0).Second()+.Minute()*60), so reading them back is only
// sound where the two formulas agree — which is every whole-hour UTC offset,
// since a whole-hour shift changes neither the minute nor the second of a
// wall clock. This checks that agreement across UTC and both a positive and
// a negative whole-hour offset.
//
// It does not test a half-hour zone (+05:30) or a 45-minute zone (+05:45):
// the two formulas diverge there by design.
// TestDespawnPhaseDivergedIgnoresFortyFiveMinuteZones covers those zones.
func TestDespawnSecondOfHourMatchesLocalInWholeHourZones(t *testing.T) {
	zones := []struct {
		name string
		zone *time.Location
	}{
		{"UTC", time.UTC},
		{"+09:00 (whole hour, positive)", time.FixedZone("+0900", 9*3600)},
		{"-08:00 (whole hour, negative)", time.FixedZone("-0800", -8*3600)},
	}

	// Instants chosen to straddle minute and hour boundaries, from the epoch
	// through an arbitrary present-day value — the domain despawn_sec
	// actually operates in; a despawn timestamp is never before 1970.
	//
	// Excludes negative unix seconds: Go's `%` truncates toward zero, so
	// `unix % 3600` is negative there (e.g. -1 % 3600 == -1) while a
	// wall-clock second-of-hour is always in [0,3600). despawnSecondOfHour
	// and the local conversion disagree before the epoch, which is harmless
	// since no real despawn instant is negative.
	instants := []int64{
		0,          // epoch
		1,          // just after epoch
		59,         // last second of minute 0
		60,         // first second of minute 1
		3599,       // last second of the hour
		3600,       // first second of the next hour
		1755000000, // an arbitrary recent instant
		1755003599, // ...one second before its hour rolls over
	}

	for _, z := range zones {
		t.Run(z.name, func(t *testing.T) {
			for _, unixSec := range instants {
				want := localSecondOfHour(unixSec, z.zone)
				got := despawnSecondOfHour(unixSec)
				if got != want {
					t.Errorf("unix=%d: despawnSecondOfHour=%d, local-in-%s=%d",
						unixSec, got, z.name, want)
				}
			}
		})
	}
}

// Tappable.setExpireTimestamp reaches the despawn_sec conversion through its
// own path (Tappable.applyVerifiedDespawn), used only by tappables, and a bare
// time.Unix(...) there reads the process's ambient time.Local without looking
// wrong. This pins its output directly against a stored despawn_sec under a
// 45-minute process timezone, where a local conversion is displaced (whole-hour
// offsets agree; see the test above).
func TestTappableSetExpireTimestampUsesAbsoluteSecondOfHour(t *testing.T) {
	kathmandu, err := time.LoadLocation("Asia/Kathmandu")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	prevLocal := time.Local
	time.Local = kathmandu
	t.Cleanup(func() { time.Local = prevLocal })

	const spawnID = int64(0x47b973e0598)
	const despawnSec = 1500

	seedCachedSpawnpoint(t, spawnID, null.IntFrom(despawnSec))

	// 12:00:00 UTC: unix % 3600 == 0, so the stored despawn second is
	// exactly despawnSec seconds away under the absolute convention.
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	wantExpiry := now.Unix() + despawnSec

	ta := &Tappable{}
	ta.SetSpawnId(null.IntFrom(spawnID))
	ta.setExpireTimestamp(context.Background(), db.DbDetails{}, now.UnixMilli())

	if got := int64(ta.ExpireTimestamp.ValueOrZero()); got != wantExpiry {
		t.Fatalf("expiry = %d, want %d", got, wantExpiry)
	}
	if !ta.ExpireTimestampVerified {
		t.Error("expiry not verified")
	}
}
