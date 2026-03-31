package decoder

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/guregu/null/v6"

	"golbat/db"
	"golbat/geo"
	"golbat/pogo"
	"golbat/stats_collector"
	"golbat/webhooks"
)

type recordingWebhooksSender struct {
	messages []webhooks.WebhookType
}

func (sender *recordingWebhooksSender) AddMessage(whType webhooks.WebhookType, _ any, _ []geo.AreaName) {
	sender.messages = append(sender.messages, whType)
}

type recordingStatsCollector struct {
	stats_collector.StatsCollector
	maxBattleLevels []int64
}

func (collector *recordingStatsCollector) UpdateMaxBattleCount(_ []geo.AreaName, level int64) {
	collector.maxBattleLevels = append(collector.maxBattleLevels, level)
}

func TestUpsertCachedStationBattleIgnoresUpdatedOnlyChange(t *testing.T) {
	initStationBattleCache()
	now := time.Now().Unix()
	battle := StationBattleData{
		BreadBattleSeed: 1,
		StationId:       "station-1",
		BattleLevel:     1,
		BattleStart:     now - 60,
		BattleEnd:       now + 3600,
		BattlePokemonId: null.IntFrom(527),
		Updated:         now,
	}

	if !upsertCachedStationBattle(battle, now) {
		t.Fatal("expected first insert to change cache")
	}

	battle.Updated = now + 120
	if upsertCachedStationBattle(battle, now) {
		t.Fatal("expected updated-only change to be ignored")
	}
}

func TestCanonicalStationBattleUsesLatestEnd(t *testing.T) {
	initStationBattleCache()
	now := time.Now().Unix()

	upsertCachedStationBattle(StationBattleData{
		BreadBattleSeed: 1,
		StationId:       "station-1",
		BattleLevel:     1,
		BattleStart:     now - 60,
		BattleEnd:       now + 1800,
		BattlePokemonId: null.IntFrom(527),
	}, now)
	upsertCachedStationBattle(StationBattleData{
		BreadBattleSeed: 2,
		StationId:       "station-1",
		BattleLevel:     2,
		BattleStart:     now - 120,
		BattleEnd:       now + 7200,
		BattlePokemonId: null.IntFrom(133),
	}, now)

	battles := getKnownStationBattles("station-1", nil, now)
	if len(battles) != 2 {
		t.Fatalf("expected 2 battles, got %d", len(battles))
	}
	if battles[0].BreadBattleSeed != 2 {
		t.Fatalf("expected latest-ending battle first, got seed %d", battles[0].BreadBattleSeed)
	}

	canonical := canonicalStationBattleFromSlice(battles, now)
	if canonical == nil || canonical.BreadBattleSeed != 2 {
		t.Fatalf("expected canonical seed 2, got %+v", canonical)
	}
}

func TestBuildStationResultUsesBattleCacheProjection(t *testing.T) {
	initStationBattleCache()
	now := time.Now().Unix()

	station := &Station{
		StationData: StationData{
			Id:              "station-1",
			Name:            "Station",
			Lat:             1,
			Lon:             2,
			StartTime:       now - 3600,
			EndTime:         now + 3600,
			Updated:         now,
			BattleLevel:     null.IntFrom(1),
			BattleStart:     null.IntFrom(now - 60),
			BattleEnd:       null.IntFrom(now + 1800),
			BattlePokemonId: null.IntFrom(527),
		},
	}

	upsertCachedStationBattle(StationBattleData{
		BreadBattleSeed: 1,
		StationId:       station.Id,
		BattleLevel:     1,
		BattleStart:     now - 60,
		BattleEnd:       now + 1800,
		BattlePokemonId: null.IntFrom(527),
	}, now)
	upsertCachedStationBattle(StationBattleData{
		BreadBattleSeed: 2,
		StationId:       station.Id,
		BattleLevel:     2,
		BattleStart:     now - 120,
		BattleEnd:       now + 7200,
		BattlePokemonId: null.IntFrom(133),
	}, now)

	result := BuildStationResult(station)
	if result.BattlePokemonId.ValueOrZero() != 133 {
		t.Fatalf("expected canonical pokemon 133, got %d", result.BattlePokemonId.ValueOrZero())
	}
	if len(result.Battles) != 2 {
		t.Fatalf("expected 2 battles, got %d", len(result.Battles))
	}
}

func TestStationFortFilterMatchesSecondaryBattle(t *testing.T) {
	now := time.Now().Unix()
	filter := ApiFortDnfFilter{
		BattlePokemon: []ApiDnfId{{Pokemon: 133}},
	}
	lookup := FortLookup{
		FortType: STATION,
		StationBattles: []FortLookupStationBattle{
			{BattleEndTimestamp: now + 1800, BattleLevel: 1, BattlePokemonId: 527},
			{BattleEndTimestamp: now + 7200, BattleLevel: 2, BattlePokemonId: 133},
		},
	}

	if !isFortDnfMatch(STATION, &lookup, &filter, now) {
		t.Fatal("expected station filter to match secondary battle")
	}
}

func TestGetActiveStationBattlesKeepsFutureBattleCached(t *testing.T) {
	initStationBattleCache()
	now := time.Now().Unix()
	future := StationBattleData{
		BreadBattleSeed: 1,
		StationId:       "station-1",
		BattleLevel:     1,
		BattleStart:     now + 600,
		BattleEnd:       now + 3600,
		BattlePokemonId: null.IntFrom(527),
	}

	if !upsertCachedStationBattle(future, now) {
		t.Fatal("expected future battle insert to change cache")
	}

	if active := getActiveStationBattles("station-1", nil, now); len(active) != 0 {
		t.Fatalf("expected no active battles, got %d", len(active))
	}

	cached, ok := stationBattleCache.Load("station-1")
	if !ok || len(cached) != 1 {
		t.Fatalf("expected future battle to remain cached, got ok=%t len=%d", ok, len(cached))
	}
	if cached[0].BreadBattleSeed != future.BreadBattleSeed {
		t.Fatalf("expected cached seed %d, got %d", future.BreadBattleSeed, cached[0].BreadBattleSeed)
	}
}

func TestCanonicalStationBattlePrefersActiveOverFuture(t *testing.T) {
	initStationBattleCache()
	now := time.Now().Unix()

	upsertCachedStationBattle(StationBattleData{
		BreadBattleSeed: 1,
		StationId:       "station-1",
		BattleLevel:     1,
		BattleStart:     now - 60,
		BattleEnd:       now + 1800,
		BattlePokemonId: null.IntFrom(527),
	}, now)
	upsertCachedStationBattle(StationBattleData{
		BreadBattleSeed: 2,
		StationId:       "station-1",
		BattleLevel:     2,
		BattleStart:     now + 600,
		BattleEnd:       now + 7200,
		BattlePokemonId: null.IntFrom(133),
	}, now)

	battles := getKnownStationBattles("station-1", nil, now)
	canonical := canonicalStationBattleFromSlice(battles, now)
	if canonical == nil || canonical.BreadBattleSeed != 1 {
		t.Fatalf("expected active battle seed 1 to override future battle, got %+v", canonical)
	}
}

func TestBuildStationResultProjectsFutureBattleFromCache(t *testing.T) {
	initStationBattleCache()
	now := time.Now().Unix()
	station := &Station{
		StationData: StationData{
			Id:                "station-1",
			Name:              "Station",
			Lat:               1,
			Lon:               2,
			StartTime:         now - 3600,
			EndTime:           now + 3600,
			IsBattleAvailable: true,
			Updated:           now,
			BattleLevel:       null.IntFrom(1),
			BattleStart:       null.IntFrom(now + 600),
			BattleEnd:         null.IntFrom(now + 3600),
			BattlePokemonId:   null.IntFrom(527),
		},
	}

	upsertCachedStationBattle(StationBattleData{
		BreadBattleSeed: 1,
		StationId:       station.Id,
		BattleLevel:     1,
		BattleStart:     now + 600,
		BattleEnd:       now + 3600,
		BattlePokemonId: null.IntFrom(527),
	}, now)

	result := BuildStationResult(station)
	if result.BattlePokemonId.ValueOrZero() != 527 {
		t.Fatalf("expected future battle in compatibility fields, got %+v", result)
	}
	if len(result.Battles) != 1 {
		t.Fatalf("expected 1 known battle, got %d", len(result.Battles))
	}
	if !result.IsBattleAvailable {
		t.Fatal("expected server is_battle_available flag to be preserved")
	}
}

func TestBuildFortLookupStationBattlesIncludesFutureBattle(t *testing.T) {
	initStationBattleCache()
	now := time.Now().Unix()
	station := &Station{StationData: StationData{Id: "station-1"}}

	upsertCachedStationBattle(StationBattleData{
		BreadBattleSeed: 1,
		StationId:       station.Id,
		BattleLevel:     1,
		BattleStart:     now + 600,
		BattleEnd:       now + 3600,
		BattlePokemonId: null.IntFrom(527),
	}, now)

	battles := buildFortLookupStationBattles(station, now)
	if len(battles) != 1 {
		t.Fatalf("expected future battle in fort lookup, got %d", len(battles))
	}
	if battles[0].BattlePokemonId != 527 {
		t.Fatalf("expected battle pokemon 527, got %d", battles[0].BattlePokemonId)
	}
}

func TestCreateStationWebhooksSkipsEmptyExistingStation(t *testing.T) {
	initStationBattleCache()
	previousSender := webhooksSender
	previousStats := statsCollector
	sender := &recordingWebhooksSender{}
	webhooksSender = sender
	statsCollector = stats_collector.NewNoopStatsCollector()
	defer func() {
		webhooksSender = previousSender
		statsCollector = previousStats
	}()

	now := time.Now().Unix()
	station := &Station{
		StationData: StationData{
			Id:      "station-1",
			Name:    "Station",
			Lat:     1,
			Lon:     2,
			CellId:  123,
			EndTime: now + 3600,
			Updated: now,
		},
	}
	station.oldValues = StationOldValues{
		EndTime:             now - 3600,
		BattleListSignature: "",
	}

	createStationWebhooks(station)
	if len(sender.messages) != 0 {
		t.Fatalf("expected no max_battle webhook, got %v", sender.messages)
	}
}

func TestCreateStationWebhooksEmitsFutureBattle(t *testing.T) {
	initStationBattleCache()
	previousSender := webhooksSender
	previousStats := statsCollector
	sender := &recordingWebhooksSender{}
	webhooksSender = sender
	statsCollector = &recordingStatsCollector{StatsCollector: stats_collector.NewNoopStatsCollector()}
	defer func() {
		webhooksSender = previousSender
		statsCollector = previousStats
	}()

	now := time.Now().Unix()
	station := &Station{
		StationData: StationData{
			Id:                "station-1",
			Name:              "Station",
			Lat:               1,
			Lon:               2,
			CellId:            123,
			EndTime:           now + 7200,
			IsBattleAvailable: false,
			Updated:           now,
		},
	}
	upsertCachedStationBattle(StationBattleData{
		BreadBattleSeed: 1,
		StationId:       station.Id,
		BattleLevel:     1,
		BattleStart:     now + 600,
		BattleEnd:       now + 3600,
		BattlePokemonId: null.IntFrom(527),
	}, now)
	station.oldValues = StationOldValues{
		EndTime:             station.EndTime,
		BattleListSignature: "",
	}

	createStationWebhooks(station)
	if len(sender.messages) != 1 || sender.messages[0] != webhooks.MaxBattle {
		t.Fatalf("expected one max_battle webhook, got %v", sender.messages)
	}
}

func TestCreateStationWebhooksDoesNotRecountCanonicalBattleSeed(t *testing.T) {
	initStationBattleCache()
	previousSender := webhooksSender
	previousStats := statsCollector
	sender := &recordingWebhooksSender{}
	collector := &recordingStatsCollector{StatsCollector: stats_collector.NewNoopStatsCollector()}
	webhooksSender = sender
	statsCollector = collector
	defer func() {
		webhooksSender = previousSender
		statsCollector = previousStats
	}()

	now := time.Now().Unix()
	station := &Station{
		StationData: StationData{
			Id:      "station-1",
			Name:    "Station",
			Lat:     1,
			Lon:     2,
			CellId:  123,
			EndTime: now + 7200,
			Updated: now,
		},
	}
	upsertCachedStationBattle(StationBattleData{
		BreadBattleSeed: 1,
		StationId:       station.Id,
		BattleLevel:     1,
		BattleStart:     now - 600,
		BattleEnd:       now + 3600,
		BattlePokemonId: null.IntFrom(527),
	}, now)
	upsertCachedStationBattle(StationBattleData{
		BreadBattleSeed: 2,
		StationId:       station.Id,
		BattleLevel:     2,
		BattleStart:     now + 600,
		BattleEnd:       now + 7200,
		BattlePokemonId: null.IntFrom(133),
	}, now)
	station.oldValues = StationOldValues{
		CanonicalBattleSeed: 1,
		EndTime:             station.EndTime,
		BattleListSignature: "old-signature",
	}

	createStationWebhooks(station)
	if len(sender.messages) != 1 || sender.messages[0] != webhooks.MaxBattle {
		t.Fatalf("expected one max_battle webhook, got %v", sender.messages)
	}
	if len(collector.maxBattleLevels) != 0 {
		t.Fatalf("expected no max battle metric increment, got %v", collector.maxBattleLevels)
	}
}

func TestSyncStationBattlesFromProtoKeepsFreshProjectionWhenSeedMissing(t *testing.T) {
	initStationBattleCache()
	now := time.Now().Unix()
	station := &Station{
		StationData: StationData{
			Id:              "station-1",
			BattleLevel:     null.IntFrom(2),
			BattleStart:     null.IntFrom(now - 60),
			BattleEnd:       null.IntFrom(now + 3600),
			BattlePokemonId: null.IntFrom(133),
		},
	}
	upsertCachedStationBattle(StationBattleData{
		BreadBattleSeed: 99,
		StationId:       station.Id,
		BattleLevel:     1,
		BattleStart:     now - 60,
		BattleEnd:       now + 7200,
		BattlePokemonId: null.IntFrom(527),
	}, now)

	syncStationBattlesFromProto(context.Background(), db.DbDetails{}, station, &pogo.BreadBattleDetailProto{
		BreadBattleSeed:     0,
		BattleWindowStartMs: (now - 60) * 1000,
		BattleWindowEndMs:   (now + 3600) * 1000,
		BattleLevel:         pogo.BreadBattleLevel_BREAD_BATTLE_LEVEL_2,
	})

	if station.BattlePokemonId.ValueOrZero() != 133 {
		t.Fatalf("expected fresh station projection to win, got %d", station.BattlePokemonId.ValueOrZero())
	}
	if _, ok := stationBattleCache.Load(station.Id); ok {
		t.Fatal("expected stale station battle cache to be cleared")
	}
}

func TestSyncStationBattlesFromProtoKeepsFreshProjectionOnUpsertFailure(t *testing.T) {
	initStationBattleCache()
	now := time.Now().Unix()
	station := &Station{
		StationData: StationData{
			Id:              "station-1",
			BattleLevel:     null.IntFrom(2),
			BattleStart:     null.IntFrom(now - 60),
			BattleEnd:       null.IntFrom(now + 3600),
			BattlePokemonId: null.IntFrom(133),
		},
	}
	upsertCachedStationBattle(StationBattleData{
		BreadBattleSeed: 99,
		StationId:       station.Id,
		BattleLevel:     1,
		BattleStart:     now - 60,
		BattleEnd:       now + 7200,
		BattlePokemonId: null.IntFrom(527),
	}, now)

	previousUpsert := upsertStationBattleRecordFunc
	upsertStationBattleRecordFunc = func(context.Context, db.DbDetails, StationBattleData) error {
		return errors.New("boom")
	}
	defer func() {
		upsertStationBattleRecordFunc = previousUpsert
	}()

	syncStationBattlesFromProto(context.Background(), db.DbDetails{}, station, &pogo.BreadBattleDetailProto{
		BreadBattleSeed:     7,
		BattleWindowStartMs: (now - 60) * 1000,
		BattleWindowEndMs:   (now + 3600) * 1000,
		BattleLevel:         pogo.BreadBattleLevel_BREAD_BATTLE_LEVEL_2,
		BattlePokemon:       &pogo.PokemonProto{PokemonId: 133},
	})

	if station.BattlePokemonId.ValueOrZero() != 133 {
		t.Fatalf("expected fresh station projection to win, got %d", station.BattlePokemonId.ValueOrZero())
	}
	if _, ok := stationBattleCache.Load(station.Id); ok {
		t.Fatal("expected stale station battle cache to be cleared")
	}
}
