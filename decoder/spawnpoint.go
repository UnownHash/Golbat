package decoder

import (
	"context"
	"database/sql"
	"golbat/db"
	"golbat/pogo"
	"strconv"
	"time"

	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
	"gopkg.in/guregu/null.v4"
)

// Spawnpoint struct.
// REMINDER! Keep hasChangesSpawnpoint updated after making changes
type Spawnpoint struct {
	Id         int64    `db:"id"`
	Lat        float64  `db:"lat"`
	Lon        float64  `db:"lon"`
	Updated    int64    `db:"updated"`
	LastSeen   int64    `db:"last_seen"`
	DespawnSec null.Int `db:"despawn_sec"`
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

func getSpawnpointRecord(ctx context.Context, db db.DbDetails, spawnpointId int64) (*Spawnpoint, error) {
	inMemorySpawnpoint := spawnpointCache.Get(spawnpointId)
	if inMemorySpawnpoint != nil {
		spawnpoint := inMemorySpawnpoint.Value()
		return &spawnpoint, nil
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

	spawnpointCache.Set(spawnpointId, spawnpoint, ttlcache.DefaultTTL)
	return &spawnpoint, nil
}

func Abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

func hasChangesSpawnpoint(old *Spawnpoint, new *Spawnpoint) bool {
	if !floatAlmostEqual(old.Lat, new.Lat, floatTolerance) ||
		!floatAlmostEqual(old.Lon, new.Lon, floatTolerance) ||
		(old.DespawnSec.Valid && !new.DespawnSec.Valid) ||
		(!old.DespawnSec.Valid && new.DespawnSec.Valid) {
		return true
	}
	if !old.DespawnSec.Valid && !new.DespawnSec.Valid {
		return false
	}

	// Ignore small movements in despawn time
	oldDespawnSec := old.DespawnSec.Int64
	newDespawnSec := new.DespawnSec.Int64

	if oldDespawnSec <= 1 && newDespawnSec >= 3598 {
		return false
	}
	if newDespawnSec <= 1 && oldDespawnSec >= 3598 {
		return false
	}

	return Abs(old.DespawnSec.Int64-new.DespawnSec.Int64) > 2
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
		spawnpoint := Spawnpoint{
			Id:         spawnId,
			Lat:        wildPokemon.Latitude,
			Lon:        wildPokemon.Longitude,
			DespawnSec: null.IntFrom(int64(secondOfHour)),
		}
		spawnpointUpdate(ctx, db, &spawnpoint)
	} else {
		spawnPoint, _ := getSpawnpointRecord(ctx, db, spawnId)
		if spawnPoint == nil {
			spawnpoint := Spawnpoint{
				Id:  spawnId,
				Lat: wildPokemon.Latitude,
				Lon: wildPokemon.Longitude,
			}
			spawnpointUpdate(ctx, db, &spawnpoint)
		} else {
			spawnpointSeen(ctx, db, spawnId)
		}
	}
}

func spawnpointUpdate(ctx context.Context, db db.DbDetails, spawnpoint *Spawnpoint) {
	oldSpawnpoint, _ := getSpawnpointRecord(ctx, db, spawnpoint.Id)

	if oldSpawnpoint != nil && !hasChangesSpawnpoint(oldSpawnpoint, spawnpoint) {
		return
	}

	//log.Println(cmp.Diff(oldSpawnpoint, spawnpoint))

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

	spawnpointCache.Set(spawnpoint.Id, *spawnpoint, ttlcache.DefaultTTL)
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
		spawnpointCache.Set(spawnpoint.Id, spawnpoint, ttlcache.DefaultTTL)
	}
}
