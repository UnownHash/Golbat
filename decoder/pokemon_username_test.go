package decoder

import (
	"testing"

	"golbat/config"

	"github.com/guregu/null/v6"
)

func withStoreUsername(t *testing.T, enabled bool) {
	t.Helper()
	previous := config.Config.StoreUsername
	config.Config.StoreUsername = enabled
	t.Cleanup(func() { config.Config.StoreUsername = previous })
}

// Default off: the account name never reaches the entity, so the column
// writes NULL and the API reports no username.
func TestUsernameNotStoredByDefault(t *testing.T) {
	withStoreUsername(t, false)

	var pokemon Pokemon
	pokemon.setUsernameIfStored("SomeAccount")

	if pokemon.Username.Valid {
		t.Fatalf("username stored with the option off: %q", pokemon.Username.ValueOrZero())
	}
}

func TestUsernameStoredWhenEnabled(t *testing.T) {
	withStoreUsername(t, true)

	var pokemon Pokemon
	pokemon.setUsernameIfStored("SomeAccount")

	if !pokemon.Username.Valid || pokemon.Username.ValueOrZero() != "SomeAccount" {
		t.Fatalf("username = %v, want SomeAccount", pokemon.Username)
	}
}

// With the option on, the first account to see the pokemon keeps ownership
// of the field. This is a deliberate change of the column's meaning, not a
// preservation of prior behavior: before store_username existed, three of
// the six setUsernameIfStored call sites (updateFromWild, updateFromNearby,
// addEncounterPokemon) called SetUsername unconditionally on every
// re-sighting, i.e. last-account-wins; only the map and tappable sites were
// already first-wins. First-wins was chosen for all of them here because it
// avoids re-dirtying (and rewriting) a row on every re-sighting merely for a
// changed reporter. The webhook and dedup consumers are unaffected by this
// choice: resolveUsername prefers the live, per-request account over the
// stored value whenever one is available, so "first account" only matters
// as the value a client reads back from a persisted row, not for
// webhook/dedup correctness.
func TestUsernameNotOverwrittenWhenEnabled(t *testing.T) {
	withStoreUsername(t, true)

	var pokemon Pokemon
	pokemon.setUsernameIfStored("FirstAccount")
	pokemon.setUsernameIfStored("SecondAccount")

	if !pokemon.Username.Valid || pokemon.Username.ValueOrZero() != "FirstAccount" {
		t.Fatalf("username = %v, want FirstAccount (first account should keep ownership)", pokemon.Username)
	}
}

// The shiny/duplicate-encounter dedup needs the account that is reporting
// *now*, which the decode context supplies — not the stored first-seen
// account. It must therefore work identically with the option off.
func TestStatsSnapshotCarriesThreadedUsername(t *testing.T) {
	withStoreUsername(t, false)

	pokemon := &Pokemon{}
	pokemon.Username = null.String{} // nothing stored

	snap := pokemon.statsSnapshot("LiveAccount")
	if snap.Username.ValueOrZero() != "LiveAccount" {
		t.Fatalf("snapshot username = %q, want LiveAccount", snap.Username.ValueOrZero())
	}
}

// Review round 1 defect: the live (reporting) account must win over the
// stored one even with the option on. Stored is a first-wins value — it
// freezes on whichever account happened to see the pokemon first — so once
// a second account reports the same pokemon, preferring stored corrupts the
// shiny/duplicate-encounter dedup: the second account's snapshot would carry
// the *first* account's name and updateEncounterStats would treat it as a
// repeat check by that first account, silently dropping it from the stats.
func TestStatsSnapshotPrefersLiveUsername(t *testing.T) {
	withStoreUsername(t, true)

	pokemon := &Pokemon{}
	pokemon.Username = null.StringFrom("StoredAccount")

	snap := pokemon.statsSnapshot("LiveAccount")
	if snap.Username.ValueOrZero() != "LiveAccount" {
		t.Fatalf("snapshot username = %q, want LiveAccount (the live account must win over the stale stored one)", snap.Username.ValueOrZero())
	}
}

// weather_iv.go's proactive IV re-save has no decode context and passes "".
// That path's behavior must stay exactly what it was before this option
// existed: fall back to whatever is stored (which, with the option off, is
// also empty).
func TestStatsSnapshotFallsBackToStoredUsernameWhenLiveEmpty(t *testing.T) {
	withStoreUsername(t, true)

	pokemon := &Pokemon{}
	pokemon.Username = null.StringFrom("StoredAccount")

	snap := pokemon.statsSnapshot("")
	if snap.Username.ValueOrZero() != "StoredAccount" {
		t.Fatalf("snapshot username = %q, want StoredAccount (no live account to prefer)", snap.Username.ValueOrZero())
	}
}

// resolveUsername is shared by statsSnapshot and createPokemonWebhooks —
// this exercises its precedence directly rather than only indirectly
// through those two callers.
func TestResolveUsername(t *testing.T) {
	cases := []struct {
		name   string
		stored null.String
		live   string
		want   null.String
	}{
		{"live wins over stored", null.StringFrom("Stored"), "Live", null.StringFrom("Live")},
		{"falls back to stored when live is empty", null.StringFrom("Stored"), "", null.StringFrom("Stored")},
		{"live wins even when nothing is stored", null.String{}, "Live", null.StringFrom("Live")},
		{"both empty resolves to invalid", null.String{}, "", null.String{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveUsername(c.stored, c.live)
			if got != c.want {
				t.Fatalf("resolveUsername(%v, %q) = %v, want %v", c.stored, c.live, got, c.want)
			}
		})
	}
}

// TestUpdateEncounterStatsCountsDistinctAccountsAcrossEncounters is the
// regression test for the review-round-1 defect, exercised at the level
// that actually matters: the shiny/duplicate-encounter dedup itself. Two
// genuinely different accounts encounter the same pokemon; the first-wins
// gate correctly freezes the stored column on the first account (that
// governs persistence only), but the dedup must still see each account as
// its own distinct reporter. Before the fix, preferring the stale stored
// value made the second account's snapshot carry the first account's name,
// so SetAccountSeen reported "already seen" and updateEncounterStats
// returned early — the second account's shiny check silently vanished from
// the stats instead of being counted as a new distinct check.
func TestUpdateEncounterStatsCountsDistinctAccountsAcrossEncounters(t *testing.T) {
	withStoreUsername(t, true)

	const encId = uint64(920001) // unique across the test binary's shared caches
	pokemon := &Pokemon{PokemonData: PokemonData{
		Id:    Uint64Str(encId),
		AtkIv: null.ValueFrom(uint8(10)),
		DefIv: null.ValueFrom(uint8(10)),
		StaIv: null.ValueFrom(uint8(10)),
	}}

	// First encounter: account A. Nothing stored yet, so it's recorded.
	pokemon.setUsernameIfStored("AccountA")
	pokemonStatsLock.Lock()
	updateEncounterStats(pokemon.statsSnapshot("AccountA"))
	pokemonStatsLock.Unlock()

	// Second encounter: a genuinely different account, B. The stored
	// column correctly stays "AccountA" (first-wins governs persistence),
	// but the live account reporting *this* encounter is B.
	pokemon.setUsernameIfStored("AccountB")
	if pokemon.Username.ValueOrZero() != "AccountA" {
		t.Fatalf("stored username = %q, want AccountA (first-wins gate must not change)", pokemon.Username.ValueOrZero())
	}
	pokemonStatsLock.Lock()
	updateEncounterStats(pokemon.statsSnapshot("AccountB"))
	pokemonStatsLock.Unlock()

	cacheVal := encounterCache.Get(encId)
	if cacheVal == nil {
		t.Fatal("no encounter cache entry after two encounters")
	}
	if got := cacheVal.NumAccountsSeen(); got != 2 {
		t.Fatalf("accounts seen for encounter %d = %d, want 2 (AccountA and AccountB both counted as distinct reporters)", encId, got)
	}
}
