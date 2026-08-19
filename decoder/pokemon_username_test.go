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
// of the field — the pre-existing behavior the option must not disturb.
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

// With the option on, the stored value still wins for the snapshot so the
// existing per-account dedup semantics are unchanged for those operators.
func TestStatsSnapshotPrefersStoredUsername(t *testing.T) {
	withStoreUsername(t, true)

	pokemon := &Pokemon{}
	pokemon.Username = null.StringFrom("StoredAccount")

	snap := pokemon.statsSnapshot("LiveAccount")
	if snap.Username.ValueOrZero() != "StoredAccount" {
		t.Fatalf("snapshot username = %q, want StoredAccount", snap.Username.ValueOrZero())
	}
}
