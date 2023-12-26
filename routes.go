package main

import (
	"context"
	b64 "encoding/base64"
	"encoding/json"
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

/* Should really be in a separate file, move later */

type ApiLocation struct {
	Latitude  float64 `json:"lat"`
	Longitude float64 `json:"lon"`
}

type GolbatClearQuest struct {
	Fence []ApiLocation `json:"fence"`
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
	var golbatClearQuest GolbatClearQuest
	if err := c.BindJSON(&golbatClearQuest); err != nil {
		log.Warnf("POST /api/clear-quests/ Error during post area %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}

	locations := make([]geo.Location, 0, len(golbatClearQuest.Fence)+1)
	for _, loc := range golbatClearQuest.Fence {
		locations = append(locations, geo.Location{
			Latitude:  loc.Latitude,
			Longitude: loc.Longitude,
		})
	}

	// Ensure the fence is closed
	if locations[0] != locations[len(locations)-1] {
		locations = append(locations, locations[0])
	}

	fence := geo.Geofence{
		Fence: locations,
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		decoder.ClearQuestsWithinGeofence(ctx, dbDetails, fence)
	}()

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

func ReloadNests(c *gin.Context) {
	decoder.ReloadNestsAndClearStats(dbDetails)

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
		c.JSON(http.StatusAccepted, map[string]interface{}{
			"lat": res.Lat,
			"lon": res.Lon,
		})
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

	res := decoder.SearchPokemon(requestBody)
	c.JSON(http.StatusAccepted, res)
}

func GetQuestStatus(c *gin.Context) {
	var golbatClearQuest GolbatClearQuest
	if err := c.BindJSON(&golbatClearQuest); err != nil {
		log.Warnf("POST /api/quest-status/ Error during post area %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}

	if len(golbatClearQuest.Fence) == 0 {
		c.JSON(http.StatusBadRequest, map[string]interface{}{
			"status": "error",
			"data":   nil,
		})
		return
	}

	locations := make([]geo.Location, 0, len(golbatClearQuest.Fence))
	for _, loc := range golbatClearQuest.Fence {
		locations = append(locations, geo.Location{
			Latitude:  loc.Latitude,
			Longitude: loc.Longitude,
		})
	}

	// Ensure the fence is closed
	if locations[0] != locations[len(locations)-1] {
		locations = append(locations, locations[0])
	}

	fence := geo.Geofence{
		Fence: locations,
	}

	questStatus := decoder.GetQuestStatusWithGeofence(dbDetails, fence)

	c.JSON(http.StatusOK, &questStatus)
}

// GetHealth provides unrestricted health status for monitoring tools
func GetHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func GetPokestopPositions(c *gin.Context) {
	var requestBody geo.Geofence

	if err := c.BindJSON(&requestBody); err != nil {
		log.Warnf("POST /api/pokestop-positions/ Error during post retrieve %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}

	res, err := decoder.GetPokestopPositions(dbDetails, requestBody)
	if err != nil {
		log.Warnf("POST /api/pokestop-positions/ Error during post retrieve %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusAccepted, res)
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

func GetDevices(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"devices": GetAllDevices()})
}
