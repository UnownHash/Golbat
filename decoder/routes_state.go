package decoder

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jellydator/ttlcache/v3"

	"golbat/db"
)

func loadRouteFromDatabase(ctx context.Context, db db.DbDetails, routeId string, route *Route) error {
	err := db.GeneralDb.GetContext(ctx, route,
		`SELECT * FROM route WHERE route.id = ?`, routeId)
	statsCollector.IncDbQuery("select route", err)
	return err
}

// peekRouteRecord - cache-only lookup, no DB fallback, returns locked.
// Caller MUST call returned unlock function if non-nil.
func peekRouteRecord(routeId string) (*Route, func(), error) {
	if item := routeCache.Get(routeId); item != nil {
		route := item.Value()
		route.Lock()
		return route, func() { route.Unlock() }, nil
	}
	return nil, nil, nil
}

// getRouteRecordReadOnly acquires lock but does NOT take snapshot.
// Use for read-only checks. Will cause a backing database lookup.
// Caller MUST call returned unlock function if non-nil.
func getRouteRecordReadOnly(ctx context.Context, db db.DbDetails, routeId string) (*Route, func(), error) {
	// Check cache first
	if item := routeCache.Get(routeId); item != nil {
		route := item.Value()
		route.Lock()
		return route, func() { route.Unlock() }, nil
	}

	dbRoute := Route{}
	err := loadRouteFromDatabase(ctx, db, routeId, &dbRoute)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	dbRoute.ClearDirty()

	// Atomically cache the loaded Route - if another goroutine raced us,
	// we'll get their Route and use that instead (ensuring same mutex)
	existingRoute, _ := routeCache.GetOrSetFunc(routeId, func() *Route {
		return &dbRoute
	})

	route := existingRoute.Value()
	route.Lock()
	return route, func() { route.Unlock() }, nil
}

// getRouteRecordForUpdate acquires lock AND takes snapshot for webhook comparison.
// Caller MUST call returned unlock function if non-nil.
func getRouteRecordForUpdate(ctx context.Context, db db.DbDetails, routeId string) (*Route, func(), error) {
	route, unlock, err := getRouteRecordReadOnly(ctx, db, routeId)
	if err != nil || route == nil {
		return nil, nil, err
	}
	route.snapshotOldValues()
	return route, unlock, nil
}

// getOrCreateRouteRecord gets existing or creates new, locked with snapshot.
// Caller MUST call returned unlock function.
func getOrCreateRouteRecord(ctx context.Context, db db.DbDetails, routeId string) (*Route, func(), error) {
	// Create new Route atomically - function only called if key doesn't exist
	routeItem, _ := routeCache.GetOrSetFunc(routeId, func() *Route {
		return &Route{Id: routeId, newRecord: true}
	})

	route := routeItem.Value()
	route.Lock()

	if route.newRecord {
		// We should attempt to load from database
		err := loadRouteFromDatabase(ctx, db, routeId, route)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				route.Unlock()
				return nil, nil, err
			}
		} else {
			// We loaded from DB
			route.newRecord = false
			route.ClearDirty()
		}
	}

	route.snapshotOldValues()
	return route, func() { route.Unlock() }, nil
}

func saveRouteRecord(ctx context.Context, db db.DbDetails, route *Route) error {
	// Skip save if not dirty and not new, unless 15-minute debounce expired
	if !route.IsDirty() && !route.IsNewRecord() {
		if route.Updated > time.Now().Unix()-GetUpdateThreshold(900) {
			// if a route is unchanged, but we did see it again after 15 minutes, then save again
			return nil
		}
	}

	route.SetUpdated(time.Now().Unix())

	if route.IsNewRecord() {
		if dbDebugEnabled {
			dbDebugLog("INSERT", "Route", route.Id, route.changedFields)
		}
		_, err := db.GeneralDb.NamedExecContext(ctx,
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
		_, err := db.GeneralDb.NamedExecContext(ctx,
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
