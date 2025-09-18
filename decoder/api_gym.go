package decoder

import (
	"context"
	"fmt"
	"math"
	"strings"

	"golbat/db"
	"golbat/geo"
)

type ApiGymSearch struct {
	Limit   int                  `json:"limit"`
	Filters []ApiGymSearchFilter `json:"filters"`
}

type LocationDistance struct {
	Location struct {
		Latitude  float64 `json:"lat"`
		Longitude float64 `json:"lon"`
	} `json:"location"`
	Distance float64 `json:"distance"`
}

type ApiGymSearchFilter struct {
	Name             *string           `json:"name"`
	Description      *string           `json:"description"`
	LocationDistance *LocationDistance `json:"location_distance"`
	Bbox             *geo.Bbox         `json:"bbox"`
}

// SearchGymsAPI searches for gyms using the new API structure with AND filters
func SearchGymsAPI(
	ctx context.Context,
	dbDetails db.DbDetails,
	search ApiGymSearch,
) ([]string, error) {
	if len(search.Filters) == 0 {
		return []string{}, nil
	}

	// Build WHERE conditions - all filters use AND logic
	var whereConditions []string
	var args []any

	// Always include enabled = 1
	whereConditions = append(whereConditions, "enabled = 1")

	for _, filter := range search.Filters {
		// Name filter
		if filter.Name != nil && strings.TrimSpace(*filter.Name) != "" {
			whereConditions = append(whereConditions, "name LIKE ?")
			args = append(args, "%"+escapeLike(strings.TrimSpace(*filter.Name))+"%")
		}

		// Description filter
		if filter.Description != nil && strings.TrimSpace(*filter.Description) != "" {
			whereConditions = append(whereConditions, "description LIKE ?")
			args = append(args, "%"+escapeLike(strings.TrimSpace(*filter.Description))+"%")
		}

		// Location distance filter
		if filter.LocationDistance != nil {
			locDist := *filter.LocationDistance
			lat, lon := locDist.Location.Latitude, locDist.Location.Longitude
			distance := locDist.Distance

			// Calculate bounding box for performance
			latDelta := distance / 111_045.0
			lonScale := math.Cos(lat * math.Pi / 180)
			if lonScale < 1e-6 {
				lonScale = 1e-6
			}
			lonDelta := distance / (111_045.0 * lonScale)

			latMin, latMax := lat-latDelta, lat+latDelta
			lonMinRaw, lonMaxRaw := lon-lonDelta, lon+lonDelta

			lonMin := geo.NormalizeLon(lonMinRaw)
			lonMax := geo.NormalizeLon(lonMaxRaw)
			crossesAM := lonMin > lonMax

			// Add bounding box conditions
			whereConditions = append(whereConditions, "lat BETWEEN ? AND ?")
			args = append(args, latMin, latMax)

			if crossesAM {
				whereConditions = append(whereConditions, "(lon >= ? OR lon <= ?)")
				args = append(args, lonMin, lonMax)
			} else {
				whereConditions = append(whereConditions, "lon BETWEEN ? AND ?")
				args = append(args, lonMin, lonMax)
			}

			// Add precise distance condition
			whereConditions = append(whereConditions, "ST_Distance_Sphere(POINT(lon, lat), POINT(?, ?)) <= ?")
			args = append(args, lon, lat, distance)
		}

		// Bounding box filter
		if filter.Bbox != nil {
			bbox := *filter.Bbox
			latMin := math.Min(bbox.MinLat, bbox.MaxLat)
			latMax := math.Max(bbox.MinLat, bbox.MaxLat)

			lonMin := geo.NormalizeLon(bbox.MinLon)
			lonMax := geo.NormalizeLon(bbox.MaxLon)
			crossesAM := lonMin > lonMax

			whereConditions = append(whereConditions, "lat BETWEEN ? AND ?")
			args = append(args, latMin, latMax)

			if crossesAM {
				whereConditions = append(whereConditions, "(lon >= ? OR lon <= ?)")
				args = append(args, lonMin, lonMax)
			} else {
				whereConditions = append(whereConditions, "lon BETWEEN ? AND ?")
				args = append(args, lonMin, lonMax)
			}
		}
	}

	// Build the final query
	whereClause := strings.Join(whereConditions, " AND ")
	rawSQL := fmt.Sprintf(`
		SELECT id
		FROM gym
		WHERE %s
		ORDER BY id ASC
		LIMIT ?
	`, whereClause)

	args = append(args, search.Limit)
	q := dbDetails.GeneralDb.Rebind(rawSQL)

	var ids []string
	if err := dbDetails.GeneralDb.SelectContext(ctx, &ids, q, args...); err != nil {
		statsCollector.IncDbQuery("search gyms api", err)
		return nil, err
	}
	statsCollector.IncDbQuery("search gyms api", nil)
	return ids, nil
}
