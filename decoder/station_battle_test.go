package decoder

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
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

func TestUpsertCachedStationBattleDropsEarlierEndAfterLaterObservation(t *testing.T) {
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
		BattleStart:     now - 60,
		BattleEnd:       now + 3600,
		BattlePokemonId: null.IntFrom(133),
	}, now)

	battles := getKnownStationBattles("station-1", nil, now)
	if len(battles) != 1 {
		t.Fatalf("expected 1 battle after later observation, got %d", len(battles))
	}
	if battles[0].BreadBattleSeed != 2 {
		t.Fatalf("expected seed 2 to replace earlier battle, got %d", battles[0].BreadBattleSeed)
	}
}

func TestUpsertCachedStationBattleReplacesEqualEndBattle(t *testing.T) {
	initStationBattleCache()
	now := time.Now().Unix()

	upsertCachedStationBattle(StationBattleData{
		BreadBattleSeed: 1,
		StationId:       "station-1",
		BattleLevel:     1,
		BattleStart:     now - 120,
		BattleEnd:       now + 3600,
		BattlePokemonId: null.IntFrom(527),
	}, now)

	upsertCachedStationBattle(StationBattleData{
		BreadBattleSeed: 2,
		StationId:       "station-1",
		BattleLevel:     2,
		BattleStart:     now - 60,
		BattleEnd:       now + 3600,
		BattlePokemonId: null.IntFrom(133),
	}, now)

	battles := getKnownStationBattles("station-1", nil, now)
	if len(battles) != 1 {
		t.Fatalf("expected 1 battle after equal-end replacement, got %d", len(battles))
	}
	if battles[0].BreadBattleSeed != 2 {
		t.Fatalf("expected latest equal-end seed 2, got %d", battles[0].BreadBattleSeed)
	}
}

func TestUpsertCachedStationBattleKeepsLongerBattleWhenShorterObserved(t *testing.T) {
	initStationBattleCache()
	now := time.Now().Unix()

	upsertCachedStationBattle(StationBattleData{
		BreadBattleSeed: 1,
		StationId:       "station-1",
		BattleLevel:     3,
		BattleStart:     now - 120,
		BattleEnd:       now + 7200,
		BattlePokemonId: null.IntFrom(374),
	}, now)

	upsertCachedStationBattle(StationBattleData{
		BreadBattleSeed: 2,
		StationId:       "station-1",
		BattleLevel:     1,
		BattleStart:     now - 60,
		BattleEnd:       now + 1800,
		BattlePokemonId: null.IntFrom(527),
	}, now)

	battles := getKnownStationBattles("station-1", nil, now)
	if len(battles) != 2 {
		t.Fatalf("expected longer and shorter battles to coexist, got %d", len(battles))
	}
	if battles[0].BreadBattleSeed != 1 || battles[1].BreadBattleSeed != 2 {
		t.Fatalf("unexpected battle ordering after shorter observation: %+v", battles)
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
	if len(battles) != 1 {
		t.Fatalf("expected later-ending battle to replace earlier one, got %d battles", len(battles))
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
	if len(result.Battles) != 1 {
		t.Fatalf("expected 1 battle after later-ending replacement, got %d", len(result.Battles))
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

func TestCanonicalStationBattleKeepsLongerBattleWhenShorterFutureObserved(t *testing.T) {
	initStationBattleCache()
	now := time.Now().Unix()

	upsertCachedStationBattle(StationBattleData{
		BreadBattleSeed: 1,
		StationId:       "station-1",
		BattleLevel:     3,
		BattleStart:     now - 120,
		BattleEnd:       now + 7200,
		BattlePokemonId: null.IntFrom(374),
	}, now)
	upsertCachedStationBattle(StationBattleData{
		BreadBattleSeed: 2,
		StationId:       "station-1",
		BattleLevel:     2,
		BattleStart:     now + 600,
		BattleEnd:       now + 1800,
		BattlePokemonId: null.IntFrom(527),
	}, now)

	battles := getKnownStationBattles("station-1", nil, now)
	canonical := canonicalStationBattleFromSlice(battles, now)
	if canonical == nil || canonical.BreadBattleSeed != 1 {
		t.Fatalf("expected longer existing battle seed 1 to remain canonical, got %+v", canonical)
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

func TestCachePreloadedStationBattlesPreservesPersistedSetRegardlessOfInputOrder(t *testing.T) {
	initStationBattleCache()
	now := time.Now().Unix()

	if !cachePreloadedStationBattles("station-1", []StationBattleData{
		{
			BreadBattleSeed: 2,
			StationId:       "station-1",
			BattleLevel:     1,
			BattleStart:     now + 600,
			BattleEnd:       now + 1800,
			BattlePokemonId: null.IntFrom(527),
		},
		{
			BreadBattleSeed: 1,
			StationId:       "station-1",
			BattleLevel:     3,
			BattleStart:     now - 120,
			BattleEnd:       now + 7200,
			BattlePokemonId: null.IntFrom(374),
		},
	}) {
		t.Fatal("expected preloaded station battles to be cached")
	}

	battles := getKnownStationBattles("station-1", nil, now)
	if len(battles) != 2 {
		t.Fatalf("expected both persisted battles after preload, got %d", len(battles))
	}
	if battles[0].BreadBattleSeed != 1 || battles[1].BreadBattleSeed != 2 {
		t.Fatalf("unexpected preloaded battle ordering: %+v", battles)
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
		BattleLevel:     3,
		BattleStart:     now - 600,
		BattleEnd:       now + 7200,
		BattlePokemonId: null.IntFrom(374),
	}, now)
	upsertCachedStationBattle(StationBattleData{
		BreadBattleSeed: 2,
		StationId:       station.Id,
		BattleLevel:     1,
		BattleStart:     now + 600,
		BattleEnd:       now + 3600,
		BattlePokemonId: null.IntFrom(527),
	}, now)
	station.oldValues = StationOldValues{
		HasCanonicalBattle:  true,
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

func TestCreateStationWebhooksCountsZeroSeedCanonicalBattle(t *testing.T) {
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
		BreadBattleSeed: 0,
		StationId:       station.Id,
		BattleLevel:     1,
		BattleStart:     now - 600,
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
	if len(collector.maxBattleLevels) != 1 || collector.maxBattleLevels[0] != 1 {
		t.Fatalf("expected one max battle metric increment, got %v", collector.maxBattleLevels)
	}
}

func TestSyncStationBattlesFromProtoAllowsZeroSeed(t *testing.T) {
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

	previousUpsert := upsertStationBattleRecordFunc
	upsertStationBattleRecordFunc = func(context.Context, db.DbDetails, StationBattleData) error {
		return nil
	}
	defer func() {
		upsertStationBattleRecordFunc = previousUpsert
	}()

	syncStationBattlesFromProto(context.Background(), db.DbDetails{}, station, &pogo.BreadBattleDetailProto{
		BreadBattleSeed:     0,
		BattleWindowStartMs: (now - 60) * 1000,
		BattleWindowEndMs:   (now + 3600) * 1000,
		BattleLevel:         pogo.BreadBattleLevel_BREAD_BATTLE_LEVEL_2,
		BattlePokemon:       &pogo.PokemonProto{PokemonId: 133},
	})

	battles := getKnownStationBattles(station.Id, station, now)
	if len(battles) != 1 || battles[0].BreadBattleSeed != 0 {
		t.Fatalf("expected zero-seed battle to be cached, got %+v", battles)
	}
	if station.BattlePokemonId.ValueOrZero() != 133 {
		t.Fatalf("expected zero-seed battle projection, got %d", station.BattlePokemonId.ValueOrZero())
	}
}

func TestSyncStationBattlesFromProtoRestoresOldProjectionOnUpsertFailure(t *testing.T) {
	initStationBattleCache()
	now := time.Now().Unix()
	station := &Station{
		StationData: StationData{
			Id:                "station-1",
			IsBattleAvailable: true,
			BattleLevel:       null.IntFrom(1),
			BattleStart:       null.IntFrom(now - 120),
			BattleEnd:         null.IntFrom(now + 7200),
			BattlePokemonId:   null.IntFrom(527),
		},
	}
	station.snapshotOldValues()
	station.SetIsBattleAvailable(true)
	station.SetBattleLevel(null.IntFrom(2))
	station.SetBattleStart(null.IntFrom(now - 60))
	station.SetBattleEnd(null.IntFrom(now + 3600))
	station.SetBattlePokemonId(null.IntFrom(133))

	upsertCachedStationBattle(StationBattleData{
		BreadBattleSeed: 99,
		StationId:       station.Id,
		BattleLevel:     1,
		BattleStart:     now - 120,
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

	if station.BattlePokemonId.ValueOrZero() != 527 {
		t.Fatalf("expected old battle projection to be restored, got %d", station.BattlePokemonId.ValueOrZero())
	}
	if !station.skipWebhook {
		t.Fatal("expected webhook suppression after failed station battle write")
	}
}

func TestSyncStationBattlesFromProtoRetriesDeadlock(t *testing.T) {
	initStationBattleCache()
	now := time.Now().Unix()
	station := &Station{
		StationData: StationData{
			Id: "station-1",
		},
	}

	attempts := 0
	previousUpsert := upsertStationBattleRecordFunc
	upsertStationBattleRecordFunc = func(context.Context, db.DbDetails, StationBattleData) error {
		attempts++
		if attempts == 1 {
			return &mysql.MySQLError{Number: 1213, Message: "deadlock"}
		}
		return nil
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

	if attempts != 2 {
		t.Fatalf("expected one deadlock retry, got %d attempts", attempts)
	}
	if station.skipWebhook {
		t.Fatal("expected retry to succeed without suppressing webhook")
	}
	if station.BattlePokemonId.ValueOrZero() != 133 {
		t.Fatalf("expected battle projection after retry success, got %d", station.BattlePokemonId.ValueOrZero())
	}
}
