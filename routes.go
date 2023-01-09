package main

import (
	"database/sql"
	b64 "encoding/base64"
	"encoding/json"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"golbat/config"
	"golbat/decoder"
	"golbat/geo"
	"io"
	"io/ioutil"
	"net/http"
	"strings"
)

type ProtoData struct {
	Data    []byte
	HaveAr  *bool
	Account string
	Level   int
	Uuid    string
}

type InboundRawData struct {
	Base64Data string
	Method     int
	HaveAr     *bool
}

func Raw(c *gin.Context) {
	var w http.ResponseWriter = c.Writer
	var r *http.Request = c.Request

	authHeader := r.Header.Get("Authorization")
	if config.Config.RawBearer != "" {
		if authHeader != "Bearer "+config.Config.RawBearer {
			log.Errorf("Raw: Incorrect authorisation received (%s)", authHeader)
			return
		}
	}

	body, err := ioutil.ReadAll(io.LimitReader(r.Body, 1048576))
	if err != nil {
		log.Errorf("Raw: Error (1) during HTTP receive %s", err)
		return
	}
	if err := r.Body.Close(); err != nil {
		log.Errorf("Raw: Error (2) during HTTP receive %s", err)
		return
	}

	decodeError := false
	uuid := ""
	account := ""
	level := 30
	var globalHaveAr *bool
	var protoData []InboundRawData

	// Objective is to normalise incoming proto data. Unfortunately each provider seems
	// to be just different enough that this ends up being a little bit more of a mess
	// than I would like

	pogodroidHeader := r.Header.Get("origin")
	userAgent := r.Header.Get("User-Agent")

	if pogodroidHeader != "" {
		var raw []map[string]interface{}
		if err := json.Unmarshal(body, &raw); err != nil {
			decodeError = true
		} else {
			for _, entry := range raw {
				protoData = append(protoData, InboundRawData{
					Base64Data: entry["payload"].(string),
					Method:     int(entry["type"].(float64)),
					HaveAr: func() *bool {
						if v := entry["have_ar"]; v != nil {
							res := v.(bool)
							return &res
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
			if v := raw["trainerlvl"]; v != nil { // Other MITM might use
				lvl, ok := v.(float64)
				if ok {
					level = int(lvl)
				}
			}
			contents := raw["contents"].([]interface{}) // Other MITM
			for _, v := range contents {
				entry := v.(map[string]interface{})
				if userAgent[:10] == "PokmonGO/0" { // GC
					protoData = append(protoData, InboundRawData{
						Base64Data: entry["data"].(string),
						Method:     int(entry["method"].(float64)),
						HaveAr: func() *bool {
							if v := entry["have_ar"]; v != nil {
								res, ok := v.(bool)
								if ok {
									return &res
								}
							} else {
								// TODO: Assume AR Quest in inv. Remove after `have_ar` will be added to GC.
								res := true
								return &res
							}
							return nil
						}(),
					})
				} else {
					protoData = append(protoData, InboundRawData{
						Base64Data: entry["payload"].(string),
						Method:     int(entry["type"].(float64)),
						HaveAr: func() *bool {
							if v := entry["have_ar"]; v != nil {
								res, ok := v.(bool)
								if ok {
									return &res
								}
							}
							return nil
						}(),
					})
				}
			}
		}
	}

	if decodeError == true {
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	}

	// Process each proto in a packet in sequence, but in a go-routine
	go func() {
		for _, entry := range protoData {
			method := entry.Method
			payload := entry.Base64Data

			haveAr := globalHaveAr
			if entry.HaveAr != nil {
				haveAr = entry.HaveAr
			}

			protoData := ProtoData{
				Account: account,
				Level:   level,
				HaveAr:  haveAr,
				Uuid:    uuid,
			}
			protoData.Data, _ = b64.StdEncoding.DecodeString(payload)

			decode(method, &protoData)
		}
	}()

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

func ClearQuests(c *gin.Context) {
	authHeader := c.Request.Header.Get("X-Golbat-Secret")
	if config.Config.ApiSecret != "" {
		if authHeader != config.Config.ApiSecret {
			log.Errorf("ClearQuests: Incorrect authorisation received (%s)", authHeader)
			c.String(http.StatusUnauthorized, "Unauthorised")
			return
		}
	}

	var golbatClearQuest GolbatClearQuest
	if err := c.BindJSON(&golbatClearQuest); err != nil {
		log.Warnf("POST /api/clearQuests/ Error during post area %v", err)
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
		decoder.ClearQuestsWithinGeofence(dbDetails, fence)
	}()

	c.JSON(http.StatusAccepted, map[string]interface{}{
		"status": "ok",
	})
}

func QueryPokemon(c *gin.Context) {
	authHeader := c.Request.Header.Get("X-Golbat-Secret")
	if config.Config.ApiSecret != "" {
		if authHeader != config.Config.ApiSecret {
			log.Errorf("Query: Incorrect authorisation received (%s)", authHeader)
			c.String(http.StatusUnauthorized, "Unauthorised")
			return
		}
	}

	data, err := c.GetRawData()
	if err != nil {
		return
	}
	query := string(data)
	//if err := c.BindJSON(&query); err != nil {
	//	return
	//}

	// This is bad

	log.Infof("Perform query API: [%d] %s", len(query), query)
	rows, err := dbDetails.PokemonDb.Query(query)
	if err != nil {
		log.Infof("Error executing query: %s", err)
		c.String(http.StatusInternalServerError, err.Error())
		return
	}

	z, err2 := toJson(rows)
	c.Data(http.StatusAccepted, "application/json", z)
	_, _ = err, err2
}

func toJson(rows *sql.Rows) ([]byte, error) {
	columnTypes, err := rows.ColumnTypes()

	if err != nil {
		return nil, err
	}

	count := len(columnTypes)
	finalRows := []interface{}{}

	for rows.Next() {

		scanArgs := make([]interface{}, count)

		for i, v := range columnTypes {
			var dbType string
			dbType = v.DatabaseTypeName()
			if idx := strings.IndexByte(dbType, '('); idx >= 0 {
				dbType = dbType[:idx]
			}

			switch dbType {
			case "varchar", "text", "VARCHAR", "TEXT", "UUID", "TIMESTAMP":
				scanArgs[i] = new(sql.NullString)
				break
			case "BOOL":
				scanArgs[i] = new(sql.NullBool)
				break
			case "smallint", "INT", "INT4":
				scanArgs[i] = new(sql.NullInt64)
				break
			case "float":
				scanArgs[i] = new(sql.NullFloat64)
				break
			default:
				scanArgs[i] = new(sql.NullString)
			}
		}

		err := rows.Scan(scanArgs...)

		if err != nil {
			return nil, err
		}

		masterData := map[string]interface{}{}

		for i, v := range columnTypes {

			if z, ok := (scanArgs[i]).(*sql.NullBool); ok {
				if z.Valid {
					masterData[v.Name()] = z.Bool
				} else {
					masterData[v.Name()] = nil
				}
				continue
			}

			if z, ok := (scanArgs[i]).(*sql.NullString); ok {
				if z.Valid {
					masterData[v.Name()] = z.String
				} else {
					masterData[v.Name()] = nil
				}
				continue
			}

			if z, ok := (scanArgs[i]).(*sql.NullInt64); ok {
				if z.Valid {
					masterData[v.Name()] = z.Int64
				} else {
					masterData[v.Name()] = nil
				}
				continue
			}

			if z, ok := (scanArgs[i]).(*sql.NullFloat64); ok {
				if z.Valid {
					masterData[v.Name()] = z.Float64
				} else {
					masterData[v.Name()] = nil
				}
				continue
			}

			if z, ok := (scanArgs[i]).(*sql.NullInt32); ok {
				if z.Valid {
					masterData[v.Name()] = z.Int32
				} else {
					masterData[v.Name()] = nil
				}
				continue
			}

			masterData[v.Name()] = scanArgs[i]
		}

		finalRows = append(finalRows, masterData)
	}

	z, err := json.Marshal(finalRows)
	return z, err
}
