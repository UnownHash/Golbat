package decoder

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"github.com/paulmach/orb/geojson"
	log "github.com/sirupsen/logrus"
	"gopkg.in/guregu/null.v4"

	"golbat/config"
	"golbat/db"
	"golbat/pogo"
	"golbat/tz"
	"golbat/util"
	"golbat/webhooks"
)

// Pokestop struct.
type Pokestop struct {
	mu sync.Mutex `db:"-" json:"-"` // Object-level mutex

	Id                         string      `db:"id" json:"id"`
	Lat                        float64     `db:"lat" json:"lat"`
	Lon                        float64     `db:"lon" json:"lon"`
	Name                       null.String `db:"name" json:"name"`
	Url                        null.String `db:"url" json:"url"`
	LureExpireTimestamp        null.Int    `db:"lure_expire_timestamp" json:"lure_expire_timestamp"`
	LastModifiedTimestamp      null.Int    `db:"last_modified_timestamp" json:"last_modified_timestamp"`
	Updated                    int64       `db:"updated" json:"updated"`
	Enabled                    null.Bool   `db:"enabled" json:"enabled"`
	QuestType                  null.Int    `db:"quest_type" json:"quest_type"`
	QuestTimestamp             null.Int    `db:"quest_timestamp" json:"quest_timestamp"`
	QuestTarget                null.Int    `db:"quest_target" json:"quest_target"`
	QuestConditions            null.String `db:"quest_conditions" json:"quest_conditions"`
	QuestRewards               null.String `db:"quest_rewards" json:"quest_rewards"`
	QuestTemplate              null.String `db:"quest_template" json:"quest_template"`
	QuestTitle                 null.String `db:"quest_title" json:"quest_title"`
	QuestExpiry                null.Int    `db:"quest_expiry" json:"quest_expiry"`
	CellId                     null.Int    `db:"cell_id" json:"cell_id"`
	Deleted                    bool        `db:"deleted" json:"deleted"`
	LureId                     int16       `db:"lure_id" json:"lure_id"`
	FirstSeenTimestamp         int16       `db:"first_seen_timestamp" json:"first_seen_timestamp"`
	SponsorId                  null.Int    `db:"sponsor_id" json:"sponsor_id"`
	PartnerId                  null.String `db:"partner_id" json:"partner_id"`
	ArScanEligible             null.Int    `db:"ar_scan_eligible" json:"ar_scan_eligible"` // is an 8
	PowerUpLevel               null.Int    `db:"power_up_level" json:"power_up_level"`
	PowerUpPoints              null.Int    `db:"power_up_points" json:"power_up_points"`
	PowerUpEndTimestamp        null.Int    `db:"power_up_end_timestamp" json:"power_up_end_timestamp"`
	AlternativeQuestType       null.Int    `db:"alternative_quest_type" json:"alternative_quest_type"`
	AlternativeQuestTimestamp  null.Int    `db:"alternative_quest_timestamp" json:"alternative_quest_timestamp"`
	AlternativeQuestTarget     null.Int    `db:"alternative_quest_target" json:"alternative_quest_target"`
	AlternativeQuestConditions null.String `db:"alternative_quest_conditions" json:"alternative_quest_conditions"`
	AlternativeQuestRewards    null.String `db:"alternative_quest_rewards" json:"alternative_quest_rewards"`
	AlternativeQuestTemplate   null.String `db:"alternative_quest_template" json:"alternative_quest_template"`
	AlternativeQuestTitle      null.String `db:"alternative_quest_title" json:"alternative_quest_title"`
	AlternativeQuestExpiry     null.Int    `db:"alternative_quest_expiry" json:"alternative_quest_expiry"`
	Description                null.String `db:"description" json:"description"`
	ShowcaseFocus              null.String `db:"showcase_focus" json:"showcase_focus"`
	ShowcasePokemon            null.Int    `db:"showcase_pokemon_id" json:"showcase_pokemon_id"`
	ShowcasePokemonForm        null.Int    `db:"showcase_pokemon_form_id" json:"showcase_pokemon_form_id"`
	ShowcasePokemonType        null.Int    `db:"showcase_pokemon_type_id" json:"showcase_pokemon_type_id"`
	ShowcaseRankingStandard    null.Int    `db:"showcase_ranking_standard" json:"showcase_ranking_standard"`
	ShowcaseExpiry             null.Int    `db:"showcase_expiry" json:"showcase_expiry"`
	ShowcaseRankings           null.String `db:"showcase_rankings" json:"showcase_rankings"`

	dirty         bool     `db:"-" json:"-"` // Not persisted - tracks if object needs saving
	newRecord     bool     `db:"-" json:"-"` // Not persisted - tracks if this is a new record
	changedFields []string `db:"-" json:"-"` // Track which fields changed (only when dbDebugEnabled)

	oldValues PokestopOldValues `db:"-" json:"-"` // Old values for webhook comparison
}

// PokestopOldValues holds old field values for webhook comparison (populated when loading from cache/DB)
type PokestopOldValues struct {
	QuestType            null.Int
	AlternativeQuestType null.Int
	LureExpireTimestamp  null.Int
	LureId               int16
	PowerUpEndTimestamp  null.Int
	Name                 null.String
	Url                  null.String
	Description          null.String
	Lat                  float64
	Lon                  float64
}

//`id` varchar(35) NOT NULL,
//`lat` double(18,14) NOT NULL,
//`lon` double(18,14) NOT NULL,
//`name` varchar(128) DEFAULT NULL,
//`url` varchar(200) DEFAULT NULL,
//`lure_expire_timestamp` int unsigned DEFAULT NULL,
//`last_modified_timestamp` int unsigned DEFAULT NULL,
//`updated` int unsigned NOT NULL,
//`enabled` tinyint unsigned DEFAULT NULL,
//`quest_type` int unsigned DEFAULT NULL,
//`quest_timestamp` int unsigned DEFAULT NULL,
//`quest_target` smallint unsigned DEFAULT NULL,
//`quest_conditions` text,
//`quest_rewards` text,
//`quest_template` varchar(100) DEFAULT NULL,
//`quest_title` varchar(100) DEFAULT NULL,
//`quest_reward_type` smallint unsigned GENERATED ALWAYS AS (json_extract(json_extract(`quest_rewards`,_utf8mb4'$[*].type'),_utf8mb4'$[0]')) VIRTUAL,
//`quest_item_id` smallint unsigned GENERATED ALWAYS AS (json_extract(json_extract(`quest_rewards`,_utf8mb4'$[*].info.item_id'),_utf8mb4'$[0]')) VIRTUAL,
//`quest_reward_amount` smallint unsigned GENERATED ALWAYS AS (json_extract(json_extract(`quest_rewards`,_utf8mb4'$[*].info.amount'),_utf8mb4'$[0]')) VIRTUAL,
//`cell_id` bigint unsigned DEFAULT NULL,
//`deleted` tinyint unsigned NOT NULL DEFAULT '0',
//`lure_id` smallint DEFAULT '0',
//`first_seen_timestamp` int unsigned NOT NULL,
//`sponsor_id` smallint unsigned DEFAULT NULL,
//`partner_id` varchar(35) DEFAULT NULL,
//`quest_pokemon_id` smallint unsigned GENERATED ALWAYS AS (json_extract(json_extract(`quest_rewards`,_utf8mb4'$[*].info.pokemon_id'),_utf8mb4'$[0]')) VIRTUAL,
//`ar_scan_eligible` tinyint unsigned DEFAULT NULL,
//`power_up_level` smallint unsigned DEFAULT NULL,
//`power_up_points` int unsigned DEFAULT NULL,
//`power_up_end_timestamp` int unsigned DEFAULT NULL,
//`alternative_quest_type` int unsigned DEFAULT NULL,
//`alternative_quest_timestamp` int unsigned DEFAULT NULL,
//`alternative_quest_target` smallint unsigned DEFAULT NULL,
//`alternative_quest_conditions` text,
//`alternative_quest_rewards` text,
//`alternative_quest_template` varchar(100) DEFAULT NULL,
//`alternative_quest_title` varchar(100) DEFAULT NULL,

// IsDirty returns true if any field has been modified
func (p *Pokestop) IsDirty() bool {
	return p.dirty
}

// ClearDirty resets the dirty flag (call after saving to DB)
func (p *Pokestop) ClearDirty() {
	p.dirty = false
}

// IsNewRecord returns true if this is a new record (not yet in DB)
func (p *Pokestop) IsNewRecord() bool {
	return p.newRecord
}

// snapshotOldValues saves current values for webhook comparison
// Call this after loading from cache/DB but before modifications
func (p *Pokestop) snapshotOldValues() {
	p.oldValues = PokestopOldValues{
		QuestType:            p.QuestType,
		AlternativeQuestType: p.AlternativeQuestType,
		LureExpireTimestamp:  p.LureExpireTimestamp,
		LureId:               p.LureId,
		PowerUpEndTimestamp:  p.PowerUpEndTimestamp,
		Name:                 p.Name,
		Url:                  p.Url,
		Description:          p.Description,
		Lat:                  p.Lat,
		Lon:                  p.Lon,
	}
}

// Lock acquires the Pokestop's mutex
func (p *Pokestop) Lock() {
	p.mu.Lock()
}

// Unlock releases the Pokestop's mutex
func (p *Pokestop) Unlock() {
	p.mu.Unlock()
}

// --- Set methods with dirty tracking ---

func (p *Pokestop) SetId(v string) {
	if p.Id != v {
		p.Id = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "Id")
		}
	}
}

func (p *Pokestop) SetLat(v float64) {
	if !floatAlmostEqual(p.Lat, v, floatTolerance) {
		p.Lat = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "Lat")
		}
	}
}

func (p *Pokestop) SetLon(v float64) {
	if !floatAlmostEqual(p.Lon, v, floatTolerance) {
		p.Lon = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "Lon")
		}
	}
}

func (p *Pokestop) SetName(v null.String) {
	if p.Name != v {
		p.Name = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "Name")
		}
	}
}

func (p *Pokestop) SetUrl(v null.String) {
	if p.Url != v {
		p.Url = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "Url")
		}
	}
}

func (p *Pokestop) SetLureExpireTimestamp(v null.Int) {
	if p.LureExpireTimestamp != v {
		p.LureExpireTimestamp = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "LureExpireTimestamp")
		}
	}
}

func (p *Pokestop) SetLastModifiedTimestamp(v null.Int) {
	if p.LastModifiedTimestamp != v {
		p.LastModifiedTimestamp = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "LastModifiedTimestamp")
		}
	}
}

func (p *Pokestop) SetEnabled(v null.Bool) {
	if p.Enabled != v {
		p.Enabled = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "Enabled")
		}
	}
}

func (p *Pokestop) SetQuestType(v null.Int) {
	if p.QuestType != v {
		p.QuestType = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "QuestType")
		}
	}
}

func (p *Pokestop) SetQuestTimestamp(v null.Int) {
	if p.QuestTimestamp != v {
		p.QuestTimestamp = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "QuestTimestamp")
		}
	}
}

func (p *Pokestop) SetQuestTarget(v null.Int) {
	if p.QuestTarget != v {
		p.QuestTarget = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "QuestTarget")
		}
	}
}

func (p *Pokestop) SetQuestConditions(v null.String) {
	if p.QuestConditions != v {
		p.QuestConditions = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "QuestConditions")
		}
	}
}

func (p *Pokestop) SetQuestRewards(v null.String) {
	if p.QuestRewards != v {
		p.QuestRewards = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "QuestRewards")
		}
	}
}

func (p *Pokestop) SetQuestTemplate(v null.String) {
	if p.QuestTemplate != v {
		p.QuestTemplate = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "QuestTemplate")
		}
	}
}

func (p *Pokestop) SetQuestTitle(v null.String) {
	if p.QuestTitle != v {
		p.QuestTitle = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "QuestTitle")
		}
	}
}

func (p *Pokestop) SetQuestExpiry(v null.Int) {
	if p.QuestExpiry != v {
		p.QuestExpiry = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "QuestExpiry")
		}
	}
}

func (p *Pokestop) SetCellId(v null.Int) {
	if p.CellId != v {
		p.CellId = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "CellId")
		}
	}
}

func (p *Pokestop) SetDeleted(v bool) {
	if p.Deleted != v {
		p.Deleted = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "Deleted")
		}
	}
}

func (p *Pokestop) SetLureId(v int16) {
	if p.LureId != v {
		p.LureId = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "LureId")
		}
	}
}

func (p *Pokestop) SetFirstSeenTimestamp(v int16) {
	if p.FirstSeenTimestamp != v {
		p.FirstSeenTimestamp = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "FirstSeenTimestamp")
		}
	}
}

func (p *Pokestop) SetSponsorId(v null.Int) {
	if p.SponsorId != v {
		p.SponsorId = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "SponsorId")
		}
	}
}

func (p *Pokestop) SetPartnerId(v null.String) {
	if p.PartnerId != v {
		p.PartnerId = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "PartnerId")
		}
	}
}

func (p *Pokestop) SetArScanEligible(v null.Int) {
	if p.ArScanEligible != v {
		p.ArScanEligible = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "ArScanEligible")
		}
	}
}

func (p *Pokestop) SetPowerUpLevel(v null.Int) {
	if p.PowerUpLevel != v {
		p.PowerUpLevel = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "PowerUpLevel")
		}
	}
}

func (p *Pokestop) SetPowerUpPoints(v null.Int) {
	if p.PowerUpPoints != v {
		p.PowerUpPoints = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "PowerUpPoints")
		}
	}
}

func (p *Pokestop) SetPowerUpEndTimestamp(v null.Int) {
	if p.PowerUpEndTimestamp != v {
		p.PowerUpEndTimestamp = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "PowerUpEndTimestamp")
		}
	}
}

func (p *Pokestop) SetAlternativeQuestType(v null.Int) {
	if p.AlternativeQuestType != v {
		p.AlternativeQuestType = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "AlternativeQuestType")
		}
	}
}

func (p *Pokestop) SetAlternativeQuestTimestamp(v null.Int) {
	if p.AlternativeQuestTimestamp != v {
		p.AlternativeQuestTimestamp = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "AlternativeQuestTimestamp")
		}
	}
}

func (p *Pokestop) SetAlternativeQuestTarget(v null.Int) {
	if p.AlternativeQuestTarget != v {
		p.AlternativeQuestTarget = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "AlternativeQuestTarget")
		}
	}
}

func (p *Pokestop) SetAlternativeQuestConditions(v null.String) {
	if p.AlternativeQuestConditions != v {
		p.AlternativeQuestConditions = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "AlternativeQuestConditions")
		}
	}
}

func (p *Pokestop) SetAlternativeQuestRewards(v null.String) {
	if p.AlternativeQuestRewards != v {
		p.AlternativeQuestRewards = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "AlternativeQuestRewards")
		}
	}
}

func (p *Pokestop) SetAlternativeQuestTemplate(v null.String) {
	if p.AlternativeQuestTemplate != v {
		p.AlternativeQuestTemplate = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "AlternativeQuestTemplate")
		}
	}
}

func (p *Pokestop) SetAlternativeQuestTitle(v null.String) {
	if p.AlternativeQuestTitle != v {
		p.AlternativeQuestTitle = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "AlternativeQuestTitle")
		}
	}
}

func (p *Pokestop) SetAlternativeQuestExpiry(v null.Int) {
	if p.AlternativeQuestExpiry != v {
		p.AlternativeQuestExpiry = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "AlternativeQuestExpiry")
		}
	}
}

func (p *Pokestop) SetDescription(v null.String) {
	if p.Description != v {
		p.Description = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "Description")
		}
	}
}

func (p *Pokestop) SetShowcaseFocus(v null.String) {
	if p.ShowcaseFocus != v {
		p.ShowcaseFocus = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "ShowcaseFocus")
		}
	}
}

func (p *Pokestop) SetShowcasePokemon(v null.Int) {
	if p.ShowcasePokemon != v {
		p.ShowcasePokemon = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "ShowcasePokemon")
		}
	}
}

func (p *Pokestop) SetShowcasePokemonForm(v null.Int) {
	if p.ShowcasePokemonForm != v {
		p.ShowcasePokemonForm = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "ShowcasePokemonForm")
		}
	}
}

func (p *Pokestop) SetShowcasePokemonType(v null.Int) {
	if p.ShowcasePokemonType != v {
		p.ShowcasePokemonType = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "ShowcasePokemonType")
		}
	}
}

func (p *Pokestop) SetShowcaseRankingStandard(v null.Int) {
	if p.ShowcaseRankingStandard != v {
		p.ShowcaseRankingStandard = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "ShowcaseRankingStandard")
		}
	}
}

func (p *Pokestop) SetShowcaseExpiry(v null.Int) {
	if p.ShowcaseExpiry != v {
		p.ShowcaseExpiry = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "ShowcaseExpiry")
		}
	}
}

func (p *Pokestop) SetShowcaseRankings(v null.String) {
	if p.ShowcaseRankings != v {
		p.ShowcaseRankings = v
		p.dirty = true
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, "ShowcaseRankings")
		}
	}
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

	// Atomically cache the loaded Pokestop - if another goroutine raced us,
	// we'll get their Pokestop and use that instead (ensuring same mutex)
	pokestop := pokestopCache.GetOrSetFunc(fortId, func() *Pokestop {
		// Only called if key doesn't exist - our Pokestop wins
		if config.Config.TestFortInMemory {
			fortRtreeUpdatePokestopOnGet(&dbPokestop)
		}
		return &dbPokestop
	}, ttlcache.DefaultTTL)

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
	pokestop := pokestopCache.GetOrSetFunc(fortId, func() *Pokestop {
		return &Pokestop{Id: fortId, newRecord: true}
	}, ttlcache.DefaultTTL)

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
			if config.Config.TestFortInMemory {
				fortRtreeUpdatePokestopOnGet(pokestop)
			}
		}
	}

	pokestop.snapshotOldValues()
	return pokestop, func() { pokestop.Unlock() }, nil
}

var LureTime int64 = 1800

func (stop *Pokestop) updatePokestopFromFort(fortData *pogo.PokemonFortProto, cellId uint64, now int64) *Pokestop {
	stop.SetId(fortData.FortId)
	stop.SetLat(fortData.Latitude)
	stop.SetLon(fortData.Longitude)

	stop.SetPartnerId(null.NewString(fortData.PartnerId, fortData.PartnerId != ""))
	stop.SetSponsorId(null.IntFrom(int64(fortData.Sponsor)))
	stop.SetEnabled(null.BoolFrom(fortData.Enabled))
	stop.SetArScanEligible(null.IntFrom(util.BoolToInt[int64](fortData.IsArScanEligible)))
	stop.SetPowerUpPoints(null.IntFrom(int64(fortData.PowerUpProgressPoints)))
	powerUpLevel, powerUpEndTimestamp := calculatePowerUpPoints(fortData)
	stop.SetPowerUpLevel(powerUpLevel)
	stop.SetPowerUpEndTimestamp(powerUpEndTimestamp)

	// lasModifiedMs is also modified when incident happens
	lastModifiedTimestamp := fortData.LastModifiedMs / 1000
	stop.SetLastModifiedTimestamp(null.IntFrom(lastModifiedTimestamp))

	if len(fortData.ActiveFortModifier) > 0 {
		lureId := int16(fortData.ActiveFortModifier[0])
		if lureId >= 501 && lureId <= 510 {
			lureEnd := lastModifiedTimestamp + LureTime
			oldLureEnd := stop.LureExpireTimestamp.ValueOrZero()
			if stop.LureId != lureId {
				stop.SetLureExpireTimestamp(null.IntFrom(lureEnd))
				stop.SetLureId(lureId)
			} else {
				// wait some time after lure end before a restart in case of timing issue
				if now > oldLureEnd+30 {
					for now > lureEnd {
						lureEnd += LureTime
					}
					// lure needs to be restarted
					stop.SetLureExpireTimestamp(null.IntFrom(lureEnd))
				}
			}
		}
	}

	if fortData.ImageUrl != "" {
		stop.SetUrl(null.StringFrom(fortData.ImageUrl))
	}
	stop.SetCellId(null.IntFrom(int64(cellId)))

	if stop.Deleted {
		stop.SetDeleted(false)
		log.Warnf("Cleared Stop with id '%s' is found again in GMO, therefore un-deleted", stop.Id)
		// Restore in fort tracker if enabled
		if fortTracker != nil {
			fortTracker.RestoreFort(stop.Id, cellId, false, time.Now().Unix())
		}
	}
	return stop
}

func (stop *Pokestop) updatePokestopFromQuestProto(questProto *pogo.FortSearchOutProto, haveAr bool) string {

	if questProto.ChallengeQuest == nil {
		log.Debugf("Received blank quest")
		return "Blank quest"
	}
	questData := questProto.ChallengeQuest.Quest
	questTitle := questProto.ChallengeQuest.QuestDisplay.Description
	questType := int64(questData.QuestType)
	questTarget := int64(questData.Goal.Target)
	questTemplate := strings.ToLower(questData.TemplateId)

	conditions := []map[string]any{}
	rewards := []map[string]any{}

	for _, conditionData := range questData.Goal.Condition {
		condition := make(map[string]any)
		infoData := make(map[string]any)
		condition["type"] = int(conditionData.Type)
		switch conditionData.Type {
		case pogo.QuestConditionProto_WITH_BADGE_TYPE:
			info := conditionData.GetWithBadgeType()
			infoData["amount"] = info.Amount
			infoData["badge_rank"] = info.BadgeRank
			badgeTypeById := []int{}
			for _, badge := range info.BadgeType {
				badgeTypeById = append(badgeTypeById, int(badge))
			}
			infoData["badge_types"] = badgeTypeById

		case pogo.QuestConditionProto_WITH_ITEM:
			info := conditionData.GetWithItem()
			if int(info.Item) != 0 {
				infoData["item_id"] = int(info.Item)
			}
		case pogo.QuestConditionProto_WITH_RAID_LEVEL:
			info := conditionData.GetWithRaidLevel()
			raidLevelById := []int{}
			for _, raidLevel := range info.RaidLevel {
				raidLevelById = append(raidLevelById, int(raidLevel))
			}
			infoData["raid_levels"] = raidLevelById
		case pogo.QuestConditionProto_WITH_POKEMON_TYPE:
			info := conditionData.GetWithPokemonType()
			pokemonTypesById := []int{}
			for _, t := range info.PokemonType {
				pokemonTypesById = append(pokemonTypesById, int(t))
			}
			infoData["pokemon_type_ids"] = pokemonTypesById
		case pogo.QuestConditionProto_WITH_POKEMON_CATEGORY:
			info := conditionData.GetWithPokemonCategory()
			if info.CategoryName != "" {
				infoData["category_name"] = info.CategoryName
			}
			pokemonById := []int{}
			for _, pokemon := range info.PokemonIds {
				pokemonById = append(pokemonById, int(pokemon))
			}
			infoData["pokemon_ids"] = pokemonById
		case pogo.QuestConditionProto_WITH_WIN_RAID_STATUS:
		case pogo.QuestConditionProto_WITH_THROW_TYPE:
			info := conditionData.GetWithThrowType()
			if int(info.GetThrowType()) != 0 { // TODO: RDM has ThrowType here, ensure it is the same thing
				infoData["throw_type_id"] = int(info.GetThrowType())
			}
			infoData["hit"] = info.GetHit()
		case pogo.QuestConditionProto_WITH_THROW_TYPE_IN_A_ROW:
			info := conditionData.GetWithThrowType()
			if int(info.GetThrowType()) != 0 {
				infoData["throw_type_id"] = int(info.GetThrowType())
			}
			infoData["hit"] = info.GetHit()
		case pogo.QuestConditionProto_WITH_LOCATION:
			info := conditionData.GetWithLocation()
			infoData["cell_ids"] = info.S2CellId
		case pogo.QuestConditionProto_WITH_DISTANCE:
			info := conditionData.GetWithDistance()
			infoData["distance"] = info.DistanceKm
		case pogo.QuestConditionProto_WITH_POKEMON_ALIGNMENT:
			info := conditionData.GetWithPokemonAlignment()
			alignmentIds := []int{}
			for _, alignment := range info.Alignment {
				alignmentIds = append(alignmentIds, int(alignment))
			}
			infoData["alignment_ids"] = alignmentIds
		case pogo.QuestConditionProto_WITH_INVASION_CHARACTER:
			info := conditionData.GetWithInvasionCharacter()
			characterCategoryIds := []int{}
			for _, characterCategory := range info.Category {
				characterCategoryIds = append(characterCategoryIds, int(characterCategory))
			}
			infoData["character_category_ids"] = characterCategoryIds
		case pogo.QuestConditionProto_WITH_NPC_COMBAT:
			info := conditionData.GetWithNpcCombat()
			infoData["win"] = info.RequiresWin
			infoData["template_ids"] = info.CombatNpcTrainerId
		case pogo.QuestConditionProto_WITH_PLAYER_LEVEL:
			info := conditionData.GetWithPlayerLevel()
			infoData["level"] = info.Level
		case pogo.QuestConditionProto_WITH_BUDDY:
			info := conditionData.GetWithBuddy()
			if info != nil {
				infoData["min_buddy_level"] = int(info.MinBuddyLevel)
				infoData["must_be_on_map"] = info.MustBeOnMap
			} else {
				infoData["min_buddy_level"] = 0
				infoData["must_be_on_map"] = false
			}
		case pogo.QuestConditionProto_WITH_DAILY_BUDDY_AFFECTION:
			info := conditionData.GetWithDailyBuddyAffection()
			infoData["min_buddy_affection_earned_today"] = info.MinBuddyAffectionEarnedToday
		case pogo.QuestConditionProto_WITH_TEMP_EVO_POKEMON:
			info := conditionData.GetWithTempEvoId()
			tempEvoIds := []int{}
			for _, evolution := range info.MegaForm {
				tempEvoIds = append(tempEvoIds, int(evolution))
			}
			infoData["raid_pokemon_evolutions"] = tempEvoIds
		case pogo.QuestConditionProto_WITH_ITEM_TYPE:
			info := conditionData.GetWithItemType()
			itemTypes := []int{}
			for _, itemType := range info.ItemType {
				itemTypes = append(itemTypes, int(itemType))
			}
			infoData["item_type_ids"] = itemTypes
		case pogo.QuestConditionProto_WITH_RAID_ELAPSED_TIME:
			info := conditionData.GetWithElapsedTime()
			infoData["time"] = int64(info.ElapsedTimeMs) / 1000
		case pogo.QuestConditionProto_WITH_WIN_GYM_BATTLE_STATUS:
		case pogo.QuestConditionProto_WITH_SUPER_EFFECTIVE_CHARGE:
		case pogo.QuestConditionProto_WITH_UNIQUE_POKESTOP:
		case pogo.QuestConditionProto_WITH_QUEST_CONTEXT:
		case pogo.QuestConditionProto_WITH_WIN_BATTLE_STATUS:
		case pogo.QuestConditionProto_WITH_CURVE_BALL:
		case pogo.QuestConditionProto_WITH_NEW_FRIEND:
		case pogo.QuestConditionProto_WITH_DAYS_IN_A_ROW:
		case pogo.QuestConditionProto_WITH_WEATHER_BOOST:
		case pogo.QuestConditionProto_WITH_DAILY_CAPTURE_BONUS:
		case pogo.QuestConditionProto_WITH_DAILY_SPIN_BONUS:
		case pogo.QuestConditionProto_WITH_UNIQUE_POKEMON:
		case pogo.QuestConditionProto_WITH_BUDDY_INTERESTING_POI:
		case pogo.QuestConditionProto_WITH_POKEMON_LEVEL:
		case pogo.QuestConditionProto_WITH_SINGLE_DAY:
		case pogo.QuestConditionProto_WITH_UNIQUE_POKEMON_TEAM:
		case pogo.QuestConditionProto_WITH_MAX_CP:
		case pogo.QuestConditionProto_WITH_LUCKY_POKEMON:
		case pogo.QuestConditionProto_WITH_LEGENDARY_POKEMON:
		case pogo.QuestConditionProto_WITH_GBL_RANK:
		case pogo.QuestConditionProto_WITH_CATCHES_IN_A_ROW:
		case pogo.QuestConditionProto_WITH_ENCOUNTER_TYPE:
		case pogo.QuestConditionProto_WITH_COMBAT_TYPE:
		case pogo.QuestConditionProto_WITH_GEOTARGETED_POI:
		case pogo.QuestConditionProto_WITH_FRIEND_LEVEL:
		case pogo.QuestConditionProto_WITH_STICKER:
		case pogo.QuestConditionProto_WITH_POKEMON_CP:
		case pogo.QuestConditionProto_WITH_RAID_LOCATION:
		case pogo.QuestConditionProto_WITH_FRIENDS_RAID:
		case pogo.QuestConditionProto_WITH_POKEMON_COSTUME:
		default:
			break
		}

		if infoData != nil {
			condition["info"] = infoData
		}
		conditions = append(conditions, condition)
	}

	for _, rewardData := range questData.QuestRewards {
		reward := make(map[string]any)
		infoData := make(map[string]any)
		reward["type"] = int(rewardData.Type)
		switch rewardData.Type {
		case pogo.QuestRewardProto_EXPERIENCE:
			infoData["amount"] = rewardData.GetExp()
		case pogo.QuestRewardProto_ITEM:
			info := rewardData.GetItem()
			infoData["amount"] = info.Amount
			infoData["item_id"] = int(info.Item)
		case pogo.QuestRewardProto_STARDUST:
			infoData["amount"] = rewardData.GetStardust()
		case pogo.QuestRewardProto_CANDY:
			info := rewardData.GetCandy()
			infoData["amount"] = info.Amount
			infoData["pokemon_id"] = int(info.PokemonId)
		case pogo.QuestRewardProto_XL_CANDY:
			info := rewardData.GetXlCandy()
			infoData["amount"] = info.Amount
			infoData["pokemon_id"] = int(info.PokemonId)
		case pogo.QuestRewardProto_POKEMON_ENCOUNTER:
			info := rewardData.GetPokemonEncounter()
			if info.IsHiddenDitto {
				infoData["pokemon_id"] = 132
				infoData["pokemon_id_display"] = int(info.GetPokemonId())
			} else {
				infoData["pokemon_id"] = int(info.GetPokemonId())
			}
			if info.ShinyProbability > 0.0 {
				infoData["shiny_probability"] = info.ShinyProbability
			}
			if display := info.PokemonDisplay; display != nil {
				if costumeId := int(display.Costume); costumeId != 0 {
					infoData["costume_id"] = costumeId
				}
				if formId := int(display.Form); formId != 0 {
					infoData["form_id"] = formId
				}
				if genderId := int(display.Gender); genderId != 0 {
					infoData["gender_id"] = genderId
				}
				if display.Shiny {
					infoData["shiny"] = display.Shiny
				}
				if background := util.ExtractBackgroundFromDisplay(display); background != nil {
					infoData["background"] = background
				}
				if breadMode := int(display.BreadModeEnum); breadMode != 0 {
					infoData["bread_mode"] = breadMode
				}
			} else {

			}
		case pogo.QuestRewardProto_POKECOIN:
			infoData["amount"] = rewardData.GetPokecoin()
		case pogo.QuestRewardProto_STICKER:
			info := rewardData.GetSticker()
			infoData["amount"] = info.Amount
			infoData["sticker_id"] = info.StickerId
		case pogo.QuestRewardProto_MEGA_RESOURCE:
			info := rewardData.GetMegaResource()
			infoData["amount"] = info.Amount
			infoData["pokemon_id"] = int(info.PokemonId)
		case pogo.QuestRewardProto_AVATAR_CLOTHING:
		case pogo.QuestRewardProto_QUEST:
		case pogo.QuestRewardProto_LEVEL_CAP:
		case pogo.QuestRewardProto_INCIDENT:
		case pogo.QuestRewardProto_PLAYER_ATTRIBUTE:
		default:
			break

		}
		reward["info"] = infoData
		rewards = append(rewards, reward)
	}

	questConditions, _ := json.Marshal(conditions)
	questRewards, _ := json.Marshal(rewards)
	questTimestamp := time.Now().Unix()

	questExpiry := null.NewInt(0, false)

	stopTimezone := tz.SearchTimezone(stop.Lat, stop.Lon)
	if stopTimezone != "" {
		loc, err := time.LoadLocation(stopTimezone)
		if err != nil {
			log.Warnf("Unrecognised time zone %s at %f,%f", stopTimezone, stop.Lat, stop.Lon)
		} else {
			year, month, day := time.Now().In(loc).Date()
			t := time.Date(year, month, day, 0, 0, 0, 0, loc).AddDate(0, 0, 1)
			unixTime := t.Unix()
			questExpiry = null.IntFrom(unixTime)
		}
	}

	if questExpiry.Valid == false {
		questExpiry = null.IntFrom(time.Now().Unix() + 24*60*60) // Set expiry to 24 hours from now
	}

	if !haveAr {
		stop.SetAlternativeQuestType(null.IntFrom(questType))
		stop.SetAlternativeQuestTarget(null.IntFrom(questTarget))
		stop.SetAlternativeQuestTemplate(null.StringFrom(questTemplate))
		stop.SetAlternativeQuestTitle(null.StringFrom(questTitle))
		stop.SetAlternativeQuestConditions(null.StringFrom(string(questConditions)))
		stop.SetAlternativeQuestRewards(null.StringFrom(string(questRewards)))
		stop.SetAlternativeQuestTimestamp(null.IntFrom(questTimestamp))
		stop.SetAlternativeQuestExpiry(questExpiry)
	} else {
		stop.SetQuestType(null.IntFrom(questType))
		stop.SetQuestTarget(null.IntFrom(questTarget))
		stop.SetQuestTemplate(null.StringFrom(questTemplate))
		stop.SetQuestTitle(null.StringFrom(questTitle))
		stop.SetQuestConditions(null.StringFrom(string(questConditions)))
		stop.SetQuestRewards(null.StringFrom(string(questRewards)))
		stop.SetQuestTimestamp(null.IntFrom(questTimestamp))
		stop.SetQuestExpiry(questExpiry)
	}

	return questTitle
}

func (stop *Pokestop) updatePokestopFromFortDetailsProto(fortData *pogo.FortDetailsOutProto) *Pokestop {
	stop.SetId(fortData.Id)
	stop.SetLat(fortData.Latitude)
	stop.SetLon(fortData.Longitude)
	if len(fortData.ImageUrl) > 0 {
		stop.SetUrl(null.StringFrom(fortData.ImageUrl[0]))
	}
	stop.SetName(null.StringFrom(fortData.Name))

	if fortData.Description == "" {
		stop.SetDescription(null.NewString("", false))
	} else {
		stop.SetDescription(null.StringFrom(fortData.Description))
	}

	if fortData.Modifier != nil && len(fortData.Modifier) > 0 {
		// DeployingPlayerCodename contains the name of the player if we want that
		lureId := int16(fortData.Modifier[0].ModifierType)
		lureExpiry := fortData.Modifier[0].ExpirationTimeMs / 1000

		stop.SetLureId(lureId)
		stop.SetLureExpireTimestamp(null.IntFrom(lureExpiry))
	}

	return stop
}

func (stop *Pokestop) updatePokestopFromGetMapFortsOutProto(fortData *pogo.GetMapFortsOutProto_FortProto) *Pokestop {
	stop.SetId(fortData.Id)
	stop.SetLat(fortData.Latitude)
	stop.SetLon(fortData.Longitude)

	if len(fortData.Image) > 0 {
		stop.SetUrl(null.StringFrom(fortData.Image[0].Url))
	}
	stop.SetName(null.StringFrom(fortData.Name))
	if stop.Deleted {
		log.Debugf("Cleared Stop with id '%s' is found again in GMF, therefore kept deleted", stop.Id)
	}
	return stop
}

func (stop *Pokestop) updatePokestopFromGetContestDataOutProto(contest *pogo.ContestProto) {
	stop.SetShowcaseRankingStandard(null.IntFrom(int64(contest.GetMetric().GetRankingStandard())))
	stop.SetShowcaseExpiry(null.IntFrom(contest.GetSchedule().GetContestCycle().GetEndTimeMs() / 1000))

	focusStore := createFocusStoreFromContestProto(contest)

	if len(focusStore) > 1 {
		log.Warnf("SHOWCASE: we got more than one showcase focus: %v", focusStore)
	}

	for key, focus := range focusStore {
		focus["type"] = key
		jsonBytes, err := json.Marshal(focus)
		if err != nil {
			log.Errorf("SHOWCASE: Stop '%s' - Focus '%v' marshalling failed: %s", stop.Id, focus, err)
		}
		stop.SetShowcaseFocus(null.StringFrom(string(jsonBytes)))
		// still support old format - probably still required to filter in external tools
		stop.extractShowcasePokemonInfoDeprecated(key, focus)
	}
}

func (stop *Pokestop) updatePokestopFromGetPokemonSizeContestEntryOutProto(contestData *pogo.GetPokemonSizeLeaderboardEntryOutProto) {
	type contestEntry struct {
		Rank                  int     `json:"rank"`
		Score                 float64 `json:"score"`
		PokemonId             int     `json:"pokemon_id"`
		Form                  int     `json:"form"`
		Costume               int     `json:"costume"`
		Gender                int     `json:"gender"`
		Shiny                 bool    `json:"shiny"`
		TempEvolution         int     `json:"temp_evolution"`
		TempEvolutionFinishMs int64   `json:"temp_evolution_finish_ms"`
		Alignment             int     `json:"alignment"`
		Badge                 int     `json:"badge"`
		Background            *int64  `json:"background,omitempty"`
	}
	type contestJson struct {
		TotalEntries   int            `json:"total_entries"`
		LastUpdate     int64          `json:"last_update"`
		ContestEntries []contestEntry `json:"contest_entries"`
	}

	j := contestJson{LastUpdate: time.Now().Unix()}
	j.TotalEntries = int(contestData.TotalEntries)

	for _, entry := range contestData.GetContestEntries() {
		rank := entry.GetRank()
		if rank > 3 {
			break
		}
		j.ContestEntries = append(j.ContestEntries, contestEntry{
			Rank:                  int(rank),
			Score:                 entry.GetScore(),
			PokemonId:             int(entry.GetPokedexId()),
			Form:                  int(entry.GetPokemonDisplay().Form),
			Costume:               int(entry.GetPokemonDisplay().Costume),
			Gender:                int(entry.GetPokemonDisplay().Gender),
			Shiny:                 entry.GetPokemonDisplay().Shiny,
			TempEvolution:         int(entry.GetPokemonDisplay().CurrentTempEvolution),
			TempEvolutionFinishMs: entry.GetPokemonDisplay().TemporaryEvolutionFinishMs,
			Alignment:             int(entry.GetPokemonDisplay().Alignment),
			Badge:                 int(entry.GetPokemonDisplay().PokemonBadge),
			Background:            util.ExtractBackgroundFromDisplay(entry.PokemonDisplay),
		})

	}
	jsonString, _ := json.Marshal(j)
	stop.SetShowcaseRankings(null.StringFrom(string(jsonString)))
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
		if pokestop.Updated > now-900 {
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
			log.Errorf("insert pokestop %s: %s", pokestop.Id, err)
			return
		}
		_ = res
	} else {
		// Existing record - UPDATE
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

func UpdatePokestopRecordWithFortDetailsOutProto(ctx context.Context, db db.DbDetails, fort *pogo.FortDetailsOutProto) string {
	pokestop, unlock, err := getOrCreatePokestopRecord(ctx, db, fort.Id)
	if err != nil {
		log.Printf("Update pokestop %s", err)
		return fmt.Sprintf("Error %s", err)
	}
	defer unlock()

	pokestop.updatePokestopFromFortDetailsProto(fort)

	updatePokestopGetMapFortCache(pokestop)
	savePokestopRecord(ctx, db, pokestop)
	return fmt.Sprintf("%s %s", fort.Id, fort.Name)
}

func UpdatePokestopWithQuest(ctx context.Context, db db.DbDetails, quest *pogo.FortSearchOutProto, haveAr bool) string {
	haveArStr := "NoAR"
	if haveAr {
		haveArStr = "AR"
	}

	if quest.ChallengeQuest == nil {
		statsCollector.IncDecodeQuest("error", "no_quest")
		return fmt.Sprintf("%s %s Blank quest", quest.FortId, haveArStr)
	}

	statsCollector.IncDecodeQuest("ok", haveArStr)

	pokestop, unlock, err := getOrCreatePokestopRecord(ctx, db, quest.FortId)
	if err != nil {
		log.Printf("Update quest %s", err)
		return fmt.Sprintf("error %s", err)
	}
	defer unlock()

	questTitle := pokestop.updatePokestopFromQuestProto(quest, haveAr)

	updatePokestopGetMapFortCache(pokestop)
	savePokestopRecord(ctx, db, pokestop)

	areas := MatchStatsGeofence(pokestop.Lat, pokestop.Lon)
	updateQuestStats(pokestop, haveAr, areas)

	return fmt.Sprintf("%s %s %s", quest.FortId, haveArStr, questTitle)
}

func ClearQuestsWithinGeofence(ctx context.Context, dbDetails db.DbDetails, geofence *geojson.Feature) {
	started := time.Now()
	rows, err := db.RemoveQuests(ctx, dbDetails, geofence)
	if err != nil {
		log.Errorf("ClearQuest: Error removing quests: %s", err)
		return
	}
	ClearPokestopCache()
	log.Infof("ClearQuest: Removed quests from %d pokestops in %s", rows, time.Since(started))
}

func GetQuestStatusWithGeofence(dbDetails db.DbDetails, geofence *geojson.Feature) db.QuestStatus {
	res, err := db.GetQuestStatus(dbDetails, geofence)
	if err != nil {
		log.Errorf("QuestStatus: Error retrieving quests: %s", err)
		return db.QuestStatus{}
	}
	return res
}

func UpdatePokestopRecordWithGetMapFortsOutProto(ctx context.Context, db db.DbDetails, mapFort *pogo.GetMapFortsOutProto_FortProto) (bool, string) {
	pokestop, unlock, err := getPokestopRecordForUpdate(ctx, db, mapFort.Id)
	if err != nil {
		log.Printf("Update pokestop %s", err)
		return false, fmt.Sprintf("Error %s", err)
	}

	if pokestop == nil {
		return false, ""
	}
	defer unlock()

	pokestop.updatePokestopFromGetMapFortsOutProto(mapFort)
	savePokestopRecord(ctx, db, pokestop)
	return true, fmt.Sprintf("%s %s", mapFort.Id, mapFort.Name)
}

func GetPokestopPositions(details db.DbDetails, geofence *geojson.Feature) ([]db.QuestLocation, error) {
	return db.GetPokestopPositions(details, geofence)
}

func UpdatePokestopWithContestData(ctx context.Context, db db.DbDetails, request *pogo.GetContestDataProto, contestData *pogo.GetContestDataOutProto) string {
	if contestData.ContestIncident == nil || len(contestData.ContestIncident.Contests) == 0 {
		return "No contests found"
	}

	var fortId string
	if request != nil {
		fortId = request.FortId
	} else {
		fortId = getFortIdFromContest(contestData.ContestIncident.Contests[0].ContestId)
	}

	if fortId == "" {
		return "No fortId found"
	}

	if len(contestData.ContestIncident.Contests) > 1 {
		log.Errorf("More than one contest found")
		return fmt.Sprintf("More than one contest found in %s", fortId)
	}

	contest := contestData.ContestIncident.Contests[0]

	pokestop, unlock, err := getPokestopRecordForUpdate(ctx, db, fortId)
	if err != nil {
		log.Printf("Get pokestop %s", err)
		return "Error getting pokestop"
	}

	if pokestop == nil {
		log.Infof("Contest data for pokestop %s not found", fortId)
		return fmt.Sprintf("Contest data for pokestop %s not found", fortId)
	}
	defer unlock()

	pokestop.updatePokestopFromGetContestDataOutProto(contest)
	savePokestopRecord(ctx, db, pokestop)

	return fmt.Sprintf("Contest %s", fortId)
}

func getFortIdFromContest(id string) string {
	return strings.Split(id, "-")[0]
}

func UpdatePokestopWithPokemonSizeContestEntry(ctx context.Context, db db.DbDetails, request *pogo.GetPokemonSizeLeaderboardEntryProto, contestData *pogo.GetPokemonSizeLeaderboardEntryOutProto) string {
	fortId := getFortIdFromContest(request.GetContestId())

	pokestop, unlock, err := getPokestopRecordForUpdate(ctx, db, fortId)
	if err != nil {
		log.Printf("Get pokestop %s", err)
		return "Error getting pokestop"
	}

	if pokestop == nil {
		log.Infof("Contest data for pokestop %s not found", fortId)
		return fmt.Sprintf("Contest data for pokestop %s not found", fortId)
	}
	defer unlock()

	pokestop.updatePokestopFromGetPokemonSizeContestEntryOutProto(contestData)
	savePokestopRecord(ctx, db, pokestop)

	return fmt.Sprintf("Contest Detail %s", fortId)
}
