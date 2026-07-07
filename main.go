package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"runtime"
	"runtime/debug"
	"sync"
	"time"
	_ "time/tzdata"

	"golbat/config"
	db2 "golbat/db"
	"golbat/decoder"
	"golbat/external"
	pb "golbat/grpc"
	"golbat/pogoshim"
	"golbat/stats_collector"
	"golbat/webhooks"

	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/mysql"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	grpcprom "github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus"
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

	if cfg.Logging.Debug {
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

	// Construct entity caches now that config is loaded. Must run before any
	// other decoder call (config drives cache shard counts, fort TTLs, and
	// fort eviction-callback registration).
	decoder.InitDataCache()

	log.Infof("Golbat starting: revision=%s modified=%v built=%s", gitRevision, gitModified, buildTime)

	// Compile per-method proto decode engines (hyperpb arenas + PGO warmup)
	// now that config is loaded. No-op stub on unsupported platforms.
	initProtoEngines()

	// engineFor() silently falls back to "std" for any [proto_engine] value
	// it doesn't recognize; warn loudly here so a config typo (e.g.
	// "hyperbp") doesn't go unnoticed.
	warnInvalidProtoEngineValues()

	// decoder cannot import package main (where engine selection lives), so
	// the GMO path's disk-encounter cache-replay decode capability
	// (gmo_decode.go) is wired in via a function value, the same pattern as
	// SetWebhooksSender/SetStatsCollector below.
	decoder.SetDiskEncounterDecoder(func(payload []byte, process func(pogoshim.DiskEncounterOutProto) string) (string, error) {
		return decodeWithArena(engMethodDiskEncounter, diskEncounterEngine, payload, pogoshim.AsDiskEncounterOutProto, process)
	})

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

	needsOhbem := cfg.Pvp.Enabled
	needsMasterfile := needsOhbem || cfg.Weather.ProactiveIVSwitching
	if needsMasterfile {
		if err := decoder.EnsureMasterFileData(); err != nil {
			log.Fatalf("Unable to initialise MasterFile: %v", err)
		}
	}

	if needsOhbem {
		decoder.InitialiseOhbem()
	}
	if cfg.Weather.ProactiveIVSwitching {
		decoder.InitProactiveIVSwitchSem()
	}

	if needsMasterfile {
		if err := decoder.WatchMasterFileData(); err != nil {
			log.Warnf("MasterFile watcher failed: %v", err)
		}
	}
	decoder.LoadStatsGeofences()
	decoder.InitWriteBehindQueue(ctx, dbDetails)
	initRawProcessingLimiter()
	initSlowDbQueryLogging()
	decoder.StartWorkerBacklogReporter()
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

	if n := cfg.Tuning.GoGCPercent; n > 0 {
		prev := debug.SetGCPercent(n)
		log.Infof("GC target set to %d%% (was %d%%): cycles ~%.1fx less frequent, peak heap up to ~%.1fx live",
			n, prev, float64(100+n)/float64(100+prev), 1+float64(n)/100)
	}
	if m := cfg.Tuning.GoMemLimitMiB; m > 0 {
		debug.SetMemoryLimit(int64(m) << 20)
		log.Infof("Go memory limit set to %d MiB", m)
	}

	if cfg.Cleanup.Pokemon && (!cfg.PokemonMemoryOnly || cfg.PreserveInMemoryPokemon) {
		StartDatabaseArchiver(db)
	}

	if cfg.Cleanup.Incidents {
		StartIncidentExpiry(db)
	}

	if cfg.Cleanup.StationBattles {
		StartStationBattleExpiry(db)
	}

	if cfg.Cleanup.Tappables {
		StartTappableExpiry(db)
	}

	if cfg.Cleanup.Quests {
		StartQuestExpiry(dbDetails)
	}

	if cfg.Cleanup.Stats {
		StartStatsExpiry(db)
	}

	// init fort tracker for memory-based fort cleanup
	staleThreshold := cfg.Cleanup.FortsStaleThreshold
	if staleThreshold <= 0 {
		staleThreshold = 3600 // def 1 hour
	}
	minMissCount := cfg.Cleanup.FortsMinMissCount
	if minMissCount <= 0 {
		minMissCount = 1
	}
	decoder.InitFortTracker(staleThreshold, minMissCount)

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

			// Initialize gRPC Prometheus metrics if enabled
			var grpcServerOpts []grpc.ServerOption
			if cfg.Prometheus.Enabled {
				srvMetrics := grpcprom.NewServerMetrics(
					grpcprom.WithServerHandlingTimeHistogram(
						grpcprom.WithHistogramBuckets(cfg.Prometheus.BucketSize),
					),
				)
				grpcServerOpts = append(grpcServerOpts,
					grpc.UnaryInterceptor(srvMetrics.UnaryServerInterceptor()),
					grpc.StreamInterceptor(srvMetrics.StreamServerInterceptor()),
				)
				srvMetrics.InitializeMetrics(grpc.NewServer(grpcServerOpts...))
			}

			s := grpc.NewServer(grpcServerOpts...)
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
	r.GET("/version", GetVersion)

	apiGroup := r.Group("/api", AuthRequired())
	apiGroup.GET("/health", GetHealth)

	apiGroup.POST("/pokemon/scan", PokemonScan)

	debugGroup := r.Group("/debug")

	if cfg.Tuning.ProfileRoutes {
		// We register the pprof routes ourselves (rather than importing
		// gin-contrib/pprof) so they stay behind AuthRequired(). Route set
		// mirrors net/http/pprof's own registration: the index plus every named
		// profile (named ones go through pprof.Handler, the index serves its HTML
		// listing and links to them).
		pprofGroup := debugGroup.Group("/pprof", AuthRequired())
		pprofHandler := func(h http.HandlerFunc) gin.HandlerFunc {
			return func(c *gin.Context) { h(c.Writer, c.Request) }
		}
		pprofGroup.GET("/", pprofHandler(pprof.Index))
		pprofGroup.GET("/cmdline", pprofHandler(pprof.Cmdline))
		pprofGroup.GET("/profile", pprofHandler(pprof.Profile))
		pprofGroup.GET("/symbol", pprofHandler(pprof.Symbol))
		pprofGroup.POST("/symbol", pprofHandler(pprof.Symbol))
		pprofGroup.GET("/trace", pprofHandler(pprof.Trace))
		pprofGroup.GET("/allocs", pprofHandler(pprof.Handler("allocs").ServeHTTP))
		pprofGroup.GET("/block", pprofHandler(pprof.Handler("block").ServeHTTP))
		pprofGroup.GET("/goroutine", pprofHandler(pprof.Handler("goroutine").ServeHTTP))
		pprofGroup.GET("/heap", pprofHandler(pprof.Handler("heap").ServeHTTP))
		pprofGroup.GET("/mutex", pprofHandler(pprof.Handler("mutex").ServeHTTP))
		pprofGroup.GET("/threadcreate", pprofHandler(pprof.Handler("threadcreate").ServeHTTP))
		if config.Config.Tuning.ProfileContention {
			runtime.SetBlockProfileRate(1)
			runtime.SetMutexProfileFraction(1)
		}
	}

	humaAPI := setupHumaAPI(r)
	registerHumaRoutes(humaAPI)
	registerFortScanRoutes(humaAPI)
	registerPokemonReadRoutes(humaAPI)
	registerTier3Routes(humaAPI)
	registerTier4Routes(humaAPI)

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: r,
		// Reap idle keep-alive connections so each one does not pin a goroutine
		// and a file descriptor indefinitely (net/http holds an open connection
		// open until the client closes it otherwise).
		IdleTimeout: 60 * time.Second,
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
			log.Warn("http server drain timed out after 5s (in-flight requests dropped); continuing shutdown — write-behind flush and pokemon preservation still run")
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
