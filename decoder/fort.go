package decoder

import (
	"context"
	"encoding/json"
	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
	"golbat/db"
	"golbat/geo"
	"golbat/pogo"
	"golbat/webhooks"
)

type Location struct {
	Latitude  float64 `json:"lat"`
	Longitude float64 `json:"lon"`
}

type FortWebhook struct {
	Type        string   `json:"type"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	ImageUrl    string   `json:"image_url"`
	Location    Location `json:"location"`
}

type FortChange string
type FortType string

func (f FortType) String() string {
	switch f {
	case POKESTOP:
		return "pokestop"
	case GYM:
		return "gym"
	}
	return "unknown"
}

func (f FortChange) String() string {
	switch f {
	case NEW:
		return "new"
	case REMOVAL:
		return "removal"
	case EDIT:
		return "edit"
	}
	return "unknown"
}

const (
	NEW     FortChange = "new"
	REMOVAL FortChange = "removal"
	EDIT    FortChange = "edit"

	POKESTOP FortType = "pokestop"
	GYM      FortType = "gym"
)

func InitWebHookFortFromGym(gym *Gym) (fort FortWebhook) {
	if gym == nil {
		return
	}
	fort.Type = GYM.String()
	fort.Name = gym.Name.ValueOrZero()
	fort.ImageUrl = gym.Url.ValueOrZero()
	fort.Description = gym.Description.ValueOrZero()
	fort.Location = Location{Latitude: gym.Lat, Longitude: gym.Lon}
	return
}

func InitWebHookFortFromPokestop(stop *Pokestop) (fort FortWebhook) {
	if stop == nil {
		return
	}
	fort.Type = POKESTOP.String()
	fort.Name = stop.Name.ValueOrZero()
	fort.ImageUrl = stop.Url.ValueOrZero()
	fort.Description = stop.Description.ValueOrZero()
	fort.Location = Location{Latitude: stop.Lat, Longitude: stop.Lon}
	return
}

func CreateFortWebhooks(ctx context.Context, dbDetails db.DbDetails, ids []string, fortType FortType, change FortChange) {
	var gyms []Gym
	var stops []Pokestop
	if fortType == GYM {
		for _, id := range ids {
			gym, err := getGymRecord(ctx, dbDetails, id)
			if err != nil {
				continue
			}
			if gym == nil {
				continue
			}
			gyms = append(gyms, *gym)
		}
	}
	if fortType == POKESTOP {
		for _, id := range ids {
			stop, err := getPokestopRecord(ctx, dbDetails, id)
			if err != nil {
				continue
			}
			if stop == nil {
				continue
			}
			stops = append(stops, *stop)
		}
	}
	for _, gym := range gyms {
		fort := InitWebHookFortFromGym(&gym)
		CreateFortWebHooks(fort, FortWebhook{}, change)
	}
	for _, stop := range stops {
		fort := InitWebHookFortFromPokestop(&stop)
		CreateFortWebHooks(fort, FortWebhook{}, change)
	}
}

func CreateFortWebHooks(old FortWebhook, new FortWebhook, change FortChange) {
	if change == NEW {
		areas := geo.MatchGeofences(statsFeatureCollection, new.Location.Latitude, new.Location.Longitude)
		hook := map[string]interface{}{
			"change_type": change.String(),
			"new": func() interface{} {
				bytes, err := json.Marshal(new)
				if err != nil {
					return nil
				}
				return json.RawMessage(bytes)
			},
		}
		webhooks.AddMessage(webhooks.FortUpdate, hook, areas)
	} else if change == REMOVAL {
		areas := geo.MatchGeofences(statsFeatureCollection, old.Location.Latitude, old.Location.Longitude)
		hook := map[string]interface{}{
			"change_type": change.String(),
			"old": func() interface{} {
				bytes, err := json.Marshal(old)
				if err != nil {
					return nil
				}
				return json.RawMessage(bytes)
			},
		}
		webhooks.AddMessage(webhooks.FortUpdate, hook, areas)
	} else if change == EDIT {
		areas := geo.MatchGeofences(statsFeatureCollection, new.Location.Latitude, new.Location.Longitude)
		hook := map[string]interface{}{
			"change_type": change.String(),
			"edit_types":  []string{"name"}, // TODO: extract that information from new and old fort
			"old": func() interface{} {
				bytes, err := json.Marshal(old)
				if err != nil {
					return nil
				}
				return json.RawMessage(bytes)
			},
			"new": func() interface{} {
				bytes, err := json.Marshal(new)
				if err != nil {
					return nil
				}
				return json.RawMessage(bytes)
			},
		}
		webhooks.AddMessage(webhooks.FortUpdate, hook, areas)
	}
}

func UpdateFortRecordWithGetMapFortsOutProto(ctx context.Context, db db.DbDetails, mapFort *pogo.GetMapFortsOutProto_FortProto) (bool, string) {
	// when we miss, we check the gym, if again, we save it in cache for 5 minutes (in gym part)
	status, output := UpdatePokestopRecordWithGetMapFortsOutProto(ctx, db, mapFort)
	if !status {
		status, output = UpdateGymRecordWithGetMapFortsOutProto(ctx, db, mapFort)
	}

	if !status {
		getMapFortsCache.Set(mapFort.Id, mapFort, ttlcache.DefaultTTL)
		log.Debugf("Saved getMapFort in cache: %s", mapFort.Id)
	}
	return status, output
}
