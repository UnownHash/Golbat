package decoder

import (
	"context"
	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
	"golbat/db"
	"golbat/pogo"
)

type FortWebhook struct {
	Type        string
	Name        string
	Description string
	ImageUrl    string
	Latitude    float64
	Longitude   float64
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

func InitWebHookFortFromGym(gym *Gym) FortWebhook {
	return FortWebhook{
		Type:        GYM.String(),
		Name:        gym.Name.ValueOrZero(),
		ImageUrl:    gym.Url.ValueOrZero(),
		Description: gym.Description.ValueOrZero(),
		Longitude:   gym.Lon,
		Latitude:    gym.Lat,
	}
}

func InitWebHookFortFromPokestop(stop *Pokestop) FortWebhook {
	return FortWebhook{
		Type:        POKESTOP.String(),
		Name:        stop.Name.ValueOrZero(),
		ImageUrl:    stop.Url.ValueOrZero(),
		Description: stop.Description.ValueOrZero(),
		Longitude:   stop.Lon,
		Latitude:    stop.Lat,
	}
}

func CreateFortWebhooks(ctx context.Context, dbDetails db.DbDetails, ids []string, fortType FortType, change FortChange) {
	var gyms []Gym
	var stops []Pokestop
	if fortType == GYM {
		for _, id := range ids {
			gym, err := GetGymRecord(ctx, dbDetails, id)
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
			stop, err := GetPokestopRecord(ctx, dbDetails, id)
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
	//TODO: send webhooks
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
