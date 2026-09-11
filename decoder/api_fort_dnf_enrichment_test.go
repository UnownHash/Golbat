package decoder

import "testing"

// Availability splits mega and base bosses into distinct raid options, so a
// scan must be able to request one without the other.
func TestFortDnfMatch_RaidTempEvolution(t *testing.T) {
	now := int64(1_000_000)
	mega := &FortLookup{FortType: GYM, RaidLevel: 5, RaidPokemonId: 150, RaidPokemonEvolution: 2, RaidEndTimestamp: now + 100}
	base := &FortLookup{FortType: GYM, RaidLevel: 5, RaidPokemonId: 150, RaidEndTimestamp: now + 100}

	onlyMega := &ApiFortDnfFilter{RaidTempEvolutionId: []int8{2}}
	if !isFortDnfMatch(GYM, mega, onlyMega, now) {
		t.Fatal("mega raid should match a temp evolution 2 selector")
	}
	if isFortDnfMatch(GYM, base, onlyMega, now) {
		t.Fatal("base raid must NOT match a temp evolution 2 selector")
	}

	onlyBase := &ApiFortDnfFilter{RaidTempEvolutionId: []int8{0}}
	if !isFortDnfMatch(GYM, base, onlyBase, now) {
		t.Fatal("base raid should match a temp evolution 0 selector")
	}
	if isFortDnfMatch(GYM, mega, onlyBase, now) {
		t.Fatal("mega raid must NOT match a temp evolution 0 selector")
	}

	if !isFortDnfMatch(GYM, mega, &ApiFortDnfFilter{}, now) {
		t.Fatal("omitted temp evolution must not constrain")
	}
}

func TestFortDnfMatch_RaidTempEvolutionRequiresActiveRaid(t *testing.T) {
	now := int64(1_000_000)
	expired := &FortLookup{FortType: GYM, RaidLevel: 5, RaidPokemonId: 150, RaidPokemonEvolution: 2, RaidEndTimestamp: now - 100}
	if isFortDnfMatch(GYM, expired, &ApiFortDnfFilter{RaidTempEvolutionId: []int8{2}}, now) {
		t.Fatal("expired raid must NOT match a temp evolution selector")
	}
}

func TestFortDnfMatch_ContestRankingStandard(t *testing.T) {
	now := int64(1_000_000)
	stop := &FortLookup{FortType: POKESTOP, ContestPokemonId: 25, ShowcaseRankingStandard: 3, ShowcaseExpiry: now + 100}

	if !isFortDnfMatch(POKESTOP, stop, &ApiFortDnfFilter{ContestRankingStandard: []int8{3}}, now) {
		t.Fatal("showcase should match its own ranking standard")
	}
	if isFortDnfMatch(POKESTOP, stop, &ApiFortDnfFilter{ContestRankingStandard: []int8{1}}, now) {
		t.Fatal("showcase must NOT match a different ranking standard")
	}
	if !isFortDnfMatch(POKESTOP, stop, &ApiFortDnfFilter{}, now) {
		t.Fatal("omitted ranking standard must not constrain")
	}
}

func TestFortDnfMatch_ContestRankingStandardRespectsExpiry(t *testing.T) {
	now := int64(1_000_000)
	expired := &FortLookup{FortType: POKESTOP, ContestPokemonId: 25, ShowcaseRankingStandard: 3, ShowcaseExpiry: now - 100}
	if isFortDnfMatch(POKESTOP, expired, &ApiFortDnfFilter{ContestRankingStandard: []int8{3}}, now) {
		t.Fatal("expired showcase must NOT match a ranking standard selector")
	}
}
