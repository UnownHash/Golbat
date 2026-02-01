package decoder

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"

	"golbat/db"
)

func loadTappableFromDatabase(ctx context.Context, db db.DbDetails, id uint64, tappable *Tappable) error {
	err := db.GeneralDb.GetContext(ctx, tappable,
		`SELECT id, lat, lon, fort_id, spawn_id, type, pokemon_id, item_id, count, expire_timestamp, expire_timestamp_verified, updated
         FROM tappable WHERE id = ?`, strconv.FormatUint(id, 10))
	statsCollector.IncDbQuery("select tappable", err)
	return err
}

// PeekTappableRecord - cache-only lookup, no DB fallback, returns locked.
// Caller MUST call returned unlock function if non-nil.
func PeekTappableRecord(id uint64) (*Tappable, func(), error) {
	if item := tappableCache.Get(id); item != nil {
		tappable := item.Value()
		tappable.Lock()
		return tappable, func() { tappable.Unlock() }, nil
	}
	return nil, nil, nil
}

// getTappableRecordReadOnly acquires lock but does NOT take snapshot.
// Use for read-only checks. Will cause a backing database lookup.
// Caller MUST call returned unlock function if non-nil.
func getTappableRecordReadOnly(ctx context.Context, db db.DbDetails, id uint64) (*Tappable, func(), error) {
	// Check cache first
	if item := tappableCache.Get(id); item != nil {
		tappable := item.Value()
		tappable.Lock()
		return tappable, func() { tappable.Unlock() }, nil
	}

	dbTappable := Tappable{}
	err := loadTappableFromDatabase(ctx, db, id, &dbTappable)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	dbTappable.ClearDirty()

	// Atomically cache the loaded Tappable - if another goroutine raced us,
	// we'll get their Tappable and use that instead (ensuring same mutex)
	existingTappable, _ := tappableCache.GetOrSetFunc(id, func() *Tappable {
		return &dbTappable
	})

	tappable := existingTappable.Value()
	tappable.Lock()
	return tappable, func() { tappable.Unlock() }, nil
}

// getOrCreateTappableRecord gets existing or creates new, locked.
// Caller MUST call returned unlock function.
func getOrCreateTappableRecord(ctx context.Context, db db.DbDetails, id uint64) (*Tappable, func(), error) {
	// Create new Tappable atomically - function only called if key doesn't exist
	tappableItem, _ := tappableCache.GetOrSetFunc(id, func() *Tappable {
		return &Tappable{Id: id, newRecord: true}
	})

	tappable := tappableItem.Value()
	tappable.Lock()

	if tappable.newRecord {
		// We should attempt to load from database
		err := loadTappableFromDatabase(ctx, db, id, tappable)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				tappable.Unlock()
				return nil, nil, err
			}
		} else {
			// We loaded from DB
			tappable.newRecord = false
			tappable.ClearDirty()
		}
	}

	return tappable, func() { tappable.Unlock() }, nil
}

func saveTappableRecord(ctx context.Context, details db.DbDetails, tappable *Tappable) {
	// Skip save if not dirty and not new
	if !tappable.IsDirty() && !tappable.IsNewRecord() {
		return
	}

	now := time.Now().Unix()
	tappable.SetUpdated(now)

	if tappable.IsNewRecord() {
		if dbDebugEnabled {
			dbDebugLog("INSERT", "Tappable", strconv.FormatUint(tappable.Id, 10), tappable.changedFields)
		}
		res, err := details.GeneralDb.NamedExecContext(ctx, fmt.Sprintf(`
			INSERT INTO tappable (
				id, lat, lon, fort_id, spawn_id, type, pokemon_id, item_id, count, expire_timestamp, expire_timestamp_verified, updated
			) VALUES (
				"%d", :lat, :lon, :fort_id, :spawn_id, :type, :pokemon_id, :item_id, :count, :expire_timestamp, :expire_timestamp_verified, :updated
			)
			`, tappable.Id), tappable)
		statsCollector.IncDbQuery("insert tappable", err)
		if err != nil {
			log.Errorf("insert tappable %d: %s", tappable.Id, err)
			return
		}
		_ = res
	} else {
		if dbDebugEnabled {
			dbDebugLog("UPDATE", "Tappable", strconv.FormatUint(tappable.Id, 10), tappable.changedFields)
		}
		res, err := details.GeneralDb.NamedExecContext(ctx, fmt.Sprintf(`
			UPDATE tappable SET
				lat = :lat,
				lon = :lon,
				fort_id = :fort_id,
				spawn_id = :spawn_id,
				type = :type,
				pokemon_id = :pokemon_id,
				item_id = :item_id,
				count = :count,
				expire_timestamp = :expire_timestamp,
				expire_timestamp_verified = :expire_timestamp_verified,
				updated = :updated
			WHERE id = "%d"
			`, tappable.Id), tappable)
		statsCollector.IncDbQuery("update tappable", err)
		if err != nil {
			log.Errorf("update tappable %d: %s", tappable.Id, err)
			return
		}
		_ = res
	}
	if dbDebugEnabled {
		tappable.changedFields = tappable.changedFields[:0]
	}
	tappable.ClearDirty()
	if tappable.IsNewRecord() {
		tappableCache.Set(tappable.Id, tappable, ttlcache.DefaultTTL)
		tappable.newRecord = false
	}
}
