package decoder

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"

	"golbat/db"
	"golbat/webhooks"
)

func loadIncidentFromDatabase(ctx context.Context, db db.DbDetails, incidentId string, incident *Incident) error {
	err := db.GeneralDb.GetContext(ctx, incident,
		"SELECT id, pokestop_id, start, expiration, display_type, style, `character`, updated, confirmed, slot_1_pokemon_id, slot_1_form, slot_2_pokemon_id, slot_2_form, slot_3_pokemon_id, slot_3_form "+
			"FROM incident WHERE incident.id = ?", incidentId)
	statsCollector.IncDbQuery("select incident", err)
	return err
}

// peekIncidentRecord - cache-only lookup, no DB fallback, returns locked.
// Caller MUST call returned unlock function if non-nil.
func peekIncidentRecord(incidentId string) (*Incident, func(), error) {
	if item := incidentCache.Get(incidentId); item != nil {
		incident := item.Value()
		incident.Lock()
		return incident, func() { incident.Unlock() }, nil
	}
	return nil, nil, nil
}

// getIncidentRecordReadOnly acquires lock but does NOT take snapshot.
// Use for read-only checks. Will cause a backing database lookup.
// Caller MUST call returned unlock function if non-nil.
func getIncidentRecordReadOnly(ctx context.Context, db db.DbDetails, incidentId string) (*Incident, func(), error) {
	// Check cache first
	if item := incidentCache.Get(incidentId); item != nil {
		incident := item.Value()
		incident.Lock()
		return incident, func() { incident.Unlock() }, nil
	}

	dbIncident := Incident{}
	err := loadIncidentFromDatabase(ctx, db, incidentId, &dbIncident)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	dbIncident.ClearDirty()

	// Atomically cache the loaded Incident - if another goroutine raced us,
	// we'll get their Incident and use that instead (ensuring same mutex)
	existingIncident, _ := incidentCache.GetOrSetFunc(incidentId, func() *Incident {
		return &dbIncident
	})

	incident := existingIncident.Value()
	incident.Lock()
	return incident, func() { incident.Unlock() }, nil
}

// getIncidentRecordForUpdate acquires lock AND takes snapshot for webhook comparison.
// Caller MUST call returned unlock function if non-nil.
func getIncidentRecordForUpdate(ctx context.Context, db db.DbDetails, incidentId string) (*Incident, func(), error) {
	incident, unlock, err := getIncidentRecordReadOnly(ctx, db, incidentId)
	if err != nil || incident == nil {
		return nil, nil, err
	}
	incident.snapshotOldValues()
	return incident, unlock, nil
}

// getOrCreateIncidentRecord gets existing or creates new, locked with snapshot.
// Caller MUST call returned unlock function.
func getOrCreateIncidentRecord(ctx context.Context, db db.DbDetails, incidentId string, pokestopId string) (*Incident, func(), error) {
	// Create new Incident atomically - function only called if key doesn't exist
	incidentItem, _ := incidentCache.GetOrSetFunc(incidentId, func() *Incident {
		return &Incident{Id: incidentId, PokestopId: pokestopId, newRecord: true}
	})

	incident := incidentItem.Value()
	incident.Lock()

	if incident.newRecord {
		// We should attempt to load from database
		err := loadIncidentFromDatabase(ctx, db, incidentId, incident)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				incident.Unlock()
				return nil, nil, err
			}
		} else {
			// We loaded from DB
			incident.newRecord = false
			incident.ClearDirty()
		}
	}

	incident.snapshotOldValues()
	return incident, func() { incident.Unlock() }, nil
}

func saveIncidentRecord(ctx context.Context, db db.DbDetails, incident *Incident) {
	// Skip save if not dirty and not new
	if !incident.IsDirty() && !incident.IsNewRecord() {
		return
	}

	incident.SetUpdated(time.Now().Unix())

	if incident.IsNewRecord() {
		res, err := db.GeneralDb.NamedExec("INSERT INTO incident (id, pokestop_id, start, expiration, display_type, style, `character`, updated, confirmed, slot_1_pokemon_id, slot_1_form, slot_2_pokemon_id, slot_2_form, slot_3_pokemon_id, slot_3_form) "+
			"VALUES (:id, :pokestop_id, :start, :expiration, :display_type, :style, :character, :updated, :confirmed, :slot_1_pokemon_id, :slot_1_form, :slot_2_pokemon_id, :slot_2_form, :slot_3_pokemon_id, :slot_3_form)", incident)

		if err != nil {
			log.Errorf("insert incident: %s", err)
			return
		}
		statsCollector.IncDbQuery("insert incident", err)
		_, _ = res, err
	} else {
		res, err := db.GeneralDb.NamedExec("UPDATE incident SET "+
			"start = :start, "+
			"expiration = :expiration, "+
			"display_type = :display_type, "+
			"style = :style, "+
			"`character` = :character, "+
			"updated = :updated, "+
			"confirmed = :confirmed, "+
			"slot_1_pokemon_id = :slot_1_pokemon_id, "+
			"slot_1_form = :slot_1_form, "+
			"slot_2_pokemon_id = :slot_2_pokemon_id, "+
			"slot_2_form = :slot_2_form, "+
			"slot_3_pokemon_id = :slot_3_pokemon_id, "+
			"slot_3_form = :slot_3_form "+
			"WHERE id = :id", incident,
		)
		statsCollector.IncDbQuery("update incident", err)
		if err != nil {
			log.Errorf("Update incident %s", err)
		}
		_, _ = res, err
	}

	createIncidentWebhooks(ctx, db, incident)

	var stopLat, stopLon float64
	stop, unlock, _ := getPokestopRecordReadOnly(ctx, db, incident.PokestopId)
	if stop != nil {
		stopLat, stopLon = stop.Lat, stop.Lon
		unlock()
	}

	areas := MatchStatsGeofence(stopLat, stopLon)
	updateIncidentStats(incident, areas)

	incident.ClearDirty()
	if incident.IsNewRecord() {
		incident.newRecord = false
		incidentCache.Set(incident.Id, incident, ttlcache.DefaultTTL)
	}
}

func createIncidentWebhooks(ctx context.Context, db db.DbDetails, incident *Incident) {
	old := &incident.oldValues
	isNew := incident.IsNewRecord()

	if isNew || (old.ExpirationTime != incident.ExpirationTime || old.Character != incident.Character || old.Confirmed != incident.Confirmed || old.Slot1PokemonId != incident.Slot1PokemonId) {
		var pokestopName, stopUrl string
		var stopLat, stopLon float64
		var stopEnabled bool
		stop, unlock, _ := getPokestopRecordReadOnly(ctx, db, incident.PokestopId)
		if stop != nil {
			pokestopName = stop.Name.ValueOrZero()
			stopLat, stopLon = stop.Lat, stop.Lon
			stopUrl = stop.Url.ValueOrZero()
			stopEnabled = stop.Enabled.ValueOrZero()
			unlock()
		}
		if pokestopName == "" {
			pokestopName = "Unknown"
		}

		var lineup []webhookLineup
		if incident.Slot1PokemonId.Valid {
			lineup = []webhookLineup{
				{
					Slot:      1,
					PokemonId: incident.Slot1PokemonId,
					Form:      incident.Slot1Form,
				},
				{
					Slot:      2,
					PokemonId: incident.Slot2PokemonId,
					Form:      incident.Slot2Form,
				},
				{
					Slot:      3,
					PokemonId: incident.Slot3PokemonId,
					Form:      incident.Slot3Form,
				},
			}
		}

		incidentHook := IncidentWebhook{
			Id:                      incident.Id,
			PokestopId:              incident.PokestopId,
			Latitude:                stopLat,
			Longitude:               stopLon,
			PokestopName:            pokestopName,
			Url:                     stopUrl,
			Enabled:                 stopEnabled,
			Start:                   incident.StartTime,
			IncidentExpireTimestamp: incident.ExpirationTime,
			Expiration:              incident.ExpirationTime,
			DisplayType:             incident.DisplayType,
			Style:                   incident.Style,
			GruntType:               incident.Character,
			Character:               incident.Character,
			Updated:                 incident.Updated,
			Confirmed:               incident.Confirmed,
			Lineup:                  lineup,
		}

		areas := MatchStatsGeofence(stopLat, stopLon)
		webhooksSender.AddMessage(webhooks.Invasion, incidentHook, areas)
		statsCollector.UpdateIncidentCount(areas)
	}
}
