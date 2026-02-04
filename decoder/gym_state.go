package decoder

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/guregu/null/v6"
	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"

	"golbat/config"
	"golbat/db"
	"golbat/geo"
	"golbat/webhooks"
)

// gymSelectColumns defines the columns for gym queries.
// Used by both single-row and bulk load queries to keep them in sync.
const gymSelectColumns = `id, lat, lon, name, url, last_modified_timestamp, raid_end_timestamp, raid_spawn_timestamp,
	raid_battle_timestamp, updated, raid_pokemon_id, guarding_pokemon_id, guarding_pokemon_display,
	available_slots, team_id, raid_level, enabled, ex_raid_eligible, in_battle, raid_pokemon_move_1,
	raid_pokemon_move_2, raid_pokemon_form, raid_pokemon_alignment, raid_pokemon_cp, raid_is_exclusive,
	cell_id, deleted, total_cp, first_seen_timestamp, raid_pokemon_gender, sponsor_id, partner_id,
	raid_pokemon_costume, raid_pokemon_evolution, ar_scan_eligible, power_up_level, power_up_points,
	power_up_end_timestamp, description, defenders, rsvps`

func loadGymFromDatabase(ctx context.Context, db db.DbDetails, fortId string, gym *Gym) error {
	err := db.GeneralDb.GetContext(ctx, gym, "SELECT "+gymSelectColumns+" FROM gym WHERE id = ?", fortId)
	statsCollector.IncDbQuery("select gym", err)
	return err
}

// PeekGymRecord - cache-only lookup, no DB fallback, returns locked.
// Caller MUST call returned unlock function if non-nil.
func PeekGymRecord(fortId string) (*Gym, func(), error) {
	if item := gymCache.Get(fortId); item != nil {
		gym := item.Value()
		gym.Lock()
		return gym, func() { gym.Unlock() }, nil
	}
	return nil, nil, nil
}

// GetGymRecordReadOnly acquires lock but does NOT take snapshot.
// Use for read-only checks. Will cause a backing database lookup.
// Caller MUST call returned unlock function if non-nil.
func GetGymRecordReadOnly(ctx context.Context, db db.DbDetails, fortId string) (*Gym, func(), error) {
	// Check cache first
	if item := gymCache.Get(fortId); item != nil {
		gym := item.Value()
		gym.Lock()
		return gym, func() { gym.Unlock() }, nil
	}

	dbGym := Gym{}
	err := loadGymFromDatabase(ctx, db, fortId, &dbGym)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	dbGym.ClearDirty()

	// Atomically cache the loaded Gym - if another goroutine raced us,
	// we'll get their Gym and use that instead (ensuring same mutex)
	existingGym, _ := gymCache.GetOrSetFunc(fortId, func() *Gym {
		// Only called if key doesn't exist - our Pokestop wins
		if config.Config.FortInMemory {
			fortRtreeUpdateGymOnGet(&dbGym)
		}
		return &dbGym
	})

	gym := existingGym.Value()
	gym.Lock()
	return gym, func() { gym.Unlock() }, nil
}

// getGymRecordForUpdate acquires lock AND takes snapshot for webhook comparison.
// Use when modifying the Gym.
// Caller MUST call returned unlock function if non-nil.
func getGymRecordForUpdate(ctx context.Context, db db.DbDetails, fortId string) (*Gym, func(), error) {
	gym, unlock, err := GetGymRecordReadOnly(ctx, db, fortId)
	if err != nil || gym == nil {
		return nil, nil, err
	}
	gym.snapshotOldValues()
	return gym, unlock, nil
}

// getOrCreateGymRecord gets existing or creates new, locked with snapshot.
// Caller MUST call returned unlock function.
func getOrCreateGymRecord(ctx context.Context, db db.DbDetails, fortId string) (*Gym, func(), error) {
	// Create new Gym atomically - function only called if key doesn't exist
	gymItem, _ := gymCache.GetOrSetFunc(fortId, func() *Gym {
		return &Gym{Id: fortId, newRecord: true}
	})

	gym := gymItem.Value()
	gym.Lock()

	if gym.newRecord {
		// We should attempt to load from database
		err := loadGymFromDatabase(ctx, db, fortId, gym)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				gym.Unlock()
				return nil, nil, err
			}
		} else {
			// We loaded from DB
			gym.newRecord = false
			gym.ClearDirty()
			if config.Config.FortInMemory {
				fortRtreeUpdateGymOnGet(gym)
			}
		}
	}

	gym.snapshotOldValues()
	return gym, func() { gym.Unlock() }, nil
}

// hasChangesGym compares two Gym structs
// Float tolerance: Lat, Lon
func hasChangesGym(old *Gym, new *Gym) bool {
	return old.Id != new.Id ||
		old.Name != new.Name ||
		old.Url != new.Url ||
		old.LastModifiedTimestamp != new.LastModifiedTimestamp ||
		old.RaidEndTimestamp != new.RaidEndTimestamp ||
		old.RaidSpawnTimestamp != new.RaidSpawnTimestamp ||
		old.RaidBattleTimestamp != new.RaidBattleTimestamp ||
		old.Updated != new.Updated ||
		old.RaidPokemonId != new.RaidPokemonId ||
		old.GuardingPokemonId != new.GuardingPokemonId ||
		old.AvailableSlots != new.AvailableSlots ||
		old.TeamId != new.TeamId ||
		old.RaidLevel != new.RaidLevel ||
		old.Enabled != new.Enabled ||
		old.ExRaidEligible != new.ExRaidEligible ||
		//		old.InBattle != new.InBattle ||
		old.RaidPokemonMove1 != new.RaidPokemonMove1 ||
		old.RaidPokemonMove2 != new.RaidPokemonMove2 ||
		old.RaidPokemonForm != new.RaidPokemonForm ||
		old.RaidPokemonAlignment != new.RaidPokemonAlignment ||
		old.RaidPokemonCp != new.RaidPokemonCp ||
		old.RaidIsExclusive != new.RaidIsExclusive ||
		old.CellId != new.CellId ||
		old.Deleted != new.Deleted ||
		old.TotalCp != new.TotalCp ||
		old.FirstSeenTimestamp != new.FirstSeenTimestamp ||
		old.RaidPokemonGender != new.RaidPokemonGender ||
		old.SponsorId != new.SponsorId ||
		old.PartnerId != new.PartnerId ||
		old.RaidPokemonCostume != new.RaidPokemonCostume ||
		old.RaidPokemonEvolution != new.RaidPokemonEvolution ||
		old.ArScanEligible != new.ArScanEligible ||
		old.PowerUpLevel != new.PowerUpLevel ||
		old.PowerUpPoints != new.PowerUpPoints ||
		old.PowerUpEndTimestamp != new.PowerUpEndTimestamp ||
		old.Description != new.Description ||
		old.Rsvps != new.Rsvps ||
		!floatAlmostEqual(old.Lat, new.Lat, floatTolerance) ||
		!floatAlmostEqual(old.Lon, new.Lon, floatTolerance)

}

// hasChangesInternalGym compares two Gym structs for changes that will be stored in memory
// Float tolerance: Lat, Lon
func hasInternalChangesGym(old *Gym, new *Gym) bool {
	return old.InBattle != new.InBattle ||
		old.Defenders != new.Defenders
}

type GymDetailsWebhook struct {
	Id                  string  `json:"id"`
	Name                string  `json:"name"`
	Url                 string  `json:"url"`
	Latitude            float64 `json:"latitude"`
	Longitude           float64 `json:"longitude"`
	Team                int64   `json:"team"`
	GuardPokemonId      int64   `json:"guard_pokemon_id"`
	SlotsAvailable      int64   `json:"slots_available"`
	ExRaidEligible      int64   `json:"ex_raid_eligible"`
	InBattle            bool    `json:"in_battle"`
	SponsorId           int64   `json:"sponsor_id"`
	PartnerId           int64   `json:"partner_id"`
	PowerUpPoints       int64   `json:"power_up_points"`
	PowerUpLevel        int64   `json:"power_up_level"`
	PowerUpEndTimestamp int64   `json:"power_up_end_timestamp"`
	ArScanEligible      int64   `json:"ar_scan_eligible"`
	Defenders           any     `json:"defenders"`
}

type RaidWebhook struct {
	GymId               string          `json:"gym_id"`
	GymName             string          `json:"gym_name"`
	GymUrl              string          `json:"gym_url"`
	Latitude            float64         `json:"latitude"`
	Longitude           float64         `json:"longitude"`
	TeamId              int64           `json:"team_id"`
	Spawn               int64           `json:"spawn"`
	Start               int64           `json:"start"`
	End                 int64           `json:"end"`
	Level               int64           `json:"level"`
	PokemonId           int64           `json:"pokemon_id"`
	Cp                  int64           `json:"cp"`
	Gender              int64           `json:"gender"`
	Form                int64           `json:"form"`
	Alignment           int64           `json:"alignment"`
	Costume             int64           `json:"costume"`
	Evolution           int64           `json:"evolution"`
	Move1               int64           `json:"move_1"`
	Move2               int64           `json:"move_2"`
	ExRaidEligible      int64           `json:"ex_raid_eligible"`
	IsExclusive         int64           `json:"is_exclusive"`
	SponsorId           int64           `json:"sponsor_id"`
	PartnerId           string          `json:"partner_id"`
	PowerUpPoints       int64           `json:"power_up_points"`
	PowerUpLevel        int64           `json:"power_up_level"`
	PowerUpEndTimestamp int64           `json:"power_up_end_timestamp"`
	ArScanEligible      int64           `json:"ar_scan_eligible"`
	Rsvps               json.RawMessage `json:"rsvps"`
	RaidSeed            null.String     `json:"raid_seed"`
}

func createGymFortWebhooks(gym *Gym) {
	fort := InitWebHookFortFromGym(gym)
	if gym.newRecord {
		CreateFortWebHooks(nil, fort, NEW)
	} else {
		// Build old fort from saved old values
		oldFort := &FortWebhook{
			Type:        GYM.String(),
			Id:          gym.Id,
			Name:        gym.oldValues.Name.Ptr(),
			ImageUrl:    gym.oldValues.Url.Ptr(),
			Description: gym.oldValues.Description.Ptr(),
			Location:    Location{Latitude: gym.oldValues.Lat, Longitude: gym.oldValues.Lon},
		}
		CreateFortWebHooks(oldFort, fort, EDIT)
	}
}

func createGymWebhooks(gym *Gym, areas []geo.AreaName) {
	if gym.newRecord ||
		(gym.oldValues.AvailableSlots != gym.AvailableSlots || gym.oldValues.TeamId != gym.TeamId || gym.oldValues.InBattle != gym.InBattle) {
		gymDetails := GymDetailsWebhook{
			Id:             gym.Id,
			Name:           gym.Name.ValueOrZero(),
			Url:            gym.Url.ValueOrZero(),
			Latitude:       gym.Lat,
			Longitude:      gym.Lon,
			Team:           gym.TeamId.ValueOrZero(),
			GuardPokemonId: gym.GuardingPokemonId.ValueOrZero(),
			SlotsAvailable: func() int64 {
				if gym.AvailableSlots.Valid {
					return gym.AvailableSlots.Int64
				} else {
					return 6
				}
			}(),
			ExRaidEligible: gym.ExRaidEligible.ValueOrZero(),
			InBattle:       func() bool { return gym.InBattle.ValueOrZero() != 0 }(),
			Defenders: func() any {
				if gym.Defenders.Valid {
					return json.RawMessage(gym.Defenders.ValueOrZero())
				} else {
					return nil
				}
			}(),
		}

		webhooksSender.AddMessage(webhooks.GymDetails, gymDetails, areas)
	}

	if gym.RaidSpawnTimestamp.ValueOrZero() > 0 &&
		(gym.newRecord || gym.oldValues.RaidLevel != gym.RaidLevel ||
			gym.oldValues.RaidPokemonId != gym.RaidPokemonId ||
			gym.oldValues.RaidSpawnTimestamp != gym.RaidSpawnTimestamp || gym.oldValues.Rsvps != gym.Rsvps) {
		raidBattleTime := gym.RaidBattleTimestamp.ValueOrZero()
		raidEndTime := gym.RaidEndTimestamp.ValueOrZero()
		now := time.Now().Unix()

		if (raidBattleTime > now && gym.RaidLevel.ValueOrZero() > 0) ||
			(raidEndTime > now && gym.RaidPokemonId.ValueOrZero() > 0) {
			gymName := "Unknown"
			if gym.Name.Valid {
				gymName = gym.Name.String
			}

			var rsvps json.RawMessage
			if gym.Rsvps.Valid {
				rsvps = json.RawMessage(gym.Rsvps.ValueOrZero())
			}

			raidHook := RaidWebhook{
				GymId:               gym.Id,
				GymName:             gymName,
				GymUrl:              gym.Url.ValueOrZero(),
				Latitude:            gym.Lat,
				Longitude:           gym.Lon,
				TeamId:              gym.TeamId.ValueOrZero(),
				Spawn:               gym.RaidSpawnTimestamp.ValueOrZero(),
				Start:               gym.RaidBattleTimestamp.ValueOrZero(),
				End:                 gym.RaidEndTimestamp.ValueOrZero(),
				Level:               gym.RaidLevel.ValueOrZero(),
				PokemonId:           gym.RaidPokemonId.ValueOrZero(),
				Cp:                  gym.RaidPokemonCp.ValueOrZero(),
				Gender:              gym.RaidPokemonGender.ValueOrZero(),
				Form:                gym.RaidPokemonForm.ValueOrZero(),
				Alignment:           gym.RaidPokemonAlignment.ValueOrZero(),
				Costume:             gym.RaidPokemonCostume.ValueOrZero(),
				Evolution:           gym.RaidPokemonEvolution.ValueOrZero(),
				Move1:               gym.RaidPokemonMove1.ValueOrZero(),
				Move2:               gym.RaidPokemonMove2.ValueOrZero(),
				ExRaidEligible:      gym.ExRaidEligible.ValueOrZero(),
				IsExclusive:         gym.RaidIsExclusive.ValueOrZero(),
				SponsorId:           gym.SponsorId.ValueOrZero(),
				PartnerId:           gym.PartnerId.ValueOrZero(),
				PowerUpPoints:       gym.PowerUpPoints.ValueOrZero(),
				PowerUpLevel:        gym.PowerUpLevel.ValueOrZero(),
				PowerUpEndTimestamp: gym.PowerUpEndTimestamp.ValueOrZero(),
				ArScanEligible:      gym.ArScanEligible.ValueOrZero(),
				Rsvps:               rsvps,
				RaidSeed:            gym.RaidSeed,
			}

			webhooksSender.AddMessage(webhooks.Raid, raidHook, areas)
			statsCollector.UpdateRaidCount(areas, gym.RaidLevel.ValueOrZero())
		}
	}
}

func saveGymRecord(ctx context.Context, db db.DbDetails, gym *Gym) {
	now := time.Now().Unix()
	if !gym.IsNewRecord() && !gym.IsDirty() && !gym.IsInternalDirty() {
		// default debounce is 15 minutes (900s). If reduce_updates is enabled, use 12 hours.
		if gym.Updated > now-GetUpdateThreshold(900) {
			// if a gym is unchanged and was seen recently, skip saving
			return
		}
	}
	gym.SetUpdated(now)

	// Capture isNewRecord before state changes
	isNewRecord := gym.IsNewRecord()

	// Debug logging before queueing
	if dbDebugEnabled {
		if gym.IsDirty() {
			if isNewRecord {
				dbDebugLog("INSERT", "Gym", gym.Id, gym.changedFields)
			} else {
				dbDebugLog("UPDATE", "Gym", gym.Id, gym.changedFields)
			}
		} else {
			dbDebugLog("MEMORY", "Gym", gym.Id, gym.changedFields)
		}
	}

	if gym.IsDirty() {
		// Queue the write through the write-behind system
		if writeBehindQueue != nil {
			writeBehindQueue.Enqueue(gym, isNewRecord, 0)
		} else {
			// Fallback to direct write if queue not initialized
			_ = gymWriteDB(db, gym, isNewRecord)
		}
	}

	if config.Config.FortInMemory {
		fortRtreeUpdateGymOnSave(gym)
	}

	areas := MatchStatsGeofence(gym.Lat, gym.Lon)
	createGymWebhooks(gym, areas)
	createGymFortWebhooks(gym)
	updateRaidStats(gym, areas)
	if dbDebugEnabled {
		gym.changedFields = gym.changedFields[:0]
	}
	if isNewRecord {
		gymCache.Set(gym.Id, gym, ttlcache.DefaultTTL)
		gym.newRecord = false
	}
	gym.ClearDirty()
}

// gymWriteDB performs the actual database INSERT/UPDATE for a Gym
// This is called by both direct writes and the write-behind queue
func gymWriteDB(db db.DbDetails, gym *Gym, isNewRecord bool) error {
	ctx := context.Background()

	if isNewRecord {
		res, err := db.GeneralDb.NamedExecContext(ctx, "INSERT INTO gym (id,lat,lon,name,url,last_modified_timestamp,raid_end_timestamp,raid_spawn_timestamp,raid_battle_timestamp,updated,raid_pokemon_id,guarding_pokemon_id,guarding_pokemon_display,available_slots,team_id,raid_level,enabled,ex_raid_eligible,in_battle,raid_pokemon_move_1,raid_pokemon_move_2,raid_pokemon_form,raid_pokemon_alignment,raid_pokemon_cp,raid_is_exclusive,cell_id,deleted,total_cp,first_seen_timestamp,raid_pokemon_gender,sponsor_id,partner_id,raid_pokemon_costume,raid_pokemon_evolution,ar_scan_eligible,power_up_level,power_up_points,power_up_end_timestamp,description, defenders, rsvps) "+
			"VALUES (:id,:lat,:lon,:name,:url,UNIX_TIMESTAMP(),:raid_end_timestamp,:raid_spawn_timestamp,:raid_battle_timestamp,:updated,:raid_pokemon_id,:guarding_pokemon_id,:guarding_pokemon_display,:available_slots,:team_id,:raid_level,:enabled,:ex_raid_eligible,:in_battle,:raid_pokemon_move_1,:raid_pokemon_move_2,:raid_pokemon_form,:raid_pokemon_alignment,:raid_pokemon_cp,:raid_is_exclusive,:cell_id,0,:total_cp,UNIX_TIMESTAMP(),:raid_pokemon_gender,:sponsor_id,:partner_id,:raid_pokemon_costume,:raid_pokemon_evolution,:ar_scan_eligible,:power_up_level,:power_up_points,:power_up_end_timestamp,:description, :defenders, :rsvps)", gym)

		statsCollector.IncDbQuery("insert gym", err)
		if err != nil {
			log.Errorf("insert gym: %s", err)
			return err
		}
		_, _ = res, err
	} else {
		res, err := db.GeneralDb.NamedExecContext(ctx, "UPDATE gym SET "+
			"lat = :lat, "+
			"lon = :lon, "+
			"name = :name, "+
			"url = :url, "+
			"last_modified_timestamp = :last_modified_timestamp, "+
			"raid_end_timestamp = :raid_end_timestamp, "+
			"raid_spawn_timestamp = :raid_spawn_timestamp, "+
			"raid_battle_timestamp = :raid_battle_timestamp, "+
			"updated = :updated, "+
			"raid_pokemon_id = :raid_pokemon_id, "+
			"guarding_pokemon_id = :guarding_pokemon_id, "+
			"guarding_pokemon_display = :guarding_pokemon_display, "+
			"available_slots = :available_slots, "+
			"team_id = :team_id, "+
			"raid_level = :raid_level, "+
			"enabled = :enabled, "+
			"ex_raid_eligible = :ex_raid_eligible, "+
			"in_battle = :in_battle, "+
			"raid_pokemon_move_1 = :raid_pokemon_move_1, "+
			"raid_pokemon_move_2 = :raid_pokemon_move_2, "+
			"raid_pokemon_form = :raid_pokemon_form, "+
			"raid_pokemon_alignment = :raid_pokemon_alignment, "+
			"raid_pokemon_cp = :raid_pokemon_cp, "+
			"raid_is_exclusive = :raid_is_exclusive, "+
			"cell_id = :cell_id, "+
			"deleted = :deleted, "+
			"total_cp = :total_cp, "+
			"raid_pokemon_gender = :raid_pokemon_gender, "+
			"sponsor_id = :sponsor_id, "+
			"partner_id = :partner_id, "+
			"raid_pokemon_costume = :raid_pokemon_costume, "+
			"raid_pokemon_evolution = :raid_pokemon_evolution, "+
			"ar_scan_eligible = :ar_scan_eligible, "+
			"power_up_level = :power_up_level, "+
			"power_up_points = :power_up_points, "+
			"power_up_end_timestamp = :power_up_end_timestamp,"+
			"description = :description,"+
			"defenders = :defenders,"+
			"rsvps = :rsvps "+
			"WHERE id = :id", gym,
		)
		statsCollector.IncDbQuery("update gym", err)
		if err != nil {
			log.Errorf("Update gym %s", err)
			return err
		}
		_, _ = res, err
	}
	return nil
}

func updateGymGetMapFortCache(gym *Gym, skipName bool) {
	storedGetMapFort := getMapFortsCache.Get(gym.Id)
	if storedGetMapFort != nil {
		getMapFort := storedGetMapFort.Value()
		getMapFortsCache.Delete(gym.Id)
		gym.updateGymFromGetMapFortsOutProto(getMapFort, skipName)
		log.Debugf("Updated Gym using stored getMapFort: %s", gym.Id)
	}
}
