package stats_collector

import (
	"golbat/config"
	"golbat/geo"

	"github.com/Depado/ginprom"
	"github.com/gin-gonic/gin"
	"github.com/guregu/null/v6"
	log "github.com/sirupsen/logrus"
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
	IncPokemonCountShiny(pokemonId, formId string)
	IncPokemonCountNonShiny(pokemonId, formId string)
	IncPokemonCountShundo()
	IncPokemonCountSnundo()
	IncPokemonCountHundo(area string)
	IncPokemonCountNundo(area string)
	UpdateVerifiedTtl(area geo.AreaName, seenType null.String, expireTimestamp null.Int)
	UpdateRaidCount(areas []geo.AreaName, raidLevel int64)
	UpdateFortCount(areas []geo.AreaName, fortType string, changeType string)
	UpdateIncidentCount(areas []geo.AreaName)
	IncDuplicateEncounters(sameAccount bool)
	IncDespawnWrapClamped()

	// IncDerivationChecked counts a real encounter whose raw observed stats
	// were compared against a fresh encounter-id derivation, by sighting
	// path. Strong Pokemon are excluded: the ordinary boosted floor this
	// derivation models does not apply to them, so every one would register
	// as a disagreement and hide a real regression inside
	// derivation_disagreed_total's baseline.
	IncDerivationChecked(path string)
	// IncDerivationDisagreed counts a checked encounter whose observed stats
	// did not match the derivation, by sighting path. This should sit at
	// approximately zero; a rising rate means the derivation has stopped
	// matching the game.
	IncDerivationDisagreed(path string)
	// IncDerivationMoveMismatch counts a checked encounter whose observed
	// move_1/move_2 did not match the derivation, by sighting path. Also
	// counted by IncDerivationDisagreed; tracked separately so the
	// move-specific rate is visible without grepping logs. Counted only when
	// the species/form's move pools resolved, and per slot only when the
	// observed move is in the pool the derivation drew from: in either case
	// the derivation has no opinion rather than a disagreement, as with
	// POKEMON_SIZE_UNSET. Out-of-pool moves have no counter of their own; the
	// logged row carries both moves, which is enough to identify them.
	IncDerivationMoveMismatch(path string)
	// IncDerivationBoostBranchDivergence counts a checked, non-strong
	// encounter whose observed IVs matched the OPPOSITE weather-boost branch
	// (ditto_branch "opposite" — decoder's dittoBranchFor) rather than the
	// one scan.Weather implies, by sighting path. It is evidence of a Ditto,
	// not a confirmed one (see dittoBranch). Not counted by
	// IncDerivationDisagreed: such a row is a Ditto whose boost state
	// follows its own typing rather than its disguise's, and counting it
	// would give derivation_disagreed_total a floor that masks real
	// regressions.
	IncDerivationBoostBranchDivergence(path string)

	// IncDespawnPhaseDiverged counts a TTH sighting whose despawn instant,
	// modulo the half hour, is more than decoder's despawnPhaseTolerance
	// from the phase derived from its spawnpoint id. Every TTH sighting is
	// checked.
	IncDespawnPhaseDiverged()

	IncDbQuery(query string, err error)
	SetGyms(teamId int8, inBattle bool, count float64)
	SetRaids(level int64, count float64)
	SetIncidents(kind int8, confirmed bool, count float64)
	SetLures(lureId int32, count float64)
	SetQuests(ar float64, noAr float64)
	IncPokemons(hasIv bool, seenType null.String)
	DecPokemons(hasIv bool, seenType null.String)
	UpdateMaxBattleCount(areas []geo.AreaName, level int64)
	IncFortChange(changeType string)

	// IncFieldClamped counts a value that arrived outside the range of the
	// column it is stored in, and was clamped to the boundary. Non-zero means
	// either the game protocol changed or a decode path is wrong.
	IncFieldClamped(field string)

	// Hot-path health metrics
	SetWorkerBacklog(worker string, depth float64)
	SetRawProcessingWaiting(waiting float64)
	IncRawPacketsShed()
	IncSlowDbQuery(caller string)
	IncStatsEventsDropped()
	AddCacheEvictionsDropped(cache string, n float64)
	ObserveDbQuery(caller string, seconds float64)
	ObserveApiScan(operation string, seconds float64)

	// Write-behind queue metrics
	SetWriteBehindQueueDepth(entityType string, depth float64)
	IncWriteBehindSquashed(entityType string)
	IncWriteBehindRateLimited(entityType string)
	IncWriteBehindErrors(entityType string)
	IncWriteBehindWrites(entityType string)
	ObserveWriteBehindLatency(entityType string, seconds float64)
	IncWriteBehindBatches(entityType string)
	ObserveWriteBehindBatchSize(entityType string, size float64)
	ObserveWriteBehindBatchTime(entityType string, seconds float64)

	// S2Cell batch metrics
	SetS2CellBatchSize(size int)
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
