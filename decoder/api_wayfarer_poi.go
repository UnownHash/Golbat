package decoder

import (
	"context"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"

	"golbat/config"
	"golbat/db"
)

// ApiWayfarerPoiResult is the API representation of a Wayfarer POI. Nullable
// database columns are represented as pointers (nil => JSON null) without
// omitempty so every key is always present.
type ApiWayfarerPoiResult struct {
	Id                 string  `json:"id" doc:"POI ID; the fort ID when the POI is in the game"`
	Lat                float64 `json:"lat" doc:"Latitude of the POI"`
	Lon                float64 `json:"lon" doc:"Longitude of the POI"`
	Name               *string `json:"name" doc:"Title of the POI"`
	Description        *string `json:"description" doc:"Description of the POI"`
	Url                *string `json:"url" doc:"Image URL of the POI"`
	InGame             bool    `json:"in_game" doc:"Whether the POI exists in Pokemon GO (as a pokestop or gym)"`
	FirstSeenTimestamp int64   `json:"first_seen_timestamp" doc:"Unix timestamp when the POI was first seen"`
	Updated            int64   `json:"updated" doc:"Unix timestamp when the record was last updated"`
}

// ApiWayfarerPoiScan is the request body for POST /api/wayfarer-poi/scan.
type ApiWayfarerPoiScan struct {
	Min          ApiLatLon `json:"min" doc:"SW (minimum lat/lon) corner of the bounding box."`
	Max          ApiLatLon `json:"max" doc:"NE (maximum lat/lon) corner of the bounding box."`
	Limit        int       `json:"limit" required:"false" doc:"Max results to return; 0 uses the server default. Clamped by tuning.max_fort_results."`
	UpdatedAfter int64     `json:"updated_after" required:"false" minimum:"0" doc:"Only return POIs whose updated timestamp is strictly newer than this unix time; 0 or omitted returns everything. Applied before the limit."`
	InGame       *bool     `json:"in_game" required:"false" doc:"Only return POIs that are (true) or are not (false) in the game; omitted or null returns both."`
}

type ApiWayfarerPoiScanResult struct {
	WayfarerPois []*ApiWayfarerPoiResult `json:"wayfarer_pois" doc:"Matching POIs within the bounding box, ordered by id."`
	LimitReached bool                    `json:"limit_reached" doc:"Whether more POIs matched than the effective result limit."`
}

func buildWayfarerPoiResult(p *WayfarerPoiData) *ApiWayfarerPoiResult {
	return &ApiWayfarerPoiResult{
		Id:                 p.Id,
		Lat:                p.Lat,
		Lon:                p.Lon,
		Name:               p.Name.Ptr(),
		Description:        p.Description.Ptr(),
		Url:                p.Url.Ptr(),
		InGame:             p.InGame,
		FirstSeenTimestamp: p.FirstSeenTimestamp,
		Updated:            p.Updated,
	}
}

// wayfarerPoiScanLimit: requested limit clamped by tuning.max_fort_results (0 = server default).
func wayfarerPoiScanLimit(limit int) int {
	maxPois := config.Config.Tuning.MaxFortResults
	if limit > 0 && limit < maxPois {
		maxPois = limit
	}
	return maxPois
}

// wayfarerPoiScanQuery builds the scan SQL. One row more than the limit is
// asked for, which is how limit_reached is known.
func wayfarerPoiScanQuery(p ApiWayfarerPoiScan, limit int) (string, []any) {
	var q strings.Builder
	q.WriteString(`SELECT ` + wayfarerPoiSelectColumns + ` FROM wayfarer_poi WHERE lat BETWEEN ? AND ? AND lon BETWEEN ? AND ?`)
	args := []any{p.Min.Lat, p.Max.Lat, p.Min.Lon, p.Max.Lon}
	if p.InGame != nil {
		q.WriteString(` AND in_game = ?`)
		args = append(args, *p.InGame)
	}
	if p.UpdatedAfter > 0 {
		q.WriteString(` AND updated > ?`)
		args = append(args, p.UpdatedAfter)
	}
	q.WriteString(` ORDER BY id LIMIT ?`)
	return q.String(), append(args, limit+1)
}

// WayfarerPoiScanEndpoint answers a bounding-box scan from the database:
// Wayfarer POIs are not held in a spatial index. A POI seen in the last moments
// may not be there yet (write-behind).
func WayfarerPoiScanEndpoint(ctx context.Context, db db.DbDetails, p ApiWayfarerPoiScan) (*ApiWayfarerPoiScanResult, error) {
	start := time.Now()
	limit := wayfarerPoiScanLimit(p.Limit)
	query, args := wayfarerPoiScanQuery(p, limit)

	var rows []WayfarerPoiData
	err := timedDbQuery("WayfarerPoiScan", db.GeneralDb, func() error {
		err := db.GeneralDb.SelectContext(ctx, &rows, query, args...)
		getStatsCollector().IncDbQuery("scan wayfarer_poi", err)
		return err
	})
	if err != nil {
		return nil, err
	}

	limitReached := len(rows) > limit
	if limitReached {
		rows = rows[:limit]
	}
	results := make([]*ApiWayfarerPoiResult, 0, len(rows))
	for i := range rows {
		results = append(results, buildWayfarerPoiResult(&rows[i]))
	}

	log.Infof("WayfarerPoiScan - scan time %s, %d returned", time.Since(start), len(results))

	return &ApiWayfarerPoiScanResult{WayfarerPois: results, LimitReached: limitReached}, nil
}

// GetWayfarerPois reads POIs by id from the database. Unknown ids are omitted.
func GetWayfarerPois(ctx context.Context, db db.DbDetails, ids []string) ([]*ApiWayfarerPoiResult, error) {
	if len(ids) == 0 {
		return []*ApiWayfarerPoiResult{}, nil
	}
	query, args, err := sqlx.In(`SELECT `+wayfarerPoiSelectColumns+` FROM wayfarer_poi WHERE id IN (?) ORDER BY id`, ids)
	if err != nil {
		return nil, err
	}

	var rows []WayfarerPoiData
	err = timedDbQuery("GetWayfarerPois", db.GeneralDb, func() error {
		err := db.GeneralDb.SelectContext(ctx, &rows, query, args...)
		getStatsCollector().IncDbQuery("select wayfarer_poi", err)
		return err
	})
	if err != nil {
		return nil, err
	}

	results := make([]*ApiWayfarerPoiResult, 0, len(rows))
	for i := range rows {
		results = append(results, buildWayfarerPoiResult(&rows[i]))
	}
	return results, nil
}
