package decoder

import (
	"context"
	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
	"golbat/db"
	"golbat/pogo"
	"golbat/webhooks"
)

type Location struct {
	Latitude  float64 `json:"lat"`
	Longitude float64 `json:"lon"`
}

type FortWebhook struct {
	Id          string   `json:"id"`
	Type        string   `json:"type"`
	Name        *string  `json:"name"`
	Description *string  `json:"description"`
	ImageUrl    *string  `json:"image_url"`
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

func InitWebHookFortFromGym(gym *Gym) *FortWebhook {
	fort := &FortWebhook{}
	if gym == nil {
		return nil
	}
	fort.Type = GYM.String()
	fort.Id = gym.Id
	fort.Name = gym.Name.Ptr()
	fort.ImageUrl = gym.Url.Ptr()
	fort.Description = gym.Description.Ptr()
	fort.Location = Location{Latitude: gym.Lat, Longitude: gym.Lon}
	return fort
}

func InitWebHookFortFromPokestop(stop *Pokestop) *FortWebhook {
	fort := &FortWebhook{}
	if stop == nil {
		return nil
	}
	fort.Type = POKESTOP.String()
	fort.Id = stop.Id
	fort.Name = stop.Name.Ptr()
	fort.ImageUrl = stop.Url.Ptr()
	fort.Description = stop.Description.Ptr()
	fort.Location = Location{Latitude: stop.Lat, Longitude: stop.Lon}
	return fort
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
		CreateFortWebHooks(fort, &FortWebhook{}, change)
	}
	for _, stop := range stops {
		fort := InitWebHookFortFromPokestop(&stop)
		CreateFortWebHooks(fort, &FortWebhook{}, change)
	}
}

func CreateFortWebHooks(old *FortWebhook, new *FortWebhook, change FortChange) {
	if change == NEW {
		areas := MatchStatsGeofence(new.Location.Latitude, new.Location.Longitude)
		hook := map[string]interface{}{
			"change_type": change.String(),
			"new":         new,
		}
		webhooks.AddMessage(webhooks.FortUpdate, hook, areas)
	} else if change == REMOVAL {
		areas := MatchStatsGeofence(old.Location.Latitude, old.Location.Longitude)
		hook := map[string]interface{}{
			"change_type": change.String(),
			"old":         old,
		}
		webhooks.AddMessage(webhooks.FortUpdate, hook, areas)
	} else if change == EDIT {
		areas := MatchStatsGeofence(new.Location.Latitude, new.Location.Longitude)
		var editTypes []string
		if !(old.Name == nil && new.Name == nil) &&
			(old.Name == nil || new.Name == nil || *old.Name != *new.Name) {
			editTypes = append(editTypes, "name")
		}
		if !(old.Description == nil && new.Description == nil) &&
			(old.Description == nil || new.Description == nil || *old.Description != *new.Description) {
			editTypes = append(editTypes, "description")
		}
		if !(old.ImageUrl == nil && new.ImageUrl == nil) &&
			(old.ImageUrl == nil || new.ImageUrl == nil || *old.ImageUrl != *new.ImageUrl) {
			editTypes = append(editTypes, "image_url")
		}
		if !floatAlmostEqual(old.Location.Latitude, new.Location.Latitude, floatTolerance) ||
			!floatAlmostEqual(old.Location.Longitude, new.Location.Longitude, floatTolerance) {
			editTypes = append(editTypes, "location")
		}
		if len(editTypes) > 0 {
			hook := map[string]interface{}{
				"change_type": change.String(),
				"edit_types":  editTypes,
				"old":         old,
				"new":         new,
			}
			webhooks.AddMessage(webhooks.FortUpdate, hook, areas)
		}
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
