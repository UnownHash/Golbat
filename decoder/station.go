package decoder

import (
	"sync"

	"github.com/guregu/null/v6"
)

// Station struct.
// REMINDER! Dirty flag pattern - use setter methods to modify fields
type Station struct {
	mu sync.Mutex `db:"-" json:"-"` // Object-level mutex

	Id                string  `db:"id"`
	Lat               float64 `db:"lat"`
	Lon               float64 `db:"lon"`
	Name              string  `db:"name"`
	CellId            int64   `db:"cell_id"`
	StartTime         int64   `db:"start_time"`
	EndTime           int64   `db:"end_time"`
	CooldownComplete  int64   `db:"cooldown_complete"`
	IsBattleAvailable bool    `db:"is_battle_available"`
	IsInactive        bool    `db:"is_inactive"`
	Updated           int64   `db:"updated"`

	BattleLevel               null.Int   `db:"battle_level"`
	BattleStart               null.Int   `db:"battle_start"`
	BattleEnd                 null.Int   `db:"battle_end"`
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

	TotalStationedPokemon null.Int    `db:"total_stationed_pokemon"`
	TotalStationedGmax    null.Int    `db:"total_stationed_gmax"`
	StationedPokemon      null.String `db:"stationed_pokemon"`

	dirty         bool     `db:"-" json:"-"` // Not persisted - tracks if object needs saving
	newRecord     bool     `db:"-" json:"-"` // Not persisted - tracks if this is a new record
	changedFields []string `db:"-" json:"-"` // Track which fields changed (only when dbDebugEnabled)

	oldValues StationOldValues `db:"-" json:"-"` // Old values for webhook comparison
}

// StationOldValues holds old field values for webhook comparison
type StationOldValues struct {
	EndTime                int64
	BattleEnd              null.Int
	BattlePokemonId        null.Int
	BattlePokemonForm      null.Int
	BattlePokemonCostume   null.Int
	BattlePokemonGender    null.Int
	BattlePokemonBreadMode null.Int
}

// IsDirty returns true if any field has been modified
func (station *Station) IsDirty() bool {
	return station.dirty
}

// ClearDirty resets the dirty flag (call after saving to DB)
func (station *Station) ClearDirty() {
	station.dirty = false
}

// IsNewRecord returns true if this is a new record (not yet in DB)
func (station *Station) IsNewRecord() bool {
	return station.newRecord
}

// Lock acquires the Station's mutex
func (station *Station) Lock() {
	station.mu.Lock()
}

// Unlock releases the Station's mutex
func (station *Station) Unlock() {
	station.mu.Unlock()
}

// snapshotOldValues saves current values for webhook comparison
// Call this after loading from cache/DB but before modifications
func (station *Station) snapshotOldValues() {
	station.oldValues = StationOldValues{
		EndTime:                station.EndTime,
		BattleEnd:              station.BattleEnd,
		BattlePokemonId:        station.BattlePokemonId,
		BattlePokemonForm:      station.BattlePokemonForm,
		BattlePokemonCostume:   station.BattlePokemonCostume,
		BattlePokemonGender:    station.BattlePokemonGender,
		BattlePokemonBreadMode: station.BattlePokemonBreadMode,
	}
}

// --- Set methods with dirty tracking ---

func (station *Station) SetId(v string) {
	if station.Id != v {
		station.Id = v
		station.dirty = true
		if dbDebugEnabled {
			station.changedFields = append(station.changedFields, "Id")
		}
	}
}

func (station *Station) SetLat(v float64) {
	if !floatAlmostEqual(station.Lat, v, floatTolerance) {
		station.Lat = v
		station.dirty = true
		if dbDebugEnabled {
			station.changedFields = append(station.changedFields, "Lat")
		}
	}
}

func (station *Station) SetLon(v float64) {
	if !floatAlmostEqual(station.Lon, v, floatTolerance) {
		station.Lon = v
		station.dirty = true
		if dbDebugEnabled {
			station.changedFields = append(station.changedFields, "Lon")
		}
	}
}

func (station *Station) SetName(v string) {
	if station.Name != v {
		station.Name = v
		station.dirty = true
		if dbDebugEnabled {
			station.changedFields = append(station.changedFields, "Name")
		}
	}
}

func (station *Station) SetCellId(v int64) {
	if station.CellId != v {
		station.CellId = v
		station.dirty = true
		if dbDebugEnabled {
			station.changedFields = append(station.changedFields, "CellId")
		}
	}
}

func (station *Station) SetStartTime(v int64) {
	if station.StartTime != v {
		station.StartTime = v
		station.dirty = true
		if dbDebugEnabled {
			station.changedFields = append(station.changedFields, "StartTime")
		}
	}
}

func (station *Station) SetEndTime(v int64) {
	if station.EndTime != v {
		station.EndTime = v
		station.dirty = true
		if dbDebugEnabled {
			station.changedFields = append(station.changedFields, "EndTime")
		}
	}
}

func (station *Station) SetCooldownComplete(v int64) {
	if station.CooldownComplete != v {
		station.CooldownComplete = v
		station.dirty = true
		if dbDebugEnabled {
			station.changedFields = append(station.changedFields, "CooldownComplete")
		}
	}
}

func (station *Station) SetIsBattleAvailable(v bool) {
	if station.IsBattleAvailable != v {
		station.IsBattleAvailable = v
		station.dirty = true
		if dbDebugEnabled {
			station.changedFields = append(station.changedFields, "IsBattleAvailable")
		}
	}
}

func (station *Station) SetIsInactive(v bool) {
	if station.IsInactive != v {
		station.IsInactive = v
		station.dirty = true
		if dbDebugEnabled {
			station.changedFields = append(station.changedFields, "IsInactive")
		}
	}
}

func (station *Station) SetBattleLevel(v null.Int) {
	if station.BattleLevel != v {
		station.BattleLevel = v
		station.dirty = true
		if dbDebugEnabled {
			station.changedFields = append(station.changedFields, "BattleLevel")
		}
	}
}

func (station *Station) SetBattleStart(v null.Int) {
	if station.BattleStart != v {
		station.BattleStart = v
		station.dirty = true
		if dbDebugEnabled {
			station.changedFields = append(station.changedFields, "BattleStart")
		}
	}
}

func (station *Station) SetBattleEnd(v null.Int) {
	if station.BattleEnd != v {
		station.BattleEnd = v
		station.dirty = true
		if dbDebugEnabled {
			station.changedFields = append(station.changedFields, "BattleEnd")
		}
	}
}

func (station *Station) SetBattlePokemonId(v null.Int) {
	if station.BattlePokemonId != v {
		station.BattlePokemonId = v
		station.dirty = true
		if dbDebugEnabled {
			station.changedFields = append(station.changedFields, "BattlePokemonId")
		}
	}
}

func (station *Station) SetBattlePokemonForm(v null.Int) {
	if station.BattlePokemonForm != v {
		station.BattlePokemonForm = v
		station.dirty = true
		if dbDebugEnabled {
			station.changedFields = append(station.changedFields, "BattlePokemonForm")
		}
	}
}

func (station *Station) SetBattlePokemonCostume(v null.Int) {
	if station.BattlePokemonCostume != v {
		station.BattlePokemonCostume = v
		station.dirty = true
		if dbDebugEnabled {
			station.changedFields = append(station.changedFields, "BattlePokemonCostume")
		}
	}
}

func (station *Station) SetBattlePokemonGender(v null.Int) {
	if station.BattlePokemonGender != v {
		station.BattlePokemonGender = v
		station.dirty = true
		if dbDebugEnabled {
			station.changedFields = append(station.changedFields, "BattlePokemonGender")
		}
	}
}

func (station *Station) SetBattlePokemonAlignment(v null.Int) {
	if station.BattlePokemonAlignment != v {
		station.BattlePokemonAlignment = v
		station.dirty = true
		if dbDebugEnabled {
			station.changedFields = append(station.changedFields, "BattlePokemonAlignment")
		}
	}
}

func (station *Station) SetBattlePokemonBreadMode(v null.Int) {
	if station.BattlePokemonBreadMode != v {
		station.BattlePokemonBreadMode = v
		station.dirty = true
		if dbDebugEnabled {
			station.changedFields = append(station.changedFields, "BattlePokemonBreadMode")
		}
	}
}

func (station *Station) SetBattlePokemonMove1(v null.Int) {
	if station.BattlePokemonMove1 != v {
		station.BattlePokemonMove1 = v
		station.dirty = true
		if dbDebugEnabled {
			station.changedFields = append(station.changedFields, "BattlePokemonMove1")
		}
	}
}

func (station *Station) SetBattlePokemonMove2(v null.Int) {
	if station.BattlePokemonMove2 != v {
		station.BattlePokemonMove2 = v
		station.dirty = true
		if dbDebugEnabled {
			station.changedFields = append(station.changedFields, "BattlePokemonMove2")
		}
	}
}

func (station *Station) SetBattlePokemonStamina(v null.Int) {
	if station.BattlePokemonStamina != v {
		station.BattlePokemonStamina = v
		station.dirty = true
		if dbDebugEnabled {
			station.changedFields = append(station.changedFields, "BattlePokemonStamina")
		}
	}
}

func (station *Station) SetBattlePokemonCpMultiplier(v null.Float) {
	if !nullFloatAlmostEqual(station.BattlePokemonCpMultiplier, v, floatTolerance) {
		station.BattlePokemonCpMultiplier = v
		station.dirty = true
		if dbDebugEnabled {
			station.changedFields = append(station.changedFields, "BattlePokemonCpMultiplier")
		}
	}
}

func (station *Station) SetTotalStationedPokemon(v null.Int) {
	if station.TotalStationedPokemon != v {
		station.TotalStationedPokemon = v
		station.dirty = true
		if dbDebugEnabled {
			station.changedFields = append(station.changedFields, "TotalStationedPokemon")
		}
	}
}

func (station *Station) SetTotalStationedGmax(v null.Int) {
	if station.TotalStationedGmax != v {
		station.TotalStationedGmax = v
		station.dirty = true
		if dbDebugEnabled {
			station.changedFields = append(station.changedFields, "TotalStationedGmax")
		}
	}
}

func (station *Station) SetStationedPokemon(v null.String) {
	if station.StationedPokemon != v {
		station.StationedPokemon = v
		station.dirty = true
		if dbDebugEnabled {
			station.changedFields = append(station.changedFields, "StationedPokemon")
		}
	}
}
