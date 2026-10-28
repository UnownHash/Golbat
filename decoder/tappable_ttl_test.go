package decoder

import (
	"testing"
	"time"

	"github.com/guregu/null/v6"
)

func TestTappableCacheTTL(t *testing.T) {
	const now = int64(1_700_000_000)
	for name, tc := range map[string]struct {
		expire null.Int
		want   time.Duration
	}{
		"future expire":     {null.IntFrom(now + 300), 360 * time.Second},
		"past expire floor": {null.IntFrom(now - 600), time.Minute},
		"near expire":       {null.IntFrom(now + 30), 90 * time.Second},
		"no expire":         {null.Int{}, 60 * time.Minute},
	} {
		t.Run(name, func(t *testing.T) {
			ta := &Tappable{TappableData: TappableData{ExpireTimestamp: tc.expire}}
			if got := tappableCacheTTL(ta, now); got != tc.want {
				t.Errorf("tappableCacheTTL = %v, want %v", got, tc.want)
			}
		})
	}
}

// recordTappableTTLUpdates swaps tappableCacheUpdateTTL for a recorder for one test.
func recordTappableTTLUpdates(t *testing.T) *[]time.Duration {
	t.Helper()
	var calls []time.Duration
	orig := tappableCacheUpdateTTL
	tappableCacheUpdateTTL = func(_ uint64, ttl time.Duration) { calls = append(calls, ttl) }
	t.Cleanup(func() { tappableCacheUpdateTTL = orig })
	return &calls
}

func TestTappableSaveExtendsCacheTTL(t *testing.T) {
	const now = int64(1_700_000_000)
	calls := recordTappableTTLUpdates(t)
	ta := &Tappable{TappableData: TappableData{Id: 9001, ExpireTimestamp: null.IntFrom(now + 120)}}
	t.Cleanup(func() { tappableCache.Delete(9001) })

	tappableCacheOnSave(ta, true, now)
	if len(*calls) != 0 {
		t.Fatalf("UpdateTTL called %d times for a new record, want 0", len(*calls))
	}
	if !tappableCache.Has(9001) {
		t.Fatal("new tappable not cached")
	}

	ta.ExpireTimestamp = null.IntFrom(now + 1200)
	tappableCacheOnSave(ta, false, now)
	if len(*calls) != 1 || (*calls)[0] != 1260*time.Second {
		t.Errorf("UpdateTTL calls = %v, want [1260s]", *calls)
	}
}

func TestTappableSaveNoTTLUpdateWhenExpireUnchanged(t *testing.T) {
	const now = int64(1_700_000_000)
	calls := recordTappableTTLUpdates(t)
	ta := &Tappable{TappableData: TappableData{Id: 9002, ExpireTimestamp: null.IntFrom(now + 120)}}
	t.Cleanup(func() { tappableCache.Delete(9002) })

	tappableCacheOnSave(ta, true, now)
	ta.SetType("TAPPABLE_TYPE_BREAKFAST")
	tappableCacheOnSave(ta, false, now)
	if len(*calls) != 0 {
		t.Errorf("UpdateTTL calls = %v, want none", *calls)
	}
}
