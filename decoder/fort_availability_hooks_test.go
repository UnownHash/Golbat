package decoder

import (
	"testing"
	"time"

	"github.com/guregu/null/v6"
)

// These tests drive the REAL fort update functions (the ones wired into the
// save paths in fortRtree.go) end-to-end and assert the resulting option
// surfaces through the public GetAvailable* readers. Unlike
// fort_availability_test.go (which exercises the observe*/read* primitives
// directly), these exist to catch a deleted observeX(...) call inside an
// update function — a regression the primitive-level tests cannot see,
// since they never call the update functions at all.

// TestUpdateGymLookupHookWiresRaidAvailability must fail if updateGymLookup's
// observeRaid(...) call is removed.
func TestUpdateGymLookupHookWiresRaidAvailability(t *testing.T) {
	initFortAvailability()
	now := time.Now().Unix()

	gym := &Gym{GymData: GymData{
		Id:               "hook-gym-raid",
		Lat:              1,
		Lon:              2,
		RaidLevel:        null.IntFrom(5),
		RaidPokemonId:    null.IntFrom(150),
		RaidPokemonForm:  null.IntFrom(0),
		RaidEndTimestamp: null.IntFrom(now + 1800),
	}}
	updateGymLookup(gym)

	got := GetAvailableGyms(now)
	found := false
	for _, r := range got.Raids {
		if r.RaidLevel == 5 && r.PokemonId == 150 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected raid option from updateGymLookup to surface via GetAvailableGyms, got %+v", got.Raids)
	}
}

// TestUpdateStationLookupWithBattlesHookWiresBattleAvailability must fail if
// updateStationLookupWithBattles's observeStationBattles(...) call is removed.
func TestUpdateStationLookupWithBattlesHookWiresBattleAvailability(t *testing.T) {
	initFortAvailability()
	now := time.Now().Unix()

	station := &Station{StationData: StationData{
		Id:        "hook-station-battle",
		Lat:       1,
		Lon:       2,
		StartTime: now - 3600,
		EndTime:   now + 3600,
		Updated:   now,
	}}
	battles := []StationBattleData{
		{
			StationId:       station.Id,
			BattleLevel:     3,
			BattleStart:     now - 60,
			BattleEnd:       now + 1800,
			BattlePokemonId: null.IntFrom(527),
		},
	}
	updateStationLookupWithBattles(station, battles)

	got := GetAvailableStations(now)
	found := false
	for _, b := range got.Battles {
		if b.BattleLevel == 3 && b.PokemonId == 527 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected battle option from updateStationLookupWithBattles to surface via GetAvailableStations, got %+v", got.Battles)
	}
}

// TestUpdatePokestopLookupHookWiresLureAndShowcaseAvailability must fail if
// updatePokestopLookup's observePokestop(...) call is removed.
func TestUpdatePokestopLookupHookWiresLureAndShowcaseAvailability(t *testing.T) {
	initFortAvailability()
	initQuestConditions() // updatePokestopLookup also reconciles quest conditions
	now := time.Now().Unix()

	stop := &Pokestop{PokestopData: PokestopData{
		Id:                  "hook-stop-lure-showcase",
		Lat:                 1,
		Lon:                 2,
		LureId:              501,
		LureExpireTimestamp: null.IntFrom(now + 1800),
		ShowcasePokemon:     null.IntFrom(25),
		ShowcasePokemonForm: null.IntFrom(0),
		ShowcasePokemonType: null.IntFrom(0),
		ShowcaseExpiry:      null.IntFrom(now + 1800),
	}}
	updatePokestopLookup(stop)

	got := GetAvailablePokestops(now)

	foundLure := false
	for _, l := range got.Lures {
		if l.LureId == 501 {
			foundLure = true
			break
		}
	}
	if !foundLure {
		t.Fatalf("expected lure option from updatePokestopLookup to surface via GetAvailablePokestops, got %+v", got.Lures)
	}

	foundShowcase := false
	for _, s := range got.Showcases {
		if s.PokemonId == 25 {
			foundShowcase = true
			break
		}
	}
	if !foundShowcase {
		t.Fatalf("expected showcase option from updatePokestopLookup to surface via GetAvailablePokestops, got %+v", got.Showcases)
	}
}

// TestUpdatePokestopIncidentLookupHookWiresInvasionAvailability must fail if
// updatePokestopIncidentLookup's observeInvasion(...) call is removed. The
// pokestop's FortLookup is seeded resident first (as fort_incident_id_test.go
// does), matching how a real incident save always follows a resident stop.
func TestUpdatePokestopIncidentLookupHookWiresInvasionAvailability(t *testing.T) {
	initFortAvailability()
	now := time.Now().Unix()

	const id = "hook-stop-invasion"
	fortLookupCache.Store(id, FortLookup{FortType: POKESTOP, Lat: 1, Lon: 2})

	inc := &Incident{IncidentData: IncidentData{
		Id:             "hook-incident-1",
		DisplayType:    1,
		Character:      5,
		Confirmed:      true,
		Slot1PokemonId: null.IntFrom(41),
		ExpirationTime: now + 1800,
	}}
	updatePokestopIncidentLookup(id, inc)

	got := GetAvailablePokestops(now)
	found := false
	for _, iv := range got.Invasions {
		if iv.Character == 5 && iv.Slot1PokemonId == 41 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected invasion option from updatePokestopIncidentLookup to surface via GetAvailablePokestops, got %+v", got.Invasions)
	}
}
