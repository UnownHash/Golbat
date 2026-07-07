package decoder

import (
	"testing"

	"buf.build/go/hyperpb"
	"google.golang.org/protobuf/proto"

	"golbat/pogo"
	"golbat/pogoshim"
)

func hyperpbWrapContest(t *testing.T, in *pogo.ContestProto) (pogoshim.ContestProto, *hyperpb.Shared) {
	t.Helper()
	raw, err := proto.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	ty := hyperpb.CompileMessageDescriptor((*pogo.ContestProto)(nil).ProtoReflect().Descriptor())
	shared := new(hyperpb.Shared)
	msg := shared.NewMessage(ty)
	if err := msg.Unmarshal(raw); err != nil {
		shared.Free()
		t.Fatal(err)
	}
	return pogoshim.AsContestProto(msg.ProtoReflect()), shared
}

// TestUpdatePokestopFromGetContestDataOutProtoShim locks in Wave 3 Task 4
// behavior for the showcase focus mapping: createFocusStoreFromContestProto's
// nil-pointer checks (pok != nil, pokType != nil, ...) become Has<Field>()
// presence checks, since a pogoshim getter never returns Go nil.
func TestUpdatePokestopFromGetContestDataOutProtoShim(t *testing.T) {
	build := func() *pogo.ContestProto {
		return &pogo.ContestProto{
			ContestId: "FORT1-1",
			Metric:    &pogo.ContestMetricProto{RankingStandard: pogo.ContestRankingStandard_MAX},
			Schedule: &pogo.ContestScheduleProto{
				ContestCycle: &pogo.ContestCycleProto{EndTimeMs: 5_000_000},
			},
			Focuses: []*pogo.ContestFocusProto{
				{ContestFocus: &pogo.ContestFocusProto_Pokemon{Pokemon: &pogo.ContestPokemonFocusProto{
					PokedexId:          pogo.HoloPokemonId_BULBASAUR,
					RequireFormToMatch: true,
					PokemonDisplay:     &pogo.PokemonDisplayProto{Form: pogo.PokemonDisplayProto_Form(3)},
				}}},
			},
		}
	}

	check := func(name string, contest pogoshim.ContestProto) {
		stop := &Pokestop{PokestopData: PokestopData{Id: "FORT1"}}
		stop.updatePokestopFromGetContestDataOutProto(contest)

		if got, want := stop.ShowcaseRankingStandard.ValueOrZero(), int64(pogo.ContestRankingStandard_MAX); got != want {
			t.Errorf("%s: ShowcaseRankingStandard = %d, want %d", name, got, want)
		}
		if got, want := stop.ShowcaseExpiry.ValueOrZero(), int64(5000); got != want {
			t.Errorf("%s: ShowcaseExpiry = %d, want %d", name, got, want)
		}
		if !stop.ShowcaseFocus.Valid {
			t.Errorf("%s: ShowcaseFocus should be set", name)
		}
		if got, want := stop.ShowcasePokemon.ValueOrZero(), int64(pogo.HoloPokemonId_BULBASAUR); got != want {
			t.Errorf("%s: ShowcasePokemon = %d, want %d", name, got, want)
		}
		if got, want := stop.ShowcasePokemonForm.ValueOrZero(), int64(3); got != want {
			t.Errorf("%s: ShowcasePokemonForm = %d, want %d", name, got, want)
		}
	}

	stdIn := build()
	check("std", pogoshim.AsContestProto(stdIn.ProtoReflect()))

	hyperShim, shared := hyperpbWrapContest(t, build())
	defer shared.Free()
	check("hyperpb", hyperShim)
}

// TestUpdatePokestopFromGetContestDataOutProtoShim_NoFormDisplay proves the
// latent-panic-removal in createFocusStoreFromContestProto: RequireFormToMatch
// true with no PokemonDisplay set (pre-shim: pok.PokemonDisplay.Form would
// nil-deref) must degrade to form 0, not panic.
func TestUpdatePokestopFromGetContestDataOutProtoShim_NoFormDisplay(t *testing.T) {
	build := func() *pogo.ContestProto {
		return &pogo.ContestProto{
			ContestId: "FORT2-1",
			Focuses: []*pogo.ContestFocusProto{
				{ContestFocus: &pogo.ContestFocusProto_Pokemon{Pokemon: &pogo.ContestPokemonFocusProto{
					PokedexId:          pogo.HoloPokemonId_CHARMANDER,
					RequireFormToMatch: true,
					// PokemonDisplay deliberately left nil.
				}}},
			},
		}
	}

	check := func(name string, contest pogoshim.ContestProto) {
		stop := &Pokestop{PokestopData: PokestopData{Id: "FORT2"}}
		stop.updatePokestopFromGetContestDataOutProto(contest)

		if got, want := stop.ShowcasePokemonForm.ValueOrZero(), int64(0); got != want {
			t.Errorf("%s: ShowcasePokemonForm = %d, want %d", name, got, want)
		}
	}

	stdIn := build()
	check("std", pogoshim.AsContestProto(stdIn.ProtoReflect()))

	hyperShim, shared := hyperpbWrapContest(t, build())
	defer shared.Free()
	check("hyperpb", hyperShim)
}

func hyperpbWrapSizeEntry(t *testing.T, in *pogo.GetPokemonSizeLeaderboardEntryOutProto) (pogoshim.GetPokemonSizeLeaderboardEntryOutProto, *hyperpb.Shared) {
	t.Helper()
	raw, err := proto.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	ty := hyperpb.CompileMessageDescriptor((*pogo.GetPokemonSizeLeaderboardEntryOutProto)(nil).ProtoReflect().Descriptor())
	shared := new(hyperpb.Shared)
	msg := shared.NewMessage(ty)
	if err := msg.Unmarshal(raw); err != nil {
		shared.Free()
		t.Fatal(err)
	}
	return pogoshim.AsGetPokemonSizeLeaderboardEntryOutProto(msg.ProtoReflect()), shared
}

// TestUpdatePokestopFromGetPokemonSizeContestEntryOutProtoShim covers the
// rank-1 top-score tracking and the JSON rankings blob.
func TestUpdatePokestopFromGetPokemonSizeContestEntryOutProtoShim(t *testing.T) {
	build := func() *pogo.GetPokemonSizeLeaderboardEntryOutProto {
		return &pogo.GetPokemonSizeLeaderboardEntryOutProto{
			Status:       pogo.GetPokemonSizeLeaderboardEntryOutProto_SUCCESS,
			TotalEntries: 2,
			ContestEntries: []*pogo.ContestEntryProto{
				{Rank: 1, Score: 99.5, PokedexId: pogo.HoloPokemonId_MAGIKARP, PokemonDisplay: &pogo.PokemonDisplayProto{Costume: pogo.PokemonDisplayProto_Costume(2)}},
				{Rank: 2, Score: 80.0, PokedexId: pogo.HoloPokemonId_RATTATA},
			},
		}
	}

	check := func(name string, data pogoshim.GetPokemonSizeLeaderboardEntryOutProto) {
		stop := &Pokestop{PokestopData: PokestopData{Id: "FORT3"}}
		stop.updatePokestopFromGetPokemonSizeContestEntryOutProto(data)

		if !stop.ShowcaseRankings.Valid {
			t.Errorf("%s: ShowcaseRankings should be set", name)
		}
		if got, want := stop.oldValues.ShowcaseTopScore.ValueOrZero(), 99.5; got != want {
			t.Errorf("%s: oldValues.ShowcaseTopScore = %v, want %v", name, got, want)
		}
		if got, want := stop.oldValues.ShowcaseTopPokemonId.ValueOrZero(), int64(pogo.HoloPokemonId_MAGIKARP); got != want {
			t.Errorf("%s: oldValues.ShowcaseTopPokemonId = %d, want %d", name, got, want)
		}
		if !stop.pokestopWebhookRequired {
			t.Errorf("%s: expected pokestopWebhookRequired=true on first rank-1 observation", name)
		}
	}

	stdIn := build()
	check("std", pogoshim.AsGetPokemonSizeLeaderboardEntryOutProto(stdIn.ProtoReflect()))

	hyperShim, shared := hyperpbWrapSizeEntry(t, build())
	defer shared.Free()
	check("hyperpb", hyperShim)
}

func hyperpbWrapStationedDetails(t *testing.T, in *pogo.GetStationedPokemonDetailsOutProto) (pogoshim.GetStationedPokemonDetailsOutProto, *hyperpb.Shared) {
	t.Helper()
	raw, err := proto.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	ty := hyperpb.CompileMessageDescriptor((*pogo.GetStationedPokemonDetailsOutProto)(nil).ProtoReflect().Descriptor())
	shared := new(hyperpb.Shared)
	msg := shared.NewMessage(ty)
	if err := msg.Unmarshal(raw); err != nil {
		shared.Free()
		t.Fatal(err)
	}
	return pogoshim.AsGetStationedPokemonDetailsOutProto(msg.ProtoReflect()), shared
}

// TestUpdateFromGetStationedPokemonDetailsOutProtoShim covers the
// stationed-pokemon JSON blob and the gmax counter.
func TestUpdateFromGetStationedPokemonDetailsOutProtoShim(t *testing.T) {
	build := func() *pogo.GetStationedPokemonDetailsOutProto {
		return &pogo.GetStationedPokemonDetailsOutProto{
			Result:                   pogo.GetStationedPokemonDetailsOutProto_SUCCESS,
			TotalNumStationedPokemon: 2,
			StationedPokemons: []*pogo.PlayerClientStationedPokemonProto{
				{Pokemon: &pogo.PokemonProto{
					PokemonId:      pogo.HoloPokemonId_BULBASAUR,
					PokemonDisplay: &pogo.PokemonDisplayProto{BreadModeEnum: pogo.BreadModeEnum_BREAD_DOUGH_MODE},
				}},
				{Pokemon: &pogo.PokemonProto{
					PokemonId:      pogo.HoloPokemonId_CHARMANDER,
					PokemonDisplay: &pogo.PokemonDisplayProto{BreadModeEnum: pogo.BreadModeEnum_BREAD_DOUGH_MODE_2},
				}},
			},
		}
	}

	check := func(name string, data pogoshim.GetStationedPokemonDetailsOutProto) {
		station := &Station{StationData: StationData{Id: "STATION1"}}
		station.updateFromGetStationedPokemonDetailsOutProto(data)

		if !station.StationedPokemon.Valid {
			t.Errorf("%s: StationedPokemon should be set", name)
		}
		if got, want := station.TotalStationedPokemon.ValueOrZero(), int64(2); got != want {
			t.Errorf("%s: TotalStationedPokemon = %d, want %d", name, got, want)
		}
		if got, want := station.TotalStationedGmax.ValueOrZero(), int64(2); got != want {
			t.Errorf("%s: TotalStationedGmax = %d, want %d (both entries are dough-mode)", name, got, want)
		}
	}

	stdIn := build()
	check("std", pogoshim.AsGetStationedPokemonDetailsOutProto(stdIn.ProtoReflect()))

	hyperShim, shared := hyperpbWrapStationedDetails(t, build())
	defer shared.Free()
	check("hyperpb", hyperShim)
}

func hyperpbWrapRsvpOut(t *testing.T, in *pogo.GetEventRsvpsOutProto) (pogoshim.GetEventRsvpsOutProto, *hyperpb.Shared) {
	t.Helper()
	raw, err := proto.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	ty := hyperpb.CompileMessageDescriptor((*pogo.GetEventRsvpsOutProto)(nil).ProtoReflect().Descriptor())
	shared := new(hyperpb.Shared)
	msg := shared.NewMessage(ty)
	if err := msg.Unmarshal(raw); err != nil {
		shared.Free()
		t.Fatal(err)
	}
	return pogoshim.AsGetEventRsvpsOutProto(msg.ProtoReflect()), shared
}

// TestUpdateGymFromRsvpProtoShim covers the RSVP timeslot JSON blob,
// including the going/maybe > 0 filter and the sort-by-timeslot step.
func TestUpdateGymFromRsvpProtoShim(t *testing.T) {
	build := func() *pogo.GetEventRsvpsOutProto {
		return &pogo.GetEventRsvpsOutProto{
			Status: pogo.GetEventRsvpsOutProto_SUCCESS,
			RsvpTimeslots: []*pogo.EventRsvpTimeslotProto{
				{TimeSlot: 200, GoingCount: 0, MaybeCount: 0}, // filtered out
				{TimeSlot: 100, GoingCount: 3, MaybeCount: 1},
				{TimeSlot: 50, GoingCount: 0, MaybeCount: 2},
			},
		}
	}

	check := func(name string, data pogoshim.GetEventRsvpsOutProto) {
		gym := &Gym{GymData: GymData{Id: "GYM1"}}
		gym.updateGymFromRsvpProto(data)

		if !gym.Rsvps.Valid {
			t.Fatalf("%s: Rsvps should be set", name)
		}
		const want = `[{"timeslot":50,"going_count":0,"maybe_count":2},{"timeslot":100,"going_count":3,"maybe_count":1}]`
		if gym.Rsvps.String != want {
			t.Errorf("%s: Rsvps = %s, want %s", name, gym.Rsvps.String, want)
		}
	}

	stdIn := build()
	check("std", pogoshim.AsGetEventRsvpsOutProto(stdIn.ProtoReflect()))

	hyperShim, shared := hyperpbWrapRsvpOut(t, build())
	defer shared.Free()
	check("hyperpb", hyperShim)
}
