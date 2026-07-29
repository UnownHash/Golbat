package decoder

import (
	"testing"

	"github.com/guregu/null/v6"
	"github.com/puzpuzpuz/xsync/v4"
)

// updatePokestopIncidentLookup must carry the incident Id onto the FortLookup
// projection so the scan can fetch the whole incident row from incidentCache.
func TestUpdatePokestopIncidentLookupCarriesId(t *testing.T) {
	fortLookupCache = xsync.NewMap[string, FortLookup]()
	const id = "stop-1"
	fortLookupCache.Store(id, FortLookup{FortType: POKESTOP, Lat: 1, Lon: 2})

	inc := &Incident{IncidentData: IncidentData{
		Id:             "-1016089077232382347",
		DisplayType:    1,
		Character:      5,
		Confirmed:      true,
		Slot1PokemonId: null.IntFrom(41),
		ExpirationTime: 9_999_999_999,
	}}
	updatePokestopIncidentLookup(id, inc)

	fl, ok := fortLookupCache.Load(id)
	if !ok || len(fl.Incidents) != 1 {
		t.Fatalf("expected 1 incident, got %+v", fl.Incidents)
	}
	if fl.Incidents[0].Id != "-1016089077232382347" {
		t.Fatalf("incident Id not carried: %q", fl.Incidents[0].Id)
	}
}
