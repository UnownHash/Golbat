package decoder

import "testing"

func TestFortDnfMatch_IncidentSlice(t *testing.T) {
	now := int64(1_000_000)
	fl := &FortLookup{FortType: POKESTOP, Incidents: []FortLookupIncident{
		{DisplayType: 2, Character: 20, ExpireTimestamp: now + 100},                                                   // leader
		{DisplayType: 9, ExpireTimestamp: now + 100},                                                                  // showcase
		{DisplayType: 1, Character: 5, Confirmed: true, Slot1PokemonId: 41, Slot1Form: 0, ExpireTimestamp: now + 100}, // grunt
		{DisplayType: 3, Character: 30, ExpireTimestamp: now - 100},                                                   // EXPIRED giovanni
	}}
	// both coexisting characters match (no clobber)
	if !isFortDnfMatch(POKESTOP, fl, &ApiFortDnfFilter{IncidentCharacter: []int16{20}}, now) {
		t.Fatal("leader (20) should match")
	}
	if !isFortDnfMatch(POKESTOP, fl, &ApiFortDnfFilter{IncidentDisplayType: []int8{9}}, now) {
		t.Fatal("showcase (dt9) should match")
	}
	// active grunt character matches
	if !isFortDnfMatch(POKESTOP, fl, &ApiFortDnfFilter{IncidentCharacter: []int16{5}}, now) {
		t.Fatal("grunt (5) should match")
	}
	// expired incident does not match
	if isFortDnfMatch(POKESTOP, fl, &ApiFortDnfFilter{IncidentCharacter: []int16{30}}, now) {
		t.Fatal("expired giovanni (30) should NOT match")
	}
}
