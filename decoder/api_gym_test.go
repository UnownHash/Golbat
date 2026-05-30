package decoder

import (
	"encoding/json"
	"testing"

	"github.com/guregu/null/v6"
)

// goldenSnapshotGym is a representative gym with a mix of set and unset (null)
// fields across every nullable column, used to pin the exact wire format.
func goldenSnapshotGym() *Gym {
	return &Gym{
		GymData: GymData{
			Id:                    "gym-abc",
			Lat:                   12.3456,
			Lon:                   -65.4321,
			Name:                  null.StringFrom("Test Gym"),
			Url:                   null.StringFrom("https://example.com/gym.png"),
			LastModifiedTimestamp: null.IntFrom(1699990000),
			RaidEndTimestamp:      null.IntFrom(1700003600),
			// RaidSpawnTimestamp intentionally left null
			RaidBattleTimestamp: null.IntFrom(1700000000),
			Updated:             1699999999,
			RaidPokemonId:       null.IntFrom(150),
			GuardingPokemonId:   null.IntFrom(143),
			// GuardingPokemonDisplay intentionally left null
			AvailableSlots:       null.IntFrom(3),
			TeamId:               null.IntFrom(2),
			RaidLevel:            null.IntFrom(5),
			Enabled:              null.IntFrom(1),
			ExRaidEligible:       null.IntFrom(0),
			InBattle:             null.IntFrom(0),
			RaidPokemonMove1:     null.IntFrom(216),
			RaidPokemonMove2:     null.IntFrom(94),
			RaidPokemonForm:      null.IntFrom(0),
			RaidPokemonAlignment: null.IntFrom(0),
			RaidPokemonCp:        null.IntFrom(3500),
			RaidIsExclusive:      null.IntFrom(0),
			CellId:               null.IntFrom(1234567890123),
			Deleted:              false,
			TotalCp:              null.IntFrom(12000),
			FirstSeenTimestamp:   1699990000,
			RaidPokemonGender:    null.IntFrom(1),
			// SponsorId intentionally left null
			PartnerId:            null.StringFrom("partner-1"),
			RaidPokemonCostume:   null.IntFrom(0),
			RaidPokemonEvolution: null.IntFrom(0),
			ArScanEligible:       null.IntFrom(1),
			PowerUpLevel:         null.IntFrom(2),
			PowerUpPoints:        null.IntFrom(50),
			// PowerUpEndTimestamp intentionally left null
			Description: null.StringFrom("A test gym"),
			// Defenders intentionally left null
			Rsvps: null.StringFrom("[]"),
		},
	}
}

// TestBuildGymResult_GoldenSnapshot pins the exact JSON wire format of an
// ApiGymResult. Any accidental change to a json tag, field type, pointer/null
// handling, or field order will fail this test. Unset nullable fields serialize
// as null (pointers are nil, no omitempty).
func TestBuildGymResult_GoldenSnapshot(t *testing.T) {
	got, err := json.Marshal(buildGymResult(goldenSnapshotGym()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	const want = `{"id":"gym-abc","lat":12.3456,"lon":-65.4321,"name":"Test Gym","url":"https://example.com/gym.png","last_modified_timestamp":1699990000,"raid_end_timestamp":1700003600,"raid_spawn_timestamp":null,"raid_battle_timestamp":1700000000,"updated":1699999999,"raid_pokemon_id":150,"guarding_pokemon_id":143,"guarding_pokemon_display":null,"available_slots":3,"team_id":2,"raid_level":5,"enabled":1,"ex_raid_eligible":0,"in_battle":0,"raid_pokemon_move_1":216,"raid_pokemon_move_2":94,"raid_pokemon_form":0,"raid_pokemon_alignment":0,"raid_pokemon_cp":3500,"raid_is_exclusive":0,"cell_id":1234567890123,"deleted":false,"total_cp":12000,"first_seen_timestamp":1699990000,"raid_pokemon_gender":1,"sponsor_id":null,"partner_id":"partner-1","raid_pokemon_costume":0,"raid_pokemon_evolution":0,"ar_scan_eligible":1,"power_up_level":2,"power_up_points":50,"power_up_end_timestamp":null,"description":"A test gym","defenders":null,"rsvps":"[]"}`

	if string(got) != want {
		t.Errorf("wire format changed.\n got: %s\nwant: %s", got, want)
	}
}
