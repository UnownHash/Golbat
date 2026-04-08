package decoder

import (
	"context"
	"slices"
	"strconv"
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

type StationBattleWrite struct {
	StationId string
	Battles   []StationBattleData
}

type stationBattleSnapshot struct {
	Battles   []StationBattleData
	Canonical *StationBattleData
	Signature string
}

const stationBattleSelectColumns = `bread_battle_seed, station_id, battle_level, battle_start, battle_end,
	battle_pokemon_id, battle_pokemon_form, battle_pokemon_costume, battle_pokemon_gender,
	battle_pokemon_alignment, battle_pokemon_bread_mode, battle_pokemon_move_1, battle_pokemon_move_2,
	battle_pokemon_stamina, battle_pokemon_cp_multiplier, updated`

var stationBattleCache *xsync.MapOf[string, []StationBattleData]
var stationBattleHydratedCache *xsync.MapOf[string, struct{}]

func initStationBattleCache() {
	stationBattleCache = xsync.NewMapOf[string, []StationBattleData]()
	stationBattleHydratedCache = xsync.NewMapOf[string, struct{}]()
}

func markStationBattlesHydrated(stationId string) {
	if stationId == "" {
		return
	}
	stationBattleHydratedCache.Store(stationId, struct{}{})
}

func clearStationBattleCaches(stationId string) {
	if stationId == "" {
		return
	}
	stationBattleCache.Delete(stationId)
	stationBattleHydratedCache.Delete(stationId)
}

func hasHydratedStationBattles(stationId string) bool {
	if stationId == "" {
		return false
	}
	_, ok := stationBattleHydratedCache.Load(stationId)
	return ok
}

func syncStationBattlesFromProto(station *Station, battleDetail *pogo.BreadBattleDetailProto) {
	if station == nil {
		return
	}
	now := time.Now().Unix()
	if battleDetail == nil {
		clearStationBattleCaches(station.Id)
		markStationBattlesHydrated(station.Id)
		applyStationBattleProjection(station, nil)
		return
	}
	if battle := stationBattleFromProto(station.Id, battleDetail, now); battle != nil {
		upsertCachedStationBattle(*battle, now)
	}
	markStationBattlesHydrated(station.Id)

	snapshot := collectStationBattleSnapshot(station, now)
	applyStationBattleProjection(station, snapshot.Canonical)
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
			if len(current) > 0 {
				return current
			}
			stationBattleCache.Delete(stationId)
			if hasHydratedStationBattles(stationId) {
				return nil
			}
		}
		if hasHydratedStationBattles(stationId) {
			return nil
		}
	}
	if fallback := stationBattleFromStationProjection(station); fallback != nil && fallback.BattleEnd > now {
		return []StationBattleData{*fallback}
	}
	return nil
}

func collectStationBattleSnapshot(station *Station, now int64) stationBattleSnapshot {
	battles := getKnownStationBattles(station.Id, station, now)
	return stationBattleSnapshot{
		Battles:   battles,
		Canonical: canonicalStationBattleFromSlice(station, battles, now),
		Signature: stationBattleSignatureFromSlice(battles),
	}
}

func getActiveStationBattles(stationId string, station *Station, now int64) []StationBattleData {
	return activeStationBattlesFromSlice(getKnownStationBattles(stationId, station, now), now)
}

func stationBattleMatchesProjection(battle StationBattleData, projection *StationBattleData) bool {
	if projection == nil {
		return false
	}
	return battle.BattleLevel == projection.BattleLevel &&
		battle.BattleStart == projection.BattleStart &&
		battle.BattleEnd == projection.BattleEnd &&
		battle.BattlePokemonId == projection.BattlePokemonId &&
		battle.BattlePokemonForm == projection.BattlePokemonForm
}

func canonicalStationBattleFromSlice(station *Station, battles []StationBattleData, now int64) *StationBattleData {
	if len(battles) == 0 {
		return nil
	}
	projection := stationBattleFromStationProjection(station)
	if projection != nil && stationBattleIsActive(*projection, now) {
		for _, battle := range battles {
			if stationBattleIsActive(battle, now) && stationBattleMatchesProjection(battle, projection) {
				current := battle
				return &current
			}
		}
	}
	for _, battle := range battles {
		if stationBattleIsActive(battle, now) {
			current := battle
			return &current
		}
	}
	if projection != nil {
		for _, battle := range battles {
			if stationBattleMatchesProjection(battle, projection) {
				current := battle
				return &current
			}
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

func stationBattleSignatureFromSlice(battles []StationBattleData) string {
	if len(battles) == 0 {
		return ""
	}
	var builder strings.Builder
	for _, battle := range battles {
		builder.WriteString(strconv.FormatInt(battle.BreadBattleSeed, 10))
		builder.WriteByte(':')
		builder.WriteString(strconv.FormatInt(int64(battle.BattleLevel), 10))
		builder.WriteByte(':')
		builder.WriteString(strconv.FormatInt(battle.BattleStart, 10))
		builder.WriteByte(':')
		builder.WriteString(strconv.FormatInt(battle.BattleEnd, 10))
		builder.WriteByte(':')
		builder.WriteString(strconv.FormatInt(battle.BattlePokemonId.ValueOrZero(), 10))
		builder.WriteByte(':')
		builder.WriteString(strconv.FormatInt(battle.BattlePokemonForm.ValueOrZero(), 10))
		builder.WriteByte(':')
		builder.WriteString(strconv.FormatInt(battle.BattlePokemonCostume.ValueOrZero(), 10))
		builder.WriteByte(':')
		builder.WriteString(strconv.FormatInt(battle.BattlePokemonGender.ValueOrZero(), 10))
		builder.WriteByte(':')
		builder.WriteString(strconv.FormatInt(battle.BattlePokemonAlignment.ValueOrZero(), 10))
		builder.WriteByte(':')
		builder.WriteString(strconv.FormatInt(battle.BattlePokemonBreadMode.ValueOrZero(), 10))
		builder.WriteByte(':')
		builder.WriteString(strconv.FormatInt(battle.BattlePokemonMove1.ValueOrZero(), 10))
		builder.WriteByte(':')
		builder.WriteString(strconv.FormatBool(battle.BattlePokemonMove2.Valid))
		builder.WriteByte(':')
		builder.WriteString(strconv.FormatBool(battle.BattlePokemonCpMultiplier.Valid))
		builder.WriteByte(':')
		builder.WriteString(strconv.FormatInt(battle.BattlePokemonMove2.ValueOrZero(), 10))
		builder.WriteByte(':')
		builder.WriteString(strconv.FormatFloat(battle.BattlePokemonCpMultiplier.ValueOrZero(), 'g', -1, 64))
		builder.WriteByte(';')
	}
	return builder.String()
}

func buildApiStationBattlesFromSlice(battles []StationBattleData) []ApiStationBattle {
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

func buildStationWebhookBattlesFromSlice(battles []StationBattleData) []StationBattleWebhook {
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

func stationBattleWriteFromSlice(stationId string, battles []StationBattleData) StationBattleWrite {
	return StationBattleWrite{
		StationId: stationId,
		Battles:   cloneStationBattles(battles),
	}
}

func storeStationBattleSnapshot(ctx context.Context, dbDetails db.DbDetails, snapshot StationBattleWrite) error {
	tx, err := dbDetails.GeneralDb.BeginTxx(ctx, nil)
	statsCollector.IncDbQuery("begin station_battle", err)
	if err != nil {
		return err
	}

	if len(snapshot.Battles) > 0 {
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
		`, snapshot.Battles); err != nil {
			_ = tx.Rollback()
			statsCollector.IncDbQuery("upsert station_battle", err)
			return err
		}
		statsCollector.IncDbQuery("upsert station_battle", nil)
	}

	deleteQuery := "DELETE FROM station_battle WHERE station_id = ?"
	deleteArgs := []any{snapshot.StationId}
	if len(snapshot.Battles) > 0 {
		deleteQuery += " AND bread_battle_seed NOT IN ("
		for i, battle := range snapshot.Battles {
			if i > 0 {
				deleteQuery += ","
			}
			deleteQuery += "?"
			deleteArgs = append(deleteArgs, battle.BreadBattleSeed)
		}
		deleteQuery += ")"
	}
	if _, err = tx.ExecContext(ctx, deleteQuery, deleteArgs...); err != nil {
		_ = tx.Rollback()
		statsCollector.IncDbQuery("delete obsolete station_battle", err)
		return err
	}
	statsCollector.IncDbQuery("delete obsolete station_battle", nil)

	err = tx.Commit()
	statsCollector.IncDbQuery("commit station_battle", err)
	return err
}

func flushStationBattleBatch(ctx context.Context, dbDetails db.DbDetails, snapshots []StationBattleWrite) error {
	var firstErr error
	for _, snapshot := range snapshots {
		if err := storeStationBattleSnapshot(ctx, dbDetails, snapshot); err != nil {
			log.Errorf("flush station_battle %s: %v", snapshot.StationId, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
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

func hydrateStationBattlesForStation(ctx context.Context, dbDetails db.DbDetails, station *Station, now int64) error {
	if station == nil || station.Id == "" {
		return nil
	}
	battles, err := loadStationBattlesForStation(ctx, dbDetails, station.Id, now)
	if err != nil {
		return err
	}
	if len(battles) == 0 {
		stationBattleCache.Delete(station.Id)
		markStationBattlesHydrated(station.Id)
		return nil
	}
	stationBattleCache.Store(station.Id, battles)
	markStationBattlesHydrated(station.Id)
	return nil
}

func cachePreloadedStationBattles(stationId string, battles []StationBattleData) bool {
	if stationId == "" || len(battles) == 0 {
		return false
	}
	sortStationBattlesByEnd(battles)
	stationBattleCache.Store(stationId, battles)
	markStationBattlesHydrated(stationId)
	return true
}

func markPreloadedStationsHydrated(populateRtree bool) {
	stationCache.Range(func(item *ttlcache.Item[string, *Station]) bool {
		stationId := item.Key()
		markStationBattlesHydrated(stationId)
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
		"ORDER BY station_id, battle_end DESC, battle_start DESC, bread_battle_seed DESC"
	rows, err := dbDetails.GeneralDb.Queryx(query, now)
	statsCollector.IncDbQuery("select station_battle active", err)
	if err != nil {
		log.Errorf("Preload: failed to query station battles - %s", err)
		return 0
	}
	defer rows.Close()

	count := int32(0)
	currentStationId := ""
	currentBattles := make([]StationBattleData, 0)
	flushCurrent := func() {
		cachePreloadedStationBattles(currentStationId, currentBattles)
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

	markPreloadedStationsHydrated(populateRtree)

	return count
}
