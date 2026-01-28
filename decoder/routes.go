package decoder

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"golbat/db"
	"golbat/pogo"
	"golbat/util"

	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
	"gopkg.in/guregu/null.v4"
)

// Route struct.
// REMINDER! Dirty flag pattern - use setter methods to modify fields
type Route struct {
	Id               string      `db:"id"`
	Name             string      `db:"name"`
	Shortcode        string      `db:"shortcode"`
	Description      string      `db:"description"`
	DistanceMeters   int64       `db:"distance_meters"`
	DurationSeconds  int64       `db:"duration_seconds"`
	EndFortId        string      `db:"end_fort_id"`
	EndImage         string      `db:"end_image"`
	EndLat           float64     `db:"end_lat"`
	EndLon           float64     `db:"end_lon"`
	Image            string      `db:"image"`
	ImageBorderColor string      `db:"image_border_color"`
	Reversible       bool        `db:"reversible"`
	StartFortId      string      `db:"start_fort_id"`
	StartImage       string      `db:"start_image"`
	StartLat         float64     `db:"start_lat"`
	StartLon         float64     `db:"start_lon"`
	Tags             null.String `db:"tags"`
	Type             int8        `db:"type"`
	Updated          int64       `db:"updated"`
	Version          int64       `db:"version"`
	Waypoints        string      `db:"waypoints"`

	dirty         bool     `db:"-" json:"-"` // Not persisted - tracks if object needs saving
	newRecord     bool     `db:"-" json:"-"` // Not persisted - tracks if this is a new record
	changedFields []string `db:"-" json:"-"` // Track which fields changed (only when dbDebugEnabled)
}

// IsDirty returns true if any field has been modified
func (r *Route) IsDirty() bool {
	return r.dirty
}

// ClearDirty resets the dirty flag (call after saving to DB)
func (r *Route) ClearDirty() {
	r.dirty = false
}

// IsNewRecord returns true if this is a new record (not yet in DB)
func (r *Route) IsNewRecord() bool {
	return r.newRecord
}

// --- Set methods with dirty tracking ---

func (r *Route) SetName(v string) {
	if r.Name != v {
		r.Name = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "Name")
		}
	}
}

func (r *Route) SetShortcode(v string) {
	if r.Shortcode != v {
		r.Shortcode = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "Shortcode")
		}
	}
}

func (r *Route) SetDescription(v string) {
	if r.Description != v {
		r.Description = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "Description")
		}
	}
}

func (r *Route) SetDistanceMeters(v int64) {
	if r.DistanceMeters != v {
		r.DistanceMeters = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "DistanceMeters")
		}
	}
}

func (r *Route) SetDurationSeconds(v int64) {
	if r.DurationSeconds != v {
		r.DurationSeconds = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "DurationSeconds")
		}
	}
}

func (r *Route) SetEndFortId(v string) {
	if r.EndFortId != v {
		r.EndFortId = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "EndFortId")
		}
	}
}

func (r *Route) SetEndImage(v string) {
	if r.EndImage != v {
		r.EndImage = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "EndImage")
		}
	}
}

func (r *Route) SetEndLat(v float64) {
	if !floatAlmostEqual(r.EndLat, v, floatTolerance) {
		r.EndLat = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "EndLat")
		}
	}
}

func (r *Route) SetEndLon(v float64) {
	if !floatAlmostEqual(r.EndLon, v, floatTolerance) {
		r.EndLon = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "EndLon")
		}
	}
}

func (r *Route) SetImage(v string) {
	if r.Image != v {
		r.Image = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "Image")
		}
	}
}

func (r *Route) SetImageBorderColor(v string) {
	if r.ImageBorderColor != v {
		r.ImageBorderColor = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "ImageBorderColor")
		}
	}
}

func (r *Route) SetReversible(v bool) {
	if r.Reversible != v {
		r.Reversible = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "Reversible")
		}
	}
}

func (r *Route) SetStartFortId(v string) {
	if r.StartFortId != v {
		r.StartFortId = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "StartFortId")
		}
	}
}

func (r *Route) SetStartImage(v string) {
	if r.StartImage != v {
		r.StartImage = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "StartImage")
		}
	}
}

func (r *Route) SetStartLat(v float64) {
	if !floatAlmostEqual(r.StartLat, v, floatTolerance) {
		r.StartLat = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "StartLat")
		}
	}
}

func (r *Route) SetStartLon(v float64) {
	if !floatAlmostEqual(r.StartLon, v, floatTolerance) {
		r.StartLon = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "StartLon")
		}
	}
}

func (r *Route) SetTags(v null.String) {
	if r.Tags != v {
		r.Tags = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "Tags")
		}
	}
}

func (r *Route) SetType(v int8) {
	if r.Type != v {
		r.Type = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "Type")
		}
	}
}

func (r *Route) SetVersion(v int64) {
	if r.Version != v {
		r.Version = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "Version")
		}
	}
}

func (r *Route) SetWaypoints(v string) {
	if r.Waypoints != v {
		r.Waypoints = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "Waypoints")
		}
	}
}

func getRouteRecord(db db.DbDetails, id string) (*Route, error) {
	inMemoryRoute := routeCache.Get(id)
	if inMemoryRoute != nil {
		route := inMemoryRoute.Value()
		return route, nil
	}

	route := Route{}
	err := db.GeneralDb.Get(&route,
		`
		SELECT *
		FROM route
		WHERE route.id = ?
		`,
		id,
	)
	statsCollector.IncDbQuery("select route", err)
	if err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	routeCache.Set(id, &route, ttlcache.DefaultTTL)
	return &route, nil
}

func saveRouteRecord(db db.DbDetails, route *Route) error {
	// Skip save if not dirty and not new, unless 15-minute debounce expired
	if !route.IsDirty() && !route.IsNewRecord() {
		if route.Updated > time.Now().Unix()-900 {
			// if a route is unchanged, but we did see it again after 15 minutes, then save again
			return nil
		}
	}

	route.Updated = time.Now().Unix()

	if route.IsNewRecord() {
		if dbDebugEnabled {
			dbDebugLog("INSERT", "Route", route.Id, route.changedFields)
		}
		_, err := db.GeneralDb.NamedExec(
			`
			INSERT INTO route (
			  id, name, shortcode, description, distance_meters,
			  duration_seconds, end_fort_id, end_image,
			  end_lat, end_lon, image, image_border_color,
			  reversible, start_fort_id, start_image,
			  start_lat, start_lon, tags, type,
			  updated, version, waypoints
			)
			VALUES
			  (
				:id, :name, :shortcode, :description, :distance_meters,
				:duration_seconds, :end_fort_id,
				:end_image, :end_lat, :end_lon, :image,
				:image_border_color, :reversible,
				:start_fort_id, :start_image, :start_lat,
				:start_lon, :tags, :type, :updated,
				:version, :waypoints
			  )
			`,
			route,
		)

		statsCollector.IncDbQuery("insert route", err)
		if err != nil {
			return fmt.Errorf("insert route error: %w", err)
		}
	} else {
		if dbDebugEnabled {
			dbDebugLog("UPDATE", "Route", route.Id, route.changedFields)
		}
		_, err := db.GeneralDb.NamedExec(
			`
			UPDATE route SET
				name = :name,
				shortcode = :shortcode,
				description = :description,
				distance_meters = :distance_meters,
				duration_seconds = :duration_seconds,
				end_fort_id = :end_fort_id,
				end_image = :end_image,
				end_lat = :end_lat,
				end_lon = :end_lon,
				image = :image,
				image_border_color = :image_border_color,
				reversible = :reversible,
				start_fort_id = :start_fort_id,
				start_image = :start_image,
				start_lat = :start_lat,
				start_lon = :start_lon,
				tags = :tags,
				type = :type,
				updated = :updated,
				version = :version,
				waypoints = :waypoints
			WHERE id = :id`,
			route,
		)

		statsCollector.IncDbQuery("update route", err)
		if err != nil {
			return fmt.Errorf("update route error %w", err)
		}
	}

	if dbDebugEnabled {
		route.changedFields = route.changedFields[:0]
	}
	route.ClearDirty()
	if route.IsNewRecord() {
		routeCache.Set(route.Id, route, ttlcache.DefaultTTL)
		route.newRecord = false
	}
	return nil
}

func (route *Route) updateFromSharedRouteProto(sharedRouteProto *pogo.SharedRouteProto) {
	route.SetName(sharedRouteProto.GetName())
	if sharedRouteProto.GetShortCode() != "" {
		route.SetShortcode(sharedRouteProto.GetShortCode())
	}
	description := sharedRouteProto.GetDescription()
	// NOTE: Some descriptions have more than 255 runes, which won't fit in our
	// varchar(255).
	if truncateStr, truncated := util.TruncateUTF8(description, 255); truncated {
		log.Warnf("truncating description for route id '%s'. Orig description: %s",
			route.Id,
			description,
		)
		description = truncateStr
	}
	route.SetDescription(description)
	route.SetDistanceMeters(sharedRouteProto.GetRouteDistanceMeters())
	route.SetDurationSeconds(sharedRouteProto.GetRouteDurationSeconds())
	route.SetEndFortId(sharedRouteProto.GetEndPoi().GetAnchor().GetFortId())
	route.SetEndImage(sharedRouteProto.GetEndPoi().GetImageUrl())
	route.SetEndLat(sharedRouteProto.GetEndPoi().GetAnchor().GetLatDegrees())
	route.SetEndLon(sharedRouteProto.GetEndPoi().GetAnchor().GetLngDegrees())
	route.SetImage(sharedRouteProto.GetImage().GetImageUrl())
	route.SetImageBorderColor(sharedRouteProto.GetImage().GetBorderColorHex())
	route.SetReversible(sharedRouteProto.GetReversible())
	route.SetStartFortId(sharedRouteProto.GetStartPoi().GetAnchor().GetFortId())
	route.SetStartImage(sharedRouteProto.GetStartPoi().GetImageUrl())
	route.SetStartLat(sharedRouteProto.GetStartPoi().GetAnchor().GetLatDegrees())
	route.SetStartLon(sharedRouteProto.GetStartPoi().GetAnchor().GetLngDegrees())
	route.SetType(int8(sharedRouteProto.GetType()))
	route.SetVersion(sharedRouteProto.GetVersion())
	waypoints, _ := json.Marshal(sharedRouteProto.GetWaypoints())
	route.SetWaypoints(string(waypoints))

	if len(sharedRouteProto.GetTags()) > 0 {
		tags, _ := json.Marshal(sharedRouteProto.GetTags())
		route.SetTags(null.StringFrom(string(tags)))
	}
}

func UpdateRouteRecordWithSharedRouteProto(db db.DbDetails, sharedRouteProto *pogo.SharedRouteProto) error {
	routeMutex, _ := routeStripedMutex.GetLock(sharedRouteProto.GetId())
	routeMutex.Lock()
	defer routeMutex.Unlock()

	route, err := getRouteRecord(db, sharedRouteProto.GetId())
	if err != nil {
		return err
	}

	if route == nil {
		route = &Route{
			Id:        sharedRouteProto.GetId(),
			newRecord: true,
		}
	}

	route.updateFromSharedRouteProto(sharedRouteProto)
	saveError := saveRouteRecord(db, route)
	return saveError
}
