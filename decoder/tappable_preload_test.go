package decoder

import (
	"testing"

	"github.com/guregu/null/v6"
)

func TestTappablePreloadRow(t *testing.T) {
	const now = int64(1_700_000_000)
	for name, tc := range map[string]struct {
		id     uint64
		expire null.Int
		want   bool
	}{
		"unexpired": {940001, null.IntFrom(now + 300), true},
		"expired":   {940002, null.IntFrom(now - 1), false},
		"no expire": {940003, null.Int{}, false},
	} {
		t.Run(name, func(t *testing.T) {
			ta := &Tappable{TappableData: TappableData{Id: tc.id, Lat: 50, Lon: 60, ExpireTimestamp: tc.expire}}
			t.Cleanup(func() { tappableCache.Delete(tc.id) })
			if got := preloadTappableRow(ta, now); got != tc.want {
				t.Fatalf("preloadTappableRow = %v, want %v", got, tc.want)
			}
			_, indexed := tappableLookupCache.Load(tc.id)
			if tappableCache.Has(tc.id) != tc.want || indexed != tc.want {
				t.Errorf("cached %v indexed %v, want %v", tappableCache.Has(tc.id), indexed, tc.want)
			}
		})
	}
}
