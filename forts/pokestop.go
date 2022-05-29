package forts

import (
	"database/sql"
	"github.com/jellydator/ttlcache/v3"
	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"
	"golbat/pogo"
	"golbat/util"
	"gopkg.in/guregu/null.v4"
	"math"
)

type Pokestop struct {
	Id                    string      `db:"id"`
	Lat                   float64     `db:"lat"`
	Lon                   float64     `db:"lon"`
	Name                  null.String `db:"name"`
	Url                   null.String `db:"url"`
	LureExpireTimestamp   int         `db:"lure_expire_timestamp"`
	LastModifiedTimestamp uint32      `db:"last_modified_timestamp"`
	CellId                null.Int    `db:"cell_id"`
	LureId                int16       `db:"lure_id"`
	FirstSeenTimestamp    int16       `db:"first_seen_timestamp"`
	Enabled               int16       `db:"enabled"`
	ArScanEligible        null.Int    `db:"ar_scan_eligible"` // is an 8
	//`id` varchar(35) NOT NULL,
	//`lat` double(18,14) NOT NULL,
	//`lon` double(18,14) NOT NULL,
	//`name` varchar(128) DEFAULT NULL,
	//`url` varchar(200) DEFAULT NULL,
	//`lure_expire_timestamp` int unsigned DEFAULT NULL,
	//`last_modified_timestamp` int unsigned DEFAULT NULL,
	//`updated` int unsigned NOT NULL,
	//`enabled` tinyint unsigned DEFAULT NULL,
	//`quest_type` int unsigned DEFAULT NULL,
	//`quest_timestamp` int unsigned DEFAULT NULL,
	//`quest_target` smallint unsigned DEFAULT NULL,
	//`quest_conditions` text,
	//`quest_rewards` text,
	//`quest_template` varchar(100) DEFAULT NULL,
	//`quest_title` varchar(100) DEFAULT NULL,
	//`quest_reward_type` smallint unsigned GENERATED ALWAYS AS (json_extract(json_extract(`quest_rewards`,_utf8mb4'$[*].type'),_utf8mb4'$[0]')) VIRTUAL,
	//`quest_item_id` smallint unsigned GENERATED ALWAYS AS (json_extract(json_extract(`quest_rewards`,_utf8mb4'$[*].info.item_id'),_utf8mb4'$[0]')) VIRTUAL,
	//`quest_reward_amount` smallint unsigned GENERATED ALWAYS AS (json_extract(json_extract(`quest_rewards`,_utf8mb4'$[*].info.amount'),_utf8mb4'$[0]')) VIRTUAL,
	//`cell_id` bigint unsigned DEFAULT NULL,
	//`deleted` tinyint unsigned NOT NULL DEFAULT '0',
	//`lure_id` smallint DEFAULT '0',
	//`first_seen_timestamp` int unsigned NOT NULL,
	//`sponsor_id` smallint unsigned DEFAULT NULL,
	//`partner_id` varchar(35) DEFAULT NULL,
	//`quest_pokemon_id` smallint unsigned GENERATED ALWAYS AS (json_extract(json_extract(`quest_rewards`,_utf8mb4'$[*].info.pokemon_id'),_utf8mb4'$[0]')) VIRTUAL,
	//`ar_scan_eligible` tinyint unsigned DEFAULT NULL,
	//`power_up_level` smallint unsigned DEFAULT NULL,
	//`power_up_points` int unsigned DEFAULT NULL,
	//`power_up_end_timestamp` int unsigned DEFAULT NULL,
	//`alternative_quest_type` int unsigned DEFAULT NULL,
	//`alternative_quest_timestamp` int unsigned DEFAULT NULL,
	//`alternative_quest_target` smallint unsigned DEFAULT NULL,
	//`alternative_quest_conditions` text,
	//`alternative_quest_rewards` text,
	//`alternative_quest_template` varchar(100) DEFAULT NULL,
	//`alternative_quest_title` varchar(100) DEFAULT NULL,

}

func getPokestop(db *sqlx.DB, fortId string) (*Pokestop, error) {
	stop := pokestopCache.Get(fortId)
	if stop != nil {
		pokestop := stop.Value()
		return &pokestop, nil
	}
	pokestop := Pokestop{}
	err := db.Get(&pokestop, "SELECT id, lat, lon, name, url FROM pokestop WHERE id = ?", fortId)

	if err == sql.ErrNoRows {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	pokestopCache.Set(fortId, pokestop, ttlcache.DefaultTTL)
	return &pokestop, nil
}

func hasChanges(old *Pokestop, new *Pokestop) bool {
	return new.LastModifiedTimestamp != old.LastModifiedTimestamp ||
		new.LureExpireTimestamp != old.LureExpireTimestamp ||
		new.LureId != old.LureId ||
		//		new.incidents.count != old.incidents.count ||
		new.Name != old.Name ||
		new.Url != old.Url ||
		new.ArScanEligible != old.ArScanEligible ||
		//		new.powerUpPoints != old.powerUpPoints ||
		//		new.powerUpEndTimestamp != old.powerUpEndTimestamp ||
		//		new.questTemplate != old.questTemplate ||
		//		new.alternativeQuestTemplate != old.alternativeQuestTemplate ||
		new.Enabled != old.Enabled ||
		//		new.sponsorId != old.sponsorId ||
		//		new.partnerId != old.partnerId ||
		math.Abs(new.Lat-old.Lat) >= 0.000001 ||
		math.Abs(new.Lon-old.Lon) >= 0.000001
}

func updatePokestopFromFort(stop *Pokestop, fortData *pogo.PokemonFortProto, cellId uint64) *Pokestop {
	stop.Id = fortData.FortId
	stop.Lat = fortData.Latitude
	stop.Lon = fortData.Longitude

	if fortData.ImageUrl != "" {
		stop.Url = null.StringFrom(fortData.ImageUrl)
	}
	stop.Enabled = util.BoolToInt[int16](fortData.Enabled)
	stop.ArScanEligible = null.IntFrom(util.BoolToInt[int64](fortData.IsArScanEligible))

	return stop
}

func updatePokestopFromFortProto(stop *Pokestop, fortData *pogo.FortDetailsOutProto) *Pokestop {
	stop.Id = fortData.Id
	stop.Lat = fortData.Latitude
	stop.Lon = fortData.Longitude
	if len(fortData.ImageUrl) > 0 {
		stop.Url = null.StringFrom(fortData.ImageUrl[0])
	}
	stop.Name = null.StringFrom(fortData.Name)

	return stop
}

func updatePokestop(db *sqlx.DB, pokestop *Pokestop) {
	oldPokestop, _ := getPokestop(db, pokestop.Id)

	if oldPokestop != nil && !hasChanges(oldPokestop, pokestop) {
		return
	}

	if oldPokestop == nil {
		res, err := db.NamedExec("INSERT INTO pokestop (id, lat, lon, name, url, updated, first_seen_timestamp)"+
			"VALUES (:id, :lat, :lon, :name, :url, UNIX_TIMESTAMP(), UNIX_TIMESTAMP())", pokestop)

		_, _ = res, err
	} else {
		res, err := db.NamedExec("UPDATE pokestop SET "+
			"lat = :lat, "+
			"lon = :lon, "+
			"url = :url, "+
			"name = :name "+
			"WHERE id = :id", pokestop,
		)
		_, _ = res, err
	}
	pokestopCache.Set(pokestop.Id, *pokestop, ttlcache.DefaultTTL)

}

func UpdatePokestopRecordWithFortDetailsOutProto(db *sqlx.DB, fort *pogo.FortDetailsOutProto) {
	pokestop, err := getPokestop(db, fort.Id) // should check error
	if err != nil {
		log.Printf("Update pokestop %s", err)
		return
	}

	if pokestop == nil {
		pokestop = &Pokestop{}
	}
	updatePokestopFromFortProto(pokestop, fort)
	updatePokestop(db, pokestop)
}
