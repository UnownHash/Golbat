package decoder

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/UnownHash/gohbem"
	"github.com/golang/geo/s2"
	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
	"gopkg.in/guregu/null.v4"

	"golbat/config"
	"golbat/db"
	"golbat/geo"
	"golbat/pogo"
	"golbat/webhooks"
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
	if config.Config.PokemonMemoryOnly {
		return nil, nil
	}
	pokemon := Pokemon{}

	err := db.PokemonDb.GetContext(ctx, &pokemon,
		"SELECT id, pokemon_id, lat, lon, spawn_id, expire_timestamp, atk_iv, def_iv, sta_iv, golbat_internal, iv, "+
			"move_1, move_2, gender, form, cp, level, strong, weather, costume, weight, height, size, "+
			"display_pokemon_id, is_ditto, pokestop_id, updated, first_seen_timestamp, changed, cell_id, "+
			"expire_timestamp_verified, shiny, username, pvp, is_event, seen_type "+
			"FROM pokemon WHERE id = ?", encounterId)

	statsCollector.IncDbQuery("select pokemon", err)
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
	pokemon = &Pokemon{Id: encounterId}
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
		!bytes.Equal(old.GolbatInternal, new.GolbatInternal) ||
		old.Form != new.Form ||
		old.Level != new.Level ||
		old.IsStrong != new.IsStrong ||
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
		if pokemon.AtkIv.Valid && (oldPokemon == nil || oldPokemon.PokemonId != pokemon.PokemonId ||
			oldPokemon.Level != pokemon.Level || oldPokemon.Form != pokemon.Form ||
			oldPokemon.Costume != pokemon.Costume || oldPokemon.Gender != pokemon.Gender) {
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
				"spawn_id, expire_timestamp, atk_iv, def_iv, sta_iv, golbat_internal, iv, move_1, move_2,"+
				"gender, form, cp, level, strong, weather, costume, weight, height, size,"+
				"display_pokemon_id, is_ditto, pokestop_id, updated, first_seen_timestamp, changed, cell_id,"+
				"expire_timestamp_verified, shiny, username, %s is_event, seen_type) "+
				"VALUES (:id, :pokemon_id, :lat, :lon, :spawn_id, :expire_timestamp, :atk_iv, :def_iv, :sta_iv,"+
				":golbat_internal, :iv, :move_1, :move_2, :gender, :form, :cp, :level, :strong, :weather, :costume,"+
				":weight, :height, :size, :display_pokemon_id, :is_ditto, :pokestop_id, :updated,"+
				":first_seen_timestamp, :changed, :cell_id, :expire_timestamp_verified, :shiny, :username, %s :is_event,"+
				":seen_type)", pvpField, pvpValue), pokemon)

			statsCollector.IncDbQuery("insert pokemon", err)
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
				"golbat_internal = :golbat_internal,"+
				"iv = :iv,"+
				"form = :form, "+
				"level = :level, "+
				"strong = :strong, "+
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
			statsCollector.IncDbQuery("update pokemon", err)
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
	createPokemonWebhooks(ctx, db, oldPokemon, pokemon, areas)
	updatePokemonStats(oldPokemon, pokemon, areas, now)

	pokemon.Pvp = null.NewString("", false) // Reset PVP field to avoid keeping it in memory cache

	if db.UsePokemonCache {
		pokemonCache.Set(pokemon.Id, *pokemon, pokemon.remainingDuration(now))
	}
}

func createPokemonWebhooks(ctx context.Context, db db.DbDetails, old *Pokemon, new *Pokemon, areas []geo.AreaName) {
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
			"pokestop_name": func() *string {
				if !new.PokestopId.Valid {
					return nil
				} else {
					pokestop, _ := GetPokestopRecord(ctx, db, new.PokestopId.String)
					name := "Unknown"
					if pokestop != nil {
						name = pokestop.Name.ValueOrZero()
					}
					return &name
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

		if new.AtkIv.Valid && new.DefIv.Valid && new.StaIv.Valid {
			webhooksSender.AddMessage(webhooks.PokemonIV, pokemonHook, areas)
		} else {
			webhooksSender.AddMessage(webhooks.PokemonNoIV, pokemonHook, areas)
		}
	}
}

func (pokemon *Pokemon) isNewRecord() bool {
	return pokemon.FirstSeenTimestamp == 0
}

func (pokemon *Pokemon) remainingDuration(now int64) time.Duration {
	remaining := ttlcache.DefaultTTL
	if pokemon.ExpireTimestampVerified {
		timeLeft := 60 + pokemon.ExpireTimestamp.ValueOrZero() - now
		if timeLeft > 1 {
			remaining = time.Duration(timeLeft) * time.Second
		}
	}
	return remaining
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

func (pokemon *Pokemon) updateFromWild(ctx context.Context, db db.DbDetails, wildPokemon *pogo.WildPokemonProto, cellId int64, weather map[int64]pogo.GameplayWeatherProto_WeatherCondition, timestampMs int64, username string) {
	pokemon.IsEvent = 0
	switch pokemon.SeenType.ValueOrZero() {
	case "", SeenType_Cell, SeenType_NearbyStop:
		pokemon.SeenType = null.StringFrom(SeenType_Wild)
	}
	calc := pokemonCalc{pokemon: pokemon, ctx: ctx, db: db, weather: weather}
	calc.addWildPokemon(wildPokemon, timestampMs)
	calc.recomputeCpIfNeeded()
	pokemon.Username = null.StringFrom(username)
	pokemon.CellId = null.IntFrom(cellId)
}

func (pokemon *Pokemon) updateFromMap(ctx context.Context, db db.DbDetails, mapPokemon *pogo.MapPokemonProto, cellId int64, weather map[int64]pogo.GameplayWeatherProto_WeatherCondition, timestampMs int64, username string) {

	if !pokemon.isNewRecord() {
		// Do not ever overwrite lure details based on seeing it again in the GMO
		return
	}

	pokemon.IsEvent = 0

	encounterId := strconv.FormatUint(mapPokemon.EncounterId, 10)
	pokemon.Id = encounterId

	spawnpointId := mapPokemon.SpawnpointId

	pokestop, _ := GetPokestopRecord(ctx, db, spawnpointId)
	if pokestop == nil {
		// Unrecognised pokestop
		return
	}
	pokemon.PokestopId = null.StringFrom(pokestop.Id)
	pokemon.Lat = pokestop.Lat
	pokemon.Lon = pokestop.Lon
	pokemon.SeenType = null.StringFrom(SeenType_LureWild)

	if mapPokemon.PokemonDisplay != nil {
		calc := pokemonCalc{pokemon: pokemon, ctx: ctx, db: db, weather: weather}
		calc.setPokemonDisplay(int16(mapPokemon.PokedexTypeId), mapPokemon.PokemonDisplay)
		calc.recomputeCpIfNeeded()
		// The mapPokemon and nearbyPokemon GMOs don't contain actual shininess.
		// shiny = mapPokemon.pokemonDisplay.shiny
	} else {
		log.Warnf("[POKEMON] MapPokemonProto missing PokemonDisplay for %s", pokemon.Id)
	}
	if !pokemon.Username.Valid {
		pokemon.Username = null.StringFrom(username)
	}

	if mapPokemon.ExpirationTimeMs > 0 && !pokemon.ExpireTimestampVerified {
		pokemon.ExpireTimestamp = null.IntFrom(mapPokemon.ExpirationTimeMs / 1000)
		pokemon.ExpireTimestampVerified = true
		// if we have cached an encounter for this pokemon, update the TTL.
		encounterCache.UpdateTTL(pokemon.Id, pokemon.remainingDuration(timestampMs/1000))
	} else {
		pokemon.ExpireTimestampVerified = false
	}

	pokemon.CellId = null.IntFrom(cellId)
}

func (pokemon *Pokemon) updateFromNearby(ctx context.Context, db db.DbDetails, nearbyPokemon *pogo.NearbyPokemonProto, cellId int64, weather map[int64]pogo.GameplayWeatherProto_WeatherCondition, timestampMs int64, username string) {
	pokemon.IsEvent = 0
	pokestopId := nearbyPokemon.FortId
	calc := pokemonCalc{pokemon: pokemon, ctx: ctx, db: db, weather: weather}
	calc.setPokemonDisplay(int16(nearbyPokemon.PokedexNumber), nearbyPokemon.PokemonDisplay)
	calc.recomputeCpIfNeeded()
	pokemon.Username = null.StringFrom(username)

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
	pokemon.setUnknownTimestamp(timestampMs / 1000)
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
		pokemon.setUnknownTimestamp(timestampMs / 1000)
	}
}

func (pokemon *Pokemon) setUnknownTimestamp(now int64) {
	if !pokemon.ExpireTimestamp.Valid {
		pokemon.ExpireTimestamp = null.IntFrom(now + 20*60) // should be configurable, add on 20min
	} else {
		if pokemon.ExpireTimestamp.Int64 < now {
			pokemon.ExpireTimestamp = null.IntFrom(now + 10*60) // should be configurable, add on 10min
		}
	}
}

func (pokemon *Pokemon) updatePokemonFromEncounterProto(ctx context.Context, db db.DbDetails, encounterData *pogo.EncounterOutProto, username string) {
	pokemon.IsEvent = 0
	calc := pokemonCalc{pokemon: pokemon, ctx: ctx, db: db}
	// TODO is there a better way to get this from the proto? This is how RDM does it
	calc.addWildPokemon(encounterData.Pokemon, time.Now().Unix()*1000)
	pokemon.SeenType = null.StringFrom(SeenType_Encounter)
	calc.addEncounterPokemon(encounterData.Pokemon.Pokemon, username)

	if pokemon.CellId.Valid == false {
		centerCoord := s2.LatLngFromDegrees(pokemon.Lat, pokemon.Lon)
		cellID := s2.CellIDFromLatLng(centerCoord).Parent(15)
		pokemon.CellId = null.IntFrom(int64(cellID))
	}
}

func (pokemon *Pokemon) updatePokemonFromDiskEncounterProto(ctx context.Context, db db.DbDetails, encounterData *pogo.DiskEncounterOutProto, username string) {
	pokemon.IsEvent = 0
	calc := pokemonCalc{pokemon: pokemon, ctx: ctx, db: db}
	calc.setPokemonDisplay(int16(encounterData.Pokemon.PokemonId), encounterData.Pokemon.PokemonDisplay)
	pokemon.SeenType = null.StringFrom(SeenType_LureEncounter)
	calc.addEncounterPokemon(encounterData.Pokemon, username)
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
	// updateEncounterStats() should only be called for encounters, and called
	// even if we have the pokemon record already.
	updateEncounterStats(pokemon)

	return fmt.Sprintf("%d %s Pokemon %d CP%d", encounter.Pokemon.EncounterId, encounterId, pokemon.PokemonId, encounter.Pokemon.Pokemon.Cp)
}

func UpdatePokemonRecordWithDiskEncounterProto(ctx context.Context, db db.DbDetails, encounter *pogo.DiskEncounterOutProto, username string) string {
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
	pokemon.updatePokemonFromDiskEncounterProto(ctx, db, encounter, username)
	savePokemonRecord(ctx, db, pokemon)
	// updateEncounterStats() should only be called for encounters, and called
	// even if we have the pokemon record already.
	updateEncounterStats(pokemon)

	return fmt.Sprintf("%s Disk Pokemon %d CP%d", encounterId, pokemon.PokemonId, encounter.Pokemon.Cp)
}
