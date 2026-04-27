package decoder

import (
	"testing"
	"time"

	"github.com/guregu/null/v6"

	"golbat/geo"
	"golbat/pogo"
	"golbat/stats_collector"
	"golbat/webhooks"
)

type recordingWebhooksSender struct {
	messages []webhooks.WebhookType
	payloads []any
}

func (sender *recordingWebhooksSender) AddMessage(whType webhooks.WebhookType, payload any, _ []geo.AreaName) {
	sender.messages = append(sender.messages, whType)
	sender.payloads = append(sender.payloads, payload)
}

func testStationBattle(stationId string, seed int64, level int16, start, end int64, pokemon int64) StationBattleData {
	return StationBattleData{
		BreadBattleSeed: seed,
		StationId:       stationId,
		BattleLevel:     level,
		BattleStart:     start,
		BattleEnd:       end,
		BattlePokemonId: null.IntFrom(pokemon),
	}
}

func TestBuildDeleteObsoleteStationBattlesQuery(t *testing.T) {
	query, args := buildDeleteObsoleteStationBattlesQuery(
		[]string{"station-1", "station-2", "station-3"},
		[]StationBattleData{
			{StationId: "station-1", BreadBattleSeed: 1},
			{StationId: "station-1", BreadBattleSeed: 2},
			{StationId: "station-3", BreadBattleSeed: 3},
		},
	)

	expectedQuery := "DELETE FROM station_battle WHERE station_id IN (?,?,?) AND bread_battle_seed NOT IN (?,?,?)"
	if query != expectedQuery {
		t.Fatalf("unexpected delete query:\nexpected: %s\ngot:      %s", expectedQuery, query)
	}

	expectedArgs := []any{
		"station-1", "station-2", "station-3",
		int64(1), int64(2), int64(3),
	}
	if len(args) != len(expectedArgs) {
		t.Fatalf("expected %d args, got %d: %+v", len(expectedArgs), len(args), args)
	}
	for i := range expectedArgs {
		if args[i] != expectedArgs[i] {
			t.Fatalf("arg %d mismatch: expected %#v, got %#v", i, expectedArgs[i], args[i])
		}
	}
}

func TestBuildDeleteObsoleteStationBattlesQueryWithNoKeepRows(t *testing.T) {
	query, args := buildDeleteObsoleteStationBattlesQuery([]string{"station-1", "station-2"}, nil)

	expectedQuery := "DELETE FROM station_battle WHERE station_id IN (?,?)"
	if query != expectedQuery {
		t.Fatalf("unexpected delete query:\nexpected: %s\ngot:      %s", expectedQuery, query)
	}
	expectedArgs := []any{"station-1", "station-2"}
	if len(args) != len(expectedArgs) {
		t.Fatalf("expected %d args, got %d: %+v", len(expectedArgs), len(args), args)
	}
	for i := range expectedArgs {
		if args[i] != expectedArgs[i] {
			t.Fatalf("arg %d mismatch: expected %#v, got %#v", i, expectedArgs[i], args[i])
		}
	}
}

func TestUpsertCachedStationBattleOrdering(t *testing.T) {
	now := time.Now().Unix()
	cases := []struct {
		name     string
		inserted []StationBattleData
		expected []int64
	}{
		{
			name: "observed earlier-ending active battle keeps later-ending cached battle",
			inserted: []StationBattleData{
				testStationBattle("station-1", 2, 2, now-60, now+3600, 133),
				testStationBattle("station-1", 1, 1, now-60, now+1800, 527),
			},
			expected: []int64{1, 2},
		},
		{
			name: "observed later-ending future battle evicts earlier-ending active battle",
			inserted: []StationBattleData{
				testStationBattle("station-1", 1, 3, now-120, now+7200, 374),
				testStationBattle("station-1", 2, 1, now+600, now+9000, 527),
			},
			expected: []int64{2},
		},
		{
			name: "observed active battle keeps later-ending future cached battle",
			inserted: []StationBattleData{
				testStationBattle("station-1", 2, 2, now+600, now+7200, 133),
				testStationBattle("station-1", 1, 1, now-60, now+1800, 527),
			},
			expected: []int64{1, 2},
		},
		{
			name: "keeps future-only battles sorted by earliest end",
			inserted: []StationBattleData{
				testStationBattle("station-1", 2, 2, now+1800, now+7200, 527),
				testStationBattle("station-1", 1, 3, now+600, now+3600, 374),
			},
			expected: []int64{1, 2},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			initStationBattleCache()
			for _, battle := range tc.inserted {
				upsertCachedStationBattle(battle, now)
			}

			battles := getKnownStationBattles("station-1", now)
			if len(battles) != len(tc.expected) {
				t.Fatalf("expected %d battles, got %d (%+v)", len(tc.expected), len(battles), battles)
			}
			for i, expectedSeed := range tc.expected {
				if battles[i].BreadBattleSeed != expectedSeed {
					t.Fatalf("expected seed %d at index %d, got %+v", expectedSeed, i, battles)
				}
			}
			topBattle := topStationBattleFromSlice(battles)
			if topBattle == nil || topBattle.BreadBattleSeed != tc.expected[0] {
				t.Fatalf("expected top seed %d, got %+v", tc.expected[0], topBattle)
			}
		})
	}
}

func TestObservedStationBattleEvictsCachedBattlesEndingNoLater(t *testing.T) {
	now := time.Now().Unix()
	cases := []struct {
		name          string
		cachedStart   int64
		cachedEnd     int64
		observedStart int64
		observedEnd   int64
	}{
		{
			name:          "cached ends before observed",
			cachedStart:   now - 120,
			cachedEnd:     now + 1800,
			observedStart: now + 600,
			observedEnd:   now + 3600,
		},
		{
			name:          "cached has same end and observed starts before cached",
			cachedStart:   now - 60,
			cachedEnd:     now + 3600,
			observedStart: now - 120,
			observedEnd:   now + 3600,
		},
		{
			name:          "cached has same end and observed starts after cached",
			cachedStart:   now - 120,
			cachedEnd:     now + 3600,
			observedStart: now - 60,
			observedEnd:   now + 3600,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			initStationBattleCache()
			upsertCachedStationBattle(testStationBattle("station-1", 1, 1, tc.cachedStart, tc.cachedEnd, 527), now)
			upsertCachedStationBattle(testStationBattle("station-1", 2, 2, tc.observedStart, tc.observedEnd, 133), now)

			battles := getKnownStationBattles("station-1", now)
			if len(battles) != 1 {
				t.Fatalf("expected observed battle to evict cached battle ending no later, got %+v", battles)
			}
			if battles[0].BreadBattleSeed != 2 {
				t.Fatalf("expected observed seed 2, got %+v", battles)
			}
		})
	}
}

func TestBuildStationResultUsesTopBattleForFlatFields(t *testing.T) {
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
		t.Fatalf("expected top battle pokemon 527, got %d", result.BattlePokemonId.ValueOrZero())
	}
	if len(result.Battles) != 2 {
		t.Fatalf("expected shorter battle to keep prior longer battle later, got %d", len(result.Battles))
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

func TestUpdateStationLookupUsesTopBattleForFlatFields(t *testing.T) {
	initStationBattleCache()
	initFortRtree()
	now := time.Now().Unix()
	station := &Station{StationData: StationData{Id: "station-1", Lat: 1, Lon: 2}}

	storeStationBattles(station.Id, []StationBattleData{
		testStationBattle(station.Id, 1, 1, now-60, now+1800, 527),
		testStationBattle(station.Id, 2, 2, now-60, now+3600, 133),
	})
	updateStationLookupWithBattles(station, getKnownStationBattles(station.Id, now))

	lookup, ok := fortLookupCache.Load(station.Id)
	if !ok {
		t.Fatal("expected station lookup")
	}
	if lookup.BattlePokemonId != 527 || lookup.BattleLevel != 1 {
		t.Fatalf("expected fort lookup flat fields from top battle, got %+v", lookup)
	}
}

func TestCreateStationWebhooksEmitsFutureBattle(t *testing.T) {
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
		EndTime: station.EndTime,
	}

	createStationWebhooksWithBattles(station, getKnownStationBattles(station.Id, now), station.IsNewRecord())
	if len(sender.messages) != 1 || sender.messages[0] != webhooks.MaxBattle {
		t.Fatalf("expected one max_battle webhook, got %v", sender.messages)
	}
}

func TestCreateStationWebhooksUsesTopBattleForFlatFields(t *testing.T) {
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
			Id:              "station-1",
			Name:            "Station",
			Lat:             1,
			Lon:             2,
			CellId:          123,
			EndTime:         now + 7200,
			Updated:         now,
			BattlePokemonId: null.IntFrom(527),
		},
	}
	upsertCachedStationBattle(testStationBattle(station.Id, 2, 2, now-60, now+3600, 133), now)
	upsertCachedStationBattle(testStationBattle(station.Id, 1, 1, now-60, now+1800, 527), now)
	station.oldValues = StationOldValues{
		EndTime: station.EndTime,
	}

	createStationWebhooksWithBattles(station, getKnownStationBattles(station.Id, now), station.IsNewRecord())
	if len(sender.payloads) != 1 {
		t.Fatalf("expected one max_battle payload, got %d", len(sender.payloads))
	}
	payload, ok := sender.payloads[0].(StationWebhook)
	if !ok {
		t.Fatalf("expected StationWebhook payload, got %T", sender.payloads[0])
	}
	if payload.BattlePokemonId.ValueOrZero() != 527 || payload.BattleLevel.ValueOrZero() != 1 {
		t.Fatalf("expected webhook flat fields from top battle, got %+v", payload)
	}
}

func TestSyncStationBattlesFromProtoClearsCachedBattlesWhenDetailsMissing(t *testing.T) {
	initStationBattleCache()
	now := time.Now().Unix()
	station := &Station{
		StationData: StationData{
			Id:        "station-1",
			Name:      "Station",
			Lat:       1,
			Lon:       2,
			StartTime: now - 3600,
			EndTime:   now + 3600,
			Updated:   now,
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

	state, ok := stationBattleCache.Load(station.Id)
	if !ok || !state.Loaded || len(state.Battles) != 0 {
		t.Fatalf("expected missing battle details to leave an empty loaded state, got %+v ok=%t", state, ok)
	}
	if !hasLoadedStationBattles(station.Id) {
		t.Fatal("expected missing battle details to leave station loaded")
	}
	result := BuildStationResult(station)
	if result.BattleEnd.Valid || result.BattlePokemonId.Valid || len(result.Battles) != 0 {
		t.Fatalf("expected API result without stale battles, got %+v", result)
	}
}

func TestBuildStationResultSuppressesStaleBattleAfterExpiredHydratedCache(t *testing.T) {
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
	storeStationBattles("station-1", []StationBattleData{{
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
	state, ok := stationBattleCache.Load(station.Id)
	if !ok || !state.Loaded || len(state.Battles) != 0 {
		t.Fatalf("expected expired loaded state to be collapsed to empty, got %+v ok=%t", state, ok)
	}
}
