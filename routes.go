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
	TimestampMs int64
}

// nebulaInvasionContext mirrors the proto InvasionContext oneof case.
type nebulaInvasionContext struct {
	FortId     string
	IncidentId string
}

type NebulaData struct {
	Endpoint    string
	Data        []byte
	Request     []byte
	Invasion    *nebulaInvasionContext // set when the context oneof case is invasion
	BattleId    string
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

type StatusResponse struct {
	Status string `json:"status"`
}

// getString extracts a string value from a map[string]any, returning "" if absent or not a string.
func getString(m map[string]any, k string) string {
	v, _ := m[k].(string)
	return v
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
	r := c.Request

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
	var nebulaItems []NebulaData
	type pushItem struct {
		MessageType string
		Payload     []byte
	}
	var pushItems []pushItem

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

			if rawNebula, ok := raw["nebula_contents"].([]any); ok {
				for _, item := range rawNebula {
					m, ok := item.(map[string]any)
					if !ok {
						continue
					}
					payload, _ := b64.StdEncoding.DecodeString(getString(m, "payload"))
					request, _ := b64.StdEncoding.DecodeString(getString(m, "request"))
					nd := NebulaData{
						Endpoint:    getString(m, "endpoint"),
						Data:        payload,
						Request:     request,
						BattleId:    getString(m, "battle_id"),
						Account:     account,
						Level:       level,
						Uuid:        uuid,
						ScanContext: scanContext,
						Lat:         latTarget,
						Lon:         lonTarget,
						TimestampMs: dataReceivedTimestamp,
					}
					// context: { "invasion": { "fort_id": "...", "incident_id": "..." } }
					if ctxObj, ok := m["context"].(map[string]any); ok {
						if inv, ok := ctxObj["invasion"].(map[string]any); ok {
							nd.Invasion = &nebulaInvasionContext{
								FortId:     getString(inv, "fort_id"),
								IncidentId: getString(inv, "incident_id"),
							}
						}
					}
					nebulaItems = append(nebulaItems, nd)
				}
			}

			if rawPush, ok := raw["push_contents"].([]any); ok {
				for _, item := range rawPush {
					m, ok := item.(map[string]any)
					if !ok {
						continue
					}
					msgType := getString(m, "message_type")
					payload, _ := b64.StdEncoding.DecodeString(getString(m, "payload"))
					if msgType == "" || payload == nil {
						continue
					}
					pushItems = append(pushItems, pushItem{MessageType: msgType, Payload: payload})
				}
			}

			contents, ok := raw["contents"].([]interface{})
			if !ok {
				if len(nebulaItems) == 0 && len(pushItems) == 0 {
					decodeError = true
				}
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

	if decodeError {
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

		for _, entry := range nebulaItems {
			go decodeNebula(context.Background(), entry.Endpoint, &entry)
		}

		for _, entry := range pushItems {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			decodePushGateway(ctx, entry.MessageType, entry.Payload)
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

	c.JSON(http.StatusAccepted, StatusResponse{Status: "ok"})
}

func ReloadGeojson(c *gin.Context) {
	decoder.ReloadGeofenceAndClearStats()

	c.JSON(http.StatusAccepted, StatusResponse{Status: "ok"})
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

func PokemonAvailable(c *gin.Context) {
	res := decoder.GetAvailablePokemon()
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

func GetDevices(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"devices": GetAllDevices()})
}

func GetFortTrackerCell(c *gin.Context) {
	cellIdStr := c.Param("cell_id")
	cellId, err := strconv.ParseUint(cellIdStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid cell ID"})
		return
	}

	fortTracker := decoder.GetFortTracker()
	if fortTracker == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "FortTracker not initialized"})
		return
	}

	cellInfo := fortTracker.GetCellInfo(cellId)
	if cellInfo == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Cell not found"})
		return
	}

	c.JSON(http.StatusOK, cellInfo)
}

func GetFortTrackerFort(c *gin.Context) {
	fortId := c.Param("fort_id")

	fortTracker := decoder.GetFortTracker()
	if fortTracker == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "FortTracker not initialized"})
		return
	}

	fortInfo := fortTracker.GetFortInfo(fortId)
	if fortInfo == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Fort not found"})
		return
	}

	c.JSON(http.StatusOK, fortInfo)
}

// SkipPreservePokemon sets a flag to prevent pokemon preservation on shutdown
func SkipPreservePokemon(c *gin.Context) {
	decoder.SetSkipPreservePokemon(true)
	log.Info("Skip preserve pokemon flag set - pokemon will not be preserved on shutdown")
	c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "Pokemon preservation will be skipped on shutdown"})
}
