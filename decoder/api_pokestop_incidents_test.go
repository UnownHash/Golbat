package decoder

import (
	"context"
	"testing"
	"time"

	db "golbat/db"
	ottercache "golbat/ottercache"

	"github.com/guregu/null/v6"
	"github.com/puzpuzpuz/xsync/v4"
)

func newTestIncidentCache() *ottercache.OtterCache[string, *Incident] {
	return ottercache.NewOtterCache(ottercache.OtterCacheConfig[string, *Incident]{
		Name: "incident-test", DefaultTTL: 60 * time.Minute,
	})
}

// CollectPokestopIncidents returns the whole active-incident rows for a fort,
// looked up from incidentCache via the FortLookup handles, skipping expired.
func TestCollectPokestopIncidents(t *testing.T) {
	fortLookupCache = xsync.NewMap[string, FortLookup]()
	incidentCache = newTestIncidentCache()
	now := int64(1_000_000)

	active := &Incident{IncidentData: IncidentData{
		Id: "inc-active", PokestopId: "s1", DisplayType: 1, Character: 5,
		Confirmed: true, Slot1PokemonId: null.IntFrom(41), ExpirationTime: now + 100,
	}}
	expired := &Incident{IncidentData: IncidentData{
		Id: "inc-expired", PokestopId: "s1", DisplayType: 3, Character: 30, ExpirationTime: now - 1,
	}}
	incidentCache.Set("inc-active", active, 0)
	incidentCache.Set("inc-expired", expired, 0)

	fortLookupCache.Store("s1", FortLookup{FortType: POKESTOP, Incidents: []FortLookupIncident{
		{Id: "inc-active", DisplayType: 1, Character: 5, ExpireTimestamp: now + 100},
		{Id: "inc-expired", DisplayType: 3, Character: 30, ExpireTimestamp: now - 1},
	}})

	got := CollectPokestopIncidents(context.Background(), db.DbDetails{}, "s1", now)
	if len(got) != 1 {
		t.Fatalf("expected 1 active incident, got %d: %+v", len(got), got)
	}
	if got[0].Id != "inc-active" || got[0].Character != 5 || got[0].Slot1PokemonId == nil || *got[0].Slot1PokemonId != 41 {
		t.Fatalf("wrong incident payload: %+v", got[0])
	}
}
