package decoder

import (
	"encoding/json"
	"testing"

	"golbat/pogo"
)

func TestUpdateGymFromGymInfoOutProto_DefenderTradedTime(t *testing.T) {
	defender := func(pokemonId pogo.HoloPokemonId, tradedTimeMs int64) *pogo.GymDefenderProto {
		return &pogo.GymDefenderProto{
			MotivatedPokemon: &pogo.MotivatedPokemonProto{
				Pokemon: &pogo.PokemonProto{
					PokemonId:      pokemonId,
					PokemonDisplay: &pogo.PokemonDisplayProto{},
					TradedTimeMs:   tradedTimeMs,
				},
			},
			DeploymentTotals: &pogo.DeploymentTotalsProto{},
		}
	}
	const tradedAt = int64(1700000000123)
	gymInfo := &pogo.GymGetInfoOutProto{
		GymStatusAndDefenders: &pogo.GymStatusAndDefendersProto{
			PokemonFortProto: &pogo.PokemonFortProto{},
			GymDefender: []*pogo.GymDefenderProto{
				defender(pogo.HoloPokemonId_SNORLAX, tradedAt),
				defender(pogo.HoloPokemonId_BLISSEY, 0),
			},
		},
	}

	gym := &Gym{}
	gym.updateGymFromGymInfoOutProto(mustFortId(t, "00000000000000000000000000000001"), gymInfo)

	var defenders []map[string]any
	if err := json.Unmarshal([]byte(gym.Defenders.String), &defenders); err != nil {
		t.Fatalf("defenders JSON %q: %v", gym.Defenders.String, err)
	}
	if len(defenders) != 2 {
		t.Fatalf("got %d defenders, want 2: %s", len(defenders), gym.Defenders.String)
	}
	if got, ok := defenders[0]["traded_time_ms"].(float64); !ok || int64(got) != tradedAt {
		t.Errorf("traded defender traded_time_ms = %v, want %d", defenders[0]["traded_time_ms"], tradedAt)
	}
	if v, ok := defenders[1]["traded_time_ms"]; ok {
		t.Errorf("never-traded defender traded_time_ms = %v, want omitted", v)
	}
}
