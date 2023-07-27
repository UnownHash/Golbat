package decoder

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/jellydator/ttlcache/v3"
	"golbat/db"
	"golbat/pogo"
	"time"
)

type Route struct {
	Id               string `db:"id"`
	Name             string `db:"name"`
	Description      string `db:"description"`
	DistanceMeters   int64  `db:"distance_meters"`
	DurationSeconds  int64  `db:"duration_seconds"`
	EndPoi           string `db:"end_poi"`
	Image            string `db:"image"`
	ImageBorderColor string `db:"image_border_color"`
	Reversible       bool   `db:"reversible"`
	StartPoi         string `db:"start_poi"`
	Tags             string `db:"tags"`
	Type             int8   `db:"type"`
	Updated          int64  `db:"updated"`
	Version          int64  `db:"version"`
	Waypoints        string `db:"waypoints"`
}

func getRouteRecord(db db.DbDetails, id string) (*Route, error) {
	inMemoryRoute := routeCache.Get(id)
	if inMemoryRoute != nil {
		route := inMemoryRoute.Value()
		return &route, nil
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
	if err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	routeCache.Set(id, route, ttlcache.DefaultTTL)
	return &route, nil
}

// hasChangesRoute compares two Route structs
func hasChangesRoute(old *Route, new *Route) bool {
	return old.Name != new.Name ||
		old.Description != new.Description ||
		old.DistanceMeters != new.DistanceMeters ||
		old.DurationSeconds != new.DurationSeconds ||
		old.EndPoi != new.EndPoi ||
		old.Image != new.Image ||
		old.ImageBorderColor != new.ImageBorderColor ||
		old.Reversible != new.Reversible ||
		old.StartPoi != new.StartPoi ||
		old.Tags != new.Tags ||
		old.Type != new.Type ||
		old.Updated != new.Updated ||
		old.Version != new.Version ||
		old.Waypoints != new.Waypoints
}

func saveRouteRecord(db db.DbDetails, route *Route) error {
	oldRoute, _ := getRouteRecord(db, route.Id)

	if oldRoute != nil && !hasChangesRoute(oldRoute, route) {
		return nil
	}

	if oldRoute == nil {
		_, err := db.GeneralDb.NamedExec(
			`
			INSERT INTO route (
			  id, name, description, distance_meters, 
			  duration_seconds, end_poi, image, 
			  image_border_color, reversible, 
			  start_poi, tags, type, updated, version, 
			  waypoints
			) 
			VALUES 
			  (
				:id, :name, :description, :distance_meters, 
				:duration_seconds, :end_poi, :image, 
				:image_border_color, :reversible, 
				:start_poi, :tags, :type, :updated, 
				:version, :waypoints
			  )
			`,
			route,
		)

		if err != nil {
			return fmt.Errorf("insert route error: %w", err)
		}
	} else {
		_, err := db.GeneralDb.NamedExec(
			`
			UPDATE route SET
				name = :name,
				description = :description,
				distance_meters = :distance_meters,
				duration_seconds = :duration_seconds,
				end_poi = :end_poi,
				image = :image,
				image_border_color = :image_border_color,
				reversible = :reversible,
				start_poi = :start_poi,
				tags = :tags,
				type = :type,
				updated = :updated,
				version = :version,
				waypoints = :waypoints
			WHERE id = :id`,
			route,
		)

		if err != nil {
			return fmt.Errorf("update route error %w", err)
		}
	}

	routeCache.Set(route.Id, *route, ttlcache.DefaultTTL)
	return nil
}

func (route *Route) updateFromSharedRouteProto(sharedRouteProto *pogo.SharedRouteProto) {
	route.Name = sharedRouteProto.GetName()
	route.Description = sharedRouteProto.GetDescription()
	route.DistanceMeters = sharedRouteProto.GetRouteDistanceMeters()
	route.DurationSeconds = sharedRouteProto.GetRouteDurationSeconds()
	route.EndPoi = sharedRouteProto.GetEndPoi().GetAnchor().GetFortId()
	route.Image = sharedRouteProto.GetImage().GetImageUrl()
	route.ImageBorderColor = sharedRouteProto.GetImage().GetBorderColorHex()
	route.Reversible = sharedRouteProto.GetReversible()
	route.StartPoi = sharedRouteProto.GetStartPoi().GetAnchor().GetFortId()
	tags, _ := json.Marshal(sharedRouteProto.GetTags())
	route.Tags = string(tags)
	route.Type = int8(sharedRouteProto.GetType())
	route.Updated = time.Now().Unix()
	route.Version = sharedRouteProto.GetVersion()
	waypoints, _ := json.Marshal(sharedRouteProto.GetWaypoints())
	route.Waypoints = string(waypoints)
}

func UpdateRouteRecordWithSharedRouteProto(db db.DbDetails, sharedRouteProto *pogo.SharedRouteProto) error {
	route, err := getRouteRecord(db, sharedRouteProto.GetId())
	if err != nil {
		return err
	}

	if route == nil {
		route = &Route{
			Id: sharedRouteProto.GetId(),
		}
	}

	route.updateFromSharedRouteProto(sharedRouteProto)
	saveError := saveRouteRecord(db, route)
	return saveError
}
