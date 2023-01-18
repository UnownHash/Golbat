package decoder

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/golang/geo/s2"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
	"golbat/config"
	"golbat/db"
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

func getPokemonRecord(ctx context.Context, db db.DbDetails, encounterId string) (*Pokemon, error) {
	if db.UsePokemonCache {
		inMemoryPokemon := pokemonCache.Get(encounterId)
		if inMemoryPokemon != nil {
			pokemon := inMemoryPokemon.Value()
			return &pokemon, nil
		}
	}
	pokemon := Pokemon{}

	err := db.PokemonDb.GetContext(ctx, &pokemon,
		"SELECT id, pokemon_id, lat, lon, spawn_id, expire_timestamp, atk_iv, def_iv, sta_iv, move_1, move_2, "+
			"gender, form, cp, level, weather, costume, weight, height, size, capture_1, capture_2, capture_3, "+
			"display_pokemon_id, pokestop_id, updated, first_seen_timestamp, changed, cell_id, "+
			"expire_timestamp_verified, shiny, username, pvp, is_event, seen_type "+
			"FROM pokemon WHERE id = ?", encounterId)

	if err == sql.ErrNoRows {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	if db.UsePokemonCache {
		pokemonCache.Set(encounterId, pokemon, ttlcache.DefaultTTL)
	}
	return &pokemon, nil
}

func hasChangesPokemon(old *Pokemon, new *Pokemon) bool {
	return !cmp.Equal(old, new, cmp.Options{
		ignoreNearFloats,
		cmpopts.IgnoreFields(Pokemon{}, "Pvp"),
	})
}

func savePokemonRecord(ctx context.Context, db db.DbDetails, pokemon *Pokemon) {
	oldPokemon, _ := getPokemonRecord(ctx, db, pokemon.Id)

	if oldPokemon != nil && !hasChangesPokemon(oldPokemon, pokemon) {
		return
	}

	now := time.Now().Unix()
	if pokemon.FirstSeenTimestamp == 0 {
		pokemon.FirstSeenTimestamp = now
	}

	pokemon.Updated = null.IntFrom(now)
	if oldPokemon == nil || oldPokemon.PokemonId != pokemon.PokemonId || oldPokemon.Cp != pokemon.Cp {
		pokemon.Changed = now
	}

	changePvpField := false
	if ohbem != nil {
		// Calculating PVP data
		if pokemon.AtkIv.Valid && (oldPokemon == nil || oldPokemon.PokemonId != pokemon.PokemonId || oldPokemon.Cp != pokemon.Cp || oldPokemon.Form != pokemon.Form || oldPokemon.Costume != pokemon.Costume) {
			pvp, err := ohbem.QueryPvPRank(int(pokemon.PokemonId),
				int(pokemon.Form.ValueOrZero()),
				int(pokemon.Costume.ValueOrZero()),
				int(pokemon.Gender.ValueOrZero()),
				int(pokemon.AtkIv.ValueOrZero()),
				int(pokemon.DefIv.ValueOrZero()),
				int(pokemon.StaIv.ValueOrZero()),
				float64(pokemon.Level.ValueOrZero()))

			if err == nil {
				pvpBytes, _ := json.Marshal(pvp)
				pokemon.Pvp = null.StringFrom(string(pvpBytes))
				changePvpField = true
			}
		}
		if !pokemon.AtkIv.Valid && (oldPokemon == nil || oldPokemon.AtkIv.Valid) {
			pokemon.Pvp = null.NewString("", false)
			changePvpField = true
		}
	}

	var oldSeenType string
	if oldPokemon == nil {
		oldSeenType = "n/a"
	} else {
		oldSeenType = oldPokemon.SeenType.ValueOrZero()
	}
	log.Debugf("Updating pokemon [%s] from %s->%s", pokemon.Id, oldSeenType, pokemon.SeenType.ValueOrZero())
	//log.Println(cmp.Diff(oldPokemon, pokemon))
	if oldPokemon == nil {
		pvpField, pvpValue := "", ""
		if changePvpField {
			pvpField, pvpValue = "pvp, ", ":pvp, "
		}
		res, err := db.PokemonDb.NamedExecContext(ctx, fmt.Sprintf("INSERT INTO pokemon (id, pokemon_id, lat, lon, spawn_id, expire_timestamp, atk_iv, def_iv, sta_iv, move_1, move_2,"+
			"gender, form, cp, level, weather, costume, weight, height, size, capture_1, capture_2, capture_3,"+
			"display_pokemon_id, pokestop_id, updated, first_seen_timestamp, changed, cell_id,"+
			"expire_timestamp_verified, shiny, username, %s is_event, seen_type) "+
			"VALUES (:id, :pokemon_id, :lat, :lon, :spawn_id, :expire_timestamp, :atk_iv, :def_iv, :sta_iv, :move_1, :move_2,"+
			":gender, :form, :cp, :level, :weather, :costume, :weight, :height, :size, :capture_1, :capture_2, :capture_3,"+
			":display_pokemon_id, :pokestop_id, :updated, :first_seen_timestamp, :changed, :cell_id,"+
			":expire_timestamp_verified, :shiny, :username, %s :is_event, :seen_type)", pvpField, pvpValue),
			pokemon)

		if err != nil {
			log.Errorf("insert pokemon: [%s] %s", pokemon.Id, err)
			log.Errorf("Full structure: %+v", pokemon)
			return
		}

		_, _ = res, err
	} else {
		pvpUpdate := ""
		if changePvpField {
			pvpUpdate = "pvp = :pvp, "
		}
		res, err := db.PokemonDb.NamedExecContext(ctx, fmt.Sprintf("UPDATE pokemon SET "+
			"pokestop_id = :pokestop_id, "+
			"spawn_id = :spawn_id, "+
			"lat = :lat, "+
			"lon = :lon, "+
			"weight = :weight, "+
			"height = :height, "+
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
			"%s"+
			"is_event = :is_event "+
			"WHERE id = :id", pvpUpdate), pokemon,
		)
		if err != nil {
			log.Errorf("Update pokemon [%s] %s", pokemon.Id, err)
			log.Errorf("Full structure: %+v", pokemon)
			return
		}
		rows, rowsErr := res.RowsAffected()
		log.Debugf("Updating pokemon [%s] after update res = %d %v", pokemon.Id, rows, rowsErr)

		_, _ = res, err
	}

	createPokemonWebhooks(oldPokemon, pokemon)
	updatePokemonStats(oldPokemon, pokemon)
	updatePokemonNests(oldPokemon, pokemon)

	pokemon.Pvp = null.NewString("", false) // Reset PVP field to avoid keeping it in memory cache

	if db.UsePokemonCache {
		remaining := ttlcache.DefaultTTL
		if pokemon.ExpireTimestampVerified {
			timeLeft := 60 + pokemon.ExpireTimestamp.ValueOrZero() - time.Now().Unix()
			if timeLeft > 1 {
				remaining = time.Duration(timeLeft) * time.Second
			}
		}
		pokemonCache.Set(pokemon.Id, *pokemon, remaining)
	}
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
			"size":                    new.Size,
			"height":                  new.Height,
			"weather":                 new.Weather,
			"capture_1":               new.Capture1.ValueOrZero(),
			"capture_2":               new.Capture2.ValueOrZero(),
			"capture_3":               new.Capture3.ValueOrZero(),
			"shiny":                   new.Shiny,
			"username":                new.Username,
			"display_pokemon_id":      new.DisplayPokemonId,
			"is_event":                new.IsEvent,
			"seen_type":               new.SeenType,
			"pvp": func() interface{} {
				if !new.Pvp.Valid {
					return nil
				}
				var j map[string]interface{}
				if err := json.Unmarshal([]byte(new.Pvp.ValueOrZero()), &j); err == nil {
					return j
				} else {
					return nil
				}
			}(),
		}

		webhooks.AddMessage(webhooks.Pokemon, pokemonHook)
	}
}

func (pokemon *Pokemon) updateFromWild(ctx context.Context, db db.DbDetails, wildPokemon *pogo.WildPokemonProto, cellId int64, timestampMs int64, username string) {
	pokemon.IsEvent = 0
	encounterId := strconv.FormatUint(wildPokemon.EncounterId, 10)
	oldWeather, oldPokemonId := pokemon.Weather, pokemon.PokemonId

	if pokemon.Id == "" || pokemon.SeenType.ValueOrZero() == SeenType_Cell || pokemon.SeenType.ValueOrZero() == SeenType_NearbyStop {
		updateStats(ctx, db, encounterId, stats_seenWild)
	}
	pokemon.Id = encounterId

	pokemon.PokemonId = int16(wildPokemon.Pokemon.PokemonId)
	pokemon.Lat = wildPokemon.Latitude
	pokemon.Lon = wildPokemon.Longitude
	spawnId, _ := strconv.ParseInt(wildPokemon.SpawnPointId, 16, 64)
	pokemon.Gender = null.IntFrom(int64(wildPokemon.Pokemon.PokemonDisplay.Gender))
	pokemon.Form = null.IntFrom(int64(wildPokemon.Pokemon.PokemonDisplay.Form))
	pokemon.Costume = null.IntFrom(int64(wildPokemon.Pokemon.PokemonDisplay.Costume))
	pokemon.Weather = null.IntFrom(int64(wildPokemon.Pokemon.PokemonDisplay.WeatherBoostedCondition))

	if pokemon.Username.Valid == false {
		// Don't be the reason that a pokemon gets updated
		pokemon.Username = null.StringFrom(username)
	}

	// Not sure I like the idea about an object updater loading another object

	pokemon.updateSpawnpointInfo(ctx, db, wildPokemon, spawnId, timestampMs, true)

	pokemon.SpawnId = null.IntFrom(spawnId)
	pokemon.CellId = null.IntFrom(cellId)

	if oldPokemonId != 0 && (oldPokemonId != pokemon.PokemonId || oldWeather != pokemon.Weather) {
		if oldWeather.Valid && oldPokemonId != 0 {
			log.Infof("Pokemon [%s] was seen-type %s, id %d, weather %d will be changed to wild id %d weather %d",
				pokemon.Id, pokemon.SeenType.ValueOrZero(), oldPokemonId, oldWeather.ValueOrZero(), pokemon.PokemonId, pokemon.Weather.ValueOrZero())
		}
		pokemon.SeenType = null.StringFrom(SeenType_Wild)

		pokemon.clearEncounterDetails()
		updateStats(ctx, db, encounterId, stats_statsReset)

	} else if pokemon.SeenType.ValueOrZero() != SeenType_Encounter {
		pokemon.SeenType = null.StringFrom(SeenType_Wild) // should be string value
	}
}

func (pokemon *Pokemon) updateFromMap(ctx context.Context, db db.DbDetails, mapPokemon *pogo.MapPokemonProto, cellId int64, username string) {

	if pokemon.Id != "" {
		// Do not ever overwrite lure details based on seeing it again in the GMO
		return
	}

	pokemon.IsEvent = 0

	encounterId := strconv.FormatUint(mapPokemon.EncounterId, 10)
	pokemon.Id = encounterId
	updateStats(ctx, db, encounterId, stats_seenLure)

	spawnpointId := mapPokemon.SpawnpointId

	pokestop, _ := getPokestopRecord(ctx, db, spawnpointId)
	if pokestop == nil {
		// Unrecognised pokestop
		return
	}
	pokemon.PokestopId = null.StringFrom(pokestop.Id)
	pokemon.PokemonId = int16(mapPokemon.PokedexTypeId)
	pokemon.Lat = pokestop.Lat
	pokemon.Lon = pokestop.Lon
	pokemon.SeenType = null.StringFrom(SeenType_LureWild) // may have been encounter... this needs fixing

	if mapPokemon.PokemonDisplay != nil {
		pokemon.Gender = null.IntFrom(int64(mapPokemon.PokemonDisplay.Gender))
		pokemon.Form = null.IntFrom(int64(mapPokemon.PokemonDisplay.Form))
		pokemon.Costume = null.IntFrom(int64(mapPokemon.PokemonDisplay.Costume))
		pokemon.Weather = null.IntFrom(int64(mapPokemon.PokemonDisplay.WeatherBoostedCondition))
		// The mapPokemon and nearbyPokemon GMOs don't contain actual shininess.
		// shiny = mapPokemon.pokemonDisplay.shiny
	}
	if !pokemon.Username.Valid {
		pokemon.Username = null.StringFrom(username)
	}

	if mapPokemon.ExpirationTimeMs > 0 {
		pokemon.ExpireTimestamp = null.IntFrom(mapPokemon.ExpirationTimeMs / 1000)
		pokemon.ExpireTimestampVerified = true
	} else {
		pokemon.ExpireTimestampVerified = false
	}

	pokemon.CellId = null.IntFrom(cellId)
}

func (pokemon *Pokemon) clearEncounterDetails() {
	pokemon.Cp = null.NewInt(0, false)
	pokemon.Move1 = null.NewInt(0, false)
	pokemon.Move2 = null.NewInt(0, false)
	pokemon.Height = null.NewFloat(0, false)
	pokemon.Size = null.NewInt(0, false)
	pokemon.Weight = null.NewFloat(0, false)
	pokemon.AtkIv = null.NewInt(0, false)
	pokemon.DefIv = null.NewInt(0, false)
	pokemon.StaIv = null.NewInt(0, false)
	pokemon.Shiny = null.NewBool(false, false)
}

func (pokemon *Pokemon) updateFromNearby(ctx context.Context, db db.DbDetails, nearbyPokemon *pogo.NearbyPokemonProto, cellId int64, username string) {
	pokemon.IsEvent = 0
	encounterId := strconv.FormatUint(nearbyPokemon.EncounterId, 10)
	pokestopId := nearbyPokemon.FortId

	if pokemon.Id == "" {
		if pokestopId == "" {
			updateStats(ctx, db, encounterId, stats_seenCell)
		} else {
			updateStats(ctx, db, encounterId, stats_seenStop)
		}
	}

	pokemon.Id = encounterId

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

	if oldWeather.Valid && oldPokemonId != 0 {
		log.Infof("Pokemon [%s] was seen-type %s, id %d, weather %d will be changed to nearby-cell id %d weather %d",
			pokemon.Id, pokemon.SeenType.ValueOrZero(), oldPokemonId, oldWeather.ValueOrZero(), pokemon.PokemonId, pokemon.Weather.ValueOrZero())

		if pokemon.SeenType.ValueOrZero() == SeenType_Wild {
			return
		}

		if pokemon.SeenType.ValueOrZero() == SeenType_Encounter {
			// clear encounter details and finish making changes - lat, lon is preserved
			pokemon.SeenType = null.StringFrom(SeenType_Wild)

			updateStats(ctx, db, encounterId, stats_statsReset)
			pokemon.clearEncounterDetails()

			return
		}
	}

	if pokestopId == "" {
		// Cell Pokemon

		s2cell := s2.CellFromCellID(s2.CellID(cellId))
		pokemon.Lat = s2cell.CapBound().RectBound().Center().Lat.Degrees()
		pokemon.Lon = s2cell.CapBound().RectBound().Center().Lng.Degrees()

		pokemon.SeenType = null.StringFrom(SeenType_Cell)
	} else {
		pokestop, _ := getPokestopRecord(ctx, db, pokestopId)
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

const SeenType_Cell string = "nearby_cell"             // Pokemon was seen in a cell (without accurate location)
const SeenType_NearbyStop string = "nearby_stop"       // Pokemon was seen at a nearby Pokestop, location set to lon, lat of pokestop
const SeenType_Wild string = "wild"                    // Pokemon was seen in the wild, accurate location but with no IV details
const SeenType_Encounter string = "encounter"          // Pokemon has been encountered giving exact details of current IV
const SeenType_LureWild string = "lure_wild"           // Pokemon was seen at a lure
const SeenType_LureEncounter string = "lure_encounter" // Pokemon has been encountered at a lure

// updateSpawnpointInfo sets the current Pokemon object ExpireTimeStamp, and ExpireTimeStampVerified from the Spawnpoint
// information held.
// db - the database connection to be used
// wildPokemon - the Pogo Proto to be decoded
// spawnId - the spawn id the Pokemon was seen at
// timestampMs - the timestamp to be used for calculations
// timestampAccurate - whether the timestamp is considered accurate (eg came from a GMO), and so can be used to create
// a new exact spawnpoint record
func (pokemon *Pokemon) updateSpawnpointInfo(ctx context.Context, db db.DbDetails, wildPokemon *pogo.WildPokemonProto, spawnId int64, timestampMs int64, timestampAccurate bool) {
	if wildPokemon.TimeTillHiddenMs <= 90000 && wildPokemon.TimeTillHiddenMs > 0 {
		expireTimeStamp := (timestampMs + int64(wildPokemon.TimeTillHiddenMs)) / 1000
		pokemon.ExpireTimestamp = null.IntFrom(expireTimeStamp)
		pokemon.ExpireTimestampVerified = true

		if timestampAccurate {
			date := time.Unix(expireTimeStamp, 0)
			secondOfHour := date.Second() + date.Minute()*60
			spawnpoint := Spawnpoint{
				Id:         spawnId,
				Lat:        pokemon.Lat,
				Lon:        pokemon.Lon,
				DespawnSec: null.IntFrom(int64(secondOfHour)),
			}
			spawnpointUpdate(ctx, db, &spawnpoint)
		}
	} else {
		pokemon.ExpireTimestampVerified = false

		spawnPoint, _ := getSpawnpointRecord(ctx, db, spawnId)
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
			spawnpointSeen(ctx, db, spawnId)
		} else {
			spawnpoint := Spawnpoint{
				Id:         spawnId,
				Lat:        pokemon.Lat,
				Lon:        pokemon.Lon,
				DespawnSec: null.NewInt(0, false),
			}
			spawnpointUpdate(ctx, db, &spawnpoint)
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

func (pokemon *Pokemon) updatePokemonFromEncounterProto(ctx context.Context, db db.DbDetails, encounterData *pogo.EncounterOutProto) {
	oldCp, oldWeather, oldPokemonId := pokemon.Cp, pokemon.Weather, pokemon.PokemonId

	pokemon.IsEvent = 0
	pokemon.Id = strconv.FormatUint(encounterData.Pokemon.EncounterId, 10)
	pokemon.PokemonId = int16(encounterData.Pokemon.Pokemon.PokemonId)
	pokemon.Cp = null.IntFrom(int64(encounterData.Pokemon.Pokemon.Cp))
	pokemon.Move1 = null.IntFrom(int64(encounterData.Pokemon.Pokemon.Move1))
	pokemon.Move2 = null.IntFrom(int64(encounterData.Pokemon.Pokemon.Move2))
	pokemon.Height = null.FloatFrom(float64(encounterData.Pokemon.Pokemon.HeightM))
	pokemon.Size = null.IntFrom(int64(encounterData.Pokemon.Pokemon.Size))
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

	if pokemon.CellId.Valid == false {
		centerCoord := s2.LatLngFromDegrees(pokemon.Lat, pokemon.Lon)
		cellID := s2.CellIDFromLatLng(centerCoord).Parent(15)
		pokemon.CellId = null.IntFrom(int64(cellID))
	}

	pokemon.Shiny = null.BoolFrom(encounterData.Pokemon.Pokemon.PokemonDisplay.Shiny)
	pokemon.Username = null.StringFrom("Account")

	if encounterData.CaptureProbability != nil {
		pokemon.Capture1 = null.FloatFrom(float64(encounterData.CaptureProbability.CaptureProbability[0]))
		pokemon.Capture2 = null.FloatFrom(float64(encounterData.CaptureProbability.CaptureProbability[1]))
		pokemon.Capture3 = null.FloatFrom(float64(encounterData.CaptureProbability.CaptureProbability[2]))

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
			if int(pokemon.PokemonId) != Ditto && pokemon.isDittoDisguised() {
				pokemon.setDittoAttributes()
			}
		}
	}

	wildPokemon := encounterData.Pokemon

	spawnId, _ := strconv.ParseInt(wildPokemon.SpawnPointId, 16, 64)
	pokemon.SpawnId = null.IntFrom(spawnId)
	timestampMs := time.Now().Unix() * 1000 // is there a better way to get this from the proto? This is how RDM does it

	pokemon.updateSpawnpointInfo(ctx, db, wildPokemon, spawnId, timestampMs, false)

	pokemon.SeenType = null.StringFrom(SeenType_Encounter)
	updateStats(ctx, db, pokemon.Id, stats_encounter)
}

func (pokemon *Pokemon) updatePokemonFromDiskEncounterProto(ctx context.Context, db db.DbDetails, encounterData *pogo.DiskEncounterOutProto) {
	oldCp, oldWeather, oldPokemonId := pokemon.Cp, pokemon.Weather, pokemon.PokemonId

	pokemon.IsEvent = 0

	//pokemon.Id = strconv.FormatUint(encounterData.EncounterId, 10)
	pokemon.PokemonId = int16(encounterData.Pokemon.PokemonId)
	pokemon.Cp = null.IntFrom(int64(encounterData.Pokemon.Cp))
	pokemon.Move1 = null.IntFrom(int64(encounterData.Pokemon.Move1))
	pokemon.Move2 = null.IntFrom(int64(encounterData.Pokemon.Move2))
	pokemon.Height = null.FloatFrom(float64(encounterData.Pokemon.HeightM))
	pokemon.Size = null.IntFrom(int64(encounterData.Pokemon.Size))
	pokemon.Weight = null.FloatFrom(float64(encounterData.Pokemon.WeightKg))
	pokemon.AtkIv = null.IntFrom(int64(encounterData.Pokemon.IndividualAttack))
	pokemon.DefIv = null.IntFrom(int64(encounterData.Pokemon.IndividualDefense))
	pokemon.StaIv = null.IntFrom(int64(encounterData.Pokemon.IndividualStamina))
	pokemon.Costume = null.IntFrom(int64(encounterData.Pokemon.PokemonDisplay.Costume))
	pokemon.Form = null.IntFrom(int64(encounterData.Pokemon.PokemonDisplay.Form))
	pokemon.Gender = null.IntFrom(int64(encounterData.Pokemon.PokemonDisplay.Gender))
	pokemon.Weather = null.IntFrom(int64(encounterData.Pokemon.PokemonDisplay.WeatherBoostedCondition))

	if encounterData.Pokemon.PokemonDisplay.Shiny {
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

		cpMultiplier := float64(encounterData.Pokemon.CpMultiplier)
		var level int64
		if cpMultiplier < 0.734 {
			level = int64(math.Round(58.35178527*cpMultiplier*cpMultiplier -
				2.838007664*cpMultiplier + 0.8539209906))
		} else {
			level = int64(math.Round(171.0112688*cpMultiplier - 95.20425243))
		}
		pokemon.Level = null.IntFrom(level)

		if oldCp != pokemon.Cp || oldPokemonId != pokemon.PokemonId || oldWeather != pokemon.Weather {
			if int(pokemon.PokemonId) != Ditto && pokemon.isDittoDisguised() {
				pokemon.setDittoAttributes()
			}
		}
	}

	pokemon.SeenType = null.StringFrom(SeenType_LureEncounter)
	updateStats(ctx, db, pokemon.Id, stats_lureEncounter)
}

func (pokemon *Pokemon) setDittoAttributes() {
	var moveTransformFast int64 = 242
	var moveStruggle int64 = 133
	pokemon.DisplayPokemonId = null.IntFrom(int64(pokemon.PokemonId))
	pokemon.PokemonId = int16(Ditto)
	pokemon.Form = null.IntFrom(0)
	pokemon.Move1 = null.IntFrom(moveTransformFast)
	pokemon.Move2 = null.IntFrom(moveStruggle)
	pokemon.Gender = null.IntFrom(3)
	pokemon.Costume = null.IntFrom(0)
	pokemon.Height = null.NewFloat(0, false)
	pokemon.Size = null.NewInt(0, false)
	pokemon.Weight = null.NewFloat(0, false)
}

var Ditto = 132
var weatherBoostMinLevel = 6
var weatherBoostMinIvStat = 4

func (pokemon *Pokemon) isDittoDisguised() bool {
	if int(pokemon.PokemonId) == Ditto {
		return true
	}
	level := int(pokemon.Level.ValueOrZero())
	atkIv := int(pokemon.AtkIv.ValueOrZero())
	defIv := int(pokemon.DefIv.ValueOrZero())
	staIv := int(pokemon.StaIv.ValueOrZero())

	isUnderLevelBoosted := level > 0 && level < weatherBoostMinLevel
	isUnderIvStatBoosted := level > 0 &&
		(atkIv < weatherBoostMinIvStat ||
			defIv < weatherBoostMinIvStat ||
			staIv < weatherBoostMinIvStat)

	isWeatherBoosted := pokemon.Weather.ValueOrZero() > 0
	isOverLevel := level > 30

	if isWeatherBoosted {
		if isUnderLevelBoosted || isUnderIvStatBoosted {
			log.Debugf("[POKEMON] Pokemon [%s] Ditto found, disguised as %d", pokemon.Id, pokemon.PokemonId)
			return true
		}
	} else {
		if isOverLevel {
			log.Debugf("[POKEMON] Pokemon [%s] Ditto found, disguised as %d", pokemon.Id, pokemon.PokemonId)
			return true
		}
	}
	return false
}

func UpdatePokemonRecordWithEncounterProto(ctx context.Context, db db.DbDetails, encounter *pogo.EncounterOutProto) string {

	if encounter.Pokemon == nil {
		return "No encounter"
	}

	encounterId := strconv.FormatUint(encounter.Pokemon.EncounterId, 10)

	pokemonMutex, _ := pokemonStripedMutex.GetLock(encounterId)
	pokemonMutex.Lock()
	defer pokemonMutex.Unlock()

	pokemon, err := getPokemonRecord(ctx, db, encounterId)
	if err != nil {
		log.Errorf("Error pokemon [%s]: %s", encounterId, err)
		return fmt.Sprintf("Error finding pokemon %s", err)
	}

	if pokemon == nil {
		pokemon = &Pokemon{}
	}
	pokemon.updatePokemonFromEncounterProto(ctx, db, encounter)
	savePokemonRecord(ctx, db, pokemon)

	return fmt.Sprintf("%d %s Pokemon %d CP%d", encounter.Pokemon.EncounterId, encounterId, pokemon.PokemonId, encounter.Pokemon.Pokemon.Cp)
}

func UpdatePokemonRecordWithDiskEncounterProto(ctx context.Context, db db.DbDetails, encounter *pogo.DiskEncounterOutProto) string {
	if encounter.Pokemon == nil {
		return "No encounter"
	}

	encounterId := strconv.FormatUint(uint64(encounter.Pokemon.PokemonDisplay.DisplayId), 10)

	pokemonMutex, _ := pokemonStripedMutex.GetLock(encounterId)
	pokemonMutex.Lock()
	defer pokemonMutex.Unlock()

	pokemon, err := getPokemonRecord(ctx, db, encounterId)
	if err != nil {
		log.Errorf("Error pokemon [%s]: %s", encounterId, err)
		return fmt.Sprintf("Error finding pokemon %s", err)
	}

	if pokemon == nil {
		// No pokemon found
		diskEncounterCache.Set(encounterId, encounter, ttlcache.DefaultTTL)
		return fmt.Sprintf("%s Disk encounter without previous GMO - Pokemon stored for later")
	}
	pokemon.updatePokemonFromDiskEncounterProto(ctx, db, encounter)
	savePokemonRecord(ctx, db, pokemon)

	return fmt.Sprintf("%s Disk Pokemon %d CP%d", encounterId, pokemon.PokemonId, encounter.Pokemon.Cp)
}

const stats_seenWild string = "seen_wild"
const stats_seenStop string = "seen_stop"
const stats_seenCell string = "seen_cell"
const stats_statsReset string = "stats_reset"
const stats_encounter string = "encounter"
const stats_seenLure string = "seen_lure"
const stats_lureEncounter string = "lure_encounter"

func updateStats(ctx context.Context, db db.DbDetails, id string, event string) {
	if config.Config.Stats == false {
		return
	}

	var sqlCommand string

	if event == stats_encounter {
		sqlCommand = "INSERT INTO pokemon_timing (id, seen_wild, first_encounter, last_encounter) " +
			"VALUES (?, UNIX_TIMESTAMP(), UNIX_TIMESTAMP(), UNIX_TIMESTAMP()) " +
			"ON DUPLICATE KEY UPDATE " +
			"first_encounter = COALESCE(first_encounter, VALUES(first_encounter))," +
			"last_encounter = VALUES(last_encounter)," +
			"seen_wild = COALESCE(seen_wild, first_encounter)"
	} else {
		sqlCommand = fmt.Sprintf("INSERT INTO pokemon_timing (id, %[1]s)"+
			"VALUES (?, UNIX_TIMESTAMP())"+
			"ON DUPLICATE KEY UPDATE "+
			"%[1]s=COALESCE(%[1]s, VALUES(%[1]s))", event)
	}

	log.Debugf("Updating pokemon timing: [%s] %s", id, event)
	_, err := db.GeneralDb.ExecContext(ctx, sqlCommand, id)

	if err != nil {
		log.Errorf("update pokemon timing: [%s] %s", id, err)
		return
	}
}
