package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"golbat/config"
	db2 "golbat/db"
	"golbat/decoder"
	"golbat/geo"

	"github.com/danielgtaylor/huma/v2"
	log "github.com/sirupsen/logrus"
)

type pokemonV2ScanInput struct {
	Body decoder.ApiPokemonScan2
}

type pokemonV2ScanOutput struct {
	Body []decoder.ApiPokemonResult
}

type pokemonV3ScanInput struct {
	Body decoder.ApiPokemonScan3
}

type pokemonV3ScanOutput struct {
	Body decoder.ApiPokemonScanResultV3
}

// registerHumaRoutes registers all Huma-backed operations on the given API.
func registerHumaRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "scan-pokemon-v2",
		Method:        http.MethodPost,
		Path:          "/api/pokemon/v2/scan",
		Summary:       "Search pokemon in a bounding box (v2, DNF filters)",
		Description:   "Returns pokemon within [min,max] matching any DNF filter clause. Clauses are OR'd; conditions within a clause are AND'd. Returns a bare array.",
		Tags:          []string{"Pokemon"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *pokemonV2ScanInput) (*pokemonV2ScanOutput, error) {
		return &pokemonV2ScanOutput{Body: decoder.GetPokemonInArea2Clean(in.Body)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "scan-pokemon-v3",
		Method:        http.MethodPost,
		Path:          "/api/pokemon/v3/scan",
		Summary:       "Search pokemon in a bounding box (v3, DNF filters)",
		Description:   "Returns pokemon within [min,max] matching any DNF filter clause. Clauses are OR'd; conditions within a clause are AND'd. Returns counts plus the matched array.",
		Tags:          []string{"Pokemon"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *pokemonV3ScanInput) (*pokemonV3ScanOutput, error) {
		return &pokemonV3ScanOutput{Body: *decoder.GetPokemonInArea3Clean(in.Body)}, nil
	})
}

type pokemonSearchInput struct{ Body decoder.ApiPokemonSearch }
type pokemonSearchOutput struct{ Body []*decoder.ApiPokemonResult }

type pokemonByIdInput struct {
	PokemonId uint64 `path:"pokemon_id" doc:"Encounter ID of the pokemon"`
}
type pokemonByIdOutput struct{ Body decoder.ApiPokemonResult }

// registerPokemonReadRoutes registers the pokemon search and by-id read operations.
func registerPokemonReadRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "search-pokemon",
		Method:        http.MethodPost,
		Path:          "/api/pokemon/search",
		Summary:       "Search pokemon by id within a bounding box",
		Description:   "Returns pokemon within [min,max] whose id is in searchIds, ordered by distance from center. Returns a bare array.",
		Tags:          []string{"Pokemon"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *pokemonSearchInput) (*pokemonSearchOutput, error) {
		res, err := decoder.SearchPokemon(in.Body)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		return &pokemonSearchOutput{Body: res}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "get-pokemon",
		Method:        http.MethodGet,
		Path:          "/api/pokemon/id/{pokemon_id}",
		Summary:       "Get a single pokemon by encounter id",
		Description:   "Returns the pokemon with the given encounter id, or 404 if not present in the cache.",
		Tags:          []string{"Pokemon"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *pokemonByIdInput) (*pokemonByIdOutput, error) {
		res := decoder.GetOnePokemon(in.PokemonId)
		if res == nil {
			return nil, huma.Error404NotFound("pokemon not found")
		}
		return &pokemonByIdOutput{Body: *res}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "available-pokemon",
		Method:        http.MethodGet,
		Path:          "/api/pokemon/available",
		Summary:       "List currently available pokemon",
		Description:   "Returns the distinct pokemon id/form combinations currently in the cache with their counts.",
		Tags:          []string{"Pokemon"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, _ *struct{}) (*pokemonAvailableOutput, error) {
		return &pokemonAvailableOutput{Body: decoder.GetAvailablePokemon()}, nil
	})
}

type pokemonAvailableOutput struct {
	Body []*decoder.ApiPokemonAvailableResult
}

type gymScanInput struct{ Body decoder.ApiFortScan }
type gymScanOutput struct{ Body decoder.ApiGymScanResult }

type pokestopScanInput struct{ Body decoder.ApiFortScan }
type pokestopScanOutput struct{ Body decoder.ApiPokestopScanResult }

type stationScanInput struct{ Body decoder.ApiFortScan }
type stationScanOutput struct{ Body decoder.ApiStationScanResult }

type fortScanInput struct{ Body decoder.ApiFortScan }
type fortScanOutput struct {
	Body decoder.ApiFortCombinedScanResult
}

// registerFortScanRoutes registers the four in-memory fort scan operations.
// These are gated by config.Config.FortInMemory and return 503 when disabled.
func registerFortScanRoutes(api huma.API) {
	gymOp := huma.Operation{
		OperationID:   "scan-gyms",
		Method:        http.MethodPost,
		Path:          "/api/gym/scan",
		Summary:       "Search gyms in a bounding box (DNF filters)",
		Description:   "Returns gyms within [min,max] matching any DNF filter clause.",
		Tags:          []string{"Fort"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}
	draftBadge(&gymOp)
	huma.Register(api, gymOp, func(ctx context.Context, in *gymScanInput) (*gymScanOutput, error) {
		if !config.Config.FortInMemory {
			return nil, huma.Error503ServiceUnavailable("fort_in_memory not enabled")
		}
		return &gymScanOutput{Body: *decoder.GymScanEndpoint(in.Body, dbDetails)}, nil
	})

	pokestopOp := huma.Operation{
		OperationID:   "scan-pokestops",
		Method:        http.MethodPost,
		Path:          "/api/pokestop/scan",
		Summary:       "Search pokestops in a bounding box (DNF filters)",
		Description:   "Returns pokestops within [min,max] matching any DNF filter clause.",
		Tags:          []string{"Fort"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}
	draftBadge(&pokestopOp)
	huma.Register(api, pokestopOp, func(ctx context.Context, in *pokestopScanInput) (*pokestopScanOutput, error) {
		if !config.Config.FortInMemory {
			return nil, huma.Error503ServiceUnavailable("fort_in_memory not enabled")
		}
		return &pokestopScanOutput{Body: *decoder.PokestopScanEndpoint(in.Body, dbDetails)}, nil
	})

	stationOp := huma.Operation{
		OperationID:   "scan-stations",
		Method:        http.MethodPost,
		Path:          "/api/station/scan",
		Summary:       "Search stations in a bounding box (DNF filters)",
		Description:   "Returns stations within [min,max] matching any DNF filter clause.",
		Tags:          []string{"Fort"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}
	draftBadge(&stationOp)
	huma.Register(api, stationOp, func(ctx context.Context, in *stationScanInput) (*stationScanOutput, error) {
		if !config.Config.FortInMemory {
			return nil, huma.Error503ServiceUnavailable("fort_in_memory not enabled")
		}
		return &stationScanOutput{Body: *decoder.StationScanEndpoint(in.Body, dbDetails)}, nil
	})

	fortOp := huma.Operation{
		OperationID:   "scan-forts",
		Method:        http.MethodPost,
		Path:          "/api/fort/scan",
		Summary:       "Search all fort types in a bounding box (DNF filters)",
		Description:   "Returns gyms, pokestops, and stations within [min,max] matching any DNF filter clause, in a single rtree traversal.",
		Tags:          []string{"Fort"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}
	draftBadge(&fortOp)
	huma.Register(api, fortOp, func(ctx context.Context, in *fortScanInput) (*fortScanOutput, error) {
		if !config.Config.FortInMemory {
			return nil, huma.Error503ServiceUnavailable("fort_in_memory not enabled")
		}
		return &fortScanOutput{Body: *decoder.FortCombinedScanEndpoint(in.Body, dbDetails)}, nil
	})
}

// maxQueryIDs caps the number of ids accepted by the by-id batch query endpoints.
const maxQueryIDs = 500

// dedupeIDs drops empty and duplicate ids while preserving order.
func dedupeIDs(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, id := range in {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

type idsQueryInput struct {
	Body struct {
		IDs []string `json:"ids" doc:"Fort IDs to fetch (max 500)"`
	}
}

type gymQueryOutput struct{ Body []decoder.ApiGymResult }
type stationQueryOutput struct{ Body []decoder.ApiStationResult }

type gymSearchInput struct{ Body decoder.ApiGymSearch }
type gymSearchOutput struct{ Body []decoder.ApiGymResult }

type gymByIdInput struct {
	GymId string `path:"gym_id" doc:"Fort ID of the gym"`
}
type gymByIdOutput struct{ Body decoder.ApiGymResult }

type pokestopByIdInput struct {
	FortId string `path:"fort_id" doc:"Fort ID of the pokestop"`
}
type pokestopByIdOutput struct{ Body decoder.ApiPokestopResult }

type tappableByIdInput struct {
	TappableId uint64 `path:"tappable_id" doc:"Encounter ID of the tappable"`
}
type tappableByIdOutput struct{ Body decoder.ApiTappableResult }

type pokestopPositionsInput struct {
	RawBody []byte
}
type pokestopPositionsOutput struct{ Body []db2.QuestLocation }

// registerTier3Routes registers the tier-3 read endpoints (by-id reads, batch
// id queries, gym search, and pokestop positions) on the given API.
func registerTier3Routes(api huma.API) {
	// POST /api/gym/query
	huma.Register(api, huma.Operation{
		OperationID:   "query-gyms",
		Method:        http.MethodPost,
		Path:          "/api/gym/query",
		Summary:       "Fetch gyms by id",
		Description:   "Returns the gyms with the given ids (max 500, deduplicated). Unknown ids are omitted.",
		Tags:          []string{"Fort"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}, func(ctx context.Context, in *idsQueryInput) (*gymQueryOutput, error) {
		ids := dedupeIDs(in.Body.IDs)
		if len(ids) > maxQueryIDs {
			return nil, huma.Error413RequestEntityTooLarge("too many ids")
		}
		if len(ids) == 0 {
			return &gymQueryOutput{Body: []decoder.ApiGymResult{}}, nil
		}

		tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		out := make([]decoder.ApiGymResult, 0, len(ids))
		for _, id := range ids {
			g, unlock, err := decoder.GetGymRecordReadOnly(tctx, dbDetails, id, "API.GetGyms")
			if err != nil {
				if unlock != nil {
					unlock()
				}
				return nil, huma.Error500InternalServerError("error retrieving gym")
			}
			if g != nil {
				out = append(out, decoder.BuildGymResult(g))
			}
			if unlock != nil {
				unlock()
			}
			if tctx.Err() != nil {
				return nil, huma.Error500InternalServerError("timed out")
			}
		}
		return &gymQueryOutput{Body: out}, nil
	})

	// POST /api/station/query
	huma.Register(api, huma.Operation{
		OperationID:   "query-stations",
		Method:        http.MethodPost,
		Path:          "/api/station/query",
		Summary:       "Fetch stations by id",
		Description:   "Returns the stations with the given ids (max 500, deduplicated). Unknown ids are omitted.",
		Tags:          []string{"Fort"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}, func(ctx context.Context, in *idsQueryInput) (*stationQueryOutput, error) {
		ids := dedupeIDs(in.Body.IDs)
		if len(ids) > maxQueryIDs {
			return nil, huma.Error413RequestEntityTooLarge("too many ids")
		}
		if len(ids) == 0 {
			return &stationQueryOutput{Body: []decoder.ApiStationResult{}}, nil
		}

		tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		out := make([]decoder.ApiStationResult, 0, len(ids))
		for _, id := range ids {
			s, unlock, err := decoder.GetStationRecordReadOnly(tctx, dbDetails, id, "API.GetStations")
			if err != nil {
				if unlock != nil {
					unlock()
				}
				return nil, huma.Error500InternalServerError("error retrieving station")
			}
			if s != nil {
				out = append(out, decoder.BuildStationResult(s))
			}
			if unlock != nil {
				unlock()
			}
			if tctx.Err() != nil {
				return nil, huma.Error500InternalServerError("timed out")
			}
		}
		return &stationQueryOutput{Body: out}, nil
	})

	// POST /api/gym/search
	huma.Register(api, huma.Operation{
		OperationID:   "search-gyms",
		Method:        http.MethodPost,
		Path:          "/api/gym/search",
		Summary:       "Search gyms by name, description, or location",
		Description:   "Returns gyms matching the AND'd filter conditions, up to limit (default 500, max 10000).",
		Tags:          []string{"Fort"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}, func(ctx context.Context, in *gymSearchInput) (*gymSearchOutput, error) {
		search := in.Body

		if len(search.Filters) == 0 {
			return nil, huma.Error400BadRequest("filters array is required")
		}

		// Validate filters (and clamp distance like the legacy handler).
		for i := range search.Filters {
			filter := &search.Filters[i]
			if filter.LocationDistance != nil {
				locDist := *filter.LocationDistance
				if locDist.Distance <= 0 {
					return nil, huma.Error400BadRequest("distance must be > 0")
				}
				if locDist.Distance > 500_000 {
					locDist.Distance = 500_000
					filter.LocationDistance = &locDist
				}
				lat, lon := locDist.Location.Latitude, locDist.Location.Longitude
				if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
					return nil, huma.Error400BadRequest("lat must be [-90,90], lon must be [-180,180]")
				}
			}
			if filter.Bbox != nil {
				bbox := *filter.Bbox
				if bbox.MinLat < -90 || bbox.MinLat > 90 || bbox.MaxLat < -90 || bbox.MaxLat > 90 ||
					bbox.MinLon < -180 || bbox.MinLon > 180 || bbox.MaxLon < -180 || bbox.MaxLon > 180 {
					return nil, huma.Error400BadRequest("bbox coordinates out of range: lat must be [-90,90], lon must be [-180,180]")
				}
				if bbox.MinLat > bbox.MaxLat {
					return nil, huma.Error400BadRequest("bbox invalid: minLat must be <= maxLat")
				}
				if bbox.MinLon > bbox.MaxLon {
					return nil, huma.Error400BadRequest("bbox invalid: minLon must be <= maxLon")
				}
			}
		}

		// Limit defaulting: default 500, cap 10000. The legacy handler used a
		// *int (nil/<=0 => default); here a missing/zero/negative limit defaults.
		if search.Limit <= 0 {
			search.Limit = 500
		}
		if search.Limit > 10000 {
			search.Limit = 10000
		}

		tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		ids, err := decoder.SearchGymsAPI(tctx, dbDetails, search)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(tctx.Err(), context.DeadlineExceeded) {
				return nil, huma.Error504GatewayTimeout("timed out")
			}
			return nil, huma.Error500InternalServerError("search failed")
		}

		out := make([]decoder.ApiGymResult, 0, len(ids))
		for _, id := range ids {
			if id == "" {
				continue
			}
			g, unlock, err := decoder.GetGymRecordReadOnly(tctx, dbDetails, id, "API.SearchGyms")
			if err != nil {
				if unlock != nil {
					unlock()
				}
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(tctx.Err(), context.DeadlineExceeded) {
					return nil, huma.Error504GatewayTimeout("timed out")
				}
				return nil, huma.Error500InternalServerError("error retrieving gym")
			}
			if g != nil {
				out = append(out, decoder.BuildGymResult(g))
			}
			if unlock != nil {
				unlock()
			}
			if tctx.Err() != nil {
				return nil, huma.Error500InternalServerError("timed out")
			}
		}
		return &gymSearchOutput{Body: out}, nil
	})

	// GET /api/gym/id/{gym_id}
	huma.Register(api, huma.Operation{
		OperationID:   "get-gym",
		Method:        http.MethodGet,
		Path:          "/api/gym/id/{gym_id}",
		Summary:       "Get a single gym by id",
		Description:   "Returns the gym with the given fort id, or 404 if not present.",
		Tags:          []string{"Fort"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *gymByIdInput) (*gymByIdOutput, error) {
		tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		gym, unlock, err := decoder.GetGymRecordReadOnly(tctx, dbDetails, in.GymId, "API.GetGym")
		if unlock != nil {
			defer unlock()
		}
		cancel()
		if err != nil {
			return nil, huma.Error500InternalServerError("error retrieving gym")
		}
		if gym == nil {
			return nil, huma.Error404NotFound("gym not found")
		}
		return &gymByIdOutput{Body: decoder.BuildGymResult(gym)}, nil
	})

	// GET /api/pokestop/id/{fort_id}
	huma.Register(api, huma.Operation{
		OperationID:   "get-pokestop",
		Method:        http.MethodGet,
		Path:          "/api/pokestop/id/{fort_id}",
		Summary:       "Get a single pokestop by id",
		Description:   "Returns the pokestop with the given fort id, or 404 if not present in the cache.",
		Tags:          []string{"Fort"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *pokestopByIdInput) (*pokestopByIdOutput, error) {
		pokestop, unlock, err := decoder.PeekPokestopRecord(in.FortId, "API.GetPokestop")
		if unlock != nil {
			defer unlock()
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("error retrieving pokestop")
		}
		if pokestop == nil {
			return nil, huma.Error404NotFound("pokestop not found")
		}
		return &pokestopByIdOutput{Body: decoder.BuildPokestopResult(pokestop)}, nil
	})

	// GET /api/tappable/id/{tappable_id}
	huma.Register(api, huma.Operation{
		OperationID:   "get-tappable",
		Method:        http.MethodGet,
		Path:          "/api/tappable/id/{tappable_id}",
		Summary:       "Get a single tappable by encounter id",
		Description:   "Returns the tappable with the given encounter id, or 404 if not present in the cache.",
		Tags:          []string{"Tappable"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *tappableByIdInput) (*tappableByIdOutput, error) {
		tappable, unlock, err := decoder.PeekTappableRecord(in.TappableId, "API.GetTappable")
		if unlock != nil {
			defer unlock()
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("error retrieving tappable")
		}
		if tappable == nil {
			return nil, huma.Error404NotFound("tappable not found")
		}
		return &tappableByIdOutput{Body: decoder.BuildTappableResult(tappable)}, nil
	})

	// POST /api/pokestop-positions
	huma.Register(api, huma.Operation{
		OperationID:   "get-pokestop-positions",
		Method:        http.MethodPost,
		Path:          "/api/pokestop-positions",
		Summary:       "List pokestop positions within a geofence",
		Description:   "Returns the positions of pokestops within the supplied geofence (geometry, feature, or Golbat fence).",
		Tags:          []string{"Fort"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *pokestopPositionsInput) (*pokestopPositionsOutput, error) {
		fence, err := geo.NormaliseFenceFromBytes(in.RawBody)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		response, err := decoder.GetPokestopPositions(dbDetails, fence)
		if err != nil {
			return nil, huma.Error500InternalServerError("error retrieving pokestop positions")
		}
		return &pokestopPositionsOutput{Body: response}, nil
	})
}

type questStatusInput struct {
	RawBody []byte
}
type questStatusOutput struct{ Body db2.QuestStatus }

type clearQuestsInput struct {
	RawBody []byte
}
type clearQuestsOutput struct{ Body StatusResponse }

// registerTier4Routes registers the geofence-body quest endpoints (quest-status
// and clear-quests) on the given API.
func registerTier4Routes(api huma.API) {
	// POST /api/quest-status
	huma.Register(api, huma.Operation{
		OperationID:   "get-quest-status",
		Method:        http.MethodPost,
		Path:          "/api/quest-status",
		Summary:       "Quest status within a geofence",
		Description:   "Returns quest completion status for pokestops within the supplied geofence (geometry, feature, or Golbat fence).",
		Tags:          []string{"Quest"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}, func(ctx context.Context, in *questStatusInput) (*questStatusOutput, error) {
		fence, err := geo.NormaliseFenceFromBytes(in.RawBody)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		status := decoder.GetQuestStatusWithGeofence(dbDetails, fence)
		return &questStatusOutput{Body: status}, nil
	})

	// POST /api/clear-quests
	huma.Register(api, huma.Operation{
		OperationID:   "clear-quests",
		Method:        http.MethodPost,
		Path:          "/api/clear-quests",
		Summary:       "Clear quests within a geofence",
		Description:   "Deletes quests for pokestops within the supplied geofence (geometry, feature, or Golbat fence).",
		Tags:          []string{"Quest"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *clearQuestsInput) (*clearQuestsOutput, error) {
		fence, err := geo.NormaliseFenceFromBytes(in.RawBody)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}

		tctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		log.Debugf("Clear quests %+v", fence)
		startTime := time.Now()
		decoder.ClearQuestsWithinGeofence(tctx, dbDetails, fence)
		log.Infof("Clear quest took %s", time.Since(startTime))

		return &clearQuestsOutput{Body: StatusResponse{Status: "ok"}}, nil
	})

	// GET /api/devices/all
	huma.Register(api, huma.Operation{
		OperationID:   "get-devices",
		Method:        http.MethodGet,
		Path:          "/api/devices/all",
		Summary:       "List all known devices",
		Description:   "Returns the last-known location for every device that has submitted data.",
		Tags:          []string{"Devices"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}, func(ctx context.Context, in *struct{}) (*devicesOutput, error) {
		out := &devicesOutput{}
		out.Body.Devices = GetAllDevices()
		return out, nil
	})

	// GET /api/fort-tracker/cell/{cell_id}
	huma.Register(api, huma.Operation{
		OperationID:   "get-fort-tracker-cell",
		Method:        http.MethodGet,
		Path:          "/api/fort-tracker/cell/{cell_id}",
		Summary:       "Forts within an S2 cell",
		Description:   "Returns the pokestops and gyms the fort tracker has seen within the given S2 cell.",
		Tags:          []string{"FortTracker"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}, func(ctx context.Context, in *fortTrackerCellInput) (*fortTrackerCellOutput, error) {
		ft := decoder.GetFortTracker()
		if ft == nil {
			return nil, huma.Error503ServiceUnavailable("FortTracker not initialized")
		}
		info := ft.GetCellInfo(in.CellId)
		if info == nil {
			return nil, huma.Error404NotFound("Cell not found")
		}
		return &fortTrackerCellOutput{Body: *info}, nil
	})

	// GET /api/fort-tracker/forts/{fort_id}
	huma.Register(api, huma.Operation{
		OperationID:   "get-fort-tracker-fort",
		Method:        http.MethodGet,
		Path:          "/api/fort-tracker/forts/{fort_id}",
		Summary:       "Fort tracker info for a fort",
		Description:   "Returns the S2 cell and last-seen timestamp the fort tracker holds for the given fort id.",
		Tags:          []string{"FortTracker"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}, func(ctx context.Context, in *fortTrackerFortInput) (*fortTrackerFortOutput, error) {
		ft := decoder.GetFortTracker()
		if ft == nil {
			return nil, huma.Error503ServiceUnavailable("FortTracker not initialized")
		}
		info := ft.GetFortInfo(in.FortId)
		if info == nil {
			return nil, huma.Error404NotFound("Fort not found")
		}
		return &fortTrackerFortOutput{Body: *info}, nil
	})

	// GET+POST /api/reload-geojson
	reloadGeojsonHandler := func(ctx context.Context, in *struct{}) (*reloadGeojsonOutput, error) {
		decoder.ReloadGeofenceAndClearStats()
		return &reloadGeojsonOutput{Body: StatusResponse{Status: "ok"}}, nil
	}
	huma.Register(api, huma.Operation{
		OperationID:   "reload-geojson-get",
		Method:        http.MethodGet,
		Path:          "/api/reload-geojson",
		Summary:       "Reload geofences and clear stats",
		Description:   "Reloads geofences from the configured source and clears area statistics.",
		Tags:          []string{"Admin"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusAccepted,
	}, reloadGeojsonHandler)
	huma.Register(api, huma.Operation{
		OperationID:   "reload-geojson-post",
		Method:        http.MethodPost,
		Path:          "/api/reload-geojson",
		Summary:       "Reload geofences and clear stats",
		Description:   "Reloads geofences from the configured source and clears area statistics.",
		Tags:          []string{"Admin"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusAccepted,
	}, reloadGeojsonHandler)

	// GET+POST /api/skip-preserve-pokemon
	skipPreserveHandler := func(ctx context.Context, in *struct{}) (*skipPreservePokemonOutput, error) {
		decoder.SetSkipPreservePokemon(true)
		log.Info("Skip preserve pokemon flag set - pokemon will not be preserved on shutdown")
		out := &skipPreservePokemonOutput{}
		out.Body.Status = "ok"
		out.Body.Message = "Pokemon preservation will be skipped on shutdown"
		return out, nil
	}
	huma.Register(api, huma.Operation{
		OperationID:   "skip-preserve-pokemon-get",
		Method:        http.MethodGet,
		Path:          "/api/skip-preserve-pokemon",
		Summary:       "Skip pokemon preservation on shutdown",
		Description:   "Sets a flag so pokemon are not preserved to the database on shutdown.",
		Tags:          []string{"Admin"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}, skipPreserveHandler)
	huma.Register(api, huma.Operation{
		OperationID:   "skip-preserve-pokemon-post",
		Method:        http.MethodPost,
		Path:          "/api/skip-preserve-pokemon",
		Summary:       "Skip pokemon preservation on shutdown",
		Description:   "Sets a flag so pokemon are not preserved to the database on shutdown.",
		Tags:          []string{"Admin"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}, skipPreserveHandler)
}

type devicesOutput struct {
	Body struct {
		Devices map[string]ApiDeviceLocation `json:"devices"`
	}
}

type fortTrackerCellInput struct {
	CellId uint64 `path:"cell_id" doc:"S2 cell id"`
}
type fortTrackerCellOutput struct{ Body decoder.CellFortInfo }

type fortTrackerFortInput struct {
	FortId string `path:"fort_id" doc:"Fort id"`
}
type fortTrackerFortOutput struct{ Body decoder.FortTrackerInfo }

type reloadGeojsonOutput struct{ Body StatusResponse }

type skipPreservePokemonOutput struct {
	Body struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
}
