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

	"golbat/forts"
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
		User:   config.Config.Database.User,     //"root",     //os.Getenv("DBUSER"),
		Passwd: config.Config.Database.Password, //"transmit", //os.Getenv("DBPASS"),
		Net:    "tcp",
		Addr:   config.Config.Database.Addr,
		DBName: config.Config.Database.Db,
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

func decode(method int, sDec []byte) {
	switch pogo.Method(method) {
	case pogo.Method_METHOD_FORT_DETAILS:
		decodeFortDetails(sDec)
	case pogo.Method_METHOD_GET_MAP_OBJECTS:
		decodeGMO(sDec)
	case pogo.Method_METHOD_GYM_GET_INFO:
		decodeGetGymInfo(sDec)
	case pogo.Method_METHOD_ENCOUNTER:
		decodeEncounter(sDec)
	case pogo.Method_METHOD_GET_PLAYER:
		break
	case pogo.Method_METHOD_GET_HOLOHOLO_INVENTORY:
		break
	default:
		log.Debugf("Did not process hook type %s", pogo.Method(method))
	}
}

func decodeFortDetails(sDec []byte) {
	decodedFort := &pogo.FortDetailsOutProto{}
	if err := proto.Unmarshal(sDec, decodedFort); err != nil {
		log.Fatalln("Failed to parse", err)
		return
	}

	switch decodedFort.FortType {
	case pogo.FortType_CHECKPOINT:
		forts.UpdatePokestopRecordWithFortDetailsOutProto(db, decodedFort)
	case pogo.FortType_GYM:
		forts.UpdateGymRecordWithFortDetailsOutProto(db, decodedFort)
	}
}

func decodeGetGymInfo(sDec []byte) {
	decodedGymInfo := &pogo.GymGetInfoOutProto{}
	if err := proto.Unmarshal(sDec, decodedGymInfo); err != nil {
		log.Fatalln("Failed to parse", err)
		return
	}

	forts.UpdateGymRecordWithGymInfoProto(db, decodedGymInfo)
}

func decodeEncounter(sDec []byte) {
	decodedEncounterInfo := &pogo.EncounterOutProto{}
	if err := proto.Unmarshal(sDec, decodedEncounterInfo); err != nil {
		log.Fatalln("Failed to parse", err)
		return
	}

	forts.UpdatePokemonRecordWithEncounterProto(db, decodedEncounterInfo)
}

func decodeGMO(sDec []byte) {
	start := time.Now()

	decodedGmo := &pogo.GetMapObjectsOutProto{}

	if err := proto.Unmarshal(sDec, decodedGmo); err != nil {
		log.Fatalln("Failed to parse", err)
	}

	var newForts []forts.RawFortData
	var newWildPokemon []forts.RawWildPokemonData
	var newNearbyPokemon []forts.RawNearbyPokemonData

	for _, mapCell := range decodedGmo.MapCell {
		timestampMs := uint64(mapCell.AsOfTimeMs)
		for _, fort := range mapCell.Fort {
			newForts = append(newForts, forts.RawFortData{Cell: mapCell.S2CellId, Data: fort})
		}
		for _, mon := range mapCell.WildPokemon {
			newWildPokemon = append(newWildPokemon, forts.RawWildPokemonData{Cell: mapCell.S2CellId, Data: mon, Timestamp: timestampMs})
		}
		for _, mon := range mapCell.NearbyPokemon {
			newNearbyPokemon = append(newNearbyPokemon, forts.RawNearbyPokemonData{Cell: mapCell.S2CellId, Data: mon})
		}
	}

	forts.UpdateFortBatch(db, newForts)
	forts.UpdatePokemonBatch(db, newWildPokemon, newNearbyPokemon)

	elapsed := time.Since(start)

	log.Debugf("GMO processing took %s %d cells containing %d forts %d mon %d nearby", elapsed, len(decodedGmo.MapCell), len(newForts), len(newWildPokemon), len(newNearbyPokemon))
}

func Raw(w http.ResponseWriter, r *http.Request) {

	body, err := ioutil.ReadAll(io.LimitReader(r.Body, 1048576))
	if err != nil {
		panic(err)
	}
	if err := r.Body.Close(); err != nil {
		panic(err)
	}
	var raw []map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		w.WriteHeader(422) // unprocessable entity
		if err := json.NewEncoder(w).Encode(err); err != nil {
			panic(err)
		}
	}

	for _, entry := range raw {
		method := int(entry["type"].(float64))
		payload := entry["payload"].(string)

		sDec, _ := b64.StdEncoding.DecodeString(payload)

		go decode(method, sDec)
	}
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(http.StatusCreated)
	//if err := json.NewEncoder(w).Encode(t); err != nil {
	//	panic(err)
	//}
}
