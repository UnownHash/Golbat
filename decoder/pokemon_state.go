package decoder

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"golbat/config"
	"golbat/db"
	"golbat/geo"
	"golbat/webhooks"

	"github.com/UnownHash/gohbem"
	"github.com/guregu/null/v6"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
)

// wildPokemonDelay is how long wild Pokemon wait for encounter data before writing
const wildPokemonDelay = 30 * time.Second

// peekPokemonRecordReadOnly acquires lock, does NOT take snapshot.
// Use for read-only checks which will not cause a backing database lookup
// Caller must use returned unlock function
func peekPokemonRecordReadOnly(encounterId uint64) (*Pokemon, func(), error) {
	if item := pokemonCache.Get(encounterId); item != nil {
		pokemon := item.Value()
		pokemon.Lock()
		return pokemon, func() { pokemon.Unlock() }, nil
	}

	return nil, nil, nil
}

func loadPokemonFromDatabase(ctx context.Context, db db.DbDetails, encounterId uint64, pokemon *Pokemon) error {
	err := db.PokemonDb.GetContext(ctx, pokemon,
		"SELECT id, pokemon_id, lat, lon, spawn_id, expire_timestamp, atk_iv, def_iv, sta_iv, golbat_internal, iv, "+
			"move_1, move_2, gender, form, cp, level, strong, weather, costume, weight, height, size, "+
			"display_pokemon_id, is_ditto, pokestop_id, updated, first_seen_timestamp, changed, cell_id, "+
			"expire_timestamp_verified, shiny, username, pvp, is_event, seen_type "+
			"FROM pokemon WHERE id = ?", strconv.FormatUint(encounterId, 10))
	statsCollector.IncDbQuery("select pokemon", err)

	return err
}

// getPokemonRecordReadOnly acquires lock but does NOT take snapshot.
// Use for read-only checks, but will cause a backing database lookup
// Caller MUST call returned unlock function.
func getPokemonRecordReadOnly(ctx context.Context, db db.DbDetails, encounterId uint64) (*Pokemon, func(), error) {
	// If we are in-memory only, this is identical to peek
	if config.Config.PokemonMemoryOnly {
		return peekPokemonRecordReadOnly(encounterId)
	}

	// Check cache first
	if item := pokemonCache.Get(encounterId); item != nil {
		pokemon := item.Value()
		pokemon.Lock()
		return pokemon, func() { pokemon.Unlock() }, nil
	}

	dbPokemon := Pokemon{}
	err := loadPokemonFromDatabase(ctx, db, encounterId, &dbPokemon)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	dbPokemon.ClearDirty()

	// Atomically cache the loaded Pokemon - if another goroutine raced us,
	// we'll get their Pokemon and use that instead (ensuring same mutex)
	existingPokemon, _ := pokemonCache.GetOrSetFunc(encounterId, func() *Pokemon {
		// Only called if key doesn't exist - our Pokemon wins
		pokemonRtreeUpdatePokemonOnGet(&dbPokemon)
		return &dbPokemon
	})

	pokemon := existingPokemon.Value()
	pokemon.Lock()
	return pokemon, func() { pokemon.Unlock() }, nil
}

// getPokemonRecordForUpdate acquires lock AND takes snapshot for webhook comparison.
// Use when modifying the Pokemon.
// Caller MUST call returned unlock function.
func getPokemonRecordForUpdate(ctx context.Context, db db.DbDetails, encounterId uint64) (*Pokemon, func(), error) {
	pokemon, unlock, err := getPokemonRecordReadOnly(ctx, db, encounterId)
	if err != nil || pokemon == nil {
		return nil, nil, err
	}
	pokemon.snapshotOldValues()
	return pokemon, unlock, nil
}

// getOrCreatePokemonRecord gets existing or creates new, locked with snapshot.
// Caller MUST call returned unlock function.
func getOrCreatePokemonRecord(ctx context.Context, db db.DbDetails, encounterId uint64) (*Pokemon, func(), error) {
	// Create new Pokemon atomically - function only called if key doesn't exist
	pokemonItem, _ := pokemonCache.GetOrSetFunc(encounterId, func() *Pokemon {
		return &Pokemon{Id: encounterId, newRecord: true}
	})

	pokemon := pokemonItem.Value()
	pokemon.Lock()

	if config.Config.PokemonMemoryOnly {
		pokemon.snapshotOldValues()
		return pokemon, func() { pokemon.Unlock() }, nil
	}

	if pokemon.newRecord {
		// We should attempt to load from database
		err := loadPokemonFromDatabase(ctx, db, encounterId, pokemon)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				pokemon.Unlock()
				return nil, nil, err
			}
		} else {
			// We loaded
			pokemon.newRecord = false
			pokemon.ClearDirty()
			pokemonRtreeUpdatePokemonOnGet(pokemon)
		}
	}

	pokemon.snapshotOldValues()
	return pokemon, func() { pokemon.Unlock() }, nil
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

func savePokemonRecordAsAtTime(ctx context.Context, db db.DbDetails, pokemon *Pokemon, isEncounter, writeDB, webhook bool, now int64) {
	if !pokemon.newRecord && !pokemon.IsDirty() {
		return
	}

	if pokemon.FirstSeenTimestamp == 0 {
		pokemon.FirstSeenTimestamp = now
	}

	pokemon.SetUpdated(null.IntFrom(now))
	if pokemon.isNewRecord() || pokemon.oldValues.PokemonId != pokemon.PokemonId || pokemon.oldValues.Cp != pokemon.Cp {
		pokemon.SetChanged(now)
	}

	changePvpField := false
	var pvpResults map[string][]gohbem.PokemonEntry
	if ohbem != nil {
		// Calculating PVP data - check for changes in pokemon properties that affect PVP rankings
		// For new records, always calculate; for existing, check if relevant fields changed
		shouldCalculatePvp := pokemon.AtkIv.Valid && (pokemon.isNewRecord() || pokemon.IsDirty())
		if shouldCalculatePvp {
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
		if !pokemon.AtkIv.Valid && pokemon.isNewRecord() {
			pokemon.Pvp = null.NewString("", false)
			changePvpField = true
		}
	}

	var oldSeenType string
	if !pokemon.oldValues.SeenType.Valid {
		oldSeenType = "n/a"
	} else {
		oldSeenType = pokemon.oldValues.SeenType.ValueOrZero()
	}

	log.Debugf("Updating pokemon [%d] from %s->%s - newRecord: %t", pokemon.Id, oldSeenType, pokemon.SeenType.ValueOrZero(), pokemon.isNewRecord())

	// Capture isNewRecord before any state changes
	isNewRecord := pokemon.isNewRecord()

	if writeDB && !config.Config.PokemonMemoryOnly {
		// Prepare internal data if needed (must happen before queueing)
		if isEncounter && config.Config.PokemonInternalToDb {
			unboosted, boosted, strong := pokemon.locateAllScans()
			if unboosted != nil && boosted != nil {
				unboosted.RemoveDittoAuxInfo()
				boosted.RemoveDittoAuxInfo()
			}
			if strong != nil {
				strong.RemoveDittoAuxInfo()
			}
			marshaled, err := proto.Marshal(&pokemon.internal)
			if err == nil {
				pokemon.GolbatInternal = marshaled
			} else {
				log.Errorf("[POKEMON] Failed to marshal internal data for %d, data may be lost: %s", pokemon.Id, err)
			}
		}

		// Debug logging happens here, before queueing
		if dbDebugEnabled {
			if isNewRecord {
				dbDebugLog("INSERT", "Pokemon", strconv.FormatUint(pokemon.Id, 10), pokemon.changedFields)
			} else {
				dbDebugLog("UPDATE", "Pokemon", strconv.FormatUint(pokemon.Id, 10), pokemon.changedFields)
			}
		}

		// Queue the write through the write-behind system
		if writeBehindQueue != nil {
			// Determine delay based on seen type
			// Wild/nearby Pokemon wait for potential encounter data, encounter writes immediately
			delay := time.Duration(0)
			seenType := pokemon.SeenType.ValueOrZero()
			if seenType == SeenType_Wild || seenType == SeenType_LureWild ||
				seenType == SeenType_Cell || seenType == SeenType_NearbyStop {
				delay = wildPokemonDelay
			}
			writeBehindQueue.Enqueue(pokemon, isNewRecord, delay)
		} else {
			// Fallback to direct write if queue not initialized
			_ = pokemonWriteDB(db, pokemon, isNewRecord)
		}
	} else {
		if dbDebugEnabled {
			dbDebugLog("MEMORY", "Pokemon", strconv.FormatUint(pokemon.Id, 10), pokemon.changedFields)
		}
	}

	// Update pokemon rtree (immediate, not queued)
	if isNewRecord {
		addPokemonToTree(pokemon)
	} else if pokemon.Lat != pokemon.oldValues.Lat || pokemon.Lon != pokemon.oldValues.Lon {
		// Position changed - update R-tree by removing from old position and adding to new
		removePokemonFromTree(pokemon.Id, pokemon.oldValues.Lat, pokemon.oldValues.Lon)
		addPokemonToTree(pokemon)
	}

	updatePokemonLookup(pokemon, changePvpField, pvpResults)

	// Webhooks and stats happen immediately (not queued)
	areas := MatchStatsGeofence(pokemon.Lat, pokemon.Lon)
	if webhook {
		createPokemonWebhooks(ctx, db, pokemon, areas)
	}
	updatePokemonStats(pokemon, areas, now)

	if dbDebugEnabled {
		pokemon.changedFields = pokemon.changedFields[:0]
	}
	pokemon.newRecord = false // After saving, it's no longer a new record
	pokemon.ClearDirty()

	pokemon.Pvp = null.NewString("", false) // Reset PVP field to avoid keeping it in memory cache

	if db.UsePokemonCache {
		pokemonCache.Set(pokemon.Id, pokemon, pokemon.remainingDuration(now))
	}
}

// pokemonWriteDB performs the actual database INSERT or UPDATE for a Pokemon
// This is called by both direct writes and the write-behind queue
func pokemonWriteDB(db db.DbDetails, pokemon *Pokemon, isNewRecord bool) error {
	ctx := context.Background()

	if isNewRecord {
		// Always include PVP field for inserts if it's set
		pvpField, pvpValue := "", ""
		if pokemon.Pvp.Valid {
			pvpField, pvpValue = "pvp, ", ":pvp, "
		}
		_, err := db.PokemonDb.NamedExecContext(ctx, fmt.Sprintf("INSERT INTO pokemon (id, pokemon_id, lat, lon,"+
			"spawn_id, expire_timestamp, atk_iv, def_iv, sta_iv, golbat_internal, iv, move_1, move_2,"+
			"gender, form, cp, level, strong, weather, costume, weight, height, size,"+
			"display_pokemon_id, is_ditto, pokestop_id, updated, first_seen_timestamp, changed, cell_id,"+
			"expire_timestamp_verified, shiny, username, %s is_event, seen_type) "+
			"VALUES (\"%d\", :pokemon_id, :lat, :lon, :spawn_id, :expire_timestamp, :atk_iv, :def_iv, :sta_iv,"+
			":golbat_internal, :iv, :move_1, :move_2, :gender, :form, :cp, :level, :strong, :weather, :costume,"+
			":weight, :height, :size, :display_pokemon_id, :is_ditto, :pokestop_id, :updated,"+
			":first_seen_timestamp, :changed, :cell_id, :expire_timestamp_verified, :shiny, :username, %s :is_event,"+
			":seen_type)", pvpField, pokemon.Id, pvpValue), pokemon)

		statsCollector.IncDbQuery("insert pokemon", err)
		if err != nil {
			log.Errorf("insert pokemon: [%d] %s", pokemon.Id, err)
			pokemonCache.Delete(pokemon.Id)
			return err
		}
	} else {
		// Always include PVP field for updates if it's set
		pvpUpdate := ""
		if pokemon.Pvp.Valid {
			pvpUpdate = "pvp = :pvp, "
		}
		_, err := db.PokemonDb.NamedExecContext(ctx, fmt.Sprintf("UPDATE pokemon SET "+
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
			"WHERE id = \"%d\"", pvpUpdate, pokemon.Id), pokemon,
		)
		statsCollector.IncDbQuery("update pokemon", err)
		if err != nil {
			log.Errorf("Update pokemon [%d] %s", pokemon.Id, err)
			pokemonCache.Delete(pokemon.Id)
			return err
		}
	}
	return nil
}

type PokemonWebhook struct {
	SpawnpointId          string          `json:"spawnpoint_id"`
	PokestopId            string          `json:"pokestop_id"`
	PokestopName          *string         `json:"pokestop_name"`
	EncounterId           string          `json:"encounter_id"`
	PokemonId             int16           `json:"pokemon_id"`
	Latitude              float64         `json:"latitude"`
	Longitude             float64         `json:"longitude"`
	DisappearTime         int64           `json:"disappear_time"`
	DisappearTimeVerified bool            `json:"disappear_time_verified"`
	FirstSeen             int64           `json:"first_seen"`
	LastModifiedTime      null.Int        `json:"last_modified_time"`
	Gender                null.Int        `json:"gender"`
	Cp                    null.Int        `json:"cp"`
	Form                  null.Int        `json:"form"`
	Costume               null.Int        `json:"costume"`
	IndividualAttack      null.Int        `json:"individual_attack"`
	IndividualDefense     null.Int        `json:"individual_defense"`
	IndividualStamina     null.Int        `json:"individual_stamina"`
	PokemonLevel          null.Int        `json:"pokemon_level"`
	Move1                 null.Int        `json:"move_1"`
	Move2                 null.Int        `json:"move_2"`
	Weight                null.Float      `json:"weight"`
	Size                  null.Int        `json:"size"`
	Height                null.Float      `json:"height"`
	Weather               null.Int        `json:"weather"`
	Capture1              float64         `json:"capture_1"`
	Capture2              float64         `json:"capture_2"`
	Capture3              float64         `json:"capture_3"`
	Shiny                 null.Bool       `json:"shiny"`
	Username              null.String     `json:"username"`
	DisplayPokemonId      null.Int        `json:"display_pokemon_id"`
	IsEvent               int8            `json:"is_event"`
	SeenType              null.String     `json:"seen_type"`
	Pvp                   json.RawMessage `json:"pvp"`
}

func createPokemonWebhooks(ctx context.Context, db db.DbDetails, pokemon *Pokemon, areas []geo.AreaName) {
	if pokemon.isNewRecord() ||
		pokemon.oldValues.PokemonId != pokemon.PokemonId ||
		pokemon.oldValues.Weather != pokemon.Weather ||
		pokemon.oldValues.Cp != pokemon.Cp {

		spawnpointId := "None"
		if pokemon.SpawnId.Valid {
			spawnpointId = strconv.FormatInt(pokemon.SpawnId.ValueOrZero(), 16)
		}

		pokestopId := "None"
		if pokemon.PokestopId.Valid {
			pokestopId = pokemon.PokestopId.ValueOrZero()
		}

		var pokestopName *string
		if pokemon.PokestopId.Valid {
			pokestop, unlock, _ := getPokestopRecordReadOnly(ctx, db, pokemon.PokestopId.String)
			name := "Unknown"
			if pokestop != nil {
				name = pokestop.Name.ValueOrZero()
				unlock()
			}
			pokestopName = &name
		}

		var pvp json.RawMessage
		if pokemon.Pvp.Valid {
			pvp = json.RawMessage(pokemon.Pvp.ValueOrZero())
		}

		pokemonHook := PokemonWebhook{
			SpawnpointId:          spawnpointId,
			PokestopId:            pokestopId,
			PokestopName:          pokestopName,
			EncounterId:           strconv.FormatUint(pokemon.Id, 10),
			PokemonId:             pokemon.PokemonId,
			Latitude:              pokemon.Lat,
			Longitude:             pokemon.Lon,
			DisappearTime:         pokemon.ExpireTimestamp.ValueOrZero(),
			DisappearTimeVerified: pokemon.ExpireTimestampVerified,
			FirstSeen:             pokemon.FirstSeenTimestamp,
			LastModifiedTime:      pokemon.Updated,
			Gender:                pokemon.Gender,
			Cp:                    pokemon.Cp,
			Form:                  pokemon.Form,
			Costume:               pokemon.Costume,
			IndividualAttack:      pokemon.AtkIv,
			IndividualDefense:     pokemon.DefIv,
			IndividualStamina:     pokemon.StaIv,
			PokemonLevel:          pokemon.Level,
			Move1:                 pokemon.Move1,
			Move2:                 pokemon.Move2,
			Weight:                pokemon.Weight,
			Size:                  pokemon.Size,
			Height:                pokemon.Height,
			Weather:               pokemon.Weather,
			Capture1:              pokemon.Capture1.ValueOrZero(),
			Capture2:              pokemon.Capture2.ValueOrZero(),
			Capture3:              pokemon.Capture3.ValueOrZero(),
			Shiny:                 pokemon.Shiny,
			Username:              pokemon.Username,
			DisplayPokemonId:      pokemon.DisplayPokemonId,
			IsEvent:               pokemon.IsEvent,
			SeenType:              pokemon.SeenType,
			Pvp:                   pvp,
		}

		if pokemon.AtkIv.Valid && pokemon.DefIv.Valid && pokemon.StaIv.Valid {
			webhooksSender.AddMessage(webhooks.PokemonIV, pokemonHook, areas)
		} else {
			webhooksSender.AddMessage(webhooks.PokemonNoIV, pokemonHook, areas)
		}
	}
}
