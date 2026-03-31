package decoder

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/guregu/null/v6"
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

type ApiStationBattle struct {
	BreadBattleSeed           int64      `json:"bread_battle_seed,omitempty"`
	BattleLevel               int16      `json:"battle_level"`
	BattleStart               int64      `json:"battle_start"`
	BattleEnd                 int64      `json:"battle_end"`
	BattlePokemonId           null.Int   `json:"battle_pokemon_id"`
	BattlePokemonForm         null.Int   `json:"battle_pokemon_form"`
	BattlePokemonCostume      null.Int   `json:"battle_pokemon_costume"`
	BattlePokemonGender       null.Int   `json:"battle_pokemon_gender"`
	BattlePokemonAlignment    null.Int   `json:"battle_pokemon_alignment"`
	BattlePokemonBreadMode    null.Int   `json:"battle_pokemon_bread_mode"`
	BattlePokemonMove1        null.Int   `json:"battle_pokemon_move_1"`
	BattlePokemonMove2        null.Int   `json:"battle_pokemon_move_2"`
	BattlePokemonStamina      null.Int   `json:"battle_pokemon_stamina"`
	BattlePokemonCpMultiplier null.Float `json:"battle_pokemon_cp_multiplier"`
}

type StationBattleWebhook struct {
	BreadBattleSeed           int64      `json:"bread_battle_seed,omitempty"`
	BattleLevel               int16      `json:"battle_level"`
	BattleStart               int64      `json:"battle_start"`
	BattleEnd                 int64      `json:"battle_end"`
	BattlePokemonId           null.Int   `json:"battle_pokemon_id"`
	BattlePokemonForm         null.Int   `json:"battle_pokemon_form"`
	BattlePokemonCostume      null.Int   `json:"battle_pokemon_costume"`
	BattlePokemonGender       null.Int   `json:"battle_pokemon_gender"`
	BattlePokemonAlignment    null.Int   `json:"battle_pokemon_alignment"`
	BattlePokemonBreadMode    null.Int   `json:"battle_pokemon_bread_mode"`
	BattlePokemonMove1        null.Int   `json:"battle_pokemon_move_1"`
	BattlePokemonMove2        null.Int   `json:"battle_pokemon_move_2"`
	BattlePokemonStamina      null.Int   `json:"battle_pokemon_stamina"`
	BattlePokemonCpMultiplier null.Float `json:"battle_pokemon_cp_multiplier"`
}

type FortLookupStationBattle struct {
	BattleEndTimestamp int64
	BattleLevel        int8
	BattlePokemonId    int16
	BattlePokemonForm  int16
}

const stationBattleSelectColumns = `bread_battle_seed, station_id, battle_level, battle_start, battle_end,
	battle_pokemon_id, battle_pokemon_form, battle_pokemon_costume, battle_pokemon_gender,
	battle_pokemon_alignment, battle_pokemon_bread_mode, battle_pokemon_move_1, battle_pokemon_move_2,
	battle_pokemon_stamina, battle_pokemon_cp_multiplier, updated`

var stationBattleCache *xsync.MapOf[string, []StationBattleData]
var upsertStationBattleRecordFunc = storeStationBattleRecord

func initStationBattleCache() {
	stationBattleCache = xsync.NewMapOf[string, []StationBattleData]()
}

func syncStationBattlesFromProto(ctx context.Context, dbDetails db.DbDetails, station *Station, battleDetail *pogo.BreadBattleDetailProto) {
	now := time.Now().Unix()
	if battle := stationBattleFromProto(station.Id, battleDetail, now); battle != nil {
		if err := upsertStationBattleRecordFunc(ctx, dbDetails, *battle); err != nil {
			log.Errorf("upsert station battle %s/%d: %v", station.Id, battle.BreadBattleSeed, err)
			restoreStationBattleProjectionFromOldValues(station)
			station.skipWebhook = true
			return
		} else if upsertCachedStationBattle(*battle, now) {
			station.MarkBattleListChanged()
		}
	}

	battles := getKnownStationBattles(station.Id, station, now)
	applyStationBattleProjection(station, canonicalStationBattleFromSlice(battles, now))
	if station.oldValues.BattleListSignature != stationBattleSignatureFromSlice(battles) {
		station.MarkBattleListChanged()
	}
}

func stationBattleFromProto(stationId string, battleDetail *pogo.BreadBattleDetailProto, updated int64) *StationBattleData {
	if stationId == "" || battleDetail == nil {
		return nil
	}
	seed := battleDetail.GetBreadBattleSeed()
	battle := &StationBattleData{
		BreadBattleSeed: seed,
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
	}
	return battle
}

func stationBattleFromStationProjection(station *Station) *StationBattleData {
	if station == nil || !station.BattleEnd.Valid {
		return nil
	}
	return &StationBattleData{
		StationId:                 station.Id,
		BattleLevel:               int16(station.BattleLevel.ValueOrZero()),
		BattleStart:               station.BattleStart.ValueOrZero(),
		BattleEnd:                 station.BattleEnd.ValueOrZero(),
		BattlePokemonId:           station.BattlePokemonId,
		BattlePokemonForm:         station.BattlePokemonForm,
		BattlePokemonCostume:      station.BattlePokemonCostume,
		BattlePokemonGender:       station.BattlePokemonGender,
		BattlePokemonAlignment:    station.BattlePokemonAlignment,
		BattlePokemonBreadMode:    station.BattlePokemonBreadMode,
		BattlePokemonMove1:        station.BattlePokemonMove1,
		BattlePokemonMove2:        station.BattlePokemonMove2,
		BattlePokemonStamina:      station.BattlePokemonStamina,
		BattlePokemonCpMultiplier: station.BattlePokemonCpMultiplier,
		Updated:                   station.Updated,
	}
}

func cloneStationBattles(battles []StationBattleData) []StationBattleData {
	if len(battles) == 0 {
		return nil
	}
	return append([]StationBattleData(nil), battles...)
}

func sortStationBattlesByEnd(battles []StationBattleData) {
	slices.SortFunc(battles, func(a, b StationBattleData) int {
		if a.BattleEnd != b.BattleEnd {
			if a.BattleEnd > b.BattleEnd {
				return -1
			}
			return 1
		}
		if a.BattleStart != b.BattleStart {
			if a.BattleStart > b.BattleStart {
				return -1
			}
			return 1
		}
		switch {
		case a.BreadBattleSeed > b.BreadBattleSeed:
			return -1
		case a.BreadBattleSeed < b.BreadBattleSeed:
			return 1
		default:
			return 0
		}
	})
}

func stationBattleIsActive(battle StationBattleData, now int64) bool {
	if battle.BattleEnd <= now {
		return false
	}
	if battle.BattleStart == 0 {
		return true
	}
	return battle.BattleStart <= now
}

func activeStationBattlesFromSlice(battles []StationBattleData, now int64) []StationBattleData {
	if len(battles) == 0 {
		return nil
	}
	active := make([]StationBattleData, 0, len(battles))
	for _, battle := range battles {
		if stationBattleIsActive(battle, now) {
			active = append(active, battle)
		}
	}
	sortStationBattlesByEnd(active)
	return active
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
	sortStationBattlesByEnd(current)
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
	existing, _ := stationBattleCache.Load(battle.StationId)
	next := pruneObsoleteStationBattles(existing, battle, now)
	sortStationBattlesByEnd(next)
	if stationBattlesEqual(existing, next) {
		return false
	}
	if len(next) == 0 {
		stationBattleCache.Delete(battle.StationId)
	} else {
		stationBattleCache.Store(battle.StationId, next)
	}
	return true
}

func pruneObsoleteStationBattles(existing []StationBattleData, battle StationBattleData, now int64) []StationBattleData {
	next := make([]StationBattleData, 0, len(existing)+1)
	if battle.BattleEnd > now {
		next = append(next, battle)
	}
	for _, cached := range existing {
		if cached.BreadBattleSeed == battle.BreadBattleSeed || cached.BattleEnd <= now || cached.BattleEnd <= battle.BattleEnd {
			continue
		}
		next = append(next, cached)
	}
	return next
}

func getKnownStationBattles(stationId string, station *Station, now int64) []StationBattleData {
	if stationId != "" {
		if cached, ok := stationBattleCache.Load(stationId); ok {
			current := nonExpiredStationBattlesFromSlice(cached, now)
			if !stationBattlesEqual(cached, current) {
				if len(current) == 0 {
					stationBattleCache.Delete(stationId)
				} else {
					stationBattleCache.Store(stationId, current)
				}
			}
			if len(current) > 0 {
				return cloneStationBattles(current)
			}
		}
	}
	if fallback := stationBattleFromStationProjection(station); fallback != nil && fallback.BattleEnd > now {
		return []StationBattleData{*fallback}
	}
	return nil
}

func getActiveStationBattles(stationId string, station *Station, now int64) []StationBattleData {
	return activeStationBattlesFromSlice(getKnownStationBattles(stationId, station, now), now)
}

func canonicalStationBattleFromSlice(battles []StationBattleData, now int64) *StationBattleData {
	if len(battles) == 0 {
		return nil
	}
	for _, battle := range battles {
		if stationBattleIsActive(battle, now) {
			current := battle
			return &current
		}
	}
	battle := battles[0]
	return &battle
}

func canonicalBattleSeed(battle *StationBattleData) int64 {
	if battle == nil {
		return 0
	}
	return battle.BreadBattleSeed
}

func clearStationBattleProjection(station *Station) {
	station.SetBattleLevel(null.Int{})
	station.SetBattleStart(null.Int{})
	station.SetBattleEnd(null.Int{})
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
}

func restoreStationBattleProjectionFromOldValues(station *Station) {
	station.SetIsBattleAvailable(station.oldValues.IsBattleAvailable)
	if station.oldValues.BattleProjection == nil {
		clearStationBattleProjection(station)
		return
	}
	battle := *station.oldValues.BattleProjection
	applyStationBattleProjection(station, &battle)
}

func applyStationBattleProjection(station *Station, battle *StationBattleData) {
	if battle == nil {
		clearStationBattleProjection(station)
		return
	}
	station.SetBattleLevel(null.IntFrom(int64(battle.BattleLevel)))
	station.SetBattleStart(null.IntFrom(battle.BattleStart))
	station.SetBattleEnd(null.IntFrom(battle.BattleEnd))
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

func stationBattleSignature(station *Station, now int64) string {
	return stationBattleSignatureFromSlice(getKnownStationBattles(station.Id, station, now))
}

func stationBattleSignatureFromSlice(battles []StationBattleData) string {
	if len(battles) == 0 {
		return ""
	}
	var builder strings.Builder
	for _, battle := range battles {
		builder.WriteString(fmt.Sprintf("%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%t:%t;",
			battle.BreadBattleSeed,
			battle.BattleLevel,
			battle.BattleStart,
			battle.BattleEnd,
			battle.BattlePokemonId.ValueOrZero(),
			battle.BattlePokemonForm.ValueOrZero(),
			battle.BattlePokemonCostume.ValueOrZero(),
			battle.BattlePokemonGender.ValueOrZero(),
			battle.BattlePokemonAlignment.ValueOrZero(),
			battle.BattlePokemonBreadMode.ValueOrZero(),
			battle.BattlePokemonMove1.ValueOrZero(),
			battle.BattlePokemonMove2.Valid,
			battle.BattlePokemonCpMultiplier.Valid,
		))
		builder.WriteString(fmt.Sprintf("%d:%g;", battle.BattlePokemonMove2.ValueOrZero(), battle.BattlePokemonCpMultiplier.ValueOrZero()))
	}
	return builder.String()
}

func buildApiStationBattles(station *Station, now int64) []ApiStationBattle {
	battles := getKnownStationBattles(station.Id, station, now)
	if len(battles) == 0 {
		return nil
	}
	result := make([]ApiStationBattle, 0, len(battles))
	for _, battle := range battles {
		result = append(result, ApiStationBattle{
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
	return result
}

func buildStationWebhookBattles(station *Station, now int64) []StationBattleWebhook {
	battles := getKnownStationBattles(station.Id, station, now)
	if len(battles) == 0 {
		return nil
	}
	result := make([]StationBattleWebhook, 0, len(battles))
	for _, battle := range battles {
		result = append(result, StationBattleWebhook{
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
	return result
}

func buildFortLookupStationBattles(station *Station, now int64) []FortLookupStationBattle {
	battles := getKnownStationBattles(station.Id, station, now)
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

func storeStationBattleRecord(ctx context.Context, dbDetails db.DbDetails, battle StationBattleData) error {
	tx, err := dbDetails.GeneralDb.BeginTxx(ctx, nil)
	statsCollector.IncDbQuery("begin station_battle", err)
	if err != nil {
		return err
	}

	if _, err = tx.NamedExecContext(ctx, `
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
	`, battle); err != nil {
		_ = tx.Rollback()
		statsCollector.IncDbQuery("upsert station_battle", err)
		return err
	}
	statsCollector.IncDbQuery("upsert station_battle", nil)

	if _, err = tx.ExecContext(ctx, `
		DELETE FROM station_battle
		WHERE station_id = ?
		  AND bread_battle_seed <> ?
		  AND battle_end <= ?
	`, battle.StationId, battle.BreadBattleSeed, battle.BattleEnd); err != nil {
		_ = tx.Rollback()
		statsCollector.IncDbQuery("delete obsolete station_battle", err)
		return err
	}
	statsCollector.IncDbQuery("delete obsolete station_battle", nil)

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
		ORDER BY battle_end DESC, battle_start DESC, bread_battle_seed DESC
	`, stationId, now)
	statsCollector.IncDbQuery("select station_battle station", err)
	if err != nil {
		return nil, err
	}
	return battles, nil
}

func hydrateStationBattlesForStation(ctx context.Context, dbDetails db.DbDetails, stationId string, now int64) error {
	if stationId == "" {
		return nil
	}
	battles, err := loadStationBattlesForStation(ctx, dbDetails, stationId, now)
	if err != nil {
		return err
	}
	if len(battles) == 0 {
		stationBattleCache.Delete(stationId)
		return nil
	}
	stationBattleCache.Store(stationId, battles)
	return nil
}

func cachePreloadedStationBattles(stationId string, battles []StationBattleData) bool {
	if stationId == "" || len(battles) == 0 {
		return false
	}
	sortStationBattlesByEnd(battles)
	stationBattleCache.Store(stationId, battles)
	return true
}

func preloadStationBattles(dbDetails db.DbDetails, populateRtree bool) int32 {
	now := time.Now().Unix()
	query := "SELECT " + stationBattleSelectColumns + " FROM station_battle WHERE battle_end > ? " +
		"ORDER BY station_id, battle_end DESC, battle_start DESC, bread_battle_seed DESC"
	rows, err := dbDetails.GeneralDb.Queryx(query, now)
	statsCollector.IncDbQuery("select station_battle active", err)
	if err != nil {
		log.Errorf("Preload: failed to query station battles - %s", err)
		return 0
	}
	defer rows.Close()

	count := int32(0)
	affected := make([]string, 0)
	currentStationId := ""
	currentBattles := make([]StationBattleData, 0)
	flushCurrent := func() {
		if cachePreloadedStationBattles(currentStationId, currentBattles) {
			affected = append(affected, currentStationId)
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
		count++
	}
	flushCurrent()

	if populateRtree {
		for _, stationId := range affected {
			station, unlock, _ := peekStationRecord(stationId, "preloadStationBattles")
			if station == nil {
				continue
			}
			updateStationLookup(station)
			unlock()
		}
	}
	return count
}
