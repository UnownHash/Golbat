package decoder

import (
	"database/sql"
	"fmt"
	"github.com/golang/geo/s2"
	"github.com/google/go-cmp/cmp"
	"github.com/jellydator/ttlcache/v3"
	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"
	"golbat/pogo"
	"golbat/webhooks"
	"gopkg.in/guregu/null.v4"
	"math"
	"strconv"
	"time"
)

type Pokemon struct {
	Id                      string      `db:"id"`
	PokestopId              null.String `db:"pokestop_id"`
	SpawnId                 null.Int    `db:"spawn_id"`
	Lat                     float64     `db:"lat"`
	Lon                     float64     `db:"lon"`
	Weight                  null.Float  `db:"weight"`
	Size                    null.Float  `db:"size"`
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
	Form                    null.Int    `db:"form"`
	Level                   null.Int    `db:"level"`
	Weather                 null.Int    `db:"weather"`
	Costume                 null.Int    `db:"costume"`
	FirstSeenTimestamp      int64       `db:"first_seen_timestamp"`
	Changed                 int64       `db:"changed"`
	CellId                  null.Int    `db:"cell_id"`
	ExpireTimestampVerified bool        `db:"expire_timestamp_verified"`
	DisplayPokemonId        null.Int    `db:"display_pokemon_id"`
	SeenType                null.String `db:"seen_type"`
	Shiny                   null.Bool   `db:"shiny"`
	Username                null.String `db:"username"`
	Capture1                null.Float  `db:"capture_1"`
	Capture2                null.Float  `db:"capture_2"`
	Capture3                null.Float  `db:"capture_3"`
	Pvp                     null.String `db:"pvp"`
	IsEvent                 int8        `db:"is_event"`
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

func getPokemonRecord(db *sqlx.DB, encounterId string) (*Pokemon, error) {
	inMemoryPokemon := pokemonCache.Get(encounterId)
	if inMemoryPokemon != nil {
		pokemon := inMemoryPokemon.Value()
		return &pokemon, nil
	}
	pokemon := Pokemon{}

	err := db.Get(&pokemon,
		"SELECT id, pokemon_id, lat, lon, spawn_id, expire_timestamp, atk_iv, def_iv, sta_iv, move_1, move_2, "+
			"gender, form, cp, level, weather, costume, weight, size, capture_1, capture_2, capture_3, "+
			"display_pokemon_id, pokestop_id, updated, first_seen_timestamp, changed, cell_id, "+
			"expire_timestamp_verified, shiny, username, pvp, is_event, seen_type "+
			"FROM pokemon WHERE id = ?", encounterId)

	if err == sql.ErrNoRows {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	pokemonCache.Set(encounterId, pokemon, ttlcache.DefaultTTL)
	return &pokemon, nil
}

func hasChangesPokemon(old *Pokemon, new *Pokemon) bool {
	return !cmp.Equal(old, new, ignoreNearFloats)
}

func savePokemonRecord(db *sqlx.DB, pokemon *Pokemon) {
	oldPokemon, _ := getPokemonRecord(db, pokemon.Id)

	if oldPokemon != nil && !hasChangesPokemon(oldPokemon, pokemon) {
		return
	}

	if pokemon.FirstSeenTimestamp == 0 {
		pokemon.FirstSeenTimestamp = time.Now().Unix()
	}

	//log.Println(cmp.Diff(oldPokemon, pokemon))
	if oldPokemon == nil {
		res, err := db.NamedExec("INSERT INTO pokemon (id, pokemon_id, lat, lon, spawn_id, expire_timestamp, atk_iv, def_iv, sta_iv, move_1, move_2,"+
			"gender, form, cp, level, weather, costume, weight, size, capture_1, capture_2, capture_3,"+
			"display_pokemon_id, pokestop_id, updated, first_seen_timestamp, changed, cell_id,"+
			"expire_timestamp_verified, shiny, username, pvp, is_event, seen_type) "+
			"VALUES (:id, :pokemon_id, :lat, :lon, :spawn_id, :expire_timestamp, :atk_iv, :def_iv, :sta_iv, :move_1, :move_2,"+
			":gender, :form, :cp, :level, :weather, :costume, :weight, :size, :capture_1, :capture_2, :capture_3,"+
			":display_pokemon_id, :pokestop_id, :updated, :first_seen_timestamp, :changed, :cell_id,"+
			":expire_timestamp_verified, :shiny, :username, :pvp, :is_event, :seen_type)",
			pokemon)

		if err != nil {
			log.Errorf("insert pokemon: %s", err)
			return
		}

		_, _ = res, err
	} else {
		res, err := db.NamedExec("UPDATE pokemon SET "+
			"pokestop_id = :pokestop_id, "+
			"spawn_id = :spawn_id, "+
			"lat = :lat, "+
			"lon = :lon, "+
			"weight = :weight, "+
			"size = :size, "+
			"expire_timestamp = :expire_timestamp, "+
			"updated = :updated, "+
			"pokemon_id = :pokemon_id, "+
			"move_1 = :move_1, "+
			"move_2 = :move_2, "+
			"gender = :gender, "+
			"cp = :cp, "+
			"atk_iv = :atk_iv, "+
			"def_iv = :def_iv, "+
			"sta_iv = :sta_iv, "+
			"form = :form, "+
			"level = :level, "+
			"weather = :weather, "+
			"costume = :costume, "+
			"first_seen_timestamp = :first_seen_timestamp, "+
			"changed = :changed, "+
			"cell_id = :cell_id, "+
			"expire_timestamp_verified = :expire_timestamp_verified, "+
			"display_pokemon_id = :display_pokemon_id, "+
			"seen_type = :seen_type, "+
			"shiny = :shiny, "+
			"username = :username, "+
			"capture_1 = :capture_1, "+
			"capture_2 = :capture_2, "+
			"capture_3 = :capture_3, "+
			"pvp = :pvp, "+
			"is_event = :is_event "+
			"WHERE id = :id", pokemon,
		)
		if err != nil {
			log.Errorf("Update pokemon %s", err)
			return
		}
		_, _ = res, err
	}

	pokemonCache.Set(pokemon.Id, *pokemon, ttlcache.DefaultTTL)
	createPokemonWebhooks(oldPokemon, pokemon)
}

func createPokemonWebhooks(old *Pokemon, new *Pokemon) {
	//nullString := func (v null.Int) interface{} {
	//	if !v.Valid {
	//		return "null"
	//	}
	//	return v.ValueOrZero()
	//}

	if old == nil ||
		old.PokemonId != new.PokemonId ||
		old.Weather != new.Weather ||
		old.Cp != new.Cp {
		pokemonHook := map[string]interface{}{
			"spawnpoint_id": func() string {
				if !new.SpawnId.Valid {
					return "None"
				}
				return strconv.FormatInt(new.SpawnId.ValueOrZero(), 16)
			}(),
			"pokestop_id": func() string {
				if !new.PokestopId.Valid {
					return "None"
				} else {
					return new.PokestopId.ValueOrZero()
				}
			}(),
			"encounter_id":            new.Id,
			"pokemon_id":              new.PokemonId,
			"latitude":                new.Lat,
			"longitude":               new.Lon,
			"disappear_time":          new.ExpireTimestamp.ValueOrZero(),
			"disappear_time_verified": new.ExpireTimestampVerified,
			"first_seen":              new.FirstSeenTimestamp,
			"last_modified_time":      new.Updated,
			"gender":                  new.Gender,
			"cp":                      new.Cp,
			"form":                    new.Form,
			"costume":                 new.Costume,
			"individual_attack":       new.AtkIv,
			"individual_defense":      new.DefIv,
			"individual_stamina":      new.StaIv,
			"pokemon_level":           new.Level,
			"move_1":                  new.Move1,
			"move_2":                  new.Move2,
			"weight":                  new.Weight,
			"height":                  new.Size,
			"weather":                 new.Weather,
			"capture_1":               new.Capture1.ValueOrZero(),
			"capture_2":               new.Capture2.ValueOrZero(),
			"capture_3":               new.Capture3.ValueOrZero(),
			"shiny":                   new.Shiny,
			"username":                new.Username,
			"display_pokemon_id":      new.DisplayPokemonId,
			"is_event":                new.IsEvent,
			"seen_type":               new.SeenType,
			//"pvp":                     []string{},
		}

		webhooks.AddMessage(webhooks.Pokemon, pokemonHook)
	}
}

func (pokemon *Pokemon) updateFromWild(db *sqlx.DB, wildPokemon *pogo.WildPokemonProto, cellId int64, timestampMs int64, username string) {
	pokemon.IsEvent = 0
	pokemon.Id = strconv.FormatUint(wildPokemon.EncounterId, 10)

	oldWeather, oldPokemonId := pokemon.Weather, pokemon.PokemonId

	pokemon.PokemonId = int16(wildPokemon.Pokemon.PokemonId)
	pokemon.Lat = wildPokemon.Latitude
	pokemon.Lon = wildPokemon.Longitude
	spawnId, _ := strconv.ParseInt(wildPokemon.SpawnPointId, 16, 64)
	pokemon.SpawnId = null.IntFrom(spawnId)
	pokemon.Gender = null.IntFrom(int64(wildPokemon.Pokemon.PokemonDisplay.Gender))
	pokemon.Form = null.IntFrom(int64(wildPokemon.Pokemon.PokemonDisplay.Form))
	pokemon.Costume = null.IntFrom(int64(wildPokemon.Pokemon.PokemonDisplay.Costume))
	pokemon.Weather = null.IntFrom(int64(wildPokemon.Pokemon.PokemonDisplay.WeatherBoostedCondition))

	if pokemon.Username.Valid == false {
		// Don't be the reason that a pokemon gets updated
		pokemon.Username = null.StringFrom(username)
	}

	// Not sure I like the idea about an object updater loading another object

	pokemon.updateSpawnpointInfo(db, wildPokemon, spawnId, timestampMs)

	pokemon.SpawnId = null.IntFrom(spawnId)
	pokemon.CellId = null.IntFrom(cellId)

	if oldPokemonId != pokemon.PokemonId || oldWeather != pokemon.Weather {
		pokemon.SeenType = null.StringFrom(SeenType_Wild) // should be string value

		pokemon.clearEncounterDetails()
	}
}

func (pokemon *Pokemon) clearEncounterDetails() {
	pokemon.Cp = null.NewInt(0, false)
	pokemon.Move1 = null.NewInt(0, false)
	pokemon.Move2 = null.NewInt(0, false)
	pokemon.Size = null.NewFloat(0, false)
	pokemon.Weight = null.NewFloat(0, false)
	pokemon.AtkIv = null.NewInt(0, false)
	pokemon.DefIv = null.NewInt(0, false)
	pokemon.StaIv = null.NewInt(0, false)
}

func (pokemon *Pokemon) updateFromNearby(db *sqlx.DB, nearbyPokemon *pogo.NearbyPokemonProto, cellId int64, username string) {
	pokemon.IsEvent = 0
	pokemon.Id = strconv.FormatUint(nearbyPokemon.EncounterId, 10)

	oldWeather, oldPokemonId := pokemon.Weather, pokemon.PokemonId

	pokemon.PokemonId = int16(nearbyPokemon.PokedexNumber)
	pokemon.Weather = null.IntFrom(int64(nearbyPokemon.PokemonDisplay.WeatherBoostedCondition))
	pokemon.Gender = null.IntFrom(int64(nearbyPokemon.PokemonDisplay.Gender))
	pokemon.Form = null.IntFrom(int64(nearbyPokemon.PokemonDisplay.Form))
	pokemon.Costume = null.IntFrom(int64(nearbyPokemon.PokemonDisplay.Costume))

	if oldWeather == pokemon.Weather && oldPokemonId == pokemon.PokemonId {
		// No change of pokemon, do not downgrade to nearby
		return
	}
	pokemon.Username = null.StringFrom(username)

	pokestopId := nearbyPokemon.FortId

	if pokestopId == "" {
		// Cell Pokemon

		s2cell := s2.CellFromCellID(s2.CellID(cellId))
		pokemon.Lat = s2cell.CapBound().RectBound().Center().Lat.Degrees()
		pokemon.Lon = s2cell.CapBound().RectBound().Center().Lng.Degrees()

		pokemon.SeenType = null.StringFrom(SeenType_Cell)
	} else {
		pokestop, _ := getPokestopRecord(db, pokestopId)
		if pokestop == nil {
			// Unrecognised pokestop
			return
		}
		pokemon.PokestopId = null.StringFrom(pokestopId)
		pokemon.Lat = pokestop.Lat
		pokemon.Lon = pokestop.Lon
		pokemon.SeenType = null.StringFrom(SeenType_NearbyStop)
	}

	pokemon.CellId = null.IntFrom(cellId)
	pokemon.setUnknownTimestamp()
	pokemon.clearEncounterDetails()
}

var SeenType_Cell = "nearby_cell"
var SeenType_NearbyStop = "nearby_stop"
var SeenType_Wild string = "wild"
var SeenType_Encounter string = "encounter"

func (pokemon *Pokemon) updateSpawnpointInfo(db *sqlx.DB, wildPokemon *pogo.WildPokemonProto, spawnId int64, timestampMs int64) {
	if wildPokemon.TimeTillHiddenMs <= 90000 && wildPokemon.TimeTillHiddenMs > 0 {
		expireTimeStamp := (timestampMs + int64(wildPokemon.TimeTillHiddenMs)) / 1000
		pokemon.ExpireTimestamp = null.IntFrom(expireTimeStamp)
		pokemon.ExpireTimestampVerified = true

		date := time.Unix(expireTimeStamp, 0)
		secondOfHour := date.Second() + date.Minute()*60
		spawnpoint := Spawnpoint{
			Id:         spawnId,
			Lat:        pokemon.Lat,
			Lon:        pokemon.Lon,
			DespawnSec: null.IntFrom(int64(secondOfHour)),
		}
		spawnpointUpdate(db, &spawnpoint)
	} else {
		pokemon.ExpireTimestampVerified = false

		spawnPoint, _ := getSpawnpointRecord(db, spawnId)
		if spawnPoint != nil && spawnPoint.DespawnSec.Valid {
			despawnSecond := int(spawnPoint.DespawnSec.ValueOrZero())

			date := time.Unix(timestampMs/1000, 0)
			secondOfHour := date.Second() + date.Minute()*60

			despawnOffset := despawnSecond - secondOfHour
			if despawnOffset < 0 {
				despawnOffset += 3600
			}
			pokemon.ExpireTimestamp = null.IntFrom(int64(timestampMs)/1000 + int64(despawnOffset))
			pokemon.ExpireTimestampVerified = true
			spawnpointSeen(db, spawnId)
		} else {
			spawnpoint := Spawnpoint{
				Id:         spawnId,
				Lat:        pokemon.Lat,
				Lon:        pokemon.Lon,
				DespawnSec: null.NewInt(0, false),
			}
			spawnpointUpdate(db, &spawnpoint)
			pokemon.setUnknownTimestamp()
		}
	}
}

func (pokemon *Pokemon) setUnknownTimestamp() {
	now := time.Now().Unix()
	if !pokemon.ExpireTimestamp.Valid {
		pokemon.ExpireTimestamp = null.IntFrom(now + 20*60) // should be configurable, add on 20min
	} else {
		if pokemon.ExpireTimestamp.Int64 < now {
			pokemon.ExpireTimestamp = null.IntFrom(now + 10*60) // should be configurable, add on 10min
		}
	}
}

func (pokemon *Pokemon) updatePokemonFromEncounterProto(db *sqlx.DB, encounterData *pogo.EncounterOutProto) {
	oldCp, oldWeather, oldPokemonId := pokemon.Cp, pokemon.Weather, pokemon.PokemonId

	pokemon.IsEvent = 0
	pokemon.Id = strconv.FormatUint(encounterData.Pokemon.EncounterId, 10)
	pokemon.PokemonId = int16(encounterData.Pokemon.Pokemon.PokemonId)
	pokemon.Cp = null.IntFrom(int64(encounterData.Pokemon.Pokemon.Cp))
	pokemon.Move1 = null.IntFrom(int64(encounterData.Pokemon.Pokemon.Move1))
	pokemon.Move2 = null.IntFrom(int64(encounterData.Pokemon.Pokemon.Move2))
	pokemon.Size = null.FloatFrom(float64(encounterData.Pokemon.Pokemon.HeightM))
	pokemon.Weight = null.FloatFrom(float64(encounterData.Pokemon.Pokemon.WeightKg))
	pokemon.AtkIv = null.IntFrom(int64(encounterData.Pokemon.Pokemon.IndividualAttack))
	pokemon.DefIv = null.IntFrom(int64(encounterData.Pokemon.Pokemon.IndividualDefense))
	pokemon.StaIv = null.IntFrom(int64(encounterData.Pokemon.Pokemon.IndividualStamina))
	pokemon.Costume = null.IntFrom(int64(encounterData.Pokemon.Pokemon.PokemonDisplay.Costume))
	pokemon.Form = null.IntFrom(int64(encounterData.Pokemon.Pokemon.PokemonDisplay.Form))
	pokemon.Gender = null.IntFrom(int64(encounterData.Pokemon.Pokemon.PokemonDisplay.Gender))
	pokemon.Weather = null.IntFrom(int64(encounterData.Pokemon.Pokemon.PokemonDisplay.WeatherBoostedCondition))
	pokemon.Lat = encounterData.Pokemon.Latitude
	pokemon.Lon = encounterData.Pokemon.Longitude

	if encounterData.Pokemon.Pokemon.PokemonDisplay.Shiny {
		pokemon.Shiny = null.BoolFrom(true)
		pokemon.Username = null.StringFrom("AccountShiny")
	} else {
		if !pokemon.Shiny.Valid {
			pokemon.Shiny = null.BoolFrom(false)
		}
		if !pokemon.Username.Valid {
			pokemon.Username = null.StringFrom("Account")
		}
	}

	if encounterData.CaptureProbability != nil {
		pokemon.Capture1 = null.FloatFrom(float64(encounterData.CaptureProbability.CaptureProbability[0]))
		pokemon.Capture2 = null.FloatFrom(float64(encounterData.CaptureProbability.CaptureProbability[0]))
		pokemon.Capture3 = null.FloatFrom(float64(encounterData.CaptureProbability.CaptureProbability[0]))

		cpMultiplier := float64(encounterData.Pokemon.Pokemon.CpMultiplier)
		var level int64
		if cpMultiplier < 0.734 {
			level = int64(math.Round(58.35178527*cpMultiplier*cpMultiplier -
				2.838007664*cpMultiplier + 0.8539209906))
		} else {
			level = int64(math.Round(171.0112688*cpMultiplier - 95.20425243))
		}
		pokemon.Level = null.IntFrom(level)

		if oldCp != pokemon.Cp || oldPokemonId != pokemon.PokemonId || oldWeather != pokemon.Weather {
			//pokemon.isDitto = Pokemon.isDittoDisguised(
			//	id: self.id,
			//	pokemonId: pokemonId,
			//	level: level,
			//	weather: weather,
			//	atkIv: atkIv,
			//	defIv: defIv,
			//	staIv: staIv
			//)
			//if self.isDitto {
			//	self.setDittoAttributes(displayPokemonId: pokemonId,
			//		weather: weather, level: level)
			//}
			//setPVP()
		}
	}

	wildPokemon := encounterData.Pokemon

	spawnId, _ := strconv.ParseInt(wildPokemon.SpawnPointId, 16, 64)
	pokemon.SpawnId = null.IntFrom(spawnId)
	//timestampMs := time.Now().Unix() * 1000 // is there a better way to get this from the proto? This is how RDM does it
	//
	//pokemon.updateSpawnpointInfo(db, wildPokemon, spawnId, timestampMs)

	pokemon.SeenType = null.StringFrom(SeenType_Encounter) // should be const
}

func UpdatePokemonRecordWithEncounterProto(db *sqlx.DB, encounter *pogo.EncounterOutProto) string {
	if encounter.Pokemon == nil {
		return "No encounter"
	}
	pokemon, err := getPokemonRecord(db, strconv.FormatUint(encounter.Pokemon.EncounterId, 10))
	if err != nil {
		log.Printf("Finding pokemon: %s", err)
		return fmt.Sprintf("Error finding pokemon %s", err)
	}

	if pokemon == nil {
		pokemon = &Pokemon{}
	}
	pokemon.updatePokemonFromEncounterProto(db, encounter)
	savePokemonRecord(db, pokemon)

	return fmt.Sprintf("%d Pokemon %d CP%d", encounter.Pokemon.EncounterId, pokemon.PokemonId, encounter.Pokemon.Pokemon.Cp)
}
