package main

import (
	"encoding/json"
	"fmt"
	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"
	"golbat/config"
	"google.golang.org/protobuf/proto"
	"io"
	"io/ioutil"

	"golbat/decoder"
	"golbat/webhooks"
	"net/http"
	"time"

	b64 "encoding/base64"
	"github.com/gorilla/mux"

	"github.com/go-sql-driver/mysql"
	"golbat/pogo"
)

var db *sqlx.DB

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

	// Get a database handle.
	var err error
	db, err = sqlx.Open("mysql", cfg.FormatDSN())
	if err != nil {
		log.Fatal(err)
		return
	}

	pingErr := db.Ping()
	if pingErr != nil {
		log.Fatal(pingErr)
		return
	}
	log.Infoln("Connected to database")

	log.SetLevel(log.DebugLevel)
	log.Infoln("Golbat started")
	webhooks.StartSender()

	router := mux.NewRouter().StrictSlash(true)
	router.HandleFunc("/raw", Raw)
	addr := fmt.Sprintf(":%d", config.Config.Port)
	log.Fatal(http.ListenAndServe(addr, router)) // addr is in form :9001
}

func decode(method int, protoData *ProtoData) {
	switch pogo.Method(method) {
	case pogo.Method_METHOD_FORT_DETAILS:
		decodeFortDetails(protoData.Data)
	case pogo.Method_METHOD_GET_MAP_OBJECTS:
		decodeGMO(protoData.Data)
	case pogo.Method_METHOD_GYM_GET_INFO:
		decodeGetGymInfo(protoData.Data)
	case pogo.Method_METHOD_ENCOUNTER:
		decodeEncounter(protoData.Data)
	case pogo.Method_METHOD_FORT_SEARCH:
		decodeQuest(protoData.Data, protoData.HaveAr)
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
}

func decodeQuest(sDec []byte, haveAr *bool) {
	if haveAr == nil {
		log.Infoln("Cannot determine AR quest - ignoring")
		// We should either assume AR quest, or trace inventory like RDM probably
		return
	}
	decodedQuest := &pogo.FortSearchOutProto{}
	if err := proto.Unmarshal(sDec, decodedQuest); err != nil {
		log.Fatalln("Failed to parse", err)
		return
	}

	decoder.UpdatePokestopWithQuest(db, decodedQuest, *haveAr)

}

func decodeFortDetails(sDec []byte) {
	decodedFort := &pogo.FortDetailsOutProto{}
	if err := proto.Unmarshal(sDec, decodedFort); err != nil {
		log.Fatalln("Failed to parse", err)
		return
	}

	switch decodedFort.FortType {
	case pogo.FortType_CHECKPOINT:
		decoder.UpdatePokestopRecordWithFortDetailsOutProto(db, decodedFort)
	case pogo.FortType_GYM:
		decoder.UpdateGymRecordWithFortDetailsOutProto(db, decodedFort)
	}
}

func decodeGetGymInfo(sDec []byte) {
	decodedGymInfo := &pogo.GymGetInfoOutProto{}
	if err := proto.Unmarshal(sDec, decodedGymInfo); err != nil {
		log.Fatalln("Failed to parse", err)
		return
	}

	decoder.UpdateGymRecordWithGymInfoProto(db, decodedGymInfo)
}

func decodeEncounter(sDec []byte) {
	decodedEncounterInfo := &pogo.EncounterOutProto{}
	if err := proto.Unmarshal(sDec, decodedEncounterInfo); err != nil {
		log.Fatalln("Failed to parse", err)
		return
	}

	decoder.UpdatePokemonRecordWithEncounterProto(db, decodedEncounterInfo)
}

func decodeGMO(sDec []byte) {
	start := time.Now()

	decodedGmo := &pogo.GetMapObjectsOutProto{}

	if err := proto.Unmarshal(sDec, decodedGmo); err != nil {
		log.Fatalln("Failed to parse", err)
	}

	var newForts []decoder.RawFortData
	var newWildPokemon []decoder.RawWildPokemonData
	var newNearbyPokemon []decoder.RawNearbyPokemonData

	for _, mapCell := range decodedGmo.MapCell {
		timestampMs := uint64(mapCell.AsOfTimeMs)
		for _, fort := range mapCell.Fort {
			newForts = append(newForts, decoder.RawFortData{Cell: mapCell.S2CellId, Data: fort})
		}
		for _, mon := range mapCell.WildPokemon {
			newWildPokemon = append(newWildPokemon, decoder.RawWildPokemonData{Cell: mapCell.S2CellId, Data: mon, Timestamp: timestampMs})
		}
		for _, mon := range mapCell.NearbyPokemon {
			newNearbyPokemon = append(newNearbyPokemon, decoder.RawNearbyPokemonData{Cell: mapCell.S2CellId, Data: mon})
		}
	}

	decoder.UpdateFortBatch(db, newForts)
	decoder.UpdatePokemonBatch(db, newWildPokemon, newNearbyPokemon)

	elapsed := time.Since(start)

	log.Debugf("GMO processing took %s %d cells containing %d forts %d mon %d nearby", elapsed, len(decodedGmo.MapCell), len(newForts), len(newWildPokemon), len(newNearbyPokemon))
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

func Raw(w http.ResponseWriter, r *http.Request) {

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
				rf := v.(float64)
				res := false
				if rf > 0 {
					res = true
				}
				globalHaveAr = &res
			}
			if v := raw["uuid"]; v != nil {
				uuid = v.(string)
			}
			if v := raw["username"]; v != nil {
				account = v.(string)
			}
			if v := raw["trainerlvl"]; v != nil { // Other MITM might use
				level = int(v.(float64))
			}
			contents := raw["contents"].([]interface{}) // Other MITM
			for _, v := range contents {
				entry := v.(map[string]interface{})
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
