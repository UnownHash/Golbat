package decoder

import (
	"encoding/json"
	"testing"

	"golbat/pogo"

	"google.golang.org/protobuf/proto"
)

func TestUpdatePokestopFromQuestProtoTempEvoBranchResource(t *testing.T) {
	stop := &Pokestop{}
	quest := &pogo.FortSearchOutProto{
		ChallengeQuest: &pogo.ClientQuestProto{
			Quest: &pogo.QuestProto{
				Goal:       &pogo.QuestGoalProto{},
				TemplateId: "QUEST_TEMP_EVO_RESOURCE",
				QuestRewards: []*pogo.QuestRewardProto{{
					Type: pogo.QuestRewardProto_TEMP_EVO_BRANCH_RESOURCE,
					Reward: &pogo.QuestRewardProto_TempEvoResource{
						TempEvoResource: &pogo.TempEvoResourceRewardProto{
							Amount: 150,
							TempEvoPokemonBranch: &pogo.TempEvoPokemonBranch{
								PokedexId: pogo.HoloPokemonId_MEWTWO,
								TempEvoId: pogo.HoloTemporaryEvolutionId_TEMP_EVOLUTION_MEGA_X,
							},
						},
					},
				}},
			},
			QuestDisplay: &pogo.QuestDisplayProto{Description: "Earn Mega Energy"},
		},
	}

	rawQuest, err := proto.Marshal(quest)
	if err != nil {
		t.Fatalf("marshal quest: %v", err)
	}
	var decodedQuest pogo.FortSearchOutProto
	if err := proto.Unmarshal(rawQuest, &decodedQuest); err != nil {
		t.Fatalf("unmarshal quest: %v", err)
	}

	stop.updatePokestopFromQuestProto(&decodedQuest, true)

	if got := stop.QuestRewardType.ValueOrZero(); got != 20 {
		t.Errorf("QuestRewardType = %d, want 20", got)
	}
	if got := stop.QuestRewardAmount.ValueOrZero(); got != 150 {
		t.Errorf("QuestRewardAmount = %d, want 150", got)
	}
	if got := stop.QuestPokemonId.ValueOrZero(); got != 150 {
		t.Errorf("QuestPokemonId = %d, want 150", got)
	}

	var rewards []struct {
		Type int `json:"type"`
		Info struct {
			Amount        int `json:"amount"`
			PokemonId     int `json:"pokemon_id"`
			TempEvolution int `json:"temp_evolution"`
		} `json:"info"`
	}
	if err := json.Unmarshal([]byte(stop.QuestRewards.ValueOrZero()), &rewards); err != nil {
		t.Fatalf("unmarshal QuestRewards: %v", err)
	}
	if len(rewards) != 1 {
		t.Fatalf("len(QuestRewards) = %d, want 1", len(rewards))
	}
	if got := rewards[0]; got.Type != 20 || got.Info.Amount != 150 || got.Info.PokemonId != 150 || got.Info.TempEvolution != 2 {
		t.Errorf("QuestRewards[0] = %+v, want type 20, amount 150, pokemon 150, temp evolution 2", got)
	}
}
