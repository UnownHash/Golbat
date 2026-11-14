package main

import (
	"context"
	"encoding/json"
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
		Description:   "Returns unexpired pokemon within [min,max] that match the filters, as a bare array, up to limit (clamped by tuning.max_pokemon_results). Filters are not a flat OR: clauses are grouped by the pokemon/form keys they list, and each pokemon is matched only against the most specific group that exists for it (exact id+form, else id with any form, else the clauses with no pokemon list). Within that group a clause matches when all its conditions hold. See the filters field and api.md \"Filter semantics\".",
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
		Description:   "Returns unexpired pokemon within [min,max] that match the filters, up to limit (clamped by tuning.max_pokemon_results), with the scan counters and limit_reached. Filters are not a flat OR: clauses are grouped by the pokemon/form keys they list, and each pokemon is matched only against the most specific group that exists for it (exact id+form, else id with any form, else the clauses with no pokemon list). Within that group a clause matches when all its conditions hold. Gender is a list of allowed values (v2 uses a range). Supports updated_after. See the filters field and api.md \"Filter semantics\".",
		Tags:          []string{"Pokemon"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *pokemonV3ScanInput) (*pokemonV3ScanOutput, error) {
		return &pokemonV3ScanOutput{Body: *decoder.GetPokemonInArea3Clean(in.Body)}, nil
	})
}

type statusOutput struct{ Body *decoder.ApiStatusResult }

// registerStatusRoutes registers the server status/capability report. Unlike
// the fort routes it is NOT gated on fort_in_memory — it exists to report it.
func registerStatusRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "get-api-status",
		Method:        http.MethodGet,
		Path:          "/api/status",
		Summary:       "Server status: enabled features and scan limits",
		Description:   "Reports which optional Golbat features are enabled (e.g. fort_in_memory), the server's effective scan result caps, and which optional DNF filter and /available fields this build supports (filters), so map consumers can detect capabilities and clamp request limits without probing gated endpoints. Not gated on fort_in_memory. The key set is a stable contract for map consumers: keys are only ever added.",
		Tags:          []string{"Status"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}, func(ctx context.Context, _ *struct{}) (*statusOutput, error) {
		return &statusOutput{Body: decoder.GetApiStatus()}, nil
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
		Description:   "Walks the pokemon index outward from center, nearest first, and returns the pokemon whose id is in searchIds as a bare array, up to limit (clamped by tuning.max_pokemon_results). min and max do not clip the results to a box: they set how far from center the walk goes, and must be no more than tuning.max_pokemon_distance km apart. 400 when searchIds is missing or the box is too large.",
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
		Description:   "Returns the pokemon with the given encounter id from the in-memory cache (no database fallback), or 404 if it is not cached.",
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
		Description:   "Returns each distinct pokemon id/form currently in the in-memory pokemon index, with how many cached pokemon have it. Unordered; null when nothing is cached.",
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

type fortScanInput struct{ Body decoder.ApiFortCombinedScan }
type fortScanOutput struct {
	Body decoder.ApiFortCombinedScanResult
}

type pokestopAvailableOutput struct {
	Body *decoder.ApiAvailablePokestops
}

type gymAvailableOutput struct {
	Body *decoder.ApiAvailableGyms
}

type fortAvailableOutput struct {
	Body *decoder.ApiAvailableForts
}
type stationAvailableOutput struct {
	Body *decoder.ApiAvailableStations
}

// registerFortScanRoutes registers the four in-memory fort scan operations
// and the four fort availability operations (pokestop, gym, station and the
// combined fort). All of them are gated by config.Config.FortInMemory and
// return 503 when it is disabled.
func registerFortScanRoutes(api huma.API) {
	gymOp := huma.Operation{
		OperationID:   "scan-gyms",
		Method:        http.MethodPost,
		Path:          "/api/gym/scan",
		Summary:       "Search gyms in a bounding box (DNF filters)",
		Description:   "Returns gyms within [min,max] from the in-memory fort index. Matching is a plain OR over the filter clauses (conditions within a clause are AND'd); omitted, null or empty filters match every gym. Up to limit results (0 = server default; clamped by tuning.max_fort_results). Supports updated_after. Requires fort_in_memory (503 otherwise).",
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
		Description:   "Returns pokestops within [min,max] from the in-memory fort index. Matching is a plain OR over the filter clauses (conditions within a clause are AND'd); omitted, null or empty filters match every pokestop. Up to limit results (0 = server default; clamped by tuning.max_fort_results). Supports updated_after. Requires fort_in_memory (503 otherwise). Set with_incidents to attach active incidents.",
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
		Description:   "Returns stations within [min,max] from the in-memory fort index. Matching is a plain OR over the filter clauses (conditions within a clause are AND'd); omitted, null or empty filters match every station. Up to limit results (0 = server default; clamped by tuning.max_fort_results). Supports updated_after. Requires fort_in_memory (503 otherwise).",
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
		Description:   "Returns the requested fort types within [min,max] in a single walk of the in-memory fort index. Each present group (gyms/pokestops/stations) opts its type in with its own clauses (a plain OR; omitted, null or empty matches every fort of the type) and its own limit; omitted groups are excluded, and omitting all three returns every type. A type stops accepting matches at its limit while the walk keeps filling the others; the top-level limit caps the total. Per-type counters are in gyms_stats/pokestops_stats/stations_stats. Supports updated_after. Requires fort_in_memory (503 otherwise).",
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

	availableOp := huma.Operation{
		OperationID:   "available-pokestops",
		Method:        http.MethodGet,
		Path:          "/api/pokestop/available",
		Summary:       "List currently available pokestop rewards/invasions/lures/showcases",
		Description:   "Returns the distinct options currently available on resident pokestops — quest rewards (with title/target), invasions, lures and showcases — for building filter options. Whole-instance; no fort or DB scan. Lures, invasions and showcases are read from maintained max-expiry indexes: an option is listed until the latest expiry seen for it passes, so a replaced option can over-report until then. Quests come from a maintained per-fort count aggregate, and only quest entries carry `count` (the number of resident pokestops offering that exact reward + temp evolution + title + target in that slot). Requires fort_in_memory (503 otherwise).",
		Tags:          []string{"Pokestop"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}
	draftBadge(&availableOp)
	huma.Register(api, availableOp, func(ctx context.Context, _ *struct{}) (*pokestopAvailableOutput, error) {
		if !config.Config.FortInMemory {
			return nil, huma.Error503ServiceUnavailable("fort_in_memory not enabled")
		}
		return &pokestopAvailableOutput{Body: decoder.GetAvailablePokestops(time.Now().Unix())}, nil
	})

	gymAvailableOp := huma.Operation{
		OperationID:   "available-gyms",
		Method:        http.MethodGet,
		Path:          "/api/gym/available",
		Summary:       "List currently available gym raid options",
		Description:   "Distinct raid level/boss/egg options on resident gyms, read from a maintained max-expiry index (no fort or DB scan): an option is listed until the latest raid end seen for it passes. Team/slot filter keys are generated statically by consumers, so they are not returned here. Whole-instance; requires fort_in_memory (503 otherwise).",
		Tags:          []string{"Fort"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}
	draftBadge(&gymAvailableOp)
	huma.Register(api, gymAvailableOp, func(ctx context.Context, _ *struct{}) (*gymAvailableOutput, error) {
		if !config.Config.FortInMemory {
			return nil, huma.Error503ServiceUnavailable("fort_in_memory not enabled")
		}
		return &gymAvailableOutput{Body: decoder.GetAvailableGyms(time.Now().Unix())}, nil
	})

	fortAvailableOp := huma.Operation{
		OperationID:   "available-forts",
		Method:        http.MethodGet,
		Path:          "/api/fort/available",
		Summary:       "List available options for all fort types in one pass",
		Description:   "Pokestop, gym and station availability (same shapes and sources as the per-type /available endpoints: maintained max-expiry indexes, plus the quest count aggregate) in one response, with no fort or DB scan — use this instead of three per-type calls when refreshing everything. Whole-instance; requires fort_in_memory (503 otherwise).",
		Tags:          []string{"Fort"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}
	draftBadge(&fortAvailableOp)
	huma.Register(api, fortAvailableOp, func(ctx context.Context, _ *struct{}) (*fortAvailableOutput, error) {
		if !config.Config.FortInMemory {
			return nil, huma.Error503ServiceUnavailable("fort_in_memory not enabled")
		}
		return &fortAvailableOutput{Body: decoder.GetAvailableForts(time.Now().Unix())}, nil
	})

	stationAvailableOp := huma.Operation{
		OperationID:   "available-stations",
		Method:        http.MethodGet,
		Path:          "/api/station/available",
		Summary:       "List currently available station battle options",
		Description:   "Distinct (battle level, pokemon, form) options on resident stations, read from a maintained max-expiry index (no fort or DB scan): an option is listed until the latest battle end seen for it passes. Whole-instance; requires fort_in_memory (503 otherwise).",
		Tags:          []string{"Fort"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}
	draftBadge(&stationAvailableOp)
	huma.Register(api, stationAvailableOp, func(ctx context.Context, _ *struct{}) (*stationAvailableOutput, error) {
		if !config.Config.FortInMemory {
			return nil, huma.Error503ServiceUnavailable("fort_in_memory not enabled")
		}
		return &stationAvailableOutput{Body: decoder.GetAvailableStations(time.Now().Unix())}, nil
	})
}

// maxQueryIDs caps the number of ids accepted by the by-id batch query endpoints.
const maxQueryIDs = 500

// dedupeIDs parses each id, dropping unparseable (including empty) and
// duplicate ids while preserving order. Callers MUST cap the raw input length against
// maxQueryIDs before calling this — parsing can only shrink the list
// (drop malformed/duplicate entries), so checking the cap on its output
// would let a request full of malformed ids dodge the limit entirely.
// Parse failures are aggregated into a single log line for the whole call
// rather than one per id: this runs once per request, not continuously,
// so a per-event aggregator (util.DropReporter) would be overkill, but an
// attacker-sized batch of garbage ids must not still cost one log line each.
func dedupeIDs(in []string) []decoder.FortId {
	seen := make(map[decoder.FortId]struct{}, len(in))
	out := make([]decoder.FortId, 0, len(in))
	dropped := 0
	for _, raw := range in {
		id, ok := decoder.ParseFortId(raw)
		if !ok {
			dropped++
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if dropped > 0 {
		log.Errorf("dedupeIDs: dropped %d unparseable fort id(s) out of %d", dropped, len(in))
	}
	return out
}

type idsQueryInput struct {
	Body struct {
		IDs []string `json:"ids" required:"false" doc:"Fort IDs to fetch (max 500). Omitted or null returns an empty result."`
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

type stationByIdInput struct {
	StationId string `path:"station_id" doc:"ID of the station"`
}
type stationByIdOutput struct{ Body decoder.ApiStationResult }

type pokestopByIdInput struct {
	FortId string `path:"fort_id" doc:"Fort ID of the pokestop"`
}
type pokestopByIdOutput struct{ Body decoder.ApiPokestopResult }

type tappableByIdInput struct {
	TappableId uint64 `path:"tappable_id" doc:"Encounter ID of the tappable"`
}
type tappableByIdOutput struct{ Body decoder.ApiTappableResult }

type tappableScanInput struct{ Body decoder.ApiTappableScan }
type tappableScanOutput struct{ Body decoder.ApiTappableScanResult }
type tappableAvailableOutput struct{ Body *decoder.ApiTappableAvailable }

type pokestopPositionsInput struct {
	// Body is the geofence: a GeoJSON polygon geometry, a GeoJSON feature with
	// one, or a Golbat fence object. Captured as raw JSON and parsed by
	// NormaliseFenceFromBytes, which rejects non-area geometries.
	Body json.RawMessage
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
		Description:   "Returns the gyms with the given ids (max 500 ids, else 413; duplicates and malformed ids are dropped). Reads through to the database on a cache miss; unknown ids are omitted. Does not require fort_in_memory.",
		Tags:          []string{"Fort"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}, func(ctx context.Context, in *idsQueryInput) (*gymQueryOutput, error) {
		// Cap check runs on the raw request size, before parsing/dedup — see
		// dedupeIDs's doc comment for why the order matters.
		if len(in.Body.IDs) > maxQueryIDs {
			return nil, huma.Error413RequestEntityTooLarge("too many ids")
		}
		ids := dedupeIDs(in.Body.IDs)
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
		Description:   "Returns the stations with the given ids (max 500 ids, else 413; duplicates and malformed ids are dropped). Reads through to the database on a cache miss; unknown ids are omitted. Does not require fort_in_memory.",
		Tags:          []string{"Fort"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}, func(ctx context.Context, in *idsQueryInput) (*stationQueryOutput, error) {
		// Cap check runs on the raw request size, before parsing/dedup — see
		// dedupeIDs's doc comment for why the order matters.
		if len(in.Body.IDs) > maxQueryIDs {
			return nil, huma.Error413RequestEntityTooLarge("too many ids")
		}
		ids := dedupeIDs(in.Body.IDs)
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
		Description:   "Searches the database for enabled gyms matching every condition of every filter (all AND'd), ordered by id, up to limit (default 500, max 10000). At least one filter is required (400). location_distance needs distance > 0 and is capped at 500000 m; out-of-range coordinates are 400. 504 if the 5 s query budget runs out.",
		Tags:          []string{"Fort"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}, func(ctx context.Context, in *gymSearchInput) (*gymSearchOutput, error) {
		search := in.Body

		if len(search.Filters) == 0 {
			return nil, huma.Error400BadRequest("filters array is required")
		}

		// Validate filters and clamp distance to 500km. The clamp writes through
		// the slice index (filter := &search.Filters[i]); clamping a range-loop
		// copy would silently leave the request unclamped.
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

		// Limit defaulting: a missing, zero or negative limit means 500; the
		// cap is 10000.
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
		Description:   "Returns the gym with the given fort id, reading through to the database on a cache miss, or 404 if it does not exist (or the id is malformed). Does not require fort_in_memory.",
		Tags:          []string{"Fort"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *gymByIdInput) (*gymByIdOutput, error) {
		fortId, ok := decoder.ParseFortId(in.GymId)
		if !ok {
			log.Errorf("API.GetGym: unparseable fort id %q", in.GymId)
			return nil, huma.Error404NotFound("gym not found")
		}
		tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		gym, unlock, err := decoder.GetGymRecordReadOnly(tctx, dbDetails, fortId, "API.GetGym")
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

	// GET /api/station/id/{station_id}
	huma.Register(api, huma.Operation{
		OperationID:   "get-station",
		Method:        http.MethodGet,
		Path:          "/api/station/id/{station_id}",
		Summary:       "Get a single station by id",
		Description:   "Returns the station with the given id, reading through to the database on a cache miss, or 404 if it does not exist (or the id is malformed). Does not require fort_in_memory.",
		Tags:          []string{"Fort"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *stationByIdInput) (*stationByIdOutput, error) {
		fortId, ok := decoder.ParseFortId(in.StationId)
		if !ok {
			log.Errorf("API.GetStation: unparseable station id %q", in.StationId)
			return nil, huma.Error404NotFound("station not found")
		}
		tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		station, unlock, err := decoder.GetStationRecordReadOnly(tctx, dbDetails, fortId, "API.GetStation")
		if unlock != nil {
			defer unlock()
		}
		cancel()
		if err != nil {
			return nil, huma.Error500InternalServerError("error retrieving station")
		}
		if station == nil {
			return nil, huma.Error404NotFound("station not found")
		}
		return &stationByIdOutput{Body: decoder.BuildStationResult(station)}, nil
	})

	// GET /api/pokestop/id/{fort_id}
	huma.Register(api, huma.Operation{
		OperationID:   "get-pokestop",
		Method:        http.MethodGet,
		Path:          "/api/pokestop/id/{fort_id}",
		Summary:       "Get a single pokestop by id",
		Description:   "Returns the pokestop with the given fort id from the in-memory cache (no database fallback), or 404 if it is not cached. Active incidents are attached as invasions when fort_in_memory is enabled (they are resolved through the in-memory fort index).",
		Tags:          []string{"Fort"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *pokestopByIdInput) (*pokestopByIdOutput, error) {
		fortId, ok := decoder.ParseFortId(in.FortId)
		if !ok {
			log.Errorf("API.GetPokestop: unparseable fort id %q", in.FortId)
			return nil, huma.Error404NotFound("pokestop not found")
		}
		pokestop, unlock, err := decoder.PeekPokestopRecord(fortId, "API.GetPokestop")
		if err != nil {
			if unlock != nil {
				unlock()
			}
			return nil, huma.Error500InternalServerError("error retrieving pokestop")
		}
		if pokestop == nil {
			if unlock != nil {
				unlock()
			}
			return nil, huma.Error404NotFound("pokestop not found")
		}
		body := decoder.BuildPokestopResult(pokestop)
		if unlock != nil {
			unlock() // release before locking incidents
		}
		body.Invasions = decoder.CollectPokestopIncidents(ctx, dbDetails, fortId, time.Now().Unix())
		return &pokestopByIdOutput{Body: body}, nil
	})

	// GET /api/tappable/id/{tappable_id}
	huma.Register(api, huma.Operation{
		OperationID:   "get-tappable",
		Method:        http.MethodGet,
		Path:          "/api/tappable/id/{tappable_id}",
		Summary:       "Get a single tappable by encounter id",
		Description:   "Returns the tappable with the given encounter id from the in-memory cache (no database fallback), or 404 if it is not cached. A tappable stays cached until 60 s past its expire_timestamp (60 min without one).",
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

	// POST /api/tappable/scan
	tappableScanOp := huma.Operation{
		OperationID:   "scan-tappables",
		Method:        http.MethodPost,
		Path:          "/api/tappable/scan",
		Summary:       "Search tappables in a bounding box (DNF filters)",
		Description:   "Returns unexpired tappables within [min,max] from the in-memory tappable index. Matching is a plain OR over the filter clauses (fields within a clause are AND'd); omitted, null or empty filters match every tappable. Up to limit results (0 = server default; clamped by tuning.max_tappable_results). Supports updated_after. Not gated on fort_in_memory. 400 on an unknown reward_type or a negative id.",
		Tags:          []string{"Tappable"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}
	draftBadge(&tappableScanOp)
	huma.Register(api, tappableScanOp, func(ctx context.Context, in *tappableScanInput) (*tappableScanOutput, error) {
		res, err := decoder.TappableScanEndpoint(in.Body)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		return &tappableScanOutput{Body: *res}, nil
	})

	// GET /api/tappable/available
	tappableAvailableOp := huma.Operation{
		OperationID:   "available-tappables",
		Method:        http.MethodGet,
		Path:          "/api/tappable/available",
		Summary:       "List currently available tappable reward options and encounter species",
		Description:   "Distinct live (reward_type, item_id, reward_pokemon_id) options and encounter species, read from a maintained max-expiry index (no tappable or DB scan). An option is listed until the latest expiry seen for it passes (60 min for tappables without an expiry), so a replaced option can over-report until then. Not gated on fort_in_memory.",
		Tags:          []string{"Tappable"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}
	draftBadge(&tappableAvailableOp)
	huma.Register(api, tappableAvailableOp, func(ctx context.Context, _ *struct{}) (*tappableAvailableOutput, error) {
		return &tappableAvailableOutput{Body: decoder.GetAvailableTappables(time.Now().Unix())}, nil
	})

	// POST /api/pokestop-positions
	huma.Register(api, huma.Operation{
		OperationID:   "get-pokestop-positions",
		Method:        http.MethodPost,
		Path:          "/api/pokestop-positions",
		Summary:       "List pokestop positions within a geofence",
		Description:   "Returns the id and position of every enabled pokestop in the database within the supplied geofence (a GeoJSON Polygon/MultiPolygon/GeometryCollection of polygons, a Feature with one, or a Golbat {\"fence\":[{lat,lon},...]} object). 400 for any other body.",
		Tags:          []string{"Fort"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *pokestopPositionsInput) (*pokestopPositionsOutput, error) {
		fence, err := geo.NormaliseFenceFromBytes(in.Body)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		response, err := decoder.GetPokestopPositions(ctx, dbDetails, fence)
		if err != nil {
			return nil, huma.Error500InternalServerError("error retrieving pokestop positions")
		}
		return &pokestopPositionsOutput{Body: response}, nil
	})
}

type questStatusInput struct {
	// Body is the geofence: a GeoJSON polygon geometry, a GeoJSON feature with
	// one, or a Golbat fence object. Captured as raw JSON and parsed by
	// NormaliseFenceFromBytes, which rejects non-area geometries.
	Body json.RawMessage
}
type questStatusOutput struct{ Body db2.QuestStatus }

type clearQuestsInput struct {
	// Body is the geofence: a GeoJSON polygon geometry, a GeoJSON feature with
	// one, or a Golbat fence object. Captured as raw JSON and parsed by
	// NormaliseFenceFromBytes, which rejects non-area geometries.
	Body json.RawMessage
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
		Description:   "Counts the non-deleted, enabled pokestops in the database within the supplied geofence (total) and how many of them hold an AR quest (ar_quests) and a non-AR quest (no_ar_quests). The geofence is a GeoJSON Polygon/MultiPolygon/GeometryCollection of polygons, a Feature with one, or a Golbat fence object; 400 for any other body. A database error returns all zeros.",
		Tags:          []string{"Quest"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}, func(ctx context.Context, in *questStatusInput) (*questStatusOutput, error) {
		fence, err := geo.NormaliseFenceFromBytes(in.Body)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		status := decoder.GetQuestStatusWithGeofence(ctx, dbDetails, fence)
		return &questStatusOutput{Body: status}, nil
	})

	// POST /api/clear-quests
	huma.Register(api, huma.Operation{
		OperationID:   "clear-quests",
		Method:        http.MethodPost,
		Path:          "/api/clear-quests",
		Summary:       "Clear quests within a geofence",
		Description:   "Clears the AR and non-AR quests of every pokestop within the supplied geofence (a GeoJSON Polygon/MultiPolygon/GeometryCollection of polygons, a Feature with one, or a Golbat fence object; 400 for any other body). 500 if clearing fails or exceeds its 10 s budget.",
		Tags:          []string{"Quest"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *clearQuestsInput) (*clearQuestsOutput, error) {
		fence, err := geo.NormaliseFenceFromBytes(in.Body)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}

		tctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		log.Debugf("Clear quests %+v", fence)
		startTime := time.Now()
		err = decoder.ClearQuestsWithinGeofence(tctx, dbDetails, fence)
		log.Infof("Clear quest took %s", time.Since(startTime))
		if err != nil {
			return nil, huma.Error500InternalServerError("error clearing quests")
		}

		return &clearQuestsOutput{Body: StatusResponse{Status: "ok"}}, nil
	})

	// GET /api/devices/all
	huma.Register(api, huma.Operation{
		OperationID:   "get-devices",
		Method:        http.MethodGet,
		Path:          "/api/devices/all",
		Summary:       "List all known devices",
		Description:   "Returns, keyed by device uuid, the last location and scan context reported by every device whose raw submissions carried a uuid and a non-zero lat/lon target within the last cleanup.device_hours hours.",
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
		Description:   "Returns the pokestop and gym ids the fort tracker holds for the given S2 cell (the GMO map cell id, decimal) and the cell's last-seen time in unix milliseconds. 404 when the tracker has no such cell.",
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
		Description:   "Returns the S2 cell, last-seen time (unix milliseconds) and type (is_gym) the fort tracker holds for the given fort id. 404 when the tracker does not hold the fort or the id is malformed.",
		Tags:          []string{"FortTracker"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}, func(ctx context.Context, in *fortTrackerFortInput) (*fortTrackerFortOutput, error) {
		ft := decoder.GetFortTracker()
		if ft == nil {
			return nil, huma.Error503ServiceUnavailable("FortTracker not initialized")
		}
		fortId, ok := decoder.ParseFortId(in.FortId)
		if !ok {
			return nil, huma.Error404NotFound("Fort not found")
		}
		info := ft.GetFortInfo(fortId)
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
		Description:   "Reloads geofences from the configured source (Koji or file) and clears the area statistics. Always returns ok; reload errors are only logged.",
		Tags:          []string{"Admin"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusAccepted,
	}, reloadGeojsonHandler)
	huma.Register(api, huma.Operation{
		OperationID:   "reload-geojson-post",
		Method:        http.MethodPost,
		Path:          "/api/reload-geojson",
		Summary:       "Reload geofences and clear stats",
		Description:   "Reloads geofences from the configured source (Koji or file) and clears the area statistics. Always returns ok; reload errors are only logged.",
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
		Description:   "Sets a flag so the in-memory pokemon are not written to the database on the next shutdown (only relevant with pokemon_memory_only and preserve_pokemon). The flag cannot be cleared except by restarting.",
		Tags:          []string{"Admin"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}, skipPreserveHandler)
	huma.Register(api, huma.Operation{
		OperationID:   "skip-preserve-pokemon-post",
		Method:        http.MethodPost,
		Path:          "/api/skip-preserve-pokemon",
		Summary:       "Skip pokemon preservation on shutdown",
		Description:   "Sets a flag so the in-memory pokemon are not written to the database on the next shutdown (only relevant with pokemon_memory_only and preserve_pokemon). The flag cannot be cleared except by restarting.",
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
