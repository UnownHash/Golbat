package decoder

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"golbat/db"
	"golbat/pogo"

	"github.com/guregu/null/v6"
	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
)

// spawnpointSelectColumns defines the columns for spawnpoint queries.
// Used by both single-row and bulk load queries to keep them in sync.
const spawnpointSelectColumns = `id, lat, lon, updated, last_seen, despawn_sec`

// Spawnpoint struct.
// REMINDER! Dirty flag pattern - use setter methods to modify fields
type Spawnpoint struct {
	mu sync.Mutex `db:"-" json:"-"` // Object-level mutex

	Id         int64    `db:"id"`
	Lat        float64  `db:"lat"`
	Lon        float64  `db:"lon"`
	Updated    int64    `db:"updated"`
	LastSeen   int64    `db:"last_seen"`
	DespawnSec null.Int `db:"despawn_sec"`

	dirty         bool     `db:"-" json:"-"` // Not persisted - tracks if object needs saving
	newRecord     bool     `db:"-" json:"-"` // Not persisted - tracks if this is a new record
	changedFields []string `db:"-" json:"-"` // Track which fields changed (only when dbDebugEnabled)
}

//CREATE TABLE `spawnpoint` (
//`id` bigint unsigned NOT NULL,
//`lat` double(18,14) NOT NULL,
//`lon` double(18,14) NOT NULL,
//`updated` int unsigned NOT NULL DEFAULT '0',
//`last_seen` int unsigned NOT NULL DEFAULT '0',
//`despawn_sec` smallint unsigned DEFAULT NULL,
//PRIMARY KEY (`id`),
//KEY `ix_coords` (`lat`,`lon`),
//KEY `ix_updated` (`updated`),
//KEY `ix_last_seen` (`last_seen`)
//)

// IsDirty returns true if any field has been modified
func (s *Spawnpoint) IsDirty() bool {
	return s.dirty
}

// ClearDirty resets the dirty flag (call after saving to DB)
func (s *Spawnpoint) ClearDirty() {
	s.dirty = false
}

// IsNewRecord returns true if this is a new record (not yet in DB)
func (s *Spawnpoint) IsNewRecord() bool {
	return s.newRecord
}

// Lock acquires the Spawnpoint's mutex
func (s *Spawnpoint) Lock() {
	s.mu.Lock()
}

// Unlock releases the Spawnpoint's mutex
func (s *Spawnpoint) Unlock() {
	s.mu.Unlock()
}

// --- Set methods with dirty tracking ---

func (s *Spawnpoint) SetLat(v float64) {
	if !floatAlmostEqual(s.Lat, v, floatTolerance) {
		if dbDebugEnabled {
			s.changedFields = append(s.changedFields, fmt.Sprintf("Lat:%f->%f", s.Lat, v))
		}
		s.Lat = v
		s.dirty = true
	}
}

func (s *Spawnpoint) SetLon(v float64) {
	if !floatAlmostEqual(s.Lon, v, floatTolerance) {
		if dbDebugEnabled {
			s.changedFields = append(s.changedFields, fmt.Sprintf("Lon:%f->%f", s.Lon, v))
		}
		s.Lon = v
		s.dirty = true
	}
}

// SetDespawnSec sets despawn_sec with 2-second tolerance logic
func (s *Spawnpoint) SetDespawnSec(v null.Int) {
	// Handle validity changes
	if (s.DespawnSec.Valid && !v.Valid) || (!s.DespawnSec.Valid && v.Valid) {
		if dbDebugEnabled {
			s.changedFields = append(s.changedFields, fmt.Sprintf("DespawnSec:%s->%s", FormatNull(s.DespawnSec), FormatNull(v)))
		}
		s.DespawnSec = v
		s.dirty = true
		return
	}

	// Both invalid - no change
	if !s.DespawnSec.Valid && !v.Valid {
		return
	}

	// Both valid - check with tolerance
	oldVal := s.DespawnSec.Int64
	newVal := v.Int64

	// Handle wraparound at hour boundary (0/3600)
	if oldVal <= 1 && newVal >= 3598 {
		return
	}
	if newVal <= 1 && oldVal >= 3598 {
		return
	}

	// Allow 2-second tolerance for despawn time
	if Abs(oldVal-newVal) > 2 {
		if dbDebugEnabled {
			s.changedFields = append(s.changedFields, fmt.Sprintf("DespawnSec:%s->%s", FormatNull(s.DespawnSec), FormatNull(v)))
		}
		s.DespawnSec = v
		s.dirty = true
	}
}

func (s *Spawnpoint) SetUpdated(v int64) {
	if s.Updated != v {
		if dbDebugEnabled {
			s.changedFields = append(s.changedFields, fmt.Sprintf("Updated:%d->%d", s.Updated, v))
		}
		s.Updated = v
		s.dirty = true
	}
}

func (s *Spawnpoint) SetLastSeen(v int64) {
	if s.LastSeen != v {
		if dbDebugEnabled {
			s.changedFields = append(s.changedFields, fmt.Sprintf("LastSeen:%d->%d", s.LastSeen, v))
		}
		s.LastSeen = v
		s.dirty = true
	}
}

func loadSpawnpointFromDatabase(ctx context.Context, db db.DbDetails, spawnpointId int64, spawnpoint *Spawnpoint) error {
	err := db.GeneralDb.GetContext(ctx, spawnpoint,
		"SELECT "+spawnpointSelectColumns+" FROM spawnpoint WHERE id = ?", spawnpointId)
	statsCollector.IncDbQuery("select spawnpoint", err)
	return err
}

// peekSpawnpointRecord - cache-only lookup, no DB fallback, returns locked.
// Caller MUST call returned unlock function if non-nil.
func peekSpawnpointRecord(spawnpointId int64) (*Spawnpoint, func(), error) {
	if item := spawnpointCache.Get(spawnpointId); item != nil {
		spawnpoint := item.Value()
		spawnpoint.Lock()
		return spawnpoint, func() { spawnpoint.Unlock() }, nil
	}
	return nil, nil, nil
}

// getSpawnpointRecord acquires lock. Will cause a backing database lookup.
// Caller MUST call returned unlock function if non-nil.
func getSpawnpointRecord(ctx context.Context, db db.DbDetails, spawnpointId int64) (*Spawnpoint, func(), error) {
	// Check cache first
	if item := spawnpointCache.Get(spawnpointId); item != nil {
		spawnpoint := item.Value()
		spawnpoint.Lock()
		return spawnpoint, func() { spawnpoint.Unlock() }, nil
	}

	dbSpawnpoint := Spawnpoint{}
	err := loadSpawnpointFromDatabase(ctx, db, spawnpointId, &dbSpawnpoint)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	dbSpawnpoint.ClearDirty()

	// Atomically cache the loaded Spawnpoint - if another goroutine raced us,
	// we'll get their Spawnpoint and use that instead (ensuring same mutex)
	existingSpawnpoint, _ := spawnpointCache.GetOrSetFunc(spawnpointId, func() *Spawnpoint {
		return &dbSpawnpoint
	})

	spawnpoint := existingSpawnpoint.Value()
	spawnpoint.Lock()
	return spawnpoint, func() { spawnpoint.Unlock() }, nil
}

// getOrCreateSpawnpointRecord gets existing or creates new, locked.
// Caller MUST call returned unlock function.
func getOrCreateSpawnpointRecord(ctx context.Context, db db.DbDetails, spawnpointId int64) (*Spawnpoint, func(), error) {
	// Create new Spawnpoint atomically - function only called if key doesn't exist
	spawnpointItem, _ := spawnpointCache.GetOrSetFunc(spawnpointId, func() *Spawnpoint {
		return &Spawnpoint{Id: spawnpointId, newRecord: true}
	})

	spawnpoint := spawnpointItem.Value()
	spawnpoint.Lock()

	if spawnpoint.newRecord {
		// We should attempt to load from database
		err := loadSpawnpointFromDatabase(ctx, db, spawnpointId, spawnpoint)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				spawnpoint.Unlock()
				return nil, nil, err
			}
		} else {
			// We loaded from DB
			spawnpoint.newRecord = false
			spawnpoint.ClearDirty()
		}
	}

	return spawnpoint, func() { spawnpoint.Unlock() }, nil
}

func Abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

func spawnpointUpdateFromWild(ctx context.Context, db db.DbDetails, wildPokemon *pogo.WildPokemonProto, timestampMs int64) {
	spawnId, err := strconv.ParseInt(wildPokemon.SpawnPointId, 16, 64)
	if err != nil {
		panic(err)
	}

	if wildPokemon.TimeTillHiddenMs <= 90000 && wildPokemon.TimeTillHiddenMs > 0 {
		expireTimeStamp := (timestampMs + int64(wildPokemon.TimeTillHiddenMs)) / 1000

		date := time.Unix(expireTimeStamp, 0)
		secondOfHour := date.Second() + date.Minute()*60

		spawnpoint, unlock, err := getOrCreateSpawnpointRecord(ctx, db, spawnId)
		if err != nil {
			log.Errorf("getOrCreateSpawnpointRecord: %s", err)
			return
		}
		spawnpoint.SetLat(wildPokemon.Latitude)
		spawnpoint.SetLon(wildPokemon.Longitude)
		spawnpoint.SetDespawnSec(null.IntFrom(int64(secondOfHour)))
		spawnpointUpdate(ctx, db, spawnpoint)
		unlock()
	} else {
		spawnpoint, unlock, err := getOrCreateSpawnpointRecord(ctx, db, spawnId)
		if err != nil {
			log.Errorf("getOrCreateSpawnpointRecord: %s", err)
			return
		}
		if spawnpoint.newRecord {
			spawnpoint.SetLat(wildPokemon.Latitude)
			spawnpoint.SetLon(wildPokemon.Longitude)
			spawnpointUpdate(ctx, db, spawnpoint)
		} else {
			spawnpointSeen(ctx, db, spawnpoint)
		}
		unlock()
	}
}

func spawnpointUpdate(ctx context.Context, db db.DbDetails, spawnpoint *Spawnpoint) {
	// Skip save if not dirty and not new
	if !spawnpoint.IsDirty() && !spawnpoint.IsNewRecord() {
		return
	}

	spawnpoint.SetUpdated(time.Now().Unix())  // ensure future updates are set correctly
	spawnpoint.SetLastSeen(time.Now().Unix()) // ensure future updates are set correctly

	if dbDebugEnabled {
		if spawnpoint.IsNewRecord() {
			dbDebugLog("INSERT", "Spawnpoint", strconv.FormatInt(spawnpoint.Id, 10), spawnpoint.changedFields)
		} else {
			dbDebugLog("UPDATE", "Spawnpoint", strconv.FormatInt(spawnpoint.Id, 10), spawnpoint.changedFields)
		}
	}

	_, err := db.GeneralDb.NamedExecContext(ctx, "INSERT INTO spawnpoint (id, lat, lon, updated, last_seen, despawn_sec)"+
		"VALUES (:id, :lat, :lon, :updated, :last_seen, :despawn_sec)"+
		"ON DUPLICATE KEY UPDATE "+
		"lat=VALUES(lat),"+
		"lon=VALUES(lon),"+
		"updated=VALUES(updated),"+
		"last_seen=VALUES(last_seen),"+
		"despawn_sec=VALUES(despawn_sec)", spawnpoint)

	statsCollector.IncDbQuery("insert spawnpoint", err)
	if err != nil {
		log.Errorf("Error updating spawnpoint %s", err)
		return
	}

	spawnpoint.ClearDirty()
	if spawnpoint.IsNewRecord() {
		spawnpoint.newRecord = false
		spawnpointCache.Set(spawnpoint.Id, spawnpoint, ttlcache.DefaultTTL)
	}
}

// spawnpointSeen updates the last_seen timestamp for a spawnpoint.
// The spawnpoint must already be locked by the caller.
func spawnpointSeen(ctx context.Context, db db.DbDetails, spawnpoint *Spawnpoint) {
	now := time.Now().Unix()

	// update at least every 6 hours (21600s). If reduce_updates is enabled, use 12 hours.
	if now-spawnpoint.LastSeen > GetUpdateThreshold(21600) {
		spawnpoint.SetLastSeen(now)

		_, err := db.GeneralDb.ExecContext(ctx, "UPDATE spawnpoint "+
			"SET last_seen=? "+
			"WHERE id = ? ", now, spawnpoint.Id)
		statsCollector.IncDbQuery("update spawnpoint", err)
		if err != nil {
			log.Printf("Error updating spawnpoint last seen %s", err)
			return
		}
		// Cache already contains a pointer, no need to update
	}
}
