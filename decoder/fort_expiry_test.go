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
