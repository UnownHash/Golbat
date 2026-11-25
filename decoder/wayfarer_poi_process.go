package decoder

import (
	"context"
	"fmt"

	"github.com/guregu/null/v6"
	log "github.com/sirupsen/logrus"

	"golbat/db"
	"golbat/pogo"
	"golbat/util"
)

// wayfarerPoiIdMaxLen is the width of the wayfarer_poi.id column.
const wayfarerPoiIdMaxLen = 64

// nullIfEmpty maps the proto3 default for an absent string to NULL.
func nullIfEmpty(s string) null.String {
	return null.NewString(s, s != "")
}

func (p *WayfarerPoi) updateFromGeodataPoiProto(poi *pogo.TitanGeodataServiceGameClientPoiProto) {
	p.SetLat(float64(poi.GetLocation().GetLatitudeE6()) / 1e6)
	p.SetLon(float64(poi.GetLocation().GetLongitudeE6()) / 1e6)
	name, _ := util.TruncateUTF8(poi.GetTitle(), 128)
	p.SetName(nullIfEmpty(name))
	p.SetDescription(nullIfEmpty(poi.GetDescription()))
	if url := poi.GetImageUrl(); len(url) <= 200 {
		p.SetUrl(nullIfEmpty(url))
	}
	p.SetInGame(poi.GetIsInGame())
}

// UpdateWayfarerPoisWithGeodataProto stores the POIs of a Wayfarer geodata
// response (TitanGetPoisInRadiusOutProto). POIs without an id or a location
// are skipped. Only what the responses return is known: a POI that stops
// being returned is not removed.
func UpdateWayfarerPoisWithGeodataProto(ctx context.Context, db db.DbDetails, pois []*pogo.TitanGeodataServiceGameClientPoiProto) string {
	saved, inGame, skipped, failed := 0, 0, 0, 0
	for _, poi := range pois {
		id := poi.GetPoiId()
		if id == "" || len(id) > wayfarerPoiIdMaxLen || poi.GetLocation() == nil {
			skipped++
			continue
		}
		if err := updateWayfarerPoi(ctx, db, id, poi); err != nil {
			log.Errorf("Failed to save wayfarer poi %s: %s", id, err)
			failed++
			continue
		}
		saved++
		if poi.GetIsInGame() {
			inGame++
		}
	}
	return fmt.Sprintf("%d wayfarer pois (%d in game), %d skipped, %d failed", saved, inGame, skipped, failed)
}

func updateWayfarerPoi(ctx context.Context, db db.DbDetails, id string, proto *pogo.TitanGeodataServiceGameClientPoiProto) error {
	poi, unlock, err := getOrCreateWayfarerPoiRecord(ctx, db, id, "UpdateWayfarerPoi")
	if err != nil {
		return err
	}
	defer unlock()

	poi.updateFromGeodataPoiProto(proto)
	return saveWayfarerPoiRecord(ctx, db, poi)
}
