package decoder

import (
	"context"
	"testing"
	"time"

	"github.com/guregu/null/v6"

	"golbat/db"
	"golbat/jsonenc"
	"golbat/pogo"
	"golbat/webhooks"
)

func TestTappableWebhookGolden(t *testing.T) {
	ta := &Tappable{TappableData: TappableData{
		Id: 18446744073709551000, Lat: 47.3769, Lon: 8.5417, Type: "TAPPABLE_TYPE_BREAKFAST",
		SpawnId: null.IntFrom(987654321), RewardType: null.StringFrom("pokemon_candy"),
		RewardPokemonId: null.IntFrom(25), Count: null.IntFrom(6),
		Rewards:         null.StringFrom(`[{"pokemon_candy":25,"count":6}]`),
		ExpireTimestamp: null.IntFrom(1761660000), ExpireTimestampVerified: true, Updated: 1761659400,
	}}
	got, err := jsonenc.Marshal(buildTappableWebhook(ta))
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"id":"18446744073709551000","latitude":47.3769,"longitude":8.5417,"type":"TAPPABLE_TYPE_BREAKFAST","fort_id":null,"spawn_id":987654321,"reward_type":"pokemon_candy","item_id":null,"reward_pokemon_id":25,"count":6,"rewards":[{"pokemon_candy":25,"count":6}],"expire_timestamp":1761660000,"expire_timestamp_verified":true,"updated":1761659400}`
	if string(got) != want {
		t.Errorf("payload changed.\n got: %s\nwant: %s", got, want)
	}
}

// recordTappableWebhooks swaps webhooksSender for one test and returns a
// function counting the tappable hooks sent so far.
func recordTappableWebhooks(t *testing.T) (*recordingWebhooksSender, func() int) {
	t.Helper()
	sender := &recordingWebhooksSender{}
	prev := webhooksSender
	webhooksSender = sender
	t.Cleanup(func() { webhooksSender = prev })
	return sender, func() int {
		n := 0
		for _, m := range sender.messages {
			if m == webhooks.Tappable {
				n++
			}
		}
		return n
	}
}

func rewardTappable(itemId, count int64) *Tappable {
	ta := &Tappable{TappableData: TappableData{Id: 950001, Lat: 1, Lon: 2}}
	ta.setRewards(tappableRewards{Type: null.StringFrom("item"), ItemId: null.IntFrom(itemId), Count: null.IntFrom(count),
		Rewards: null.StringFrom(`[{"item":1301,"count":1}]`)})
	return ta
}

func TestTappableRewardChangedFlag(t *testing.T) {
	item := tappableRewards{Type: null.StringFrom("item"), ItemId: null.IntFrom(1301), Count: null.IntFrom(1), Rewards: null.StringFrom(`[{"item":1301,"count":1}]`)}
	for name, tc := range map[string]struct {
		next tappableRewards
		want bool
	}{
		"same reward":     {item, false},
		"count changed":   {tappableRewards{Type: item.Type, ItemId: item.ItemId, Count: null.IntFrom(2), Rewards: item.Rewards}, true},
		"species changed": {tappableRewards{Type: null.StringFrom("pokemon_candy"), PokemonId: null.IntFrom(26), Count: item.Count}, true},
		"to encounter":    {tappableRewards{}, true},
		"rewards only":    {tappableRewards{Type: item.Type, ItemId: item.ItemId, Count: item.Count, Rewards: null.StringFrom(`[{"item":1301,"count":1},{"stardust":true,"count":100}]`)}, false},
	} {
		t.Run(name, func(t *testing.T) {
			ta := &Tappable{}
			ta.setRewards(item)
			ta.rewardChanged = false
			ta.setRewards(tc.next)
			if ta.rewardChanged != tc.want {
				t.Errorf("rewardChanged = %v, want %v", ta.rewardChanged, tc.want)
			}
		})
	}
}

func TestTappableWebhookTrigger(t *testing.T) {
	sender, count := recordTappableWebhooks(t)

	ta := rewardTappable(1301, 1)
	tappableWebhookOnSave(ta, true)
	if count() != 1 {
		t.Fatalf("new reward: %d hooks, want 1", count())
	}
	if p, ok := sender.payloads[len(sender.payloads)-1].(TappableWebhook); !ok || p.RewardType == nil || *p.RewardType != "item" {
		t.Errorf("payload = %#v, want TappableWebhook with reward_type item", sender.payloads[len(sender.payloads)-1])
	}
	if ta.rewardChanged {
		t.Error("rewardChanged not cleared after send")
	}

	ta.SetUpdated(123) // dirty, reward unchanged
	tappableWebhookOnSave(ta, false)
	if count() != 1 {
		t.Errorf("same reward resaved: %d hooks, want 1", count())
	}

	ta.setRewards(tappableRewards{Type: null.StringFrom("item"), ItemId: null.IntFrom(1301), Count: null.IntFrom(3)})
	tappableWebhookOnSave(ta, false)
	if count() != 2 {
		t.Errorf("changed reward: %d hooks, want 2", count())
	}

	enc := &Tappable{TappableData: TappableData{Id: 950002, Encounter: null.IntFrom(25)}}
	tappableWebhookOnSave(enc, true)
	if count() != 2 {
		t.Errorf("encounter: %d hooks, want 2", count())
	}
}

func TestTappableWebhookCleanDbLoadedSave(t *testing.T) {
	_, count := recordTappableWebhooks(t)
	ta := newIndexedTappable(950003, 12.1, 22.1)
	ta.rewardChanged = true // must be cleared without sending
	tappableCache.Set(950003, ta, time.Minute)
	t.Cleanup(func() { tappableCache.Delete(950003) })

	saveTappableRecord(context.Background(), db.DbDetails{}, ta)
	if count() != 0 || ta.rewardChanged {
		t.Errorf("clean save: %d hooks, rewardChanged %v; want 0, false", count(), ta.rewardChanged)
	}
}

func TestTappableWebhookRewardReturnsAfterEncounter(t *testing.T) {
	_, count := recordTappableWebhooks(t)
	ta := rewardTappable(1301, 1)
	tappableWebhookOnSave(ta, true)
	ta.setRewards(tappableRewards{})
	ta.SetEncounter(null.IntFrom(25))
	tappableWebhookOnSave(ta, false)
	ta.SetEncounter(null.Int{})
	ta.setRewards(tappableRewards{Type: null.StringFrom("item"), ItemId: null.IntFrom(1301), Count: null.IntFrom(1)})
	tappableWebhookOnSave(ta, false)
	if count() != 2 {
		t.Errorf("reward → encounter → reward: %d hooks, want 2", count())
	}
}

func TestTappableWebhookDbLoadedRewardChange(t *testing.T) {
	_, count := recordTappableWebhooks(t)
	ta := rewardTappable(1301, 1)
	ta.rewardChanged = false // as loaded from the DB
	ta.setRewards(tappableRewards{Type: null.StringFrom("pokemon_candy"), PokemonId: null.IntFrom(25), Count: null.IntFrom(6)})
	tappableWebhookOnSave(ta, false)
	if count() != 1 {
		t.Errorf("DB-loaded reward change: %d hooks, want 1", count())
	}
}

func TestTappableWebhookSameRewardFortRetap(t *testing.T) {
	_, count := recordTappableWebhooks(t)
	loc := &pogo.TappableLocation{LocationId: &pogo.TappableLocation_FortId{FortId: "0123456789abcdef0123456789abcdef"}}
	ta := &Tappable{}
	decodeTappableAt(t, ta, loc, 1700000000000)
	tappableWebhookOnSave(ta, true)
	decodeTappableAt(t, ta, loc, 1700000005000)
	tappableWebhookOnSave(ta, false)
	if count() != 1 {
		t.Errorf("fort re-tap, same reward: %d hooks, want 1", count())
	}
}
