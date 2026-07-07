package decoder

import (
	"testing"

	"buf.build/go/hyperpb"
	"google.golang.org/protobuf/proto"

	"golbat/pogo"
	"golbat/pogoshim"
)

// hyperpbWrapPublicProfile mirrors the established hyperpbWrap<Root>
// convention (routes_shim_test.go, quest_shim_test.go).
func hyperpbWrapPublicProfile(t *testing.T, in *pogo.PlayerPublicProfileProto) (pogoshim.PlayerPublicProfileProto, *hyperpb.Shared) {
	t.Helper()
	raw, err := proto.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	ty := hyperpb.CompileMessageDescriptor((*pogo.PlayerPublicProfileProto)(nil).ProtoReflect().Descriptor())
	shared := new(hyperpb.Shared)
	msg := shared.NewMessage(ty)
	if err := msg.Unmarshal(raw); err != nil {
		shared.Free()
		t.Fatal(err)
	}
	return pogoshim.AsPlayerPublicProfileProto(msg.ProtoReflect()), shared
}

// TestUpdateFromPublicProfileShim is the "player summary field copy" test
// the Wave 3 Task 4 brief calls for: updateFromPublicProfile must extract
// every field via pogoshim getters exactly like the pre-shim code's direct
// *pogo.PlayerPublicProfileProto field/getter access, for both known badges
// (mapped via badgeTypeToPlayerKey through reflection) and event badges
// (BadgeType > BADGE_EVENT_MIN, accumulated as a comma-joined id list). The
// resulting Player struct holds only plain Go values (null.Int/null.String/
// string) -- no pogoshim value or protoreflect.Message field exists on
// Player at all, so there is nothing for playerCache (which stores *Player)
// to retain past the hyperpb arena's lifetime; this test's assertions run
// AFTER the hyperpb Shared backing the shim argument is still alive, but
// savePlayerRecord/getPlayerRecord in player.go never touch the shim again
// once updateFromPublicProfile returns, and the struct fields checked below
// are demonstrably independent copies (int64/string values, not shims).
func TestUpdateFromPublicProfileShim(t *testing.T) {
	build := func() *pogo.PlayerPublicProfileProto {
		return &pogo.PlayerPublicProfileProto{
			Name:          "Ash",
			Team:          pogo.Team_TEAM_RED,
			Level:         40,
			Experience:    20000000,
			BattlesWon:    12,
			KmWalked:      123.5,
			CaughtPokemon: 456,
			CombatRank:    7,
			CombatRating:  2500,
			Badges: []*pogo.PlayerBadgeProto{
				{BadgeType: pogo.HoloBadgeType_BADGE_POKESTOPS_VISITED, CurrentValue: 321},
				{BadgeType: pogo.HoloBadgeType_BADGE_RAID_BATTLE_WON, CurrentValue: 8},
				// Event badge: BadgeType > BADGE_EVENT_MIN, current value > 0
				// -> its numeric id gets appended to the comma-joined
				// EventBadges string instead of a per-column Set*.
				{BadgeType: pogo.HoloBadgeType_BADGE_EVENT_MIN + 1, CurrentValue: 1},
			},
		}
	}

	check := func(name string, profile pogoshim.PlayerPublicProfileProto) {
		player := &Player{}
		player.updateFromPublicProfile(profile)

		if player.Name != "Ash" {
			t.Errorf("%s: Name = %q, want Ash", name, player.Name)
		}
		if got, want := player.Team.ValueOrZero(), int64(pogo.Team_TEAM_RED); got != want {
			t.Errorf("%s: Team = %d, want %d", name, got, want)
		}
		if got, want := player.Level.ValueOrZero(), int64(40); got != want {
			t.Errorf("%s: Level = %d, want %d", name, got, want)
		}
		if got, want := player.Xp.ValueOrZero(), int64(20000000); got != want {
			t.Errorf("%s: Xp = %d, want %d", name, got, want)
		}
		if got, want := player.BattlesWon.ValueOrZero(), int64(12); got != want {
			t.Errorf("%s: BattlesWon = %d, want %d", name, got, want)
		}
		if got, want := player.KmWalked.ValueOrZero(), float64(float32(123.5)); got != want {
			t.Errorf("%s: KmWalked = %v, want %v", name, got, want)
		}
		if got, want := player.CaughtPokemon.ValueOrZero(), int64(456); got != want {
			t.Errorf("%s: CaughtPokemon = %d, want %d", name, got, want)
		}
		if got, want := player.GblRank.ValueOrZero(), int64(7); got != want {
			t.Errorf("%s: GblRank = %d, want %d", name, got, want)
		}
		if got, want := player.GblRating.ValueOrZero(), int64(2500); got != want {
			t.Errorf("%s: GblRating = %d, want %d", name, got, want)
		}
		if got, want := player.StopsSpun.ValueOrZero(), int64(321); got != want {
			t.Errorf("%s: StopsSpun = %d, want %d", name, got, want)
		}
		if got, want := player.NormalRaidsWon.ValueOrZero(), int64(8); got != want {
			t.Errorf("%s: NormalRaidsWon = %d, want %d", name, got, want)
		}
		if !player.EventBadges.Valid {
			t.Errorf("%s: EventBadges should be set (even if empty), got invalid", name)
		}
	}

	stdIn := build()
	check("std", pogoshim.AsPlayerPublicProfileProto(stdIn.ProtoReflect()))

	hyperShim, shared := hyperpbWrapPublicProfile(t, build())
	defer shared.Free()
	check("hyperpb", hyperShim)
}

// TestUpdateFromPublicProfileShim_EventBadgeIdMatchesAcrossEngines locks the
// EventBadges string itself (not just "is it set"): the comma-joined numeric
// badge id list must be byte-identical between engines.
func TestUpdateFromPublicProfileShim_EventBadgeIdMatchesAcrossEngines(t *testing.T) {
	build := func() *pogo.PlayerPublicProfileProto {
		return &pogo.PlayerPublicProfileProto{
			Name: "Misty",
			Badges: []*pogo.PlayerBadgeProto{
				{BadgeType: pogo.HoloBadgeType_BADGE_EVENT_MIN + 1, CurrentValue: 1},
				{BadgeType: pogo.HoloBadgeType_BADGE_EVENT_MIN + 2, CurrentValue: 5},
			},
		}
	}

	run := func(profile pogoshim.PlayerPublicProfileProto) string {
		player := &Player{}
		player.updateFromPublicProfile(profile)
		return player.EventBadges.ValueOrZero()
	}

	stdIn := build()
	stdBadges := run(pogoshim.AsPlayerPublicProfileProto(stdIn.ProtoReflect()))

	hyperShim, shared := hyperpbWrapPublicProfile(t, build())
	defer shared.Free()
	hyperBadges := run(hyperShim)

	if stdBadges == "" {
		t.Fatal("expected non-empty EventBadges")
	}
	if stdBadges != hyperBadges {
		t.Fatalf("EventBadges mismatch: std=%q hyperpb=%q", stdBadges, hyperBadges)
	}
}
