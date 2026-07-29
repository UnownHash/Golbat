package decoder

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/guregu/null/v6"

	"golbat/db"
	"golbat/geo"
)

// ApiGymGuardingPokemon is the display detail of the pokemon guarding a gym.
// It is doc-only: used purely for OpenAPI schema reflection (see
// ApiGymGuardingPokemonRaw.Schema) and never unmarshaled into. The wire value
// is passed through verbatim from the stored JSON blob.
type ApiGymGuardingPokemon struct {
	Form                  int    `json:"form,omitempty" doc:"Form id"`
	Costume               int    `json:"costume,omitempty" doc:"Costume id"`
	Gender                int    `json:"gender" doc:"Gender"`
	Shiny                 bool   `json:"shiny,omitempty" doc:"Shiny"`
	TempEvolution         int    `json:"temp_evolution,omitempty" doc:"Temp (mega) evolution id"`
	TempEvolutionFinishMs int64  `json:"temp_evolution_finish_ms,omitempty" doc:"Temp evolution finish (ms)"`
	Alignment             int    `json:"alignment,omitempty" doc:"Alignment (shadow/purified)"`
	Badge                 int    `json:"badge,omitempty" doc:"Pokemon badge"`
	Background            *int64 `json:"background,omitempty" doc:"Background id"`
}

// ApiGymDefender is one pokemon defending a gym. It is doc-only: used purely
// for OpenAPI schema reflection (see ApiGymDefendersRaw.Schema) and never
// unmarshaled into. The wire value is passed through verbatim from the stored
// JSON blob. MotivationNow is a plain float64 here (rather than
// util.RoundedFloat4) since this struct is never marshaled for the wire — the
// two types produce an identical `number` schema.
type ApiGymDefender struct {
	PokemonId             int     `json:"pokemon_id,omitempty" doc:"Defender pokedex id"`
	Form                  int     `json:"form,omitempty" doc:"Form id"`
	Costume               int     `json:"costume,omitempty" doc:"Costume id"`
	Gender                int     `json:"gender" doc:"Gender"`
	Shiny                 bool    `json:"shiny,omitempty" doc:"Shiny"`
	TempEvolution         int     `json:"temp_evolution,omitempty" doc:"Temp evolution id"`
	TempEvolutionFinishMs int64   `json:"temp_evolution_finish_ms,omitempty" doc:"Temp evolution finish (ms)"`
	Alignment             int     `json:"alignment,omitempty" doc:"Alignment"`
	Badge                 int     `json:"badge,omitempty" doc:"Badge"`
	Background            *int64  `json:"background,omitempty" doc:"Background id"`
	DeployedMs            int64   `json:"deployed_ms,omitempty" doc:"Deployment duration (ms)"`
	DeployedTime          int64   `json:"deployed_time,omitempty" doc:"Approx unix deploy time"`
	BattlesWon            int32   `json:"battles_won" doc:"Battles won"`
	BattlesLost           int32   `json:"battles_lost" doc:"Battles lost"`
	TimesFed              int32   `json:"times_fed" doc:"Times fed"`
	MotivationNow         float64 `json:"motivation_now" doc:"Current motivation"`
	CpNow                 int32   `json:"cp_now" doc:"Current CP"`
	CpWhenDeployed        int32   `json:"cp_when_deployed" doc:"CP when deployed"`
}

// jsonRaw wraps a pre-serialized JSON blob stored as a null.String column so it
// is emitted as native JSON instead of an escaped string. Returns nil (JSON
// null) when the column is unset, empty, or not valid JSON.
func jsonRaw(s null.String) *json.RawMessage {
	if !s.Valid || s.String == "" || !json.Valid([]byte(s.String)) {
		return nil
	}
	r := json.RawMessage(s.String)
	return &r
}

// ApiGymGuardingPokemonRaw carries the stored guarding_pokemon_display JSON
// verbatim (no decode/re-encode). Schema() advertises the ApiGymGuardingPokemon
// shape to OpenAPI; the wire bytes are passed through unchanged.
type ApiGymGuardingPokemonRaw json.RawMessage

// MarshalJSON returns the wrapped bytes verbatim. This must be defined
// explicitly: a named json.RawMessage type does not inherit RawMessage's
// MarshalJSON, so without this override encoding/json would base64-encode it
// as a byte slice instead of passing it through as JSON.
func (m ApiGymGuardingPokemonRaw) MarshalJSON() ([]byte, error) {
	if len(m) == 0 {
		return []byte("null"), nil
	}
	return m, nil
}

// Schema implements huma.SchemaProvider, documenting the real
// ApiGymGuardingPokemon shape even though the wire value is passed through raw.
func (ApiGymGuardingPokemonRaw) Schema(r huma.Registry) *huma.Schema {
	return r.Schema(reflect.TypeOf(ApiGymGuardingPokemon{}), true, "ApiGymGuardingPokemon")
}

// ApiGymDefendersRaw carries the stored defenders JSON array verbatim (no
// decode/re-encode). Schema() advertises the []ApiGymDefender shape to
// OpenAPI; the wire bytes are passed through unchanged.
type ApiGymDefendersRaw json.RawMessage

// MarshalJSON returns the wrapped bytes verbatim (see
// ApiGymGuardingPokemonRaw.MarshalJSON for why this override is required).
func (m ApiGymDefendersRaw) MarshalJSON() ([]byte, error) {
	if len(m) == 0 {
		return []byte("null"), nil
	}
	return m, nil
}

// Schema implements huma.SchemaProvider, documenting the real
// []ApiGymDefender shape even though the wire value is passed through raw.
func (ApiGymDefendersRaw) Schema(r huma.Registry) *huma.Schema {
	return r.Schema(reflect.TypeOf([]ApiGymDefender{}), true, "ApiGymDefender")
}

// gymGuardingRaw returns the pre-serialized guarding_pokemon_display blob
// stored on the Gym record as a verbatim raw-JSON passthrough value. Returns
// nil (JSON null) when the column is unset, empty, or not valid JSON, so we
// never emit invalid JSON verbatim.
func gymGuardingRaw(s null.String) ApiGymGuardingPokemonRaw {
	if !s.Valid || s.String == "" || !json.Valid([]byte(s.String)) {
		return nil
	}
	return ApiGymGuardingPokemonRaw(s.String)
}

// gymDefendersRaw returns the pre-serialized defenders blob stored on the Gym
// record as a verbatim raw-JSON passthrough value. Returns nil (JSON null)
// when the column is unset, empty, or not valid JSON, so we never emit
// invalid JSON verbatim.
func gymDefendersRaw(s null.String) ApiGymDefendersRaw {
	if !s.Valid || s.String == "" || !json.Valid([]byte(s.String)) {
		return nil
	}
	return ApiGymDefendersRaw(s.String)
}

// ApiGymResult is the API representation of a gym. Nullable database columns are
// represented as pointers (nil => JSON null) without omitempty so every key is
// always present.
type ApiGymResult struct {
	Id                     string                   `json:"id" doc:"Fort ID of the gym"`
	Lat                    float64                  `json:"lat" doc:"Latitude of the gym"`
	Lon                    float64                  `json:"lon" doc:"Longitude of the gym"`
	Name                   *string                  `json:"name" doc:"Name of the gym"`
	Url                    *string                  `json:"url" doc:"Image URL of the gym"`
	LastModifiedTimestamp  *int64                   `json:"last_modified_timestamp" doc:"Unix timestamp when the gym was last modified in-game"`
	RaidEndTimestamp       *int64                   `json:"raid_end_timestamp" doc:"Unix timestamp when the current raid ends"`
	RaidSpawnTimestamp     *int64                   `json:"raid_spawn_timestamp" doc:"Unix timestamp when the current raid egg spawned"`
	RaidBattleTimestamp    *int64                   `json:"raid_battle_timestamp" doc:"Unix timestamp when the current raid battle begins"`
	Updated                int64                    `json:"updated" doc:"Unix timestamp when the record was last updated"`
	RaidPokemonId          *int64                   `json:"raid_pokemon_id" doc:"Pokedex ID of the raid boss"`
	GuardingPokemonId      *int64                   `json:"guarding_pokemon_id" doc:"Pokedex ID of the pokemon guarding the gym"`
	GuardingPokemonDisplay ApiGymGuardingPokemonRaw `json:"guarding_pokemon_display" doc:"Display details of the guarding pokemon"`
	AvailableSlots         *int64                   `json:"available_slots" doc:"Number of open defender slots"`
	TeamId                 *int64                   `json:"team_id" doc:"ID of the team controlling the gym"`
	RaidLevel              *int64                   `json:"raid_level" doc:"Level/tier of the current raid"`
	Enabled                *int64                   `json:"enabled" doc:"Whether the gym is enabled"`
	ExRaidEligible         *int64                   `json:"ex_raid_eligible" doc:"Whether the gym is eligible for EX raids"`
	InBattle               *int64                   `json:"in_battle" doc:"Whether the gym is currently in battle"`
	RaidPokemonMove1       *int64                   `json:"raid_pokemon_move_1" doc:"Fast move ID of the raid boss"`
	RaidPokemonMove2       *int64                   `json:"raid_pokemon_move_2" doc:"Charge move ID of the raid boss"`
	RaidPokemonForm        *int64                   `json:"raid_pokemon_form" doc:"Form ID of the raid boss"`
	RaidPokemonAlignment   *int64                   `json:"raid_pokemon_alignment" doc:"Alignment of the raid boss"`
	RaidPokemonCp          *int64                   `json:"raid_pokemon_cp" doc:"Combat power of the raid boss"`
	RaidIsExclusive        *int64                   `json:"raid_is_exclusive" doc:"Whether the current raid is exclusive (EX)"`
	CellId                 *int64                   `json:"cell_id" doc:"S2 cell ID the gym belongs to"`
	Deleted                bool                     `json:"deleted" doc:"Whether the gym has been deleted"`
	TotalCp                *int64                   `json:"total_cp" doc:"Total combat power of the gym defenders"`
	FirstSeenTimestamp     int64                    `json:"first_seen_timestamp" doc:"Unix timestamp when the gym was first seen"`
	RaidPokemonGender      *int64                   `json:"raid_pokemon_gender" doc:"Gender of the raid boss"`
	SponsorId              *int64                   `json:"sponsor_id" doc:"Sponsor ID of the gym, if sponsored"`
	PartnerId              *string                  `json:"partner_id" doc:"Partner ID of the gym, if partnered"`
	RaidPokemonCostume     *int64                   `json:"raid_pokemon_costume" doc:"Costume ID of the raid boss"`
	RaidPokemonEvolution   *int64                   `json:"raid_pokemon_evolution" doc:"Evolution ID of the raid boss (e.g. mega)"`
	ArScanEligible         *int64                   `json:"ar_scan_eligible" doc:"Whether the gym is eligible for AR scanning"`
	PowerUpLevel           *int64                   `json:"power_up_level" doc:"Power-up level of the gym"`
	PowerUpPoints          *int64                   `json:"power_up_points" doc:"Power-up points accumulated for the gym"`
	PowerUpEndTimestamp    *int64                   `json:"power_up_end_timestamp" doc:"Unix timestamp when the power-up ends"`
	Description            *string                  `json:"description" doc:"Description of the gym"`
	Defenders              ApiGymDefendersRaw       `json:"defenders" doc:"Defender pokemon"`
	Rsvps                  *json.RawMessage         `json:"rsvps" doc:"Raid RSVP data"`
}

func buildGymResult(gym *Gym) ApiGymResult {
	return ApiGymResult{
		Id:                     gym.Id,
		Lat:                    gym.Lat,
		Lon:                    gym.Lon,
		Name:                   gym.Name.Ptr(),
		Url:                    gym.Url.Ptr(),
		LastModifiedTimestamp:  gym.LastModifiedTimestamp.Ptr(),
		RaidEndTimestamp:       gym.RaidEndTimestamp.Ptr(),
		RaidSpawnTimestamp:     gym.RaidSpawnTimestamp.Ptr(),
		RaidBattleTimestamp:    gym.RaidBattleTimestamp.Ptr(),
		Updated:                gym.Updated,
		RaidPokemonId:          gym.RaidPokemonId.Ptr(),
		GuardingPokemonId:      gym.GuardingPokemonId.Ptr(),
		GuardingPokemonDisplay: gymGuardingRaw(gym.GuardingPokemonDisplay),
		AvailableSlots:         gym.AvailableSlots.Ptr(),
		TeamId:                 gym.TeamId.Ptr(),
		RaidLevel:              gym.RaidLevel.Ptr(),
		Enabled:                gym.Enabled.Ptr(),
		ExRaidEligible:         gym.ExRaidEligible.Ptr(),
		InBattle:               gym.InBattle.Ptr(),
		RaidPokemonMove1:       gym.RaidPokemonMove1.Ptr(),
		RaidPokemonMove2:       gym.RaidPokemonMove2.Ptr(),
		RaidPokemonForm:        gym.RaidPokemonForm.Ptr(),
		RaidPokemonAlignment:   gym.RaidPokemonAlignment.Ptr(),
		RaidPokemonCp:          gym.RaidPokemonCp.Ptr(),
		RaidIsExclusive:        gym.RaidIsExclusive.Ptr(),
		CellId:                 gym.CellId.Ptr(),
		Deleted:                gym.Deleted,
		TotalCp:                gym.TotalCp.Ptr(),
		FirstSeenTimestamp:     gym.FirstSeenTimestamp,
		RaidPokemonGender:      gym.RaidPokemonGender.Ptr(),
		SponsorId:              gym.SponsorId.Ptr(),
		PartnerId:              gym.PartnerId.Ptr(),
		RaidPokemonCostume:     gym.RaidPokemonCostume.Ptr(),
		RaidPokemonEvolution:   gym.RaidPokemonEvolution.Ptr(),
		ArScanEligible:         gym.ArScanEligible.Ptr(),
		PowerUpLevel:           gym.PowerUpLevel.Ptr(),
		PowerUpPoints:          gym.PowerUpPoints.Ptr(),
		PowerUpEndTimestamp:    gym.PowerUpEndTimestamp.Ptr(),
		Description:            gym.Description.Ptr(),
		Defenders:              gymDefendersRaw(gym.Defenders),
		Rsvps:                  jsonRaw(gym.Rsvps),
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
