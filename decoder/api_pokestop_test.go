package decoder

import (
	"encoding/json"
	"testing"

	"github.com/guregu/null/v6"
)

// goldenSnapshotPokestop is a representative pokestop with a mix of set and
// unset (null) fields across every nullable column, used to pin the exact wire
// format.
func goldenSnapshotPokestop() *Pokestop {
	return &Pokestop{
		PokestopData: PokestopData{
			Id:   "stop-abc",
			Lat:  12.3456,
			Lon:  -65.4321,
			Name: null.StringFrom("Test Pokestop"),
			Url:  null.StringFrom("https://example.com/stop.png"),
			// LureExpireTimestamp intentionally left null
			LastModifiedTimestamp: null.IntFrom(1699990000),
			Updated:               1699999999,
			Enabled:               null.BoolFrom(true),
			QuestType:             null.IntFrom(7),
			QuestTimestamp:        null.IntFrom(1699991000),
			QuestTarget:           null.IntFrom(3),
			QuestRewardType:       null.IntFrom(1),
			QuestRewardAmount:     null.IntFrom(100),
			// QuestItemId, QuestPokemonId, QuestPokemonFormId left null (xp reward)
			QuestConditions: null.StringFrom("[]"),
			QuestRewards:    null.StringFrom("[{\"type\":1}]"),
			QuestTemplate:   null.StringFrom("challenge_template"),
			// QuestTitle intentionally left null
			QuestExpiry: null.IntFrom(1700003600),
			CellId:      null.IntFrom(1234567890123),
			Deleted:     false,
			LureId:      501,
			// FirstSeenTimestamp is int16, plain field
			FirstSeenTimestamp: 0,
			// SponsorId intentionally left null
			PartnerId:                    null.StringFrom("partner-1"),
			ArScanEligible:               null.IntFrom(1),
			PowerUpLevel:                 null.IntFrom(2),
			PowerUpPoints:                null.IntFrom(50),
			PowerUpEndTimestamp:          null.IntFrom(1700007200),
			AlternativeQuestType:         null.IntFrom(7),
			AlternativeQuestTimestamp:    null.IntFrom(1699992000),
			AlternativeQuestTarget:       null.IntFrom(5),
			AlternativeQuestRewardType:   null.IntFrom(2),
			AlternativeQuestItemId:       null.IntFrom(1),
			AlternativeQuestRewardAmount: null.IntFrom(3),
			// AlternativeQuestPokemonId, AlternativeQuestPokemonFormId left null (item reward)
			// AlternativeQuestConditions intentionally left null
			AlternativeQuestRewards:  null.StringFrom("[{\"type\":2}]"),
			AlternativeQuestTemplate: null.StringFrom("alt_template"),
			AlternativeQuestTitle:    null.StringFrom("Alt Quest"),
			AlternativeQuestExpiry:   null.IntFrom(1700003601),
			Description:              null.StringFrom("A test pokestop"),
			// ShowcaseFocus intentionally left null
			ShowcasePokemon:         null.IntFrom(150),
			ShowcasePokemonForm:     null.IntFrom(0),
			ShowcasePokemonType:     null.IntFrom(1),
			ShowcaseRankingStandard: null.IntFrom(0),
			// ShowcaseExpiry intentionally left null
			ShowcaseRankings: null.StringFrom("[]"),
		},
	}
}

// TestBuildPokestopResult_GoldenSnapshot pins the exact JSON wire format of an
// ApiPokestopResult. Any accidental change to a json tag, field type,
// pointer/null handling, or field order will fail this test. Unset nullable
// fields serialize as null (pointers are nil, no omitempty).
func TestBuildPokestopResult_GoldenSnapshot(t *testing.T) {
	got, err := json.Marshal(buildPokestopResult(goldenSnapshotPokestop()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	const want = `{"id":"stop-abc","lat":12.3456,"lon":-65.4321,"name":"Test Pokestop","url":"https://example.com/stop.png","lure_expire_timestamp":null,"last_modified_timestamp":1699990000,"updated":1699999999,"enabled":true,"quest_type":7,"quest_timestamp":1699991000,"quest_target":3,"quest_reward_type":1,"quest_item_id":null,"quest_reward_amount":100,"quest_pokemon_id":null,"quest_pokemon_form_id":null,"quest_conditions":"[]","quest_rewards":[{"type":1}],"quest_template":"challenge_template","quest_title":null,"quest_expiry":1700003600,"cell_id":1234567890123,"deleted":false,"lure_id":501,"first_seen_timestamp":0,"sponsor_id":null,"partner_id":"partner-1","ar_scan_eligible":1,"power_up_level":2,"power_up_points":50,"power_up_end_timestamp":1700007200,"alternative_quest_type":7,"alternative_quest_timestamp":1699992000,"alternative_quest_target":5,"alternative_quest_reward_type":2,"alternative_quest_item_id":1,"alternative_quest_reward_amount":3,"alternative_quest_pokemon_id":null,"alternative_quest_pokemon_form_id":null,"alternative_quest_conditions":null,"alternative_quest_rewards":[{"type":2}],"alternative_quest_template":"alt_template","alternative_quest_title":"Alt Quest","alternative_quest_expiry":1700003601,"description":"A test pokestop","showcase_focus":null,"showcase_pokemon_id":150,"showcase_pokemon_form_id":0,"showcase_pokemon_type_id":1,"showcase_ranking_standard":0,"showcase_expiry":null,"showcase_rankings":"[]"}`

	if string(got) != want {
		t.Errorf("wire format changed.\n got: %s\nwant: %s", got, want)
	}
}
