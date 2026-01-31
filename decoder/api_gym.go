package decoder

import (
	"context"
	"fmt"
	"math"
	"strings"

	"golbat/db"
	"golbat/geo"

	"github.com/guregu/null/v6"
)

type ApiGymResult struct {
	Id                     string      `json:"id"`
	Lat                    float64     `json:"lat"`
	Lon                    float64     `json:"lon"`
	Name                   null.String `json:"name"`
	Url                    null.String `json:"url"`
	LastModifiedTimestamp  null.Int    `json:"last_modified_timestamp"`
	RaidEndTimestamp       null.Int    `json:"raid_end_timestamp"`
	RaidSpawnTimestamp     null.Int    `json:"raid_spawn_timestamp"`
	RaidBattleTimestamp    null.Int    `json:"raid_battle_timestamp"`
	Updated                int64       `json:"updated"`
	RaidPokemonId          null.Int    `json:"raid_pokemon_id"`
	GuardingPokemonId      null.Int    `json:"guarding_pokemon_id"`
	GuardingPokemonDisplay null.String `json:"guarding_pokemon_display"`
	AvailableSlots         null.Int    `json:"available_slots"`
	TeamId                 null.Int    `json:"team_id"`
	RaidLevel              null.Int    `json:"raid_level"`
	Enabled                null.Int    `json:"enabled"`
	ExRaidEligible         null.Int    `json:"ex_raid_eligible"`
	InBattle               null.Int    `json:"in_battle"`
	RaidPokemonMove1       null.Int    `json:"raid_pokemon_move_1"`
	RaidPokemonMove2       null.Int    `json:"raid_pokemon_move_2"`
	RaidPokemonForm        null.Int    `json:"raid_pokemon_form"`
	RaidPokemonAlignment   null.Int    `json:"raid_pokemon_alignment"`
	RaidPokemonCp          null.Int    `json:"raid_pokemon_cp"`
	RaidIsExclusive        null.Int    `json:"raid_is_exclusive"`
	CellId                 null.Int    `json:"cell_id"`
	Deleted                bool        `json:"deleted"`
	TotalCp                null.Int    `json:"total_cp"`
	FirstSeenTimestamp     int64       `json:"first_seen_timestamp"`
	RaidPokemonGender      null.Int    `json:"raid_pokemon_gender"`
	SponsorId              null.Int    `json:"sponsor_id"`
	PartnerId              null.String `json:"partner_id"`
	RaidPokemonCostume     null.Int    `json:"raid_pokemon_costume"`
	RaidPokemonEvolution   null.Int    `json:"raid_pokemon_evolution"`
	ArScanEligible         null.Int    `json:"ar_scan_eligible"`
	PowerUpLevel           null.Int    `json:"power_up_level"`
	PowerUpPoints          null.Int    `json:"power_up_points"`
	PowerUpEndTimestamp    null.Int    `json:"power_up_end_timestamp"`
	Description            null.String `json:"description"`
	Defenders              null.String `json:"defenders"`
	Rsvps                  null.String `json:"rsvps"`
}

func buildGymResult(gym *Gym) ApiGymResult {
	return ApiGymResult{
		Id:                     gym.Id,
		Lat:                    gym.Lat,
		Lon:                    gym.Lon,
		Name:                   gym.Name,
		Url:                    gym.Url,
		LastModifiedTimestamp:  gym.LastModifiedTimestamp,
		RaidEndTimestamp:       gym.RaidEndTimestamp,
		RaidSpawnTimestamp:     gym.RaidSpawnTimestamp,
		RaidBattleTimestamp:    gym.RaidBattleTimestamp,
		Updated:                gym.Updated,
		RaidPokemonId:          gym.RaidPokemonId,
		GuardingPokemonId:      gym.GuardingPokemonId,
		GuardingPokemonDisplay: gym.GuardingPokemonDisplay,
		AvailableSlots:         gym.AvailableSlots,
		TeamId:                 gym.TeamId,
		RaidLevel:              gym.RaidLevel,
		Enabled:                gym.Enabled,
		ExRaidEligible:         gym.ExRaidEligible,
		InBattle:               gym.InBattle,
		RaidPokemonMove1:       gym.RaidPokemonMove1,
		RaidPokemonMove2:       gym.RaidPokemonMove2,
		RaidPokemonForm:        gym.RaidPokemonForm,
		RaidPokemonAlignment:   gym.RaidPokemonAlignment,
		RaidPokemonCp:          gym.RaidPokemonCp,
		RaidIsExclusive:        gym.RaidIsExclusive,
		CellId:                 gym.CellId,
		Deleted:                gym.Deleted,
		TotalCp:                gym.TotalCp,
		FirstSeenTimestamp:     gym.FirstSeenTimestamp,
		RaidPokemonGender:      gym.RaidPokemonGender,
		SponsorId:              gym.SponsorId,
		PartnerId:              gym.PartnerId,
		RaidPokemonCostume:     gym.RaidPokemonCostume,
		RaidPokemonEvolution:   gym.RaidPokemonEvolution,
		ArScanEligible:         gym.ArScanEligible,
		PowerUpLevel:           gym.PowerUpLevel,
		PowerUpPoints:          gym.PowerUpPoints,
		PowerUpEndTimestamp:    gym.PowerUpEndTimestamp,
		Description:            gym.Description,
		Defenders:              gym.Defenders,
		Rsvps:                  gym.Rsvps,
	}
}

func BuildGymResult(gym *Gym) ApiGymResult {
	return buildGymResult(gym)
}

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
