package main

import (
	"fmt"
	"github.com/golang-migrate/migrate/v4"
	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"
	"golbat/config"
	"golbat/decoder"
	"golbat/webhooks"
	"google.golang.org/protobuf/proto"
	"io/ioutil"
	"time"
	_ "time/tzdata"

	"github.com/gin-gonic/gin"
	"github.com/toorop/gin-logrus"

	"github.com/go-sql-driver/mysql"
	_ "github.com/golang-migrate/migrate/v4/database/mysql"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/mattn/go-sqlite3"
	"golbat/pogo"
)

var db *sqlx.DB
var inMemoryDb *sqlx.DB
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

	if config.Config.InMemory {
		// Initialise in memory db
		inMemoryDb, err = sqlx.Open("sqlite3", ":memory:")
		if err != nil {
			log.Fatal(err)
			return
		}

		inMemoryDb.SetMaxOpenConns(1)

		pingErr = inMemoryDb.Ping()
		if pingErr != nil {
			log.Fatal(pingErr)
			return
		}

		// Create database
		content, fileErr := ioutil.ReadFile("sql/sqlite/create.sql")

		if fileErr != nil {
			log.Fatal(err)
		}

		inMemoryDb.MustExec(string(content))

		dbDetails = decoder.DbDetails{
			PokemonDb:       inMemoryDb,
			UsePokemonCache: false,
			GeneralDb:       db,
		}
	} else {
		dbDetails = decoder.DbDetails{
			PokemonDb:       db,
			UsePokemonCache: true,
			GeneralDb:       db,
		}
	}

	logLevel := log.InfoLevel

	if config.Config.Logging.Debug == true {
		logLevel = log.DebugLevel
	}
	SetupLogger(logLevel)

	log.Infoln("Golbat started")
	webhooks.StartSender()

	StartStatsLogger(db)

	if config.Config.InMemory {
		StartInMemoryCleardown(inMemoryDb)
	} else {
		if config.Config.Cleanup.Pokemon == true {
			StartDatabaseArchiver(db)
		}
	}

	if config.Config.Cleanup.Incidents == true {
		StartIncidentExpiry(db)
	}

	if config.Config.Cleanup.Quests == true {
		StartQuestExpiry(db)
	}

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
	if protoData.Level < 30 {
		log.Debugf("Insufficient Level %d Did not process hook type %s", protoData.Level, pogo.Method(method))

		return
	}

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
