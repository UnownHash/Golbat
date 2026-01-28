package decoder

import (
	"context"
	"database/sql"
	"strconv"
	"time"

	"golbat/db"
	"golbat/pogo"

	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
	"gopkg.in/guregu/null.v4"
)

// Spawnpoint struct.
// REMINDER! Dirty flag pattern - use setter methods to modify fields
type Spawnpoint struct {
	Id         int64    `db:"id"`
	Lat        float64  `db:"lat"`
	Lon        float64  `db:"lon"`
	Updated    int64    `db:"updated"`
	LastSeen   int64    `db:"last_seen"`
	DespawnSec null.Int `db:"despawn_sec"`

	dirty     bool `db:"-" json:"-"` // Not persisted - tracks if object needs saving
	newRecord bool `db:"-" json:"-"` // Not persisted - tracks if this is a new record
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

// --- Set methods with dirty tracking ---

func (s *Spawnpoint) SetLat(v float64) {
	if !floatAlmostEqual(s.Lat, v, floatTolerance) {
		s.Lat = v
		s.dirty = true
	}
}

func (s *Spawnpoint) SetLon(v float64) {
	if !floatAlmostEqual(s.Lon, v, floatTolerance) {
		s.Lon = v
		s.dirty = true
	}
}

// SetDespawnSec sets despawn_sec with 2-second tolerance logic
func (s *Spawnpoint) SetDespawnSec(v null.Int) {
	// Handle validity changes
	if (s.DespawnSec.Valid && !v.Valid) || (!s.DespawnSec.Valid && v.Valid) {
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
		s.DespawnSec = v
		s.dirty = true
	}
}

func getSpawnpointRecord(ctx context.Context, db db.DbDetails, spawnpointId int64) (*Spawnpoint, error) {
	inMemorySpawnpoint := spawnpointCache.Get(spawnpointId)
	if inMemorySpawnpoint != nil {
		spawnpoint := inMemorySpawnpoint.Value()
		return spawnpoint, nil
	}
	spawnpoint := Spawnpoint{}

	err := db.GeneralDb.GetContext(ctx, &spawnpoint, "SELECT id, lat, lon, updated, last_seen, despawn_sec FROM spawnpoint WHERE id = ?", spawnpointId)

	statsCollector.IncDbQuery("select spawnpoint", err)
	if err == sql.ErrNoRows {
		return nil, nil
	}

	if err != nil {
		return &Spawnpoint{Id: spawnpointId}, err
	}

	return &spawnpoint, nil
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

		spawnpoint, _ := getSpawnpointRecord(ctx, db, spawnId)
		if spawnpoint == nil {
			spawnpoint = &Spawnpoint{Id: spawnId, newRecord: true}
		}
		spawnpoint.SetLat(wildPokemon.Latitude)
		spawnpoint.SetLon(wildPokemon.Longitude)
		spawnpoint.SetDespawnSec(null.IntFrom(int64(secondOfHour)))
		spawnpointUpdate(ctx, db, spawnpoint)
	} else {
		spawnPoint, _ := getSpawnpointRecord(ctx, db, spawnId)
		if spawnPoint == nil {
			spawnpoint := &Spawnpoint{
				Id:        spawnId,
				Lat:       wildPokemon.Latitude,
				Lon:       wildPokemon.Longitude,
				newRecord: true,
			}
			spawnpointUpdate(ctx, db, spawnpoint)
		} else {
			spawnpointSeen(ctx, db, spawnId)
		}
	}
}

func spawnpointUpdate(ctx context.Context, db db.DbDetails, spawnpoint *Spawnpoint) {
	// Skip save if not dirty and not new
	if !spawnpoint.IsDirty() && !spawnpoint.IsNewRecord() {
		return
	}

	spawnpoint.Updated = time.Now().Unix()  // ensure future updates are set correctly
	spawnpoint.LastSeen = time.Now().Unix() // ensure future updates are set correctly

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

func spawnpointSeen(ctx context.Context, db db.DbDetails, spawnpointId int64) {
	inMemorySpawnpoint := spawnpointCache.Get(spawnpointId)
	if inMemorySpawnpoint == nil {
		// This should never happen, since all routes here have previously created a spawnpoint in the cache
		return
	}

	spawnpoint := inMemorySpawnpoint.Value()
	now := time.Now().Unix()

	if now-spawnpoint.LastSeen > 3600 {
		spawnpoint.LastSeen = now

		_, err := db.GeneralDb.ExecContext(ctx, "UPDATE spawnpoint "+
			"SET last_seen=? "+
			"WHERE id = ? ", now, spawnpointId)
		statsCollector.IncDbQuery("update spawnpoint", err)
		if err != nil {
			log.Printf("Error updating spawnpoint last seen %s", err)
			return
		}
		// Cache already contains a pointer, no need to update
	}
}
