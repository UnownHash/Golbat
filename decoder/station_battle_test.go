package decoder

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/guregu/null/v6"
	"github.com/jellydator/ttlcache/v3"

	"golbat/config"
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

	canonical := canonicalStationBattleFromSlice(nil, battles, now)
	if canonical == nil || canonical.BreadBattleSeed != 2 {
		t.Fatalf("expected canonical seed 2, got %+v", canonical)
	}
}

func TestBuildStationResultPrefersCurrentActiveProjection(t *testing.T) {
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
		BreadBattleSeed: 2,
		StationId:       station.Id,
		BattleLevel:     2,
		BattleStart:     now - 120,
		BattleEnd:       now + 7200,
		BattlePokemonId: null.IntFrom(133),
	}, now)
	upsertCachedStationBattle(StationBattleData{
		BreadBattleSeed: 1,
		StationId:       station.Id,
		BattleLevel:     1,
		BattleStart:     now - 60,
		BattleEnd:       now + 1800,
		BattlePokemonId: null.IntFrom(527),
	}, now)

	result := BuildStationResult(station)
	if result.BattlePokemonId.ValueOrZero() != 527 {
		t.Fatalf("expected current active pokemon 527, got %d", result.BattlePokemonId.ValueOrZero())
	}
	if len(result.Battles) != 2 {
		t.Fatalf("expected both battles to remain known, got %d", len(result.Battles))
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
	canonical := canonicalStationBattleFromSlice(nil, battles, now)
	if canonical == nil || canonical.BreadBattleSeed != 1 {
		t.Fatalf("expected longer existing battle seed 1 to remain canonical, got %+v", canonical)
	}
}

func TestCanonicalStationBattlePrefersCurrentActiveProjection(t *testing.T) {
	initStationBattleCache()
	now := time.Now().Unix()
	station := &Station{
		StationData: StationData{
			Id:              "station-1",
			BattleLevel:     null.IntFrom(1),
			BattleStart:     null.IntFrom(now - 60),
			BattleEnd:       null.IntFrom(now + 1800),
			BattlePokemonId: null.IntFrom(527),
		},
	}

	upsertCachedStationBattle(StationBattleData{
		BreadBattleSeed: 2,
		StationId:       station.Id,
		BattleLevel:     2,
		BattleStart:     now - 120,
		BattleEnd:       now + 7200,
		BattlePokemonId: null.IntFrom(133),
	}, now)
	upsertCachedStationBattle(StationBattleData{
		BreadBattleSeed: 1,
		StationId:       station.Id,
		BattleLevel:     1,
		BattleStart:     now - 60,
		BattleEnd:       now + 1800,
		BattlePokemonId: null.IntFrom(527),
	}, now)

	battles := getKnownStationBattles(station.Id, station, now)
	if len(battles) != 2 {
		t.Fatalf("expected both active battles to remain known, got %d", len(battles))
	}

	canonical := canonicalStationBattleFromSlice(station, battles, now)
	if canonical == nil || canonical.BreadBattleSeed != 1 {
		t.Fatalf("expected current station projection seed 1 to be canonical, got %+v", canonical)
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

	syncStationBattlesFromProto(station, &pogo.BreadBattleDetailProto{
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

func TestSyncStationBattlesFromProtoClearsCachedBattlesWhenDetailsMissing(t *testing.T) {
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
			BattleLevel:     null.IntFrom(2),
			BattleStart:     null.IntFrom(now - 60),
			BattleEnd:       null.IntFrom(now + 3600),
			BattlePokemonId: null.IntFrom(133),
		},
	}

	syncStationBattlesFromProto(station, &pogo.BreadBattleDetailProto{
		BreadBattleSeed:     7,
		BattleWindowStartMs: (now - 60) * 1000,
		BattleWindowEndMs:   (now + 3600) * 1000,
		BattleLevel:         pogo.BreadBattleLevel_BREAD_BATTLE_LEVEL_2,
		BattlePokemon:       &pogo.PokemonProto{PokemonId: 133},
	})

	syncStationBattlesFromProto(station, nil)

	if _, ok := stationBattleCache.Load(station.Id); ok {
		t.Fatal("expected cached battle state to be cleared")
	}
	if !hasHydratedStationBattles(station.Id) {
		t.Fatal("expected missing battle details to leave station hydrated")
	}
	if station.BattleEnd.Valid || station.BattlePokemonId.Valid {
		t.Fatalf("expected station projection cleared, got %+v", station)
	}

	result := BuildStationResult(station)
	if result.BattleEnd.Valid || result.BattlePokemonId.Valid || len(result.Battles) != 0 {
		t.Fatalf("expected API result without stale battles, got %+v", result)
	}
}

func TestGetKnownStationBattlesDoesNotMutateCacheOnRead(t *testing.T) {
	initStationBattleCache()
	now := time.Now().Unix()
	expired := StationBattleData{
		BreadBattleSeed: 1,
		StationId:       "station-1",
		BattleLevel:     1,
		BattleStart:     now - 7200,
		BattleEnd:       now - 60,
		BattlePokemonId: null.IntFrom(527),
	}
	current := StationBattleData{
		BreadBattleSeed: 2,
		StationId:       "station-1",
		BattleLevel:     2,
		BattleStart:     now - 60,
		BattleEnd:       now + 3600,
		BattlePokemonId: null.IntFrom(133),
	}
	stationBattleCache.Store("station-1", []StationBattleData{current, expired})

	battles := getKnownStationBattles("station-1", nil, now)
	if len(battles) != 1 || battles[0].BreadBattleSeed != 2 {
		t.Fatalf("expected only current battle from read, got %+v", battles)
	}

	cached, ok := stationBattleCache.Load("station-1")
	if !ok || len(cached) != 2 {
		t.Fatalf("expected cached slice to remain unchanged, got %+v", cached)
	}
}

func TestBuildStationResultSuppressesStaleProjectionAfterExpiredHydratedCache(t *testing.T) {
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
			BattleStart:     null.IntFrom(now - 600),
			BattleEnd:       null.IntFrom(now + 600),
			BattlePokemonId: null.IntFrom(527),
		},
	}
	markStationBattlesHydrated(station.Id)

	stationBattleCache.Store("station-1", []StationBattleData{{
		BreadBattleSeed: 1,
		StationId:       station.Id,
		BattleLevel:     1,
		BattleStart:     now - 7200,
		BattleEnd:       now - 60,
		BattlePokemonId: null.IntFrom(527),
	}})

	result := BuildStationResult(station)
	if result.BattleEnd.Valid || result.BattlePokemonId.Valid {
		t.Fatalf("expected expired hydrated cache to suppress stale projection, got %+v", result)
	}
	if _, ok := stationBattleCache.Load(station.Id); ok {
		t.Fatal("expected expired hydrated cache entry to be cleaned up")
	}
}

func TestGetStationRecordReadOnlyRetriesHydrationOnCachedStation(t *testing.T) {
	initStationBattleCache()
	stationId := "station-hydration-retry"
	station := &Station{StationData: StationData{Id: stationId}}
	stationCache.Set(stationId, station, ttlcache.DefaultTTL)
	defer stationCache.Delete(stationId)
	defer clearStationBattleCaches(stationId)

	attempts := 0
	previousHydrate := hydrateStationBattlesForStationFunc
	hydrateStationBattlesForStationFunc = func(_ context.Context, _ db.DbDetails, station *Station, _ int64) error {
		attempts++
		if attempts == 1 {
			return errors.New("boom")
		}
		markStationBattlesHydrated(station.Id)
		return nil
	}
	defer func() {
		hydrateStationBattlesForStationFunc = previousHydrate
	}()

	record, unlock, err := GetStationRecordReadOnly(context.Background(), db.DbDetails{}, stationId, "test")
	if err != nil {
		t.Fatalf("expected cached station to be served even when hydration fails, got %v", err)
	}
	if record == nil || unlock == nil {
		t.Fatal("expected cached station record on hydration failure")
	}
	unlock()

	record, unlock, err = GetStationRecordReadOnly(context.Background(), db.DbDetails{}, stationId, "test")
	if err != nil {
		t.Fatalf("expected second hydration attempt to succeed, got %v", err)
	}
	if record == nil || unlock == nil {
		t.Fatal("expected cached station record after retry")
	}
	unlock()
	if attempts != 2 {
		t.Fatalf("expected hydration retry on cached station, got %d attempts", attempts)
	}
}

func TestGetStationRecordReadOnlyKeepsSingletonAfterHydrationFailureOnCacheMiss(t *testing.T) {
	initStationBattleCache()
	stationId := "station-hydration-miss-retry"
	defer stationCache.Delete(stationId)
	defer clearStationBattleCaches(stationId)

	loadCalls := 0
	previousLoad := loadStationFromDatabaseFunc
	loadStationFromDatabaseFunc = func(_ context.Context, _ db.DbDetails, id string, station *Station) error {
		loadCalls++
		station.Id = id
		station.Name = "Station"
		return nil
	}
	defer func() {
		loadStationFromDatabaseFunc = previousLoad
	}()

	hydrateCalls := 0
	previousHydrate := hydrateStationBattlesForStationFunc
	hydrateStationBattlesForStationFunc = func(_ context.Context, _ db.DbDetails, station *Station, _ int64) error {
		hydrateCalls++
		if hydrateCalls == 1 {
			return errors.New("boom")
		}
		markStationBattlesHydrated(station.Id)
		return nil
	}
	defer func() {
		hydrateStationBattlesForStationFunc = previousHydrate
	}()

	record, unlock, err := GetStationRecordReadOnly(context.Background(), db.DbDetails{}, stationId, "test")
	if err == nil {
		if unlock != nil {
			unlock()
		}
		t.Fatal("expected first cache-miss hydration to fail")
	}
	if record != nil || unlock != nil {
		t.Fatal("expected no station return on failed cache-miss hydration")
	}

	cachedItem := stationCache.Get(stationId)
	if cachedItem == nil {
		t.Fatal("expected failed hydration to keep cached station instance")
	}
	cachedStation := cachedItem.Value()

	record, unlock, err = GetStationRecordReadOnly(context.Background(), db.DbDetails{}, stationId, "test")
	if err != nil {
		t.Fatalf("expected retry on cached singleton to succeed, got %v", err)
	}
	if record == nil || unlock == nil {
		t.Fatal("expected cached station record after retry")
	}
	if record != cachedStation {
		unlock()
		t.Fatal("expected retry to reuse cached station singleton")
	}
	unlock()

	if loadCalls != 1 {
		t.Fatalf("expected one DB load across retry, got %d", loadCalls)
	}
	if hydrateCalls != 2 {
		t.Fatalf("expected two hydration attempts across retry, got %d", hydrateCalls)
	}
}

func TestGetStationRecordReadOnlySkipsHydrationAfterProtoSync(t *testing.T) {
	initStationBattleCache()
	now := time.Now().Unix()
	stationId := "station-hydration-skip"
	station := &Station{StationData: StationData{Id: stationId}}
	stationCache.Set(stationId, station, ttlcache.DefaultTTL)
	defer stationCache.Delete(stationId)
	defer clearStationBattleCaches(stationId)

	syncStationBattlesFromProto(station, &pogo.BreadBattleDetailProto{
		BreadBattleSeed:     7,
		BattleWindowStartMs: (now - 60) * 1000,
		BattleWindowEndMs:   (now + 3600) * 1000,
		BattleLevel:         pogo.BreadBattleLevel_BREAD_BATTLE_LEVEL_2,
		BattlePokemon:       &pogo.PokemonProto{PokemonId: 133},
	})

	attempts := 0
	previousHydrate := hydrateStationBattlesForStationFunc
	hydrateStationBattlesForStationFunc = func(_ context.Context, _ db.DbDetails, _ *Station, _ int64) error {
		attempts++
		return nil
	}
	defer func() {
		hydrateStationBattlesForStationFunc = previousHydrate
	}()

	record, unlock, err := GetStationRecordReadOnly(context.Background(), db.DbDetails{}, stationId, "test")
	if err != nil {
		t.Fatalf("expected cached station read to succeed, got %v", err)
	}
	if record == nil || unlock == nil {
		t.Fatal("expected cached station record")
	}
	unlock()
	if attempts != 0 {
		t.Fatalf("expected no DB hydration after proto sync, got %d attempts", attempts)
	}
}

func TestMarkPreloadedStationsHydratedMarksEmptyStations(t *testing.T) {
	initStationBattleCache()
	stationId := "station-preload-empty"
	station := &Station{StationData: StationData{Id: stationId}}
	stationCache.Set(stationId, station, ttlcache.DefaultTTL)
	defer stationCache.Delete(stationId)
	defer clearStationBattleCaches(stationId)

	if hasHydratedStationBattles(stationId) {
		t.Fatal("expected station to start unhydrated")
	}

	markPreloadedStationsHydrated(false)

	if !hasHydratedStationBattles(stationId) {
		t.Fatal("expected empty preloaded station to be marked hydrated")
	}
}

func TestGetStationRecordReadOnlyHydrationRefreshesFortLookup(t *testing.T) {
	initStationBattleCache()
	previousFortInMemory := config.Config.FortInMemory
	config.Config.FortInMemory = true
	defer func() {
		config.Config.FortInMemory = previousFortInMemory
	}()

	now := time.Now().Unix()
	stationId := "station-hydration-lookup"
	station := &Station{
		StationData: StationData{
			Id:              stationId,
			Lat:             1,
			Lon:             2,
			BattleLevel:     null.IntFrom(1),
			BattleStart:     null.IntFrom(now - 600),
			BattleEnd:       null.IntFrom(now + 600),
			BattlePokemonId: null.IntFrom(527),
		},
	}
	stationCache.Set(stationId, station, ttlcache.DefaultTTL)
	defer stationCache.Delete(stationId)
	defer clearStationBattleCaches(stationId)
	fortLookupCache.Store(stationId, FortLookup{
		FortType:           STATION,
		Lat:                station.Lat,
		Lon:                station.Lon,
		BattleEndTimestamp: station.BattleEnd.ValueOrZero(),
		BattleLevel:        int8(station.BattleLevel.ValueOrZero()),
		BattlePokemonId:    int16(station.BattlePokemonId.ValueOrZero()),
	})

	previousHydrate := hydrateStationBattlesForStationFunc
	hydrateStationBattlesForStationFunc = func(_ context.Context, _ db.DbDetails, station *Station, _ int64) error {
		markStationBattlesHydrated(station.Id)
		stationBattleCache.Delete(station.Id)
		return nil
	}
	defer func() {
		hydrateStationBattlesForStationFunc = previousHydrate
	}()

	record, unlock, err := GetStationRecordReadOnly(context.Background(), db.DbDetails{}, stationId, "test")
	if err != nil {
		t.Fatalf("expected hydration to succeed, got %v", err)
	}
	if record == nil || unlock == nil {
		t.Fatal("expected cached station")
	}
	unlock()

	lookup, ok := fortLookupCache.Load(stationId)
	if !ok {
		t.Fatal("expected fort lookup entry")
	}
	if lookup.BattleEndTimestamp != 0 || lookup.BattleLevel != 0 || lookup.BattlePokemonId != 0 {
		t.Fatalf("expected fort lookup to be cleared after hydration, got %+v", lookup)
	}
}
