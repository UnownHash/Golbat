package decoder

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/guregu/null/v6"
	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"

	"golbat/config"
	"golbat/db"
	"golbat/webhooks"
)

func loadPokestopFromDatabase(ctx context.Context, db db.DbDetails, fortId string, pokestop *Pokestop) error {
	err := db.GeneralDb.GetContext(ctx, pokestop,
		`SELECT pokestop.id, lat, lon, name, url, enabled, lure_expire_timestamp, last_modified_timestamp,
			pokestop.updated, quest_type, quest_timestamp, quest_target, quest_conditions,
			quest_rewards, quest_template, quest_title,
			alternative_quest_type, alternative_quest_timestamp, alternative_quest_target,
			alternative_quest_conditions, alternative_quest_rewards,
			alternative_quest_template, alternative_quest_title, cell_id, deleted, lure_id, sponsor_id, partner_id,
			ar_scan_eligible, power_up_points, power_up_level, power_up_end_timestamp,
			quest_expiry, alternative_quest_expiry, description, showcase_pokemon_id, showcase_pokemon_form_id,
			showcase_pokemon_type_id, showcase_ranking_standard, showcase_expiry, showcase_rankings
			FROM pokestop
			WHERE pokestop.id = ? `, fortId)
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
		if config.Config.TestFortInMemory {
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
			if config.Config.TestFortInMemory {
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
	if !pokestop.IsNewRecord() && !pokestop.IsDirty() {
		// default debounce is 15 minutes (900s). If reduce_updates is enabled, use 12 hours.
		if pokestop.Updated > now-GetUpdateThreshold(900) {
			// if a pokestop is unchanged, but we did see it again after 15 minutes, then save again
			return
		}
	}
	pokestop.Updated = now

	if pokestop.IsNewRecord() {
		if dbDebugEnabled {
			dbDebugLog("INSERT", "Pokestop", pokestop.Id, pokestop.changedFields)
		}
		res, err := db.GeneralDb.NamedExecContext(ctx, `
			INSERT INTO pokestop (
				id, lat, lon, name, url, enabled, lure_expire_timestamp, last_modified_timestamp, quest_type,
				quest_timestamp, quest_target, quest_conditions, quest_rewards, quest_template, quest_title,
				alternative_quest_type, alternative_quest_timestamp, alternative_quest_target,
				alternative_quest_conditions, alternative_quest_rewards, alternative_quest_template,
				alternative_quest_title, cell_id, lure_id, sponsor_id, partner_id, ar_scan_eligible,
				power_up_points, power_up_level, power_up_end_timestamp, updated, first_seen_timestamp,
				quest_expiry, alternative_quest_expiry, description, showcase_focus, showcase_pokemon_id,
				showcase_pokemon_form_id, showcase_pokemon_type_id, showcase_ranking_standard, showcase_expiry, showcase_rankings
				)
				VALUES (
				:id, :lat, :lon, :name, :url, :enabled, :lure_expire_timestamp, :last_modified_timestamp, :quest_type,
				:quest_timestamp, :quest_target, :quest_conditions, :quest_rewards, :quest_template, :quest_title,
				:alternative_quest_type, :alternative_quest_timestamp, :alternative_quest_target,
				:alternative_quest_conditions, :alternative_quest_rewards, :alternative_quest_template,
				:alternative_quest_title, :cell_id, :lure_id, :sponsor_id, :partner_id, :ar_scan_eligible,
				:power_up_points, :power_up_level, :power_up_end_timestamp,
				UNIX_TIMESTAMP(), UNIX_TIMESTAMP(),
				:quest_expiry, :alternative_quest_expiry, :description, :showcase_focus, :showcase_pokemon_id,
				:showcase_pokemon_form_id, :showcase_pokemon_type_id, :showcase_ranking_standard, :showcase_expiry, :showcase_rankings)`,
			pokestop)

		statsCollector.IncDbQuery("insert pokestop", err)
		//log.Debugf("Insert pokestop %s %+v", pokestop.Id, pokestop)
		if err != nil {
			log.Errorf("insert pokestop: %s", err)
			return
		}

		_, _ = res, err
	} else {
		if dbDebugEnabled {
			dbDebugLog("UPDATE", "Pokestop", pokestop.Id, pokestop.changedFields)
		}
		res, err := db.GeneralDb.NamedExecContext(ctx, `
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
				alternative_quest_type = :alternative_quest_type,
				alternative_quest_timestamp = :alternative_quest_timestamp,
				alternative_quest_target = :alternative_quest_target,
				alternative_quest_conditions = :alternative_quest_conditions,
				alternative_quest_rewards = :alternative_quest_rewards,
				alternative_quest_template = :alternative_quest_template,
				alternative_quest_title = :alternative_quest_title,
				cell_id = :cell_id,
				lure_id = :lure_id,
				deleted = :deleted,
				sponsor_id = :sponsor_id,
				partner_id = :partner_id,
				ar_scan_eligible = :ar_scan_eligible,
				power_up_points = :power_up_points,
				power_up_level = :power_up_level,
				power_up_end_timestamp = :power_up_end_timestamp,
				quest_expiry = :quest_expiry,
				alternative_quest_expiry = :alternative_quest_expiry,
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
		//log.Debugf("Update pokestop %s %+v", pokestop.Id, pokestop)
		if err != nil {
			log.Errorf("update pokestop %s: %s", pokestop.Id, err)
			return
		}
		_ = res
	}
	//pokestopCache.Set(pokestop.Id, pokestop, ttlcache.DefaultTTL)
	if dbDebugEnabled {
		pokestop.changedFields = pokestop.changedFields[:0]
	}

	createPokestopWebhooks(pokestop)
	createPokestopFortWebhooks(pokestop)
	if pokestop.IsNewRecord() {
		pokestopCache.Set(pokestop.Id, pokestop, ttlcache.DefaultTTL)
		pokestop.newRecord = false
	}
	pokestop.ClearDirty()

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
