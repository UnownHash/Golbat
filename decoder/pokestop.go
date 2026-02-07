package decoder

import (
	"fmt"
	"sync"

	"github.com/guregu/null/v6"
)

// PokestopData contains all database-persisted fields for Pokestop.
// This struct is embedded in Pokestop and can be safely copied for write-behind queueing.
type PokestopData struct {
	Id                            string      `db:"id"`
	Lat                           float64     `db:"lat"`
	Lon                           float64     `db:"lon"`
	Name                          null.String `db:"name"`
	Url                           null.String `db:"url"`
	LureExpireTimestamp           null.Int    `db:"lure_expire_timestamp"`
	LastModifiedTimestamp         null.Int    `db:"last_modified_timestamp"`
	Updated                       int64       `db:"updated"`
	Enabled                       null.Bool   `db:"enabled"`
	QuestType                     null.Int    `db:"quest_type"`
	QuestTimestamp                null.Int    `db:"quest_timestamp"`
	QuestTarget                   null.Int    `db:"quest_target"`
	QuestConditions               null.String `db:"quest_conditions"`
	QuestRewards                  null.String `db:"quest_rewards"`
	QuestTemplate                 null.String `db:"quest_template"`
	QuestTitle                    null.String `db:"quest_title"`
	QuestExpiry                   null.Int    `db:"quest_expiry"`
	QuestRewardType               null.Int    `db:"quest_reward_type"`
	QuestItemId                   null.Int    `db:"quest_item_id"`
	QuestRewardAmount             null.Int    `db:"quest_reward_amount"`
	QuestPokemonId                null.Int    `db:"quest_pokemon_id"`
	QuestPokemonFormId            null.Int    `db:"quest_pokemon_form_id"`
	CellId                        null.Int    `db:"cell_id"`
	Deleted                       bool        `db:"deleted"`
	LureId                        int16       `db:"lure_id"`
	FirstSeenTimestamp            int16       `db:"first_seen_timestamp"`
	SponsorId                     null.Int    `db:"sponsor_id"`
	PartnerId                     null.String `db:"partner_id"`
	ArScanEligible                null.Int    `db:"ar_scan_eligible"` // is an 8
	PowerUpLevel                  null.Int    `db:"power_up_level"`
	PowerUpPoints                 null.Int    `db:"power_up_points"`
	PowerUpEndTimestamp           null.Int    `db:"power_up_end_timestamp"`
	AlternativeQuestType          null.Int    `db:"alternative_quest_type"`
	AlternativeQuestTimestamp     null.Int    `db:"alternative_quest_timestamp"`
	AlternativeQuestTarget        null.Int    `db:"alternative_quest_target"`
	AlternativeQuestConditions    null.String `db:"alternative_quest_conditions"`
	AlternativeQuestRewards       null.String `db:"alternative_quest_rewards"`
	AlternativeQuestTemplate      null.String `db:"alternative_quest_template"`
	AlternativeQuestTitle         null.String `db:"alternative_quest_title"`
	AlternativeQuestExpiry        null.Int    `db:"alternative_quest_expiry"`
	AlternativeQuestRewardType    null.Int    `db:"alternative_quest_reward_type"`
	AlternativeQuestItemId        null.Int    `db:"alternative_quest_item_id"`
	AlternativeQuestRewardAmount  null.Int    `db:"alternative_quest_reward_amount"`
	AlternativeQuestPokemonId     null.Int    `db:"alternative_quest_pokemon_id"`
	AlternativeQuestPokemonFormId null.Int    `db:"alternative_quest_pokemon_form_id"`
	Description                   null.String `db:"description"`
	ShowcaseFocus                 null.String `db:"showcase_focus"`
	ShowcasePokemon               null.Int    `db:"showcase_pokemon_id"`
	ShowcasePokemonForm           null.Int    `db:"showcase_pokemon_form_id"`
	ShowcasePokemonType           null.Int    `db:"showcase_pokemon_type_id"`
	ShowcaseRankingStandard       null.Int    `db:"showcase_ranking_standard"`
	ShowcaseExpiry                null.Int    `db:"showcase_expiry"`
	ShowcaseRankings              null.String `db:"showcase_rankings"`
}

// Pokestop struct.
type Pokestop struct {
	mu sync.Mutex `db:"-"` // Object-level mutex

	PokestopData // Embedded data fields - can be copied for write-behind queue

	// Memory-only fields (not persisted to DB)
	QuestSeed            null.Int `db:"-"` // Quest seed for AR quest (memory only, sent in webhook)
	AlternativeQuestSeed null.Int `db:"-"` // Quest seed for non-AR quest (memory only, sent in webhook)

	dirty         bool     `db:"-"` // Not persisted - tracks if object needs saving
	internalDirty bool     `db:"-"` // Not persisted - tracks if object needs saving (in memory only)
	newRecord     bool     `db:"-"` // Not persisted - tracks if this is a new record
	changedFields []string `db:"-"` // Track which fields changed (only when dbDebugEnabled)

	oldValues PokestopOldValues `db:"-"` // Old values for webhook comparison
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

// IsInternalDirty returns true if any field has been modified for in-memory
func (p *Pokestop) IsInternalDirty() bool {
	return p.internalDirty
}

// ClearDirty resets the dirty flag (call after saving to DB)
func (p *Pokestop) ClearDirty() {
	p.dirty = false
	p.internalDirty = false
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
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("Id:%s->%s", p.Id, v))
		}
		p.Id = v
		p.dirty = true
	}
}

func (p *Pokestop) SetLat(v float64) {
	if !floatAlmostEqual(p.Lat, v, floatTolerance) {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("Lat:%f->%f", p.Lat, v))
		}
		p.Lat = v
		p.dirty = true
	}
}

func (p *Pokestop) SetLon(v float64) {
	if !floatAlmostEqual(p.Lon, v, floatTolerance) {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("Lon:%f->%f", p.Lon, v))
		}
		p.Lon = v
		p.dirty = true
	}
}

func (p *Pokestop) SetName(v null.String) {
	if p.Name != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("Name:%s->%s", FormatNull(p.Name), FormatNull(v)))
		}
		p.Name = v
		p.dirty = true
	}
}

func (p *Pokestop) SetUrl(v null.String) {
	if p.Url != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("Url:%s->%s", FormatNull(p.Url), FormatNull(v)))
		}
		p.Url = v
		p.dirty = true
	}
}

func (p *Pokestop) SetLureExpireTimestamp(v null.Int) {
	if p.LureExpireTimestamp != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("LureExpireTimestamp:%s->%s", FormatNull(p.LureExpireTimestamp), FormatNull(v)))
		}
		p.LureExpireTimestamp = v
		p.dirty = true
	}
}

func (p *Pokestop) SetLastModifiedTimestamp(v null.Int) {
	if p.LastModifiedTimestamp != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("LastModifiedTimestamp:%s->%s", FormatNull(p.LastModifiedTimestamp), FormatNull(v)))
		}
		p.LastModifiedTimestamp = v
		p.dirty = true
	}
}

func (p *Pokestop) SetEnabled(v null.Bool) {
	if p.Enabled != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("Enabled:%s->%s", FormatNull(p.Enabled), FormatNull(v)))
		}
		p.Enabled = v
		p.dirty = true
	}
}

func (p *Pokestop) SetQuestType(v null.Int) {
	if p.QuestType != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("QuestType:%s->%s", FormatNull(p.QuestType), FormatNull(v)))
		}
		p.QuestType = v
		p.dirty = true
	}
}

func (p *Pokestop) SetQuestTimestamp(v null.Int) {
	if p.QuestTimestamp != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("QuestTimestamp:%s->%s", FormatNull(p.QuestTimestamp), FormatNull(v)))
		}
		p.QuestTimestamp = v
		p.dirty = true
	}
}

func (p *Pokestop) SetQuestTarget(v null.Int) {
	if p.QuestTarget != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("QuestTarget:%s->%s", FormatNull(p.QuestTarget), FormatNull(v)))
		}
		p.QuestTarget = v
		p.dirty = true
	}
}

func (p *Pokestop) SetQuestConditions(v null.String) {
	if p.QuestConditions != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("QuestConditions:%s->%s", FormatNull(p.QuestConditions), FormatNull(v)))
		}
		p.QuestConditions = v
		p.dirty = true
	}
}

func (p *Pokestop) SetQuestRewards(v null.String) {
	if p.QuestRewards != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("QuestRewards:%s->%s", FormatNull(p.QuestRewards), FormatNull(v)))
		}
		p.QuestRewards = v
		p.dirty = true
	}
}

func (p *Pokestop) SetQuestTemplate(v null.String) {
	if p.QuestTemplate != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("QuestTemplate:%s->%s", FormatNull(p.QuestTemplate), FormatNull(v)))
		}
		p.QuestTemplate = v
		p.dirty = true
	}
}

func (p *Pokestop) SetQuestTitle(v null.String) {
	if p.QuestTitle != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("QuestTitle:%s->%s", FormatNull(p.QuestTitle), FormatNull(v)))
		}
		p.QuestTitle = v
		p.dirty = true
	}
}

func (p *Pokestop) SetQuestExpiry(v null.Int) {
	if p.QuestExpiry != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("QuestExpiry:%s->%s", FormatNull(p.QuestExpiry), FormatNull(v)))
		}
		p.QuestExpiry = v
		p.dirty = true
	}
}

func (p *Pokestop) SetQuestRewardType(v null.Int) {
	if p.QuestRewardType != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("QuestRewardType:%s->%s", FormatNull(p.QuestRewardType), FormatNull(v)))
		}
		p.QuestRewardType = v
		p.dirty = true
	}
}

func (p *Pokestop) SetQuestItemId(v null.Int) {
	if p.QuestItemId != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("QuestItemId:%s->%s", FormatNull(p.QuestItemId), FormatNull(v)))
		}
		p.QuestItemId = v
		p.dirty = true
	}
}

func (p *Pokestop) SetQuestRewardAmount(v null.Int) {
	if p.QuestRewardAmount != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("QuestRewardAmount:%s->%s", FormatNull(p.QuestRewardAmount), FormatNull(v)))
		}
		p.QuestRewardAmount = v
		p.dirty = true
	}
}

func (p *Pokestop) SetQuestPokemonId(v null.Int) {
	if p.QuestPokemonId != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("QuestPokemonId:%s->%s", FormatNull(p.QuestPokemonId), FormatNull(v)))
		}
		p.QuestPokemonId = v
		p.dirty = true
	}
}

func (p *Pokestop) SetQuestPokemonFormId(v null.Int) {
	if p.QuestPokemonFormId != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("QuestPokemonFormId:%s->%s", FormatNull(p.QuestPokemonFormId), FormatNull(v)))
		}
		p.QuestPokemonFormId = v
		p.dirty = true
	}
}

func (p *Pokestop) SetCellId(v null.Int) {
	if p.CellId != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("CellId:%s->%s", FormatNull(p.CellId), FormatNull(v)))
		}
		p.CellId = v
		p.dirty = true
	}
}

func (p *Pokestop) SetDeleted(v bool) {
	if p.Deleted != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("Deleted:%t->%t", p.Deleted, v))
		}
		p.Deleted = v
		p.dirty = true
	}
}

func (p *Pokestop) SetLureId(v int16) {
	if p.LureId != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("LureId:%d->%d", p.LureId, v))
		}
		p.LureId = v
		p.dirty = true
	}
}

func (p *Pokestop) SetFirstSeenTimestamp(v int16) {
	if p.FirstSeenTimestamp != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("FirstSeenTimestamp:%d->%d", p.FirstSeenTimestamp, v))
		}
		p.FirstSeenTimestamp = v
		p.dirty = true
	}
}

func (p *Pokestop) SetSponsorId(v null.Int) {
	if p.SponsorId != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("SponsorId:%s->%s", FormatNull(p.SponsorId), FormatNull(v)))
		}
		p.SponsorId = v
		p.dirty = true
	}
}

func (p *Pokestop) SetPartnerId(v null.String) {
	if p.PartnerId != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("PartnerId:%s->%s", FormatNull(p.PartnerId), FormatNull(v)))
		}
		p.PartnerId = v
		p.dirty = true
	}
}

func (p *Pokestop) SetArScanEligible(v null.Int) {
	if p.ArScanEligible != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("ArScanEligible:%s->%s", FormatNull(p.ArScanEligible), FormatNull(v)))
		}
		p.ArScanEligible = v
		p.dirty = true
	}
}

func (p *Pokestop) SetPowerUpLevel(v null.Int) {
	if p.PowerUpLevel != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("PowerUpLevel:%s->%s", FormatNull(p.PowerUpLevel), FormatNull(v)))
		}
		p.PowerUpLevel = v
		p.dirty = true
	}
}

func (p *Pokestop) SetPowerUpPoints(v null.Int) {
	if p.PowerUpPoints != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("PowerUpPoints:%s->%s", FormatNull(p.PowerUpPoints), FormatNull(v)))
		}
		p.PowerUpPoints = v
		p.dirty = true
	}
}

func (p *Pokestop) SetPowerUpEndTimestamp(v null.Int) {
	if p.PowerUpEndTimestamp != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("PowerUpEndTimestamp:%s->%s", FormatNull(p.PowerUpEndTimestamp), FormatNull(v)))
		}
		p.PowerUpEndTimestamp = v
		p.dirty = true
	}
}

func (p *Pokestop) SetAlternativeQuestType(v null.Int) {
	if p.AlternativeQuestType != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("AlternativeQuestType:%s->%s", FormatNull(p.AlternativeQuestType), FormatNull(v)))
		}
		p.AlternativeQuestType = v
		p.dirty = true
	}
}

func (p *Pokestop) SetAlternativeQuestTimestamp(v null.Int) {
	if p.AlternativeQuestTimestamp != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("AlternativeQuestTimestamp:%s->%s", FormatNull(p.AlternativeQuestTimestamp), FormatNull(v)))
		}
		p.AlternativeQuestTimestamp = v
		p.dirty = true
	}
}

func (p *Pokestop) SetAlternativeQuestTarget(v null.Int) {
	if p.AlternativeQuestTarget != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("AlternativeQuestTarget:%s->%s", FormatNull(p.AlternativeQuestTarget), FormatNull(v)))
		}
		p.AlternativeQuestTarget = v
		p.dirty = true
	}
}

func (p *Pokestop) SetAlternativeQuestConditions(v null.String) {
	if p.AlternativeQuestConditions != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("AlternativeQuestConditions:%s->%s", FormatNull(p.AlternativeQuestConditions), FormatNull(v)))
		}
		p.AlternativeQuestConditions = v
		p.dirty = true
	}
}

func (p *Pokestop) SetAlternativeQuestRewards(v null.String) {
	if p.AlternativeQuestRewards != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("AlternativeQuestRewards:%s->%s", FormatNull(p.AlternativeQuestRewards), FormatNull(v)))
		}
		p.AlternativeQuestRewards = v
		p.dirty = true
	}
}

func (p *Pokestop) SetAlternativeQuestTemplate(v null.String) {
	if p.AlternativeQuestTemplate != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("AlternativeQuestTemplate:%s->%s", FormatNull(p.AlternativeQuestTemplate), FormatNull(v)))
		}
		p.AlternativeQuestTemplate = v
		p.dirty = true
	}
}

func (p *Pokestop) SetAlternativeQuestTitle(v null.String) {
	if p.AlternativeQuestTitle != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("AlternativeQuestTitle:%s->%s", FormatNull(p.AlternativeQuestTitle), FormatNull(v)))
		}
		p.AlternativeQuestTitle = v
		p.dirty = true
	}
}

func (p *Pokestop) SetAlternativeQuestExpiry(v null.Int) {
	if p.AlternativeQuestExpiry != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("AlternativeQuestExpiry:%s->%s", FormatNull(p.AlternativeQuestExpiry), FormatNull(v)))
		}
		p.AlternativeQuestExpiry = v
		p.dirty = true
	}
}

func (p *Pokestop) SetAlternativeQuestRewardType(v null.Int) {
	if p.AlternativeQuestRewardType != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("AlternativeQuestRewardType:%s->%s", FormatNull(p.AlternativeQuestRewardType), FormatNull(v)))
		}
		p.AlternativeQuestRewardType = v
		p.dirty = true
	}
}

func (p *Pokestop) SetAlternativeQuestItemId(v null.Int) {
	if p.AlternativeQuestItemId != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("AlternativeQuestItemId:%s->%s", FormatNull(p.AlternativeQuestItemId), FormatNull(v)))
		}
		p.AlternativeQuestItemId = v
		p.dirty = true
	}
}

func (p *Pokestop) SetAlternativeQuestRewardAmount(v null.Int) {
	if p.AlternativeQuestRewardAmount != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("AlternativeQuestRewardAmount:%s->%s", FormatNull(p.AlternativeQuestRewardAmount), FormatNull(v)))
		}
		p.AlternativeQuestRewardAmount = v
		p.dirty = true
	}
}

func (p *Pokestop) SetAlternativeQuestPokemonId(v null.Int) {
	if p.AlternativeQuestPokemonId != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("AlternativeQuestPokemonId:%s->%s", FormatNull(p.AlternativeQuestPokemonId), FormatNull(v)))
		}
		p.AlternativeQuestPokemonId = v
		p.dirty = true
	}
}

func (p *Pokestop) SetAlternativeQuestPokemonFormId(v null.Int) {
	if p.AlternativeQuestPokemonFormId != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("AlternativeQuestPokemonFormId:%s->%s", FormatNull(p.AlternativeQuestPokemonFormId), FormatNull(v)))
		}
		p.AlternativeQuestPokemonFormId = v
		p.dirty = true
	}
}

func (p *Pokestop) SetDescription(v null.String) {
	if p.Description != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("Description:%s->%s", FormatNull(p.Description), FormatNull(v)))
		}
		p.Description = v
		p.dirty = true
	}
}

func (p *Pokestop) SetShowcaseFocus(v null.String) {
	if p.ShowcaseFocus != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("ShowcaseFocus:%s->%s", FormatNull(p.ShowcaseFocus), FormatNull(v)))
		}
		p.ShowcaseFocus = v
		p.dirty = true
	}
}

func (p *Pokestop) SetShowcasePokemon(v null.Int) {
	if p.ShowcasePokemon != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("ShowcasePokemon:%s->%s", FormatNull(p.ShowcasePokemon), FormatNull(v)))
		}
		p.ShowcasePokemon = v
		p.dirty = true
	}
}

func (p *Pokestop) SetShowcasePokemonForm(v null.Int) {
	if p.ShowcasePokemonForm != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("ShowcasePokemonForm:%s->%s", FormatNull(p.ShowcasePokemonForm), FormatNull(v)))
		}
		p.ShowcasePokemonForm = v
		p.dirty = true
	}
}

func (p *Pokestop) SetShowcasePokemonType(v null.Int) {
	if p.ShowcasePokemonType != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("ShowcasePokemonType:%s->%s", FormatNull(p.ShowcasePokemonType), FormatNull(v)))
		}
		p.ShowcasePokemonType = v
		p.dirty = true
	}
}

func (p *Pokestop) SetShowcaseRankingStandard(v null.Int) {
	if p.ShowcaseRankingStandard != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("ShowcaseRankingStandard:%s->%s", FormatNull(p.ShowcaseRankingStandard), FormatNull(v)))
		}
		p.ShowcaseRankingStandard = v
		p.dirty = true
	}
}

func (p *Pokestop) SetShowcaseExpiry(v null.Int) {
	if p.ShowcaseExpiry != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("ShowcaseExpiry:%s->%s", FormatNull(p.ShowcaseExpiry), FormatNull(v)))
		}
		p.ShowcaseExpiry = v
		p.dirty = true
	}
}

func (p *Pokestop) SetShowcaseRankings(v null.String) {
	if p.ShowcaseRankings != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("ShowcaseRankings:%s->%s", FormatNull(p.ShowcaseRankings), FormatNull(v)))
		}
		p.ShowcaseRankings = v
		p.dirty = true
	}
}

func (p *Pokestop) SetUpdated(v int64) {
	if p.Updated != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("Updated:%d->%d", p.Updated, v))
		}
		p.Updated = v
		p.dirty = true
	}
}

// SetQuestSeed sets the quest seed (memory only, not saved to DB)
func (p *Pokestop) SetQuestSeed(v null.Int) {
	if p.QuestSeed != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("QuestSeed:%s->%s", FormatNull(p.QuestSeed), FormatNull(v)))
		}
		p.QuestSeed = v
		// Do not set dirty, as this doesn't trigger a DB update
		p.internalDirty = true
	}
}

// SetAlternativeQuestSeed sets the alternative quest seed (memory only, not saved to DB)
func (p *Pokestop) SetAlternativeQuestSeed(v null.Int) {
	if p.AlternativeQuestSeed != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("AlternativeQuestSeed:%s->%s", FormatNull(p.AlternativeQuestSeed), FormatNull(v)))
		}
		p.AlternativeQuestSeed = v
		// Do not set dirty, as this doesn't trigger a DB update
		p.internalDirty = true
	}
}
