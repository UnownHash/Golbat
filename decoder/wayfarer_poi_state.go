package decoder

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"golbat/db"

	"golbat/ottercache"
)

const wayfarerPoiSelectColumns = `id, lat, lon, name, description, url, in_game, first_seen_timestamp, updated`

func loadWayfarerPoiFromDatabase(ctx context.Context, db db.DbDetails, poiId string, poi *WayfarerPoi) error {
	return timedDbQuery("loadWayfarerPoiFromDatabase", db.GeneralDb, func() error {
		err := db.GeneralDb.GetContext(ctx, &poi.WayfarerPoiData,
			`SELECT `+wayfarerPoiSelectColumns+` FROM wayfarer_poi WHERE id = ?`, poiId)
		getStatsCollector().IncDbQuery("select wayfarer_poi", err)
		return err
	})
}

// getOrCreateWayfarerPoiRecord gets existing or creates new, locked with snapshot.
// Caller MUST call returned unlock function.
func getOrCreateWayfarerPoiRecord(ctx context.Context, db db.DbDetails, poiId string, caller string) (*WayfarerPoi, func(), error) {
	// Create new WayfarerPoi atomically - function only called if key doesn't exist
	poi, _ := wayfarerPoiCache.GetOrSetFunc(poiId, func() *WayfarerPoi {
		return &WayfarerPoi{WayfarerPoiData: WayfarerPoiData{Id: poiId}, newRecord: true}
	})
	poi.Lock(caller)

	if poi.newRecord {
		// We should attempt to load from database
		err := loadWayfarerPoiFromDatabase(ctx, db, poiId, poi)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				poi.Unlock()
				return nil, nil, err
			}
		} else {
			// We loaded from DB
			poi.newRecord = false
			poi.ClearDirty()
		}
	}

	poi.snapshotOldValues()
	return poi, func() { poi.Unlock() }, nil
}

func saveWayfarerPoiRecord(ctx context.Context, db db.DbDetails, poi *WayfarerPoi) error {
	now := time.Now().Unix()
	// Skip save if not dirty and not new, unless the 15-minute debounce expired:
	// `updated` then records that the POI is still being returned
	if !poi.IsDirty() && !poi.IsNewRecord() {
		if poi.Updated > now-GetUpdateThreshold(900) {
			return nil
		}
	}

	poi.SetUpdated(now)
	if poi.FirstSeenTimestamp == 0 {
		poi.SetFirstSeenTimestamp(now)
	}

	// Capture isNewRecord before state changes
	isNewRecord := poi.IsNewRecord()

	if dbDebugEnabled {
		if isNewRecord {
			dbDebugLog("INSERT", "WayfarerPoi", poi.Id, poi.debug.fields())
		} else {
			dbDebugLog("UPDATE", "WayfarerPoi", poi.Id, poi.debug.fields())
		}
	}

	// Queue the write through the typed write-behind queue
	if wayfarerPoiQueue != nil {
		wayfarerPoiQueue.Enqueue(poi.WayfarerPoiData, isNewRecord, 0)
	} else {
		// Fallback to direct write if queue not initialized
		if err := flushWayfarerPoiBatch(ctx, db, []WayfarerPoiData{poi.WayfarerPoiData}); err != nil {
			return fmt.Errorf("save wayfarer_poi error: %w", err)
		}
	}

	if dbDebugEnabled {
		poi.debug.reset()
	}
	poi.ClearDirty()
	wayfarerPoiWebhookOnSave(poi, isNewRecord)
	if isNewRecord {
		wayfarerPoiCache.Set(poi.Id, poi, ottercache.DefaultTTL)
		poi.newRecord = false
	}
	return nil
}
