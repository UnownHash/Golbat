package decoder

import (
	"context"
	"net/url"
	"strings"

	"github.com/guregu/null/v6"
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
			gym, unlock, err := GetGymRecordReadOnly(ctx, dbDetails, id)
			if err != nil || gym == nil {
				if unlock != nil {
					unlock()
				}
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
			if err != nil || stop == nil {
				if unlock != nil {
					unlock()
				}
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

// SharedFortFields holds fields shared between gyms and pokestops for safe cross-entity copying.
// This allows copying data without holding locks on both entities simultaneously.
type SharedFortFields struct {
	Name                null.String
	Url                 null.String
	Description         null.String
	PartnerId           null.String
	ArScanEligible      null.Int64
	PowerUpLevel        null.Int64
	PowerUpPoints       null.Int64
	PowerUpEndTimestamp null.Int64
}

// GetSharedFields returns a copy of shared fields from a Gym.
// Safe to call while holding the gym lock.
func (gym *Gym) GetSharedFields() SharedFortFields {
	return SharedFortFields{
		Name:                gym.Name,
		Url:                 gym.Url,
		Description:         gym.Description,
		PartnerId:           gym.PartnerId,
		ArScanEligible:      gym.ArScanEligible,
		PowerUpLevel:        gym.PowerUpLevel,
		PowerUpPoints:       gym.PowerUpPoints,
		PowerUpEndTimestamp: gym.PowerUpEndTimestamp,
	}
}

// GetSharedFields returns a copy of shared fields from a Pokestop.
// Safe to call while holding the pokestop lock.
func (stop *Pokestop) GetSharedFields() SharedFortFields {
	return SharedFortFields{
		Name:                stop.Name,
		Url:                 stop.Url,
		Description:         stop.Description,
		PartnerId:           stop.PartnerId,
		ArScanEligible:      stop.ArScanEligible,
		PowerUpLevel:        stop.PowerUpLevel,
		PowerUpPoints:       stop.PowerUpPoints,
		PowerUpEndTimestamp: stop.PowerUpEndTimestamp,
	}
}

// ApplySharedFields applies shared fields to a Gym if not already set.
// Safe to call while holding only the gym lock.
func (gym *Gym) ApplySharedFields(fields SharedFortFields) {
	if fields.Name.Valid && !gym.Name.Valid {
		gym.SetName(fields.Name)
	}
	if fields.Url.Valid && !gym.Url.Valid {
		gym.SetUrl(fields.Url)
	}
	if fields.Description.Valid && !gym.Description.Valid {
		gym.SetDescription(fields.Description)
	}
	if fields.PartnerId.Valid && !gym.PartnerId.Valid {
		gym.SetPartnerId(fields.PartnerId)
	}
	if fields.ArScanEligible.Valid && !gym.ArScanEligible.Valid {
		gym.SetArScanEligible(fields.ArScanEligible)
	}
	if fields.PowerUpLevel.Valid && !gym.PowerUpLevel.Valid {
		gym.SetPowerUpLevel(fields.PowerUpLevel)
	}
	if fields.PowerUpPoints.Valid && !gym.PowerUpPoints.Valid {
		gym.SetPowerUpPoints(fields.PowerUpPoints)
	}
	if fields.PowerUpEndTimestamp.Valid && !gym.PowerUpEndTimestamp.Valid {
		gym.SetPowerUpEndTimestamp(fields.PowerUpEndTimestamp)
	}
}

// ApplySharedFields applies shared fields to a Pokestop if not already set.
// Safe to call while holding only the pokestop lock.
func (stop *Pokestop) ApplySharedFields(fields SharedFortFields) {
	if fields.Name.Valid && !stop.Name.Valid {
		stop.SetName(fields.Name)
	}
	if fields.Url.Valid && !stop.Url.Valid {
		stop.SetUrl(fields.Url)
	}
	if fields.Description.Valid && !stop.Description.Valid {
		stop.SetDescription(fields.Description)
	}
	if fields.PartnerId.Valid && !stop.PartnerId.Valid {
		stop.SetPartnerId(fields.PartnerId)
	}
	if fields.ArScanEligible.Valid && !stop.ArScanEligible.Valid {
		stop.SetArScanEligible(fields.ArScanEligible)
	}
	if fields.PowerUpLevel.Valid && !stop.PowerUpLevel.Valid {
		stop.SetPowerUpLevel(fields.PowerUpLevel)
	}
	if fields.PowerUpPoints.Valid && !stop.PowerUpPoints.Valid {
		stop.SetPowerUpPoints(fields.PowerUpPoints)
	}
	if fields.PowerUpEndTimestamp.Valid && !stop.PowerUpEndTimestamp.Valid {
		stop.SetPowerUpEndTimestamp(fields.PowerUpEndTimestamp)
	}
}
