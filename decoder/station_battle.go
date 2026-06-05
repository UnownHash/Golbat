package decoder

import (
	"cmp"
	"context"
	"encoding/binary"
	"hash/maphash"
	"math"
	"slices"
	"time"

	"github.com/guregu/null/v6"
	"github.com/jellydator/ttlcache/v3"
	"github.com/jmoiron/sqlx"
	"github.com/puzpuzpuz/xsync/v3"
	log "github.com/sirupsen/logrus"

	"golbat/db"
	"golbat/pogo"
)

type StationBattleData struct {
	BreadBattleSeed           int64      `db:"bread_battle_seed"`
	StationId                 string     `db:"station_id"`
	BattleLevel               int16      `db:"battle_level"`
	BattleStart               int64      `db:"battle_start"`
	BattleEnd                 int64      `db:"battle_end"`
	BattlePokemonId           null.Int   `db:"battle_pokemon_id"`
	BattlePokemonForm         null.Int   `db:"battle_pokemon_form"`
	BattlePokemonCostume      null.Int   `db:"battle_pokemon_costume"`
	BattlePokemonGender       null.Int   `db:"battle_pokemon_gender"`
	BattlePokemonAlignment    null.Int   `db:"battle_pokemon_alignment"`
	BattlePokemonBreadMode    null.Int   `db:"battle_pokemon_bread_mode"`
	BattlePokemonMove1        null.Int   `db:"battle_pokemon_move_1"`
	BattlePokemonMove2        null.Int   `db:"battle_pokemon_move_2"`
	BattlePokemonStamina      null.Int   `db:"battle_pokemon_stamina"`
	BattlePokemonCpMultiplier null.Float `db:"battle_pokemon_cp_multiplier"`
	Updated                   int64      `db:"updated"`
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

type stationBattleSnapshot struct {
	Count              int
	Signature          uint64
	TopBreadBattleSeed int64
	HasTopBreadBattle  bool
}

type stationBattleProjection struct {
	BattleLevel               null.Int
	BattleStart               null.Int
	BattleEnd                 null.Int
	BattlePokemonId           null.Int
	BattlePokemonForm         null.Int
	BattlePokemonCostume      null.Int
	BattlePokemonGender       null.Int
	BattlePokemonAlignment    null.Int
	BattlePokemonBreadMode    null.Int
	BattlePokemonMove1        null.Int
	BattlePokemonMove2        null.Int
	BattlePokemonStamina      null.Int
	BattlePokemonCpMultiplier null.Float
}

const stationBattleSelectColumns = `bread_battle_seed, station_id, battle_level, battle_start, battle_end,
	battle_pokemon_id, battle_pokemon_form, battle_pokemon_costume, battle_pokemon_gender,
	battle_pokemon_alignment, battle_pokemon_bread_mode, battle_pokemon_move_1, battle_pokemon_move_2,
	battle_pokemon_stamina, battle_pokemon_cp_multiplier, updated`

const stationBattleSelectColumnsQualified = `sb.bread_battle_seed, sb.station_id, sb.battle_level, sb.battle_start, sb.battle_end,
	sb.battle_pokemon_id, sb.battle_pokemon_form, sb.battle_pokemon_costume, sb.battle_pokemon_gender,
	sb.battle_pokemon_alignment, sb.battle_pokemon_bread_mode, sb.battle_pokemon_move_1, sb.battle_pokemon_move_2,
	sb.battle_pokemon_stamina, sb.battle_pokemon_cp_multiplier, sb.updated`

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

var (
	stationBattleCache        *xsync.MapOf[string, stationBattleState]
	stationBattleSnapshotSeed = maphash.MakeSeed()
)

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
		return cmp.Compare(a.BattleEnd, b.BattleEnd)
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

func snapshotStationBattles(battles []StationBattleData) stationBattleSnapshot {
	if len(battles) == 0 {
		return stationBattleSnapshot{}
	}
	var h maphash.Hash
	h.SetSeed(stationBattleSnapshotSeed)
	for _, battle := range battles {
		hashStationBattle(&h, battle)
	}
	topBattle := topStationBattleFromSlice(battles)
	return stationBattleSnapshot{
		Count:              len(battles),
		Signature:          h.Sum64(),
		TopBreadBattleSeed: topBattle.BreadBattleSeed,
		HasTopBreadBattle:  true,
	}
}

func hashStationBattle(h *maphash.Hash, battle StationBattleData) {
	writeInt64(h, battle.BreadBattleSeed)
	writeString(h, battle.StationId)
	writeInt64(h, int64(battle.BattleLevel))
	writeInt64(h, battle.BattleStart)
	writeInt64(h, battle.BattleEnd)
	writeNullInt(h, battle.BattlePokemonId)
	writeNullInt(h, battle.BattlePokemonForm)
	writeNullInt(h, battle.BattlePokemonCostume)
	writeNullInt(h, battle.BattlePokemonGender)
	writeNullInt(h, battle.BattlePokemonAlignment)
	writeNullInt(h, battle.BattlePokemonBreadMode)
	writeNullInt(h, battle.BattlePokemonMove1)
	writeNullInt(h, battle.BattlePokemonMove2)
	writeNullInt(h, battle.BattlePokemonStamina)
	writeNullFloat(h, battle.BattlePokemonCpMultiplier)
}

func writeInt64(h *maphash.Hash, v int64) {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(v))
	_, _ = h.Write(buf[:])
}

func writeString(h *maphash.Hash, v string) {
	writeInt64(h, int64(len(v)))
	h.WriteString(v)
}

func writeNullInt(h *maphash.Hash, v null.Int) {
	writeInt64(h, boolAsInt64(v.Valid))
	if v.Valid {
		writeInt64(h, v.Int64)
	}
}

func writeNullFloat(h *maphash.Hash, v null.Float) {
	writeInt64(h, boolAsInt64(v.Valid))
	if v.Valid {
		writeInt64(h, int64(math.Float64bits(v.Float64)))
	}
}

func boolAsInt64(v bool) int64 {
	if v {
		return 1
	}
	return 0
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

// getKnownStationBattles returns the non-expired battle list already loaded for a station.
// Callers that pair this with Station fields must hold that station's lock, except during preload/test setup where no live readers exist.
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

func stationBattleProjectionFromBattle(battle *StationBattleData) stationBattleProjection {
	if battle == nil {
		return stationBattleProjection{}
	}
	return stationBattleProjection{
		BattleLevel:               stationBattleLevel(battle),
		BattleStart:               stationBattleStart(battle),
		BattleEnd:                 stationBattleEnd(battle),
		BattlePokemonId:           battle.BattlePokemonId,
		BattlePokemonForm:         battle.BattlePokemonForm,
		BattlePokemonCostume:      battle.BattlePokemonCostume,
		BattlePokemonGender:       battle.BattlePokemonGender,
		BattlePokemonAlignment:    battle.BattlePokemonAlignment,
		BattlePokemonBreadMode:    battle.BattlePokemonBreadMode,
		BattlePokemonMove1:        battle.BattlePokemonMove1,
		BattlePokemonMove2:        battle.BattlePokemonMove2,
		BattlePokemonStamina:      battle.BattlePokemonStamina,
		BattlePokemonCpMultiplier: battle.BattlePokemonCpMultiplier,
	}
}

func applyTopStationBattleToStation(station *Station, battles []StationBattleData) {
	projection := stationBattleProjectionFromBattle(topStationBattleFromSlice(battles))
	station.SetBattleLevel(projection.BattleLevel)
	station.SetBattleStart(projection.BattleStart)
	station.SetBattleEnd(projection.BattleEnd)
	station.SetBattlePokemonId(projection.BattlePokemonId)
	station.SetBattlePokemonForm(projection.BattlePokemonForm)
	station.SetBattlePokemonCostume(projection.BattlePokemonCostume)
	station.SetBattlePokemonGender(projection.BattlePokemonGender)
	station.SetBattlePokemonAlignment(projection.BattlePokemonAlignment)
	station.SetBattlePokemonBreadMode(projection.BattlePokemonBreadMode)
	station.SetBattlePokemonMove1(projection.BattlePokemonMove1)
	station.SetBattlePokemonMove2(projection.BattlePokemonMove2)
	station.SetBattlePokemonStamina(projection.BattlePokemonStamina)
	station.SetBattlePokemonCpMultiplier(projection.BattlePokemonCpMultiplier)
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

func buildApiStationBattleResults(battles []StationBattleData) []ApiStationBattleResult {
	if len(battles) == 0 {
		return nil
	}
	results := make([]ApiStationBattleResult, 0, len(battles))
	for _, battle := range battles {
		results = append(results, ApiStationBattleResult{
			BreadBattleSeed:           battle.BreadBattleSeed,
			BattleLevel:               battle.BattleLevel,
			BattleStart:               battle.BattleStart,
			BattleEnd:                 battle.BattleEnd,
			BattlePokemonId:           battle.BattlePokemonId,
			BattlePokemonForm:         battle.BattlePokemonForm,
			BattlePokemonCostume:      battle.BattlePokemonCostume,
			BattlePokemonGender:       battle.BattlePokemonGender,
			BattlePokemonAlignment:    battle.BattlePokemonAlignment,
			BattlePokemonBreadMode:    battle.BattlePokemonBreadMode,
			BattlePokemonMove1:        battle.BattlePokemonMove1,
			BattlePokemonMove2:        battle.BattlePokemonMove2,
			BattlePokemonStamina:      battle.BattlePokemonStamina,
			BattlePokemonCpMultiplier: battle.BattlePokemonCpMultiplier,
		})
	}
	return results
}

func buildStationBattleWebhooks(battles []StationBattleData) []StationBattleWebhook {
	if len(battles) == 0 {
		return nil
	}
	results := make([]StationBattleWebhook, 0, len(battles))
	for _, battle := range battles {
		results = append(results, StationBattleWebhook{
			BreadBattleSeed:           battle.BreadBattleSeed,
			BattleLevel:               battle.BattleLevel,
			BattleStart:               battle.BattleStart,
			BattleEnd:                 battle.BattleEnd,
			BattlePokemonId:           battle.BattlePokemonId,
			BattlePokemonForm:         battle.BattlePokemonForm,
			BattlePokemonCostume:      battle.BattlePokemonCostume,
			BattlePokemonGender:       battle.BattlePokemonGender,
			BattlePokemonAlignment:    battle.BattlePokemonAlignment,
			BattlePokemonBreadMode:    battle.BattlePokemonBreadMode,
			BattlePokemonMove1:        battle.BattlePokemonMove1,
			BattlePokemonMove2:        battle.BattlePokemonMove2,
			BattlePokemonStamina:      battle.BattlePokemonStamina,
			BattlePokemonCpMultiplier: battle.BattlePokemonCpMultiplier,
		})
	}
	return results
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

func buildDeleteObsoleteStationBattlesQuery(stationIds []string, battles []StationBattleData) (string, []any, error) {
	if len(stationIds) == 0 {
		return "", nil, nil
	}
	if len(battles) == 0 {
		return sqlx.In("DELETE FROM station_battle WHERE station_id IN (?)", stationIds)
	}
	seeds := make([]int64, len(battles))
	for i, battle := range battles {
		seeds[i] = battle.BreadBattleSeed
	}
	return sqlx.In(
		"DELETE FROM station_battle WHERE station_id IN (?) AND bread_battle_seed NOT IN (?)",
		stationIds,
		seeds,
	)
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

	deleteQuery, deleteArgs, err := buildDeleteObsoleteStationBattlesQuery(stationIds, battles)
	if err != nil {
		_ = tx.Rollback()
		statsCollector.IncDbQuery("delete obsolete station_battle", err)
		return err
	}
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
		ORDER BY battle_end ASC
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
	query := "SELECT " + stationBattleSelectColumnsQualified + " FROM station_battle sb " +
		"JOIN station s ON s.id = sb.station_id " +
		"WHERE sb.battle_end > ? ORDER BY sb.station_id, sb.battle_end ASC"
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
		if currentStationId != "" && stationCache.Get(currentStationId) != nil {
			storeStationBattles(currentStationId, currentBattles)
			count += int32(len(currentBattles))
		}
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
	}
	flushCurrent()

	finalizePreloadedStationBattles(populateRtree)

	return count
}
