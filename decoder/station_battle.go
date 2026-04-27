package decoder

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/guregu/null/v6"
	"github.com/jellydator/ttlcache/v3"
	"github.com/puzpuzpuz/xsync/v3"
	log "github.com/sirupsen/logrus"

	"golbat/db"
	"golbat/pogo"
)

type StationBattleData struct {
	BreadBattleSeed           int64      `db:"bread_battle_seed" json:"bread_battle_seed,omitempty"`
	StationId                 string     `db:"station_id" json:"-"`
	BattleLevel               int16      `db:"battle_level" json:"battle_level"`
	BattleStart               int64      `db:"battle_start" json:"battle_start"`
	BattleEnd                 int64      `db:"battle_end" json:"battle_end"`
	BattlePokemonId           null.Int   `db:"battle_pokemon_id" json:"battle_pokemon_id"`
	BattlePokemonForm         null.Int   `db:"battle_pokemon_form" json:"battle_pokemon_form"`
	BattlePokemonCostume      null.Int   `db:"battle_pokemon_costume" json:"battle_pokemon_costume"`
	BattlePokemonGender       null.Int   `db:"battle_pokemon_gender" json:"battle_pokemon_gender"`
	BattlePokemonAlignment    null.Int   `db:"battle_pokemon_alignment" json:"battle_pokemon_alignment"`
	BattlePokemonBreadMode    null.Int   `db:"battle_pokemon_bread_mode" json:"battle_pokemon_bread_mode"`
	BattlePokemonMove1        null.Int   `db:"battle_pokemon_move_1" json:"battle_pokemon_move_1"`
	BattlePokemonMove2        null.Int   `db:"battle_pokemon_move_2" json:"battle_pokemon_move_2"`
	BattlePokemonStamina      null.Int   `db:"battle_pokemon_stamina" json:"battle_pokemon_stamina"`
	BattlePokemonCpMultiplier null.Float `db:"battle_pokemon_cp_multiplier" json:"battle_pokemon_cp_multiplier"`
	Updated                   int64      `db:"updated" json:"-"`
}

type FortLookupStationBattle struct {
	BattleEndTimestamp int64
	BattleLevel        int8
	BattlePokemonId    int16
	BattlePokemonForm  int16
}

type stationBattleWrite struct {
	StationId string
	Battles   []StationBattleData
}

type stationBattleState struct {
	Battles []StationBattleData
	Loaded  bool
}

const stationBattleSelectColumns = `bread_battle_seed, station_id, battle_level, battle_start, battle_end,
	battle_pokemon_id, battle_pokemon_form, battle_pokemon_costume, battle_pokemon_gender,
	battle_pokemon_alignment, battle_pokemon_bread_mode, battle_pokemon_move_1, battle_pokemon_move_2,
	battle_pokemon_stamina, battle_pokemon_cp_multiplier, updated`

const stationBattleBatchUpsertQuery = `
INSERT INTO station_battle (
	bread_battle_seed, station_id, battle_level, battle_start, battle_end,
	battle_pokemon_id, battle_pokemon_form, battle_pokemon_costume, battle_pokemon_gender,
	battle_pokemon_alignment, battle_pokemon_bread_mode, battle_pokemon_move_1, battle_pokemon_move_2,
	battle_pokemon_stamina, battle_pokemon_cp_multiplier, updated
) VALUES (
	:bread_battle_seed, :station_id, :battle_level, :battle_start, :battle_end,
	:battle_pokemon_id, :battle_pokemon_form, :battle_pokemon_costume, :battle_pokemon_gender,
	:battle_pokemon_alignment, :battle_pokemon_bread_mode, :battle_pokemon_move_1, :battle_pokemon_move_2,
	:battle_pokemon_stamina, :battle_pokemon_cp_multiplier, :updated
)
ON DUPLICATE KEY UPDATE
	station_id = VALUES(station_id),
	battle_level = VALUES(battle_level),
	battle_start = VALUES(battle_start),
	battle_end = VALUES(battle_end),
	battle_pokemon_id = VALUES(battle_pokemon_id),
	battle_pokemon_form = VALUES(battle_pokemon_form),
	battle_pokemon_costume = VALUES(battle_pokemon_costume),
	battle_pokemon_gender = VALUES(battle_pokemon_gender),
	battle_pokemon_alignment = VALUES(battle_pokemon_alignment),
	battle_pokemon_bread_mode = VALUES(battle_pokemon_bread_mode),
	battle_pokemon_move_1 = VALUES(battle_pokemon_move_1),
	battle_pokemon_move_2 = VALUES(battle_pokemon_move_2),
	battle_pokemon_stamina = VALUES(battle_pokemon_stamina),
	battle_pokemon_cp_multiplier = VALUES(battle_pokemon_cp_multiplier),
	updated = VALUES(updated)
`

var stationBattleCache *xsync.MapOf[string, stationBattleState]

func initStationBattleCache() {
	stationBattleCache = xsync.NewMapOf[string, stationBattleState]()
}

func storeStationBattles(stationId string, battles []StationBattleData) {
	if stationId == "" {
		return
	}
	if len(battles) == 0 {
		stationBattleCache.Store(stationId, stationBattleState{Loaded: true})
		return
	}
	stateBattles := slices.Clone(battles)
	sortStationBattlesByEnd(stateBattles)
	stationBattleCache.Store(stationId, stationBattleState{
		Battles: stateBattles,
		Loaded:  true,
	})
}

func clearStationBattleState(stationId string) {
	if stationId == "" {
		return
	}
	stationBattleCache.Delete(stationId)
}

func hasLoadedStationBattles(stationId string) bool {
	if stationId == "" {
		return false
	}
	state, ok := stationBattleCache.Load(stationId)
	return ok && state.Loaded
}

func syncStationBattlesFromProto(station *Station, battleDetail *pogo.BreadBattleDetailProto) {
	if station == nil {
		return
	}
	now := time.Now().Unix()
	if battleDetail == nil {
		storeStationBattles(station.Id, nil)
		return
	}
	if battle := stationBattleFromProto(station.Id, battleDetail, now); battle != nil {
		upsertCachedStationBattle(*battle, now)
	}
}

func stationBattleFromProto(stationId string, battleDetail *pogo.BreadBattleDetailProto, updated int64) *StationBattleData {
	if stationId == "" || battleDetail == nil {
		return nil
	}
	battle := &StationBattleData{
		BreadBattleSeed: battleDetail.GetBreadBattleSeed(),
		StationId:       stationId,
		BattleLevel:     int16(battleDetail.GetBattleLevel()),
		BattleStart:     int64(battleDetail.GetBattleWindowStartMs() / 1000),
		BattleEnd:       int64(battleDetail.GetBattleWindowEndMs() / 1000),
		Updated:         updated,
	}
	if pokemon := battleDetail.GetBattlePokemon(); pokemon != nil {
		battle.BattlePokemonId = null.IntFrom(int64(pokemon.GetPokemonId()))
		battle.BattlePokemonMove1 = null.IntFrom(int64(pokemon.GetMove1()))
		battle.BattlePokemonMove2 = null.IntFrom(int64(pokemon.GetMove2()))
		battle.BattlePokemonForm = null.IntFrom(int64(pokemon.GetPokemonDisplay().GetForm()))
		battle.BattlePokemonCostume = null.IntFrom(int64(pokemon.GetPokemonDisplay().GetCostume()))
		battle.BattlePokemonGender = null.IntFrom(int64(pokemon.GetPokemonDisplay().GetGender()))
		battle.BattlePokemonAlignment = null.IntFrom(int64(pokemon.GetPokemonDisplay().GetAlignment()))
		battle.BattlePokemonBreadMode = null.IntFrom(int64(pokemon.GetPokemonDisplay().GetBreadModeEnum()))
		battle.BattlePokemonStamina = null.IntFrom(int64(pokemon.GetStamina()))
		battle.BattlePokemonCpMultiplier = null.FloatFrom(float64(pokemon.GetCpMultiplier()))
		if rewardPokemon := battleDetail.GetRewardPokemon(); rewardPokemon != nil && pokemon.GetPokemonId() != rewardPokemon.GetPokemonId() {
			log.Infof("[DYNAMAX] Pokemon reward differs from battle: Battle %v - Reward %v", pokemon, rewardPokemon)
		}
	}
	return battle
}

func sortStationBattlesByEnd(battles []StationBattleData) {
	slices.SortFunc(battles, func(a, b StationBattleData) int {
		if a.BattleEnd != b.BattleEnd {
			if a.BattleEnd < b.BattleEnd {
				return -1
			}
			return 1
		}
		if a.BattleStart != b.BattleStart {
			if a.BattleStart < b.BattleStart {
				return -1
			}
			return 1
		}
		switch {
		case a.BreadBattleSeed < b.BreadBattleSeed:
			return -1
		case a.BreadBattleSeed > b.BreadBattleSeed:
			return 1
		default:
			return 0
		}
	})
}

func nonExpiredStationBattlesFromSlice(battles []StationBattleData, now int64) []StationBattleData {
	if len(battles) == 0 {
		return nil
	}
	current := make([]StationBattleData, 0, len(battles))
	for _, battle := range battles {
		if battle.BattleEnd > now {
			current = append(current, battle)
		}
	}
	return current
}

func stationBattlesEqual(a []StationBattleData, b []StationBattleData) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		left := a[i]
		right := b[i]
		left.Updated = 0
		right.Updated = 0
		if left != right {
			return false
		}
	}
	return true
}

func upsertCachedStationBattle(battle StationBattleData, now int64) bool {
	state, _ := stationBattleCache.Load(battle.StationId)
	next := mergeStationBattles(state.Battles, battle, now)
	if state.Loaded && stationBattlesEqual(state.Battles, next) {
		return false
	}
	storeStationBattles(battle.StationId, next)
	return true
}

func mergeStationBattles(existing []StationBattleData, observed StationBattleData, now int64) []StationBattleData {
	next := make([]StationBattleData, 0, len(existing)+1)
	if observed.BattleEnd > now {
		next = append(next, observed)
	}
	for _, cached := range existing {
		if cached.BreadBattleSeed == observed.BreadBattleSeed || cached.BattleEnd <= now || cached.BattleEnd <= observed.BattleEnd {
			continue
		}
		next = append(next, cached)
	}
	sortStationBattlesByEnd(next)
	return next
}

func getKnownStationBattles(stationId string, now int64) []StationBattleData {
	if stationId == "" {
		return nil
	}
	state, ok := stationBattleCache.Load(stationId)
	if !ok {
		return nil
	}
	current := nonExpiredStationBattlesFromSlice(state.Battles, now)
	if len(current) > 0 {
		return current
	}
	if len(state.Battles) > 0 {
		stationBattleCache.Store(stationId, stationBattleState{Loaded: state.Loaded})
	}
	return nil
}

func topStationBattleFromSlice(battles []StationBattleData) *StationBattleData {
	if len(battles) == 0 {
		return nil
	}
	return &battles[0]
}

func stationBattleLevel(battle *StationBattleData) null.Int {
	if battle == nil {
		return null.Int{}
	}
	return null.IntFrom(int64(battle.BattleLevel))
}

func stationBattleStart(battle *StationBattleData) null.Int {
	if battle == nil {
		return null.Int{}
	}
	return null.IntFrom(battle.BattleStart)
}

func stationBattleEnd(battle *StationBattleData) null.Int {
	if battle == nil {
		return null.Int{}
	}
	return null.IntFrom(battle.BattleEnd)
}

func applyTopStationBattleToStation(station *Station, battles []StationBattleData) {
	battle := topStationBattleFromSlice(battles)
	station.SetBattleLevel(stationBattleLevel(battle))
	station.SetBattleStart(stationBattleStart(battle))
	station.SetBattleEnd(stationBattleEnd(battle))
	if battle == nil {
		station.SetBattlePokemonId(null.Int{})
		station.SetBattlePokemonForm(null.Int{})
		station.SetBattlePokemonCostume(null.Int{})
		station.SetBattlePokemonGender(null.Int{})
		station.SetBattlePokemonAlignment(null.Int{})
		station.SetBattlePokemonBreadMode(null.Int{})
		station.SetBattlePokemonMove1(null.Int{})
		station.SetBattlePokemonMove2(null.Int{})
		station.SetBattlePokemonStamina(null.Int{})
		station.SetBattlePokemonCpMultiplier(null.Float{})
		return
	}
	station.SetBattlePokemonId(battle.BattlePokemonId)
	station.SetBattlePokemonForm(battle.BattlePokemonForm)
	station.SetBattlePokemonCostume(battle.BattlePokemonCostume)
	station.SetBattlePokemonGender(battle.BattlePokemonGender)
	station.SetBattlePokemonAlignment(battle.BattlePokemonAlignment)
	station.SetBattlePokemonBreadMode(battle.BattlePokemonBreadMode)
	station.SetBattlePokemonMove1(battle.BattlePokemonMove1)
	station.SetBattlePokemonMove2(battle.BattlePokemonMove2)
	station.SetBattlePokemonStamina(battle.BattlePokemonStamina)
	station.SetBattlePokemonCpMultiplier(battle.BattlePokemonCpMultiplier)
}

func applyTopStationBattleToApiStationResult(result *ApiStationResult, battles []StationBattleData) {
	battle := topStationBattleFromSlice(battles)
	result.BattleLevel = stationBattleLevel(battle)
	result.BattleStart = stationBattleStart(battle)
	result.BattleEnd = stationBattleEnd(battle)
	if battle == nil {
		return
	}
	result.BattlePokemonId = battle.BattlePokemonId
	result.BattlePokemonForm = battle.BattlePokemonForm
	result.BattlePokemonCostume = battle.BattlePokemonCostume
	result.BattlePokemonGender = battle.BattlePokemonGender
	result.BattlePokemonAlignment = battle.BattlePokemonAlignment
	result.BattlePokemonBreadMode = battle.BattlePokemonBreadMode
	result.BattlePokemonMove1 = battle.BattlePokemonMove1
	result.BattlePokemonMove2 = battle.BattlePokemonMove2
}

func applyTopStationBattleToStationWebhook(hook *StationWebhook, battles []StationBattleData) {
	battle := topStationBattleFromSlice(battles)
	hook.BattleLevel = stationBattleLevel(battle)
	hook.BattleStart = stationBattleStart(battle)
	hook.BattleEnd = stationBattleEnd(battle)
	if battle == nil {
		return
	}
	hook.BattlePokemonId = battle.BattlePokemonId
	hook.BattlePokemonForm = battle.BattlePokemonForm
	hook.BattlePokemonCostume = battle.BattlePokemonCostume
	hook.BattlePokemonGender = battle.BattlePokemonGender
	hook.BattlePokemonAlignment = battle.BattlePokemonAlignment
	hook.BattlePokemonBreadMode = battle.BattlePokemonBreadMode
	hook.BattlePokemonMove1 = battle.BattlePokemonMove1
	hook.BattlePokemonMove2 = battle.BattlePokemonMove2
}

func applyTopStationBattleToFortLookup(lookup *FortLookup, battles []StationBattleData) {
	battle := topStationBattleFromSlice(battles)
	if battle == nil {
		return
	}
	lookup.BattleEndTimestamp = battle.BattleEnd
	lookup.BattleLevel = int8(battle.BattleLevel)
	lookup.BattlePokemonId = int16(battle.BattlePokemonId.ValueOrZero())
	lookup.BattlePokemonForm = int16(battle.BattlePokemonForm.ValueOrZero())
}

func buildFortLookupStationBattlesFromSlice(battles []StationBattleData) []FortLookupStationBattle {
	if len(battles) == 0 {
		return nil
	}
	result := make([]FortLookupStationBattle, 0, len(battles))
	for _, battle := range battles {
		result = append(result, FortLookupStationBattle{
			BattleEndTimestamp: battle.BattleEnd,
			BattleLevel:        int8(battle.BattleLevel),
			BattlePokemonId:    int16(battle.BattlePokemonId.ValueOrZero()),
			BattlePokemonForm:  int16(battle.BattlePokemonForm.ValueOrZero()),
		})
	}
	return result
}

func flattenStationBattleWrites(snapshots []stationBattleWrite) ([]StationBattleData, []string) {
	if len(snapshots) == 0 {
		return nil, nil
	}
	stationIds := make([]string, 0, len(snapshots))
	seenStations := make(map[string]struct{}, len(snapshots))
	var battles []StationBattleData
	for _, snapshot := range snapshots {
		if snapshot.StationId == "" {
			continue
		}
		if _, ok := seenStations[snapshot.StationId]; !ok {
			seenStations[snapshot.StationId] = struct{}{}
			stationIds = append(stationIds, snapshot.StationId)
		}
		battles = append(battles, snapshot.Battles...)
	}
	return battles, stationIds
}

func buildDeleteObsoleteStationBattlesQuery(stationIds []string, battles []StationBattleData) (string, []any) {
	if len(stationIds) == 0 {
		return "", nil
	}
	args := make([]any, 0, len(stationIds)+len(battles))
	var builder strings.Builder
	builder.WriteString("DELETE FROM station_battle WHERE station_id IN (")
	for i, stationId := range stationIds {
		if i > 0 {
			builder.WriteByte(',')
		}
		builder.WriteByte('?')
		args = append(args, stationId)
	}
	builder.WriteByte(')')
	if len(battles) == 0 {
		return builder.String(), args
	}
	builder.WriteString(" AND bread_battle_seed NOT IN (")
	for i, battle := range battles {
		if i > 0 {
			builder.WriteByte(',')
		}
		builder.WriteByte('?')
		args = append(args, battle.BreadBattleSeed)
	}
	builder.WriteByte(')')
	return builder.String(), args
}

func flushStationBattleBatch(ctx context.Context, dbDetails db.DbDetails, snapshots []stationBattleWrite) error {
	battles, stationIds := flattenStationBattleWrites(snapshots)
	if len(stationIds) == 0 {
		return nil
	}
	tx, err := dbDetails.GeneralDb.BeginTxx(ctx, nil)
	statsCollector.IncDbQuery("begin station_battle", err)
	if err != nil {
		return err
	}

	if len(battles) > 0 {
		if _, err = tx.NamedExecContext(ctx, stationBattleBatchUpsertQuery, battles); err != nil {
			_ = tx.Rollback()
			statsCollector.IncDbQuery("upsert station_battle", err)
			return err
		}
		statsCollector.IncDbQuery("upsert station_battle", nil)
	}

	deleteQuery, deleteArgs := buildDeleteObsoleteStationBattlesQuery(stationIds, battles)
	if deleteQuery != "" {
		if _, err = tx.ExecContext(ctx, deleteQuery, deleteArgs...); err != nil {
			_ = tx.Rollback()
			statsCollector.IncDbQuery("delete obsolete station_battle", err)
			return err
		}
		statsCollector.IncDbQuery("delete obsolete station_battle", nil)
	}

	err = tx.Commit()
	statsCollector.IncDbQuery("commit station_battle", err)
	return err
}

func loadStationBattlesForStation(ctx context.Context, dbDetails db.DbDetails, stationId string, now int64) ([]StationBattleData, error) {
	var battles []StationBattleData
	err := dbDetails.GeneralDb.SelectContext(ctx, &battles, `
		SELECT `+stationBattleSelectColumns+`
		FROM station_battle
		WHERE station_id = ? AND battle_end > ?
		ORDER BY battle_end ASC, battle_start ASC, bread_battle_seed ASC
	`, stationId, now)
	statsCollector.IncDbQuery("select station_battle station", err)
	if err != nil {
		return nil, err
	}
	return battles, nil
}

func hydrateStationBattlesForStation(ctx context.Context, dbDetails db.DbDetails, station *Station, now int64) error {
	if station == nil || station.Id == "" {
		return nil
	}
	battles, err := loadStationBattlesForStation(ctx, dbDetails, station.Id, now)
	if err != nil {
		return err
	}
	storeStationBattles(station.Id, battles)
	return nil
}

func finalizePreloadedStationBattles(populateRtree bool) {
	stationCache.Range(func(item *ttlcache.Item[string, *Station]) bool {
		stationId := item.Key()
		if _, ok := stationBattleCache.Load(stationId); !ok {
			storeStationBattles(stationId, nil)
		}
		if populateRtree {
			station := item.Value()
			station.Lock("preloadStationBattles")
			fortRtreeUpdateStationOnSave(station)
			station.Unlock()
		}
		return true
	})
}

func preloadStationBattles(dbDetails db.DbDetails, populateRtree bool) int32 {
	now := time.Now().Unix()
	query := "SELECT " + stationBattleSelectColumns + " FROM station_battle WHERE battle_end > ? " +
		"ORDER BY station_id, battle_end ASC, battle_start ASC, bread_battle_seed ASC"
	rows, err := dbDetails.GeneralDb.Queryx(query, now)
	statsCollector.IncDbQuery("select station_battle non_expired", err)
	if err != nil {
		log.Errorf("Preload: failed to query station battles - %s", err)
		return 0
	}
	defer rows.Close()

	count := int32(0)
	currentStationId := ""
	currentBattles := make([]StationBattleData, 0)
	flushCurrent := func() {
		storeStationBattles(currentStationId, currentBattles)
		currentStationId = ""
		currentBattles = nil
	}
	for rows.Next() {
		var battle StationBattleData
		if err := rows.StructScan(&battle); err != nil {
			log.Errorf("Preload: station battle scan error - %s", err)
			continue
		}
		if currentStationId != "" && battle.StationId != currentStationId {
			flushCurrent()
		}
		if currentStationId == "" {
			currentStationId = battle.StationId
		}
		currentBattles = append(currentBattles, battle)
		count++
	}
	flushCurrent()

	finalizePreloadedStationBattles(populateRtree)

	return count
}
