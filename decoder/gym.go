package decoder

import (
	"sync"

	"github.com/guregu/null/v6"
)

// Gym struct.
// REMINDER! Keep hasChangesGym updated after making changes
type Gym struct {
	mu sync.Mutex `db:"-" json:"-"` // Object-level mutex

	Id                     string      `db:"id" json:"id"`
	Lat                    float64     `db:"lat" json:"lat"`
	Lon                    float64     `db:"lon" json:"lon"`
	Name                   null.String `db:"name" json:"name"`
	Url                    null.String `db:"url" json:"url"`
	LastModifiedTimestamp  null.Int    `db:"last_modified_timestamp" json:"last_modified_timestamp"`
	RaidEndTimestamp       null.Int    `db:"raid_end_timestamp" json:"raid_end_timestamp"`
	RaidSpawnTimestamp     null.Int    `db:"raid_spawn_timestamp" json:"raid_spawn_timestamp"`
	RaidBattleTimestamp    null.Int    `db:"raid_battle_timestamp" json:"raid_battle_timestamp"`
	Updated                int64       `db:"updated" json:"updated"`
	RaidPokemonId          null.Int    `db:"raid_pokemon_id" json:"raid_pokemon_id"`
	GuardingPokemonId      null.Int    `db:"guarding_pokemon_id" json:"guarding_pokemon_id"`
	GuardingPokemonDisplay null.String `db:"guarding_pokemon_display" json:"guarding_pokemon_display"`
	AvailableSlots         null.Int    `db:"available_slots" json:"available_slots"`
	TeamId                 null.Int    `db:"team_id" json:"team_id"`
	RaidLevel              null.Int    `db:"raid_level" json:"raid_level"`
	Enabled                null.Int    `db:"enabled" json:"enabled"`
	ExRaidEligible         null.Int    `db:"ex_raid_eligible" json:"ex_raid_eligible"`
	InBattle               null.Int    `db:"in_battle" json:"in_battle"`
	RaidPokemonMove1       null.Int    `db:"raid_pokemon_move_1" json:"raid_pokemon_move_1"`
	RaidPokemonMove2       null.Int    `db:"raid_pokemon_move_2" json:"raid_pokemon_move_2"`
	RaidPokemonForm        null.Int    `db:"raid_pokemon_form" json:"raid_pokemon_form"`
	RaidPokemonAlignment   null.Int    `db:"raid_pokemon_alignment" json:"raid_pokemon_alignment"`
	RaidPokemonCp          null.Int    `db:"raid_pokemon_cp" json:"raid_pokemon_cp"`
	RaidIsExclusive        null.Int    `db:"raid_is_exclusive" json:"raid_is_exclusive"`
	CellId                 null.Int    `db:"cell_id" json:"cell_id"`
	Deleted                bool        `db:"deleted" json:"deleted"`
	TotalCp                null.Int    `db:"total_cp" json:"total_cp"`
	FirstSeenTimestamp     int64       `db:"first_seen_timestamp" json:"first_seen_timestamp"`
	RaidPokemonGender      null.Int    `db:"raid_pokemon_gender" json:"raid_pokemon_gender"`
	SponsorId              null.Int    `db:"sponsor_id" json:"sponsor_id"`
	PartnerId              null.String `db:"partner_id" json:"partner_id"`
	RaidPokemonCostume     null.Int    `db:"raid_pokemon_costume" json:"raid_pokemon_costume"`
	RaidPokemonEvolution   null.Int    `db:"raid_pokemon_evolution" json:"raid_pokemon_evolution"`
	ArScanEligible         null.Int    `db:"ar_scan_eligible" json:"ar_scan_eligible"`
	PowerUpLevel           null.Int    `db:"power_up_level" json:"power_up_level"`
	PowerUpPoints          null.Int    `db:"power_up_points" json:"power_up_points"`
	PowerUpEndTimestamp    null.Int    `db:"power_up_end_timestamp" json:"power_up_end_timestamp"`
	Description            null.String `db:"description" json:"description"`
	Defenders              null.String `db:"defenders" json:"defenders"`
	Rsvps                  null.String `db:"rsvps" json:"rsvps"`

	dirty         bool     `db:"-" json:"-"` // Not persisted - tracks if object needs saving (to db)
	internalDirty bool     `db:"-" json:"-"` // Not persisted - tracks if object needs saving (in memory only)
	newRecord     bool     `db:"-" json:"-"` // Not persisted - tracks if this is a new record
	changedFields []string `db:"-" json:"-"` // Track which fields changed (only when dbDebugEnabled)

	oldValues GymOldValues `db:"-" json:"-"` // Old values for webhook comparison
}

// GymOldValues holds old field values for webhook comparison (populated when loading from cache/DB)
type GymOldValues struct {
	Name               null.String
	Url                null.String
	Description        null.String
	Lat                float64
	Lon                float64
	TeamId             null.Int
	AvailableSlots     null.Int
	RaidLevel          null.Int
	RaidPokemonId      null.Int
	RaidSpawnTimestamp null.Int
	Rsvps              null.String
	InBattle           null.Int
}

//`id` varchar(35) NOT NULL,
//`lat` double(18,14) NOT NULL,
//`lon` double(18,14) NOT NULL,
//`name` varchar(128) DEFAULT NULL,
//`url` varchar(200) DEFAULT NULL,
//`last_modified_timestamp` int unsigned DEFAULT NULL,
//`raid_end_timestamp` int unsigned DEFAULT NULL,
//`raid_spawn_timestamp` int unsigned DEFAULT NULL,
//`raid_battle_timestamp` int unsigned DEFAULT NULL,
//`updated` int unsigned NOT NULL,
//`raid_pokemon_id` smallint unsigned DEFAULT NULL,
//`guarding_pokemon_id` smallint unsigned DEFAULT NULL,
//`available_slots` smallint unsigned DEFAULT NULL,
//`availble_slots` smallint unsigned GENERATED ALWAYS AS (`available_slots`) VIRTUAL,
//`team_id` tinyint unsigned DEFAULT NULL,
//`raid_level` tinyint unsigned DEFAULT NULL,
//`enabled` tinyint unsigned DEFAULT NULL,
//`ex_raid_eligible` tinyint unsigned DEFAULT NULL,
//`in_battle` tinyint unsigned DEFAULT NULL,
//`raid_pokemon_move_1` smallint unsigned DEFAULT NULL,
//`raid_pokemon_move_2` smallint unsigned DEFAULT NULL,
//`raid_pokemon_form` smallint unsigned DEFAULT NULL,
//`raid_pokemon_cp` int unsigned DEFAULT NULL,
//`raid_is_exclusive` tinyint unsigned DEFAULT NULL,
//`cell_id` bigint unsigned DEFAULT NULL,
//`deleted` tinyint unsigned NOT NULL DEFAULT '0',
//`total_cp` int unsigned DEFAULT NULL,
//`first_seen_timestamp` int unsigned NOT NULL,
//`raid_pokemon_gender` tinyint unsigned DEFAULT NULL,
//`sponsor_id` smallint unsigned DEFAULT NULL,
//`partner_id` varchar(35) DEFAULT NULL,
//`raid_pokemon_costume` smallint unsigned DEFAULT NULL,
//`raid_pokemon_evolution` tinyint unsigned DEFAULT NULL,
//`ar_scan_eligible` tinyint unsigned DEFAULT NULL,
//`power_up_level` smallint unsigned DEFAULT NULL,
//`power_up_points` int unsigned DEFAULT NULL,
//`power_up_end_timestamp` int unsigned DEFAULT NULL,

//
//SELECT CONCAT("'", GROUP_CONCAT(column_name ORDER BY ordinal_position SEPARATOR "', '"), "'") AS columns
//FROM information_schema.columns
//WHERE table_schema = 'db_name' AND table_name = 'tbl_name'
//
//SELECT CONCAT("'", GROUP_CONCAT(column_name ORDER BY ordinal_position SEPARATOR "', '"), " = ", "'") AS columns
//FROM information_schema.columns
//WHERE table_schema = 'db_name' AND table_name = 'tbl_name'

// IsDirty returns true if any field has been modified
func (gym *Gym) IsDirty() bool {
	return gym.dirty
}

// IsInternalDirty returns true if any field has been modified for in-memory
func (gym *Gym) IsInternalDirty() bool {
	return gym.internalDirty
}

// ClearDirty resets the dirty flag (call after saving to DB)
func (gym *Gym) ClearDirty() {
	gym.dirty = false
	gym.internalDirty = false
}

// IsNewRecord returns true if this is a new record (not yet in DB)
func (gym *Gym) IsNewRecord() bool {
	return gym.newRecord
}

// snapshotOldValues saves current values for webhook comparison
// Call this after loading from cache/DB but before modifications
func (gym *Gym) snapshotOldValues() {
	gym.oldValues = GymOldValues{
		Name:               gym.Name,
		Url:                gym.Url,
		Description:        gym.Description,
		Lat:                gym.Lat,
		Lon:                gym.Lon,
		TeamId:             gym.TeamId,
		AvailableSlots:     gym.AvailableSlots,
		RaidLevel:          gym.RaidLevel,
		RaidPokemonId:      gym.RaidPokemonId,
		RaidSpawnTimestamp: gym.RaidSpawnTimestamp,
		Rsvps:              gym.Rsvps,
		InBattle:           gym.InBattle,
	}
}

// Lock acquires the Gym's mutex
func (gym *Gym) Lock() {
	gym.mu.Lock()
}

// Unlock releases the Gym's mutex
func (gym *Gym) Unlock() {
	gym.mu.Unlock()
}

// --- Set methods with dirty tracking ---

func (gym *Gym) SetId(v string) {
	if gym.Id != v {
		gym.Id = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "Id")
		}
	}
}

func (gym *Gym) SetLat(v float64) {
	if !floatAlmostEqual(gym.Lat, v, floatTolerance) {
		gym.Lat = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "Lat")
		}
	}
}

func (gym *Gym) SetLon(v float64) {
	if !floatAlmostEqual(gym.Lon, v, floatTolerance) {
		gym.Lon = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "Lon")
		}
	}
}

func (gym *Gym) SetName(v null.String) {
	if gym.Name != v {
		gym.Name = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "Name")
		}
	}
}

func (gym *Gym) SetUrl(v null.String) {
	if gym.Url != v {
		gym.Url = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "Url")
		}
	}
}

func (gym *Gym) SetLastModifiedTimestamp(v null.Int) {
	if gym.LastModifiedTimestamp != v {
		gym.LastModifiedTimestamp = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "LastModifiedTimestamp")
		}
	}
}

func (gym *Gym) SetRaidEndTimestamp(v null.Int) {
	if gym.RaidEndTimestamp != v {
		gym.RaidEndTimestamp = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "RaidEndTimestamp")
		}
	}
}

func (gym *Gym) SetRaidSpawnTimestamp(v null.Int) {
	if gym.RaidSpawnTimestamp != v {
		gym.RaidSpawnTimestamp = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "RaidSpawnTimestamp")
		}
	}
}

func (gym *Gym) SetRaidBattleTimestamp(v null.Int) {
	if gym.RaidBattleTimestamp != v {
		gym.RaidBattleTimestamp = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "RaidBattleTimestamp")
		}
	}
}

func (gym *Gym) SetRaidPokemonId(v null.Int) {
	if gym.RaidPokemonId != v {
		gym.RaidPokemonId = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "RaidPokemonId")
		}
	}
}

func (gym *Gym) SetGuardingPokemonId(v null.Int) {
	if gym.GuardingPokemonId != v {
		gym.GuardingPokemonId = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "GuardingPokemonId")
		}
	}
}

func (gym *Gym) SetGuardingPokemonDisplay(v null.String) {
	if gym.GuardingPokemonDisplay != v {
		gym.GuardingPokemonDisplay = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "GuardingPokemonDisplay")
		}
	}
}

func (gym *Gym) SetAvailableSlots(v null.Int) {
	if gym.AvailableSlots != v {
		gym.AvailableSlots = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "AvailableSlots")
		}
	}
}

func (gym *Gym) SetTeamId(v null.Int) {
	if gym.TeamId != v {
		gym.TeamId = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "TeamId")
		}
	}
}

func (gym *Gym) SetRaidLevel(v null.Int) {
	if gym.RaidLevel != v {
		gym.RaidLevel = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "RaidLevel")
		}
	}
}

func (gym *Gym) SetEnabled(v null.Int) {
	if gym.Enabled != v {
		gym.Enabled = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "Enabled")
		}
	}
}

func (gym *Gym) SetExRaidEligible(v null.Int) {
	if gym.ExRaidEligible != v {
		gym.ExRaidEligible = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "ExRaidEligible")
		}
	}
}

func (gym *Gym) SetInBattle(v null.Int) {
	if gym.InBattle != v {
		gym.InBattle = v
		//Do not set to dirty, as don't trigger an update
		gym.internalDirty = true
	}
}

func (gym *Gym) SetRaidPokemonMove1(v null.Int) {
	if gym.RaidPokemonMove1 != v {
		gym.RaidPokemonMove1 = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "RaidPokemonMove1")
		}
	}
}

func (gym *Gym) SetRaidPokemonMove2(v null.Int) {
	if gym.RaidPokemonMove2 != v {
		gym.RaidPokemonMove2 = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "RaidPokemonMove2")
		}
	}
}

func (gym *Gym) SetRaidPokemonForm(v null.Int) {
	if gym.RaidPokemonForm != v {
		gym.RaidPokemonForm = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "RaidPokemonForm")
		}
	}
}

func (gym *Gym) SetRaidPokemonAlignment(v null.Int) {
	if gym.RaidPokemonAlignment != v {
		gym.RaidPokemonAlignment = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "RaidPokemonAlignment")
		}
	}
}

func (gym *Gym) SetRaidPokemonCp(v null.Int) {
	if gym.RaidPokemonCp != v {
		gym.RaidPokemonCp = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "RaidPokemonCp")
		}
	}
}

func (gym *Gym) SetRaidIsExclusive(v null.Int) {
	if gym.RaidIsExclusive != v {
		gym.RaidIsExclusive = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "RaidIsExclusive")
		}
	}
}

func (gym *Gym) SetCellId(v null.Int) {
	if gym.CellId != v {
		gym.CellId = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "CellId")
		}
	}
}

func (gym *Gym) SetDeleted(v bool) {
	if gym.Deleted != v {
		gym.Deleted = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "Deleted")
		}
	}
}

func (gym *Gym) SetTotalCp(v null.Int) {
	if gym.TotalCp != v {
		gym.TotalCp = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "TotalCp")
		}
	}
}

func (gym *Gym) SetRaidPokemonGender(v null.Int) {
	if gym.RaidPokemonGender != v {
		gym.RaidPokemonGender = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "RaidPokemonGender")
		}
	}
}

func (gym *Gym) SetSponsorId(v null.Int) {
	if gym.SponsorId != v {
		gym.SponsorId = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "SponsorId")
		}
	}
}

func (gym *Gym) SetPartnerId(v null.String) {
	if gym.PartnerId != v {
		gym.PartnerId = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "PartnerId")
		}
	}
}

func (gym *Gym) SetRaidPokemonCostume(v null.Int) {
	if gym.RaidPokemonCostume != v {
		gym.RaidPokemonCostume = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "RaidPokemonCostume")
		}
	}
}

func (gym *Gym) SetRaidPokemonEvolution(v null.Int) {
	if gym.RaidPokemonEvolution != v {
		gym.RaidPokemonEvolution = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "RaidPokemonEvolution")
		}
	}
}

func (gym *Gym) SetArScanEligible(v null.Int) {
	if gym.ArScanEligible != v {
		gym.ArScanEligible = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "ArScanEligible")
		}
	}
}

func (gym *Gym) SetPowerUpLevel(v null.Int) {
	if gym.PowerUpLevel != v {
		gym.PowerUpLevel = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "PowerUpLevel")
		}
	}
}

func (gym *Gym) SetPowerUpPoints(v null.Int) {
	if gym.PowerUpPoints != v {
		gym.PowerUpPoints = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "PowerUpPoints")
		}
	}
}

func (gym *Gym) SetPowerUpEndTimestamp(v null.Int) {
	if gym.PowerUpEndTimestamp != v {
		gym.PowerUpEndTimestamp = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "PowerUpEndTimestamp")
		}
	}
}

func (gym *Gym) SetDescription(v null.String) {
	if gym.Description != v {
		gym.Description = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "Description")
		}
	}
}

func (gym *Gym) SetDefenders(v null.String) {
	if gym.Defenders != v {
		gym.Defenders = v
		//Do not set to dirty, as don't trigger an update
		gym.internalDirty = true
	}
}

func (gym *Gym) SetRsvps(v null.String) {
	if gym.Rsvps != v {
		gym.Rsvps = v
		gym.dirty = true
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, "Rsvps")
		}
	}
}
