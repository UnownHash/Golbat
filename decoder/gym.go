package decoder

import (
	"fmt"
	"sync"

	"github.com/guregu/null/v6"
)

// GymData contains all database-persisted fields for a Gym.
// This struct is copyable and used for write-behind queue snapshots.
type GymData struct {
	Id                     string      `db:"id"`
	Lat                    float64     `db:"lat"`
	Lon                    float64     `db:"lon"`
	Name                   null.String `db:"name"`
	Url                    null.String `db:"url"`
	LastModifiedTimestamp  null.Int    `db:"last_modified_timestamp"`
	RaidEndTimestamp       null.Int    `db:"raid_end_timestamp"`
	RaidSpawnTimestamp     null.Int    `db:"raid_spawn_timestamp"`
	RaidBattleTimestamp    null.Int    `db:"raid_battle_timestamp"`
	Updated                int64       `db:"updated"`
	RaidPokemonId          null.Int    `db:"raid_pokemon_id"`
	GuardingPokemonId      null.Int    `db:"guarding_pokemon_id"`
	GuardingPokemonDisplay null.String `db:"guarding_pokemon_display"`
	AvailableSlots         null.Int    `db:"available_slots"`
	TeamId                 null.Int    `db:"team_id"`
	RaidLevel              null.Int    `db:"raid_level"`
	Enabled                null.Int    `db:"enabled"`
	ExRaidEligible         null.Int    `db:"ex_raid_eligible"`
	InBattle               null.Int    `db:"in_battle"`
	RaidPokemonMove1       null.Int    `db:"raid_pokemon_move_1"`
	RaidPokemonMove2       null.Int    `db:"raid_pokemon_move_2"`
	RaidPokemonForm        null.Int    `db:"raid_pokemon_form"`
	RaidPokemonAlignment   null.Int    `db:"raid_pokemon_alignment"`
	RaidPokemonCp          null.Int    `db:"raid_pokemon_cp"`
	RaidIsExclusive        null.Int    `db:"raid_is_exclusive"`
	CellId                 null.Int    `db:"cell_id"`
	Deleted                bool        `db:"deleted"`
	TotalCp                null.Int    `db:"total_cp"`
	FirstSeenTimestamp     int64       `db:"first_seen_timestamp"`
	RaidPokemonGender      null.Int    `db:"raid_pokemon_gender"`
	SponsorId              null.Int    `db:"sponsor_id"`
	PartnerId              null.String `db:"partner_id"`
	RaidPokemonCostume     null.Int    `db:"raid_pokemon_costume"`
	RaidPokemonEvolution   null.Int    `db:"raid_pokemon_evolution"`
	ArScanEligible         null.Int    `db:"ar_scan_eligible"`
	PowerUpLevel           null.Int    `db:"power_up_level"`
	PowerUpPoints          null.Int    `db:"power_up_points"`
	PowerUpEndTimestamp    null.Int    `db:"power_up_end_timestamp"`
	Description            null.String `db:"description"`
	Defenders              null.String `db:"defenders"`
	Rsvps                  null.String `db:"rsvps"`
}

// Gym struct.
// REMINDER! Keep hasChangesGym updated after making changes
type Gym struct {
	mu sync.Mutex `db:"-"` // Object-level mutex

	GymData // Embedded data fields (all db columns)

	// Memory-only fields (not persisted to DB)
	RaidSeed null.Int `db:"-"` // Raid seed (memory only, sent in webhook as string)

	dirty         bool     `db:"-"` // Not persisted - tracks if object needs saving (to db)
	internalDirty bool     `db:"-"` // Not persisted - tracks if object needs saving (in memory only)
	newRecord     bool     `db:"-"` // Not persisted - tracks if this is a new record
	changedFields []string `db:"-"` // Track which fields changed (only when dbDebugEnabled)

	oldValues GymOldValues `db:"-"` // Old values for webhook comparison
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
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("Id:%s->%s", gym.Id, v))
		}
		gym.Id = v
		gym.dirty = true
	}
}

func (gym *Gym) SetLat(v float64) {
	if !floatAlmostEqual(gym.Lat, v, floatTolerance) {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("Lat:%f->%f", gym.Lat, v))
		}
		gym.Lat = v
		gym.dirty = true
	}
}

func (gym *Gym) SetLon(v float64) {
	if !floatAlmostEqual(gym.Lon, v, floatTolerance) {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("Lon:%f->%f", gym.Lon, v))
		}
		gym.Lon = v
		gym.dirty = true
	}
}

func (gym *Gym) SetName(v null.String) {
	if gym.Name != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("Name:%s->%s", FormatNull(gym.Name), FormatNull(v)))
		}
		gym.Name = v
		gym.dirty = true
	}
}

func (gym *Gym) SetUrl(v null.String) {
	if gym.Url != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("Url:%s->%s", FormatNull(gym.Url), FormatNull(v)))
		}
		gym.Url = v
		gym.dirty = true
	}
}

func (gym *Gym) SetLastModifiedTimestamp(v null.Int) {
	if gym.LastModifiedTimestamp != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("LastModifiedTimestamp:%s->%s", FormatNull(gym.LastModifiedTimestamp), FormatNull(v)))
		}
		gym.LastModifiedTimestamp = v
		gym.dirty = true
	}
}

func (gym *Gym) SetRaidEndTimestamp(v null.Int) {
	if gym.RaidEndTimestamp != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("RaidEndTimestamp:%s->%s", FormatNull(gym.RaidEndTimestamp), FormatNull(v)))
		}
		gym.RaidEndTimestamp = v
		gym.dirty = true
	}
}

func (gym *Gym) SetRaidSpawnTimestamp(v null.Int) {
	if gym.RaidSpawnTimestamp != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("RaidSpawnTimestamp:%s->%s", FormatNull(gym.RaidSpawnTimestamp), FormatNull(v)))
		}
		gym.RaidSpawnTimestamp = v
		gym.dirty = true
	}
}

func (gym *Gym) SetRaidBattleTimestamp(v null.Int) {
	if gym.RaidBattleTimestamp != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("RaidBattleTimestamp:%s->%s", FormatNull(gym.RaidBattleTimestamp), FormatNull(v)))
		}
		gym.RaidBattleTimestamp = v
		gym.dirty = true
	}
}

func (gym *Gym) SetRaidPokemonId(v null.Int) {
	if gym.RaidPokemonId != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("RaidPokemonId:%s->%s", FormatNull(gym.RaidPokemonId), FormatNull(v)))
		}
		gym.RaidPokemonId = v
		gym.dirty = true
	}
}

func (gym *Gym) SetGuardingPokemonId(v null.Int) {
	if gym.GuardingPokemonId != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("GuardingPokemonId:%s->%s", FormatNull(gym.GuardingPokemonId), FormatNull(v)))
		}
		gym.GuardingPokemonId = v
		gym.dirty = true
	}
}

func (gym *Gym) SetGuardingPokemonDisplay(v null.String) {
	if gym.GuardingPokemonDisplay != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("GuardingPokemonDisplay:%s->%s", FormatNull(gym.GuardingPokemonDisplay), FormatNull(v)))
		}
		gym.GuardingPokemonDisplay = v
		gym.dirty = true
	}
}

func (gym *Gym) SetAvailableSlots(v null.Int) {
	if gym.AvailableSlots != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("AvailableSlots:%s->%s", FormatNull(gym.AvailableSlots), FormatNull(v)))
		}
		gym.AvailableSlots = v
		gym.dirty = true
	}
}

func (gym *Gym) SetTeamId(v null.Int) {
	if gym.TeamId != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("TeamId:%s->%s", FormatNull(gym.TeamId), FormatNull(v)))
		}
		gym.TeamId = v
		gym.dirty = true
	}
}

func (gym *Gym) SetRaidLevel(v null.Int) {
	if gym.RaidLevel != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("RaidLevel:%s->%s", FormatNull(gym.RaidLevel), FormatNull(v)))
		}
		gym.RaidLevel = v
		gym.dirty = true
	}
}

func (gym *Gym) SetEnabled(v null.Int) {
	if gym.Enabled != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("Enabled:%s->%s", FormatNull(gym.Enabled), FormatNull(v)))
		}
		gym.Enabled = v
		gym.dirty = true
	}
}

func (gym *Gym) SetExRaidEligible(v null.Int) {
	if gym.ExRaidEligible != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("ExRaidEligible:%s->%s", FormatNull(gym.ExRaidEligible), FormatNull(v)))
		}
		gym.ExRaidEligible = v
		gym.dirty = true
	}
}

func (gym *Gym) SetInBattle(v null.Int) {
	if gym.InBattle != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("InBattle:%s->%s", FormatNull(gym.InBattle), FormatNull(v)))
		}
		gym.InBattle = v
		//Do not set to dirty, as don't trigger an update
		gym.internalDirty = true
	}
}

func (gym *Gym) SetRaidPokemonMove1(v null.Int) {
	if gym.RaidPokemonMove1 != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("RaidPokemonMove1:%s->%s", FormatNull(gym.RaidPokemonMove1), FormatNull(v)))
		}
		gym.RaidPokemonMove1 = v
		gym.dirty = true
	}
}

func (gym *Gym) SetRaidPokemonMove2(v null.Int) {
	if gym.RaidPokemonMove2 != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("RaidPokemonMove2:%s->%s", FormatNull(gym.RaidPokemonMove2), FormatNull(v)))
		}
		gym.RaidPokemonMove2 = v
		gym.dirty = true
	}
}

func (gym *Gym) SetRaidPokemonForm(v null.Int) {
	if gym.RaidPokemonForm != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("RaidPokemonForm:%s->%s", FormatNull(gym.RaidPokemonForm), FormatNull(v)))
		}
		gym.RaidPokemonForm = v
		gym.dirty = true
	}
}

func (gym *Gym) SetRaidPokemonAlignment(v null.Int) {
	if gym.RaidPokemonAlignment != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("RaidPokemonAlignment:%s->%s", FormatNull(gym.RaidPokemonAlignment), FormatNull(v)))
		}
		gym.RaidPokemonAlignment = v
		gym.dirty = true
	}
}

func (gym *Gym) SetRaidPokemonCp(v null.Int) {
	if gym.RaidPokemonCp != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("RaidPokemonCp:%s->%s", FormatNull(gym.RaidPokemonCp), FormatNull(v)))
		}
		gym.RaidPokemonCp = v
		gym.dirty = true
	}
}

func (gym *Gym) SetRaidIsExclusive(v null.Int) {
	if gym.RaidIsExclusive != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("RaidIsExclusive:%s->%s", FormatNull(gym.RaidIsExclusive), FormatNull(v)))
		}
		gym.RaidIsExclusive = v
		gym.dirty = true
	}
}

func (gym *Gym) SetCellId(v null.Int) {
	if gym.CellId != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("CellId:%s->%s", FormatNull(gym.CellId), FormatNull(v)))
		}
		gym.CellId = v
		gym.dirty = true
	}
}

func (gym *Gym) SetDeleted(v bool) {
	if gym.Deleted != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("Deleted:%t->%t", gym.Deleted, v))
		}
		gym.Deleted = v
		gym.dirty = true
	}
}

func (gym *Gym) SetTotalCp(v null.Int) {
	if gym.TotalCp != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("TotalCp:%s->%s", FormatNull(gym.TotalCp), FormatNull(v)))
		}
		gym.TotalCp = v
		gym.dirty = true
	}
}

func (gym *Gym) SetRaidPokemonGender(v null.Int) {
	if gym.RaidPokemonGender != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("RaidPokemonGender:%s->%s", FormatNull(gym.RaidPokemonGender), FormatNull(v)))
		}
		gym.RaidPokemonGender = v
		gym.dirty = true
	}
}

func (gym *Gym) SetSponsorId(v null.Int) {
	if gym.SponsorId != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("SponsorId:%s->%s", FormatNull(gym.SponsorId), FormatNull(v)))
		}
		gym.SponsorId = v
		gym.dirty = true
	}
}

func (gym *Gym) SetPartnerId(v null.String) {
	if gym.PartnerId != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("PartnerId:%s->%s", FormatNull(gym.PartnerId), FormatNull(v)))
		}
		gym.PartnerId = v
		gym.dirty = true
	}
}

func (gym *Gym) SetRaidPokemonCostume(v null.Int) {
	if gym.RaidPokemonCostume != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("RaidPokemonCostume:%s->%s", FormatNull(gym.RaidPokemonCostume), FormatNull(v)))
		}
		gym.RaidPokemonCostume = v
		gym.dirty = true
	}
}

func (gym *Gym) SetRaidPokemonEvolution(v null.Int) {
	if gym.RaidPokemonEvolution != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("RaidPokemonEvolution:%s->%s", FormatNull(gym.RaidPokemonEvolution), FormatNull(v)))
		}
		gym.RaidPokemonEvolution = v
		gym.dirty = true
	}
}

func (gym *Gym) SetArScanEligible(v null.Int) {
	if gym.ArScanEligible != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("ArScanEligible:%s->%s", FormatNull(gym.ArScanEligible), FormatNull(v)))
		}
		gym.ArScanEligible = v
		gym.dirty = true
	}
}

func (gym *Gym) SetPowerUpLevel(v null.Int) {
	if gym.PowerUpLevel != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("PowerUpLevel:%s->%s", FormatNull(gym.PowerUpLevel), FormatNull(v)))
		}
		gym.PowerUpLevel = v
		gym.dirty = true
	}
}

func (gym *Gym) SetPowerUpPoints(v null.Int) {
	if gym.PowerUpPoints != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("PowerUpPoints:%s->%s", FormatNull(gym.PowerUpPoints), FormatNull(v)))
		}
		gym.PowerUpPoints = v
		gym.dirty = true
	}
}

func (gym *Gym) SetPowerUpEndTimestamp(v null.Int) {
	if gym.PowerUpEndTimestamp != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("PowerUpEndTimestamp:%s->%s", FormatNull(gym.PowerUpEndTimestamp), FormatNull(v)))
		}
		gym.PowerUpEndTimestamp = v
		gym.dirty = true
	}
}

func (gym *Gym) SetDescription(v null.String) {
	if gym.Description != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("Description:%s->%s", FormatNull(gym.Description), FormatNull(v)))
		}
		gym.Description = v
		gym.dirty = true
	}
}

func (gym *Gym) SetDefenders(v null.String) {
	if gym.Defenders != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("Defenders:%s->%s", FormatNull(gym.Defenders), FormatNull(v)))
		}
		gym.Defenders = v
		//Do not set to dirty, as don't trigger an update
		gym.internalDirty = true
	}
}

func (gym *Gym) SetRsvps(v null.String) {
	if gym.Rsvps != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("Rsvps:%s->%s", FormatNull(gym.Rsvps), FormatNull(v)))
		}
		gym.Rsvps = v
		gym.dirty = true
	}
}

// SetRaidSeed sets the raid seed (memory only, not saved to DB)
func (gym *Gym) SetRaidSeed(v null.Int) {
	if gym.RaidSeed != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("RaidSeed:%s->%s", FormatNull(gym.RaidSeed), FormatNull(v)))
		}
		gym.RaidSeed = v
		// Do not set dirty, as this doesn't trigger a DB update
		gym.internalDirty = true
	}
}

func (gym *Gym) SetUpdated(v int64) {
	if gym.Updated != v {
		if dbDebugEnabled {
			gym.changedFields = append(gym.changedFields, fmt.Sprintf("Updated:%d->%d", gym.Updated, v))
		}
		gym.Updated = v
		gym.dirty = true
	}
}
