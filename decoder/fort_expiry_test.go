package decoder

import "testing"

func TestFortDnfMatch_LureExpiry(t *testing.T) {
	now := int64(1_000_000)
	active := &FortLookup{FortType: POKESTOP, LureId: 501, LureExpireTimestamp: now + 100}
	expired := &FortLookup{FortType: POKESTOP, LureId: 501, LureExpireTimestamp: now - 100}
	f := &ApiFortDnfFilter{LureId: []int16{501}}
	if !isFortDnfMatch(POKESTOP, active, f, now) {
		t.Fatal("active lure should match")
	}
	if isFortDnfMatch(POKESTOP, expired, f, now) {
		t.Fatal("expired lure should NOT match")
	}
}

func TestFortDnfMatch_ShowcaseExpiry(t *testing.T) {
	now := int64(1_000_000)
	active := &FortLookup{FortType: POKESTOP, ContestPokemonId: 1, ShowcaseExpiry: now + 100}
	expired := &FortLookup{FortType: POKESTOP, ContestPokemonId: 1, ShowcaseExpiry: now - 100}
	f := &ApiFortDnfFilter{ContestPokemon: []ApiDnfId{{Pokemon: 1}}}
	if !isFortDnfMatch(POKESTOP, active, f, now) {
		t.Fatal("active showcase should match")
	}
	if isFortDnfMatch(POKESTOP, expired, f, now) {
		t.Fatal("expired showcase should NOT match")
	}
}

func TestFortDnfMatch_BuddyShowcaseFocus(t *testing.T) {
	now := int64(1_000_000)
	good := int8(2)
	great := int8(3)
	active := &FortLookup{FortType: POKESTOP, ShowcaseBuddyMinLevel: great, ShowcaseExpiry: now + 100}
	expired := &FortLookup{FortType: POKESTOP, ShowcaseBuddyMinLevel: great, ShowcaseExpiry: now - 100}

	if !isFortDnfMatch(POKESTOP, active, &ApiFortDnfFilter{}, now) {
		t.Fatal("omitted contest_focus should not constrain the showcase")
	}
	if isFortDnfMatch(POKESTOP, active, &ApiFortDnfFilter{ContestFocus: []ApiFortDnfContestFocus{}}, now) {
		t.Fatal("explicitly empty contest_focus should match nothing")
	}
	if !isFortDnfMatch(POKESTOP, active, &ApiFortDnfFilter{ContestFocus: []ApiFortDnfContestFocus{{Type: "buddy", MinLevel: &great}}}, now) {
		t.Fatal("active Great Buddy showcase should match its exact focus")
	}
	if isFortDnfMatch(POKESTOP, active, &ApiFortDnfFilter{ContestFocus: []ApiFortDnfContestFocus{{Type: "buddy", MinLevel: &good}}}, now) {
		t.Fatal("Great Buddy showcase must not match a Good Buddy selector")
	}
	if isFortDnfMatch(POKESTOP, active, &ApiFortDnfFilter{ContestFocus: []ApiFortDnfContestFocus{{Type: "buddy"}}}, now) {
		t.Fatal("Buddy selector without min_level must match nothing")
	}
	if isFortDnfMatch(POKESTOP, active, &ApiFortDnfFilter{ContestFocus: []ApiFortDnfContestFocus{{Type: "pokemon", MinLevel: &great}}}, now) {
		t.Fatal("unsupported focus types must match nothing")
	}
	if isFortDnfMatch(POKESTOP, expired, &ApiFortDnfFilter{ContestFocus: []ApiFortDnfContestFocus{{Type: "buddy", MinLevel: &great}}}, now) {
		t.Fatal("expired Buddy showcase must not match")
	}
}
