package decoder

import (
	"cmp"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/guregu/null/v6"

	"golbat/db"
	"golbat/decoder/writebehind"
	"golbat/jsonenc"
	"golbat/pogo"
	"golbat/stats_collector"
	"golbat/webhooks"
)

func TestTappableWebhookGolden(t *testing.T) {
	ta := &Tappable{TappableData: TappableData{
		Id: 18446744073709551000, Lat: 47.3769, Lon: 8.5417, Type: "TAPPABLE_TYPE_BREAKFAST",
		SpawnId: null.IntFrom(0x3b7a1c), RewardType: null.StringFrom("pokemon_candy"),
		RewardPokemonId: null.IntFrom(25), Count: null.IntFrom(6),
		Rewards:         null.StringFrom(`[{"pokemon_candy":25,"count":6}]`),
		ExpireTimestamp: null.IntFrom(1761660000), ExpireTimestampVerified: true, Updated: 1761659400,
	}}
	got, err := jsonenc.Marshal(buildTappableWebhook(ta, nil))
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"encounter_id":"18446744073709551000","spawnpoint_id":"3b7a1c","pokestop_id":"None","pokestop_name":null,"latitude":47.3769,"longitude":8.5417,"type":"TAPPABLE_TYPE_BREAKFAST","reward_type":"pokemon_candy","item_id":null,"reward_pokemon_id":25,"count":6,"rewards":[{"pokemon_candy":25,"count":6}],"disappear_time":1761660000,"disappear_time_verified":true,"updated":1761659400}`
	if string(got) != want {
		t.Errorf("payload changed.\n got: %s\nwant: %s", got, want)
	}
}

func TestTappableWebhookFortAndUnknownExpiry(t *testing.T) {
	ta := &Tappable{TappableData: TappableData{Id: 1, FortId: mustFortId(t, "0123456789abcdef0123456789abcdef")}}
	name := "Fountain"
	got, err := jsonenc.Marshal(buildTappableWebhook(ta, &name))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"spawnpoint_id":"None"`, `"pokestop_id":"0123456789abcdef0123456789abcdef"`, `"pokestop_name":"Fountain"`, `"disappear_time":0`, `"rewards":null`} {
		if !strings.Contains(string(got), want) {
			t.Errorf("payload missing %s: %s", want, got)
		}
	}
}

// withTestTappableQueue gives saveTappableRecord a write-behind queue that is
// never started, so dirty saves need no DB.
func withTestTappableQueue(t *testing.T) {
	t.Helper()
	prev := tappableQueue
	tappableQueue = writebehind.NewTypedQueue(writebehind.TypedQueueConfig[uint64, TappableData]{
		Name:       "tappable-test",
		BatchSize:  50,
		Db:         db.DbDetails{},
		Stats:      stats_collector.NewNoopStatsCollector(),
		FlushFunc:  func(context.Context, db.DbDetails, []TappableData) error { return nil },
		KeyFunc:    func(d TappableData) uint64 { return d.Id },
		KeyCompare: cmp.Compare[uint64],
	})
	t.Cleanup(func() { tappableQueue = prev })
}

// recordTappableWebhooks swaps webhooksSender for one test and returns the
// sender plus a counter of tappable hooks sent so far.
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

func itemReward(itemId, count int64) tappableRewards {
	return tappableRewards{Type: null.StringFrom("item"), ItemId: null.IntFrom(itemId), Count: null.IntFrom(count)}
}

// newSaveTappable is a new (not yet saved) tappable, cached and removed on cleanup.
func newSaveTappable(t *testing.T, id uint64) *Tappable {
	t.Helper()
	ta := &Tappable{TappableData: TappableData{Id: id, Lat: 1, Lon: 2, ExpireTimestamp: null.IntFrom(time.Now().Unix() + 600)}, newRecord: true}
	tappableCache.Set(id, ta, time.Minute)
	t.Cleanup(func() { tappableCache.Delete(id) })
	return ta
}

func save(ta *Tappable) { saveTappableRecord(context.Background(), db.DbDetails{}, ta, nil) }

func TestTappableRewardChangedFlag(t *testing.T) {
	for name, tc := range map[string]struct {
		next tappableRewards
		want bool
	}{
		"same type":    {itemReward(1301, 1), false},
		"count only":   {itemReward(1301, 2), false},
		"type changed": {tappableRewards{Type: null.StringFrom("pokemon_candy"), PokemonId: null.IntFrom(25), Count: null.IntFrom(6)}, true},
		"to encounter": {tappableRewards{}, true}, // set, but nothing sends: RewardType is null
	} {
		t.Run(name, func(t *testing.T) {
			ta := &Tappable{}
			ta.setRewards(itemReward(1301, 1))
			ta.rewardChanged = false
			ta.setRewards(tc.next)
			if ta.rewardChanged != tc.want {
				t.Errorf("rewardChanged = %v, want %v", ta.rewardChanged, tc.want)
			}
		})
	}
}

func TestTappableWebhookTrigger(t *testing.T) {
	withTestTappableQueue(t)
	sender, count := recordTappableWebhooks(t)

	ta := newSaveTappable(t, 950001)
	ta.setRewards(itemReward(1301, 1))
	save(ta)
	if count() != 1 {
		t.Fatalf("new reward: %d hooks, want 1", count())
	}
	if p, ok := sender.payloads[len(sender.payloads)-1].(TappableWebhook); !ok || p.RewardType != null.StringFrom("item") || p.EncounterId != "950001" {
		t.Errorf("payload = %#v, want TappableWebhook item 950001", sender.payloads[len(sender.payloads)-1])
	}

	ta.SetType("TAPPABLE_TYPE_BREAKFAST") // dirty, reward unchanged
	save(ta)
	if count() != 1 {
		t.Errorf("same reward resaved: %d hooks, want 1", count())
	}
	if ta.rewardChanged {
		t.Error("rewardChanged not cleared after save")
	}

	enc := newSaveTappable(t, 950002)
	enc.SetEncounter(null.IntFrom(25))
	save(enc)
	if count() != 1 {
		t.Errorf("encounter: %d hooks, want 1", count())
	}
}

func TestTappableWebhookCleanDbLoadedSave(t *testing.T) {
	_, count := recordTappableWebhooks(t)
	ta := newIndexedTappable(950003, 12.1, 22.1)
	tappableCache.Set(950003, ta, time.Minute)
	t.Cleanup(func() { tappableCache.Delete(950003) })

	save(ta)
	if count() != 0 {
		t.Errorf("clean save: %d hooks, want 0", count())
	}
}

func TestTappableWebhookRewardReturnsAfterEncounter(t *testing.T) {
	withTestTappableQueue(t)
	_, count := recordTappableWebhooks(t)
	ta := newSaveTappable(t, 950004)
	ta.setRewards(itemReward(1301, 1))
	save(ta)
	ta.setRewards(tappableRewards{})
	ta.SetEncounter(null.IntFrom(25))
	save(ta)
	ta.SetEncounter(null.Int{})
	ta.setRewards(itemReward(1301, 1))
	save(ta)
	if count() != 2 {
		t.Errorf("reward → encounter → reward: %d hooks, want 2", count())
	}
}

func TestTappableWebhookDbLoadedRewardArrives(t *testing.T) {
	withTestTappableQueue(t)
	_, count := recordTappableWebhooks(t)
	ta := newSaveTappable(t, 950005)
	ta.newRecord = false // as loaded from the DB, no reward yet
	ta.setRewards(itemReward(1301, 1))
	save(ta)
	if count() != 1 {
		t.Errorf("DB-loaded tappable, first reward: %d hooks, want 1", count())
	}
}

func TestTappableWebhookSameRewardFortRetap(t *testing.T) {
	withTestTappableQueue(t)
	_, count := recordTappableWebhooks(t)
	loc := &pogo.TappableLocation{LocationId: &pogo.TappableLocation_FortId{FortId: "0123456789abcdef0123456789abcdef"}}
	ta := newSaveTappable(t, 42)
	decodeTappableAt(t, ta, loc, 1700000000000)
	save(ta)
	decodeTappableAt(t, ta, loc, 1700000005000)
	save(ta)
	if count() != 1 {
		t.Errorf("fort re-tap, same reward: %d hooks, want 1", count())
	}
}

func TestUpdateTappableIgnoresMissingEncounterId(t *testing.T) {
	_, count := recordTappableWebhooks(t)
	out := &pogo.ProcessTappableOutProto{Reward: []*pogo.LootProto{{LootItem: []*pogo.LootItemProto{
		{Type: &pogo.LootItemProto_Item{Item: pogo.Item_ITEM_RARE_CANDY}, Count: 1},
	}}}}
	UpdateTappable(context.Background(), db.DbDetails{}, &pogo.ProcessTappableProto{}, out, 1700000000000)
	if tappableCache.Has(0) || count() != 0 {
		t.Errorf("encounter id 0: cached %v, %d hooks; want not cached, 0", tappableCache.Has(0), count())
	}
}
