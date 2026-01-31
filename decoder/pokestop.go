package decoder

import (
	"sync"

	"github.com/guregu/null/v6"
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
