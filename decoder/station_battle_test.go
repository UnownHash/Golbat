package decoder

import (
	"context"
	"database/sql/driver"
	"fmt"
	"testing"
	"time"

	"github.com/guregu/null/v6"

	"golbat/db"
	"golbat/decoder/writebehind"
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

// testStationId returns a deterministic, valid FortId derived from n, so
// tests that need distinct station identities (or the same identity reused
// across several battle rows) can name them cheaply and unambiguously.
func testStationId(t *testing.T, n int) FortId {
	t.Helper()
	return mustFortId(t, fmt.Sprintf("%032x", n))
}

func testStationBattle(stationId FortId, seed int64, level int16, start, end int64, pokemon int64) StationBattleData {
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
	id1 := testStationId(t, 1)
	id2 := testStationId(t, 2)
	id3 := testStationId(t, 3)
	query, args, err := buildDeleteObsoleteStationBattlesQuery(
		[]FortId{id1, id2, id3},
		[]StationBattleData{
			{StationId: id1, BreadBattleSeed: 1},
			{StationId: id1, BreadBattleSeed: 2},
			{StationId: id3, BreadBattleSeed: 3},
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedQuery := "DELETE FROM station_battle WHERE station_id IN (?, ?, ?) AND bread_battle_seed NOT IN (?, ?, ?)"
	if query != expectedQuery {
		t.Fatalf("unexpected delete query:\nexpected: %s\ngot:      %s", expectedQuery, query)
	}

	// sqlx.In does not itself call driver.Valuer on slice elements (only the
	// database/sql machinery does, at Exec time) — see
	// TestDeleteObsoleteStationBattlesBindsVarcharIds for the assertion that
	// covers that later conversion. Here the bind args are still raw FortId
	// values.
	expectedArgs := []any{
		id1, id2, id3,
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
	id1 := testStationId(t, 1)
	id2 := testStationId(t, 2)
	query, args, err := buildDeleteObsoleteStationBattlesQuery([]FortId{id1, id2}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedQuery := "DELETE FROM station_battle WHERE station_id IN (?, ?)"
	if query != expectedQuery {
		t.Fatalf("unexpected delete query:\nexpected: %s\ngot:      %s", expectedQuery, query)
	}
	expectedArgs := []any{id1, id2}
	if len(args) != len(expectedArgs) {
		t.Fatalf("expected %d args, got %d: %+v", len(expectedArgs), len(args), args)
	}
	for i := range expectedArgs {
		if args[i] != expectedArgs[i] {
			t.Fatalf("arg %d mismatch: expected %#v, got %#v", i, expectedArgs[i], args[i])
		}
	}
}

// sqlx.In expands []FortId through driver.Valuer. If that ever regressed,
// this DELETE would bind the wrong values and quietly remove the wrong rows.
func TestDeleteObsoleteStationBattlesBindsVarcharIds(t *testing.T) {
	ids := []FortId{
		mustFortId(t, "a1b2c3d4e5f60718293a4b5c6d7e8f90.23"),
		mustFortId(t, "00000000000000000000000000000001.16"),
	}
	query, args, err := buildDeleteObsoleteStationBattlesQuery(ids, nil)
	if err != nil {
		t.Fatalf("buildDeleteObsoleteStationBattlesQuery error: %v", err)
	}
	if len(args) != len(ids) {
		t.Fatalf("got %d bind args, want %d (query: %s)", len(args), len(ids), query)
	}
	for i, arg := range args {
		v, err := driver.DefaultParameterConverter.ConvertValue(arg)
		if err != nil {
			t.Fatalf("arg %d is not a valid driver value: %v", i, err)
		}
		s, ok := v.(string)
		if !ok {
			t.Fatalf("arg %d converted to %T (%v), want the varchar string", i, v, v)
		}
		if s != ids[i].String() {
			t.Fatalf("arg %d = %q, want %q", i, s, ids[i].String())
		}
	}
}

func TestUpsertCachedStationBattleOrdering(t *testing.T) {
	now := time.Now().Unix()
	stationId := testStationId(t, 1)
	cases := []struct {
		name     string
		inserted []StationBattleData
		expected []int64
	}{
		{
			name: "observed earlier-ending active battle keeps later-ending cached battle",
			inserted: []StationBattleData{
				testStationBattle(stationId, 2, 2, now-60, now+3600, 133),
				testStationBattle(stationId, 1, 1, now-60, now+1800, 527),
			},
			expected: []int64{1, 2},
		},
		{
			name: "observed later-ending future battle evicts earlier-ending active battle",
			inserted: []StationBattleData{
				testStationBattle(stationId, 1, 3, now-120, now+7200, 374),
				testStationBattle(stationId, 2, 1, now+600, now+9000, 527),
			},
			expected: []int64{2},
		},
		{
			name: "observed active battle keeps later-ending future cached battle",
			inserted: []StationBattleData{
				testStationBattle(stationId, 2, 2, now+600, now+7200, 133),
				testStationBattle(stationId, 1, 1, now-60, now+1800, 527),
			},
			expected: []int64{1, 2},
		},
		{
			name: "keeps future-only battles sorted by earliest end",
			inserted: []StationBattleData{
				testStationBattle(stationId, 2, 2, now+1800, now+7200, 527),
				testStationBattle(stationId, 1, 3, now+600, now+3600, 374),
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

			battles := getKnownStationBattles(stationId, now)
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
	stationId := testStationId(t, 1)
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
			name:          "cached has same end",
			cachedStart:   now - 60,
			cachedEnd:     now + 3600,
			observedStart: now - 120,
			observedEnd:   now + 3600,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			initStationBattleCache()
			upsertCachedStationBattle(testStationBattle(stationId, 1, 1, tc.cachedStart, tc.cachedEnd, 527), now)
			upsertCachedStationBattle(testStationBattle(stationId, 2, 2, tc.observedStart, tc.observedEnd, 133), now)

			battles := getKnownStationBattles(stationId, now)
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
			Id:              testStationId(t, 1),
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
	if derefInt64OrNeg(result.BattlePokemonId) != 527 {
		t.Fatalf("expected top battle pokemon 527, got %d", derefInt64OrNeg(result.BattlePokemonId))
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
			Id:                testStationId(t, 1),
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
	if derefInt64OrNeg(result.BattlePokemonId) != 527 {
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
	now := time.Now().Unix()
	stationId := testStationId(t, 1)
	station := &Station{StationData: StationData{Id: stationId, Lat: 1, Lon: 2}}

	storeStationBattles(station.Id, []StationBattleData{
		testStationBattle(station.Id, 1, 1, now-60, now+1800, 527),
		testStationBattle(station.Id, 2, 2, now-60, now+3600, 133),
	})
	updateStationLookupWithBattles(stationId, station, getKnownStationBattles(station.Id, now))

	lookup, ok := fortLookupCache.Load(stationId)
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
	sender := &recordingWebhooksSender{}
	webhooksSender = sender
	setStatsCollectorForTest(t, stats_collector.NewNoopStatsCollector())
	defer func() {
		webhooksSender = previousSender
	}()

	now := time.Now().Unix()
	station := &Station{
		StationData: StationData{
			Id:                testStationId(t, 1),
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

	battles := getKnownStationBattles(station.Id, now)
	createStationWebhooksWithBattles(station, battles, snapshotStationBattles(battles), station.IsNewRecord(), now)
	if len(sender.messages) != 1 || sender.messages[0] != webhooks.MaxBattle {
		t.Fatalf("expected one max_battle webhook, got %v", sender.messages)
	}
}

func TestCreateStationWebhooksUsesTopBattleForFlatFields(t *testing.T) {
	initStationBattleCache()
	previousSender := webhooksSender
	sender := &recordingWebhooksSender{}
	webhooksSender = sender
	setStatsCollectorForTest(t, stats_collector.NewNoopStatsCollector())
	defer func() {
		webhooksSender = previousSender
	}()

	now := time.Now().Unix()
	station := &Station{
		StationData: StationData{
			Id:              testStationId(t, 1),
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

	battles := getKnownStationBattles(station.Id, now)
	createStationWebhooksWithBattles(station, battles, snapshotStationBattles(battles), station.IsNewRecord(), now)
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
			Id:        testStationId(t, 1),
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
	if result.BattleEnd != nil || result.BattlePokemonId != nil || len(result.Battles) != 0 {
		t.Fatalf("expected API result without stale battles, got %+v", result)
	}
}

func TestBuildStationResultSuppressesStaleBattleAfterExpiredHydratedCache(t *testing.T) {
	initStationBattleCache()
	now := time.Now().Unix()
	station := &Station{
		StationData: StationData{
			Id:              testStationId(t, 1),
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
	storeStationBattles(station.Id, []StationBattleData{{
		BreadBattleSeed: 1,
		StationId:       station.Id,
		BattleLevel:     1,
		BattleStart:     now - 7200,
		BattleEnd:       now - 60,
		BattlePokemonId: null.IntFrom(527),
	}})

	result := BuildStationResult(station)
	if result.BattleEnd != nil || result.BattlePokemonId != nil {
		t.Fatalf("expected expired hydrated cache to suppress stale projection, got %+v", result)
	}
	state, ok := stationBattleCache.Load(station.Id)
	if !ok || !state.Loaded || len(state.Battles) != 0 {
		t.Fatalf("expected expired loaded state to be collapsed to empty, got %+v ok=%t", state, ok)
	}
}

func TestSaveStationRecordRefreshesStationWhenOnlyBattleListChanges(t *testing.T) {
	initStationBattleCache()
	previousStationQueue := stationQueue
	previousStationBattleQueue := stationBattleQueue
	previousSender := webhooksSender
	stats := stats_collector.NewNoopStatsCollector()
	stationQueue = writebehind.NewTypedQueue(writebehind.TypedQueueConfig[FortId, StationData]{
		Name:       "station",
		Stats:      stats,
		FlushFunc:  func(context.Context, db.DbDetails, []StationData) error { return nil },
		KeyFunc:    func(d StationData) FortId { return d.Id },
		KeyCompare: FortId.Compare,
	})
	stationBattleQueue = writebehind.NewTypedQueue(writebehind.TypedQueueConfig[FortId, stationBattleWrite]{
		Name:       "station_battle",
		Stats:      stats,
		FlushFunc:  func(context.Context, db.DbDetails, []stationBattleWrite) error { return nil },
		KeyFunc:    func(d stationBattleWrite) FortId { return d.StationId },
		KeyCompare: FortId.Compare,
	})
	webhooksSender = &recordingWebhooksSender{}
	setStatsCollectorForTest(t, stats)
	defer func() {
		stationQueue = previousStationQueue
		stationBattleQueue = previousStationBattleQueue
		webhooksSender = previousSender
	}()

	now := time.Now().Unix()
	stationId := testStationId(t, 1)
	oldBattles := []StationBattleData{
		testStationBattle(stationId, 1, 1, now-60, now+1800, 527),
		testStationBattle(stationId, 2, 2, now-60, now+3600, 133),
	}
	newBattles := []StationBattleData{
		testStationBattle(stationId, 1, 1, now-60, now+1800, 527),
		testStationBattle(stationId, 3, 2, now-60, now+3600, 133),
	}
	storeStationBattles(stationId, newBattles)
	station := &Station{
		StationData: StationData{
			Id:              stationId,
			Name:            "Station",
			Lat:             1,
			Lon:             2,
			EndTime:         now + 7200,
			Updated:         now - 3600,
			BattleLevel:     null.IntFrom(1),
			BattleStart:     null.IntFrom(now - 60),
			BattleEnd:       null.IntFrom(now + 1800),
			BattlePokemonId: null.IntFrom(527),
		},
		oldValues: StationOldValues{
			EndTime:        now + 7200,
			BattleSnapshot: snapshotStationBattles(oldBattles),
		},
	}

	saveStationRecord(context.Background(), db.DbDetails{}, station)

	if stationQueue.Size() != 1 {
		t.Fatalf("expected battle-list change to refresh station row, station queue size=%d", stationQueue.Size())
	}
	if stationBattleQueue.Size() != 1 {
		t.Fatalf("expected battle-list change to write station battles, station battle queue size=%d", stationBattleQueue.Size())
	}
	if station.Updated <= now-3600 {
		t.Fatalf("expected station updated to advance, got %d", station.Updated)
	}
}

func TestApplyTopStationBattleToStationUsesCpMultiplierTolerance(t *testing.T) {
	now := time.Now().Unix()
	station := &Station{
		StationData: StationData{
			Id:                        testStationId(t, 1),
			BattleLevel:               null.IntFrom(1),
			BattleStart:               null.IntFrom(now - 60),
			BattleEnd:                 null.IntFrom(now + 1800),
			BattlePokemonId:           null.IntFrom(527),
			BattlePokemonCpMultiplier: null.FloatFrom(0.5),
		},
	}
	station.ClearDirty()
	applyTopStationBattleToStation(station, []StationBattleData{{
		BreadBattleSeed:           1,
		StationId:                 station.Id,
		BattleLevel:               1,
		BattleStart:               now - 60,
		BattleEnd:                 now + 1800,
		BattlePokemonId:           null.IntFrom(527),
		BattlePokemonCpMultiplier: null.FloatFrom(0.5 + floatTolerance/2),
	}})

	if station.IsDirty() {
		t.Fatal("expected tolerated cp multiplier difference not to dirty station")
	}
	if station.BattlePokemonCpMultiplier.Float64 != 0.5 {
		t.Fatalf("expected tolerated cp multiplier difference to leave value unchanged, got %f", station.BattlePokemonCpMultiplier.Float64)
	}
}

// derefInt64OrNeg returns *p, or -1 when p is nil (test helper for the
// pointer-based ApiStationResult battle fields).
func derefInt64OrNeg(p *int64) int64 {
	if p == nil {
		return -1
	}
	return *p
}
