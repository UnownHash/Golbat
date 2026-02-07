package decoder

import (
	"fmt"

	"golbat/grpc"

	"github.com/guregu/null/v6"
	"github.com/sasha-s/go-deadlock"
)

// PokemonData contains all database-persisted fields for Pokemon.
// This struct is embedded in Pokemon and can be safely copied for write-behind queueing.
type PokemonData struct {
	Id                      Uint64Str   `db:"id"`
	PokestopId              null.String `db:"pokestop_id"`
	SpawnId                 null.Int    `db:"spawn_id"`
	Lat                     float64     `db:"lat"`
	Lon                     float64     `db:"lon"`
	Weight                  null.Float  `db:"weight"`
	Size                    null.Int    `db:"size"`
	Height                  null.Float  `db:"height"`
	ExpireTimestamp         null.Int    `db:"expire_timestamp"`
	Updated                 null.Int    `db:"updated"`
	PokemonId               int16       `db:"pokemon_id"`
	Move1                   null.Int    `db:"move_1"`
	Move2                   null.Int    `db:"move_2"`
	Gender                  null.Int    `db:"gender"`
	Cp                      null.Int    `db:"cp"`
	AtkIv                   null.Int    `db:"atk_iv"`
	DefIv                   null.Int    `db:"def_iv"`
	StaIv                   null.Int    `db:"sta_iv"`
	GolbatInternal          []byte      `db:"golbat_internal"`
	Iv                      null.Float  `db:"iv"`
	Form                    null.Int    `db:"form"`
	Level                   null.Int    `db:"level"`
	IsStrong                null.Bool   `db:"strong"`
	Weather                 null.Int    `db:"weather"`
	Costume                 null.Int    `db:"costume"`
	FirstSeenTimestamp      int64       `db:"first_seen_timestamp"`
	Changed                 int64       `db:"changed"`
	CellId                  null.Int    `db:"cell_id"`
	ExpireTimestampVerified bool        `db:"expire_timestamp_verified"`
	DisplayPokemonId        null.Int    `db:"display_pokemon_id"`
	IsDitto                 bool        `db:"is_ditto"`
	SeenType                null.String `db:"seen_type"`
	Shiny                   null.Bool   `db:"shiny"`
	Username                null.String `db:"username"`
	Capture1                null.Float  `db:"capture_1"`
	Capture2                null.Float  `db:"capture_2"`
	Capture3                null.Float  `db:"capture_3"`
	Pvp                     null.String `db:"pvp"`
	IsEvent                 int8        `db:"is_event"`
}

// Pokemon struct.
// REMINDER! Keep hasChangesPokemon updated after making changes
//
// AtkIv/DefIv/StaIv: Should not be set directly. Use calculateIv
//
// GolbatInternal: internal data not exposed to frontend/users
//
// FirstSeenTimestamp: This field is used in IsNewRecord. It should only be set in savePokemonRecord.
type Pokemon struct {
	mu deadlock.Mutex `db:"-"` // Object-level mutex

	PokemonData // Embedded data fields - can be copied for write-behind queue

	internal grpc.PokemonInternal `db:"-"` // Memory-only internal state

	dirty         bool     `db:"-"` // Not persisted - tracks if object needs saving
	newRecord     bool     `db:"-"`
	changedFields []string `db:"-"` // Track which fields changed (only when dbDebugEnabled)

	oldValues PokemonOldValues `db:"-"` // Old values for webhook comparison and stats
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
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("PokestopId:%s->%s", FormatNull(pokemon.PokestopId), FormatNull(v)))
		}
		pokemon.PokestopId = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetSpawnId(v null.Int) {
	if pokemon.SpawnId != v {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("SpawnId:%s->%s", FormatNull(pokemon.SpawnId), FormatNull(v)))
		}
		pokemon.SpawnId = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetLat(v float64) {
	if !floatAlmostEqual(pokemon.Lat, v, floatTolerance) {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("Lat:%f->%f", pokemon.Lat, v))
		}
		pokemon.Lat = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetLon(v float64) {
	if !floatAlmostEqual(pokemon.Lon, v, floatTolerance) {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("Lon:%f->%f", pokemon.Lon, v))
		}
		pokemon.Lon = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetPokemonId(v int16) {
	if pokemon.PokemonId != v {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("PokemonId:%d->%d", pokemon.PokemonId, v))
		}
		pokemon.PokemonId = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetForm(v null.Int) {
	if pokemon.Form != v {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("Form:%s->%s", FormatNull(pokemon.Form), FormatNull(v)))
		}
		pokemon.Form = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetCostume(v null.Int) {
	if pokemon.Costume != v {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("Costume:%s->%s", FormatNull(pokemon.Costume), FormatNull(v)))
		}
		pokemon.Costume = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetGender(v null.Int) {
	if pokemon.Gender != v {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("Gender:%s->%s", FormatNull(pokemon.Gender), FormatNull(v)))
		}
		pokemon.Gender = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetWeather(v null.Int) {
	if pokemon.Weather != v {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("Weather:%s->%s", FormatNull(pokemon.Weather), FormatNull(v)))
		}
		pokemon.Weather = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetIsStrong(v null.Bool) {
	if pokemon.IsStrong != v {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("IsStrong:%s->%s", FormatNull(pokemon.IsStrong), FormatNull(v)))
		}
		pokemon.IsStrong = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetExpireTimestamp(v null.Int) {
	if pokemon.ExpireTimestamp != v {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("ExpireTimestamp:%s->%s", FormatNull(pokemon.ExpireTimestamp), FormatNull(v)))
		}
		pokemon.ExpireTimestamp = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetExpireTimestampVerified(v bool) {
	if pokemon.ExpireTimestampVerified != v {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("ExpireTimestampVerified:%t->%t", pokemon.ExpireTimestampVerified, v))
		}
		pokemon.ExpireTimestampVerified = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetSeenType(v null.String) {
	if pokemon.SeenType != v {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("SeenType:%s->%s", FormatNull(pokemon.SeenType), FormatNull(v)))
		}
		pokemon.SeenType = v
		pokemon.dirty = true
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
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("CellId:%s->%s", FormatNull(pokemon.CellId), FormatNull(v)))
		}
		pokemon.CellId = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetIsEvent(v int8) {
	if pokemon.IsEvent != v {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("IsEvent:%d->%d", pokemon.IsEvent, v))
		}
		pokemon.IsEvent = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetShiny(v null.Bool) {
	if pokemon.Shiny != v {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("Shiny:%s->%s", FormatNull(pokemon.Shiny), FormatNull(v)))
		}
		pokemon.Shiny = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetCp(v null.Int) {
	if pokemon.Cp != v {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("Cp:%s->%s", FormatNull(pokemon.Cp), FormatNull(v)))
		}
		pokemon.Cp = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetLevel(v null.Int) {
	if pokemon.Level != v {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("Level:%s->%s", FormatNull(pokemon.Level), FormatNull(v)))
		}
		pokemon.Level = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetMove1(v null.Int) {
	if pokemon.Move1 != v {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("Move1:%s->%s", FormatNull(pokemon.Move1), FormatNull(v)))
		}
		pokemon.Move1 = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetMove2(v null.Int) {
	if pokemon.Move2 != v {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("Move2:%s->%s", FormatNull(pokemon.Move2), FormatNull(v)))
		}
		pokemon.Move2 = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetHeight(v null.Float) {
	if !nullFloatAlmostEqual(pokemon.Height, v, floatTolerance) {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("Height:%s->%s", FormatNull(pokemon.Height), FormatNull(v)))
		}
		pokemon.Height = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetWeight(v null.Float) {
	if !nullFloatAlmostEqual(pokemon.Weight, v, floatTolerance) {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("Weight:%s->%s", FormatNull(pokemon.Weight), FormatNull(v)))
		}
		pokemon.Weight = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetSize(v null.Int) {
	if pokemon.Size != v {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("Size:%s->%s", FormatNull(pokemon.Size), FormatNull(v)))
		}
		pokemon.Size = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetIsDitto(v bool) {
	if pokemon.IsDitto != v {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("IsDitto:%t->%t", pokemon.IsDitto, v))
		}
		pokemon.IsDitto = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetDisplayPokemonId(v null.Int) {
	if pokemon.DisplayPokemonId != v {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("DisplayPokemonId:%s->%s", FormatNull(pokemon.DisplayPokemonId), FormatNull(v)))
		}
		pokemon.DisplayPokemonId = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetPvp(v null.String) {
	if pokemon.Pvp != v {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("Pvp:%s->%s", FormatNull(pokemon.Pvp), FormatNull(v)))
		}
		pokemon.Pvp = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetCapture1(v null.Float) {
	if !nullFloatAlmostEqual(pokemon.Capture1, v, floatTolerance) {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("Capture1:%s->%s", FormatNull(pokemon.Capture1), FormatNull(v)))
		}
		pokemon.Capture1 = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetCapture2(v null.Float) {
	if !nullFloatAlmostEqual(pokemon.Capture2, v, floatTolerance) {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("Capture2:%s->%s", FormatNull(pokemon.Capture2), FormatNull(v)))
		}
		pokemon.Capture2 = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetCapture3(v null.Float) {
	if !nullFloatAlmostEqual(pokemon.Capture3, v, floatTolerance) {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("Capture3:%s->%s", FormatNull(pokemon.Capture3), FormatNull(v)))
		}
		pokemon.Capture3 = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetUpdated(v null.Int) {
	if pokemon.Updated != v {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("Updated:%s->%s", FormatNull(pokemon.Updated), FormatNull(v)))
		}
		pokemon.Updated = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetChanged(v int64) {
	if pokemon.Changed != v {
		if dbDebugEnabled {
			pokemon.changedFields = append(pokemon.changedFields, fmt.Sprintf("Changed:%d->%d", pokemon.Changed, v))
		}
		pokemon.Changed = v
		pokemon.dirty = true
	}
}
