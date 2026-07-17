package decoder

import "testing"

func TestObserveExpiryAndReadRaids(t *testing.T) {
	initFortAvailability()
	now := int64(1000)

	// active raid boss + active egg
	observeRaid(&FortLookup{RaidLevel: 5, RaidPokemonId: 150, RaidPokemonForm: 0, RaidEndTimestamp: 2000}, now)
	observeRaid(&FortLookup{RaidLevel: 3, RaidPokemonId: 0, RaidPokemonForm: 0, RaidEndTimestamp: 2000}, now)
	// no raid (level 0) -> ignored
	observeRaid(&FortLookup{RaidLevel: 0, RaidEndTimestamp: 2000}, now)
	// already-expired -> ignored
	observeRaid(&FortLookup{RaidLevel: 5, RaidPokemonId: 999, RaidEndTimestamp: 500}, now)

	got := readRaids(now)
	if len(got) != 2 {
		t.Fatalf("want 2 raid options, got %d: %+v", len(got), got)
	}
	for _, r := range got {
		if r.PokemonId == 999 {
			t.Fatal("expired raid must not appear")
		}
	}

	// keep-larger: re-observe boss 150 with a LATER expiry, then read after the
	// first expiry has passed — it must still be present.
	observeRaid(&FortLookup{RaidLevel: 5, RaidPokemonId: 150, RaidEndTimestamp: 3000}, now)
	if len(readRaids(2500)) == 0 {
		t.Fatal("refreshed raid should survive past its first expiry")
	}

	// prune-on-read: once fully expired, it drops out.
	if len(readRaids(4000)) != 0 {
		t.Fatal("all raids expired -> empty")
	}
	// and empty read returns [] not nil
	if readRaids(4000) == nil {
		t.Fatal("read must return non-nil empty slice")
	}
}

func TestObserveStationBattlesAndRead(t *testing.T) {
	initFortAvailability()
	now := int64(1000)

	// station with two active battles (slice) — both distinct options
	observeStationBattles(&FortLookup{StationBattles: []FortLookupStationBattle{
		{BattleLevel: 5, BattlePokemonId: 150, BattlePokemonForm: 0, BattleEndTimestamp: 2000},
		{BattleLevel: 3, BattlePokemonId: 0, BattlePokemonForm: 0, BattleEndTimestamp: 2000},
	}}, now)
	// level 0 -> ignored; expired -> ignored
	observeStationBattles(&FortLookup{StationBattles: []FortLookupStationBattle{
		{BattleLevel: 0, BattleEndTimestamp: 2000},
		{BattleLevel: 5, BattlePokemonId: 999, BattleEndTimestamp: 500},
	}}, now)
	// no slice: fall back to the top-battle scalar projection
	observeStationBattles(&FortLookup{BattleLevel: 4, BattlePokemonId: 200, BattleEndTimestamp: 2000}, now)

	got := readBattles(now)
	if len(got) != 3 {
		t.Fatalf("want 3 battle options, got %d: %+v", len(got), got)
	}
	for _, b := range got {
		if b.PokemonId == 999 {
			t.Fatal("expired battle leaked")
		}
	}
	if len(readBattles(3000)) != 0 {
		t.Fatal("all battles expired -> empty")
	}
}

func TestObservePokestopAggregatesAndRead(t *testing.T) {
	initFortAvailability()
	now := int64(1000)

	// lure + showcase on one stop
	observePokestop(&FortLookup{
		LureId: 501, LureExpireTimestamp: 2000,
		ContestPokemonId: 25, ContestPokemonForm: 0, ContestPokemonType: 0, ShowcaseExpiry: 2000,
	}, now)
	// expired lure + no showcase -> both ignored
	observePokestop(&FortLookup{LureId: 502, LureExpireTimestamp: 500}, now)

	// invasions (per incident)
	observeInvasion(&FortLookupIncident{Character: 5, DisplayType: 1, Confirmed: true, Slot1PokemonId: 41, ExpireTimestamp: 2000}, now)
	observeInvasion(&FortLookupIncident{DisplayType: 9, ExpireTimestamp: 2000}, now)               // showcase incident, character 0
	observeInvasion(&FortLookupIncident{Character: 30, DisplayType: 3, ExpireTimestamp: 500}, now) // expired

	if l := readLures(now); len(l) != 1 || l[0].LureId != 501 {
		t.Fatalf("lures: %+v", l)
	}
	if s := readShowcases(now); len(s) != 1 || s[0].PokemonId != 25 {
		t.Fatalf("showcases: %+v", s)
	}
	inv := readInvasions(now)
	if len(inv) != 2 {
		t.Fatalf("want 2 invasions, got %d: %+v", len(inv), inv)
	}
	for _, in := range inv {
		if in.Character == 30 {
			t.Fatal("expired invasion leaked")
		}
	}
	// everything expires
	if len(readLures(3000)) != 0 || len(readShowcases(3000)) != 0 || len(readInvasions(3000)) != 0 {
		t.Fatal("all pokestop aggregates should expire to empty")
	}
}
