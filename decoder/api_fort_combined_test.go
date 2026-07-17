package decoder

import "testing"

// TestCombinedFortMatches locks the typed-group dispatch of the combined scan:
// a clause group only governs its own fort type, so a gym clause can never
// vacuously match a pokestop (its pokestop-facing fields are all wildcards).
func TestCombinedFortMatches(t *testing.T) {
	now := int64(1000)
	gym := FortLookup{FortType: GYM, RaidLevel: 5, RaidBattleTimestamp: 900, RaidEndTimestamp: 2000}
	stop := FortLookup{FortType: POKESTOP, QuestNoArRewardType: 2, QuestNoArRewardItemId: 3}
	station := FortLookup{FortType: STATION, StationEndTimestamp: 2000}

	gymClauses := &ApiFortTypeScanGroup{DnfFilters: []ApiFortDnfFilter{{RaidLevel: []int8{5}}}}

	// vacuous cross-type match is impossible: only the gyms group exists
	p := &ApiFortCombinedScan{Gyms: gymClauses}
	if !combinedFortMatches(p, &gym, now) {
		t.Error("tier-5 raid gym should match its own group")
	}
	if combinedFortMatches(p, &stop, now) {
		t.Error("pokestop must NOT match when its group is omitted (excluded type)")
	}
	if combinedFortMatches(p, &station, now) {
		t.Error("station must NOT match when its group is omitted")
	}

	// group present with no clauses = match-all for that type only
	p = &ApiFortCombinedScan{Gyms: gymClauses, Pokestops: &ApiFortTypeScanGroup{}}
	if !combinedFortMatches(p, &stop, now) {
		t.Error("empty pokestop group should match every pokestop")
	}
	if combinedFortMatches(p, &station, now) {
		t.Error("station still excluded")
	}

	// all groups omitted = bare probe, everything matches
	p = &ApiFortCombinedScan{}
	for _, fl := range []*FortLookup{&gym, &stop, &station} {
		if !combinedFortMatches(p, fl, now) {
			t.Errorf("bare probe should match fort type %v", fl.FortType)
		}
	}

	// clauses within a group still narrow
	p = &ApiFortCombinedScan{Pokestops: &ApiFortTypeScanGroup{DnfFilters: []ApiFortDnfFilter{{QuestRewardType: []int16{7}}}}}
	if combinedFortMatches(p, &stop, now) {
		t.Error("item-quest stop must not match an encounter-only clause")
	}
}
