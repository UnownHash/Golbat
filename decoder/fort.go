package decoder

import (
	"context"
	"net/url"
	"strings"

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

type FortChangeWebhook struct {
	ChangeType string       `json:"change_type"`
	EditTypes  []string     `json:"edit_types,omitempty"`
	Old        *FortWebhook `json:"old,omitempty"`
	New        *FortWebhook `json:"new,omitempty"`
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
	if fortType == GYM {
		for _, id := range ids {
			gym, unlock, err := getGymRecordReadOnly(ctx, dbDetails, id)
			if err != nil {
				continue
			}
			if gym == nil {
				continue
			}

			fort := InitWebHookFortFromGym(gym)
			unlock()

			CreateFortWebHooks(fort, &FortWebhook{}, change)
		}
	}
	if fortType == POKESTOP {
		for _, id := range ids {
			stop, unlock, err := getPokestopRecordReadOnly(ctx, dbDetails, id)
			if err != nil {
				continue
			}
			if stop == nil {
				continue
			}

			fort := InitWebHookFortFromPokestop(stop)
			unlock()

			CreateFortWebHooks(fort, &FortWebhook{}, change)
		}
	}
}

func CreateFortWebHooks(old *FortWebhook, new *FortWebhook, change FortChange) {
	if change == NEW {
		areas := MatchStatsGeofence(new.Location.Latitude, new.Location.Longitude)
		hook := FortChangeWebhook{
			ChangeType: change.String(),
			New:        new,
		}
		webhooksSender.AddMessage(webhooks.FortUpdate, hook, areas)
		statsCollector.UpdateFortCount(areas, new.Type, "addition")
	} else if change == REMOVAL {
		areas := MatchStatsGeofence(old.Location.Latitude, old.Location.Longitude)
		hook := FortChangeWebhook{
			ChangeType: change.String(),
			Old:        old,
		}
		webhooksSender.AddMessage(webhooks.FortUpdate, hook, areas)
		statsCollector.UpdateFortCount(areas, old.Type, "removal")
	} else if change == EDIT {
		areas := MatchStatsGeofence(new.Location.Latitude, new.Location.Longitude)
		var editTypes []string

		// Check if Name has changed
		if old.Name == nil {
			if new.Name != nil && *new.Name != "" {
				editTypes = append(editTypes, "name")
			}
		} else if new.Name != nil && *old.Name != *new.Name {
			editTypes = append(editTypes, "name")
		}

		// Check if Description has changed
		if old.Description == nil {
			if new.Description != nil && *new.Description != "" {
				editTypes = append(editTypes, "description")
			}
		} else if new.Description != nil && *old.Description != *new.Description {
			editTypes = append(editTypes, "description")
		}

		// Check if ImageUrl has changed
		if old.ImageUrl != nil && new.ImageUrl != nil && *old.ImageUrl != *new.ImageUrl {
			oldPath := getPathFromURL(*old.ImageUrl)
			newPath := getPathFromURL(*new.ImageUrl)
			if oldPath != newPath {
				editTypes = append(editTypes, "image_url")
			}
		} else if (old.ImageUrl == nil || *old.ImageUrl == "") && new.ImageUrl != nil && *new.ImageUrl != "" {
			editTypes = append(editTypes, "image_url")
		}
		// Check if location has changed
		if !floatAlmostEqual(old.Location.Latitude, new.Location.Latitude, floatTolerance) ||
			!floatAlmostEqual(old.Location.Longitude, new.Location.Longitude, floatTolerance) {
			editTypes = append(editTypes, "location")
		}
		if len(editTypes) > 0 {
			hook := FortChangeWebhook{
				ChangeType: change.String(),
				EditTypes:  editTypes,
				Old:        old,
				New:        new,
			}
			webhooksSender.AddMessage(webhooks.FortUpdate, hook, areas)
			statsCollector.UpdateFortCount(areas, new.Type, "edit")
		}
	}
}

func getPathFromURL(u string) string {
	parsedURL, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(parsedURL.Path, "/")
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

// copySharedFieldsFrom copies shared fields from a pokestop to a gym during conversion
func (gym *Gym) copySharedFieldsFrom(pokestop *Pokestop) {
	if pokestop.Name.Valid && !gym.Name.Valid {
		gym.SetName(pokestop.Name)
	}
	if pokestop.Url.Valid && !gym.Url.Valid {
		gym.SetUrl(pokestop.Url)
	}
	if pokestop.Description.Valid && !gym.Description.Valid {
		gym.SetDescription(pokestop.Description)
	}
	if pokestop.PartnerId.Valid && !gym.PartnerId.Valid {
		gym.SetPartnerId(pokestop.PartnerId)
	}
	if pokestop.ArScanEligible.Valid && !gym.ArScanEligible.Valid {
		gym.SetArScanEligible(pokestop.ArScanEligible)
	}
	if pokestop.PowerUpLevel.Valid && !gym.PowerUpLevel.Valid {
		gym.SetPowerUpLevel(pokestop.PowerUpLevel)
	}
	if pokestop.PowerUpPoints.Valid && !gym.PowerUpPoints.Valid {
		gym.SetPowerUpPoints(pokestop.PowerUpPoints)
	}
	if pokestop.PowerUpEndTimestamp.Valid && !gym.PowerUpEndTimestamp.Valid {
		gym.SetPowerUpEndTimestamp(pokestop.PowerUpEndTimestamp)
	}
}

// copySharedFieldsFrom copies shared fields from a gym to a pokestop during conversion
func (stop *Pokestop) copySharedFieldsFrom(gym *Gym) {
	if gym.Name.Valid && !stop.Name.Valid {
		stop.SetName(gym.Name)
	}
	if gym.Url.Valid && !stop.Url.Valid {
		stop.SetUrl(gym.Url)
	}
	if gym.Description.Valid && !stop.Description.Valid {
		stop.SetDescription(gym.Description)
	}
	if gym.PartnerId.Valid && !stop.PartnerId.Valid {
		stop.SetPartnerId(gym.PartnerId)
	}
	if gym.ArScanEligible.Valid && !stop.ArScanEligible.Valid {
		stop.SetArScanEligible(gym.ArScanEligible)
	}
	if gym.PowerUpLevel.Valid && !stop.PowerUpLevel.Valid {
		stop.SetPowerUpLevel(gym.PowerUpLevel)
	}
	if gym.PowerUpPoints.Valid && !stop.PowerUpPoints.Valid {
		stop.SetPowerUpPoints(gym.PowerUpPoints)
	}
	if gym.PowerUpEndTimestamp.Valid && !stop.PowerUpEndTimestamp.Valid {
		stop.SetPowerUpEndTimestamp(gym.PowerUpEndTimestamp)
	}
}
