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

func TestQuestDistributionPokemonId(t *testing.T) {
	tests := []struct {
		templateId string
		want       pogo.HoloPokemonId
	}{
		{"QUEST_POKEMON_ENCOUNTER_SINISTEA_HALLOWEEN_2026", pogo.HoloPokemonId_SINISTEA},
		{"QUEST_POKEMON_ENCOUNTER_MR_MIME_EVENT", pogo.HoloPokemonId_MR_MIME},
		{"QUEST_POKEMON_ENCOUNTER_MIME_JR", pogo.HoloPokemonId_MIME_JR},
		{"QUEST_POKEMON_ENCOUNTER_NOT_A_SPECIES", pogo.HoloPokemonId_MISSINGNO},
		{"QUEST_POKEMON_ENCOUNTER_MISSINGNO", pogo.HoloPokemonId_MISSINGNO},
		{"SINISTEA_HALLOWEEN_2026", pogo.HoloPokemonId_MISSINGNO},
		{"", pogo.HoloPokemonId_MISSINGNO},
	}
	for _, tt := range tests {
		if got := questDistributionPokemonId(tt.templateId); got != tt.want {
			t.Errorf("questDistributionPokemonId(%q) = %v, want %v", tt.templateId, got, tt.want)
		}
	}
}

func TestUpdatePokestopFromQuestProtoEncounterDistribution(t *testing.T) {
	const templateId = "QUEST_POKEMON_ENCOUNTER_SINISTEA_HALLOWEEN_2026"
	quest := func(templateId string) *pogo.FortSearchOutProto {
		return &pogo.FortSearchOutProto{
			ChallengeQuest: &pogo.ClientQuestProto{
				Quest: &pogo.QuestProto{
					Goal:       &pogo.QuestGoalProto{},
					TemplateId: "QUEST_SINISTEA",
					QuestRewards: []*pogo.QuestRewardProto{{
						Type: pogo.QuestRewardProto_POKEMON_ENCOUNTER,
						Reward: &pogo.QuestRewardProto_PokemonEncounter{
							PokemonEncounter: &pogo.PokemonEncounterRewardProto{
								Type: &pogo.PokemonEncounterRewardProto_PokemonEncounterDistribution{
									PokemonEncounterDistribution: &pogo.PokemonEncounterDistributionRewardProto{
										QuestDistributionTemplateId: templateId,
									},
								},
								ShinyProbability: 1.0 / 128,
							},
						},
					}},
				},
				QuestDisplay: &pogo.QuestDisplayProto{Description: "Catch a Sinistea"},
			},
		}
	}

	stop := &Pokestop{}
	stop.updatePokestopFromQuestProto(quest(templateId), true)

	if got := stop.QuestPokemonId.ValueOrZero(); got != 854 {
		t.Errorf("QuestPokemonId = %d, want 854", got)
	}
	var rewards []struct {
		Info struct {
			PokemonId              int    `json:"pokemon_id"`
			DistributionTemplateId string `json:"distribution_template_id"`
		} `json:"info"`
	}
	if err := json.Unmarshal([]byte(stop.QuestRewards.ValueOrZero()), &rewards); err != nil {
		t.Fatalf("unmarshal QuestRewards: %v", err)
	}
	if len(rewards) != 1 || rewards[0].Info.PokemonId != 854 || rewards[0].Info.DistributionTemplateId != templateId {
		t.Errorf("QuestRewards = %+v, want pokemon 854 with template %s", rewards, templateId)
	}

	unresolved := &Pokestop{}
	unresolved.updatePokestopFromQuestProto(quest("QUEST_POKEMON_ENCOUNTER_UNKNOWN"), true)
	if unresolved.QuestPokemonId.Valid {
		t.Errorf("QuestPokemonId = %d, want null for unresolved template", unresolved.QuestPokemonId.Int64)
	}
}

func TestUpdatePokestopFromQuestProtoEncounterWithoutId(t *testing.T) {
	stop := &Pokestop{}
	stop.updatePokestopFromQuestProto(&pogo.FortSearchOutProto{
		ChallengeQuest: &pogo.ClientQuestProto{
			Quest: &pogo.QuestProto{
				Goal: &pogo.QuestGoalProto{},
				QuestRewards: []*pogo.QuestRewardProto{{
					Type: pogo.QuestRewardProto_POKEMON_ENCOUNTER,
					Reward: &pogo.QuestRewardProto_PokemonEncounter{
						PokemonEncounter: &pogo.PokemonEncounterRewardProto{},
					},
				}},
			},
			QuestDisplay: &pogo.QuestDisplayProto{},
		},
	}, true)
	if !stop.QuestPokemonId.Valid || stop.QuestPokemonId.Int64 != 0 {
		t.Errorf("QuestPokemonId = %+v, want valid 0", stop.QuestPokemonId)
	}
}
