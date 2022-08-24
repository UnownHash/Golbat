package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/golang-migrate/migrate/v4"
	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"
	"golbat/config"
	"google.golang.org/protobuf/proto"
	"io"
	"io/ioutil"
	"strings"

	"golbat/decoder"
	"golbat/webhooks"
	"net/http"
	"time"

	b64 "encoding/base64"
	"github.com/gin-gonic/gin"
	"github.com/toorop/gin-logrus"

	"github.com/go-sql-driver/mysql"
	"golbat/pogo"

	_ "github.com/VoltDB/voltdb-client-go/voltdbclient"
	_ "github.com/golang-migrate/migrate/v4/database/mysql"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/mattn/go-sqlite3"
)

var db *sqlx.DB
var voltDb *sqlx.DB
var dbDetails decoder.DbDetails

func main() {

	config.ReadConfig()

	// Capture connection properties.
	cfg := mysql.Config{
		User:                 config.Config.Database.User,     //"root",     //os.Getenv("DBUSER"),
		Passwd:               config.Config.Database.Password, //"transmit", //os.Getenv("DBPASS"),
		Net:                  "tcp",
		Addr:                 config.Config.Database.Addr,
		DBName:               config.Config.Database.Db,
		AllowNativePasswords: true,
	}

	dbConnectionString := cfg.FormatDSN()
	driver := "mysql"

	m, err := migrate.New(
		"file://sql",
		driver+"://"+dbConnectionString+"&multiStatements=true")
	if err != nil {
		log.Fatal(err)
		return
	}
	err = m.Up()
	if err != nil && err != migrate.ErrNoChange {
		log.Fatal(err)
		return
	}

	// Get a database handle.

	db, err = sqlx.Open(driver, dbConnectionString)
	if err != nil {
		log.Fatal(err)
		return
	}

	db.SetMaxOpenConns(50)
	db.SetMaxIdleConns(10)
	db.SetConnMaxIdleTime(time.Minute)

	pingErr := db.Ping()
	if pingErr != nil {
		log.Fatal(pingErr)
		return
	}
	log.Infoln("Connected to database")

	voltDb, err = sqlx.Open("sqlite3", ":memory:")
	if err != nil {
		log.Fatal(err)
		return
	}

	voltDb.SetMaxOpenConns(1)

	pingErr = voltDb.Ping()
	if pingErr != nil {
		log.Fatal(pingErr)
		return
	}

	// Create database
	content, fileErr := ioutil.ReadFile("sql/voltdb/create.sql")

	if fileErr != nil {
		log.Fatal(err)
	}

	voltDb.MustExec(string(content))

	dbDetails = decoder.DbDetails{
		PokemonDb: voltDb,
		GeneralDb: db,
	}

	if config.Config.DebugLog == true {
		log.SetLevel(log.DebugLevel)
	}
	log.Infoln("Golbat started")
	webhooks.StartSender()
	StartStatsLogger(voltDb) // clear internal db

	//if config.Config.Archive == true {
	StartDatabaseArchiver(db)
	//}

	r := gin.New()
	r.Use(ginlogrus.Logger(log.StandardLogger()), gin.Recovery())
	r.POST("/raw", Raw)
	r.POST("/api/clearQuests", ClearQuests)
	r.POST("/api/queryPokemon", QueryPokemon)

	//router := mux.NewRouter().StrictSlash(true)
	//router.HandleFunc("/raw", Raw)
	addr := fmt.Sprintf(":%d", config.Config.Port)
	//log.Fatal(http.ListenAndServe(addr, router)) // addr is in form :9001
	err = r.Run(addr)
	if err != nil {
		log.Fatal(err)
	}
}

func decode(method int, protoData *ProtoData) {
	processed := false
	start := time.Now()
	result := ""

	switch pogo.Method(method) {
	case pogo.Method_METHOD_FORT_DETAILS:
		result = decodeFortDetails(protoData.Data)
		processed = true
	case pogo.Method_METHOD_GET_MAP_OBJECTS:
		result = decodeGMO(protoData.Data)
		processed = true
	case pogo.Method_METHOD_GYM_GET_INFO:
		result = decodeGetGymInfo(protoData.Data)
		processed = true
	case pogo.Method_METHOD_ENCOUNTER:
		result = decodeEncounter(protoData.Data)
		processed = true
	case pogo.Method_METHOD_DISK_ENCOUNTER:
		result = decodeDiskEncounter(protoData.Data)
		processed = true
	case pogo.Method_METHOD_FORT_SEARCH:
		result = decodeQuest(protoData.Data, protoData.HaveAr)
		processed = true
	case pogo.Method_METHOD_GET_PLAYER:
		break
	case pogo.Method_METHOD_GET_HOLOHOLO_INVENTORY:
		break
	case pogo.Method_METHOD_CREATE_COMBAT_CHALLENGE:
		// ignore
		break
	default:
		log.Debugf("Did not process hook type %s", pogo.Method(method))
	}

	if processed == true {
		elapsed := time.Since(start)

		log.Debugf("%s/%s %s - %s - %s", protoData.Uuid, protoData.Account, pogo.Method(method), elapsed, result)
	}
}

func decodeQuest(sDec []byte, haveAr *bool) string {
	if haveAr == nil {
		log.Infoln("Cannot determine AR quest - ignoring")
		// We should either assume AR quest, or trace inventory like RDM probably
		return "No AR quest info"
	}
	decodedQuest := &pogo.FortSearchOutProto{}
	if err := proto.Unmarshal(sDec, decodedQuest); err != nil {
		log.Fatalln("Failed to parse", err)
		return "Parse failure"
	}

	if decodedQuest.Result != pogo.FortSearchOutProto_SUCCESS {
		res := fmt.Sprintf(`GymGetInfoOutProto: Ignored non-success value %d:%s`, decodedQuest.Result,
			pogo.FortSearchOutProto_Result_name[int32(decodedQuest.Result)])
		return res
	}

	return decoder.UpdatePokestopWithQuest(dbDetails, decodedQuest, *haveAr)

}

func decodeFortDetails(sDec []byte) string {
	decodedFort := &pogo.FortDetailsOutProto{}
	if err := proto.Unmarshal(sDec, decodedFort); err != nil {
		log.Fatalln("Failed to parse", err)
		return fmt.Sprintf("Failed to parse %s", err)
	}

	switch decodedFort.FortType {
	case pogo.FortType_CHECKPOINT:
		return decoder.UpdatePokestopRecordWithFortDetailsOutProto(dbDetails, decodedFort)
	case pogo.FortType_GYM:
		return decoder.UpdateGymRecordWithFortDetailsOutProto(dbDetails, decodedFort)
	}
	return "Unknown fort type"
}

func decodeGetGymInfo(sDec []byte) string {
	decodedGymInfo := &pogo.GymGetInfoOutProto{}
	if err := proto.Unmarshal(sDec, decodedGymInfo); err != nil {
		log.Fatalln("Failed to parse", err)
		return fmt.Sprintf("Failed to parse %s", err)
	}

	if decodedGymInfo.Result != pogo.GymGetInfoOutProto_SUCCESS {
		res := fmt.Sprintf(`GymGetInfoOutProto: Ignored non-success value %d:%s`, decodedGymInfo.Result,
			pogo.GymGetInfoOutProto_Result_name[int32(decodedGymInfo.Result)])
		return res
	}
	return decoder.UpdateGymRecordWithGymInfoProto(dbDetails, decodedGymInfo)
}

func decodeEncounter(sDec []byte) string {
	decodedEncounterInfo := &pogo.EncounterOutProto{}
	if err := proto.Unmarshal(sDec, decodedEncounterInfo); err != nil {
		log.Fatalln("Failed to parse", err)
		return fmt.Sprintf("Failed to parse %s", err)
	}

	if decodedEncounterInfo.Status != pogo.EncounterOutProto_ENCOUNTER_SUCCESS {
		res := fmt.Sprintf(`GymGetInfoOutProto: Ignored non-success value %d:%s`, decodedEncounterInfo.Status,
			pogo.EncounterOutProto_Status_name[int32(decodedEncounterInfo.Status)])
		return res
	}
	return decoder.UpdatePokemonRecordWithEncounterProto(dbDetails, decodedEncounterInfo)
}

func decodeDiskEncounter(sDec []byte) string {
	decodedEncounterInfo := &pogo.DiskEncounterOutProto{}
	if err := proto.Unmarshal(sDec, decodedEncounterInfo); err != nil {
		log.Fatalln("Failed to parse", err)
		return fmt.Sprintf("Failed to parse %s", err)
	}

	if decodedEncounterInfo.Result != pogo.DiskEncounterOutProto_SUCCESS {
		res := fmt.Sprintf(`DiskEncounterOutProto: Ignored non-success value %d:%s`, decodedEncounterInfo.Result,
			pogo.DiskEncounterOutProto_Result_name[int32(decodedEncounterInfo.Result)])
		return res
	}

	return decoder.UpdatePokemonRecordWithDiskEncounterProto(dbDetails, decodedEncounterInfo)
}

func decodeGMO(sDec []byte) string {
	decodedGmo := &pogo.GetMapObjectsOutProto{}

	if err := proto.Unmarshal(sDec, decodedGmo); err != nil {
		log.Fatalln("Failed to parse", err)
	}

	if decodedGmo.Status != pogo.GetMapObjectsOutProto_SUCCESS {
		res := fmt.Sprintf(`GetMapObjectsOutProto: Ignored non-success value %d:%s`, decodedGmo.Status,
			pogo.GetMapObjectsOutProto_Status_name[int32(decodedGmo.Status)])
		return res
	}

	var newForts []decoder.RawFortData
	var newWildPokemon []decoder.RawWildPokemonData
	var newNearbyPokemon []decoder.RawNearbyPokemonData
	var newMapPokemon []decoder.RawMapPokemonData

	for _, mapCell := range decodedGmo.MapCell {
		timestampMs := uint64(mapCell.AsOfTimeMs)
		for _, fort := range mapCell.Fort {
			newForts = append(newForts, decoder.RawFortData{Cell: mapCell.S2CellId, Data: fort})

			if fort.ActivePokemon != nil {
				newMapPokemon = append(newMapPokemon, decoder.RawMapPokemonData{Cell: mapCell.S2CellId, Data: fort.ActivePokemon})
			}
		}
		for _, mon := range mapCell.WildPokemon {
			newWildPokemon = append(newWildPokemon, decoder.RawWildPokemonData{Cell: mapCell.S2CellId, Data: mon, Timestamp: timestampMs})
		}
		for _, mon := range mapCell.NearbyPokemon {
			newNearbyPokemon = append(newNearbyPokemon, decoder.RawNearbyPokemonData{Cell: mapCell.S2CellId, Data: mon})
		}
	}

	decoder.UpdateFortBatch(dbDetails, newForts)
	decoder.UpdatePokemonBatch(dbDetails, newWildPokemon, newNearbyPokemon, newMapPokemon)

	return fmt.Sprintf("%d cells containing %d forts %d mon %d nearby", len(decodedGmo.MapCell), len(newForts), len(newWildPokemon), len(newNearbyPokemon))
}

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

	body, err := ioutil.ReadAll(io.LimitReader(r.Body, 1048576))
	if err != nil {
		panic(err)
	}
	if err := r.Body.Close(); err != nil {
		log.Errorf("Raw: Error during HTTP receive %s", err)
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

	if decodeError == true {
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	}

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

		go decode(method, &protoData)
	}
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(http.StatusCreated)
	//if err := json.NewEncoder(w).Encode(t); err != nil {
	//	panic(err)
	//}
}

func ClearQuests(c *gin.Context) {
	c.JSON(http.StatusAccepted, map[string]interface{}{
		"status": "ok",
	})
}

func QueryPokemon(c *gin.Context) {

	data, err := c.GetRawData()
	if err != nil {
		return
	}
	query := string(data)
	//if err := c.BindJSON(&query); err != nil {
	//	return
	//}

	// This is bad

	log.Infof("Perform query API: %s", query)
	rows, err := voltDb.Query(query)
	if err != nil {
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
