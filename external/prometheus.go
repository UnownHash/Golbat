package external

import (
	"github.com/Depado/ginprom"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	"golbat/config"
	"golbat/geo"
	"golbat/pogo"
	"gopkg.in/guregu/null.v4"
	"strconv"
	"time"
)

var (
	RawRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "raw_requests",
			Help: "Total number of requests received by raw endpoint",
		},
		[]string{"status", "message"},
	)

	DecodeMethods = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "decode_methods",
			Help: "Total number of decoded methods",
		},
		[]string{"status", "message", "method"},
	)
	DecodeFortDetails = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "decode_fort_details",
			Help: "Total number of decoded: FortDetails",
		},
		[]string{"status", "message"},
	)
	DecodeGetMapForts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "decode_get_map_forts",
			Help: "Total number of decoded: GMF",
		},
		[]string{"status", "message"},
	)
	DecodeGetGymInfo = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "decode_get_gym_info",
			Help: "Total number of decoded: GetGymInfo",
		},
		[]string{"status", "message"},
	)
	DecodeEncounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "decode_encounter",
			Help: "Total number of decoded: Encounter",
		},
		[]string{"status", "message"},
	)
	DecodeDiskEncounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "decode_disk_encounter",
			Help: "Total number of decoded DiskEncounter",
		},
		[]string{"status", "message"},
	)
	DecodeQuest = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "decode_quest",
			Help: "Total number of decoded: Quests",
		},
		[]string{"status", "message"},
	)
	DecodeSocialActionWithRequest = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "decode_social_action_with_request",
			Help: "Total number of decoded: SocialActionWithRequest",
		},
		[]string{"status", "message"},
	)
	DecodeGetFriendDetails = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "decode_get_friend_details",
			Help: "Total number of decoded: GetFriendDetails",
		},
		[]string{"status", "message"},
	)
	DecodeSearchPlayer = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "decode_search_player",
			Help: "Total number of decoded: SearchPlayer",
		},
		[]string{"status", "message"},
	)
	DecodeGMO = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "decode_gmo",
			Help: "Total number of decoded: GMO",
		},
		[]string{"status", "message"},
	)
	DecodeGMOType = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "decode_gmo_type",
			Help: "Total number of decoded: GMO sub-cat",
		},
		[]string{"type"},
	)
	DecodeStartIncident = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "decode_start_incident",
			Help: "Total number of decoded: StartIncident",
		},
		[]string{"status", "message"},
	)
	DecodeOpenInvasion = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "decode_open_invasion",
			Help: "Total number of decoded: OpenInvasion",
		},
		[]string{"status", "message"},
	)

	PokemonStatsResetCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pokemon_stats_reset_count",
			Help: "Total number of stats reset",
		},
		[]string{"area"},
	)

	PokemonCountNew = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pokemon_count_new",
			Help: "Total new Pokemon count",
		},
		[]string{"area"},
	)
	PokemonCountIv = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pokemon_count_iv",
			Help: "Total Pokemon with IV",
		},
		[]string{"area"},
	)
	/*
		PokemonCountShiny = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "pokemon_count_shiny",
				Help: "Total Shiny count",
			},
			[]string{"area"},
		)
		PokemonCountShundo = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "pokemon_count_shundo",
				Help: "Total Shundo count",
			},
			[]string{"area"},
		)
		PokemonCountSnundo = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "pokemon_count_snundo",
				Help: "Total Snundo count",
			},
			[]string{"area"},
		)
	*/
	PokemonCountHundo = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pokemon_count_hundo",
			Help: "Total Hundo count",
		},
		[]string{"area"},
	)
	PokemonCountNundo = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pokemon_count_nundo",
			Help: "Total Nundo count",
		},
		[]string{"area"},
	)

	VerifiedPokemonTTL = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "verified_pokemon_ttl",
			Help: "Verified Pokemon count by type",
		},
		[]string{"area", "type"},
	)

	VerifiedPokemonTTLCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "verified_pokemon_ttl_counter",
			Help: "Verified Pokemon count by type counter",
		},
		[]string{"area", "type"},
	)

	RaidCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "raid_count",
			Help: "Total number of created raids",
		},
		[]string{"area", "level"},
	)
	GymCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gym_count",
			Help: "Total number of newly discovered gyms",
		},
		[]string{"area"},
	)
	FortCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fort_count",
			Help: "Total number of forts additions, removals and updates",
		},
		[]string{"area", "type", "change"},
	)
	IncidentCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "incident_count",
			Help: "Total number of incidents",
		},
		[]string{"area"},
	)
	WeatherCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "weather_count",
			Help: "Total number of weather updates by gameplay condition",
		},
		[]string{"area", "gameplay_condition"},
	)
)

func InitPrometheus(r *gin.Engine) {
	if config.Config.Prometheus.Enabled {
		log.Infof("Prometheus init")
		p := ginprom.New(
			ginprom.Engine(r),
			ginprom.Subsystem("gin"),
			ginprom.Path("/metrics"),
			ginprom.Token(config.Config.Prometheus.Token),
			ginprom.BucketSize(config.Config.Prometheus.BucketSize),
		)

		r.Use(p.Instrument())

		prometheus.MustRegister(
			RawRequests, DecodeMethods, DecodeFortDetails, DecodeGetMapForts, DecodeGetGymInfo, DecodeEncounter,
			DecodeDiskEncounter, DecodeQuest, DecodeSocialActionWithRequest, DecodeGMO, DecodeGetFriendDetails,
			DecodeSearchPlayer, DecodeOpenInvasion, DecodeStartIncident, DecodeGMOType,

			PokemonStatsResetCount,

			PokemonCountNew, PokemonCountIv, PokemonCountHundo, PokemonCountNundo,

			VerifiedPokemonTTL, VerifiedPokemonTTLCounter, RaidCount, FortCount, IncidentCount, GymCount, WeatherCount,
		)
	}
}

func UpdateVerifiedTtl(area geo.AreaName, seenType null.String, expireTimestamp null.Int) {
	remainingTtlMin := (expireTimestamp.ValueOrZero() - time.Now().Unix()) / 60
	var seenTypeStr = seenType.String

	if remainingTtlMin < 0 {
		return
	}

	VerifiedPokemonTTL.WithLabelValues(area.String(), seenTypeStr).Add(float64(remainingTtlMin))
	VerifiedPokemonTTLCounter.WithLabelValues(area.String(), seenTypeStr).Inc()
}

func UpdateGymCount(areas []geo.AreaName) {
	processed := make(map[string]bool)
	for _, area := range areas {
		if !processed[area.String()] {
			GymCount.WithLabelValues(area.String()).Inc()
			processed[area.String()] = true
		}
	}
}

func UpdateRaidCount(areas []geo.AreaName, raidLevel int64) {
	processed := make(map[string]bool)
	for _, area := range areas {
		if !processed[area.String()] {
			RaidCount.WithLabelValues(area.String(), strconv.FormatInt(raidLevel, 10)).Inc()
			processed[area.String()] = true
		}
	}
}

func UpdateFortCount(areas []geo.AreaName, fortType string, changeType string) {
	processed := make(map[string]bool)
	for _, area := range areas {
		if !processed[area.String()] {
			FortCount.WithLabelValues(area.String(), fortType, changeType).Inc()
			processed[area.String()] = true
		}
	}
}

func UpdateIncidentCount(areas []geo.AreaName) {
	processed := make(map[string]bool)
	for _, area := range areas {
		if !processed[area.String()] {
			IncidentCount.WithLabelValues(area.String()).Inc()
			processed[area.String()] = true
		}
	}
}

func UpdateWeatherCount(areas []geo.AreaName, gameplayCondition int64) {
	gameplayConditionStr := "UNKNOWN"
	if _, ok := pogo.GameplayWeatherProto_WeatherCondition_name[int32(gameplayCondition)]; ok {
		gameplayConditionStr = pogo.GameplayWeatherProto_WeatherCondition_name[int32(gameplayCondition)]
	}

	processed := make(map[string]bool)
	for _, area := range areas {
		if !processed[area.String()] {
			WeatherCount.WithLabelValues(area.String(), gameplayConditionStr).Inc()
			processed[area.String()] = true
		}
	}
}
