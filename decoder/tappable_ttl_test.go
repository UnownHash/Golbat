package decoder

import (
	"context"
	"testing"
	"time"

	"github.com/guregu/null/v6"

	"golbat/db"
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

func TestTappableSaveRecachesEvicted(t *testing.T) {
	const now = int64(1_700_000_000)
	ta := &Tappable{TappableData: TappableData{Id: 9001, ExpireTimestamp: null.IntFrom(now + 120)}}
	t.Cleanup(func() { tappableCache.Delete(9001) })

	tappableCacheOnSave(ta, now)
	tappableCache.Delete(9001) // evicted between the caller's cache get and its save
	tappableCacheOnSave(ta, now)
	if !tappableCache.Has(9001) {
		t.Error("tappable evicted mid-save not re-cached by the save")
	}
}

func TestTappableCleanSaveIndexesDbLoaded(t *testing.T) {
	// a DB-loaded, unchanged tappable is clean; its save must still index it
	ta := newIndexedTappable(9002, 11.1, 21.1)
	tappableCache.Set(9002, ta, time.Minute)
	t.Cleanup(func() { tappableCache.Delete(9002) })
	if _, ok := tappableLookupCache.Load(9002); ok {
		t.Fatal("setup: already indexed")
	}

	saveTappableRecord(context.Background(), db.DbDetails{}, ta)
	if _, ok := tappableLookupCache.Load(9002); !ok {
		t.Error("clean DB-loaded tappable not indexed")
	}
}
