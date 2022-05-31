package decoder

import (
	"database/sql"
	"github.com/jellydator/ttlcache/v3"
	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"
	"gopkg.in/guregu/null.v4"
	"time"
)

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

func getSpawnpointRecord(db *sqlx.DB, spawnpointId int64) (*Spawnpoint, error) {
	inMemorySpawnpoint := spawnpointCache.Get(spawnpointId)
	if inMemorySpawnpoint != nil {
		spawnpoint := inMemorySpawnpoint.Value()
		return &spawnpoint, nil
	}
	spawnpoint := Spawnpoint{}

	err := db.Get(&spawnpoint, "SELECT id, lat, lon, updated, last_seen, despawn_sec FROM spawnpoint WHERE id = ?", spawnpointId)

	if err == sql.ErrNoRows {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	spawnpointCache.Set(spawnpointId, spawnpoint, ttlcache.DefaultTTL)
	return &spawnpoint, nil
}

func hasChangesSpawnpoint(old *Spawnpoint, new *Spawnpoint) bool {
	return (old.Lat != new.Lat ||
		old.Lon != new.Lon ||
		old.DespawnSec != new.DespawnSec)
}

func spawnpointUpdate(db *sqlx.DB, spawnpoint *Spawnpoint) {
	oldSpawnpoint, _ := getSpawnpointRecord(db, spawnpoint.Id)

	if oldSpawnpoint != nil && !hasChangesSpawnpoint(oldSpawnpoint, spawnpoint) {
		return
	}

	//log.Println(cmp.Diff(oldSpawnpoint, spawnpoint))

	_, err := db.NamedExec("INSERT INTO spawnpoint (id, lat, lon, updated, last_seen, despawn_sec)"+
		"VALUES (:id, :lat, :lon, UNIX_TIMESTAMP(), UNIX_TIMESTAMP(), :despawn_sec)"+
		"ON DUPLICATE KEY UPDATE "+
		"lat=VALUES(lat),"+
		"lon=VALUES(lon),"+
		"updated=VALUES(updated),"+
		"last_seen=VALUES(last_seen),"+
		"despawn_sec=VALUES(despawn_sec)", spawnpoint)

	if err != nil {
		log.Errorf("Error updating spawnpoint %s", err)
		return
	}

	spawnpoint.LastSeen = int64(time.Now().Unix()) // ensure future updates are set correctly
	spawnpointCache.Set(spawnpoint.Id, *spawnpoint, ttlcache.DefaultTTL)
}

func spawnpointSeen(db *sqlx.DB, spawnpointId int64) {
	inMemorySpawnpoint := spawnpointCache.Get(spawnpointId)
	if inMemorySpawnpoint == nil {
		// This should never happen, since all routes here have previously created a spawnpoint in the cache
		return
	}

	spawnpoint := inMemorySpawnpoint.Value()
	now := time.Now().Unix()

	if now-spawnpoint.LastSeen > 900 {
		spawnpoint.LastSeen = now

		_, err := db.Exec("UPDATE spawnpoint "+
			"SET last_seen=? "+
			"WHERE id = ? ", now, spawnpointId)
		if err != nil {
			log.Printf("Error updating spawnpoint last seen", err)
			return
		}
		spawnpointCache.Set(spawnpoint.Id, spawnpoint, ttlcache.DefaultTTL)
	}
}
