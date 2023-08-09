package main

import (
	"context"
	"fmt"
	"golbat/config"
	db2 "golbat/db"
	"golbat/decoder"
	"golbat/external"
	pb "golbat/grpc"
	"golbat/webhooks"
	"google.golang.org/grpc"
	"net"
	"strings"
	"net/http"
	"sync"
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
var dbDetails db2.DbDetails

func main() {
	var wg sync.WaitGroup
	ctx, cancelFn := context.WithCancel(context.Background())
	defer cancelFn()

	wg.Add(1)
	go func() {
		defer wg.Done()
		watchForShutdown(ctx, cancelFn)
	}()

	cfg, err := config.ReadConfig()
	if err != nil {
		panic(err)
	}

	logLevel := log.InfoLevel

	// Both Sentry & Pyroscope are optional and off by default. Read more:
	// https://docs.sentry.io/platforms/go
	// https://pyroscope.io/docs/golang
	external.InitSentry()
	external.InitPyroscope()

	if cfg.Logging.Debug == true {
		logLevel = log.DebugLevel
	}
	SetupLogger(
		logLevel,
		cfg.Logging.SaveLogs,
		cfg.Logging.MaxSize,
		cfg.Logging.MaxAge,
		cfg.Logging.MaxBackups,
		cfg.Logging.Compress,
	)

	webhooksSender, err := webhooks.NewWebhooksSender(cfg)
	if err != nil {
		log.Fatalf("failed to setup webhooks sender: %s", err)
	}
	decoder.SetWebhooksSender(webhooksSender)

	log.Infof("Golbat starting")

	// Capture connection properties.
	mysqlConfig := mysql.Config{
		User:                 cfg.Database.User,     //"root",     //os.Getenv("DBUSER"),
		Passwd:               cfg.Database.Password, //"transmit", //os.Getenv("DBPASS"),
		Net:                  "tcp",
		Addr:                 cfg.Database.Addr,
		DBName:               cfg.Database.Db,
		AllowNativePasswords: true,
	}

	dbConnectionString := mysqlConfig.FormatDSN()
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

	log.Infof("Opening database for processing, max pool = %d", cfg.Database.MaxPool)

	// Get a database handle.

	db, err = sqlx.Open(driver, dbConnectionString)
	if err != nil {
		log.Fatal(err)
		return
	}

	db.SetConnMaxLifetime(time.Minute * 3) // Recommended by go mysql driver
	db.SetMaxOpenConns(cfg.Database.MaxPool)
	db.SetMaxIdleConns(10)
	db.SetConnMaxIdleTime(time.Minute)

	pingErr := db.Ping()
	if pingErr != nil {
		log.Fatal(pingErr)
		return
	}
	log.Infoln("Connected to database")

	decoder.SetKojiUrl(cfg.Koji.Url, cfg.Koji.BearerToken)

	//if cfg.LegacyInMemory {
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

	wg.Add(1)
	go func() {
		defer cancelFn()
		defer wg.Done()

		err := webhooksSender.Run(ctx)
		if err != nil {
			log.Errorf("failed to start webhooks sender: %s", err)
		}
	}()

	log.Infoln("Golbat started")

	StartDbUsageStatsLogger(db)
	decoder.StartStatsWriter(db)

	if cfg.Tuning.ExtendedTimeout {
		log.Info("Extended timeout enabled")
	}

	if cfg.Cleanup.Pokemon == true && !cfg.PokemonMemoryOnly {
		StartDatabaseArchiver(db)
	}

	if cfg.Cleanup.Incidents == true {
		StartIncidentExpiry(db)
	}

	if cfg.Cleanup.Quests == true {
		StartQuestExpiry(db)
	}

	if cfg.Cleanup.Stats == true {
		StartStatsExpiry(db)
	}

	if cfg.TestFortInMemory {
		go decoder.LoadAllPokestops(dbDetails)
		go decoder.LoadAllGyms(dbDetails)
	}

	// Start the GRPC receiver

	if cfg.GrpcPort > 0 {
		log.Infof("Starting GRPC server on port %d", cfg.GrpcPort)
		go func() {
			lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.GrpcPort))
			if err != nil {
				log.Fatalf("failed to listen: %v", err)
			}
			s := grpc.NewServer()
			pb.RegisterRawProtoServer(s, &grpcRawServer{})
			pb.RegisterPokemonServer(s, &grpcPokemonServer{})
			log.Printf("grpc server listening at %v", lis.Addr())
			if err := s.Serve(lis); err != nil {
				log.Fatalf("failed to serve: %v", err)
			}
		}()
	}

	// Start the web server.
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	external.InitPrometheus(r) // init prometheus if enabled

	if cfg.Logging.Debug {
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
	apiGroup.POST("/pokemon/v2/scan", PokemonScan2)
	apiGroup.POST("/pokemon/search", PokemonSearch)

	apiGroup.GET("/devices/all", GetDevices)

	//router := mux.NewRouter().StrictSlash(true)
	//router.HandleFunc("/raw", Raw)

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: r,
	}

	wg.Add(1)
	go func() {
		defer cancelFn()
		defer wg.Done()

		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Errorf("Failed to listen and start http server: %s", err)
		}
	}()

	// wait for shutdown to be signaled in some way. This can be from a failure
	// to start the webhook sender, failure to start the http server, and/or
	// watchForShutdown() saying it is time to shutdown. (watchForShutdown() on unix
	// waits for a SIGINT or SIGTERM)
	<-ctx.Done()

	log.Info("Starting shutdown...")

	// So now we attempt to shutdown the http server, telling it to wait for open requests to
	// finish for 5 seconds before just pulling the plug.
	shutdownCtx, shutdownCancelFn := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancelFn()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		if err == context.DeadlineExceeded {
			log.Warn("Graceful shutdown timed out, exiting.")
		} else {
			log.Errorf("Error during http server shutdown: %s", err)
		}
	}

	// wait for other started goroutines to cleanup and exit before we flush the
	// webhooks and exit the program.
	log.Info("http server is shutdown, waiting for other go routines to exit...")
	wg.Wait()

	log.Info("go routines have exited, flushing webhooks now...")
	webhooksSender.Flush()

	log.Info("Golbat exiting!")
}

func decode(ctx context.Context, method int, protoData *ProtoData) {
	getMethodName := func(method int, trimString bool) string {
		if val, ok := pogo.Method_name[int32(method)]; ok {
			if trimString && strings.HasPrefix(val, "METHOD_") {
				return strings.TrimPrefix(val, "METHOD_")
			}
			return val
		}
		return fmt.Sprintf("#%d", method)
	}

	if method != int(pogo.ClientAction_CLIENT_ACTION_PROXY_SOCIAL_ACTION) && protoData.Level < 30 {
		external.DecodeMethods.WithLabelValues("error", "low_level", getMethodName(method, true)).Inc()
		log.Debugf("Insufficient Level %d Did not process hook type %s", protoData.Level, pogo.Method(method))
		return
	}

	processed := false
	ignore := false
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
		ignore = true
		break
	case pogo.Method_METHOD_GET_HOLOHOLO_INVENTORY:
		ignore = true
		break
	case pogo.Method_METHOD_CREATE_COMBAT_CHALLENGE:
		ignore = true
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
	case pogo.Method_METHOD_GET_ROUTES:
		result = decodeGetRoutes(protoData.Data)
		processed = true
	case pogo.Method_METHOD_GET_CONTEST_DATA:
		// Request helps, but can be decoded without it
		result = decodeGetContestData(ctx, protoData.Request, protoData.Data)
		processed = true
		break
	case pogo.Method_METHOD_GET_POKEMON_SIZE_CONTEST_ENTRY:
		// Request is essential to decode this
		if protoData.Request != nil {
			result = decodeGetPokemonSizeContestEntry(ctx, protoData.Request, protoData.Data)
			processed = true
		}
		break
	default:
		log.Debugf("Did not know hook type %s", pogo.Method(method))
	}
	if !ignore {
		elapsed := time.Since(start)
		if processed == true {
			external.DecodeMethods.WithLabelValues("ok", "", getMethodName(method, true)).Inc()
			log.Debugf("%s/%s %s - %s - %s", protoData.Uuid, protoData.Account, pogo.Method(method), elapsed, result)
		} else {
			log.Debugf("%s/%s %s - %s - %s", protoData.Uuid, protoData.Account, pogo.Method(method), elapsed, "**Did not process**")
			external.DecodeMethods.WithLabelValues("unprocessed", "", getMethodName(method, true)).Inc()
		}
	}
}

func getScanParameters(protoData *ProtoData) decoder.ScanParameters {
	return decoder.FindScanConfiguration(protoData.ScanContext, protoData.Lat, protoData.Lon)
}

func decodeQuest(ctx context.Context, sDec []byte, haveAr *bool) string {
	if haveAr == nil {
		external.DecodeQuest.WithLabelValues("error", "missing_ar_info").Inc()
		log.Infoln("Cannot determine AR quest - ignoring")
		// We should either assume AR quest, or trace inventory like RDM probably
		return "No AR quest info"
	}
	decodedQuest := &pogo.FortSearchOutProto{}
	if err := proto.Unmarshal(sDec, decodedQuest); err != nil {
		log.Errorf("Failed to parse %s", err)
		external.DecodeQuest.WithLabelValues("error", "parse").Inc()
		return "Parse failure"
	}

	if decodedQuest.Result != pogo.FortSearchOutProto_SUCCESS {
		external.DecodeQuest.WithLabelValues("error", "non_success").Inc()
		res := fmt.Sprintf(`GymGetInfoOutProto: Ignored non-success value %d:%s`, decodedQuest.Result,
			pogo.FortSearchOutProto_Result_name[int32(decodedQuest.Result)])
		return res
	}

	haveArStr := "NoAR"
	if *haveAr {
		haveArStr = "AR"
	}

	external.DecodeQuest.WithLabelValues("ok", haveArStr).Inc()
	return decoder.UpdatePokestopWithQuest(ctx, dbDetails, decodedQuest, *haveAr)

}

func decodeSocialActionWithRequest(request []byte, payload []byte) string {
	var proxyRequestProto pogo.ProxyRequestProto

	if err := proto.Unmarshal(request, &proxyRequestProto); err != nil {
		log.Errorf("Failed to parse %s", err)
		external.DecodeSocialActionWithRequest.WithLabelValues("error", "request_parse").Inc()
		return fmt.Sprintf("Failed to parse %s", err)
	}

	var proxyResponseProto pogo.ProxyResponseProto

	if err := proto.Unmarshal(payload, &proxyResponseProto); err != nil {
		log.Errorf("Failed to parse %s", err)
		external.DecodeSocialActionWithRequest.WithLabelValues("error", "response_parse").Inc()
		return fmt.Sprintf("Failed to parse %s", err)
	}

	if proxyResponseProto.Status != pogo.ProxyResponseProto_COMPLETED && proxyResponseProto.Status != pogo.ProxyResponseProto_COMPLETED_AND_REASSIGNED {
		external.DecodeSocialActionWithRequest.WithLabelValues("error", "non_success").Inc()
		return fmt.Sprintf("unsuccessful proxyResponseProto response %d %s", int(proxyResponseProto.Status), proxyResponseProto.Status)
	}

	switch pogo.SocialAction(proxyRequestProto.GetAction()) {
	case pogo.SocialAction_SOCIAL_ACTION_LIST_FRIEND_STATUS:
		external.DecodeSocialActionWithRequest.WithLabelValues("ok", "list_friend_status").Inc()
		return decodeGetFriendDetails(proxyResponseProto.Payload)
	case pogo.SocialAction_SOCIAL_ACTION_SEARCH_PLAYER:
		external.DecodeSocialActionWithRequest.WithLabelValues("ok", "search_player").Inc()
		return decodeSearchPlayer(proxyRequestProto, proxyResponseProto.Payload)

	}

	external.DecodeSocialActionWithRequest.WithLabelValues("ok", "unknown").Inc()
	return fmt.Sprintf("Did not process %s", pogo.SocialAction(proxyRequestProto.GetAction()).String())
}

func decodeGetFriendDetails(payload []byte) string {
	var getFriendDetailsOutProto pogo.GetFriendDetailsOutProto
	getFriendDetailsError := proto.Unmarshal(payload, &getFriendDetailsOutProto)

	if getFriendDetailsError != nil {
		external.DecodeGetFriendDetails.WithLabelValues("error", "parse").Inc()
		log.Errorf("Failed to parse %s", getFriendDetailsError)
		return fmt.Sprintf("Failed to parse %s", getFriendDetailsError)
	}

	if getFriendDetailsOutProto.GetResult() != pogo.GetFriendDetailsOutProto_SUCCESS || getFriendDetailsOutProto.GetFriend() == nil {
		external.DecodeGetFriendDetails.WithLabelValues("error", "non_success").Inc()
		return fmt.Sprintf("unsuccessful get friends details")
	}

	failures := 0

	for _, friend := range getFriendDetailsOutProto.GetFriend() {
		player := friend.GetPlayer()

		updatePlayerError := decoder.UpdatePlayerRecordWithPlayerSummary(dbDetails, player, player.PublicData, "", player.GetPlayerId())
		if updatePlayerError != nil {
			failures++
		}
	}

	external.DecodeGetFriendDetails.WithLabelValues("ok", "").Inc()
	return fmt.Sprintf("%d players decoded on %d", len(getFriendDetailsOutProto.GetFriend())-failures, len(getFriendDetailsOutProto.GetFriend()))
}

func decodeSearchPlayer(proxyRequestProto pogo.ProxyRequestProto, payload []byte) string {
	var searchPlayerOutProto pogo.SearchPlayerOutProto
	searchPlayerOutError := proto.Unmarshal(payload, &searchPlayerOutProto)

	if searchPlayerOutError != nil {
		log.Errorf("Failed to parse %s", searchPlayerOutError)
		external.DecodeSearchPlayer.WithLabelValues("error", "parse").Inc()
		return fmt.Sprintf("Failed to parse %s", searchPlayerOutError)
	}

	if searchPlayerOutProto.GetResult() != pogo.SearchPlayerOutProto_SUCCESS || searchPlayerOutProto.GetPlayer() == nil {
		external.DecodeSearchPlayer.WithLabelValues("error", "non_success").Inc()
		return fmt.Sprintf("unsuccessful search player response")
	}

	var searchPlayerProto pogo.SearchPlayerProto
	searchPlayerError := proto.Unmarshal(proxyRequestProto.GetPayload(), &searchPlayerProto)

	if searchPlayerError != nil || searchPlayerProto.GetFriendCode() == "" {
		external.DecodeSearchPlayer.WithLabelValues("error", "parse").Inc()
		return fmt.Sprintf("Failed to parse %s", searchPlayerError)
	}

	player := searchPlayerOutProto.GetPlayer()
	updatePlayerError := decoder.UpdatePlayerRecordWithPlayerSummary(dbDetails, player, player.PublicData, searchPlayerProto.GetFriendCode(), "")
	if updatePlayerError != nil {
		external.DecodeSearchPlayer.WithLabelValues("error", "update").Inc()
		return fmt.Sprintf("Failed update player %s", updatePlayerError)
	}

	external.DecodeSearchPlayer.WithLabelValues("ok", "").Inc()
	return fmt.Sprintf("1 player decoded from SearchPlayerProto")
}

func decodeFortDetails(ctx context.Context, sDec []byte) string {
	decodedFort := &pogo.FortDetailsOutProto{}
	if err := proto.Unmarshal(sDec, decodedFort); err != nil {
		log.Errorf("Failed to parse %s", err)
		external.DecodeFortDetails.WithLabelValues("error", "parse").Inc()
		return fmt.Sprintf("Failed to parse %s", err)
	}

	switch decodedFort.FortType {
	case pogo.FortType_CHECKPOINT:
		external.DecodeFortDetails.WithLabelValues("ok", "pokestop").Inc()
		return decoder.UpdatePokestopRecordWithFortDetailsOutProto(ctx, dbDetails, decodedFort)
	case pogo.FortType_GYM:
		external.DecodeFortDetails.WithLabelValues("ok", "gym").Inc()
		return decoder.UpdateGymRecordWithFortDetailsOutProto(ctx, dbDetails, decodedFort)
	}

	external.DecodeFortDetails.WithLabelValues("ok", "unknown").Inc()
	return "Unknown fort type"
}

func decodeGetMapForts(ctx context.Context, sDec []byte) string {
	decodedMapForts := &pogo.GetMapFortsOutProto{}
	if err := proto.Unmarshal(sDec, decodedMapForts); err != nil {
		log.Errorf("Failed to parse %s", err)
		external.DecodeGetMapForts.WithLabelValues("error", "parse").Inc()
		return fmt.Sprintf("Failed to parse %s", err)
	}

	if decodedMapForts.Status != pogo.GetMapFortsOutProto_SUCCESS {
		external.DecodeGetMapForts.WithLabelValues("error", "non_success").Inc()
		res := fmt.Sprintf(`GetMapFortsOutProto: Ignored non-success value %d:%s`, decodedMapForts.Status,
			pogo.GetMapFortsOutProto_Status_name[int32(decodedMapForts.Status)])
		return res
	}

	external.DecodeGetMapForts.WithLabelValues("ok", "").Inc()
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

func decodeGetRoutes(payload []byte) string {
	getRoutesOutProto := &pogo.GetRoutesOutProto{}
	if err := proto.Unmarshal(payload, getRoutesOutProto); err != nil {
		return fmt.Sprintf("failed to decode GetRoutesOutProto %s", err)
	}

	if getRoutesOutProto.Status != pogo.GetRoutesOutProto_SUCCESS {
		return fmt.Sprintf("GetRoutesOutProto: Ignored non-success value %d:%s", getRoutesOutProto.Status, getRoutesOutProto.Status.String())
	}

	decodeSuccesses := map[string]bool{}
	decodeErrors := map[string]bool{}

	for _, routeMapCell := range getRoutesOutProto.GetRouteMapCell() {
		for _, route := range routeMapCell.GetRoute() {
			if route.RouteSubmissionStatus.Status != pogo.RouteSubmissionStatus_PUBLISHED {
				log.Warnf("Non published Route found in GetRoutesOutProto, status: %s", route.RouteSubmissionStatus.String())
				continue
			}
			decodeError := decoder.UpdateRouteRecordWithSharedRouteProto(dbDetails, route)
			if decodeError != nil {
				if decodeErrors[route.Id] != true {
					decodeErrors[route.Id] = true
				}
				log.Errorf("Failed to decode route %s", decodeError)
			} else if decodeSuccesses[route.Id] != true {
				decodeSuccesses[route.Id] = true
			}
		}
	}

	return fmt.Sprintf(
		"Decoded %d routes, failed to decode %d routes, from %d cells",
		len(decodeSuccesses),
		len(decodeErrors),
		len(getRoutesOutProto.GetRouteMapCell()),
	)
}

func decodeGetGymInfo(ctx context.Context, sDec []byte) string {
	decodedGymInfo := &pogo.GymGetInfoOutProto{}
	if err := proto.Unmarshal(sDec, decodedGymInfo); err != nil {
		log.Errorf("Failed to parse %s", err)
		external.DecodeGetGymInfo.WithLabelValues("error", "parse").Inc()
		return fmt.Sprintf("Failed to parse %s", err)
	}

	if decodedGymInfo.Result != pogo.GymGetInfoOutProto_SUCCESS {
		external.DecodeGetGymInfo.WithLabelValues("error", "non_success").Inc()
		res := fmt.Sprintf(`GymGetInfoOutProto: Ignored non-success value %d:%s`, decodedGymInfo.Result,
			pogo.GymGetInfoOutProto_Result_name[int32(decodedGymInfo.Result)])
		return res
	}

	external.DecodeGetGymInfo.WithLabelValues("ok", "").Inc()
	return decoder.UpdateGymRecordWithGymInfoProto(ctx, dbDetails, decodedGymInfo)
}

func decodeEncounter(ctx context.Context, sDec []byte, username string) string {
	decodedEncounterInfo := &pogo.EncounterOutProto{}
	if err := proto.Unmarshal(sDec, decodedEncounterInfo); err != nil {
		log.Errorf("Failed to parse %s", err)
		external.DecodeEncounter.WithLabelValues("error", "parse").Inc()
		return fmt.Sprintf("Failed to parse %s", err)
	}

	if decodedEncounterInfo.Status != pogo.EncounterOutProto_ENCOUNTER_SUCCESS {
		external.DecodeEncounter.WithLabelValues("error", "non_success").Inc()
		res := fmt.Sprintf(`GymGetInfoOutProto: Ignored non-success value %d:%s`, decodedEncounterInfo.Status,
			pogo.EncounterOutProto_Status_name[int32(decodedEncounterInfo.Status)])
		return res
	}

	external.DecodeEncounter.WithLabelValues("ok", "").Inc()
	return decoder.UpdatePokemonRecordWithEncounterProto(ctx, dbDetails, decodedEncounterInfo, username)
}

func decodeDiskEncounter(ctx context.Context, sDec []byte) string {
	decodedEncounterInfo := &pogo.DiskEncounterOutProto{}
	if err := proto.Unmarshal(sDec, decodedEncounterInfo); err != nil {
		log.Errorf("Failed to parse %s", err)
		external.DecodeDiskEncounter.WithLabelValues("error", "parse").Inc()
		return fmt.Sprintf("Failed to parse %s", err)
	}

	if decodedEncounterInfo.Result != pogo.DiskEncounterOutProto_SUCCESS {
		external.DecodeDiskEncounter.WithLabelValues("error", "non_success").Inc()
		res := fmt.Sprintf(`DiskEncounterOutProto: Ignored non-success value %d:%s`, decodedEncounterInfo.Result,
			pogo.DiskEncounterOutProto_Result_name[int32(decodedEncounterInfo.Result)])
		return res
	}

	external.DecodeDiskEncounter.WithLabelValues("ok", "").Inc()
	return decoder.UpdatePokemonRecordWithDiskEncounterProto(ctx, dbDetails, decodedEncounterInfo)
}

func decodeStartIncident(ctx context.Context, sDec []byte) string {
	decodedIncident := &pogo.StartIncidentOutProto{}
	if err := proto.Unmarshal(sDec, decodedIncident); err != nil {
		log.Errorf("Failed to parse %s", err)
		external.DecodeStartIncident.WithLabelValues("error", "parse").Inc()
		return fmt.Sprintf("Failed to parse %s", err)
	}

	if decodedIncident.Status != pogo.StartIncidentOutProto_SUCCESS {
		external.DecodeStartIncident.WithLabelValues("error", "non_success").Inc()
		res := fmt.Sprintf(`GiovanniOutProto: Ignored non-success value %d:%s`, decodedIncident.Status,
			pogo.StartIncidentOutProto_Status_name[int32(decodedIncident.Status)])
		return res
	}

	external.DecodeStartIncident.WithLabelValues("ok", "").Inc()
	return decoder.ConfirmIncident(ctx, dbDetails, decodedIncident)
}

func decodeOpenInvasion(ctx context.Context, request []byte, payload []byte) string {
	decodeOpenInvasionRequest := &pogo.OpenInvasionCombatSessionProto{}

	if err := proto.Unmarshal(request, decodeOpenInvasionRequest); err != nil {
		log.Errorf("Failed to parse %s", err)
		external.DecodeOpenInvasion.WithLabelValues("error", "parse").Inc()
		return fmt.Sprintf("Failed to parse %s", err)
	}
	if decodeOpenInvasionRequest.IncidentLookup == nil {
		return "Invalid OpenInvasionCombatSessionProto received"
	}

	decodedOpenInvasionResponse := &pogo.OpenInvasionCombatSessionOutProto{}
	if err := proto.Unmarshal(payload, decodedOpenInvasionResponse); err != nil {
		log.Errorf("Failed to parse %s", err)
		external.DecodeOpenInvasion.WithLabelValues("error", "parse").Inc()
		return fmt.Sprintf("Failed to parse %s", err)
	}

	if decodedOpenInvasionResponse.Status != pogo.InvasionStatus_SUCCESS {
		external.DecodeOpenInvasion.WithLabelValues("error", "non_success").Inc()
		res := fmt.Sprintf(`InvasionLineupOutProto: Ignored non-success value %d:%s`, decodedOpenInvasionResponse.Status,
			pogo.InvasionStatus_Status_name[int32(decodedOpenInvasionResponse.Status)])
		return res
	}

	external.DecodeOpenInvasion.WithLabelValues("ok", "").Inc()
	return decoder.UpdateIncidentLineup(ctx, dbDetails, decodeOpenInvasionRequest, decodedOpenInvasionResponse)
}

func decodeGMO(ctx context.Context, protoData *ProtoData, scanParameters decoder.ScanParameters) string {
	decodedGmo := &pogo.GetMapObjectsOutProto{}

	if err := proto.Unmarshal(protoData.Data, decodedGmo); err != nil {
		external.DecodeGMO.WithLabelValues("error", "parse").Inc()
		log.Errorf("Failed to parse %s", err)
	}

	if decodedGmo.Status != pogo.GetMapObjectsOutProto_SUCCESS {
		external.DecodeGMO.WithLabelValues("error", "non_success").Inc()
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
	var cellsToBeCleaned []uint64

	for _, mapCell := range decodedGmo.MapCell {
		if isCellNotEmpty(mapCell) {
			newMapCells = append(newMapCells, mapCell.S2CellId)
			if cellContainsForts(mapCell) {
				cellsToBeCleaned = append(cellsToBeCleaned, mapCell.S2CellId)
			}
		}
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
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				decoder.ClearRemovedForts(ctx, dbDetails, cellsToBeCleaned)
			}()
		}
	}

	newFortsLen := len(newForts)
	newWildPokemonLen := len(newWildPokemon)
	newNearbyPokemonLen := len(newNearbyPokemon)
	newMapPokemonLen := len(newMapPokemon)
	newClientWeatherLen := len(newClientWeather)
	newMapCellsLen := len(newMapCells)

	external.DecodeGMO.WithLabelValues("ok", "").Inc()
	external.DecodeGMOType.WithLabelValues("fort").Add(float64(newFortsLen))
	external.DecodeGMOType.WithLabelValues("wild_pokemon").Add(float64(newWildPokemonLen))
	external.DecodeGMOType.WithLabelValues("nearby_pokemon").Add(float64(newNearbyPokemonLen))
	external.DecodeGMOType.WithLabelValues("map_pokemon").Add(float64(newMapPokemonLen))
	external.DecodeGMOType.WithLabelValues("weather").Add(float64(newClientWeatherLen))
	external.DecodeGMOType.WithLabelValues("cell").Add(float64(newMapCellsLen))

	return fmt.Sprintf("%d cells containing %d forts %d mon %d nearby", newMapCellsLen, newFortsLen, newWildPokemonLen, newNearbyPokemonLen)
}

func isCellNotEmpty(mapCell *pogo.ClientMapCellProto) bool {
	return len(mapCell.Fort) > 0 || len(mapCell.WildPokemon) > 0 || len(mapCell.NearbyPokemon) > 0 || len(mapCell.CatchablePokemon) > 0
}

func cellContainsForts(mapCell *pogo.ClientMapCellProto) bool {
	return len(mapCell.Fort) > 0
}

func decodeGetContestData(ctx context.Context, request []byte, data []byte) string {
	var decodedContestData pogo.GetContestDataOutProto
	if err := proto.Unmarshal(data, &decodedContestData); err != nil {
		log.Errorf("Failed to parse GetContestDataOutProto %s", err)
		return fmt.Sprintf("Failed to parse GetContestDataOutProto %s", err)
	}

	var decodedContestDataRequest pogo.GetContestDataProto
	if request != nil {
		if err := proto.Unmarshal(request, &decodedContestDataRequest); err != nil {
			log.Errorf("Failed to parse GetContestDataProto %s", err)
			return fmt.Sprintf("Failed to parse GetContestDataProto %s", err)
		}
	}
	return decoder.UpdatePokestopWithContestData(ctx, dbDetails, &decodedContestDataRequest, &decodedContestData)
}

func decodeGetPokemonSizeContestEntry(ctx context.Context, request []byte, data []byte) string {
	var decodedPokemonSizeContestEntry pogo.GetPokemonSizeContestEntryOutProto
	if err := proto.Unmarshal(data, &decodedPokemonSizeContestEntry); err != nil {
		log.Errorf("Failed to parse GetPokemonSizeContestEntryOutProto %s", err)
		return fmt.Sprintf("Failed to parse GetPokemonSizeContestEntryOutProto %s", err)
	}

	if decodedPokemonSizeContestEntry.Status != pogo.GetPokemonSizeContestEntryOutProto_SUCCESS {
		return fmt.Sprintf("Ignored GetPokemonSizeContestEntryOutProto non-success status %s", decodedPokemonSizeContestEntry.Status)
	}

	var decodedPokemonSizeContestEntryRequest pogo.GetPokemonSizeContestEntryProto
	if request != nil {
		if err := proto.Unmarshal(request, &decodedPokemonSizeContestEntryRequest); err != nil {
			log.Errorf("Failed to parse GetPokemonSizeContestEntryProto %s", err)
			return fmt.Sprintf("Failed to parse GetPokemonSizeContestEntryProto %s", err)
		}
	}

	return decoder.UpdatePokestopWithPokemonSizeContestEntry(ctx, dbDetails, &decodedPokemonSizeContestEntryRequest, &decodedPokemonSizeContestEntry)
}
