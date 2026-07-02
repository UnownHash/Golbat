package decoder

import (
	"context"
	"fmt"
	"math"
	"strings"

	"golbat/db"
	"golbat/geo"
)

// ApiGymResult is the API representation of a gym. Nullable database columns are
// represented as pointers (nil => JSON null) without omitempty so every key is
// always present.
type ApiGymResult struct {
	Id                      string   `json:"id" doc:"Fort ID of the gym"`
	Lat                     float64  `json:"lat" doc:"Latitude of the gym"`
	Lon                     float64  `json:"lon" doc:"Longitude of the gym"`
	Name                    *string  `json:"name" doc:"Name of the gym"`
	Url                     *string  `json:"url" doc:"Image URL of the gym"`
	LastModifiedTimestamp   *int64   `json:"last_modified_timestamp" doc:"Unix timestamp when the gym was last modified in-game"`
	RaidEndTimestamp        *int64   `json:"raid_end_timestamp" doc:"Unix timestamp when the current raid ends"`
	RaidSpawnTimestamp      *int64   `json:"raid_spawn_timestamp" doc:"Unix timestamp when the current raid egg spawned"`
	RaidBattleTimestamp     *int64   `json:"raid_battle_timestamp" doc:"Unix timestamp when the current raid battle begins"`
	Updated                 int64    `json:"updated" doc:"Unix timestamp when the record was last updated"`
	RaidPokemonId           *int64   `json:"raid_pokemon_id" doc:"Pokedex ID of the raid boss"`
	GuardingPokemonId       *int64   `json:"guarding_pokemon_id" doc:"Pokedex ID of the pokemon guarding the gym"`
	GuardingPokemonDisplay  *string  `json:"guarding_pokemon_display" doc:"Display details of the guarding pokemon"`
	AvailableSlots          *int64   `json:"available_slots" doc:"Number of open defender slots"`
	TeamId                  *int64   `json:"team_id" doc:"ID of the team controlling the gym"`
	RaidLevel               *int64   `json:"raid_level" doc:"Level/tier of the current raid"`
	Enabled                 *int64   `json:"enabled" doc:"Whether the gym is enabled"`
	ExRaidEligible          *int64   `json:"ex_raid_eligible" doc:"Whether the gym is eligible for EX raids"`
	InBattle                *int64   `json:"in_battle" doc:"Whether the gym is currently in battle"`
	RaidPokemonMove1        *int64   `json:"raid_pokemon_move_1" doc:"Fast move ID of the raid boss"`
	RaidPokemonMove2        *int64   `json:"raid_pokemon_move_2" doc:"Charge move ID of the raid boss"`
	RaidPokemonForm         *int64   `json:"raid_pokemon_form" doc:"Form ID of the raid boss"`
	RaidPokemonAlignment    *int64   `json:"raid_pokemon_alignment" doc:"Alignment of the raid boss"`
	RaidPokemonCp           *int64   `json:"raid_pokemon_cp" doc:"Combat power of the raid boss"`
	RaidIsExclusive         *int64   `json:"raid_is_exclusive" doc:"Whether the current raid is exclusive (EX)"`
	CellId                  *int64   `json:"cell_id" doc:"S2 cell ID the gym belongs to"`
	Deleted                 bool     `json:"deleted" doc:"Whether the gym has been deleted"`
	TotalCp                 *int64   `json:"total_cp" doc:"Total combat power of the gym defenders"`
	FirstSeenTimestamp      int64    `json:"first_seen_timestamp" doc:"Unix timestamp when the gym was first seen"`
	RaidPokemonGender       *int64   `json:"raid_pokemon_gender" doc:"Gender of the raid boss"`
	SponsorId               *int64   `json:"sponsor_id" doc:"Sponsor ID of the gym, if sponsored"`
	PartnerId               *string  `json:"partner_id" doc:"Partner ID of the gym, if partnered"`
	RaidPokemonCostume      *int64   `json:"raid_pokemon_costume" doc:"Costume ID of the raid boss"`
	RaidPokemonEvolution    *int64   `json:"raid_pokemon_evolution" doc:"Evolution ID of the raid boss (e.g. mega)"`
	RaidSeed                *int64   `json:"raid_seed" doc:"Raid seed for the current raid"`
	RaidPokemonStamina      *int64   `json:"raid_pokemon_stamina" doc:"Stamina of the raid boss"`
	RaidPokemonCpMultiplier *float64 `json:"raid_pokemon_cp_multiplier" doc:"CP multiplier of the raid boss"`
	ArScanEligible          *int64   `json:"ar_scan_eligible" doc:"Whether the gym is eligible for AR scanning"`
	PowerUpLevel            *int64   `json:"power_up_level" doc:"Power-up level of the gym"`
	PowerUpPoints           *int64   `json:"power_up_points" doc:"Power-up points accumulated for the gym"`
	PowerUpEndTimestamp     *int64   `json:"power_up_end_timestamp" doc:"Unix timestamp when the power-up ends"`
	Description             *string  `json:"description" doc:"Description of the gym"`
	Defenders               *string  `json:"defenders" doc:"Serialized defender pokemon data"`
	Rsvps                   *string  `json:"rsvps" doc:"Serialized raid RSVP data"`
}

func buildGymResult(gym *Gym) ApiGymResult {
	return ApiGymResult{
		Id:                      gym.Id,
		Lat:                     gym.Lat,
		Lon:                     gym.Lon,
		Name:                    gym.Name.Ptr(),
		Url:                     gym.Url.Ptr(),
		LastModifiedTimestamp:   gym.LastModifiedTimestamp.Ptr(),
		RaidEndTimestamp:        gym.RaidEndTimestamp.Ptr(),
		RaidSpawnTimestamp:      gym.RaidSpawnTimestamp.Ptr(),
		RaidBattleTimestamp:     gym.RaidBattleTimestamp.Ptr(),
		Updated:                 gym.Updated,
		RaidPokemonId:           gym.RaidPokemonId.Ptr(),
		GuardingPokemonId:       gym.GuardingPokemonId.Ptr(),
		GuardingPokemonDisplay:  gym.GuardingPokemonDisplay.Ptr(),
		AvailableSlots:          gym.AvailableSlots.Ptr(),
		TeamId:                  gym.TeamId.Ptr(),
		RaidLevel:               gym.RaidLevel.Ptr(),
		Enabled:                 gym.Enabled.Ptr(),
		ExRaidEligible:          gym.ExRaidEligible.Ptr(),
		InBattle:                gym.InBattle.Ptr(),
		RaidPokemonMove1:        gym.RaidPokemonMove1.Ptr(),
		RaidPokemonMove2:        gym.RaidPokemonMove2.Ptr(),
		RaidPokemonForm:         gym.RaidPokemonForm.Ptr(),
		RaidPokemonAlignment:    gym.RaidPokemonAlignment.Ptr(),
		RaidPokemonCp:           gym.RaidPokemonCp.Ptr(),
		RaidIsExclusive:         gym.RaidIsExclusive.Ptr(),
		CellId:                  gym.CellId.Ptr(),
		Deleted:                 gym.Deleted,
		TotalCp:                 gym.TotalCp.Ptr(),
		FirstSeenTimestamp:      gym.FirstSeenTimestamp,
		RaidPokemonGender:       gym.RaidPokemonGender.Ptr(),
		SponsorId:               gym.SponsorId.Ptr(),
		PartnerId:               gym.PartnerId.Ptr(),
		RaidPokemonCostume:      gym.RaidPokemonCostume.Ptr(),
		RaidPokemonEvolution:    gym.RaidPokemonEvolution.Ptr(),
		RaidSeed:                gym.RaidSeed.Ptr(),
		RaidPokemonStamina:      gym.RaidPokemonStamina.Ptr(),
		RaidPokemonCpMultiplier: gym.RaidPokemonCpMultiplier.Ptr(),
		ArScanEligible:          gym.ArScanEligible.Ptr(),
		PowerUpLevel:            gym.PowerUpLevel.Ptr(),
		PowerUpPoints:           gym.PowerUpPoints.Ptr(),
		PowerUpEndTimestamp:     gym.PowerUpEndTimestamp.Ptr(),
		Description:             gym.Description.Ptr(),
		Defenders:               gym.Defenders.Ptr(),
		Rsvps:                   gym.Rsvps.Ptr(),
	}
}

func BuildGymResult(gym *Gym) ApiGymResult {
	return buildGymResult(gym)
}

type ApiGymSearch struct {
	Limit   int                  `json:"limit" required:"false" doc:"Maximum number of gyms to return (default 500, max 10000)"`
	Filters []ApiGymSearchFilter `json:"filters" required:"false" doc:"Filter clauses; conditions within a clause are AND'd. At least one clause is required."`
}

type LocationDistance struct {
	Location struct {
		Latitude  float64 `json:"lat" doc:"Latitude of the search center"`
		Longitude float64 `json:"lon" doc:"Longitude of the search center"`
	} `json:"location" doc:"Center point of the radius search"`
	Distance float64 `json:"distance" required:"false" doc:"Search radius in meters (must be > 0, max 500000)"`
}

type ApiGymSearchFilter struct {
	Name             *string           `json:"name" required:"false" doc:"Optional gym name substring to match"`
	Description      *string           `json:"description" required:"false" doc:"Optional gym description substring to match"`
	LocationDistance *LocationDistance `json:"location_distance" required:"false" doc:"Optional geographic radius search"`
	Bbox             *geo.Bbox         `json:"bbox" required:"false" doc:"Optional bounding box search"`
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
