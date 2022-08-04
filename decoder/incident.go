package decoder

import (
	"database/sql"
	"github.com/google/go-cmp/cmp"
	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
	"golbat/pogo"
	"golbat/webhooks"
	"time"
)

type Incident struct {
	Id             string `db:"id"`
	PokestopId     string `db:"pokestop_id"`
	StartTime      int64  `db:"start"`
	ExpirationTime int64  `db:"expiration"`
	DisplayType    int16  `db:"display_type"`
	Style          int16  `db:"style"`
	Character      int16  `db:"character"`
	Updated        int64  `db:"updated"`
}

//->   `id` varchar(35) NOT NULL,
//->   `pokestop_id` varchar(35) NOT NULL,
//->   `start` int unsigned NOT NULL,
//->   `expiration` int unsigned NOT NULL,
//->   `display_type` smallint unsigned NOT NULL,
//->   `style` smallint unsigned NOT NULL,
//->   `character` smallint unsigned NOT NULL,
//->   `updated` int unsigned NOT NULL,

func getIncidentRecord(db DbDetails, incidentId string) (*Incident, error) {
	inMemoryIncident := incidentCache.Get(incidentId)
	if inMemoryIncident != nil {
		incident := inMemoryIncident.Value()
		return &incident, nil
	}

	incident := Incident{}
	err := db.GeneralDb.Get(&incident,
		"SELECT id, pokestop_id, start, expiration, display_type, style, `character`, updated "+
			"FROM incident "+
			"WHERE incident.id = ? ", incidentId)
	if err == sql.ErrNoRows {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	incidentCache.Set(incidentId, incident, ttlcache.DefaultTTL)
	return &incident, nil
}

func hasChangesIncident(old *Incident, new *Incident) bool {
	return !cmp.Equal(old, new, ignoreNearFloats)
}

func saveIncidentRecord(db DbDetails, incident *Incident) {
	oldIncident, _ := getIncidentRecord(db, incident.Id)

	if oldIncident != nil && !hasChangesIncident(oldIncident, incident) {
		return
	}

	log.Traceln(cmp.Diff(oldIncident, incident))

	incident.Updated = time.Now().Unix()

	//log.Println(cmp.Diff(oldIncident, incident))

	if oldIncident == nil {
		res, err := db.GeneralDb.NamedExec("INSERT INTO incident (id, pokestop_id, start, expiration, display_type, style, `character`, updated) "+
			"VALUES (:id, :pokestop_id, :start, :expiration, :display_type, :style, :character, :updated)", incident)

		if err != nil {
			log.Errorf("insert incident: %s", err)
			return
		}

		_, _ = res, err
	} else {
		res, err := db.GeneralDb.NamedExec("UPDATE incident SET "+
			"start = :start, "+
			"expiration = :expiration, "+
			"display_type = :display_type, "+
			"style = :style, "+
			"`character` = :character, "+
			"updated = :updated "+
			"WHERE id = :id", incident,
		)
		if err != nil {
			log.Errorf("Update incident %s", err)
		}
		_, _ = res, err
	}

	incidentCache.Set(incident.Id, *incident, ttlcache.DefaultTTL)
	createIncidentWebhooks(db, oldIncident, incident)
}

func createIncidentWebhooks(db DbDetails, oldIncident *Incident, incident *Incident) {
	if oldIncident == nil || (oldIncident.ExpirationTime != incident.ExpirationTime || oldIncident.Character != incident.Character) {
		stop, _ := getPokestopRecord(db, incident.PokestopId)
		if stop == nil {
			stop = &Pokestop{}
		}

		incidentHook := map[string]interface{}{
			"id":          incident.Id,
			"pokestop_id": incident.PokestopId,
			"latitude":    stop.Lat,
			"longitude":   stop.Lon,
			"pokestop_name": func() string {
				if stop.Name.Valid {
					return stop.Name.String
				} else {
					return "Unknown"
				}
			}(),
			"url":                       stop.Url.ValueOrZero(),
			"enabled":                   stop.Enabled.ValueOrZero(),
			"start":                     incident.StartTime,
			"incident_expire_timestamp": incident.ExpirationTime, // deprecated, remove old key in the future
			"expiration":                incident.ExpirationTime,
			"display_type":              incident.DisplayType,
			"style":                     incident.Style,
			"grunt_type":                incident.Character, // deprecated, remove old key in the future
			"character":                 incident.Character,
			"updated":                   incident.Updated,
		}

		webhooks.AddMessage(webhooks.Invasion, incidentHook)
	}
}

func (incident *Incident) updateFromPokestopIncidentDisplay(pokestopDisplay *pogo.PokestopIncidentDisplayProto) {
	incident.Id = pokestopDisplay.IncidentId
	incident.StartTime = int64(pokestopDisplay.IncidentStartMs / 1000)
	incident.ExpirationTime = int64(pokestopDisplay.IncidentExpirationMs / 1000)
	incident.DisplayType = int16(pokestopDisplay.IncidentDisplayType)
	characterDisplay := pokestopDisplay.GetCharacterDisplay()
	if characterDisplay != nil {
		incident.Style = int16(characterDisplay.Style)
		incident.Character = int16(characterDisplay.Character)
	} else {
		incident.Style, incident.Character = 0, 0
	}
}
