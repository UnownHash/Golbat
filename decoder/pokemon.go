package decoder

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/UnownHash/gohbem"
	"github.com/golang/geo/s2"
	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
	"golbat/config"
	"golbat/db"
	"golbat/geo"
	"golbat/pogo"
	"golbat/webhooks"
	"gopkg.in/guregu/null.v4"
	"math"
	"strconv"
	"time"
)

// Pokemon struct.
// REMINDER! Keep hasChangesPokemon updated after making changes
//
// AtkIv/DefIv/StaIv: Should not be set directly. Use calculateIv
//
// IvInactive: This field is used for storing weather-boosted IV when spawn is not boosted,
// or non-boosted IV when spawn is boosted.
// Since it should not be used for indexing, it stores atk in the lowest 4 bits, and then def and sta.
//
// EncounterWeather: weather when encountered, not last seen weather. Used for advanced Ditto detection.
//
// FirstSeenTimestamp: This field is used in IsNewRecord. It should only be set in savePokemonRecord.
type Pokemon struct {
	Id                      string      `db:"id" json:"id"`
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
	IvInactive              null.Int    `db:"iv_inactive" json:"iv_inactive"`
	Iv                      null.Float  `db:"iv" json:"iv"`
	Form                    null.Int    `db:"form" json:"form"`
	Level                   null.Int    `db:"level" json:"level"`
	EncounterWeather        uint8       `db:"encounter_weather" json:"encounter_weather"`
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
}

const EncounterWeather_Invalid uint8 = 0xFF                  // invalid/unscanned
const EncounterWeather_Rerolled uint8 = 0x80                 // flag for marking that the spawn rerolled since last scan
const EncounterWeather_UnboostedNotPartlyCloudy uint8 = 0x40 // flag for marking that it was not partly cloudy when scanned and the spawn was not boosted

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
	if config.Config.PokemonMemoryOnly {
		return nil, nil
	}
	pokemon := Pokemon{}

	err := db.PokemonDb.GetContext(ctx, &pokemon,
		"SELECT id, pokemon_id, lat, lon, spawn_id, expire_timestamp, atk_iv, def_iv, sta_iv, iv_inactive, iv, "+
			"move_1, move_2, gender, form, cp, level, encounter_weather, weather, costume, weight, height, size, "+
			"display_pokemon_id, is_ditto, pokestop_id, updated, first_seen_timestamp, changed, cell_id, "+
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
	pokemonRtreeUpdatePokemonOnGet(&pokemon)
	return &pokemon, nil
}

func getOrCreatePokemonRecord(ctx context.Context, db db.DbDetails, encounterId string) (*Pokemon, error) {
	pokemon, err := getPokemonRecord(ctx, db, encounterId)
	if pokemon != nil || err != nil {
		return pokemon, err
	}
	pokemon = &Pokemon{Id: encounterId, EncounterWeather: EncounterWeather_Invalid}
	if db.UsePokemonCache {
		pokemonCache.Set(encounterId, *pokemon, ttlcache.DefaultTTL)
	}
	return pokemon, nil
}

// hasChangesPokemon compares two Pokemon structs
// Ignored: Username, Iv, Pvp
// Float tolerance: Lat, Lon
// Null Float tolerance: Weight, Height, Capture1, Capture2, Capture3
func hasChangesPokemon(old *Pokemon, new *Pokemon) bool {
	return old.Id != new.Id ||
		old.PokestopId != new.PokestopId ||
		old.SpawnId != new.SpawnId ||
		old.Size != new.Size ||
		old.ExpireTimestamp != new.ExpireTimestamp ||
		old.Updated != new.Updated ||
		old.PokemonId != new.PokemonId ||
		old.Move1 != new.Move1 ||
		old.Move2 != new.Move2 ||
		old.Gender != new.Gender ||
		old.Cp != new.Cp ||
		old.AtkIv != new.AtkIv ||
		old.DefIv != new.DefIv ||
		old.StaIv != new.StaIv ||
		old.IvInactive != new.IvInactive ||
		old.Form != new.Form ||
		old.Level != new.Level ||
		old.EncounterWeather != new.EncounterWeather ||
		old.Weather != new.Weather ||
		old.Costume != new.Costume ||
		old.FirstSeenTimestamp != new.FirstSeenTimestamp ||
		old.Changed != new.Changed ||
		old.CellId != new.CellId ||
		old.ExpireTimestampVerified != new.ExpireTimestampVerified ||
		old.DisplayPokemonId != new.DisplayPokemonId ||
		old.IsDitto != new.IsDitto ||
		old.SeenType != new.SeenType ||
		old.Shiny != new.Shiny ||
		old.IsEvent != new.IsEvent ||
		!floatAlmostEqual(old.Lat, new.Lat, floatTolerance) ||
		!floatAlmostEqual(old.Lon, new.Lon, floatTolerance) ||
		!nullFloatAlmostEqual(old.Weight, new.Weight, floatTolerance) ||
		!nullFloatAlmostEqual(old.Height, new.Height, floatTolerance) ||
		!nullFloatAlmostEqual(old.Capture1, new.Capture1, floatTolerance) ||
		!nullFloatAlmostEqual(old.Capture2, new.Capture2, floatTolerance) ||
		!nullFloatAlmostEqual(old.Capture3, new.Capture3, floatTolerance)
}

func savePokemonRecord(ctx context.Context, db db.DbDetails, pokemon *Pokemon) {
	savePokemonRecordAsAtTime(ctx, db, pokemon, time.Now().Unix())
}

func savePokemonRecordAsAtTime(ctx context.Context, db db.DbDetails, pokemon *Pokemon, now int64) {
	oldPokemon, _ := getPokemonRecord(ctx, db, pokemon.Id)

	if oldPokemon != nil && !hasChangesPokemon(oldPokemon, pokemon) {
		return
	}

	// Blank, non-persisted record are now inserted into the cache to save on DB calls
	if oldPokemon != nil && oldPokemon.isNewRecord() {
		oldPokemon = nil
	}

	// uncomment to debug excessive writes
	//if oldPokemon != nil && oldPokemon.AtkIv == pokemon.AtkIv && oldPokemon.DefIv == pokemon.DefIv && oldPokemon.StaIv == pokemon.StaIv && oldPokemon.Level == pokemon.Level && oldPokemon.ExpireTimestampVerified == pokemon.ExpireTimestampVerified && oldPokemon.PokemonId == pokemon.PokemonId && oldPokemon.ExpireTimestamp == pokemon.ExpireTimestamp && oldPokemon.PokestopId == pokemon.PokestopId && math.Abs(pokemon.Lat-oldPokemon.Lat) < .000001 && math.Abs(pokemon.Lon-oldPokemon.Lon) < .000001 {
	//	log.Errorf("Why are we updating this? %s", cmp.Diff(oldPokemon, pokemon, cmp.Options{
	//		ignoreNearFloats, ignoreNearNullFloats,
	//		cmpopts.IgnoreFields(Pokemon{}, "Username", "Iv", "Pvp"),
	//	}))
	//}

	if pokemon.FirstSeenTimestamp == 0 {
		pokemon.FirstSeenTimestamp = now
	}

	pokemon.Updated = null.IntFrom(now)
	if oldPokemon == nil || oldPokemon.PokemonId != pokemon.PokemonId || oldPokemon.Cp != pokemon.Cp {
		pokemon.Changed = now
	}

	changePvpField := false
	var pvpResults map[string][]gohbem.PokemonEntry
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
				pvpResults = pvp
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

	if !config.Config.PokemonMemoryOnly {
		if oldPokemon == nil {
			pvpField, pvpValue := "", ""
			if changePvpField {
				pvpField, pvpValue = "pvp, ", ":pvp, "
			}
			res, err := db.PokemonDb.NamedExecContext(ctx, fmt.Sprintf("INSERT INTO pokemon (id, pokemon_id, lat, lon,"+
				"spawn_id, expire_timestamp, atk_iv, def_iv, sta_iv, iv_inactive, iv, move_1, move_2,"+
				"gender, form, cp, level, encounter_weather, weather, costume, weight, height, size,"+
				"display_pokemon_id, is_ditto, pokestop_id, updated, first_seen_timestamp, changed, cell_id,"+
				"expire_timestamp_verified, shiny, username, %s is_event, seen_type) "+
				"VALUES (:id, :pokemon_id, :lat, :lon, :spawn_id, :expire_timestamp, :atk_iv, :def_iv, :sta_iv,"+
				":iv_inactive, :iv, :move_1, :move_2, :gender, :form, :cp, :level, :encounter_weather, :weather, :costume,"+
				":weight, :height, :size, :display_pokemon_id, :is_ditto, :pokestop_id, :updated,"+
				":first_seen_timestamp, :changed, :cell_id, :expire_timestamp_verified, :shiny, :username, %s :is_event,"+
				":seen_type)", pvpField, pvpValue), pokemon)

			if err != nil {
				log.Errorf("insert pokemon: [%s] %s", pokemon.Id, err)
				log.Errorf("Full structure: %+v", pokemon)
				pokemonCache.Delete(pokemon.Id) // Force reload of pokemon from database
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
				"iv_inactive = :iv_inactive,"+
				"iv = :iv,"+
				"form = :form, "+
				"level = :level, "+
				"encounter_weather = :encounter_weather, "+
				"weather = :weather, "+
				"costume = :costume, "+
				"first_seen_timestamp = :first_seen_timestamp, "+
				"changed = :changed, "+
				"cell_id = :cell_id, "+
				"expire_timestamp_verified = :expire_timestamp_verified, "+
				"display_pokemon_id = :display_pokemon_id, "+
				"is_ditto = :is_ditto, "+
				"seen_type = :seen_type, "+
				"shiny = :shiny, "+
				"username = :username, "+
				"%s"+
				"is_event = :is_event "+
				"WHERE id = :id", pvpUpdate), pokemon,
			)
			if err != nil {
				log.Errorf("Update pokemon [%s] %s", pokemon.Id, err)
				log.Errorf("Full structure: %+v", pokemon)
				pokemonCache.Delete(pokemon.Id) // Force reload of pokemon from database

				return
			}
			rows, rowsErr := res.RowsAffected()
			log.Debugf("Updating pokemon [%s] after update res = %d %v", pokemon.Id, rows, rowsErr)
		}
	}

	// Update pokemon rtree
	if oldPokemon == nil {
		addPokemonToTree(pokemon)
	} else {
		if pokemon.Lat != oldPokemon.Lat || pokemon.Lon != oldPokemon.Lon {
			removePokemonFromTree(oldPokemon)
			addPokemonToTree(pokemon)
		}
	}

	updatePokemonLookup(pokemon, changePvpField, pvpResults)

	areas := MatchStatsGeofence(pokemon.Lat, pokemon.Lon)
	createPokemonWebhooks(oldPokemon, pokemon, areas)
	updatePokemonStats(oldPokemon, pokemon, areas)
	updatePokemonNests(oldPokemon, pokemon)

	pokemon.Pvp = null.NewString("", false) // Reset PVP field to avoid keeping it in memory cache

	if db.UsePokemonCache {
		pokemonCache.Set(pokemon.Id, *pokemon, pokemon.remainingDuration())
	}
}

func createPokemonWebhooks(old *Pokemon, new *Pokemon, areas []geo.AreaName) {
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
				} else {
					return json.RawMessage(new.Pvp.ValueOrZero())
				}
			}(),
		}

		webhooks.AddMessage(webhooks.Pokemon, pokemonHook, areas)
	}
}

func (pokemon *Pokemon) isNewRecord() bool {
	return pokemon.FirstSeenTimestamp == 0
}

func (pokemon *Pokemon) remainingDuration() time.Duration {
	remaining := ttlcache.DefaultTTL
	if pokemon.ExpireTimestampVerified {
		timeLeft := 60 + pokemon.ExpireTimestamp.ValueOrZero() - time.Now().Unix()
		if timeLeft > 1 {
			remaining = time.Duration(timeLeft) * time.Second
		}
	}
	return remaining
}

func (pokemon *Pokemon) addWildPokemon(ctx context.Context, db db.DbDetails, wildPokemon *pogo.WildPokemonProto, timestampMs int64) bool {
	if strconv.FormatUint(wildPokemon.EncounterId, 10) != pokemon.Id {
		panic("Unmatched EncounterId")
	}
	pokemon.Lat = wildPokemon.Latitude
	pokemon.Lon = wildPokemon.Longitude

	pokemon.updateSpawnpointInfo(ctx, db, wildPokemon, timestampMs)
	return pokemon.setPokemonDisplay(int16(wildPokemon.Pokemon.PokemonId), wildPokemon.Pokemon.PokemonDisplay)
}

// wildSignificantUpdate returns true if the wild pokemon is significantly different from the current pokemon and
// should be written.
func (pokemon *Pokemon) wildSignificantUpdate(wildPokemon *pogo.WildPokemonProto, time int64) bool {
	pokemonDisplay := wildPokemon.Pokemon.PokemonDisplay
	// We would accept a wild update if the pokemon has changed; or to extend an unknown spawn time that is expired

	return pokemon.SeenType.ValueOrZero() == SeenType_Cell ||
		pokemon.SeenType.ValueOrZero() == SeenType_NearbyStop ||
		pokemon.PokemonId != int16(wildPokemon.Pokemon.PokemonId) ||
		pokemon.Form.ValueOrZero() != int64(pokemonDisplay.Form) ||
		pokemon.Weather.ValueOrZero() != int64(pokemonDisplay.WeatherBoostedCondition) ||
		pokemon.Costume.ValueOrZero() != int64(pokemonDisplay.Costume) ||
		pokemon.Gender.ValueOrZero() != int64(pokemonDisplay.Gender) ||
		(!pokemon.ExpireTimestampVerified && pokemon.ExpireTimestamp.ValueOrZero() < time)
}

func (pokemon *Pokemon) updateFromWild(ctx context.Context, db db.DbDetails, wildPokemon *pogo.WildPokemonProto, cellId int64, timestampMs int64, username string) {
	pokemon.IsEvent = 0
	encounterId := strconv.FormatUint(wildPokemon.EncounterId, 10)
	switch pokemon.SeenType.ValueOrZero() {
	case "", SeenType_Cell, SeenType_NearbyStop:
		pokemon.SeenType = null.StringFrom(SeenType_Wild)
		updateStats(ctx, db, encounterId, stats_seenWild)
	}
	if pokemon.addWildPokemon(ctx, db, wildPokemon, timestampMs) {
		updateStats(ctx, db, pokemon.Id, stats_statsReset)
	}
	pokemon.repopulateStatsIfNeeded(ctx, db)
	pokemon.Username = null.StringFrom(username)
	pokemon.CellId = null.IntFrom(cellId)
}

func (pokemon *Pokemon) updateFromMap(ctx context.Context, db db.DbDetails, mapPokemon *pogo.MapPokemonProto, cellId int64, username string) {

	if !pokemon.isNewRecord() {
		// Do not ever overwrite lure details based on seeing it again in the GMO
		return
	}

	pokemon.IsEvent = 0

	encounterId := strconv.FormatUint(mapPokemon.EncounterId, 10)
	pokemon.Id = encounterId
	updateStats(ctx, db, encounterId, stats_seenLure)

	spawnpointId := mapPokemon.SpawnpointId

	pokestop, _ := GetPokestopRecord(ctx, db, spawnpointId)
	if pokestop == nil {
		// Unrecognised pokestop
		return
	}
	pokemon.PokestopId = null.StringFrom(pokestop.Id)
	pokemon.PokemonId = int16(mapPokemon.PokedexTypeId)
	pokemon.Lat = pokestop.Lat
	pokemon.Lon = pokestop.Lon
	pokemon.SeenType = null.StringFrom(SeenType_LureWild) // TODO may have been encounter... this needs fixing

	if mapPokemon.PokemonDisplay != nil {
		if pokemon.setPokemonDisplay(pokemon.PokemonId, mapPokemon.PokemonDisplay) {
			updateStats(ctx, db, pokemon.Id, stats_statsReset)
		}
		pokemon.repopulateStatsIfNeeded(ctx, db)
		// The mapPokemon and nearbyPokemon GMOs don't contain actual shininess.
		// shiny = mapPokemon.pokemonDisplay.shiny
	} else {
		log.Warnf("[POKEMON] MapPokemonProto missing PokemonDisplay for %s", pokemon.Id)
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

func (pokemon *Pokemon) calculateIv(a int64, d int64, s int64) {
	pokemon.AtkIv = null.IntFrom(a)
	pokemon.DefIv = null.IntFrom(d)
	pokemon.StaIv = null.IntFrom(s)
	pokemon.Iv = null.FloatFrom(float64(a+d+s) / .45)
}

func (pokemon *Pokemon) updateFromNearby(ctx context.Context, db db.DbDetails, nearbyPokemon *pogo.NearbyPokemonProto, cellId int64, username string) {
	pokemon.IsEvent = 0
	encounterId := strconv.FormatUint(nearbyPokemon.EncounterId, 10)
	pokestopId := nearbyPokemon.FortId
	pokemonId := int16(nearbyPokemon.PokedexNumber)
	if pokemon.setPokemonDisplay(pokemonId, nearbyPokemon.PokemonDisplay) {
		updateStats(ctx, db, pokemon.Id, stats_statsReset)
	}
	pokemon.repopulateStatsIfNeeded(ctx, db)
	pokemon.Username = null.StringFrom(username)

	if pokemon.isNewRecord() {
		if pokestopId == "" {
			updateStats(ctx, db, encounterId, stats_seenCell)
		} else {
			updateStats(ctx, db, encounterId, stats_seenStop)
		}
	}

	var lat, lon float64
	overrideLatLon := pokemon.isNewRecord()
	useCellLatLon := true
	if pokestopId != "" {
		switch pokemon.SeenType.ValueOrZero() {
		case "", SeenType_Cell:
			overrideLatLon = true // a better estimate is available
		case SeenType_NearbyStop:
		default:
			return
		}
		pokestop, _ := GetPokestopRecord(ctx, db, pokestopId)
		if pokestop == nil {
			// Unrecognised pokestop, rollback changes
			overrideLatLon = pokemon.isNewRecord()
		} else {
			pokemon.SeenType = null.StringFrom(SeenType_NearbyStop)
			pokemon.PokestopId = null.StringFrom(pokestopId)
			lat, lon = pokestop.Lat, pokestop.Lon
			useCellLatLon = false
		}
	}
	if useCellLatLon {
		// Cell Pokemon
		if !overrideLatLon && pokemon.SeenType.ValueOrZero() != SeenType_Cell {
			// do not downgrade to nearby cell
			return
		}

		s2cell := s2.CellFromCellID(s2.CellID(cellId))
		lat = s2cell.CapBound().RectBound().Center().Lat.Degrees()
		lon = s2cell.CapBound().RectBound().Center().Lng.Degrees()

		pokemon.SeenType = null.StringFrom(SeenType_Cell)
	}
	if overrideLatLon {
		pokemon.Lat, pokemon.Lon = lat, lon
	} else {
		midpoint := s2.LatLngFromPoint(s2.Point{s2.PointFromLatLng(s2.LatLngFromDegrees(pokemon.Lat, pokemon.Lon)).
			Add(s2.PointFromLatLng(s2.LatLngFromDegrees(lat, lon)).Vector)})
		pokemon.Lat = midpoint.Lat.Degrees()
		pokemon.Lon = midpoint.Lng.Degrees()
	}
	pokemon.CellId = null.IntFrom(cellId)
	pokemon.setUnknownTimestamp()
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
// timestampMs - the timestamp to be used for calculations
func (pokemon *Pokemon) updateSpawnpointInfo(ctx context.Context, db db.DbDetails, wildPokemon *pogo.WildPokemonProto, timestampMs int64) {
	spawnId, err := strconv.ParseInt(wildPokemon.SpawnPointId, 16, 64)
	if err != nil {
		panic(err)
	}

	pokemon.SpawnId = null.IntFrom(spawnId)
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
	} else {
		pokemon.setUnknownTimestamp()
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

func (pokemon *Pokemon) addEncounterPokemon(ctx context.Context, db db.DbDetails, proto *pogo.PokemonProto) {
	pokemon.Cp = null.IntFrom(int64(proto.Cp))
	pokemon.Move1 = null.IntFrom(int64(proto.Move1))
	pokemon.Move2 = null.IntFrom(int64(proto.Move2))
	pokemon.Height = null.FloatFrom(float64(proto.HeightM))
	pokemon.Size = null.IntFrom(int64(proto.Size))
	pokemon.Weight = null.FloatFrom(float64(proto.WeightKg))
	oldWeather := pokemon.EncounterWeather
	pokemon.EncounterWeather = uint8(proto.PokemonDisplay.WeatherBoostedCondition)
	isUnboostedPartlyCloudy := false
	if proto.PokemonDisplay.WeatherBoostedCondition == pogo.GameplayWeatherProto_NONE {
		weather, err := findWeatherRecordByLatLon(ctx, db, pokemon.Lat, pokemon.Lon)
		if err != nil || weather == nil || !weather.GameplayCondition.Valid {
			log.Warnf("Failed to obtain weather for Pokemon %s: %s", pokemon.Id, err)
		} else if weather.GameplayCondition.Int64 == int64(pogo.GameplayWeatherProto_PARTLY_CLOUDY) {
			isUnboostedPartlyCloudy = true
		} else {
			pokemon.EncounterWeather = EncounterWeather_UnboostedNotPartlyCloudy
		}
	}
	cpMultiplier := float64(proto.CpMultiplier)
	var level int64
	if cpMultiplier < 0.734 {
		level = int64(math.Round((58.35178527*cpMultiplier-2.838007664)*cpMultiplier + 0.8539209906))
	} else {
		level = int64(math.Round(171.0112688*cpMultiplier - 95.20425243))
	}

	// Here comes the Ditto logic. Embrace yourself :)
	// Ditto weather can be split into 4 categories:
	//  - 00: No weather boost
	//  - 0P: No weather boost but Ditto is actually boosted by partly cloudy [atypical]
	//  - B0: Weather boosts disguise but not Ditto [atypical]
	//  - PP: Weather being partly cloudy boosts both disguise and Ditto
	// We will also use 0N/BN/PN to denote a normal non-Ditto spawn with corresponding weather boosts.
	// Disguise IV depends on Ditto weather boost instead, and caught Ditto is boosted only in PP state.
	// archive should be set to false for [normal]>0P or 0P>B0
	setDittoAttributes := func(mode string, to0P, archive, setDitto bool) {
		if mode == "B0" || mode == "0P" {
			log.Debugf("[POKEMON] %s: %s Ditto found, current display %d. (%x,%d,%x%x%x)",
				pokemon.Id, mode, pokemon.PokemonId, pokemon.EncounterWeather,
				level, proto.IndividualStamina, proto.IndividualDefense, proto.IndividualAttack)
		} else {
			log.Infof("[POKEMON] %s: %s Ditto found, current display %d. (%x,%d,%x%x%x,%03x)>(%x,%d,%x%x%x)",
				pokemon.Id, mode, pokemon.PokemonId, oldWeather, pokemon.Level.Int64, pokemon.StaIv.ValueOrZero(),
				pokemon.DefIv.ValueOrZero(), pokemon.AtkIv.ValueOrZero(), pokemon.IvInactive.ValueOrZero(),
				pokemon.EncounterWeather, level,
				proto.IndividualStamina, proto.IndividualDefense, proto.IndividualAttack)
		}
		pokemon.IsDitto = setDitto
		if setDitto {
			pokemon.DisplayPokemonId = null.IntFrom(int64(proto.PokemonId))
			pokemon.PokemonId = int16(pogo.HoloPokemonId_DITTO)
		} else {
			pokemon.DisplayPokemonId = null.NewInt(0, false)
			pokemon.PokemonId = int16(proto.PokemonId)
		}
		if to0P { // IV switching needed if we are transitioning into a 0P Ditto
			if archive {
				if pokemon.IvInactive.Valid {
					pokemon.calculateIv(pokemon.IvInactive.Int64&15, pokemon.IvInactive.Int64>>4&15,
						pokemon.IvInactive.Int64>>8&15)
				} else {
					pokemon.AtkIv = null.NewInt(0, false)
					pokemon.DefIv = null.NewInt(0, false)
					pokemon.StaIv = null.NewInt(0, false)
					pokemon.Iv = null.NewFloat(0, false)
				}
			}
			pokemon.Level = null.IntFrom(level - 5)
			pokemon.IvInactive = null.IntFrom(int64(
				proto.IndividualAttack | proto.IndividualDefense<<4 | proto.IndividualStamina<<8))
		} else {
			if archive {
				pokemon.IvInactive = pokemon.compressIv()
			}
			pokemon.Level = null.IntFrom(level)
			pokemon.calculateIv(int64(proto.IndividualAttack), int64(proto.IndividualDefense),
				int64(proto.IndividualStamina))
		}
	}
	if pokemon.IsDitto {
		// For a confirmed Ditto, we persist IV in inactive only in 0P state
		// when disguise is boosted, it has same IV as Ditto
		if isUnboostedPartlyCloudy {
			if pokemon.Level.Int64 == level-5 {
				pokemon.IvInactive = null.IntFrom(int64(
					proto.IndividualAttack | proto.IndividualDefense<<4 | proto.IndividualStamina<<8))
			} else {
				setDittoAttributes("0N", false, true, false)
			}
		} else if pokemon.EncounterWeather == uint8(pogo.GameplayWeatherProto_NONE) &&
			// a 0P Ditto can never be in PP state
			oldWeather != uint8(pogo.GameplayWeatherProto_PARTLY_CLOUDY) &&
			// at this point we are not sure if we are in 00 or 0P, so we guess 0P only if the last scanned level agrees
			pokemon.Level.Int64 == level-5 {
			pokemon.IvInactive = null.IntFrom(int64(
				proto.IndividualAttack | proto.IndividualDefense<<4 | proto.IndividualStamina<<8))
		} else if pokemon.Weather.Int64 != int64(pogo.GameplayWeatherProto_NONE) &&
			pokemon.Weather.Int64 != int64(pogo.GameplayWeatherProto_PARTLY_CLOUDY) && pokemon.Level.Int64 != level {
			setDittoAttributes("BN", false, true, false)
		} else {
			pokemon.Level = null.IntFrom(level)
			pokemon.calculateIv(int64(proto.IndividualAttack), int64(proto.IndividualDefense),
				int64(proto.IndividualStamina))
		}
		return
	}
	// There are 10 total possible transitions among these states, i.e. all 12 of them except for 0P <-> PP.
	// A Ditto in 00/PP state is undetectable. We try to detect them in the remaining possibilities.
	// Now we try to detect all 10 possible conditions where we could identify Ditto with certainty
	if pokemon.Level.Valid {
		switch level - pokemon.Level.Int64 {
		case 0:
		// the Pokemon has been encountered before but we find an unexpected level when reencountering it => Ditto
		// note that at this point the level should have been already readjusted according to the new weather boost
		case 5:
			switch pokemon.Weather.Int64 {
			case int64(pogo.GameplayWeatherProto_NONE):
				switch oldWeather {
				case EncounterWeather_Invalid: // should only happen when upgrading with pre-existing data
					if !pokemon.IvInactive.Valid {
						setDittoAttributes("00/0N>0P", true, false, true)
					} else if level > 30 {
						setDittoAttributes("BN/PN>0P", true, false, true)
					} else {
						setDittoAttributes("00/0N/BN/PN>0P or B0>00/[0N]", false, true, false)
					}
				case uint8(pogo.GameplayWeatherProto_NONE), EncounterWeather_UnboostedNotPartlyCloudy,
					uint8(pogo.GameplayWeatherProto_NONE) | EncounterWeather_Rerolled,
					EncounterWeather_UnboostedNotPartlyCloudy | EncounterWeather_Rerolled:
					setDittoAttributes("00/0N>0P", true, false, true)
				case uint8(pogo.GameplayWeatherProto_PARTLY_CLOUDY),
					uint8(pogo.GameplayWeatherProto_PARTLY_CLOUDY) | EncounterWeather_Rerolled:
					setDittoAttributes("PN>0P", true, false, true)
				default:
					if level > 30 {
						setDittoAttributes("BN>0P", true, false, true)
					} else if oldWeather&EncounterWeather_Rerolled != 0 {
						// in case of BN>0P, we set Ditto to be a hidden 0P state, hoping we rediscover later
						setDittoAttributes("BN>0P or B0>00/[0N]", false, true, false)
					} else if isUnboostedPartlyCloudy {
						setDittoAttributes("BN>0P or B0>[0N]", false, true, false)
					} else if pokemon.EncounterWeather == EncounterWeather_UnboostedNotPartlyCloudy {
						setDittoAttributes("B0>[00]/0N", false, true, true)
					} else {
						// set Ditto as it is most likely B0>00 if species did not reroll
						setDittoAttributes("BN>0P or B0>[00]/0N", false, true, true)
					}
				}
			case int64(pogo.GameplayWeatherProto_PARTLY_CLOUDY):
				// we can never be sure if this is a Ditto or rerolling into non-Ditto so assume not
				if oldWeather&EncounterWeather_Rerolled == 0 {
					setDittoAttributes("B0>[PP]/PN", false, true, true)
				} else {
					setDittoAttributes("B0>PP/[PN]", false, true, false)
				}
			default:
				setDittoAttributes("B0>BN", false, true, false)
			}
			return
		case -5:
			switch pokemon.Weather.Int64 {
			case int64(pogo.GameplayWeatherProto_NONE):
				if oldWeather&EncounterWeather_Rerolled == 0 {
					setDittoAttributes("0P>[00]/0N", false, true, true)
				} else {
					// we can never be sure if this is a Ditto or rerolling into non-Ditto so assume not
					setDittoAttributes("0P>00/[0N]", false, true, false)
				}
			case int64(pogo.GameplayWeatherProto_PARTLY_CLOUDY):
				setDittoAttributes("0P>PN", false, true, false)
			default:
				switch oldWeather {
				case EncounterWeather_Invalid: // should only happen when upgrading with pre-existing data
					if !pokemon.IvInactive.Valid {
						setDittoAttributes("BN/PP/PN>B0", false, true, true)
					} else if level <= 5 ||
						proto.IndividualAttack < 4 || proto.IndividualDefense < 4 || proto.IndividualStamina < 4 {
						setDittoAttributes("00/0N/BN/PP/PN>B0", false, true, true)
					} else {
						setDittoAttributes("00/0N/BN/PP/PN>B0 or 0P>[BN]", false, true, false)
					}
				case uint8(pogo.GameplayWeatherProto_NONE), EncounterWeather_UnboostedNotPartlyCloudy,
					uint8(pogo.GameplayWeatherProto_NONE) | EncounterWeather_Rerolled,
					EncounterWeather_UnboostedNotPartlyCloudy | EncounterWeather_Rerolled:
					if level <= 5 ||
						proto.IndividualAttack < 4 || proto.IndividualDefense < 4 || proto.IndividualStamina < 4 {
						setDittoAttributes("00/0N>B0", false, true, true)
					} else if oldWeather != uint8(pogo.GameplayWeatherProto_NONE)|EncounterWeather_Rerolled {
						setDittoAttributes("00/0N>[B0] or 0P>BN", false, true, true)
					} else {
						setDittoAttributes("00/0N>B0 or 0P>[BN]", false, true, false)
					}
				case uint8(pogo.GameplayWeatherProto_PARTLY_CLOUDY),
					uint8(pogo.GameplayWeatherProto_PARTLY_CLOUDY) | EncounterWeather_Rerolled:
					setDittoAttributes("PP/PN>B0", false, true, true)
				default:
					setDittoAttributes("BN>B0", false, true, true)
				}
			}
			return
		case 10:
			setDittoAttributes("B0>0P", true, true, true)
			return
		case -10:
			setDittoAttributes("0P>B0", false, false, true)
			return
		default:
			log.Errorf("[POKEMON] An unexpected level was seen upon reencountering %s: %d -> %d. Old IV is lost.",
				pokemon.Id, pokemon.Level.Int64, level)
			pokemon.AtkIv = null.NewInt(0, false)
			pokemon.DefIv = null.NewInt(0, false)
			pokemon.StaIv = null.NewInt(0, false)
			pokemon.Iv = null.NewFloat(0, false)
			pokemon.IvInactive = null.NewInt(0, false)
		}
	}
	if pokemon.Weather.Int64 != int64(pogo.GameplayWeatherProto_NONE) {
		if level <= 5 || proto.IndividualAttack < 4 || proto.IndividualDefense < 4 || proto.IndividualStamina < 4 {
			setDittoAttributes("B0", false, false, true)
		} else {
			pokemon.Level = null.IntFrom(level)
			pokemon.calculateIv(int64(proto.IndividualAttack), int64(proto.IndividualDefense),
				int64(proto.IndividualStamina))
		}
	} else if level > 30 {
		setDittoAttributes("0P", true, false, true)
	} else {
		pokemon.Level = null.IntFrom(level)
		pokemon.calculateIv(int64(proto.IndividualAttack), int64(proto.IndividualDefense),
			int64(proto.IndividualStamina))
	}
}

func (pokemon *Pokemon) updatePokemonFromEncounterProto(ctx context.Context, db db.DbDetails, encounterData *pogo.EncounterOutProto, username string) {
	pokemon.IsEvent = 0
	// TODO is there a better way to get this from the proto? This is how RDM does it
	pokemon.addWildPokemon(ctx, db, encounterData.Pokemon, time.Now().Unix()*1000)
	pokemon.addEncounterPokemon(ctx, db, encounterData.Pokemon.Pokemon)

	if pokemon.CellId.Valid == false {
		centerCoord := s2.LatLngFromDegrees(pokemon.Lat, pokemon.Lon)
		cellID := s2.CellIDFromLatLng(centerCoord).Parent(15)
		pokemon.CellId = null.IntFrom(int64(cellID))
	}

	pokemon.Shiny = null.BoolFrom(encounterData.Pokemon.Pokemon.PokemonDisplay.Shiny)
	pokemon.Username = null.StringFrom(username)

	pokemon.SeenType = null.StringFrom(SeenType_Encounter)
	updateStats(ctx, db, pokemon.Id, stats_encounter)
}

func (pokemon *Pokemon) updatePokemonFromDiskEncounterProto(ctx context.Context, db db.DbDetails, encounterData *pogo.DiskEncounterOutProto) {
	pokemon.IsEvent = 0
	pokemon.addEncounterPokemon(ctx, db, encounterData.Pokemon)

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

	pokemon.SeenType = null.StringFrom(SeenType_LureEncounter)
	updateStats(ctx, db, pokemon.Id, stats_lureEncounter)
}

func (pokemon *Pokemon) setPokemonDisplay(pokemonId int16, display *pogo.PokemonDisplayProto) bool {
	if !pokemon.isNewRecord() {
		// If we would like to support detect A/B spawn in the future, fill in more code here from Chuck
		var oldId int16
		if pokemon.IsDitto {
			oldId = int16(pokemon.DisplayPokemonId.ValueOrZero())
		} else {
			oldId = pokemon.PokemonId
		}
		if oldId != pokemonId || pokemon.Form != null.IntFrom(int64(display.Form)) ||
			pokemon.Costume != null.IntFrom(int64(display.Costume)) ||
			pokemon.Gender != null.IntFrom(int64(display.Gender)) {
			log.Debugf("Pokemon %s changed from (%d,%d,%d,%d) to (%d,%d,%d,%d)", pokemon.Id, oldId,
				pokemon.Form.ValueOrZero(), pokemon.Costume.ValueOrZero(), pokemon.Gender.ValueOrZero(),
				pokemonId, display.Form, display.Costume, display.Gender)
			pokemon.Weight = null.NewFloat(0, false)
			pokemon.Height = null.NewFloat(0, false)
			pokemon.Size = null.NewInt(0, false)
			pokemon.Move1 = null.NewInt(0, false)
			pokemon.Move2 = null.NewInt(0, false)
			pokemon.Cp = null.NewInt(0, false)
			pokemon.Shiny = null.NewBool(false, false)
			if pokemon.IsDitto {
				if pokemon.Weather.Int64 != int64(pogo.GameplayWeatherProto_NONE) &&
					pokemon.Weather.Int64 != int64(pogo.GameplayWeatherProto_PARTLY_CLOUDY) {
					// reset weather for B0 state Ditto
					pokemon.Weather = null.IntFrom(int64(pogo.GameplayWeatherProto_NONE))
				}
				pokemon.IsDitto = false
			}
			pokemon.EncounterWeather |= EncounterWeather_Rerolled
			pokemon.DisplayPokemonId = null.NewInt(0, false)
			pokemon.Pvp = null.NewString("", false)
		}
	}
	if pokemon.isNewRecord() || !pokemon.IsDitto {
		pokemon.PokemonId = pokemonId
	}
	pokemon.Gender = null.IntFrom(int64(display.Gender))
	pokemon.Form = null.IntFrom(int64(display.Form))
	pokemon.Costume = null.IntFrom(int64(display.Costume))
	return pokemon.setWeather(int64(display.WeatherBoostedCondition))
}

func (pokemon *Pokemon) compressIv() null.Int {
	if pokemon.AtkIv.Valid {
		if !pokemon.DefIv.Valid || !pokemon.StaIv.Valid {
			panic("Set atk but not also def and sta")
		}
		return null.IntFrom(pokemon.AtkIv.Int64 | pokemon.DefIv.Int64<<4 | pokemon.StaIv.Int64<<8)
	} else {
		return null.NewInt(0, false)
	}
}

func (pokemon *Pokemon) setWeather(weather int64) bool {
	shouldReencounter := false // whether reencountering might give more information. Returns false for new record
	if !pokemon.isNewRecord() && pokemon.Weather.ValueOrZero() != weather {
		var reset, isBoosted bool
		if pokemon.IsDitto {
			isBoosted = weather == int64(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
			// both Ditto and disguise are boosted and Ditto was not boosted: none -> boosted
			// or both Ditto and disguise were boosted and Ditto is not boosted: boosted -> none
			reset = isBoosted || pokemon.Weather.ValueOrZero() == int64(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
			// Technically Ditto should also be rescanned during 0P>B0 (and optionally B0>0P) but this is not the place to do it
		} else {
			isBoosted = weather != int64(pogo.GameplayWeatherProto_NONE)
			reset = pokemon.Weather.ValueOrZero() != int64(pogo.GameplayWeatherProto_NONE) != isBoosted
		}
		if reset {
			currentIv := pokemon.compressIv()
			if pokemon.IvInactive.Valid {
				pokemon.calculateIv(pokemon.IvInactive.Int64&15, pokemon.IvInactive.Int64>>4&15,
					pokemon.IvInactive.Int64>>8&15)
				switch pokemon.SeenType.ValueOrZero() {
				case SeenType_LureWild:
					pokemon.SeenType = null.StringFrom(SeenType_LureEncounter)
				case SeenType_Wild:
					pokemon.SeenType = null.StringFrom(SeenType_Encounter)
				}
			} else {
				pokemon.AtkIv = null.NewInt(0, false)
				pokemon.DefIv = null.NewInt(0, false)
				pokemon.StaIv = null.NewInt(0, false)
				pokemon.Iv = null.NewFloat(0, false)
				shouldReencounter = true
				switch pokemon.SeenType.ValueOrZero() {
				case SeenType_LureEncounter:
					pokemon.SeenType = null.StringFrom(SeenType_LureWild)
				case SeenType_Encounter:
					pokemon.SeenType = null.StringFrom(SeenType_Wild)
				}
			}
			pokemon.IvInactive = currentIv
			pokemon.Cp = null.NewInt(0, false)
			if pokemon.Level.Valid {
				if isBoosted {
					pokemon.Level.Int64 += 5
				} else {
					pokemon.Level.Int64 -= 5
				}
			}
			pokemon.Pvp = null.NewString("", false)
		}
	}
	pokemon.Weather = null.IntFrom(weather)
	return shouldReencounter
}

func (pokemon *Pokemon) repopulateStatsIfNeeded(ctx context.Context, db db.DbDetails) {
	// TODO: repopulate weight/size/height?
	if pokemon.Cp.Valid || ohbem == nil {
		return
	}
	var displayPokemon int64
	useInactive := false
	if pokemon.IsDitto {
		displayPokemon = pokemon.DisplayPokemonId.Int64
		if pokemon.Weather.Int64 == int64(pogo.GameplayWeatherProto_NONE) {
			weather, err := findWeatherRecordByLatLon(ctx, db, pokemon.Lat, pokemon.Lon)
			if err != nil || weather == nil || !weather.GameplayCondition.Valid {
				log.Warnf("Failed to obtain weather for Pokemon %s: %s", pokemon.Id, err)
			} else if weather.GameplayCondition.Int64 == int64(pogo.GameplayWeatherProto_PARTLY_CLOUDY) {
				useInactive = true
			}
		}
	} else {
		displayPokemon = int64(pokemon.PokemonId)
	}
	var cp int
	var err error
	if useInactive {
		if !pokemon.IvInactive.Valid {
			return
		}
		// You should see boosted IV for 0P Ditto
		cp, err = ohbem.CalculateCp(int(displayPokemon), int(pokemon.Form.ValueOrZero()), 0,
			int(pokemon.IvInactive.Int64&15), int(pokemon.IvInactive.Int64>>4&15), int(pokemon.IvInactive.Int64>>8&15),
			float64(pokemon.Level.Int64+5))
	} else {
		if !pokemon.AtkIv.Valid {
			return
		}
		cp, err = ohbem.CalculateCp(int(displayPokemon), int(pokemon.Form.ValueOrZero()), 0,
			int(pokemon.AtkIv.Int64), int(pokemon.DefIv.Int64), int(pokemon.StaIv.Int64),
			float64(pokemon.Level.Int64))
	}
	if err == nil {
		pokemon.Cp = null.IntFrom(int64(cp))
	} else {
		log.Warnf("Pokemon %s %d CP unset due to error %s", pokemon.Id, displayPokemon, err)
	}
}

func UpdatePokemonRecordWithEncounterProto(ctx context.Context, db db.DbDetails, encounter *pogo.EncounterOutProto, username string) string {
	if encounter.Pokemon == nil {
		return "No encounter"
	}

	encounterId := strconv.FormatUint(encounter.Pokemon.EncounterId, 10)

	pokemonMutex, _ := pokemonStripedMutex.GetLock(encounterId)
	pokemonMutex.Lock()
	defer pokemonMutex.Unlock()

	pokemon, err := getOrCreatePokemonRecord(ctx, db, encounterId)
	if err != nil {
		log.Errorf("Error pokemon [%s]: %s", encounterId, err)
		return fmt.Sprintf("Error finding pokemon %s", err)
	}

	pokemon.updatePokemonFromEncounterProto(ctx, db, encounter, username)
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

	if pokemon == nil || pokemon.isNewRecord() {
		// No pokemon found
		diskEncounterCache.Set(encounterId, encounter, ttlcache.DefaultTTL)
		return fmt.Sprintf("%s Disk encounter without previous GMO - Pokemon stored for later", encounterId)
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
