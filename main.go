package main

import (
	"context"
	"fmt"
	"golbat/config"
	db2 "golbat/db"
	"golbat/decoder"
	"golbat/external"
	"golbat/webhooks"
	"time"
	_ "time/tzdata"

	"github.com/golang-migrate/migrate/v4"
	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"
	ginlogrus "github.com/toorop/gin-logrus"
	"google.golang.org/protobuf/proto"

	"golbat/pogo"

	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
	_ "github.com/golang-migrate/migrate/v4/database/mysql"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

var db *sqlx.DB
var inMemoryDb *sqlx.DB
var dbDetails db2.DbDetails

func main() {
	config.ReadConfig()

	logLevel := log.InfoLevel

	// Both Sentry & Pyroscope are optional and off by default. Read more:
	// https://docs.sentry.io/platforms/go
	// https://pyroscope.io/docs/golang
	external.InitSentry()
	external.InitPyroscope()

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

	log.Infof("Opening database for processing, max pool = %d", config.Config.Database.MaxPool)

	// Get a database handle.

	db, err = sqlx.Open(driver, dbConnectionString)
	if err != nil {
		log.Fatal(err)
		return
	}

	db.SetMaxOpenConns(config.Config.Database.MaxPool)
	db.SetMaxIdleConns(10)
	db.SetConnMaxIdleTime(time.Minute)

	pingErr := db.Ping()
	if pingErr != nil {
		log.Fatal(pingErr)
		return
	}
	log.Infoln("Connected to database")

	decoder.SetKojiUrl(config.Config.Koji.Url, config.Config.Koji.BearerToken)

	//if config.Config.LegacyInMemory {
	//	// Initialise in memory db
	//	inMemoryDb, err = sqlx.Open("sqlite3", ":memory:")
	//	if err != nil {
	//		log.Fatal(err)
	//		return
	//	}
	//
	//	inMemoryDb.SetMaxOpenConns(1)
	//
	//	pingErr = inMemoryDb.Ping()
	//	if pingErr != nil {
	//		log.Fatal(pingErr)
	//		return
	//	}
	//
	//	// Create database
	//	content, fileErr := ioutil.ReadFile("sql/sqlite/create.sql")
	//
	//	if fileErr != nil {
	//		log.Fatal(err)
	//	}
	//
	//	inMemoryDb.MustExec(string(content))
	//
	//	dbDetails = db2.DbDetails{
	//		PokemonDb:       inMemoryDb,
	//		UsePokemonCache: false,
	//		GeneralDb:       db,
	//	}
	//} else {
	dbDetails = db2.DbDetails{
		PokemonDb:       db,
		UsePokemonCache: true,
		GeneralDb:       db,
	}
	//}

	decoder.InitialiseOhbem()
	decoder.LoadStatsGeofences()
	decoder.LoadNests(dbDetails)
	InitDeviceCache()

	log.Infoln("Golbat started")
	webhooks.StartSender()

	StartDbUsageStatsLogger(db)
	decoder.StartStatsWriter(db)

	if config.Config.Tuning.ExtendedTimeout {
		log.Info("Extended timeout enabled")
	}

	if config.Config.Cleanup.Pokemon == true && !config.Config.PokemonMemoryOnly {
		StartDatabaseArchiver(db)
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

	if config.Config.TestFortInMemory {
		go decoder.LoadAllPokestops(dbDetails)
		go decoder.LoadAllGyms(dbDetails)
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	if config.Config.Logging.Debug {
		r.Use(ginlogrus.Logger(log.StandardLogger()))
	} else {
		r.Use(gin.Recovery())
	}
	r.POST("/raw", Raw)
	r.GET("/health", GetHealth)

	apiGroup := r.Group("/api", AuthRequired())
	apiGroup.POST("/clear-quests", ClearQuests)
	apiGroup.POST("/quest-status", GetQuestStatus)
	apiGroup.POST("/pokestop-positions", GetPokestopPositions)
	apiGroup.GET("/pokestop/id/:fort_id", GetPokestop)
	apiGroup.POST("/reload-geojson", ReloadGeojson)
	apiGroup.GET("/reload-geojson", ReloadGeojson)
	apiGroup.POST("/reload-nests", ReloadNests)
	apiGroup.GET("/reload-nests", ReloadNests)

	apiGroup.GET("/pokemon/id/:pokemon_id", PokemonOne)
	apiGroup.GET("/pokemon/available", PokemonAvailable)
	apiGroup.POST("/pokemon/scan", PokemonScan)
	apiGroup.POST("/pokemon/search", PokemonSearch)
	apiGroup.POST("/pokemon/scan-msgpack", PokemonScanMsgPack)

	apiGroup.GET("/devices/all", GetDevices)

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
	case pogo.Method_METHOD_START_INCIDENT:
		result = decodeStartIncident(ctx, protoData.Data)
		processed = true
	case pogo.Method_METHOD_INVASION_OPEN_COMBAT_SESSION:
		if protoData.Request != nil {
			result = decodeOpenInvasion(ctx, protoData.Request, protoData.Data)
			processed = true
		}
		break
	case pogo.Method_METHOD_FORT_DETAILS:
		result = decodeFortDetails(ctx, protoData.Data)
		processed = true
	case pogo.Method_METHOD_GET_MAP_OBJECTS:
		result = decodeGMO(ctx, protoData, getScanParameters(protoData))
		processed = true
	case pogo.Method_METHOD_GYM_GET_INFO:
		result = decodeGetGymInfo(ctx, protoData.Data)
		processed = true
	case pogo.Method_METHOD_ENCOUNTER:
		if getScanParameters(protoData).ProcessPokemon {
			result = decodeEncounter(ctx, protoData.Data, protoData.Account)
		}
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
			processed = true
		}
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

func getScanParameters(protoData *ProtoData) decoder.ScanParameters {
	return decoder.FindScanConfiguration(protoData.ScanContext, protoData.Lat, protoData.Lon)
}

func decodeQuest(ctx context.Context, sDec []byte, haveAr *bool) string {
	if haveAr == nil {
		log.Infoln("Cannot determine AR quest - ignoring")
		// We should either assume AR quest, or trace inventory like RDM probably
		return "No AR quest info"
	}
	decodedQuest := &pogo.FortSearchOutProto{}
	if err := proto.Unmarshal(sDec, decodedQuest); err != nil {
		log.Errorf("Failed to parse %s", err)
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
		log.Errorf("Failed to parse %s", err)
		return fmt.Sprintf("Failed to parse %s", err)
	}

	var proxyResponseProto pogo.ProxyResponseProto

	if err := proto.Unmarshal(payload, &proxyResponseProto); err != nil {
		log.Errorf("Failed to parse %s", err)
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

	return fmt.Sprintf("Did not process %s", pogo.SocialAction(proxyRequestProto.GetAction()).String())
}

func decodeGetFriendDetails(payload []byte) string {
	var getFriendDetailsOutProto pogo.GetFriendDetailsOutProto
	getFriendDetailsError := proto.Unmarshal(payload, &getFriendDetailsOutProto)

	if getFriendDetailsError != nil {
		log.Errorf("Failed to parse %s", getFriendDetailsError)
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
		log.Errorf("Failed to parse %s", searchPlayerOutError)
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

func decodeFortDetails(ctx context.Context, sDec []byte) string {
	decodedFort := &pogo.FortDetailsOutProto{}
	if err := proto.Unmarshal(sDec, decodedFort); err != nil {
		log.Errorf("Failed to parse %s", err)
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
		log.Errorf("Failed to parse %s", err)
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
		log.Errorf("Failed to parse %s", err)
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
		log.Errorf("Failed to parse %s", err)
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
		log.Errorf("Failed to parse %s", err)
		return fmt.Sprintf("Failed to parse %s", err)
	}

	if decodedEncounterInfo.Result != pogo.DiskEncounterOutProto_SUCCESS {
		res := fmt.Sprintf(`DiskEncounterOutProto: Ignored non-success value %d:%s`, decodedEncounterInfo.Result,
			pogo.DiskEncounterOutProto_Result_name[int32(decodedEncounterInfo.Result)])
		return res
	}

	return decoder.UpdatePokemonRecordWithDiskEncounterProto(ctx, dbDetails, decodedEncounterInfo)
}

func decodeStartIncident(ctx context.Context, sDec []byte) string {
	decodedIncident := &pogo.StartIncidentOutProto{}
	if err := proto.Unmarshal(sDec, decodedIncident); err != nil {
		log.Errorf("Failed to parse %s", err)
		return fmt.Sprintf("Failed to parse %s", err)
	}

	if decodedIncident.Status != pogo.StartIncidentOutProto_SUCCESS {
		res := fmt.Sprintf(`GiovanniOutProto: Ignored non-success value %d:%s`, decodedIncident.Status,
			pogo.StartIncidentOutProto_Status_name[int32(decodedIncident.Status)])
		return res
	}

	return decoder.ConfirmIncident(ctx, dbDetails, decodedIncident)
}

func decodeOpenInvasion(ctx context.Context, request []byte, payload []byte) string {
	decodeOpenInvasionRequest := &pogo.OpenInvasionCombatSessionProto{}

	if err := proto.Unmarshal(request, decodeOpenInvasionRequest); err != nil {
		log.Errorf("Failed to parse %s", err)
		return fmt.Sprintf("Failed to parse %s", err)
	}
	if decodeOpenInvasionRequest.IncidentLookup == nil {
		return "Invalid OpenInvasionCombatSessionProto received"
	}

	decodedOpenInvasionResponse := &pogo.OpenInvasionCombatSessionOutProto{}
	if err := proto.Unmarshal(payload, decodedOpenInvasionResponse); err != nil {
		log.Errorf("Failed to parse %s", err)
		return fmt.Sprintf("Failed to parse %s", err)
	}

	if decodedOpenInvasionResponse.Status != pogo.InvasionStatus_SUCCESS {
		res := fmt.Sprintf(`InvasionLineupOutProto: Ignored non-success value %d:%s`, decodedOpenInvasionResponse.Status,
			pogo.InvasionStatus_Status_name[int32(decodedOpenInvasionResponse.Status)])
		return res
	}

	return decoder.UpdateIncidentLineup(ctx, dbDetails, decodeOpenInvasionRequest, decodedOpenInvasionResponse)
}

func decodeGMO(ctx context.Context, protoData *ProtoData, scanParameters decoder.ScanParameters) string {
	decodedGmo := &pogo.GetMapObjectsOutProto{}

	if err := proto.Unmarshal(protoData.Data, decodedGmo); err != nil {
		log.Errorf("Failed to parse %s", err)
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
	var newMapCells []uint64

	for _, mapCell := range decodedGmo.MapCell {
		timestampMs := uint64(mapCell.AsOfTimeMs)
		for _, fort := range mapCell.Fort {
			newForts = append(newForts, decoder.RawFortData{Cell: mapCell.S2CellId, Data: fort})

			if fort.ActivePokemon != nil {
				newMapPokemon = append(newMapPokemon, decoder.RawMapPokemonData{Cell: mapCell.S2CellId, Data: fort.ActivePokemon})
			}
		}
		newMapCells = append(newMapCells, mapCell.S2CellId)
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

	if scanParameters.ProcessGyms || scanParameters.ProcessPokestops {
		decoder.UpdateFortBatch(ctx, dbDetails, scanParameters, newForts)
	}
	if scanParameters.ProcessPokemon {
		decoder.UpdatePokemonBatch(ctx, dbDetails, scanParameters, newWildPokemon, newNearbyPokemon, newMapPokemon, protoData.Account)
	}
	if scanParameters.ProcessWeather {
		decoder.UpdateClientWeatherBatch(ctx, dbDetails, newClientWeather)
	}
	if scanParameters.ProcessCells {
		decoder.UpdateClientMapS2CellBatch(ctx, dbDetails, newMapCells)
		if scanParameters.ProcessGyms || scanParameters.ProcessPokestops {
			if !(len(newMapPokemon) == 0 && len(newNearbyPokemon) == 0 && len(newForts) == 0) {
				decoder.ClearRemovedForts(ctx, dbDetails, newMapCells)
			}
		}
	}
	return fmt.Sprintf("%d cells containing %d forts %d mon %d nearby", len(decodedGmo.MapCell), len(newForts), len(newWildPokemon), len(newNearbyPokemon))
}
