package decoder

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/guregu/null/v6"
	"github.com/jellydator/ttlcache/v3"
	"github.com/paulmach/orb/geojson"
	log "github.com/sirupsen/logrus"

	"golbat/config"
	"golbat/db"
	"golbat/webhooks"
)

// pokestopSelectColumns defines the columns for pokestop queries.
// Used by both single-row and bulk load queries to keep them in sync.
const pokestopSelectColumns = `id, lat, lon, name, url, enabled, lure_expire_timestamp, last_modified_timestamp,
	updated, quest_type, quest_timestamp, quest_target, quest_conditions,
	quest_rewards, quest_template, quest_title, quest_expiry,
	quest_reward_type, quest_item_id, quest_reward_amount, quest_pokemon_id, quest_pokemon_form_id,
	alternative_quest_type, alternative_quest_timestamp, alternative_quest_target,
	alternative_quest_conditions, alternative_quest_rewards,
	alternative_quest_template, alternative_quest_title, alternative_quest_expiry,
	alternative_quest_reward_type, alternative_quest_item_id, alternative_quest_reward_amount,
	alternative_quest_pokemon_id, alternative_quest_pokemon_form_id,
	cell_id, deleted, lure_id, sponsor_id, partner_id,
	ar_scan_eligible, power_up_points, power_up_level, power_up_end_timestamp,
	description, showcase_pokemon_id, showcase_pokemon_form_id,
	showcase_pokemon_type_id, showcase_ranking_standard, showcase_expiry, showcase_rankings`

func loadPokestopFromDatabase(ctx context.Context, db db.DbDetails, fortId string, pokestop *Pokestop) error {
	err := db.GeneralDb.GetContext(ctx, pokestop,
		`SELECT `+pokestopSelectColumns+` FROM pokestop WHERE id = ?`, fortId)
	statsCollector.IncDbQuery("select pokestop", err)
	return err
}

// PeekPokestopRecord - cache-only lookup, no DB fallback, returns locked.
// Caller MUST call returned unlock function if non-nil.
func PeekPokestopRecord(fortId string) (*Pokestop, func(), error) {
	if item := pokestopCache.Get(fortId); item != nil {
		pokestop := item.Value()
		pokestop.Lock()
		return pokestop, func() { pokestop.Unlock() }, nil
	}
	return nil, nil, nil
}

// DoesPokestopExist checks if a pokestop exists in cache or database without acquiring a lock.
// This is useful for checking if a fort was converted from a pokestop before doing cross-entity updates.
func DoesPokestopExist(ctx context.Context, db db.DbDetails, fortId string) bool {
	// Check cache first (fast path)
	if item := pokestopCache.Get(fortId); item != nil {
		return true
	}

	// Check database
	var exists bool
	err := db.GeneralDb.GetContext(ctx, &exists, "SELECT EXISTS(SELECT 1 FROM pokestop WHERE id = ?)", fortId)
	if err != nil {
		return false
	}
	return exists
}

// getPokestopRecordReadOnly acquires lock but does NOT take snapshot.
// Use for read-only checks. Will cause a backing database lookup.
// Caller MUST call returned unlock function if non-nil.
func getPokestopRecordReadOnly(ctx context.Context, db db.DbDetails, fortId string) (*Pokestop, func(), error) {
	// Check cache first
	if item := pokestopCache.Get(fortId); item != nil {
		pokestop := item.Value()
		pokestop.Lock()
		return pokestop, func() { pokestop.Unlock() }, nil
	}

	dbPokestop := Pokestop{}
	err := loadPokestopFromDatabase(ctx, db, fortId, &dbPokestop)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}

	dbPokestop.ClearDirty()

	// Atomically cache the loaded Pokestop - if another goroutine raced us,
	// we'll get their Pokestop and use that instead (ensuring same mutex)
	existingPokestop, _ := pokestopCache.GetOrSetFunc(fortId, func() *Pokestop {
		// Only called if key doesn't exist - our Pokestop wins
		if config.Config.FortInMemory {
			fortRtreeUpdatePokestopOnGet(&dbPokestop)
		}
		return &dbPokestop
	})

	pokestop := existingPokestop.Value()
	pokestop.Lock()
	return pokestop, func() { pokestop.Unlock() }, nil
}

// getPokestopRecordForUpdate acquires lock AND takes snapshot for webhook comparison.
// Use when modifying the Pokestop.
// Caller MUST call returned unlock function if non-nil.
func getPokestopRecordForUpdate(ctx context.Context, db db.DbDetails, fortId string) (*Pokestop, func(), error) {
	pokestop, unlock, err := getPokestopRecordReadOnly(ctx, db, fortId)
	if err != nil || pokestop == nil {
		return nil, nil, err
	}
	pokestop.snapshotOldValues()
	return pokestop, unlock, nil
}

// getOrCreatePokestopRecord gets existing or creates new, locked with snapshot.
// Caller MUST call returned unlock function.
func getOrCreatePokestopRecord(ctx context.Context, db db.DbDetails, fortId string) (*Pokestop, func(), error) {
	// Create new Pokestop atomically - function only called if key doesn't exist
	pokestopItem, _ := pokestopCache.GetOrSetFunc(fortId, func() *Pokestop {
		return &Pokestop{Id: fortId, newRecord: true}
	})

	pokestop := pokestopItem.Value()
	pokestop.Lock()

	if pokestop.newRecord {
		// We should attempt to load from database
		err := loadPokestopFromDatabase(ctx, db, fortId, pokestop)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				pokestop.Unlock()
				return nil, nil, err
			}
		} else {
			// We loaded from DB
			pokestop.newRecord = false
			pokestop.ClearDirty()
			if config.Config.FortInMemory {
				fortRtreeUpdatePokestopOnGet(pokestop)
			}
		}
	}

	pokestop.snapshotOldValues()
	return pokestop, func() { pokestop.Unlock() }, nil
}

type QuestWebhook struct {
	PokestopId     string          `json:"pokestop_id"`
	Latitude       float64         `json:"latitude"`
	Longitude      float64         `json:"longitude"`
	PokestopName   string          `json:"pokestop_name"`
	Type           null.Int        `json:"type"`
	Target         null.Int        `json:"target"`
	Template       null.String     `json:"template"`
	Title          null.String     `json:"title"`
	Conditions     json.RawMessage `json:"conditions"`
	Rewards        json.RawMessage `json:"rewards"`
	Updated        int64           `json:"updated"`
	ArScanEligible int64           `json:"ar_scan_eligible"`
	PokestopUrl    string          `json:"pokestop_url"`
	WithAr         bool            `json:"with_ar"`
	QuestSeed      null.Int        `json:"quest_seed"`
}

type PokestopWebhook struct {
	PokestopId              string          `json:"pokestop_id"`
	Latitude                float64         `json:"latitude"`
	Longitude               float64         `json:"longitude"`
	Name                    string          `json:"name"`
	Url                     string          `json:"url"`
	LureExpiration          int64           `json:"lure_expiration"`
	LastModified            int64           `json:"last_modified"`
	Enabled                 bool            `json:"enabled"`
	LureId                  int16           `json:"lure_id"`
	ArScanEligible          int64           `json:"ar_scan_eligible"`
	PowerUpLevel            int64           `json:"power_up_level"`
	PowerUpPoints           int64           `json:"power_up_points"`
	PowerUpEndTimestamp     int64           `json:"power_up_end_timestamp"`
	Updated                 int64           `json:"updated"`
	ShowcaseFocus           null.String     `json:"showcase_focus"`
	ShowcasePokemonId       null.Int        `json:"showcase_pokemon_id"`
	ShowcasePokemonFormId   null.Int        `json:"showcase_pokemon_form_id"`
	ShowcasePokemonTypeId   null.Int        `json:"showcase_pokemon_type_id"`
	ShowcaseRankingStandard null.Int        `json:"showcase_ranking_standard"`
	ShowcaseExpiry          null.Int        `json:"showcase_expiry"`
	ShowcaseRankings        json.RawMessage `json:"showcase_rankings"`
}

func createPokestopFortWebhooks(stop *Pokestop) {
	fort := InitWebHookFortFromPokestop(stop)
	if stop.newRecord {
		CreateFortWebHooks(nil, fort, NEW)
	} else {
		// Build old fort from saved old values
		oldFort := &FortWebhook{
			Type:        POKESTOP.String(),
			Id:          stop.Id,
			Name:        stop.oldValues.Name.Ptr(),
			ImageUrl:    stop.oldValues.Url.Ptr(),
			Description: stop.oldValues.Description.Ptr(),
			Location:    Location{Latitude: stop.oldValues.Lat, Longitude: stop.oldValues.Lon},
		}
		CreateFortWebHooks(oldFort, fort, EDIT)
	}
}

func createPokestopWebhooks(stop *Pokestop) {

	areas := MatchStatsGeofence(stop.Lat, stop.Lon)

	pokestopName := "Unknown"
	if stop.Name.Valid {
		pokestopName = stop.Name.String
	}

	if stop.AlternativeQuestType.Valid && (stop.newRecord || stop.AlternativeQuestType != stop.oldValues.AlternativeQuestType) {
		questHook := QuestWebhook{
			PokestopId:     stop.Id,
			Latitude:       stop.Lat,
			Longitude:      stop.Lon,
			PokestopName:   pokestopName,
			Type:           stop.AlternativeQuestType,
			Target:         stop.AlternativeQuestTarget,
			Template:       stop.AlternativeQuestTemplate,
			Title:          stop.AlternativeQuestTitle,
			Conditions:     json.RawMessage(stop.AlternativeQuestConditions.ValueOrZero()),
			Rewards:        json.RawMessage(stop.AlternativeQuestRewards.ValueOrZero()),
			Updated:        stop.Updated,
			ArScanEligible: stop.ArScanEligible.ValueOrZero(),
			PokestopUrl:    stop.Url.ValueOrZero(),
			WithAr:         false,
			QuestSeed:      stop.AlternativeQuestSeed,
		}
		webhooksSender.AddMessage(webhooks.Quest, questHook, areas)
	}

	if stop.QuestType.Valid && (stop.newRecord || stop.QuestType != stop.oldValues.QuestType) {
		questHook := QuestWebhook{
			PokestopId:     stop.Id,
			Latitude:       stop.Lat,
			Longitude:      stop.Lon,
			PokestopName:   pokestopName,
			Type:           stop.QuestType,
			Target:         stop.QuestTarget,
			Template:       stop.QuestTemplate,
			Title:          stop.QuestTitle,
			Conditions:     json.RawMessage(stop.QuestConditions.ValueOrZero()),
			Rewards:        json.RawMessage(stop.QuestRewards.ValueOrZero()),
			Updated:        stop.Updated,
			ArScanEligible: stop.ArScanEligible.ValueOrZero(),
			PokestopUrl:    stop.Url.ValueOrZero(),
			WithAr:         true,
			QuestSeed:      stop.QuestSeed,
		}
		webhooksSender.AddMessage(webhooks.Quest, questHook, areas)
	}
	if (stop.newRecord && (stop.LureId != 0 || stop.PowerUpEndTimestamp.ValueOrZero() != 0)) || (!stop.newRecord && ((stop.LureExpireTimestamp != stop.oldValues.LureExpireTimestamp && stop.LureId != 0) || stop.PowerUpEndTimestamp != stop.oldValues.PowerUpEndTimestamp)) {
		var showcaseRankings json.RawMessage
		if stop.ShowcaseRankings.Valid {
			showcaseRankings = json.RawMessage(stop.ShowcaseRankings.ValueOrZero())
		}

		pokestopHook := PokestopWebhook{
			PokestopId:              stop.Id,
			Latitude:                stop.Lat,
			Longitude:               stop.Lon,
			Name:                    pokestopName,
			Url:                     stop.Url.ValueOrZero(),
			LureExpiration:          stop.LureExpireTimestamp.ValueOrZero(),
			LastModified:            stop.LastModifiedTimestamp.ValueOrZero(),
			Enabled:                 stop.Enabled.ValueOrZero(),
			LureId:                  stop.LureId,
			ArScanEligible:          stop.ArScanEligible.ValueOrZero(),
			PowerUpLevel:            stop.PowerUpLevel.ValueOrZero(),
			PowerUpPoints:           stop.PowerUpPoints.ValueOrZero(),
			PowerUpEndTimestamp:     stop.PowerUpEndTimestamp.ValueOrZero(),
			Updated:                 stop.Updated,
			ShowcaseFocus:           stop.ShowcaseFocus,
			ShowcasePokemonId:       stop.ShowcasePokemon,
			ShowcasePokemonFormId:   stop.ShowcasePokemonForm,
			ShowcasePokemonTypeId:   stop.ShowcasePokemonType,
			ShowcaseRankingStandard: stop.ShowcaseRankingStandard,
			ShowcaseExpiry:          stop.ShowcaseExpiry,
			ShowcaseRankings:        showcaseRankings,
		}

		webhooksSender.AddMessage(webhooks.Pokestop, pokestopHook, areas)
	}
}

func savePokestopRecord(ctx context.Context, db db.DbDetails, pokestop *Pokestop) {
	now := time.Now().Unix()
	if !pokestop.IsNewRecord() && !pokestop.IsDirty() && !pokestop.IsInternalDirty() {
		// default debounce is 15 minutes (900s). If reduce_updates is enabled, use 12 hours.
		if pokestop.Updated > now-GetUpdateThreshold(900) {
			// if a pokestop is unchanged and was seen recently, skip saving
			return
		}
	}
	pokestop.SetUpdated(now)

	// Capture isNewRecord before state changes
	isNewRecord := pokestop.IsNewRecord()

	// Debug logging happens here, before queueing
	if dbDebugEnabled {
		if pokestop.IsDirty() {
			if isNewRecord {
				dbDebugLog("INSERT", "Pokestop", pokestop.Id, pokestop.changedFields)
			} else {
				dbDebugLog("UPDATE", "Pokestop", pokestop.Id, pokestop.changedFields)
			}
		} else {
			dbDebugLog("MEMORY", "Pokestop", pokestop.Id, pokestop.changedFields)
		}
	}

	// Queue the write through the write-behind system (no delay for pokestops)
	// Only queue if dirty (not just internalDirty)
	if pokestop.IsDirty() {
		if writeBehindQueue != nil {
			writeBehindQueue.Enqueue(pokestop, isNewRecord, 0)
		} else {
			// Fallback to direct write if queue not initialized
			_ = pokestopWriteDB(db, pokestop, isNewRecord)
		}
	}

	if dbDebugEnabled {
		pokestop.changedFields = pokestop.changedFields[:0]
	}

	if config.Config.FortInMemory {
		fortRtreeUpdatePokestopOnSave(pokestop)
	}

	// Webhooks happen immediately (not queued)
	createPokestopWebhooks(pokestop)
	createPokestopFortWebhooks(pokestop)

	if isNewRecord {
		pokestopCache.Set(pokestop.Id, pokestop, ttlcache.DefaultTTL)
		pokestop.newRecord = false
	}
	pokestop.ClearDirty()
}

// pokestopWriteDB performs the actual database INSERT or UPDATE for a Pokestop
// This is called by both direct writes and the write-behind queue
func pokestopWriteDB(db db.DbDetails, pokestop *Pokestop, isNewRecord bool) error {
	ctx := context.Background()

	if isNewRecord {
		_, err := db.GeneralDb.NamedExecContext(ctx, `
			INSERT INTO pokestop (
				id, lat, lon, name, url, enabled, lure_expire_timestamp, last_modified_timestamp, quest_type,
				quest_timestamp, quest_target, quest_conditions, quest_rewards, quest_template, quest_title,
				quest_expiry, quest_reward_type, quest_item_id, quest_reward_amount, quest_pokemon_id, quest_pokemon_form_id,
				alternative_quest_type, alternative_quest_timestamp, alternative_quest_target,
				alternative_quest_conditions, alternative_quest_rewards, alternative_quest_template,
				alternative_quest_title, alternative_quest_expiry, alternative_quest_reward_type, alternative_quest_item_id,
				alternative_quest_reward_amount, alternative_quest_pokemon_id, alternative_quest_pokemon_form_id,
				cell_id, lure_id, sponsor_id, partner_id, ar_scan_eligible,
				power_up_points, power_up_level, power_up_end_timestamp, updated, first_seen_timestamp,
				description, showcase_focus, showcase_pokemon_id,
				showcase_pokemon_form_id, showcase_pokemon_type_id, showcase_ranking_standard, showcase_expiry, showcase_rankings
				)
				VALUES (
				:id, :lat, :lon, :name, :url, :enabled, :lure_expire_timestamp, :last_modified_timestamp, :quest_type,
				:quest_timestamp, :quest_target, :quest_conditions, :quest_rewards, :quest_template, :quest_title,
				:quest_expiry, :quest_reward_type, :quest_item_id, :quest_reward_amount, :quest_pokemon_id, :quest_pokemon_form_id,
				:alternative_quest_type, :alternative_quest_timestamp, :alternative_quest_target,
				:alternative_quest_conditions, :alternative_quest_rewards, :alternative_quest_template,
				:alternative_quest_title, :alternative_quest_expiry, :alternative_quest_reward_type, :alternative_quest_item_id,
				:alternative_quest_reward_amount, :alternative_quest_pokemon_id, :alternative_quest_pokemon_form_id,
				:cell_id, :lure_id, :sponsor_id, :partner_id, :ar_scan_eligible,
				:power_up_points, :power_up_level, :power_up_end_timestamp,
				UNIX_TIMESTAMP(), UNIX_TIMESTAMP(),
				:description, :showcase_focus, :showcase_pokemon_id,
				:showcase_pokemon_form_id, :showcase_pokemon_type_id, :showcase_ranking_standard, :showcase_expiry, :showcase_rankings)`,
			pokestop)

		statsCollector.IncDbQuery("insert pokestop", err)
		if err != nil {
			log.Errorf("insert pokestop: %s", err)
			return err
		}
	} else {
		_, err := db.GeneralDb.NamedExecContext(ctx, `
			UPDATE pokestop SET
				lat = :lat,
				lon = :lon,
				name = :name,
				url = :url,
				enabled = :enabled,
				lure_expire_timestamp = :lure_expire_timestamp,
				last_modified_timestamp = :last_modified_timestamp,
				updated = :updated,
				quest_type = :quest_type,
				quest_timestamp = :quest_timestamp,
				quest_target = :quest_target,
				quest_conditions = :quest_conditions,
				quest_rewards = :quest_rewards,
				quest_template = :quest_template,
				quest_title = :quest_title,
				quest_expiry = :quest_expiry,
				quest_reward_type = :quest_reward_type,
				quest_item_id = :quest_item_id,
				quest_reward_amount = :quest_reward_amount,
				quest_pokemon_id = :quest_pokemon_id,
				quest_pokemon_form_id = :quest_pokemon_form_id,
				alternative_quest_type = :alternative_quest_type,
				alternative_quest_timestamp = :alternative_quest_timestamp,
				alternative_quest_target = :alternative_quest_target,
				alternative_quest_conditions = :alternative_quest_conditions,
				alternative_quest_rewards = :alternative_quest_rewards,
				alternative_quest_template = :alternative_quest_template,
				alternative_quest_title = :alternative_quest_title,
				alternative_quest_expiry = :alternative_quest_expiry,
				alternative_quest_reward_type = :alternative_quest_reward_type,
				alternative_quest_item_id = :alternative_quest_item_id,
				alternative_quest_reward_amount = :alternative_quest_reward_amount,
				alternative_quest_pokemon_id = :alternative_quest_pokemon_id,
				alternative_quest_pokemon_form_id = :alternative_quest_pokemon_form_id,
				cell_id = :cell_id,
				lure_id = :lure_id,
				deleted = :deleted,
				sponsor_id = :sponsor_id,
				partner_id = :partner_id,
				ar_scan_eligible = :ar_scan_eligible,
				power_up_points = :power_up_points,
				power_up_level = :power_up_level,
				power_up_end_timestamp = :power_up_end_timestamp,
				description = :description,
				showcase_focus = :showcase_focus,
				showcase_pokemon_id = :showcase_pokemon_id,
				showcase_pokemon_form_id = :showcase_pokemon_form_id,
				showcase_pokemon_type_id = :showcase_pokemon_type_id,
				showcase_ranking_standard = :showcase_ranking_standard,
				showcase_expiry = :showcase_expiry,
				showcase_rankings = :showcase_rankings
			WHERE id = :id`,
			pokestop,
		)
		statsCollector.IncDbQuery("update pokestop", err)
		if err != nil {
			log.Errorf("update pokestop %s: %s", pokestop.Id, err)
			return err
		}
	}
	return nil
}

func updatePokestopGetMapFortCache(pokestop *Pokestop) {
	storedGetMapFort := getMapFortsCache.Get(pokestop.Id)
	if storedGetMapFort != nil {
		getMapFort := storedGetMapFort.Value()
		getMapFortsCache.Delete(pokestop.Id)
		pokestop.updatePokestopFromGetMapFortsOutProto(getMapFort)
		log.Debugf("Updated Gym using stored getMapFort: %s", pokestop.Id)
	}
}

// RemoveQuestsWithinGeofence clears all quest fields for pokestops within a geofence
// Uses cache and write-behind queue for consistency
func RemoveQuestsWithinGeofence(ctx context.Context, dbDetails db.DbDetails, geofence *geojson.Feature) (int, error) {
	bbox := geofence.Geometry.Bound()
	bytes, err := geofence.MarshalJSON()
	if err != nil {
		return 0, err
	}

	// Query for pokestop IDs within the geofence
	var pokestopIds []string
	err = dbDetails.GeneralDb.SelectContext(ctx, &pokestopIds,
		"SELECT id FROM pokestop "+
			"WHERE lat >= ? AND lon >= ? AND lat <= ? AND lon <= ? AND enabled = 1 "+
			"AND ST_CONTAINS(ST_GeomFromGeoJSON('"+string(bytes)+"', 2, 0), POINT(lon, lat))",
		bbox.Min.Lat(), bbox.Min.Lon(), bbox.Max.Lat(), bbox.Max.Lon())
	statsCollector.IncDbQuery("select pokestops for quest removal", err)
	if err != nil {
		return 0, err
	}

	clearedCount := 0

	for _, id := range pokestopIds {
		pokestop, unlock, err := getOrCreatePokestopRecord(ctx, dbDetails, id)
		if err != nil {
			log.Errorf("RemoveQuestsWithinGeofence: failed to get pokestop %s: %v", id, err)
			continue
		}

		// Clear regular quest fields
		pokestop.SetQuestType(null.Int{})
		pokestop.SetQuestTimestamp(null.Int{})
		pokestop.SetQuestTarget(null.Int{})
		pokestop.SetQuestConditions(null.String{})
		pokestop.SetQuestRewards(null.String{})
		pokestop.SetQuestTemplate(null.String{})
		pokestop.SetQuestTitle(null.String{})
		pokestop.SetQuestExpiry(null.Int{})
		pokestop.SetQuestRewardType(null.Int{})
		pokestop.SetQuestItemId(null.Int{})
		pokestop.SetQuestRewardAmount(null.Int{})
		pokestop.SetQuestPokemonId(null.Int{})
		pokestop.SetQuestPokemonFormId(null.Int{})

		// Clear alternative quest fields
		pokestop.SetAlternativeQuestType(null.Int{})
		pokestop.SetAlternativeQuestTimestamp(null.Int{})
		pokestop.SetAlternativeQuestTarget(null.Int{})
		pokestop.SetAlternativeQuestConditions(null.String{})
		pokestop.SetAlternativeQuestRewards(null.String{})
		pokestop.SetAlternativeQuestTemplate(null.String{})
		pokestop.SetAlternativeQuestTitle(null.String{})
		pokestop.SetAlternativeQuestExpiry(null.Int{})
		pokestop.SetAlternativeQuestRewardType(null.Int{})
		pokestop.SetAlternativeQuestItemId(null.Int{})
		pokestop.SetAlternativeQuestRewardAmount(null.Int{})
		pokestop.SetAlternativeQuestPokemonId(null.Int{})
		pokestop.SetAlternativeQuestPokemonFormId(null.Int{})

		if pokestop.IsDirty() {
			savePokestopRecord(ctx, dbDetails, pokestop)
			clearedCount++
		}
		unlock()
	}

	return clearedCount, nil
}

// ExpireQuests finds pokestops with expired quests, clears quest fields, and saves through write-behind queue
func ExpireQuests(ctx context.Context, dbDetails db.DbDetails) (int, error) {
	now := time.Now().Unix()

	// Query for pokestop IDs with expired regular quests
	var expiredQuestIds []string
	err := dbDetails.GeneralDb.SelectContext(ctx, &expiredQuestIds,
		"SELECT id FROM pokestop WHERE quest_expiry IS NOT NULL AND quest_expiry < ?", now)
	statsCollector.IncDbQuery("select expired quests", err)
	if err != nil {
		return 0, err
	}

	// Query for pokestop IDs with expired alternative quests
	var expiredAltQuestIds []string
	err = dbDetails.GeneralDb.SelectContext(ctx, &expiredAltQuestIds,
		"SELECT id FROM pokestop WHERE alternative_quest_expiry IS NOT NULL AND alternative_quest_expiry < ?", now)
	statsCollector.IncDbQuery("select expired alt quests", err)
	if err != nil {
		return 0, err
	}

	// Build sets for quick lookup
	hasExpiredQuest := make(map[string]bool, len(expiredQuestIds))
	for _, id := range expiredQuestIds {
		hasExpiredQuest[id] = true
	}

	hasExpiredAltQuest := make(map[string]bool, len(expiredAltQuestIds))
	for _, id := range expiredAltQuestIds {
		hasExpiredAltQuest[id] = true
	}

	// Combine and deduplicate IDs
	allIds := make(map[string]bool, len(expiredQuestIds)+len(expiredAltQuestIds))
	for _, id := range expiredQuestIds {
		allIds[id] = true
	}
	for _, id := range expiredAltQuestIds {
		allIds[id] = true
	}

	expiredCount := 0

	// Process each pokestop once, clearing both quest types if needed
	for id := range allIds {
		pokestop, unlock, err := getOrCreatePokestopRecord(ctx, dbDetails, id)
		if err != nil {
			log.Errorf("ExpireQuests: failed to get pokestop %s: %v", id, err)
			continue
		}

		// Clear regular quest fields if expired
		if hasExpiredQuest[id] {
			pokestop.SetQuestType(null.Int{})
			pokestop.SetQuestTimestamp(null.Int{})
			pokestop.SetQuestTarget(null.Int{})
			pokestop.SetQuestConditions(null.String{})
			pokestop.SetQuestRewards(null.String{})
			pokestop.SetQuestTemplate(null.String{})
			pokestop.SetQuestTitle(null.String{})
			pokestop.SetQuestExpiry(null.Int{})
			pokestop.SetQuestRewardType(null.Int{})
			pokestop.SetQuestItemId(null.Int{})
			pokestop.SetQuestRewardAmount(null.Int{})
			pokestop.SetQuestPokemonId(null.Int{})
			pokestop.SetQuestPokemonFormId(null.Int{})
		}

		// Clear alternative quest fields if expired
		if hasExpiredAltQuest[id] {
			pokestop.SetAlternativeQuestType(null.Int{})
			pokestop.SetAlternativeQuestTimestamp(null.Int{})
			pokestop.SetAlternativeQuestTarget(null.Int{})
			pokestop.SetAlternativeQuestConditions(null.String{})
			pokestop.SetAlternativeQuestRewards(null.String{})
			pokestop.SetAlternativeQuestTemplate(null.String{})
			pokestop.SetAlternativeQuestTitle(null.String{})
			pokestop.SetAlternativeQuestExpiry(null.Int{})
			pokestop.SetAlternativeQuestRewardType(null.Int{})
			pokestop.SetAlternativeQuestItemId(null.Int{})
			pokestop.SetAlternativeQuestRewardAmount(null.Int{})
			pokestop.SetAlternativeQuestPokemonId(null.Int{})
			pokestop.SetAlternativeQuestPokemonFormId(null.Int{})
		}

		if pokestop.IsDirty() {
			savePokestopRecord(ctx, dbDetails, pokestop)
			expiredCount++
		}
		unlock()
	}

	return expiredCount, nil
}
