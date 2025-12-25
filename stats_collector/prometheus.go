package stats_collector

import (
	"database/sql"
	"golbat/util"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"gopkg.in/guregu/null.v4"

	"golbat/geo"
)

var (
	ns = "golbat"

	rawRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "raw_requests",
			Help:      "Total number of requests received by raw endpoint",
		},
		[]string{"status", "message"},
	)

	decodeMethods = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "decode_methods",
			Help:      "Total number of decoded methods",
		},
		[]string{"status", "message", "method"},
	)
	decodeFortDetails = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "decode_fort_details",
			Help:      "Total number of decoded: FortDetails",
		},
		[]string{"status", "message"},
	)
	decodeGetMapForts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "decode_get_map_forts",
			Help:      "Total number of decoded: GMF",
		},
		[]string{"status", "message"},
	)
	decodeGetGymInfo = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "decode_get_gym_info",
			Help:      "Total number of decoded: GetGymInfo",
		},
		[]string{"status", "message"},
	)
	decodeEncounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "decode_encounter",
			Help:      "Total number of decoded: Encounter",
		},
		[]string{"status", "message"},
	)
	decodeDiskEncounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "decode_disk_encounter",
			Help:      "Total number of decoded DiskEncounter",
		},
		[]string{"status", "message"},
	)
	decodeQuest = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "decode_quest",
			Help:      "Total number of decoded: Quests",
		},
		[]string{"status", "message"},
	)
	decodeSocialActionWithRequest = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "decode_social_action_with_request",
			Help:      "Total number of decoded: SocialActionWithRequest",
		},
		[]string{"status", "message"},
	)
	decodeGetFriendDetails = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "decode_get_friend_details",
			Help:      "Total number of decoded: GetFriendDetails",
		},
		[]string{"status", "message"},
	)
	decodeSearchPlayer = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "decode_search_player",
			Help:      "Total number of decoded: SearchPlayer",
		},
		[]string{"status", "message"},
	)
	decodeGMO = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "decode_gmo",
			Help:      "Total number of decoded: GMO",
		},
		[]string{"status", "message"},
	)
	decodeGMOType = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "decode_gmo_type",
			Help:      "Total number of decoded: GMO sub-cat",
		},
		[]string{"type"},
	)
	decodeStartIncident = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "decode_start_incident",
			Help:      "Total number of decoded: StartIncident",
		},
		[]string{"status", "message"},
	)
	decodeOpenInvasion = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "decode_open_invasion",
			Help:      "Total number of decoded: OpenInvasion",
		},
		[]string{"status", "message"},
	)

	pokemonStatsResetCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "pokemon_stats_reset_count",
			Help:      "Total number of stats reset",
		},
		[]string{"area"},
	)

	pokemonCountNew = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "pokemon_count_new",
			Help:      "Total new Pokemon count",
		},
		[]string{"area"},
	)
	pokemonCountIv = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "pokemon_count_iv",
			Help:      "Total Pokemon with IV",
		},
		[]string{"area"},
	)
	pokemonCountShiny = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "pokemon_count_shiny",
			Help:      "Total Shiny count by pokemon dex id",
		},
		[]string{"pokemon_id", "form_id"},
	)
	pokemonCountNonShiny = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "pokemon_count_non_shiny",
			Help:      "Total Non-Shiny count by pokemon dex id",
		},
		[]string{"pokemon_id", "form_id"},
	)
	pokemonCountShundo = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "pokemon_count_shundo",
			Help:      "Total Shundo count",
		},
	)
	pokemonCountSnundo = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "pokemon_count_snundo",
			Help:      "Total Snundo count",
		},
	)
	pokemonCountHundo = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "pokemon_count_hundo",
			Help:      "Total Hundo count",
		},
		[]string{"area"},
	)
	pokemonCountNundo = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "pokemon_count_nundo",
			Help:      "Total Nundo count",
		},
		[]string{"area"},
	)

	verifiedPokemonTTL = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "verified_pokemon_ttl",
			Help:      "Verified Pokemon count by area, type and with a flag stating if a Pokemon had TTL over 30 minutes",
		},
		[]string{"area", "type", "above30"},
	)

	verifiedPokemonTTLCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "verified_pokemon_ttl_counter",
			Help:      "Verified Pokemon counter by area, type and with a flag stating if a Pokemon had TTL over 30 minutes",
		},
		[]string{"area", "type", "above30"},
	)

	raidCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "raid_count",
			Help:      "Total number of created raids",
		},
		[]string{"area", "level"},
	)
	fortCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "fort_count",
			Help:      "Total number of forts additions, removals and updates",
		},
		[]string{"area", "type", "change"},
	)
	incidentCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "incident_count",
			Help:      "Total number of incidents updates",
		},
		[]string{"area"},
	)
	duplicateEncounters = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "duplicate_encounters",
			Help:      "Total number of duplicate encounters",
		},
		[]string{"sameacct"},
	)
	dbQueries = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "database_queries",
			Help:      "Total number of database queries by query and status",
		},
		[]string{"query", "status"},
	)

	// query updated stats
	gyms = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: ns,
			Name:      "gyms",
			Help:      "Current gyms grouped by team, in_battle",
		},
		[]string{"team", "in_battle"},
	)
	incidents = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: ns,
			Name:      "incidents",
			Help:      "Current incidents grouped by kind, confirmed",
		},
		[]string{"kind", "confirmed"},
	)
	pokemons = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: ns,
			Name:      "pokemons",
			Help:      "Current Pokemons grouped by has_iv, seen_type",
		},
		[]string{"has_iv", "seen_type"},
	)
	lures = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: ns,
			Name:      "lures",
			Help:      "Current lures grouped by type",
		},
		[]string{"type"},
	)
	quests = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: ns,
			Name:      "quests",
			Help:      "Current quests",
		},
		[]string{"ar"},
	)
	raids = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: ns,
			Name:      "raids",
			Help:      "Current raids grouped by level",
		},
		[]string{"level"},
	)
	maxBattleCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "max_battle_count",
			Help:      "Total number of created max battles",
		},
		[]string{"area", "level"},
	)
	fortChange = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: ns,
			Name:      "fort_change",
			Help:      "Total number of fort changes (deletions and conversions)",
		},
		[]string{"change_type"},
	)
)

var _ StatsCollector = (*promCollector)(nil)

type promCollector struct {
}

func (col *promCollector) IncRawRequests(status, message string) {
	rawRequests.WithLabelValues(status, message).Inc()
}

func (col *promCollector) IncDecodeMethods(status, message, method string) {
	decodeMethods.WithLabelValues(status, message, method).Inc()
}

func (col *promCollector) IncDecodeFortDetails(status, message string) {
	decodeFortDetails.WithLabelValues(status, message).Inc()
}

func (col *promCollector) IncDecodeGetMapForts(status, message string) {
	decodeGetMapForts.WithLabelValues(status, message).Inc()
}

func (col *promCollector) IncDecodeGetGymInfo(status, message string) {
	decodeGetGymInfo.WithLabelValues(status, message).Inc()
}

func (col *promCollector) IncDecodeEncounter(status, message string) {
	decodeEncounter.WithLabelValues(status, message).Inc()
}

func (col *promCollector) IncDecodeDiskEncounter(status, message string) {
	decodeDiskEncounter.WithLabelValues(status, message).Inc()
}

func (col *promCollector) IncDecodeQuest(status, message string) {
	decodeQuest.WithLabelValues(status, message).Inc()
}

func (col *promCollector) IncDecodeSocialActionWithRequest(status, message string) {
	decodeSocialActionWithRequest.WithLabelValues(status, message).Inc()
}

func (col *promCollector) IncDecodeGetFriendDetails(status, message string) {
	decodeGetFriendDetails.WithLabelValues(status, message).Inc()
}

func (col *promCollector) IncDecodeSearchPlayer(status, message string) {
	decodeSearchPlayer.WithLabelValues(status, message).Inc()
}

func (col *promCollector) IncDecodeGMO(status, message string) {
	decodeGMO.WithLabelValues(status, message).Inc()
}

func (col *promCollector) AddDecodeGMOType(typ string, value float64) {
	decodeGMOType.WithLabelValues(typ).Add(value)
}

func (col *promCollector) IncDecodeStartIncident(status, message string) {
	decodeStartIncident.WithLabelValues(status, message).Inc()
}

func (col *promCollector) IncDecodeOpenInvasion(status, message string) {
	decodeOpenInvasion.WithLabelValues(status, message).Inc()
}

func (col *promCollector) AddPokemonStatsResetCount(area string, val float64) {
	pokemonStatsResetCount.WithLabelValues(area).Add(val)
}

func (col *promCollector) IncPokemonCountNew(area string) {
	pokemonCountNew.WithLabelValues(area).Inc()
}

func (col *promCollector) IncPokemonCountIv(area string) {
	pokemonCountIv.WithLabelValues(area).Inc()
}

func (col *promCollector) IncPokemonCountShiny(pokemonId, formId string) {
	pokemonCountShiny.WithLabelValues(pokemonId, formId).Inc()
}

func (col *promCollector) IncPokemonCountNonShiny(pokemonId, formId string) {
	pokemonCountNonShiny.WithLabelValues(pokemonId, formId).Inc()
}

func (col *promCollector) IncPokemonCountShundo() {
	pokemonCountShundo.Inc()
}

func (col *promCollector) IncPokemonCountSnundo() {
	pokemonCountSnundo.Inc()
}

func (col *promCollector) IncPokemonCountHundo(area string) {
	pokemonCountHundo.WithLabelValues(area).Inc()
}

func (col *promCollector) IncPokemonCountNundo(area string) {
	pokemonCountNundo.WithLabelValues(area).Inc()
}

func (col *promCollector) UpdateVerifiedTtl(area geo.AreaName, seenType null.String, expireTimestamp null.Int) {
	remainingTtlMin := (expireTimestamp.ValueOrZero() - time.Now().Unix()) / 60
	var seenTypeStr = seenType.String
	above30 := "0"

	if remainingTtlMin < 0 {
		return
	}

	// set above30 when TTL is over 30 minutes
	// depending on the route times can be unreliable
	if remainingTtlMin > 30 {
		above30 = "1"
	}

	areaName := area.String()
	verifiedPokemonTTL.WithLabelValues(areaName, seenTypeStr, above30).Add(float64(remainingTtlMin))
	verifiedPokemonTTLCounter.WithLabelValues(areaName, seenTypeStr, above30).Inc()
}

func (col *promCollector) UpdateRaidCount(areas []geo.AreaName, raidLevel int64) {
	processed := make(map[string]bool)
	for _, area := range areas {
		areaName := area.String()
		if !processed[areaName] {
			raidCount.WithLabelValues(areaName, strconv.FormatInt(raidLevel, 10)).Inc()
			processed[areaName] = true
		}
	}
}

func (col *promCollector) UpdateFortCount(areas []geo.AreaName, fortType string, changeType string) {
	processed := make(map[string]bool)
	for _, area := range areas {
		areaName := area.String()
		if !processed[areaName] {
			fortCount.WithLabelValues(areaName, fortType, changeType).Inc()
			processed[areaName] = true
		}
	}
}

func (col *promCollector) UpdateIncidentCount(areas []geo.AreaName) {
	processed := make(map[string]bool)
	for _, area := range areas {
		areaName := area.String()
		if !processed[areaName] {
			incidentCount.WithLabelValues(areaName).Inc()
			processed[areaName] = true
		}
	}
}

func (col *promCollector) IncDuplicateEncounters(sameAccount bool) {
	var v string
	if sameAccount {
		v = "1"
	} else {
		v = "0"
	}
	duplicateEncounters.WithLabelValues(v).Inc()
}

func (col *promCollector) IncDbQuery(query string, err error) {
	var status string

	if err != nil && err != sql.ErrNoRows {
		status = "error"
	} else {
		status = "success"
	}
	dbQueries.WithLabelValues(query, status).Inc()
}

func (col *promCollector) SetGyms(teamId int8, inBattle bool, count float64) {
	var inBattleStr string
	teamIdString := "Unown"

	if name, ok := util.TeamIdToName[teamId]; ok {
		teamIdString = name
	}

	if inBattle {
		inBattleStr = "1"
	} else {
		inBattleStr = "0"
	}
	gyms.WithLabelValues(teamIdString, inBattleStr).Set(count)
}

func (col *promCollector) SetRaids(level int64, count float64) {
	raids.WithLabelValues(strconv.FormatInt(level, 10)).Set(count)
}

func (col *promCollector) SetIncidents(kind int8, confirmed bool, count float64) {
	var confirmedStr string
	kindStr := "Unown"

	if name, ok := util.IncidentTypeToName[kind]; ok {
		kindStr = name
	}

	if confirmed {
		confirmedStr = "1"
	} else {
		confirmedStr = "0"
	}

	incidents.WithLabelValues(kindStr, confirmedStr).Set(count)
}

func (col *promCollector) SetLures(lureId int32, count float64) {
	lureStr := "Unown"
	if name, ok := util.LureIdToName[lureId]; ok {
		lureStr = name
	}

	lures.WithLabelValues(lureStr).Set(count)
}

func (col *promCollector) SetQuests(ar float64, noAr float64) {
	quests.WithLabelValues("1").Set(ar)
	quests.WithLabelValues("0").Set(noAr)
}

func (col *promCollector) IncPokemons(hasIv bool, seenType null.String) {
	hasIvStr := "1"
	if !hasIv {
		hasIvStr = "0"
	}

	pokemons.WithLabelValues(hasIvStr, seenType.ValueOrZero()).Inc()
}

func (col *promCollector) DecPokemons(hasIv bool, seenType null.String) {
	hasIvStr := "1"
	if !hasIv {
		hasIvStr = "0"
	}

	pokemons.WithLabelValues(hasIvStr, seenType.ValueOrZero()).Dec()
}

func (col *promCollector) UpdateMaxBattleCount(areas []geo.AreaName, level int64) {
	processed := make(map[string]bool)
	for _, area := range areas {
		areaName := area.String()
		if !processed[areaName] {
			maxBattleCount.WithLabelValues(areaName, strconv.FormatInt(level, 10)).Inc()
			processed[areaName] = true
		}
	}
}

func (col *promCollector) IncFortChange(changeType string) {
	fortChange.WithLabelValues(changeType).Inc()
}

func initPrometheus() {
	prometheus.MustRegister(
		rawRequests, decodeMethods, decodeFortDetails, decodeGetMapForts, decodeGetGymInfo, decodeEncounter,
		decodeDiskEncounter, decodeQuest, decodeSocialActionWithRequest, decodeGMO, decodeGMOType,
		decodeGetFriendDetails, decodeSearchPlayer, decodeOpenInvasion, decodeStartIncident,

		pokemonStatsResetCount,

		pokemonCountNew, pokemonCountIv, pokemonCountHundo, pokemonCountNundo,
		pokemonCountShiny, pokemonCountNonShiny, pokemonCountShundo, pokemonCountSnundo,

		verifiedPokemonTTL, verifiedPokemonTTLCounter, raidCount, fortCount, incidentCount, maxBattleCount, fortChange,
		duplicateEncounters, dbQueries,

		gyms, incidents, pokemons, lures, quests, raids,
	)
}

var initOnce sync.Once

func NewPrometheusCollector() StatsCollector {
	initOnce.Do(initPrometheus)
	return &promCollector{}
}
