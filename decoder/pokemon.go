package decoder

import (
	"sync"

	"golbat/grpc"

	"github.com/guregu/null/v6"
)

// Pokemon struct.
// REMINDER! Keep hasChangesPokemon updated after making changes
//
// AtkIv/DefIv/StaIv: Should not be set directly. Use calculateIv
//
// GolbatInternal: internal data not exposed to frontend/users
//
// FirstSeenTimestamp: This field is used in IsNewRecord. It should only be set in savePokemonRecord.
type Pokemon struct {
	mu sync.Mutex `db:"-" json:"-"` // Object-level mutex

	Id                      uint64      `db:"id" json:"id,string"`
	PokestopId              null.String `db:"pokestop_id" json:"pokestop_id"`
	SpawnId                 null.Int    `db:"spawn_id" json:"spawn_id"`
	Lat                     float64     `db:"lat" json:"lat"`
	Lon                     float64     `db:"lon" json:"lon"`
	Weight                  null.Float  `db:"weight" json:"weight"`
	Size                    null.Int    `db:"size" json:"size"`
	Height                  null.Float  `db:"height" json:"height"`
	ExpireTimestamp         null.Int    `db:"expire_timestamp" json:"expire_timestamp"`
	Updated                 null.Int    `db:"updated" json:"updated"`
	PokemonId               int16       `db:"pokemon_id" json:"pokemon_id"`
	Move1                   null.Int    `db:"move_1" json:"move_1"`
	Move2                   null.Int    `db:"move_2" json:"move_2"`
	Gender                  null.Int    `db:"gender" json:"gender"`
	Cp                      null.Int    `db:"cp" json:"cp"`
	AtkIv                   null.Int    `db:"atk_iv" json:"atk_iv"`
	DefIv                   null.Int    `db:"def_iv" json:"def_iv"`
	StaIv                   null.Int    `db:"sta_iv" json:"sta_iv"`
	GolbatInternal          []byte      `db:"golbat_internal" json:"golbat_internal"`
	Iv                      null.Float  `db:"iv" json:"iv"`
	Form                    null.Int    `db:"form" json:"form"`
	Level                   null.Int    `db:"level" json:"level"`
	IsStrong                null.Bool   `db:"strong" json:"strong"`
	Weather                 null.Int    `db:"weather" json:"weather"`
	Costume                 null.Int    `db:"costume" json:"costume"`
	FirstSeenTimestamp      int64       `db:"first_seen_timestamp" json:"first_seen_timestamp"`
	Changed                 int64       `db:"changed" json:"changed"`
	CellId                  null.Int    `db:"cell_id" json:"cell_id"`
	ExpireTimestampVerified bool        `db:"expire_timestamp_verified" json:"expire_timestamp_verified"`
	DisplayPokemonId        null.Int    `db:"display_pokemon_id" json:"display_pokemon_id"`
	IsDitto                 bool        `db:"is_ditto" json:"is_ditto"`
	SeenType                null.String `db:"seen_type" json:"seen_type"`
	Shiny                   null.Bool   `db:"shiny" json:"shiny"`
	Username                null.String `db:"username" json:"username"`
	Capture1                null.Float  `db:"capture_1" json:"capture_1"`
	Capture2                null.Float  `db:"capture_2" json:"capture_2"`
	Capture3                null.Float  `db:"capture_3" json:"capture_3"`
	Pvp                     null.String `db:"pvp" json:"pvp"`
	IsEvent                 int8        `db:"is_event" json:"is_event"`

	internal grpc.PokemonInternal

	dirty         bool     `db:"-" json:"-"` // Not persisted - tracks if object needs saving
	newRecord     bool     `db:"-" json:"-"`
	changedFields []string `db:"-" json:"-"` // Track which fields changed (only when dbDebugEnabled)

	oldValues PokemonOldValues `db:"-" json:"-"` // Old values for webhook comparison and stats
}

// PokemonOldValues holds old field values for webhook comparison, stats, and R-tree updates
type PokemonOldValues struct {
	PokemonId int16
	Weather   null.Int
	Cp        null.Int
	SeenType  null.String
	Lat       float64
	Lon       float64
}

//
//CREATE TABLE `pokemon` (
//`id` varchar(25) NOT NULL,
//`pokestop_id` varchar(35) DEFAULT NULL,
//`spawn_id` bigint unsigned DEFAULT NULL,
//`lat` double(18,14) NOT NULL,
//`lon` double(18,14) NOT NULL,
//`weight` double(18,14) DEFAULT NULL,
//`size` double(18,14) DEFAULT NULL,
//`expire_timestamp` int unsigned DEFAULT NULL,
//`updated` int unsigned DEFAULT NULL,
//`pokemon_id` smallint unsigned NOT NULL,
//`move_1` smallint unsigned DEFAULT NULL,
//`move_2` smallint unsigned DEFAULT NULL,
//`gender` tinyint unsigned DEFAULT NULL,
//`cp` smallint unsigned DEFAULT NULL,
//`atk_iv` tinyint unsigned DEFAULT NULL,
//`def_iv` tinyint unsigned DEFAULT NULL,
//`sta_iv` tinyint unsigned DEFAULT NULL,
//`form` smallint unsigned DEFAULT NULL,
//`level` tinyint unsigned DEFAULT NULL,
//`weather` tinyint unsigned DEFAULT NULL,
//`costume` tinyint unsigned DEFAULT NULL,
//`first_seen_timestamp` int unsigned NOT NULL,
//`changed` int unsigned NOT NULL DEFAULT '0',
//`iv` float(5,2) unsigned GENERATED ALWAYS AS (((((`atk_iv` + `def_iv`) + `sta_iv`) * 100) / 45)) VIRTUAL,
//`cell_id` bigint unsigned DEFAULT NULL,
//`expire_timestamp_verified` tinyint unsigned NOT NULL,
//`display_pokemon_id` smallint unsigned DEFAULT NULL,
//`seen_type` enum('wild','encounter','nearby_stop','nearby_cell') DEFAULT NULL,
//`shiny` tinyint(1) DEFAULT '0',
//`username` varchar(32) DEFAULT NULL,
//`capture_1` double(18,14) DEFAULT NULL,
//`capture_2` double(18,14) DEFAULT NULL,
//`capture_3` double(18,14) DEFAULT NULL,
//`pvp` text,
//`is_event` tinyint unsigned NOT NULL DEFAULT '0',
//PRIMARY KEY (`id`,`is_event`),
//KEY `ix_coords` (`lat`,`lon`),
//KEY `ix_pokemon_id` (`pokemon_id`),
//KEY `ix_updated` (`updated`),
//KEY `fk_spawn_id` (`spawn_id`),
//KEY `fk_pokestop_id` (`pokestop_id`),
//KEY `ix_atk_iv` (`atk_iv`),
//KEY `ix_def_iv` (`def_iv`),
//KEY `ix_sta_iv` (`sta_iv`),
//KEY `ix_changed` (`changed`),
//KEY `ix_level` (`level`),
//KEY `fk_pokemon_cell_id` (`cell_id`),
//KEY `ix_expire_timestamp` (`expire_timestamp`),
//KEY `ix_iv` (`iv`)
//)

// IsDirty returns true if any field has been modified
func (pokemon *Pokemon) IsDirty() bool {
	return pokemon.dirty
}

// ClearDirty resets the dirty flag (call after saving to DB)
func (pokemon *Pokemon) ClearDirty() {
	pokemon.dirty = false
}

// snapshotOldValues saves current values for webhook comparison, stats, and R-tree updates
// Call this after loading from cache/DB but before modifications
func (pokemon *Pokemon) snapshotOldValues() {
	pokemon.oldValues = PokemonOldValues{
		PokemonId: pokemon.PokemonId,
		Weather:   pokemon.Weather,
		Cp:        pokemon.Cp,
		SeenType:  pokemon.SeenType,
		Lat:       pokemon.Lat,
		Lon:       pokemon.Lon,
	}
}

// Lock acquires the Pokemon's mutex
func (pokemon *Pokemon) Lock() {
	pokemon.mu.Lock()
}

// Unlock releases the Pokemon's mutex
func (pokemon *Pokemon) Unlock() {
	pokemon.mu.Unlock()
}

// --- Set methods with dirty tracking ---

func (pokemon *Pokemon) SetPokestopId(v null.String) {
	if pokemon.PokestopId != v {
		pokemon.PokestopId = v
		pokemon.dirty = true
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, "PokestopId")
		}
	}
}

func (pokemon *Pokemon) SetSpawnId(v null.Int) {
	if pokemon.SpawnId != v {
		pokemon.SpawnId = v
		pokemon.dirty = true
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, "SpawnId")
		}
	}
}

func (pokemon *Pokemon) SetLat(v float64) {
	if !floatAlmostEqual(pokemon.Lat, v, floatTolerance) {
		pokemon.Lat = v
		pokemon.dirty = true
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, "Lat")
		}
	}
}

func (pokemon *Pokemon) SetLon(v float64) {
	if !floatAlmostEqual(pokemon.Lon, v, floatTolerance) {
		pokemon.Lon = v
		pokemon.dirty = true
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, "Lon")
		}
	}
}

func (pokemon *Pokemon) SetPokemonId(v int16) {
	if pokemon.PokemonId != v {
		pokemon.PokemonId = v
		pokemon.dirty = true
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, "PokemonId")
		}
	}
}

func (pokemon *Pokemon) SetForm(v null.Int) {
	if pokemon.Form != v {
		pokemon.Form = v
		pokemon.dirty = true
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, "Form")
		}
	}
}

func (pokemon *Pokemon) SetCostume(v null.Int) {
	if pokemon.Costume != v {
		pokemon.Costume = v
		pokemon.dirty = true
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, "Costume")
		}
	}
}

func (pokemon *Pokemon) SetGender(v null.Int) {
	if pokemon.Gender != v {
		pokemon.Gender = v
		pokemon.dirty = true
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, "Gender")
		}
	}
}

func (pokemon *Pokemon) SetWeather(v null.Int) {
	if pokemon.Weather != v {
		pokemon.Weather = v
		pokemon.dirty = true
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, "Weather")
		}
	}
}

func (pokemon *Pokemon) SetIsStrong(v null.Bool) {
	if pokemon.IsStrong != v {
		pokemon.IsStrong = v
		pokemon.dirty = true
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, "IsStrong")
		}
	}
}

func (pokemon *Pokemon) SetExpireTimestamp(v null.Int) {
	if pokemon.ExpireTimestamp != v {
		pokemon.ExpireTimestamp = v
		pokemon.dirty = true
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, "ExpireTimestamp")
		}
	}
}

func (pokemon *Pokemon) SetExpireTimestampVerified(v bool) {
	if pokemon.ExpireTimestampVerified != v {
		pokemon.ExpireTimestampVerified = v
		pokemon.dirty = true
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, "ExpireTimestampVerified")
		}
	}
}

func (pokemon *Pokemon) SetSeenType(v null.String) {
	if pokemon.SeenType != v {
		pokemon.SeenType = v
		pokemon.dirty = true
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, "SeenType")
		}
	}
}

func (pokemon *Pokemon) SetUsername(v null.String) {
	if pokemon.Username != v {
		pokemon.Username = v
		//pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetCellId(v null.Int) {
	if pokemon.CellId != v {
		pokemon.CellId = v
		pokemon.dirty = true
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, "CellId")
		}
	}
}

func (pokemon *Pokemon) SetIsEvent(v int8) {
	if pokemon.IsEvent != v {
		pokemon.IsEvent = v
		pokemon.dirty = true
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, "IsEvent")
		}
	}
}

func (pokemon *Pokemon) SetShiny(v null.Bool) {
	if pokemon.Shiny != v {
		pokemon.Shiny = v
		pokemon.dirty = true
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, "Shiny")
		}
	}
}

func (pokemon *Pokemon) SetCp(v null.Int) {
	if pokemon.Cp != v {
		pokemon.Cp = v
		pokemon.dirty = true
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, "Cp")
		}
	}
}

func (pokemon *Pokemon) SetLevel(v null.Int) {
	if pokemon.Level != v {
		pokemon.Level = v
		pokemon.dirty = true
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, "Level")
		}
	}
}

func (pokemon *Pokemon) SetMove1(v null.Int) {
	if pokemon.Move1 != v {
		pokemon.Move1 = v
		pokemon.dirty = true
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, "Move1")
		}
	}
}

func (pokemon *Pokemon) SetMove2(v null.Int) {
	if pokemon.Move2 != v {
		pokemon.Move2 = v
		pokemon.dirty = true
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, "Move2")
		}
	}
}

func (pokemon *Pokemon) SetHeight(v null.Float) {
	if !nullFloatAlmostEqual(pokemon.Height, v, floatTolerance) {
		pokemon.Height = v
		pokemon.dirty = true
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, "Height")
		}
	}
}

func (pokemon *Pokemon) SetWeight(v null.Float) {
	if !nullFloatAlmostEqual(pokemon.Weight, v, floatTolerance) {
		pokemon.Weight = v
		pokemon.dirty = true
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, "Weight")
		}
	}
}

func (pokemon *Pokemon) SetSize(v null.Int) {
	if pokemon.Size != v {
		pokemon.Size = v
		pokemon.dirty = true
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, "Size")
		}
	}
}

func (pokemon *Pokemon) SetIsDitto(v bool) {
	if pokemon.IsDitto != v {
		pokemon.IsDitto = v
		pokemon.dirty = true
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, "IsDitto")
		}
	}
}

func (pokemon *Pokemon) SetDisplayPokemonId(v null.Int) {
	if pokemon.DisplayPokemonId != v {
		pokemon.DisplayPokemonId = v
		pokemon.dirty = true
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, "DisplayPokemonId")
		}
	}
}

func (pokemon *Pokemon) SetPvp(v null.String) {
	if pokemon.Pvp != v {
		pokemon.Pvp = v
		pokemon.dirty = true
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, "Pvp")
		}
	}
}

func (pokemon *Pokemon) SetCapture1(v null.Float) {
	if !nullFloatAlmostEqual(pokemon.Capture1, v, floatTolerance) {
		pokemon.Capture1 = v
		pokemon.dirty = true
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, "Capture1")
		}
	}
}

func (pokemon *Pokemon) SetCapture2(v null.Float) {
	if !nullFloatAlmostEqual(pokemon.Capture2, v, floatTolerance) {
		pokemon.Capture2 = v
		pokemon.dirty = true
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, "Capture2")
		}
	}
}

func (pokemon *Pokemon) SetCapture3(v null.Float) {
	if !nullFloatAlmostEqual(pokemon.Capture3, v, floatTolerance) {
		pokemon.Capture3 = v
		pokemon.dirty = true
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, "Capture3")
		}
	}
}
