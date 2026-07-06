package decoder

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"golbat/db"
	"golbat/pogo"

	"github.com/guregu/null/v6"
	log "github.com/sirupsen/logrus"
)

// spawnpointSelectColumns defines the columns for spawnpoint queries.
// Used by both single-row and bulk load queries to keep them in sync.
const spawnpointSelectColumns = `id, lat, lon, updated, last_seen, despawn_sec`

// SpawnpointData contains all database-persisted fields for Spawnpoint.
// This struct is embedded in Spawnpoint and can be safely copied for write-behind queueing.
type SpawnpointData struct {
	Id         int64    `db:"id"`
	Lat        float64  `db:"lat"`
	Lon        float64  `db:"lon"`
	Updated    int64    `db:"updated"`
	LastSeen   int64    `db:"last_seen"`
	DespawnSec null.Int `db:"despawn_sec"`
}

// Spawnpoint struct.
// REMINDER! Dirty flag pattern - use setter methods to modify fields
type Spawnpoint struct {
	mu TrackedMutex[int64] `db:"-" json:"-"` // Object-level mutex with contention tracking

	SpawnpointData // Embedded data fields - can be copied for write-behind queue

	dirty         bool     `db:"-" json:"-"` // Not persisted - tracks if object needs saving
	newRecord     bool     `db:"-" json:"-"` // Not persisted - tracks if this is a new record
	changedFields []string `db:"-" json:"-"` // Track which fields changed (only when dbDebugEnabled)

	// despawnSecFast mirrors DespawnSec for the lock-free read path
	// (setExpireTimestampFromSpawnpoint runs once per wild/nearby pokemon
	// and needs only this one value — taking the entity mutex for it queued
	// readers behind writers holding the lock across DB loads).
	// Encoding: 0 = not yet synced (fall back to the locked path),
	// -1 = DespawnSec known-null, 1..3600 = DespawnSec+1.
	// Lives on Spawnpoint, not SpawnpointData: atomics must not be copied
	// into write-behind snapshots.
	despawnSecFast atomic.Int32 `db:"-" json:"-"`

	// lastSeenFast mirrors LastSeen for the lock-free writer fast path
	// (spawnpointUpdateFromWild runs once per wild sighting; when nothing
	// would change it must not take the entity mutex at all). 0 = never
	// seen / not synced, which always routes to the locked path.
	lastSeenFast atomic.Int64 `db:"-" json:"-"`
}

// syncFastFields publishes all lock-free mirrors; call after DB loads (and
// after a no-row load resolves, so readers know null-despawn is authoritative
// rather than not-yet-loaded).
func (s *Spawnpoint) syncFastFields() {
	s.syncDespawnFast()
	s.lastSeenFast.Store(s.LastSeen)
}

// LastSeenFast returns the lock-free LastSeen mirror (0 = unknown).
func (s *Spawnpoint) LastSeenFast() int64 {
	return s.lastSeenFast.Load()
}

// syncDespawnFast publishes DespawnSec to the lock-free mirror. Call after
// any mutation or DB load of DespawnSec (caller holds the entity lock, or
// has exclusive access during load).
func (s *Spawnpoint) syncDespawnFast() {
	if s.DespawnSec.Valid {
		s.despawnSecFast.Store(int32(s.DespawnSec.Int64) + 1)
	} else {
		s.despawnSecFast.Store(-1)
	}
}

// DespawnSecFast returns (despawnSec, known, synced) without any locking.
// synced=false means the mirror has not been populated yet — callers must
// fall back to the locked read path.
func (s *Spawnpoint) DespawnSecFast() (int, bool, bool) {
	switch v := s.despawnSecFast.Load(); {
	case v == 0:
		return 0, false, false
	case v < 0:
		return 0, false, true
	default:
		return int(v - 1), true, true
	}
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

// Lock acquires the Spawnpoint's mutex with caller tracking
func (s *Spawnpoint) Lock(caller string) {
	s.mu.Lock(caller, "Spawnpoint", s.Id)
}

// Unlock releases the Spawnpoint's mutex
func (s *Spawnpoint) Unlock() {
	s.mu.Unlock("Spawnpoint", s.Id)
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
	// Republish the lock-free mirror on every exit path (this function has
	// several early returns); no-change calls harmlessly re-store the same
	// value and heal a not-yet-synced mirror.
	defer s.syncDespawnFast()

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
	if despawnSecUnchanged(s.DespawnSec.Int64, v.Int64) {
		return
	}

	{
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
	// Closure, not direct defer arg: the mirror must publish the
	// post-mutation value (deferred call args evaluate at defer time).
	defer func() { s.lastSeenFast.Store(s.LastSeen) }()
	if s.LastSeen != v {
		if dbDebugEnabled {
			s.changedFields = append(s.changedFields, fmt.Sprintf("LastSeen:%d->%d", s.LastSeen, v))
		}
		s.LastSeen = v
		s.dirty = true
	}
}
func loadSpawnpointFromDatabase(ctx context.Context, db db.DbDetails, spawnpointId int64, spawnpoint *Spawnpoint) error {
	return timedDbQuery("loadSpawnpointFromDatabase", db.GeneralDb, func() error {
		err := db.GeneralDb.GetContext(ctx, spawnpoint,
			"SELECT "+spawnpointSelectColumns+" FROM spawnpoint WHERE id = ?", spawnpointId)
		statsCollector.IncDbQuery("select spawnpoint", err)
		return err
	})
}

// peekSpawnpointRecord - cache-only lookup, no DB fallback, returns locked.
// Caller MUST call returned unlock function if non-nil.
func peekSpawnpointRecord(spawnpointId int64, caller string) (*Spawnpoint, func(), error) {
	if item := spawnpointCache.Get(spawnpointId); item != nil {
		spawnpoint := item.Value()
		spawnpoint.Lock(caller)
		return spawnpoint, func() { spawnpoint.Unlock() }, nil
	}
	return nil, nil, nil
}

// getSpawnpointRecord acquires lock. Will cause a backing database lookup.
// Caller MUST call returned unlock function if non-nil.
func getSpawnpointRecord(ctx context.Context, db db.DbDetails, spawnpointId int64, caller string) (*Spawnpoint, func(), error) {
	// Check cache first
	if item := spawnpointCache.Get(spawnpointId); item != nil {
		spawnpoint := item.Value()
		spawnpoint.Lock(caller)
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
	dbSpawnpoint.syncFastFields()

	// Atomically cache the loaded Spawnpoint - if another goroutine raced us,
	// we'll get their Spawnpoint and use that instead (ensuring same mutex)
	existingSpawnpoint, _ := spawnpointCache.GetOrSetFunc(spawnpointId, func() *Spawnpoint {
		return &dbSpawnpoint
	})

	spawnpoint := existingSpawnpoint.Value()
	spawnpoint.Lock(caller)
	return spawnpoint, func() { spawnpoint.Unlock() }, nil
}

// getOrCreateSpawnpointRecord gets existing or creates new, locked.
// Caller MUST call returned unlock function.
func getOrCreateSpawnpointRecord(ctx context.Context, db db.DbDetails, spawnpointId int64, caller string) (*Spawnpoint, func(), error) {
	// Create new Spawnpoint atomically - function only called if key doesn't exist
	spawnpointItem, _ := spawnpointCache.GetOrSetFunc(spawnpointId, func() *Spawnpoint {
		return &Spawnpoint{SpawnpointData: SpawnpointData{Id: spawnpointId}, newRecord: true}
	})

	spawnpoint := spawnpointItem.Value()
	spawnpoint.Lock(caller)

	if spawnpoint.newRecord {
		// We should attempt to load from database
		err := loadSpawnpointFromDatabase(ctx, db, spawnpointId, spawnpoint)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				spawnpoint.Unlock()
				return nil, nil, err
			}
			// No DB row: this record's null despawn is now authoritative
			// (not merely unloaded) — publish so the lock-free read path
			// engages. lastSeenFast stays 0, keeping the writer fast path
			// disabled until the record is persisted.
			spawnpoint.syncDespawnFast()
		} else {
			// We loaded from DB
			spawnpoint.newRecord = false
			spawnpoint.ClearDirty()
			spawnpoint.syncFastFields()
		}
	}

	return spawnpoint, func() { spawnpoint.Unlock() }, nil
}

// despawnSecUnchanged is SetDespawnSec's no-change rule: 2-second
// tolerance, with wraparound at the hour boundary (0/3600). Shared with the
// lock-free writer fast path so the two can never drift.
func despawnSecUnchanged(oldVal, newVal int64) bool {
	if oldVal <= 1 && newVal >= 3598 {
		return true
	}
	if newVal <= 1 && oldVal >= 3598 {
		return true
	}
	return Abs(oldVal-newVal) <= 2
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

	hasTTH := wildPokemon.TimeTillHiddenMs <= 90000 && wildPokemon.TimeTillHiddenMs > 0
	var secondOfHour int
	if hasTTH {
		expireTimeStamp := (timestampMs + int64(wildPokemon.TimeTillHiddenMs)) / 1000
		date := time.Unix(expireTimeStamp, 0)
		secondOfHour = date.Second() + date.Minute()*60
	}

	// Lock-free fast path: spawnpoints appear in every GMO (once per wild
	// sighting), and the overwhelmingly common case changes nothing — the
	// spawnpoint is known, its despawn second matches within SetDespawnSec's
	// tolerance, and LastSeen is fresh. Prove all of that from the atomic
	// mirrors and skip the entity mutex entirely. Anything unprovable
	// (cache miss, mid-load entity, unpersisted new record via
	// lastSeenFast==0, stale LastSeen, despawn change) takes the locked
	// path below, unchanged. Lat/lon are not re-verified here: a
	// spawnpoint's id derives from its location, and the locked path
	// re-checks position at least every LastSeen refresh.
	if item := spawnpointCache.Get(spawnId); item != nil {
		sp := item.Value()
		if despawn, known, synced := sp.DespawnSecFast(); synced {
			if last := sp.LastSeenFast(); last > 0 && time.Now().Unix()-last <= GetUpdateThreshold(21600) {
				if !hasTTH || (known && despawnSecUnchanged(int64(despawn), int64(secondOfHour))) {
					return
				}
			}
		}
	}

	if hasTTH {

		spawnpoint, unlock, err := getOrCreateSpawnpointRecord(ctx, db, spawnId, "spawnpointUpdateFromWild")
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
		spawnpoint, unlock, err := getOrCreateSpawnpointRecord(ctx, db, spawnId, "spawnpointUpdateFromMap")
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
			spawnpointUpdate(ctx, db, spawnpoint)
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

	// Capture isNewRecord before state changes
	isNewRecord := spawnpoint.IsNewRecord()

	// Debug logging happens here, before queueing
	if dbDebugEnabled {
		if isNewRecord {
			dbDebugLog("INSERT", "Spawnpoint", strconv.FormatInt(spawnpoint.Id, 10), spawnpoint.changedFields)
		} else {
			dbDebugLog("UPDATE", "Spawnpoint", strconv.FormatInt(spawnpoint.Id, 10), spawnpoint.changedFields)
		}
	}

	// Queue the write through the typed write-behind queue (no delay for spawnpoints)
	if spawnpointQueue != nil {
		spawnpointQueue.Enqueue(spawnpoint.SpawnpointData, isNewRecord, 0)
	} else {
		// Fallback to direct write if queue not initialized
		_ = spawnpointWriteDB(db, spawnpoint)
	}

	if dbDebugEnabled {
		spawnpoint.changedFields = spawnpoint.changedFields[:0]
	}
	spawnpoint.ClearDirty()
	if isNewRecord {
		spawnpoint.newRecord = false
		spawnpointCache.Set(spawnpoint.Id, spawnpoint, 0 /* default TTL */)
	}
}

// spawnpointWriteDB performs the actual database INSERT/UPDATE for a Spawnpoint
// This is called by both direct writes and the write-behind queue
// Spawnpoint uses UPSERT pattern so isNewRecord is not needed
func spawnpointWriteDB(db db.DbDetails, spawnpoint *Spawnpoint) error {
	ctx := context.Background()

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
		return err
	}
	return nil
}

// spawnpointSeen updates the last_seen timestamp for a spawnpoint.
// The spawnpoint must already be locked by the caller.
func spawnpointSeen(ctx context.Context, db db.DbDetails, spawnpoint *Spawnpoint) {
	now := time.Now().Unix()

	// update at least every 6 hours (21600s). If reduce_updates is enabled, use 12 hours.
	if now-spawnpoint.LastSeen > GetUpdateThreshold(21600) {
		spawnpoint.SetLastSeen(now)
		//_, err := db.GeneralDb.ExecContext(ctx, "UPDATE spawnpoint "+
		//	"SET last_seen=? "+
		//	"WHERE id = ? ", now, spawnpoint.Id)
		//statsCollector.IncDbQuery("update spawnpoint", err)
		//if err != nil {
		//	log.Printf("Error updating spawnpoint last seen %s", err)
		//	return
		//}
		// Cache already contains a pointer, no need to update
	}
}
