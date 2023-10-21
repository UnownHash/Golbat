package stats_collector

import (
	"github.com/Depado/ginprom"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"gopkg.in/guregu/null.v4"

	"golbat/config"
	"golbat/geo"
)

type StatsCollector interface {
	IncRawRequests(status, message string)
	IncDecodeMethods(status, message, method string)
	IncDecodeFortDetails(status, message string)
	IncDecodeGetMapForts(status, message string)
	IncDecodeGetGymInfo(status, message string)
	IncDecodeEncounter(status, messages string)
	IncDecodeDiskEncounter(status, message string)
	IncDecodeQuest(status, message string)
	IncDecodeSocialActionWithRequest(status, message string)
	IncDecodeGetFriendDetails(status, message string)
	IncDecodeSearchPlayer(status, message string)
	IncDecodeGMO(status, message string)
	AddDecodeGMOType(typ string, value float64)
	IncDecodeStartIncident(status, message string)
	IncDecodeOpenInvasion(status, message string)
	AddPokemonStatsResetCount(area string, val float64)
	IncPokemonCountNew(area string)
	IncPokemonCountIv(area string)
	/*
		IncPokemonCountShiny(area string)
		IncPokemonCountShundo(area string)
		IncPokemonCountSnundo(area string)
	*/
	IncPokemonCountHundo(area string)
	IncPokemonCountNundo(area string)
	UpdateVerifiedTtl(area geo.AreaName, seenType null.String, expireTimestamp null.Int)
	UpdateRaidCount(areas []geo.AreaName, raidLevel int64)
	UpdateFortCount(areas []geo.AreaName, fortType string, changeType string)
	UpdateIncidentCount(areas []geo.AreaName)
}

type Config interface {
	GetPrometheus() config.Prometheus
}

func GetStatsCollector(cfg Config, ginEngine *gin.Engine) StatsCollector {
	promSettings := cfg.GetPrometheus()
	if !promSettings.Enabled {
		return NewNoopStatsCollector()
	}
	log.Infof("Prometheus init")
	if ginEngine != nil {
		p := ginprom.New(
			ginprom.Engine(ginEngine),
			ginprom.Subsystem("gin"),
			ginprom.Path("/metrics"),
			ginprom.Token(promSettings.Token),
			ginprom.BucketSize(promSettings.BucketSize),
		)
		ginEngine.Use(p.Instrument())
	}
	return NewPrometheusCollector()
}
