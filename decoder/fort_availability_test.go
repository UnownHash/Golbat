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
