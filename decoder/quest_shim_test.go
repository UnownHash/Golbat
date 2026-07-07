package decoder

import (
	"encoding/json"
	"testing"

	"buf.build/go/hyperpb"
	"google.golang.org/protobuf/proto"

	"golbat/pogo"
	"golbat/pogoshim"
)

// buildQuestFortSearchOutProto builds a synthetic FortSearchOutProto covering:
//   - an AR-eligible first reward (ITEM) plus a second reward (STARDUST), to
//     lock in the "first reward populates indexed columns" behavior.
//   - a WITH_ITEM condition (scalar oneof member, [] int{}-then-append list).
//   - two WITH_LOCATION conditions: one with cell ids (non-empty ScalarList)
//     and one with none, to lock in the nil-vs-empty-slice JSON distinction
//     the migration must preserve (info.S2CellId was a raw, possibly-nil
//     []int64 field in the pre-shim code -- json.Marshal renders nil as null
//     and []int64{} as [], so the shim path must not silently upgrade an
//     absent list to an empty-but-present one).
func buildQuestFortSearchOutProto() *pogo.FortSearchOutProto {
	return &pogo.FortSearchOutProto{
		Result: pogo.FortSearchOutProto_SUCCESS,
		FortId: "STOP1",
		ChallengeQuest: &pogo.ClientQuestProto{
			Quest: &pogo.QuestProto{
				QuestType:  pogo.QuestType_QUEST_CATCH_POKEMON,
				TemplateId: "Some_Template_ID",
				QuestSeed:  424242,
				Goal: &pogo.QuestGoalProto{
					Target: 3,
					Condition: []*pogo.QuestConditionProto{
						{
							Type: pogo.QuestConditionProto_WITH_ITEM,
							Condition: &pogo.QuestConditionProto_WithItem{
								WithItem: &pogo.WithItemProto{Item: pogo.Item_ITEM_POKE_BALL},
							},
						},
						{
							Type: pogo.QuestConditionProto_WITH_LOCATION,
							Condition: &pogo.QuestConditionProto_WithLocation{
								WithLocation: &pogo.WithLocationProto{S2CellId: []int64{111, 222}},
							},
						},
						{
							Type: pogo.QuestConditionProto_WITH_LOCATION,
							Condition: &pogo.QuestConditionProto_WithLocation{
								WithLocation: &pogo.WithLocationProto{}, // no cell ids
							},
						},
					},
				},
				QuestRewards: []*pogo.QuestRewardProto{
					{
						Type: pogo.QuestRewardProto_ITEM,
						Reward: &pogo.QuestRewardProto_Item{
							Item: &pogo.ItemRewardProto{Item: pogo.Item_ITEM_RAZZ_BERRY, Amount: 3},
						},
					},
					{
						Type:   pogo.QuestRewardProto_STARDUST,
						Reward: &pogo.QuestRewardProto_Stardust{Stardust: 500},
					},
				},
			},
			QuestDisplay: &pogo.QuestDisplayProto{
				Description: "Catch 3 Pokemon",
			},
		},
	}
}

// buildHiddenDittoQuestFortSearchOutProto covers the POKEMON_ENCOUNTER reward
// branch, including the is_hidden_ditto special case (pokemon_id forced to
// 132 regardless of the wire PokemonId) and a non-zero display form (to
// exercise the "form_id populates the indexed column" path).
func buildHiddenDittoQuestFortSearchOutProto() *pogo.FortSearchOutProto {
	return &pogo.FortSearchOutProto{
		Result: pogo.FortSearchOutProto_SUCCESS,
		FortId: "STOP1",
		ChallengeQuest: &pogo.ClientQuestProto{
			Quest: &pogo.QuestProto{
				QuestType:  pogo.QuestType_QUEST_CATCH_POKEMON,
				TemplateId: "Alt_Template",
				QuestSeed:  99,
				Goal: &pogo.QuestGoalProto{
					Target: 1,
				},
				QuestRewards: []*pogo.QuestRewardProto{
					{
						Type: pogo.QuestRewardProto_POKEMON_ENCOUNTER,
						Reward: &pogo.QuestRewardProto_PokemonEncounter{
							PokemonEncounter: &pogo.PokemonEncounterRewardProto{
								Type:             &pogo.PokemonEncounterRewardProto_PokemonId{PokemonId: pogo.HoloPokemonId_PIKACHU},
								IsHiddenDitto:    true,
								ShinyProbability: 0.05,
								PokemonDisplay: &pogo.PokemonDisplayProto{
									Form:    pogo.PokemonDisplayProto_UNOWN_A, // non-zero, so form_id path is exercised
									Gender:  pogo.PokemonDisplayProto_MALE,
									Shiny:   true,
									Costume: pogo.PokemonDisplayProto_UNSET,
								},
							},
						},
					},
				},
			},
			QuestDisplay: &pogo.QuestDisplayProto{
				Description: "Catch a hidden Ditto",
			},
		},
	}
}

// questConditions/questRewards unmarshal QuestConditions/QuestRewards (or
// their Alternative equivalents) JSON strings into generic maps for
// key-level assertions (map key order from json.Marshal of map[string]any is
// alphabetical/deterministic, but asserting individual keys is more robust to
// incidental reordering of the source switch).
func questConditions(t *testing.T, s string) []map[string]any {
	t.Helper()
	var out []map[string]any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		t.Fatalf("unmarshal conditions %q: %s", s, err)
	}
	return out
}

func questRewards(t *testing.T, s string) []map[string]any {
	t.Helper()
	var out []map[string]any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		t.Fatalf("unmarshal rewards %q: %s", s, err)
	}
	return out
}

// checkArQuest asserts the AR-branch entity fields/JSON produced by
// buildQuestFortSearchOutProto, for a given engine's shim wrap.
func checkArQuest(t *testing.T, name string, shim pogoshim.FortSearchOutProto) {
	t.Helper()
	stop := &Pokestop{}
	title := stop.updatePokestopFromQuestProto(shim, true)

	if got, want := title, "Catch 3 Pokemon"; got != want {
		t.Errorf("%s: title = %q, want %q", name, got, want)
	}
	if got, want := stop.QuestType.ValueOrZero(), int64(pogo.QuestType_QUEST_CATCH_POKEMON); got != want {
		t.Errorf("%s: QuestType = %d, want %d", name, got, want)
	}
	if got, want := stop.QuestTarget.ValueOrZero(), int64(3); got != want {
		t.Errorf("%s: QuestTarget = %d, want %d", name, got, want)
	}
	if got, want := stop.QuestTemplate.ValueOrZero(), "some_template_id"; got != want {
		t.Errorf("%s: QuestTemplate = %q, want %q", name, got, want)
	}
	if got, want := stop.QuestSeed.ValueOrZero(), int64(424242); got != want {
		t.Errorf("%s: QuestSeed = %d, want %d", name, got, want)
	}
	// First reward (ITEM) populates the indexed reward columns.
	if got, want := stop.QuestRewardType.ValueOrZero(), int64(pogo.QuestRewardProto_ITEM); got != want {
		t.Errorf("%s: QuestRewardType = %d, want %d", name, got, want)
	}
	if got, want := stop.QuestItemId.ValueOrZero(), int64(pogo.Item_ITEM_RAZZ_BERRY); got != want {
		t.Errorf("%s: QuestItemId = %d, want %d", name, got, want)
	}
	if got, want := stop.QuestRewardAmount.ValueOrZero(), int64(3); got != want {
		t.Errorf("%s: QuestRewardAmount = %d, want %d", name, got, want)
	}

	conds := questConditions(t, stop.QuestConditions.ValueOrZero())
	if got, want := len(conds), 3; got != want {
		t.Fatalf("%s: len(conditions) = %d, want %d", name, got, want)
	}
	item0 := conds[0]["info"].(map[string]any)
	if got, want := int(item0["item_id"].(float64)), int(pogo.Item_ITEM_POKE_BALL); got != want {
		t.Errorf("%s: condition[0].item_id = %d, want %d", name, got, want)
	}
	withLoc := conds[1]["info"].(map[string]any)
	cellIds, ok := withLoc["cell_ids"].([]any)
	if !ok || len(cellIds) != 2 {
		t.Fatalf("%s: condition[1].cell_ids = %v, want [111 222]", name, withLoc["cell_ids"])
	}
	if got, want := int64(cellIds[0].(float64)), int64(111); got != want {
		t.Errorf("%s: condition[1].cell_ids[0] = %d, want %d", name, got, want)
	}
	// The empty-S2CellId WITH_LOCATION condition must marshal cell_ids as
	// JSON null (nil slice), not [] -- this is the nil-vs-empty preservation
	// the migration's ScalarList materialization must get right.
	emptyLoc := conds[2]["info"].(map[string]any)
	if v, present := emptyLoc["cell_ids"]; !present || v != nil {
		t.Errorf("%s: condition[2].cell_ids = %v (%T), want JSON null", name, v, v)
	}

	rews := questRewards(t, stop.QuestRewards.ValueOrZero())
	if got, want := len(rews), 2; got != want {
		t.Fatalf("%s: len(rewards) = %d, want %d", name, got, want)
	}
	if got, want := int(rews[0]["type"].(float64)), int(pogo.QuestRewardProto_ITEM); got != want {
		t.Errorf("%s: rewards[0].type = %d, want %d", name, got, want)
	}
	if got, want := int(rews[1]["type"].(float64)), int(pogo.QuestRewardProto_STARDUST); got != want {
		t.Errorf("%s: rewards[1].type = %d, want %d", name, got, want)
	}
	stardustInfo := rews[1]["info"].(map[string]any)
	if got, want := int(stardustInfo["amount"].(float64)), 500; got != want {
		t.Errorf("%s: rewards[1].info.amount = %d, want %d", name, got, want)
	}

	// Non-AR (Alternative*) columns must be untouched by an AR quest.
	if stop.AlternativeQuestType.Valid {
		t.Errorf("%s: AlternativeQuestType should be unset for an AR quest, got %v", name, stop.AlternativeQuestType)
	}
}

// checkHiddenDittoQuest asserts the non-AR branch entity fields produced by
// buildHiddenDittoQuestFortSearchOutProto, for a given engine's shim wrap.
func checkHiddenDittoQuest(t *testing.T, name string, shim pogoshim.FortSearchOutProto) {
	t.Helper()
	stop := &Pokestop{}
	stop.updatePokestopFromQuestProto(shim, false)

	if got, want := stop.AlternativeQuestRewardType.ValueOrZero(), int64(pogo.QuestRewardProto_POKEMON_ENCOUNTER); got != want {
		t.Errorf("%s: AlternativeQuestRewardType = %d, want %d", name, got, want)
	}
	// is_hidden_ditto forces pokemon_id to 132 regardless of the wire PokemonId.
	if got, want := stop.AlternativeQuestPokemonId.ValueOrZero(), int64(132); got != want {
		t.Errorf("%s: AlternativeQuestPokemonId = %d, want %d (hidden ditto)", name, got, want)
	}
	if got, want := stop.AlternativeQuestPokemonFormId.ValueOrZero(), int64(pogo.PokemonDisplayProto_UNOWN_A); got != want {
		t.Errorf("%s: AlternativeQuestPokemonFormId = %d, want %d", name, got, want)
	}

	rews := questRewards(t, stop.AlternativeQuestRewards.ValueOrZero())
	if got, want := len(rews), 1; got != want {
		t.Fatalf("%s: len(rewards) = %d, want %d", name, got, want)
	}
	info := rews[0]["info"].(map[string]any)
	if got, want := int(info["pokemon_id"].(float64)), 132; got != want {
		t.Errorf("%s: rewards[0].info.pokemon_id = %d, want %d", name, got, want)
	}
	if _, present := info["pokemon_id_display"]; !present {
		t.Errorf("%s: rewards[0].info.pokemon_id_display missing", name)
	}
	// info.GetShinyProbability() is a float32 field; json.Marshal renders it
	// with float32 (not float64) precision, so the round-tripped value here
	// is the decimal "0.05", not the wider float64(float32(0.05)) bit
	// pattern -- compare against the decimal literal.
	if got, want := info["shiny_probability"].(float64), 0.05; got != want {
		t.Errorf("%s: rewards[0].info.shiny_probability = %v, want %v", name, got, want)
	}
	if got, want := int(info["form_id"].(float64)), int(pogo.PokemonDisplayProto_UNOWN_A); got != want {
		t.Errorf("%s: rewards[0].info.form_id = %d, want %d", name, got, want)
	}

	// AR columns must be untouched by a non-AR quest.
	if stop.QuestType.Valid {
		t.Errorf("%s: QuestType should be unset for a non-AR quest, got %v", name, stop.QuestType)
	}
}

// hyperpbWrapFortSearch marshals in and returns a hyperpb-backed shim; the
// returned Shared must be Freed by the caller once done with the shim (and
// everything reachable from it).
func hyperpbWrapFortSearch(t *testing.T, in *pogo.FortSearchOutProto) (pogoshim.FortSearchOutProto, *hyperpb.Shared) {
	t.Helper()
	raw, err := proto.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	ty := hyperpb.CompileMessageDescriptor((*pogo.FortSearchOutProto)(nil).ProtoReflect().Descriptor())
	shared := new(hyperpb.Shared)
	msg := shared.NewMessage(ty)
	if err := msg.Unmarshal(raw); err != nil {
		shared.Free()
		t.Fatal(err)
	}
	return pogoshim.AsFortSearchOutProto(msg.ProtoReflect()), shared
}

// TestUpdatePokestopFromQuestProtoShim locks in Wave 3 Task 2 behavior for
// the hyperpb migration: updatePokestopFromQuestProto's ~280-line
// condition/reward switch must extract identical entity state (and,
// critically, identical nil-vs-empty JSON list rendering) via pogoshim
// getters as the pre-migration code extracted via direct *pogo.X field
// access and oneof wrapper type assertions. Runs each scenario through both
// the std and hyperpb wraps.
func TestUpdatePokestopFromQuestProtoShim(t *testing.T) {
	arIn := buildQuestFortSearchOutProto()
	checkArQuest(t, "std", pogoshim.AsFortSearchOutProto(arIn.ProtoReflect()))
	hyperShim, shared := hyperpbWrapFortSearch(t, arIn)
	checkArQuest(t, "hyperpb", hyperShim)
	shared.Free()

	dittoIn := buildHiddenDittoQuestFortSearchOutProto()
	checkHiddenDittoQuest(t, "std", pogoshim.AsFortSearchOutProto(dittoIn.ProtoReflect()))
	hyperDittoShim, dittoShared := hyperpbWrapFortSearch(t, dittoIn)
	checkHiddenDittoQuest(t, "hyperpb", hyperDittoShim)
	dittoShared.Free()
}

// TestUpdatePokestopFromQuestProtoShim_BlankQuest locks in the "no
// ChallengeQuest" short-circuit -- HasChallengeQuest() must correctly report
// false (not chase a zero-value shim into the reward/condition switch) for
// both engines.
func TestUpdatePokestopFromQuestProtoShim_BlankQuest(t *testing.T) {
	in := &pogo.FortSearchOutProto{Result: pogo.FortSearchOutProto_SUCCESS, FortId: "STOP1"}

	stop := &Pokestop{}
	if got, want := stop.updatePokestopFromQuestProto(pogoshim.AsFortSearchOutProto(in.ProtoReflect()), true), "Blank quest"; got != want {
		t.Errorf("std: title = %q, want %q", got, want)
	}
	if stop.QuestType.Valid {
		t.Errorf("std: QuestType should remain unset, got %v", stop.QuestType)
	}

	hyperShim, shared := hyperpbWrapFortSearch(t, in)
	defer shared.Free()
	stop2 := &Pokestop{}
	if got, want := stop2.updatePokestopFromQuestProto(hyperShim, true), "Blank quest"; got != want {
		t.Errorf("hyperpb: title = %q, want %q", got, want)
	}
}

// TestMapFortSummaryRoundTrip locks in the getMapFortsCache value fix: the
// cache must store a plain mapFortSummary value (never the shim or the
// arena-backed proto it wraps), extracted at Set time via
// mapFortSummaryFromShim, and the Gym/Pokestop consumers must read the same
// fields back correctly regardless of which engine produced the summary.
func TestMapFortSummaryRoundTrip(t *testing.T) {
	in := &pogo.GetMapFortsOutProto_FortProto{
		Id:        "FORT1",
		Name:      "Test Fort",
		Latitude:  12.345,
		Longitude: -67.891,
		Image: []*pogo.GetMapFortsOutProto_Image{
			{Url: "https://example.com/fort.png"},
		},
	}

	check := func(name string, shim pogoshim.GetMapFortsOutProto_FortProto) {
		summary := mapFortSummaryFromShim(shim)
		if got, want := summary.Id, "FORT1"; got != want {
			t.Errorf("%s: Id = %q, want %q", name, got, want)
		}
		if got, want := summary.Latitude, 12.345; got != want {
			t.Errorf("%s: Latitude = %v, want %v", name, got, want)
		}
		if got, want := summary.Longitude, -67.891; got != want {
			t.Errorf("%s: Longitude = %v, want %v", name, got, want)
		}
		if got, want := summary.ImageUrl, "https://example.com/fort.png"; got != want {
			t.Errorf("%s: ImageUrl = %q, want %q", name, got, want)
		}
		if got, want := summary.Name, "Test Fort"; got != want {
			t.Errorf("%s: Name = %q, want %q", name, got, want)
		}

		gym := &Gym{}
		gym.updateGymFromMapFortSummary(summary, false)
		if got, want := gym.Id, "FORT1"; got != want {
			t.Errorf("%s: gym.Id = %q, want %q", name, got, want)
		}
		if got, want := gym.Url.ValueOrZero(), "https://example.com/fort.png"; got != want {
			t.Errorf("%s: gym.Url = %q, want %q", name, got, want)
		}
		if got, want := gym.Name.ValueOrZero(), "Test Fort"; got != want {
			t.Errorf("%s: gym.Name = %q, want %q", name, got, want)
		}

		stop := &Pokestop{}
		stop.updatePokestopFromMapFortSummary(summary)
		if got, want := stop.Id, "FORT1"; got != want {
			t.Errorf("%s: stop.Id = %q, want %q", name, got, want)
		}
		if got, want := stop.Url.ValueOrZero(), "https://example.com/fort.png"; got != want {
			t.Errorf("%s: stop.Url = %q, want %q", name, got, want)
		}
		if got, want := stop.Name.ValueOrZero(), "Test Fort"; got != want {
			t.Errorf("%s: stop.Name = %q, want %q", name, got, want)
		}
	}

	check("std", pogoshim.AsGetMapFortsOutProto_FortProto(in.ProtoReflect()))

	raw, err := proto.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	ty := hyperpb.CompileMessageDescriptor((*pogo.GetMapFortsOutProto_FortProto)(nil).ProtoReflect().Descriptor())
	shared := new(hyperpb.Shared)
	defer shared.Free()
	msg := shared.NewMessage(ty)
	if err := msg.Unmarshal(raw); err != nil {
		t.Fatal(err)
	}
	check("hyperpb", pogoshim.AsGetMapFortsOutProto_FortProto(msg.ProtoReflect()))
}

// TestMapFortSummary_NoImage locks in the "Image[0].Url with Len guard"
// extraction rule: a fort with zero images must yield an empty ImageUrl
// (and the Gym/Pokestop consumers must then leave Url untouched), not panic
// on an out-of-range index.
func TestMapFortSummary_NoImage(t *testing.T) {
	in := &pogo.GetMapFortsOutProto_FortProto{Id: "FORT2", Name: "No Image Fort"}
	summary := mapFortSummaryFromShim(pogoshim.AsGetMapFortsOutProto_FortProto(in.ProtoReflect()))
	if summary.ImageUrl != "" {
		t.Errorf("ImageUrl = %q, want empty", summary.ImageUrl)
	}

	gym := &Gym{}
	gym.updateGymFromMapFortSummary(summary, false)
	if gym.Url.Valid {
		t.Errorf("gym.Url = %v, want unset", gym.Url)
	}
}
