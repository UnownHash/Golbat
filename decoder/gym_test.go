package decoder

import (
	"encoding/json"
	"testing"

	"golbat/pogo"
)

func TestUpdateGymFromGymInfoOutProtoExtractsFortFields(t *testing.T) {
	lastModifiedMs := int64(9_876_543_210)

	gym := &Gym{}
	gymInfo := &pogo.GymGetInfoOutProto{
		GymStatusAndDefenders: &pogo.GymStatusAndDefendersProto{
			PokemonFortProto: &pogo.PokemonFortProto{
				FortId:         "gym-1",
				Latitude:       51.5014,
				Longitude:      -0.1419,
				Team:           pogo.Team_TEAM_RED,
				GuardPokemonId: pogo.HoloPokemonId_BULBASAUR,
				GuardPokemonDisplay: &pogo.PokemonDisplayProto{
					Form: pogo.PokemonDisplayProto_Form(1),
				},
				GymDisplay: &pogo.GymDisplayProto{
					SlotsAvailable: 2,
					TotalGymCp:     1_500,
				},
				LastModifiedMs:   lastModifiedMs,
				Enabled:          true,
				IsExRaidEligible: true,
				IsInBattle:       true,
			},
			GymDefender: []*pogo.GymDefenderProto{
				{
					MotivatedPokemon: &pogo.MotivatedPokemonProto{
						Pokemon: &pogo.PokemonProto{
							PokemonId:      pogo.HoloPokemonId_SQUIRTLE,
							PokemonDisplay: &pogo.PokemonDisplayProto{},
						},
						CpNow:          1_200,
						CpWhenDeployed: 1_300,
						MotivationNow:  0.75,
					},
					DeploymentTotals: &pogo.DeploymentTotalsProto{
						BattlesLost:          1,
						BattlesWon:           5,
						TimesFed:             3,
						DeploymentDurationMs: 60_000,
					},
				},
			},
		},
		Name:        "Test Gym",
		Url:         "https://example.com/gym.jpg",
		Description: "Gym description",
	}

	gym.updateGymFromGymInfoOutProto(gymInfo)

	if gym.Id != "gym-1" {
		t.Fatalf("expected gym id to be gym-1, got %s", gym.Id)
	}

	if got := gym.TeamId.ValueOrZero(); got != int64(pogo.Team_TEAM_RED) {
		t.Fatalf("expected team id %d, got %d", pogo.Team_TEAM_RED, got)
	}

	if got := gym.AvailableSlots.ValueOrZero(); got != 2 {
		t.Fatalf("expected available slots 2, got %d", got)
	}

	if got := gym.GuardingPokemonId.ValueOrZero(); got != int64(pogo.HoloPokemonId_BULBASAUR) {
		t.Fatalf("expected guarding pokemon %d, got %d", pogo.HoloPokemonId_BULBASAUR, got)
	}

	if got := gym.InBattle.ValueOrZero(); got != 1 {
		t.Fatalf("expected in_battle 1, got %d", got)
	}

	if got := gym.LastModifiedTimestamp.ValueOrZero(); got != lastModifiedMs/1000 {
		t.Fatalf("expected last_modified_timestamp %d, got %d", lastModifiedMs/1000, got)
	}

	if gym.CellId.Valid {
		t.Fatalf("expected cell_id to remain invalid when unknown")
	}

	if got := gym.Name.ValueOrZero(); got != "Test Gym" {
		t.Fatalf("expected name %q, got %q", "Test Gym", got)
	}

	if got := gym.Url.ValueOrZero(); got != "https://example.com/gym.jpg" {
		t.Fatalf("expected url %q, got %q", "https://example.com/gym.jpg", got)
	}

	if got := gym.Description.ValueOrZero(); got != "Gym description" {
		t.Fatalf("expected description %q, got %q", "Gym description", got)
	}

	if !gym.Defenders.Valid {
		t.Fatalf("expected defenders json to be stored")
	}

	var storedDefenders []map[string]any
	if err := json.Unmarshal([]byte(gym.Defenders.ValueOrZero()), &storedDefenders); err != nil {
		t.Fatalf("failed to unmarshal defenders json: %v", err)
	}

	if len(storedDefenders) != 1 {
		t.Fatalf("expected 1 defender entry, got %d", len(storedDefenders))
	}

	if cpNow, ok := storedDefenders[0]["cp_now"].(float64); !ok || cpNow != 1200 {
		t.Fatalf("expected defender cp_now 1200, got %#v", storedDefenders[0]["cp_now"])
	}
}
