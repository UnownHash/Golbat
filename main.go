package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"runtime"
	"sync"
	"time"
	_ "time/tzdata"

	"golbat/config"
	db2 "golbat/db"
	"golbat/decoder"
	"golbat/external"
	pb "golbat/grpc"
	"golbat/stats_collector"
	"golbat/webhooks"

	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/mysql"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"
	ginlogrus "github.com/toorop/gin-logrus"
	"google.golang.org/grpc"
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
	decoder.InitWriteBehindQueue(ctx, dbDetails)
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

	if cfg.Cleanup.Pokemon && (!cfg.PokemonMemoryOnly || cfg.PreserveInMemoryPokemon) {
		StartDatabaseArchiver(db)
	}

	if cfg.Cleanup.Incidents == true {
		StartIncidentExpiry(db)
	}

	if cfg.Cleanup.Tappables == true {
		StartTappableExpiry(db)
	}

	if cfg.Cleanup.Quests == true {
		StartQuestExpiry(dbDetails)
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

	// Determine loading strategy
	// Preload: warms cache for forts, stations, and recent spawnpoints
	// FortInMemory: enables rtree spatial lookups (only loads forts)
	fortInMemory := cfg.FortInMemory

	if cfg.Preload {
		// Full preload: loads forts, stations, spawnpoints into cache
		// Registers forts with fort tracker, optionally builds rtree
		decoder.Preload(dbDetails, fortInMemory)
	} else if fortInMemory {
		// Fort in memory only: loads forts into cache with rtree
		if err := decoder.PreloadForts(dbDetails, true); err != nil {
			log.Errorf("failed to preload forts: %s", err)
		}
	} else {
		// No preload: fort tracker loads its own minimal data
		if err := decoder.LoadFortsFromDB(ctx, dbDetails); err != nil {
			log.Errorf("failed to load forts into tracker: %s", err)
		}
	}

	// Load preserved pokemon if enabled
	if cfg.PreserveInMemoryPokemon && cfg.PokemonMemoryOnly {
		decoder.PreloadPreservedPokemon(dbDetails)
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
	apiGroup.POST("/gym/scan", GymScan)
	apiGroup.POST("/pokestop/scan", PokestopScan)
	apiGroup.POST("/station/scan", StationScan)
	apiGroup.POST("/fort/scan", FortScan)
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
	apiGroup.GET("/skip-preserve-pokemon", SkipPreservePokemon)
	apiGroup.POST("/skip-preserve-pokemon", SkipPreservePokemon)

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
			pprof.Handler("block").ServeHTTP(c.Writer, c.Request)
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
		pprofGroup.GET("/goroutine", func(c *gin.Context) {
			pprof.Handler("goroutine").ServeHTTP(c.Writer, c.Request)
		})
		if config.Config.Tuning.ProfileContention {
			runtime.SetBlockProfileRate(1)
			runtime.SetMutexProfileFraction(1)
		}
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

	log.Info("go routines have exited, flushing write-behind queue...")
	decoder.FlushWriteBehindQueue()

	// Preserve in-memory pokemon if enabled and not skipped via API
	if cfg.PreserveInMemoryPokemon && cfg.PokemonMemoryOnly {
		if decoder.ShouldPreservePokemon() {
			log.Info("preserving in-memory pokemon to database...")
			decoder.PreservePokemonToDatabase(dbDetails)
		} else {
			log.Info("skipping pokemon preservation (disabled via API)")
		}
	}

	log.Info("flushing webhooks now...")
	webhooksSender.Flush()

	log.Info("Golbat exiting!")
}
