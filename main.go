package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/mysql"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"
	ginlogrus "github.com/toorop/gin-logrus"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	"golbat/config"
	db2 "golbat/db"
	"golbat/decoder"
	"golbat/external"
	pb "golbat/grpc"
	"golbat/pogo"
	"golbat/stats_collector"
	"golbat/webhooks"
)

var db *sqlx.DB
var dbDetails db2.DbDetails
var statsCollector stats_collector.StatsCollector

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

	log.Infof("Golbat starting")

	// Both Sentry & Pyroscope are optional and off by default. Read more:
	// https://docs.sentry.io/platforms/go
	// https://pyroscope.io/docs/golang
	external.InitSentry()
	external.InitPyroscope()

	webhooksSender, err := webhooks.NewWebhooksSender(cfg)
	if err != nil {
		log.Fatalf("failed to setup webhooks sender: %s", err)
	}
	decoder.SetWebhooksSender(webhooksSender)

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

	// Create the web server.
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	if cfg.Logging.Debug {
		r.Use(ginlogrus.Logger(log.StandardLogger()))
	} else {
		r.Use(gin.Recovery())
	}

	// choose the statsCollector we will use.
	statsCollector = stats_collector.GetStatsCollector(cfg, r)
	// tell the decoder the stats collector to use
	decoder.SetStatsCollector(statsCollector)
	db2.SetStatsCollector(statsCollector)

	// collect live stats when prometheus and liveStats are enabled
	if cfg.Prometheus.Enabled && cfg.Prometheus.LiveStats {
		go db2.PromLiveStatsUpdater(dbDetails, cfg.Prometheus.LiveStatsSleep)
	}

	decoder.InitialiseOhbem()
	if cfg.Weather.ProactiveIVSwitching {
		decoder.InitProactiveIVSwitchSem()

		// Try to fetch from remote first, fallback to cache, then fallback to bundled file
		if err := decoder.FetchMasterFileData(); err != nil {
			if err2 := decoder.LoadMasterFileData(""); err2 != nil {
				_ = decoder.LoadMasterFileData("pogo/master-latest-rdm.json")
				log.Errorf("Weather MasterFile fetch failed. Loading from cache failed: %s. Loading from pogo/master-latest-rdm.json instead.", err2)
			} else {
				log.Warnf("Weather MasterFile fetch failed, loaded from cache: %s", err)
			}
		} else {
			// Save to cache if successfully fetched
			_ = decoder.SaveMasterFileData()
		}

		_ = decoder.WatchMasterFileData()
	}
	decoder.LoadStatsGeofences()
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

	if cfg.Cleanup.Tappables == true {
		StartTappableExpiry(db)
	}

	if cfg.Cleanup.Quests == true {
		StartQuestExpiry(db)
	}

	if cfg.Cleanup.Stats == true {
		StartStatsExpiry(db)
	}

	// init fort tracker for memory-based fort cleanup
	staleThreshold := cfg.Cleanup.FortsStaleThreshold
	if staleThreshold <= 0 {
		staleThreshold = 3600 // def 1 hour
	}
	decoder.InitFortTracker(staleThreshold)
	if err := decoder.LoadFortsFromDB(ctx, dbDetails); err != nil {
		log.Errorf("failed to load forts into tracker: %s", err)
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

	r.POST("/raw", Raw)
	r.GET("/health", GetHealth)

	apiGroup := r.Group("/api", AuthRequired())
	apiGroup.GET("/health", GetHealth)
	apiGroup.POST("/clear-quests", ClearQuests)
	apiGroup.POST("/quest-status", GetQuestStatus)
	apiGroup.POST("/pokestop-positions", GetPokestopPositions)
	apiGroup.GET("/pokestop/id/:fort_id", GetPokestop)
	apiGroup.GET("/gym/id/:gym_id", GetGym)
	apiGroup.POST("/gym/query", GetGyms)
	apiGroup.POST("/gym/search", SearchGyms)
	apiGroup.POST("/reload-geojson", ReloadGeojson)
	apiGroup.GET("/reload-geojson", ReloadGeojson)

	apiGroup.GET("/pokemon/id/:pokemon_id", PokemonOne)
	apiGroup.GET("/pokemon/available", PokemonAvailable)
	apiGroup.POST("/pokemon/scan", PokemonScan)
	apiGroup.POST("/pokemon/v2/scan", PokemonScan2)
	apiGroup.POST("/pokemon/v3/scan", PokemonScan3)
	apiGroup.POST("/pokemon/search", PokemonSearch)

	apiGroup.GET("/tappable/id/:tappable_id", GetTappable)

	apiGroup.GET("/devices/all", GetDevices)

	debugGroup := r.Group("/debug")

	if cfg.Tuning.ProfileRoutes {
		pprofGroup := debugGroup.Group("/pprof", AuthRequired())
		pprofGroup.GET("/cmdline", func(c *gin.Context) {
			pprof.Cmdline(c.Writer, c.Request)
		})
		pprofGroup.GET("/heap", func(c *gin.Context) {
			pprof.Index(c.Writer, c.Request)
		})
		pprofGroup.GET("/block", func(c *gin.Context) {
			pprof.Index(c.Writer, c.Request)
		})
		pprofGroup.GET("/mutex", func(c *gin.Context) {
			pprof.Index(c.Writer, c.Request)
		})
		pprofGroup.GET("/trace", func(c *gin.Context) {
			pprof.Trace(c.Writer, c.Request)
		})
		pprofGroup.GET("/profile", func(c *gin.Context) {
			pprof.Profile(c.Writer, c.Request)
		})
		pprofGroup.GET("/symbol", func(c *gin.Context) {
			pprof.Symbol(c.Writer, c.Request)
		})
	}

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: r,
	}

	// Start the server in a goroutine, as it will block until told to shutdown.
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

	if method != int(pogo.InternalPlatformClientAction_INTERNAL_PROXY_SOCIAL_ACTION) && protoData.Level < 30 {
		statsCollector.IncDecodeMethods("error", "low_level", getMethodName(method, true))
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
			result = decodeEncounter(ctx, protoData.Data, protoData.Account, protoData.TimestampMs)
		}
		processed = true
	case pogo.Method_METHOD_DISK_ENCOUNTER:
		result = decodeDiskEncounter(ctx, protoData.Data, protoData.Account)
		processed = true
	case pogo.Method_METHOD_FORT_SEARCH:
		result = decodeQuest(ctx, protoData.Data, protoData.HaveAr)
		processed = true
	case pogo.Method_METHOD_GET_PLAYER:
		ignore = true
	case pogo.Method_METHOD_GET_HOLOHOLO_INVENTORY:
		ignore = true
	case pogo.Method_METHOD_CREATE_COMBAT_CHALLENGE:
		ignore = true
	case pogo.Method(pogo.InternalPlatformClientAction_INTERNAL_PROXY_SOCIAL_ACTION):
		if protoData.Request != nil {
			result = decodeSocialActionWithRequest(protoData.Request, protoData.Data)
			processed = true
		}
	case pogo.Method_METHOD_GET_MAP_FORTS:
		result = decodeGetMapForts(ctx, protoData.Data)
		processed = true
	case pogo.Method_METHOD_GET_ROUTES:
		result = decodeGetRoutes(protoData.Data)
		processed = true
	case pogo.Method_METHOD_GET_CONTEST_DATA:
		if getScanParameters(protoData).ProcessPokestops {
			// Request helps, but can be decoded without it
			result = decodeGetContestData(ctx, protoData.Request, protoData.Data)
		}
		processed = true
	case pogo.Method_METHOD_GET_POKEMON_SIZE_CONTEST_ENTRY:
		// Request is essential to decode this
		if protoData.Request != nil {
			if getScanParameters(protoData).ProcessPokestops {
				result = decodeGetPokemonSizeContestEntry(ctx, protoData.Request, protoData.Data)
			}
			processed = true
		}
	case pogo.Method_METHOD_GET_STATION_DETAILS:
		if getScanParameters(protoData).ProcessStations {
			// Request is essential to decode this
			result = decodeGetStationDetails(ctx, protoData.Request, protoData.Data)
		}
		processed = true
	case pogo.Method_METHOD_PROCESS_TAPPABLE:
		if getScanParameters(protoData).ProcessTappables {
			// Request is essential to decode this
			result = decodeTappable(ctx, protoData.Request, protoData.Data, protoData.Account, protoData.TimestampMs)
		}
		processed = true
	case pogo.Method_METHOD_GET_EVENT_RSVPS:
		if getScanParameters(protoData).ProcessGyms {
			result = decodeGetEventRsvp(ctx, protoData.Request, protoData.Data)
		}
		processed = true
	case pogo.Method_METHOD_GET_EVENT_RSVP_COUNT:
		if getScanParameters(protoData).ProcessGyms {
			result = decodeGetEventRsvpCount(ctx, protoData.Data)
		}
		processed = true
	default:
		log.Debugf("Did not know hook type %s", pogo.Method(method))
	}
	if !ignore {
		elapsed := time.Since(start)
		if processed == true {
			statsCollector.IncDecodeMethods("ok", "", getMethodName(method, true))
			log.Debugf("%s/%s %s - %s - %s", protoData.Uuid, protoData.Account, pogo.Method(method), elapsed, result)
		} else {
			log.Debugf("%s/%s %s - %s - %s", protoData.Uuid, protoData.Account, pogo.Method(method), elapsed, "**Did not process**")
			statsCollector.IncDecodeMethods("unprocessed", "", getMethodName(method, true))
		}
	}
}

func getScanParameters(protoData *ProtoData) decoder.ScanParameters {
	return decoder.FindScanConfiguration(protoData.ScanContext, protoData.Lat, protoData.Lon)
}

func decodeQuest(ctx context.Context, sDec []byte, haveAr *bool) string {
	if haveAr == nil {
		statsCollector.IncDecodeQuest("error", "missing_ar_info")
		log.Infoln("Cannot determine AR quest - ignoring")
		// We should either assume AR quest, or trace inventory like RDM probably
		return "No AR quest info"
	}
	decodedQuest := &pogo.FortSearchOutProto{}
	if err := proto.Unmarshal(sDec, decodedQuest); err != nil {
		log.Errorf("Failed to parse %s", err)
		statsCollector.IncDecodeQuest("error", "parse")
		return "Parse failure"
	}

	if decodedQuest.Result != pogo.FortSearchOutProto_SUCCESS {
		statsCollector.IncDecodeQuest("error", "non_success")
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
		statsCollector.IncDecodeSocialActionWithRequest("error", "request_parse")
		return fmt.Sprintf("Failed to parse %s", err)
	}

	var proxyResponseProto pogo.ProxyResponseProto

	if err := proto.Unmarshal(payload, &proxyResponseProto); err != nil {
		log.Errorf("Failed to parse %s", err)
		statsCollector.IncDecodeSocialActionWithRequest("error", "response_parse")
		return fmt.Sprintf("Failed to parse %s", err)
	}

	if proxyResponseProto.Status != pogo.ProxyResponseProto_COMPLETED && proxyResponseProto.Status != pogo.ProxyResponseProto_COMPLETED_AND_REASSIGNED {
		statsCollector.IncDecodeSocialActionWithRequest("error", "non_success")
		return fmt.Sprintf("unsuccessful proxyResponseProto response %d %s", int(proxyResponseProto.Status), proxyResponseProto.Status)
	}

	switch pogo.InternalSocialAction(proxyRequestProto.GetAction()) {
	case pogo.InternalSocialAction_SOCIAL_ACTION_LIST_FRIEND_STATUS:
		statsCollector.IncDecodeSocialActionWithRequest("ok", "list_friend_status")
		return decodeGetFriendDetails(proxyResponseProto.Payload)
	case pogo.InternalSocialAction_SOCIAL_ACTION_SEARCH_PLAYER:
		statsCollector.IncDecodeSocialActionWithRequest("ok", "search_player")
		return decodeSearchPlayer(&proxyRequestProto, proxyResponseProto.Payload)

	}

	statsCollector.IncDecodeSocialActionWithRequest("ok", "unknown")
	return fmt.Sprintf("Did not process %s", pogo.InternalSocialAction(proxyRequestProto.GetAction()).String())
}

func decodeGetFriendDetails(payload []byte) string {
	var getFriendDetailsOutProto pogo.InternalGetFriendDetailsOutProto
	getFriendDetailsError := proto.Unmarshal(payload, &getFriendDetailsOutProto)

	if getFriendDetailsError != nil {
		statsCollector.IncDecodeGetFriendDetails("error", "parse")
		log.Errorf("Failed to parse %s", getFriendDetailsError)
		return fmt.Sprintf("Failed to parse %s", getFriendDetailsError)
	}

	if getFriendDetailsOutProto.GetResult() != pogo.InternalGetFriendDetailsOutProto_SUCCESS || getFriendDetailsOutProto.GetFriend() == nil {
		statsCollector.IncDecodeGetFriendDetails("error", "non_success")
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

	statsCollector.IncDecodeGetFriendDetails("ok", "")
	return fmt.Sprintf("%d players decoded on %d", len(getFriendDetailsOutProto.GetFriend())-failures, len(getFriendDetailsOutProto.GetFriend()))
}

func decodeSearchPlayer(proxyRequestProto *pogo.ProxyRequestProto, payload []byte) string {
	var searchPlayerOutProto pogo.InternalSearchPlayerOutProto
	searchPlayerOutError := proto.Unmarshal(payload, &searchPlayerOutProto)

	if searchPlayerOutError != nil {
		log.Errorf("Failed to parse %s", searchPlayerOutError)
		statsCollector.IncDecodeSearchPlayer("error", "parse")
		return fmt.Sprintf("Failed to parse %s", searchPlayerOutError)
	}

	if searchPlayerOutProto.GetResult() != pogo.InternalSearchPlayerOutProto_SUCCESS || searchPlayerOutProto.GetPlayer() == nil {
		statsCollector.IncDecodeSearchPlayer("error", "non_success")
		return fmt.Sprintf("unsuccessful search player response")
	}

	var searchPlayerProto pogo.InternalSearchPlayerProto
	searchPlayerError := proto.Unmarshal(proxyRequestProto.GetPayload(), &searchPlayerProto)

	if searchPlayerError != nil || searchPlayerProto.GetFriendCode() == "" {
		statsCollector.IncDecodeSearchPlayer("error", "parse")
		return fmt.Sprintf("Failed to parse %s", searchPlayerError)
	}

	player := searchPlayerOutProto.GetPlayer()
	updatePlayerError := decoder.UpdatePlayerRecordWithPlayerSummary(dbDetails, player, player.PublicData, searchPlayerProto.GetFriendCode(), "")
	if updatePlayerError != nil {
		statsCollector.IncDecodeSearchPlayer("error", "update")
		return fmt.Sprintf("Failed update player %s", updatePlayerError)
	}

	statsCollector.IncDecodeSearchPlayer("ok", "")
	return fmt.Sprintf("1 player decoded from SearchPlayerProto")
}

func decodeFortDetails(ctx context.Context, sDec []byte) string {
	decodedFort := &pogo.FortDetailsOutProto{}
	if err := proto.Unmarshal(sDec, decodedFort); err != nil {
		log.Errorf("Failed to parse %s", err)
		statsCollector.IncDecodeFortDetails("error", "parse")
		return fmt.Sprintf("Failed to parse %s", err)
	}

	switch decodedFort.FortType {
	case pogo.FortType_CHECKPOINT:
		statsCollector.IncDecodeFortDetails("ok", "pokestop")
		return decoder.UpdatePokestopRecordWithFortDetailsOutProto(ctx, dbDetails, decodedFort)
	case pogo.FortType_GYM:
		statsCollector.IncDecodeFortDetails("ok", "gym")
		return decoder.UpdateGymRecordWithFortDetailsOutProto(ctx, dbDetails, decodedFort)
	}

	statsCollector.IncDecodeFortDetails("ok", "unknown")
	return "Unknown fort type"
}

func decodeGetMapForts(ctx context.Context, sDec []byte) string {
	decodedMapForts := &pogo.GetMapFortsOutProto{}
	if err := proto.Unmarshal(sDec, decodedMapForts); err != nil {
		log.Errorf("Failed to parse %s", err)
		statsCollector.IncDecodeGetMapForts("error", "parse")
		return fmt.Sprintf("Failed to parse %s", err)
	}

	if decodedMapForts.Status != pogo.GetMapFortsOutProto_SUCCESS {
		statsCollector.IncDecodeGetMapForts("error", "non_success")
		res := fmt.Sprintf(`GetMapFortsOutProto: Ignored non-success value %d:%s`, decodedMapForts.Status,
			pogo.GetMapFortsOutProto_Status_name[int32(decodedMapForts.Status)])
		return res
	}

	statsCollector.IncDecodeGetMapForts("ok", "")
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
			//TODO we need to check the repeated field, for now access last element
			routeSubmissionStatus := route.RouteSubmissionStatus[len(route.RouteSubmissionStatus)-1]
			if routeSubmissionStatus != nil && routeSubmissionStatus.Status != pogo.RouteSubmissionStatus_PUBLISHED {
				log.Warnf("Non published Route found in GetRoutesOutProto, status: %s", routeSubmissionStatus.String())
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
		statsCollector.IncDecodeGetGymInfo("error", "parse")
		return fmt.Sprintf("Failed to parse %s", err)
	}

	if decodedGymInfo.Result != pogo.GymGetInfoOutProto_SUCCESS {
		statsCollector.IncDecodeGetGymInfo("error", "non_success")
		res := fmt.Sprintf(`GymGetInfoOutProto: Ignored non-success value %d:%s`, decodedGymInfo.Result,
			pogo.GymGetInfoOutProto_Result_name[int32(decodedGymInfo.Result)])
		return res
	}

	statsCollector.IncDecodeGetGymInfo("ok", "")
	return decoder.UpdateGymRecordWithGymInfoProto(ctx, dbDetails, decodedGymInfo)
}

func decodeEncounter(ctx context.Context, sDec []byte, username string, timestampMs int64) string {
	decodedEncounterInfo := &pogo.EncounterOutProto{}
	if err := proto.Unmarshal(sDec, decodedEncounterInfo); err != nil {
		log.Errorf("Failed to parse %s", err)
		statsCollector.IncDecodeEncounter("error", "parse")
		return fmt.Sprintf("Failed to parse %s", err)
	}

	if decodedEncounterInfo.Status != pogo.EncounterOutProto_ENCOUNTER_SUCCESS {
		statsCollector.IncDecodeEncounter("error", "non_success")
		res := fmt.Sprintf(`EncounterOutProto: Ignored non-success value %d:%s`, decodedEncounterInfo.Status,
			pogo.EncounterOutProto_Status_name[int32(decodedEncounterInfo.Status)])
		return res
	}

	statsCollector.IncDecodeEncounter("ok", "")
	return decoder.UpdatePokemonRecordWithEncounterProto(ctx, dbDetails, decodedEncounterInfo, username, timestampMs)
}

func decodeDiskEncounter(ctx context.Context, sDec []byte, username string) string {
	decodedEncounterInfo := &pogo.DiskEncounterOutProto{}
	if err := proto.Unmarshal(sDec, decodedEncounterInfo); err != nil {
		log.Errorf("Failed to parse %s", err)
		statsCollector.IncDecodeDiskEncounter("error", "parse")
		return fmt.Sprintf("Failed to parse %s", err)
	}

	if decodedEncounterInfo.Result != pogo.DiskEncounterOutProto_SUCCESS {
		statsCollector.IncDecodeDiskEncounter("error", "non_success")
		res := fmt.Sprintf(`DiskEncounterOutProto: Ignored non-success value %d:%s`, decodedEncounterInfo.Result,
			pogo.DiskEncounterOutProto_Result_name[int32(decodedEncounterInfo.Result)])
		return res
	}

	statsCollector.IncDecodeDiskEncounter("ok", "")
	return decoder.UpdatePokemonRecordWithDiskEncounterProto(ctx, dbDetails, decodedEncounterInfo, username)
}

func decodeStartIncident(ctx context.Context, sDec []byte) string {
	decodedIncident := &pogo.StartIncidentOutProto{}
	if err := proto.Unmarshal(sDec, decodedIncident); err != nil {
		log.Errorf("Failed to parse %s", err)
		statsCollector.IncDecodeStartIncident("error", "parse")
		return fmt.Sprintf("Failed to parse %s", err)
	}

	if decodedIncident.Status != pogo.StartIncidentOutProto_SUCCESS {
		statsCollector.IncDecodeStartIncident("error", "non_success")
		res := fmt.Sprintf(`GiovanniOutProto: Ignored non-success value %d:%s`, decodedIncident.Status,
			pogo.StartIncidentOutProto_Status_name[int32(decodedIncident.Status)])
		return res
	}

	statsCollector.IncDecodeStartIncident("ok", "")
	return decoder.ConfirmIncident(ctx, dbDetails, decodedIncident)
}

func decodeOpenInvasion(ctx context.Context, request []byte, payload []byte) string {
	decodeOpenInvasionRequest := &pogo.OpenInvasionCombatSessionProto{}

	if err := proto.Unmarshal(request, decodeOpenInvasionRequest); err != nil {
		log.Errorf("Failed to parse %s", err)
		statsCollector.IncDecodeOpenInvasion("error", "parse")
		return fmt.Sprintf("Failed to parse %s", err)
	}
	if decodeOpenInvasionRequest.IncidentLookup == nil {
		return "Invalid OpenInvasionCombatSessionProto received"
	}

	decodedOpenInvasionResponse := &pogo.OpenInvasionCombatSessionOutProto{}
	if err := proto.Unmarshal(payload, decodedOpenInvasionResponse); err != nil {
		log.Errorf("Failed to parse %s", err)
		statsCollector.IncDecodeOpenInvasion("error", "parse")
		return fmt.Sprintf("Failed to parse %s", err)
	}

	if decodedOpenInvasionResponse.Status != pogo.InvasionStatus_SUCCESS {
		statsCollector.IncDecodeOpenInvasion("error", "non_success")
		res := fmt.Sprintf(`InvasionLineupOutProto: Ignored non-success value %d:%s`, decodedOpenInvasionResponse.Status,
			pogo.InvasionStatus_Status_name[int32(decodedOpenInvasionResponse.Status)])
		return res
	}

	statsCollector.IncDecodeOpenInvasion("ok", "")
	return decoder.UpdateIncidentLineup(ctx, dbDetails, decodeOpenInvasionRequest, decodedOpenInvasionResponse)
}

func decodeGMO(ctx context.Context, protoData *ProtoData, scanParameters decoder.ScanParameters) string {
	decodedGmo := &pogo.GetMapObjectsOutProto{}

	if err := proto.Unmarshal(protoData.Data, decodedGmo); err != nil {
		statsCollector.IncDecodeGMO("error", "parse")
		log.Errorf("Failed to parse %s", err)
	}

	if decodedGmo.Status != pogo.GetMapObjectsOutProto_SUCCESS {
		statsCollector.IncDecodeGMO("error", "non_success")
		res := fmt.Sprintf(`GetMapObjectsOutProto: Ignored non-success value %d:%s`, decodedGmo.Status,
			pogo.GetMapObjectsOutProto_Status_name[int32(decodedGmo.Status)])
		return res
	}

	var newForts []decoder.RawFortData
	var newStations []decoder.RawStationData
	var newWildPokemon []decoder.RawWildPokemonData
	var newNearbyPokemon []decoder.RawNearbyPokemonData
	var newMapPokemon []decoder.RawMapPokemonData
	var newMapCells []uint64
	var cellsToBeCleaned []uint64

	// track forts per cell for memory-based cleanup (only if tracker enabled)
	cellForts := make(map[uint64]*decoder.FortTrackerGMOContents)

	if len(decodedGmo.MapCell) == 0 {
		return "Skipping GetMapObjectsOutProto: No map cells found"
	}
	for _, mapCell := range decodedGmo.MapCell {
		// initialize cell forts tracking for every map cell (so empty fort lists are seen as "no forts")
		cellForts[mapCell.S2CellId] = &decoder.FortTrackerGMOContents{
			Pokestops: make([]string, 0),
			Gyms:      make([]string, 0),
			Timestamp: mapCell.AsOfTimeMs,
		}
		// always mark this mapCell to be checked for removed forts. Previously only cells with forts were
		// added which meant an empty fort list (all forts removed) was never passed to the tracker.
		cellsToBeCleaned = append(cellsToBeCleaned, mapCell.S2CellId)

		if isCellNotEmpty(mapCell) {
			newMapCells = append(newMapCells, mapCell.S2CellId)
		}

		for _, fort := range mapCell.Fort {
			newForts = append(newForts, decoder.RawFortData{Cell: mapCell.S2CellId, Data: fort, Timestamp: mapCell.AsOfTimeMs})

			// track fort by type for memory-based cleanup (only if tracker enabled)
			if cf, ok := cellForts[mapCell.S2CellId]; ok {
				switch fort.FortType {
				case pogo.FortType_GYM:
					cf.Gyms = append(cf.Gyms, fort.FortId)
				case pogo.FortType_CHECKPOINT:
					cf.Pokestops = append(cf.Pokestops, fort.FortId)
				}
			}

			if fort.ActivePokemon != nil {
				newMapPokemon = append(newMapPokemon, decoder.RawMapPokemonData{Cell: mapCell.S2CellId, Data: fort.ActivePokemon, Timestamp: mapCell.AsOfTimeMs})
			}
		}
		for _, mon := range mapCell.WildPokemon {
			newWildPokemon = append(newWildPokemon, decoder.RawWildPokemonData{Cell: mapCell.S2CellId, Data: mon, Timestamp: mapCell.AsOfTimeMs})
		}
		for _, mon := range mapCell.NearbyPokemon {
			newNearbyPokemon = append(newNearbyPokemon, decoder.RawNearbyPokemonData{Cell: mapCell.S2CellId, Data: mon, Timestamp: mapCell.AsOfTimeMs})
		}
		for _, station := range mapCell.Stations {
			newStations = append(newStations, decoder.RawStationData{Cell: mapCell.S2CellId, Data: station})
		}
	}

	if scanParameters.ProcessGyms || scanParameters.ProcessPokestops {
		decoder.UpdateFortBatch(ctx, dbDetails, scanParameters, newForts)
	}
	var weatherUpdates []decoder.WeatherUpdate
	if scanParameters.ProcessWeather {
		weatherUpdates = decoder.UpdateClientWeatherBatch(ctx, dbDetails, decodedGmo.ClientWeather, decodedGmo.MapCell[0].AsOfTimeMs, protoData.Account)
	}
	if scanParameters.ProcessPokemon {
		decoder.UpdatePokemonBatch(ctx, dbDetails, scanParameters, newWildPokemon, newNearbyPokemon, newMapPokemon, decodedGmo.ClientWeather, protoData.Account)
		if scanParameters.ProcessWeather && scanParameters.ProactiveIVSwitching {
			for _, weatherUpdate := range weatherUpdates {
				go func(weatherUpdate decoder.WeatherUpdate) {
					decoder.ProactiveIVSwitchSem <- true
					defer func() { <-decoder.ProactiveIVSwitchSem }()
					decoder.ProactiveIVSwitch(ctx, dbDetails, weatherUpdate, scanParameters.ProactiveIVSwitchingToDB, decodedGmo.MapCell[0].AsOfTimeMs/1000)
				}(weatherUpdate)
			}
		}
	}
	if scanParameters.ProcessStations {
		decoder.UpdateStationBatch(ctx, dbDetails, scanParameters, newStations)
	}

	if scanParameters.ProcessCells {
		decoder.UpdateClientMapS2CellBatch(ctx, dbDetails, newMapCells)
	}

	if scanParameters.ProcessGyms || scanParameters.ProcessPokestops {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			decoder.CheckRemovedForts(ctx, dbDetails, cellsToBeCleaned, cellForts)
		}()
	}

	newFortsLen := len(newForts)
	newStationsLen := len(newStations)
	newWildPokemonLen := len(newWildPokemon)
	newNearbyPokemonLen := len(newNearbyPokemon)
	newMapPokemonLen := len(newMapPokemon)
	newClientWeatherLen := len(decodedGmo.ClientWeather)
	newMapCellsLen := len(newMapCells)

	statsCollector.IncDecodeGMO("ok", "")
	statsCollector.AddDecodeGMOType("fort", float64(newFortsLen))
	statsCollector.AddDecodeGMOType("station", float64(newStationsLen))
	statsCollector.AddDecodeGMOType("wild_pokemon", float64(newWildPokemonLen))
	statsCollector.AddDecodeGMOType("nearby_pokemon", float64(newNearbyPokemonLen))
	statsCollector.AddDecodeGMOType("map_pokemon", float64(newMapPokemonLen))
	statsCollector.AddDecodeGMOType("weather", float64(newClientWeatherLen))
	statsCollector.AddDecodeGMOType("cell", float64(newMapCellsLen))

	return fmt.Sprintf("%d cells containing %d forts %d stations %d mon %d nearby", newMapCellsLen, newFortsLen, newStationsLen, newWildPokemonLen, newNearbyPokemonLen)
}

func isCellNotEmpty(mapCell *pogo.ClientMapCellProto) bool {
	return len(mapCell.Stations) > 0 || len(mapCell.Fort) > 0 || len(mapCell.WildPokemon) > 0 || len(mapCell.NearbyPokemon) > 0 || len(mapCell.CatchablePokemon) > 0
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
	var decodedPokemonSizeContestEntry pogo.GetPokemonSizeLeaderboardEntryOutProto
	if err := proto.Unmarshal(data, &decodedPokemonSizeContestEntry); err != nil {
		log.Errorf("Failed to parse GetPokemonSizeLeaderboardEntryOutProto %s", err)
		return fmt.Sprintf("Failed to parse GetPokemonSizeLeaderboardEntryOutProto %s", err)
	}

	if decodedPokemonSizeContestEntry.Status != pogo.GetPokemonSizeLeaderboardEntryOutProto_SUCCESS {
		return fmt.Sprintf("Ignored GetPokemonSizeLeaderboardEntryOutProto non-success status %s", decodedPokemonSizeContestEntry.Status)
	}

	var decodedPokemonSizeContestEntryRequest pogo.GetPokemonSizeLeaderboardEntryProto
	if request != nil {
		if err := proto.Unmarshal(request, &decodedPokemonSizeContestEntryRequest); err != nil {
			log.Errorf("Failed to parse GetPokemonSizeLeaderboardEntryOutProto %s", err)
			return fmt.Sprintf("Failed to parse GetPokemonSizeLeaderboardEntryOutProto %s", err)
		}
	}

	return decoder.UpdatePokestopWithPokemonSizeContestEntry(ctx, dbDetails, &decodedPokemonSizeContestEntryRequest, &decodedPokemonSizeContestEntry)
}

func decodeGetStationDetails(ctx context.Context, request []byte, data []byte) string {
	var decodedGetStationDetails pogo.GetStationedPokemonDetailsOutProto
	if err := proto.Unmarshal(data, &decodedGetStationDetails); err != nil {
		log.Errorf("Failed to parse GetStationedPokemonDetailsOutProto %s", err)
		return fmt.Sprintf("Failed to parse GetStationedPokemonDetailsOutProto %s", err)
	}

	var decodedGetStationDetailsRequest pogo.GetStationedPokemonDetailsProto
	if request != nil {
		if err := proto.Unmarshal(request, &decodedGetStationDetailsRequest); err != nil {
			log.Errorf("Failed to parse GetStationedPokemonDetailsProto %s", err)
			return fmt.Sprintf("Failed to parse GetStationedPokemonDetailsProto %s", err)
		}
	}

	if decodedGetStationDetails.Result == pogo.GetStationedPokemonDetailsOutProto_STATION_NOT_FOUND {
		// station without stationed pokemon found, therefore we need to reset the columns
		return decoder.ResetStationedPokemonWithStationDetailsNotFound(ctx, dbDetails, &decodedGetStationDetailsRequest)
	} else if decodedGetStationDetails.Result != pogo.GetStationedPokemonDetailsOutProto_SUCCESS {
		return fmt.Sprintf("Ignored GetStationedPokemonDetailsOutProto non-success status %s", decodedGetStationDetails.Result)
	}

	return decoder.UpdateStationWithStationDetails(ctx, dbDetails, &decodedGetStationDetailsRequest, &decodedGetStationDetails)
}

func decodeTappable(ctx context.Context, request, data []byte, username string, timestampMs int64) string {
	var tappable pogo.ProcessTappableOutProto
	if err := proto.Unmarshal(data, &tappable); err != nil {
		log.Errorf("Failed to parse %s", err)
		return fmt.Sprintf("Failed to parse ProcessTappableOutProto %s", err)
	}

	var tappableRequest pogo.ProcessTappableProto
	if request != nil {
		if err := proto.Unmarshal(request, &tappableRequest); err != nil {
			log.Errorf("Failed to parse %s", err)
			return fmt.Sprintf("Failed to parse ProcessTappableProto %s", err)
		}
	}

	if tappable.Status != pogo.ProcessTappableOutProto_SUCCESS {
		return fmt.Sprintf("Ignored ProcessTappableOutProto non-success status %s", tappable.Status)
	}
	var result string
	if encounter := tappable.GetEncounter(); encounter != nil {
		result = decoder.UpdatePokemonRecordWithTappableEncounter(ctx, dbDetails, &tappableRequest, encounter, username, timestampMs)
	}
	return result + " " + decoder.UpdateTappable(ctx, dbDetails, &tappableRequest, &tappable, timestampMs)
}

func decodeGetEventRsvp(ctx context.Context, request []byte, data []byte) string {
	var rsvp pogo.GetEventRsvpsOutProto
	if err := proto.Unmarshal(data, &rsvp); err != nil {
		log.Errorf("Failed to parse %s", err)
		return fmt.Sprintf("Failed to parse GetEventRsvpsOutProto %s", err)
	}

	var rsvpRequest pogo.GetEventRsvpsProto
	if request != nil {
		if err := proto.Unmarshal(request, &rsvpRequest); err != nil {
			log.Errorf("Failed to parse %s", err)
			return fmt.Sprintf("Failed to parse GetEventRsvpsProto %s", err)
		}
	}

	if rsvp.Status != pogo.GetEventRsvpsOutProto_SUCCESS {
		return fmt.Sprintf("Ignored GetEventRsvpsOutProto non-success status %s", rsvp.Status)
	}

	switch op := rsvpRequest.EventDetails.(type) {
	case *pogo.GetEventRsvpsProto_Raid:
		return decoder.UpdateGymRecordWithRsvpProto(ctx, dbDetails, op.Raid, &rsvp)
	case *pogo.GetEventRsvpsProto_GmaxBattle:
		return "Unsupported GmaxBattle Rsvp received"
	}

	return "Failed to parse GetEventRsvpsProto - unknown event type"
}

func decodeGetEventRsvpCount(ctx context.Context, data []byte) string {
	var rsvp pogo.GetEventRsvpCountOutProto
	if err := proto.Unmarshal(data, &rsvp); err != nil {
		log.Errorf("Failed to parse %s", err)
		return fmt.Sprintf("Failed to parse GetEventRsvpCountOutProto %s", err)
	}

	if rsvp.Status != pogo.GetEventRsvpCountOutProto_SUCCESS {
		return fmt.Sprintf("Ignored GetEventRsvpCountOutProto non-success status %s", rsvp.Status)
	}

	var clearLocations []string
	for _, rsvpDetails := range rsvp.RsvpDetails {
		if rsvpDetails.MaybeCount == 0 && rsvpDetails.GoingCount == 0 {
			clearLocations = append(clearLocations, rsvpDetails.LocationId)
			decoder.ClearGymRsvp(ctx, dbDetails, rsvpDetails.LocationId)
		}
	}

	return "Cleared RSVP @ " + strings.Join(clearLocations, ", ")
}
