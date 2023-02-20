package main

import (
	"context"
	"fmt"
	"github.com/golang-migrate/migrate/v4"
	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"
	ginlogrus "github.com/toorop/gin-logrus"
	"golbat/config"
	db2 "golbat/db"
	"golbat/decoder"
	"golbat/webhooks"
	"google.golang.org/protobuf/proto"
	"io/ioutil"
	"time"
	_ "time/tzdata"

	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
	_ "github.com/golang-migrate/migrate/v4/database/mysql"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/mattn/go-sqlite3"
	"golbat/pogo"
)

var db *sqlx.DB
var inMemoryDb *sqlx.DB
var dbDetails db2.DbDetails

func main() {
	config.ReadConfig()

	logLevel := log.InfoLevel

	if config.Config.Logging.Debug == true {
		logLevel = log.DebugLevel
	}
	SetupLogger(logLevel, config.Config.Logging.SaveLogs)

	log.Infof("Golbat starting")

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

	log.Infof("Starting migration")

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

	log.Infof("Opening database for processing")

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
		//sql.Register("sqlite3_settings",
		//	&sqlite3.SQLiteDriver{
		//		ConnectHook: func(conn *sqlite3.SQLiteConn) error {
		//			conn.SetLimit(sqlite3.SQLITE_LIMIT_EXPR_DEPTH, 50000)
		//			return nil
		//		},
		//	})
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

		dbDetails = db2.DbDetails{
			PokemonDb:       inMemoryDb,
			UsePokemonCache: false,
			GeneralDb:       db,
		}
	} else {
		dbDetails = db2.DbDetails{
			PokemonDb:       db,
			UsePokemonCache: true,
			GeneralDb:       db,
		}
	}

	decoder.InitialiseOhbem()

	log.Infoln("Golbat started")
	webhooks.StartSender()

	StartDbUsageStatsLogger(db)
	decoder.StartStatsWriter(db)

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

	if config.Config.Cleanup.Stats == true {
		StartStatsExpiry(db)
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	if config.Config.Logging.Debug {
		r.Use(ginlogrus.Logger(log.StandardLogger()))
	} else {
		r.Use(gin.Recovery())
	}
	r.POST("/raw", Raw)
	r.POST("/api/clearQuests", ClearQuests)
	r.POST("/api/reloadGeojson", ReloadGeojson)
	r.GET("/api/reloadGeojson", ReloadGeojson)
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

func decode(ctx context.Context, method int, protoData *ProtoData) {
	if method != int(pogo.ClientAction_CLIENT_ACTION_PROXY_SOCIAL_ACTION) && protoData.Level < 30 {
		log.Debugf("Insufficient Level %d Did not process hook type %s", protoData.Level, pogo.Method(method))

		return
	}

	processed := false
	start := time.Now()
	result := ""

	switch pogo.Method(method) {
	case pogo.Method_METHOD_FORT_DETAILS:
		result = decodeFortDetails(ctx, protoData.Data)
		processed = true
	case pogo.Method_METHOD_GET_MAP_OBJECTS:
		result = decodeGMO(ctx, protoData.Data, protoData.Account)
		processed = true
	case pogo.Method_METHOD_GYM_GET_INFO:
		result = decodeGetGymInfo(ctx, protoData.Data)
		processed = true
	case pogo.Method_METHOD_ENCOUNTER:
		result = decodeEncounter(ctx, protoData.Data, protoData.Account)
		processed = true
	case pogo.Method_METHOD_DISK_ENCOUNTER:
		result = decodeDiskEncounter(ctx, protoData.Data)
		processed = true
	case pogo.Method_METHOD_FORT_SEARCH:
		result = decodeQuest(ctx, protoData.Data, protoData.HaveAr)
		processed = true
	case pogo.Method_METHOD_GET_PLAYER:
		break
	case pogo.Method_METHOD_GET_HOLOHOLO_INVENTORY:
		break
	case pogo.Method_METHOD_CREATE_COMBAT_CHALLENGE:
		// ignore
		break
	case pogo.Method(pogo.ClientAction_CLIENT_ACTION_PROXY_SOCIAL_ACTION):
		if protoData.Request != nil {
			result = decodeSocialActionWithRequest(protoData.Request, protoData.Data)
		} else {
			result = decodeSocialActionProxy(protoData.Data)
		}
		processed = true
		break
	case pogo.Method_METHOD_GET_MAP_FORTS:
		result = decodeGetMapForts(ctx, protoData.Data)
		processed = true
	default:
		log.Debugf("Did not process hook type %s", pogo.Method(method))
	}

	if processed == true {
		elapsed := time.Since(start)

		log.Debugf("%s/%s %s - %s - %s", protoData.Uuid, protoData.Account, pogo.Method(method), elapsed, result)
	}
}

func decodeQuest(ctx context.Context, sDec []byte, haveAr *bool) string {
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

	return decoder.UpdatePokestopWithQuest(ctx, dbDetails, decodedQuest, *haveAr)

}

func decodeSocialActionWithRequest(request []byte, payload []byte) string {
	var proxyRequestProto pogo.ProxyRequestProto

	if err := proto.Unmarshal(request, &proxyRequestProto); err != nil {
		log.Fatalln("Failed to parse %s", err)
		return fmt.Sprintf("Failed to parse %s", err)
	}

	var proxyResponseProto pogo.ProxyResponseProto

	if err := proto.Unmarshal(payload, &proxyResponseProto); err != nil {
		log.Fatalln("Failed to parse %s", err)
		return fmt.Sprintf("Failed to parse %s", err)
	}

	if proxyResponseProto.Status != pogo.ProxyResponseProto_COMPLETED && proxyResponseProto.Status != pogo.ProxyResponseProto_COMPLETED_AND_REASSIGNED {
		return fmt.Sprintf("unsuccessful proxyResponseProto response %d %s", int(proxyResponseProto.Status), proxyResponseProto.Status)
	}

	switch pogo.SocialAction(proxyRequestProto.GetAction()) {
	case pogo.SocialAction_SOCIAL_ACTION_LIST_FRIEND_STATUS:
		return decodeGetFriendDetails(proxyResponseProto.Payload)
	case pogo.SocialAction_SOCIAL_ACTION_SEARCH_PLAYER:
		return decodeSearchPlayer(proxyRequestProto, proxyResponseProto.Payload)

	}

	return fmt.Sprintf("Can't parse %s", pogo.SocialAction(proxyRequestProto.GetAction()).String())
}

func decodeGetFriendDetails(payload []byte) string {
	var getFriendDetailsOutProto pogo.GetFriendDetailsOutProto
	getFriendDetailsError := proto.Unmarshal(payload, &getFriendDetailsOutProto)

	if getFriendDetailsError != nil {
		log.Fatalln("Failed to parse %s", getFriendDetailsError)
		return fmt.Sprintf("Failed to parse %s", getFriendDetailsError)
	}

	if getFriendDetailsOutProto.GetResult() != pogo.GetFriendDetailsOutProto_SUCCESS || getFriendDetailsOutProto.GetFriend() == nil {
		return fmt.Sprintf("unsuccessful get friends details")
	}

	failures := 0

	for _, friend := range getFriendDetailsOutProto.GetFriend() {
		player := friend.GetPlayer()
		publicData, publicDataErr := decodePlayerPublicProfile(player.GetPublicData())

		if publicDataErr != nil {
			failures++
			continue
		}

		updatePlayerError := decoder.UpdatePlayerRecordWithPlayerSummary(dbDetails, player, publicData, "", player.GetPlayerId())
		if updatePlayerError != nil {
			failures++
		}
	}

	return fmt.Sprintf("%d players decoded on %d", len(getFriendDetailsOutProto.GetFriend())-failures, len(getFriendDetailsOutProto.GetFriend()))
}

func decodeSearchPlayer(proxyRequestProto pogo.ProxyRequestProto, payload []byte) string {
	var searchPlayerOutProto pogo.SearchPlayerOutProto
	searchPlayerOutError := proto.Unmarshal(payload, &searchPlayerOutProto)

	if searchPlayerOutError != nil {
		log.Fatalln("Failed to parse %s", searchPlayerOutError)
		return fmt.Sprintf("Failed to parse %s", searchPlayerOutError)
	}

	if searchPlayerOutProto.GetResult() != pogo.SearchPlayerOutProto_SUCCESS || searchPlayerOutProto.GetPlayer() == nil {
		return fmt.Sprintf("unsuccessful search player response")
	}

	var searchPlayerProto pogo.SearchPlayerProto
	searchPlayerError := proto.Unmarshal(proxyRequestProto.GetPayload(), &searchPlayerProto)

	if searchPlayerError != nil || searchPlayerProto.GetFriendCode() == "" {
		return fmt.Sprintf("Failed to parse %s", searchPlayerError)
	}

	player := searchPlayerOutProto.GetPlayer()
	publicData, publicDataError := decodePlayerPublicProfile(player.GetPublicData())

	if publicDataError != nil {
		return fmt.Sprintf("Failed to parse %s", publicDataError)
	}

	updatePlayerError := decoder.UpdatePlayerRecordWithPlayerSummary(dbDetails, player, publicData, searchPlayerProto.GetFriendCode(), "")
	if updatePlayerError != nil {
		return fmt.Sprintf("Failed update player %s", updatePlayerError)
	}

	return fmt.Sprintf("1 player decoded from SearchPlayerProto")
}

func decodePlayerPublicProfile(publicProfile []byte) (*pogo.PlayerPublicProfileProto, error) {
	var publicData pogo.PlayerPublicProfileProto
	publicDataErr := proto.Unmarshal(publicProfile, &publicData)

	return &publicData, publicDataErr
}

func decodeSocialActionProxy(sDec []byte) string {
	var proxy pogo.ProxyResponseProto

	if err := proto.Unmarshal(sDec, &proxy); err != nil {
		log.Fatalln("Failed to parse %s", err)
		return fmt.Sprintf("Failed to parse %s", err)
	}

	if proxy.Status != pogo.ProxyResponseProto_COMPLETED && proxy.Status != pogo.ProxyResponseProto_COMPLETED_AND_REASSIGNED {
		return fmt.Sprintf("unsuccessful proxy response %d %s", int(proxy.Status), proxy.Status)
	}

	players := make([]*pogo.PlayerSummaryProto, 0)

	// for now, we handle both those protos
	// but we don't know which one we received...
	// so we parse both, and continue only if one pass without issue
	var searchPlayerOutProto pogo.SearchPlayerOutProto
	var getFriendDetailsOutProto pogo.GetFriendDetailsOutProto

	searchPlayerError := proto.Unmarshal(proxy.Payload, &searchPlayerOutProto)
	getFriendDetailsError := proto.Unmarshal(proxy.Payload, &getFriendDetailsOutProto)

	if searchPlayerError == nil && getFriendDetailsError == nil {
		return fmt.Sprintf("Could not determine which social proto received")
	} else if searchPlayerError == nil {
		if searchPlayerOutProto.GetResult() != pogo.SearchPlayerOutProto_SUCCESS || searchPlayerOutProto.GetPlayer() == nil {
			return fmt.Sprintf("unsuccessful search player response")
		}

		players = append(players, searchPlayerOutProto.GetPlayer())
	} else if getFriendDetailsError == nil {
		if getFriendDetailsOutProto.GetResult() != pogo.GetFriendDetailsOutProto_SUCCESS || getFriendDetailsOutProto.GetFriend() == nil {
			return fmt.Sprintf("unsuccessful get friends details")
		}

		for _, friend := range getFriendDetailsOutProto.GetFriend() {
			players = append(players, friend.GetPlayer())
		}
	} else {
		return fmt.Sprintf("Failed to parse social proto")
	}

	failures := 0

	for _, player := range players {
		var publicData pogo.PlayerPublicProfileProto
		publicDataErr := proto.Unmarshal(player.GetPublicData(), &publicData)

		if publicDataErr != nil {
			failures++
			continue
		}

		updatePlayerError := decoder.UpdatePlayerRecordWithPlayerSummary(dbDetails, player, &publicData, "", "")
		if updatePlayerError != nil {
			failures++
		}
	}

	return fmt.Sprintf("%d players decoded on %d", len(players)-failures, len(players))
}

func decodeFortDetails(ctx context.Context, sDec []byte) string {
	decodedFort := &pogo.FortDetailsOutProto{}
	if err := proto.Unmarshal(sDec, decodedFort); err != nil {
		log.Fatalln("Failed to parse", err)
		return fmt.Sprintf("Failed to parse %s", err)
	}

	switch decodedFort.FortType {
	case pogo.FortType_CHECKPOINT:
		return decoder.UpdatePokestopRecordWithFortDetailsOutProto(ctx, dbDetails, decodedFort)
	case pogo.FortType_GYM:
		return decoder.UpdateGymRecordWithFortDetailsOutProto(ctx, dbDetails, decodedFort)
	}
	return "Unknown fort type"
}

func decodeGetMapForts(ctx context.Context, sDec []byte) string {
	decodedMapForts := &pogo.GetMapFortsOutProto{}
	if err := proto.Unmarshal(sDec, decodedMapForts); err != nil {
		log.Fatalln("Failed to parse", err)
		return fmt.Sprintf("Failed to parse %s", err)
	}

	if decodedMapForts.Status != pogo.GetMapFortsOutProto_SUCCESS {
		res := fmt.Sprintf(`GetMapFortsOutProto: Ignored non-success value %d:%s`, decodedMapForts.Status,
			pogo.GetMapFortsOutProto_Status_name[int32(decodedMapForts.Status)])
		return res
	}

	var outputString string
	processedForts := 0

	for _, fort := range decodedMapForts.Fort {
		status, output := decoder.UpdateFortRecordWithGetMapFortsOutProto(ctx, dbDetails, fort)
		if status {
			processedForts += 1
			outputString += output + ", "
		}
	}

	if processedForts > 0 {
		return fmt.Sprintf("Updated %d forts: %s", processedForts, outputString)
	}
	return "No forts updated"
}

func decodeGetGymInfo(ctx context.Context, sDec []byte) string {
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
	return decoder.UpdateGymRecordWithGymInfoProto(ctx, dbDetails, decodedGymInfo)
}

func decodeEncounter(ctx context.Context, sDec []byte, username string) string {
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
	return decoder.UpdatePokemonRecordWithEncounterProto(ctx, dbDetails, decodedEncounterInfo, username)
}

func decodeDiskEncounter(ctx context.Context, sDec []byte) string {
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

	return decoder.UpdatePokemonRecordWithDiskEncounterProto(ctx, dbDetails, decodedEncounterInfo)
}

func decodeGMO(ctx context.Context, sDec []byte, username string) string {
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
	var newClientWeather []decoder.RawClientWeatherData
	var newClientMapCellS2CellIds []uint64
	var gymIdsPerCell = make(map[uint64][]string)
	var stopIdsPerCell = make(map[uint64][]string)

	for _, mapCell := range decodedGmo.MapCell {
		timestampMs := uint64(mapCell.AsOfTimeMs)
		for _, fort := range mapCell.Fort {
			newForts = append(newForts, decoder.RawFortData{Cell: mapCell.S2CellId, Data: fort})

			if fort.ActivePokemon != nil {
				newMapPokemon = append(newMapPokemon, decoder.RawMapPokemonData{Cell: mapCell.S2CellId, Data: fort.ActivePokemon})
			}
			// collect fort information of cell
			switch fort.FortType {
			case pogo.FortType_CHECKPOINT:
				stopIdsPerCell[mapCell.S2CellId] = append(stopIdsPerCell[mapCell.S2CellId], fort.FortId)
			case pogo.FortType_GYM:
				gymIdsPerCell[mapCell.S2CellId] = append(gymIdsPerCell[mapCell.S2CellId], fort.FortId)
			}
		}
		newClientMapCellS2CellIds = append(newClientMapCellS2CellIds, mapCell.S2CellId)
		for _, mon := range mapCell.WildPokemon {
			newWildPokemon = append(newWildPokemon, decoder.RawWildPokemonData{Cell: mapCell.S2CellId, Data: mon, Timestamp: timestampMs})
		}
		for _, mon := range mapCell.NearbyPokemon {
			newNearbyPokemon = append(newNearbyPokemon, decoder.RawNearbyPokemonData{Cell: mapCell.S2CellId, Data: mon})
		}
	}
	for _, clientWeather := range decodedGmo.ClientWeather {
		newClientWeather = append(newClientWeather, decoder.RawClientWeatherData{Cell: clientWeather.S2CellId, Data: clientWeather})
	}

	decoder.UpdateFortBatch(ctx, dbDetails, newForts)
	decoder.UpdatePokemonBatch(ctx, dbDetails, newWildPokemon, newNearbyPokemon, newMapPokemon, username)
	decoder.UpdateClientWeatherBatch(ctx, dbDetails, newClientWeather)
	decoder.UpdateClientMapS2CellBatch(ctx, dbDetails, newClientMapCellS2CellIds)

	decoder.ClearRemovedForts(ctx, dbDetails, gymIdsPerCell, stopIdsPerCell)

	return fmt.Sprintf("%d cells containing %d forts %d mon %d nearby", len(decodedGmo.MapCell), len(newForts), len(newWildPokemon), len(newNearbyPokemon))
}
