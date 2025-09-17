package main

import (
	"context"
	b64 "encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"golbat/config"
	"golbat/decoder"
	"golbat/geo"
	"golbat/pogo"
)

type ProtoData struct {
	Method      int
	Data        []byte
	Request     []byte
	HaveAr      *bool
	Account     string
	Level       int
	Uuid        string
	ScanContext string
	Lat         float64
	Lon         float64
	TimestampMs int64
}

type InboundRawData struct {
	Base64Data string
	Request    string
	Method     int
	HaveAr     *bool
}

func questsHeldHasARTask(quests_held any) *bool {
	const ar_quest_id = int64(pogo.QuestType_QUEST_GEOTARGETED_AR_SCAN)

	quests_held_list, ok := quests_held.([]any)
	if !ok {
		log.Errorf("Raw: unexpected quests_held type in data: %T", quests_held)
		return nil
	}
	for _, quest_id := range quests_held_list {
		if quest_id_f, ok := quest_id.(float64); ok {
			if int64(quest_id_f) == ar_quest_id {
				res := true
				return &res
			}
			continue
		}
		// quest_id is not float64? Treat the whole thing as unknown.
		log.Errorf("Raw: unexpected quest_id type in quests_held: %T", quest_id)
		return nil
	}
	res := false
	return &res
}

func Raw(c *gin.Context) {
	var w http.ResponseWriter = c.Writer
	var r *http.Request = c.Request

	dataReceivedTimestamp := time.Now().UnixMilli()

	authHeader := r.Header.Get("Authorization")
	if config.Config.RawBearer != "" {
		if authHeader != "Bearer "+config.Config.RawBearer {
			statsCollector.IncRawRequests("error", "auth")
			log.Errorf("Raw: Incorrect authorisation received (%s)", authHeader)
			return
		}
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 5*1048576))
	if err != nil {
		statsCollector.IncRawRequests("error", "io_error")
		log.Errorf("Raw: Error (1) during HTTP receive %s", err)
		return
	}
	if err := r.Body.Close(); err != nil {
		statsCollector.IncRawRequests("error", "io_close_error")
		log.Errorf("Raw: Error (2) during HTTP receive %s", err)
		return
	}

	decodeError := false
	uuid := ""
	account := ""
	level := 30
	scanContext := ""
	var latTarget, lonTarget float64
	var globalHaveAr *bool
	var protoData []InboundRawData

	// Objective is to normalise incoming proto data. Unfortunately each provider seems
	// to be just different enough that this ends up being a little bit more of a mess
	// than I would like

	pogodroidHeader := r.Header.Get("origin")
	userAgent := r.Header.Get("User-Agent")

	//log.Infof("Raw: Received data from %s", body)
	//log.Infof("User agent is %s", userAgent)

	if pogodroidHeader != "" {
		var raw []map[string]interface{}
		if err := json.Unmarshal(body, &raw); err != nil {
			decodeError = true
		} else {
			for _, entry := range raw {
				if latTarget == 0 && lonTarget == 0 {
					lat := entry["lat"]
					lng := entry["lng"]
					if lat != nil && lng != nil {
						lat_f, _ := lat.(float64)
						lng_f, _ := lng.(float64)
						if lat_f != 0 && lng_f != 0 {
							latTarget = lat_f
							lonTarget = lng_f
						}
					}
				}
				protoData = append(protoData, InboundRawData{
					Base64Data: entry["payload"].(string),
					Method:     int(entry["type"].(float64)),
					HaveAr: func() *bool {
						if v := entry["quests_held"]; v != nil {
							return questsHeldHasARTask(v)
						}
						return nil
					}(),
				})
			}
		}
		uuid = pogodroidHeader
		account = "Pogodroid"
	} else {
		var raw map[string]interface{}
		if err := json.Unmarshal(body, &raw); err != nil {
			decodeError = true
		} else {
			if v := raw["have_ar"]; v != nil {
				res, ok := v.(bool)
				if ok {
					globalHaveAr = &res
				}
			}
			if v := raw["uuid"]; v != nil {
				uuid, _ = v.(string)
			}
			if v := raw["username"]; v != nil {
				account, _ = v.(string)
			}
			if v := raw["trainerlvl"]; v != nil {
				lvl, ok := v.(float64)
				if ok {
					level = int(lvl)
				}
			}
			if v := raw["scan_context"]; v != nil {
				scanContext, _ = v.(string)
			}

			if v := raw["lat_target"]; v != nil {
				latTarget, _ = v.(float64)
			}

			if v := raw["lon_target"]; v != nil {
				lonTarget, _ = v.(float64)
			}

			if v := raw["timestamp_ms"]; v != nil {
				ts, _ := v.(int64)
				if ts > 0 {
					dataReceivedTimestamp = ts
				}
			}

			contents, ok := raw["contents"].([]interface{})
			if !ok {
				decodeError = true

			} else {

				decodeAlternate := func(data map[string]interface{}, key1, key2 string) interface{} {
					if v := data[key1]; v != nil {
						return v
					}
					if v := data[key2]; v != nil {
						return v
					}
					return nil
				}

				for _, v := range contents {
					entry := v.(map[string]interface{})
					// Try to decode the payload automatically without requiring any knowledge of the
					// provider type

					b64data := decodeAlternate(entry, "data", "payload")
					method := decodeAlternate(entry, "method", "type")
					request := entry["request"]
					haveAr := entry["have_ar"]

					if method == nil || b64data == nil {
						log.Errorf("Error decoding raw")
						continue
					}
					inboundRawData := InboundRawData{
						Base64Data: func() string {
							if res, ok := b64data.(string); ok {
								return res
							}
							return ""
						}(),
						Method: func() int {
							if res, ok := method.(float64); ok {
								return int(res)
							}

							return 0
						}(),
						Request: func() string {
							if request != nil {
								res, ok := request.(string)
								if ok {
									return res
								}
							}
							return ""
						}(),
						HaveAr: func() *bool {
							if haveAr != nil {
								res, ok := haveAr.(bool)
								if ok {
									return &res
								}
							}
							return nil
						}(),
					}

					protoData = append(protoData, inboundRawData)
				}
			}
		}
	}

	if decodeError == true {
		statsCollector.IncRawRequests("error", "decode")
		log.Infof("Raw: Data could not be decoded. From User agent %s - Received data %s", userAgent, body)

		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	}

	// Process each proto in a packet in sequence, but in a go-routine
	go func() {
		timeout := 5 * time.Second
		if config.Config.Tuning.ExtendedTimeout {
			timeout = 30 * time.Second
		}

		for _, entry := range protoData {
			method := entry.Method
			payload := entry.Base64Data
			request := entry.Request

			haveAr := globalHaveAr
			if entry.HaveAr != nil {
				haveAr = entry.HaveAr
			}

			protoData := ProtoData{
				Account:     account,
				Level:       level,
				HaveAr:      haveAr,
				Uuid:        uuid,
				Lat:         latTarget,
				Lon:         lonTarget,
				ScanContext: scanContext,
				TimestampMs: dataReceivedTimestamp,
			}
			protoData.Data, _ = b64.StdEncoding.DecodeString(payload)
			if request != "" {
				protoData.Request, _ = b64.StdEncoding.DecodeString(request)
			}

			// provide independent cancellation contexts for each proto decode
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			decode(ctx, method, &protoData)
			cancel()
		}
	}()

	if latTarget != 0 && lonTarget != 0 && uuid != "" {
		UpdateDeviceLocation(uuid, latTarget, lonTarget, scanContext)
	}

	statsCollector.IncRawRequests("ok", "")
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(http.StatusCreated)
	//if err := json.NewEncoder(w).Encode(t); err != nil {
	//	panic(err)
	//}
}

func AuthRequired() gin.HandlerFunc {
	return func(context *gin.Context) {
		if config.Config.ApiSecret != "" {
			authHeader := context.Request.Header.Get("X-Golbat-Secret")
			if authHeader != config.Config.ApiSecret {
				log.Errorf("Incorrect authorisation received (%s)", authHeader)
				context.String(http.StatusUnauthorized, "Unauthorised")
				context.Abort()
				return
			}
		}
		context.Next()
	}
}

func ClearQuests(c *gin.Context) {
	fence, err := geo.NormaliseFenceRequest(c)

	if err != nil {
		log.Warnf("POST /api/clear-quests/ Error during post area %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	log.Debugf("Clear quests %+v", fence)
	startTime := time.Now()
	decoder.ClearQuestsWithinGeofence(ctx, dbDetails, fence)
	log.Infof("Clear quest took %s", time.Since(startTime))

	c.JSON(http.StatusAccepted, map[string]interface{}{
		"status": "ok",
	})
}

func ReloadGeojson(c *gin.Context) {
	decoder.ReloadGeofenceAndClearStats()

	c.JSON(http.StatusAccepted, map[string]interface{}{
		"status": "ok",
	})
}

func PokemonScan(c *gin.Context) {
	var requestBody decoder.ApiPokemonScan

	if err := c.BindJSON(&requestBody); err != nil {
		log.Warnf("POST /api/pokemon/scan/ Error during post retrieve %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}

	res := decoder.GetPokemonInArea(requestBody)
	if res == nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.JSON(http.StatusAccepted, res)
}

func PokemonScan2(c *gin.Context) {
	var requestBody decoder.ApiPokemonScan2

	if err := c.BindJSON(&requestBody); err != nil {
		log.Warnf("POST /api/pokemon/scan/ Error during post retrieve %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}

	res := decoder.GetPokemonInArea2(requestBody)
	if res == nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.JSON(http.StatusAccepted, res)
}

func PokemonOne(c *gin.Context) {
	pokemonId, err := strconv.ParseUint(c.Param("pokemon_id"), 10, 64)
	if err != nil {
		log.Warnf("GET /api/pokemon/:pokemon_id/ Error during get pokemon %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}
	res := decoder.GetOnePokemon(uint64(pokemonId))

	if res != nil {
		c.JSON(http.StatusAccepted, res)
	} else {
		c.Status(http.StatusNotFound)
	}
}

func PokemonAvailable(c *gin.Context) {
	res := decoder.GetAvailablePokemon()
	c.JSON(http.StatusAccepted, res)
}

func PokemonSearch(c *gin.Context) {
	var requestBody decoder.ApiPokemonSearch

	if err := c.BindJSON(&requestBody); err != nil {
		log.Warnf("POST /api/search/ Error during post search %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}

	res, err := decoder.SearchPokemon(requestBody)
	if err != nil {
		log.Warnf("POST /api/search/ Error during post search %v", err)
		c.Status(http.StatusBadRequest)
		return
	}
	c.JSON(http.StatusAccepted, res)
}

func GetQuestStatus(c *gin.Context) {
	fence, err := geo.NormaliseFenceRequest(c)

	if err != nil {
		log.Warnf("POST /api/quest-status/ Error during post area %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}

	questStatus := decoder.GetQuestStatusWithGeofence(dbDetails, fence)

	c.JSON(http.StatusOK, &questStatus)
}

// GetHealth provides unrestricted health status for monitoring tools
func GetHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func GetPokestopPositions(c *gin.Context) {
	fence, err := geo.NormaliseFenceRequest(c)
	if err != nil {
		log.Warnf("POST /api/pokestop-positions/ Error during post area %v %v", err, fence)
		c.Status(http.StatusInternalServerError)
		return
	}

	response, err := decoder.GetPokestopPositions(dbDetails, fence)
	if err != nil {
		log.Warnf("POST /api/pokestop-positions/ Error during post retrieve %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusAccepted, response)
}

func GetPokestop(c *gin.Context) {
	fortId := c.Param("fort_id")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	pokestop, err := decoder.GetPokestopRecord(ctx, dbDetails, fortId)
	cancel()
	if err != nil {
		log.Warnf("GET /api/pokestop/id/:fort_id/ Error during post retrieve %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusAccepted, pokestop)
}

func GetGym(c *gin.Context) {
	gymId := c.Param("gym_id")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	gym, err := decoder.GetGymRecord(ctx, dbDetails, gymId)
	cancel()
	if err != nil {
		log.Warnf("GET /api/gym/id/:gym_id/ Error during post retrieve %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusAccepted, gym)
}

// POST /api/gym/query
//
//	{ "ids": ["gymid1", "gymid2", ...] }
func GetGyms(c *gin.Context) {
	type idsPayload struct {
		IDs []string `json:"ids"`
	}

	var payload idsPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		var arr []string
		if err2 := c.ShouldBindJSON(&arr); err2 != nil {
			log.Warnf("invalid JSON: %v / %v", err, err2)
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body; expected {\"ids\":[...] }"})
			return
		}
		payload.IDs = arr
	}

	seen := make(map[string]struct{}, len(payload.IDs))
	ids := make([]string, 0, len(payload.IDs))
	for _, id := range payload.IDs {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}

	const maxIDs = 500
	if len(ids) > maxIDs {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"error":         "too many ids",
			"max_supported": maxIDs,
		})
		return
	}

	if len(ids) == 0 {
		c.JSON(http.StatusOK, []decoder.Gym{})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out := make([]*decoder.Gym, 0, len(ids))
	for _, id := range ids {
		g, err := decoder.GetGymRecord(ctx, dbDetails, id)
		if err != nil {
			log.Warnf("error retrieving gym %s: %v", id, err)
			c.Status(http.StatusInternalServerError)
			return
		}
		if g != nil {
			out = append(out, g)
		}
		if ctx.Err() != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
	}

	c.JSON(http.StatusOK, out)
}

// POST /api/gym/search
// Multiple filter combinations with AND logic
//
//	{
//	  "filters": [
//	    {
//	      "name": "central park",           // optional: gym name search
//	      "description": "playground",      // optional: gym description search
//	      "location_distance": {            // optional: geographic radius search
//	        "location": {"lat": 40.7829, "lon": -73.9654},
//	        "distance": 500                 // meters, max 500_000
//	      },
//	      "bbox": {                         // optional: bounding box search
//	        "min_lon": -74.0, "min_lat": 40.7,
//	        "max_lon": -73.9, "max_lat": 40.8
//	      }
//	    }
//	  ],
//	  "limit": 100                        // optional, default 500, max 10000
//	}
func SearchGyms(c *gin.Context) {
	type payload struct {
		Filters []decoder.ApiGymSearchFilter `json:"filters"`
		Limit   *int                         `json:"limit"`
	}

	var p payload
	if err := c.ShouldBindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
		return
	}

	// Validate request
	if len(p.Filters) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "filters array is required"})
		return
	}

	var search decoder.ApiGymSearch
	search.Filters = p.Filters

	// Validate filters
	for _, filter := range search.Filters {
		if filter.LocationDistance != nil {
			locDist := *filter.LocationDistance
			if locDist.Distance <= 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "distance must be > 0"})
				return
			}
			if locDist.Distance > 500_000 {
				locDist.Distance = 500_000
				filter.LocationDistance = &locDist
			}
			lat, lon := locDist.Location.Latitude, locDist.Location.Longitude
			if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "lat must be [-90,90], lon must be [-180,180]"})
				return
			}
		}
		if filter.Bbox != nil {
			bbox := *filter.Bbox
			if bbox.MinLat < -90 || bbox.MinLat > 90 || bbox.MaxLat < -90 || bbox.MaxLat > 90 ||
				bbox.MinLon < -180 || bbox.MinLon > 180 || bbox.MaxLon < -180 || bbox.MaxLon > 180 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "bbox coordinates out of range: lat must be [-90,90], lon must be [-180,180]"})
				return
			}
			if bbox.MinLat > bbox.MaxLat {
				c.JSON(http.StatusBadRequest, gin.H{"error": "bbox invalid: minLat must be <= maxLat"})
				return
			}
			if bbox.MinLon > bbox.MaxLon {
				c.JSON(http.StatusBadRequest, gin.H{"error": "bbox invalid: minLon must be <= maxLon"})
				return
			}
		}
	}

	// Set limit
	search.Limit = 500
	if p.Limit != nil && *p.Limit > 0 {
		search.Limit = *p.Limit
	}
	if search.Limit > 10000 {
		search.Limit = 10000
	}

	// Execute search
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ids, err := decoder.SearchGymsAPI(ctx, dbDetails, search)

	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			log.Warnf("timed out: %v", err)
			c.Status(http.StatusGatewayTimeout)
			return
		}
		log.Warnf("error: %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}

	out := make([]*decoder.Gym, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		g, err := decoder.GetGymRecord(ctx, dbDetails, id)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				log.Warnf("timed out while fetching %s: %v", id, err)
				c.Status(http.StatusGatewayTimeout)
				return
			}
			log.Warnf("error retrieving gym %s: %v", id, err)
			c.Status(http.StatusInternalServerError)
			return
		}
		if g != nil {
			out = append(out, g)
		}
		if ctx.Err() != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
	}

	c.JSON(http.StatusOK, out)
}

func GetTappable(c *gin.Context) {
	id := c.Param("tappable_id")
	tappableId, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		log.Warnf("GET /api/tappable/id/:tappable_id/ Non valid param: %v", err)
		c.Status(http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	tappable, err := decoder.GetTappableRecord(ctx, dbDetails, tappableId)
	cancel()
	if err != nil {
		log.Warnf("GET /api/tappable/id/:tappable_id/ Error during post retrieve %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusAccepted, tappable)
}

func GetDevices(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"devices": GetAllDevices()})
}
