package decoder

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"

	"golbat/config"
	"golbat/db"
	"golbat/decoder/writebehind"
	"golbat/stats_collector"
)

// S2CellData holds the data needed for an S2Cell write
type S2CellData struct {
	Id        uint64  `db:"id"`
	Latitude  float64 `db:"center_lat"`
	Longitude float64 `db:"center_lon"`
	Level     int64   `db:"level"`
	Updated   int64   `db:"updated"`
}

// Typed queues for each entity type - using native key types for efficiency
var (
	pokestopQueue   *writebehind.TypedQueue[string, PokestopData]
	gymQueue        *writebehind.TypedQueue[string, GymData]
	pokemonQueue    *writebehind.TypedQueue[uint64, PokemonData]
	spawnpointQueue *writebehind.TypedQueue[int64, SpawnpointData]
	routeQueue      *writebehind.TypedQueue[string, RouteData]
	tappableQueue   *writebehind.TypedQueue[uint64, TappableData]
	stationQueue    *writebehind.TypedQueue[string, StationData]
	incidentQueue   *writebehind.TypedQueue[string, IncidentData]
	s2cellQueue     *writebehind.TypedQueue[uint64, S2CellData]

	// QueueManager coordinates all queues
	queueManager *writebehind.QueueManager
)

// InitTypedQueues initializes all typed write-behind queues
func InitTypedQueues(ctx context.Context, dbDetails db.DbDetails, stats stats_collector.StatsCollector) {
	startupDelay := config.Config.Tuning.WriteBehindStartupDelay
	batchSize := config.Config.Tuning.WriteBehindBatchSize
	batchTimeout := time.Duration(config.Config.Tuning.WriteBehindBatchTimeoutMs) * time.Millisecond
	workerCount := config.Config.Tuning.WriteBehindWorkerCount

	if batchSize <= 0 {
		batchSize = 50
	}
	if batchTimeout <= 0 {
		batchTimeout = 100 * time.Millisecond
	}
	if workerCount <= 0 {
		workerCount = 50
	}

	// Shared limiter coordinates concurrency across all queues
	limiter := writebehind.NewSharedLimiter(workerCount)

	// Create queue manager
	queueManager = writebehind.NewQueueManager(startupDelay)

	// Create typed queues for each entity type - using native key types
	pokestopQueue = writebehind.NewTypedQueue(writebehind.TypedQueueConfig[string, PokestopData]{
		Name:                "pokestop",
		BatchSize:           batchSize,
		BatchTimeout:        batchTimeout,
		StartupDelaySeconds: startupDelay,
		Limiter:             limiter,
		Db:                  dbDetails,
		Stats:               stats,
		FlushFunc:           flushPokestopBatch,
		KeyFunc:             func(d PokestopData) string { return d.Id },
	})
	queueManager.Register(pokestopQueue)

	gymQueue = writebehind.NewTypedQueue(writebehind.TypedQueueConfig[string, GymData]{
		Name:                "gym",
		BatchSize:           batchSize,
		BatchTimeout:        batchTimeout,
		StartupDelaySeconds: startupDelay,
		Limiter:             limiter,
		Db:                  dbDetails,
		Stats:               stats,
		FlushFunc:           flushGymBatch,
		KeyFunc:             func(d GymData) string { return d.Id },
	})
	queueManager.Register(gymQueue)

	pokemonQueue = writebehind.NewTypedQueue(writebehind.TypedQueueConfig[uint64, PokemonData]{
		Name:                "pokemon",
		BatchSize:           batchSize,
		BatchTimeout:        batchTimeout,
		StartupDelaySeconds: startupDelay,
		Limiter:             limiter,
		Db:                  dbDetails,
		Stats:               stats,
		FlushFunc:           flushPokemonBatchTyped,
		KeyFunc:             func(d PokemonData) uint64 { return uint64(d.Id) },
	})
	queueManager.Register(pokemonQueue)

	spawnpointQueue = writebehind.NewTypedQueue(writebehind.TypedQueueConfig[int64, SpawnpointData]{
		Name:                "spawnpoint",
		BatchSize:           batchSize,
		BatchTimeout:        batchTimeout,
		StartupDelaySeconds: startupDelay,
		Limiter:             limiter,
		Db:                  dbDetails,
		Stats:               stats,
		FlushFunc:           flushSpawnpointBatch,
		KeyFunc:             func(d SpawnpointData) int64 { return d.Id },
	})
	queueManager.Register(spawnpointQueue)

	routeQueue = writebehind.NewTypedQueue(writebehind.TypedQueueConfig[string, RouteData]{
		Name:                "route",
		BatchSize:           batchSize,
		BatchTimeout:        batchTimeout,
		StartupDelaySeconds: startupDelay,
		Limiter:             limiter,
		Db:                  dbDetails,
		Stats:               stats,
		FlushFunc:           flushRouteBatch,
		KeyFunc:             func(d RouteData) string { return d.Id },
	})
	queueManager.Register(routeQueue)

	tappableQueue = writebehind.NewTypedQueue(writebehind.TypedQueueConfig[uint64, TappableData]{
		Name:                "tappable",
		BatchSize:           batchSize,
		BatchTimeout:        batchTimeout,
		StartupDelaySeconds: startupDelay,
		Limiter:             limiter,
		Db:                  dbDetails,
		Stats:               stats,
		FlushFunc:           flushTappableBatch,
		KeyFunc:             func(d TappableData) uint64 { return d.Id },
	})
	queueManager.Register(tappableQueue)

	stationQueue = writebehind.NewTypedQueue(writebehind.TypedQueueConfig[string, StationData]{
		Name:                "station",
		BatchSize:           batchSize,
		BatchTimeout:        batchTimeout,
		StartupDelaySeconds: startupDelay,
		Limiter:             limiter,
		Db:                  dbDetails,
		Stats:               stats,
		FlushFunc:           flushStationBatch,
		KeyFunc:             func(d StationData) string { return d.Id },
	})
	queueManager.Register(stationQueue)

	incidentQueue = writebehind.NewTypedQueue(writebehind.TypedQueueConfig[string, IncidentData]{
		Name:                "incident",
		BatchSize:           batchSize,
		BatchTimeout:        batchTimeout,
		StartupDelaySeconds: startupDelay,
		Limiter:             limiter,
		Db:                  dbDetails,
		Stats:               stats,
		FlushFunc:           flushIncidentBatch,
		KeyFunc:             func(d IncidentData) string { return d.Id },
	})
	queueManager.Register(incidentQueue)

	s2cellQueue = writebehind.NewTypedQueue(writebehind.TypedQueueConfig[uint64, S2CellData]{
		Name:                "s2cell",
		BatchSize:           100,
		BatchTimeout:        batchTimeout,
		StartupDelaySeconds: startupDelay,
		Limiter:             limiter,
		Db:                  dbDetails,
		Stats:               stats,
		FlushFunc:           flushS2CellBatch,
		KeyFunc:             func(d S2CellData) uint64 { return d.Id },
	})
	queueManager.Register(s2cellQueue)

	log.Infof("Typed write-behind queues initialized: startup_delay=%ds, batch_size=%d, batch_timeout=%dms, max_concurrent=%d",
		startupDelay, batchSize, batchTimeout.Milliseconds(), workerCount)

	// Warn if concurrency exceeds half of database pool size
	maxPool := config.Config.Database.MaxPool
	if workerCount > maxPool/2 {
		log.Warnf("Write-behind concurrency (%d) exceeds half of database pool size (%d). "+
			"Consider increasing database.max_pool or reducing tuning.write_behind_worker_count",
			workerCount, maxPool)
	}

	// Start the queue manager
	queueManager.Start(ctx)
}

// FlushTypedQueues flushes all typed queues (for shutdown)
func FlushTypedQueues() {
	if queueManager != nil {
		queueManager.Flush()
	}
}

// StopTypedQueues stops all typed queues gracefully
func StopTypedQueues() {
	if queueManager != nil {
		queueManager.Stop()
	}
}

// Flush functions for typed queues - receive []T directly, no type assertions needed

func flushPokestopBatch(ctx context.Context, dbDetails db.DbDetails, pokestops []PokestopData) error {
	_, err := dbDetails.GeneralDb.NamedExecContext(ctx, pokestopBatchUpsertQuery, pokestops)
	return err
}

func flushGymBatch(ctx context.Context, dbDetails db.DbDetails, gyms []GymData) error {
	_, err := dbDetails.GeneralDb.NamedExecContext(ctx, gymBatchUpsertQuery, gyms)
	return err
}

func flushPokemonBatchTyped(ctx context.Context, dbDetails db.DbDetails, pokemon []PokemonData) error {
	_, err := dbDetails.PokemonDb.NamedExecContext(ctx, pokemonBatchUpsertQuery, pokemon)
	return err
}

func flushSpawnpointBatch(ctx context.Context, dbDetails db.DbDetails, spawnpoints []SpawnpointData) error {
	_, err := dbDetails.GeneralDb.NamedExecContext(ctx, spawnpointBatchUpsertQuery, spawnpoints)
	return err
}

func flushRouteBatch(ctx context.Context, dbDetails db.DbDetails, routes []RouteData) error {
	_, err := dbDetails.GeneralDb.NamedExecContext(ctx, routeBatchUpsertQuery, routes)
	return err
}

func flushTappableBatch(ctx context.Context, dbDetails db.DbDetails, tappables []TappableData) error {
	_, err := dbDetails.GeneralDb.NamedExecContext(ctx, tappableBatchUpsertQuery, tappables)
	return err
}

func flushStationBatch(ctx context.Context, dbDetails db.DbDetails, stations []StationData) error {
	_, err := dbDetails.GeneralDb.NamedExecContext(ctx, stationBatchUpsertQuery, stations)
	return err
}

func flushIncidentBatch(ctx context.Context, dbDetails db.DbDetails, incidents []IncidentData) error {
	_, err := dbDetails.GeneralDb.NamedExecContext(ctx, incidentBatchUpsertQuery, incidents)
	return err
}

func flushS2CellBatch(ctx context.Context, dbDetails db.DbDetails, cells []S2CellData) error {
	_, err := dbDetails.GeneralDb.NamedExecContext(ctx, s2cellBatchUpsertQuery, cells)
	if err != nil {
		log.Errorf("flushS2CellBatch: %s", err)
	}
	statsCollector.IncDbQuery("insert s2cell", err)
	return err
}

// Batch upsert queries - using INSERT ... ON DUPLICATE KEY UPDATE
// This eliminates need to track isNewRecord

const pokestopBatchUpsertQuery = `
INSERT INTO pokestop (
	id, lat, lon, name, url, enabled, lure_expire_timestamp, last_modified_timestamp,
	quest_type, quest_timestamp, quest_target, quest_conditions, quest_rewards,
	quest_template, quest_title, quest_expiry, quest_reward_type, quest_item_id,
	quest_reward_amount, quest_pokemon_id, quest_pokemon_form_id,
	alternative_quest_type, alternative_quest_timestamp, alternative_quest_target,
	alternative_quest_conditions, alternative_quest_rewards, alternative_quest_template,
	alternative_quest_title, alternative_quest_expiry, alternative_quest_reward_type,
	alternative_quest_item_id, alternative_quest_reward_amount,
	alternative_quest_pokemon_id, alternative_quest_pokemon_form_id,
	cell_id, lure_id, deleted, sponsor_id, partner_id, ar_scan_eligible,
	power_up_points, power_up_level, power_up_end_timestamp, updated, first_seen_timestamp,
	description, showcase_focus, showcase_pokemon_id, showcase_pokemon_form_id,
	showcase_pokemon_type_id, showcase_ranking_standard, showcase_expiry, showcase_rankings
)
VALUES (
	:id, :lat, :lon, :name, :url, :enabled, :lure_expire_timestamp, :last_modified_timestamp,
	:quest_type, :quest_timestamp, :quest_target, :quest_conditions, :quest_rewards,
	:quest_template, :quest_title, :quest_expiry, :quest_reward_type, :quest_item_id,
	:quest_reward_amount, :quest_pokemon_id, :quest_pokemon_form_id,
	:alternative_quest_type, :alternative_quest_timestamp, :alternative_quest_target,
	:alternative_quest_conditions, :alternative_quest_rewards, :alternative_quest_template,
	:alternative_quest_title, :alternative_quest_expiry, :alternative_quest_reward_type,
	:alternative_quest_item_id, :alternative_quest_reward_amount,
	:alternative_quest_pokemon_id, :alternative_quest_pokemon_form_id,
	:cell_id, :lure_id, :deleted, :sponsor_id, :partner_id, :ar_scan_eligible,
	:power_up_points, :power_up_level, :power_up_end_timestamp, :updated, UNIX_TIMESTAMP(),
	:description, :showcase_focus, :showcase_pokemon_id, :showcase_pokemon_form_id,
	:showcase_pokemon_type_id, :showcase_ranking_standard, :showcase_expiry, :showcase_rankings
)
ON DUPLICATE KEY UPDATE
	lat = VALUES(lat),
	lon = VALUES(lon),
	name = VALUES(name),
	url = VALUES(url),
	enabled = VALUES(enabled),
	lure_expire_timestamp = VALUES(lure_expire_timestamp),
	last_modified_timestamp = VALUES(last_modified_timestamp),
	quest_type = VALUES(quest_type),
	quest_timestamp = VALUES(quest_timestamp),
	quest_target = VALUES(quest_target),
	quest_conditions = VALUES(quest_conditions),
	quest_rewards = VALUES(quest_rewards),
	quest_template = VALUES(quest_template),
	quest_title = VALUES(quest_title),
	quest_expiry = VALUES(quest_expiry),
	quest_reward_type = VALUES(quest_reward_type),
	quest_item_id = VALUES(quest_item_id),
	quest_reward_amount = VALUES(quest_reward_amount),
	quest_pokemon_id = VALUES(quest_pokemon_id),
	quest_pokemon_form_id = VALUES(quest_pokemon_form_id),
	alternative_quest_type = VALUES(alternative_quest_type),
	alternative_quest_timestamp = VALUES(alternative_quest_timestamp),
	alternative_quest_target = VALUES(alternative_quest_target),
	alternative_quest_conditions = VALUES(alternative_quest_conditions),
	alternative_quest_rewards = VALUES(alternative_quest_rewards),
	alternative_quest_template = VALUES(alternative_quest_template),
	alternative_quest_title = VALUES(alternative_quest_title),
	alternative_quest_expiry = VALUES(alternative_quest_expiry),
	alternative_quest_reward_type = VALUES(alternative_quest_reward_type),
	alternative_quest_item_id = VALUES(alternative_quest_item_id),
	alternative_quest_reward_amount = VALUES(alternative_quest_reward_amount),
	alternative_quest_pokemon_id = VALUES(alternative_quest_pokemon_id),
	alternative_quest_pokemon_form_id = VALUES(alternative_quest_pokemon_form_id),
	cell_id = VALUES(cell_id),
	lure_id = VALUES(lure_id),
	deleted = VALUES(deleted),
	sponsor_id = VALUES(sponsor_id),
	partner_id = VALUES(partner_id),
	ar_scan_eligible = VALUES(ar_scan_eligible),
	power_up_points = VALUES(power_up_points),
	power_up_level = VALUES(power_up_level),
	power_up_end_timestamp = VALUES(power_up_end_timestamp),
	updated = VALUES(updated),
	description = VALUES(description),
	showcase_focus = VALUES(showcase_focus),
	showcase_pokemon_id = VALUES(showcase_pokemon_id),
	showcase_pokemon_form_id = VALUES(showcase_pokemon_form_id),
	showcase_pokemon_type_id = VALUES(showcase_pokemon_type_id),
	showcase_ranking_standard = VALUES(showcase_ranking_standard),
	showcase_expiry = VALUES(showcase_expiry),
	showcase_rankings = VALUES(showcase_rankings)
`

const gymBatchUpsertQuery = `
INSERT INTO gym (
	id, lat, lon, name, url, last_modified_timestamp, raid_end_timestamp,
	raid_spawn_timestamp, raid_battle_timestamp, updated, raid_pokemon_id,
	guarding_pokemon_id, guarding_pokemon_display, available_slots, team_id,
	raid_level, enabled, ex_raid_eligible, in_battle, raid_pokemon_move_1,
	raid_pokemon_move_2, raid_pokemon_form, raid_pokemon_alignment, raid_pokemon_cp,
	raid_is_exclusive, cell_id, deleted, total_cp, first_seen_timestamp,
	raid_pokemon_gender, sponsor_id, partner_id, raid_pokemon_costume,
	raid_pokemon_evolution, ar_scan_eligible, power_up_level, power_up_points,
	power_up_end_timestamp, description, rsvps, defenders
)
VALUES (
	:id, :lat, :lon, :name, :url, :last_modified_timestamp, :raid_end_timestamp,
	:raid_spawn_timestamp, :raid_battle_timestamp, :updated, :raid_pokemon_id,
	:guarding_pokemon_id, :guarding_pokemon_display, :available_slots, :team_id,
	:raid_level, :enabled, :ex_raid_eligible, :in_battle, :raid_pokemon_move_1,
	:raid_pokemon_move_2, :raid_pokemon_form, :raid_pokemon_alignment, :raid_pokemon_cp,
	:raid_is_exclusive, :cell_id, :deleted, :total_cp, UNIX_TIMESTAMP(),
	:raid_pokemon_gender, :sponsor_id, :partner_id, :raid_pokemon_costume,
	:raid_pokemon_evolution, :ar_scan_eligible, :power_up_level, :power_up_points,
	:power_up_end_timestamp, :description, :rsvps, :defenders
)
ON DUPLICATE KEY UPDATE
	lat = VALUES(lat),
	lon = VALUES(lon),
	name = VALUES(name),
	url = VALUES(url),
	last_modified_timestamp = VALUES(last_modified_timestamp),
	raid_end_timestamp = VALUES(raid_end_timestamp),
	raid_spawn_timestamp = VALUES(raid_spawn_timestamp),
	raid_battle_timestamp = VALUES(raid_battle_timestamp),
	updated = VALUES(updated),
	raid_pokemon_id = VALUES(raid_pokemon_id),
	guarding_pokemon_id = VALUES(guarding_pokemon_id),
	guarding_pokemon_display = VALUES(guarding_pokemon_display),
	available_slots = VALUES(available_slots),
	team_id = VALUES(team_id),
	raid_level = VALUES(raid_level),
	enabled = VALUES(enabled),
	ex_raid_eligible = VALUES(ex_raid_eligible),
	in_battle = VALUES(in_battle),
	raid_pokemon_move_1 = VALUES(raid_pokemon_move_1),
	raid_pokemon_move_2 = VALUES(raid_pokemon_move_2),
	raid_pokemon_form = VALUES(raid_pokemon_form),
	raid_pokemon_alignment = VALUES(raid_pokemon_alignment),
	raid_pokemon_cp = VALUES(raid_pokemon_cp),
	raid_is_exclusive = VALUES(raid_is_exclusive),
	cell_id = VALUES(cell_id),
	deleted = VALUES(deleted),
	total_cp = VALUES(total_cp),
	raid_pokemon_gender = VALUES(raid_pokemon_gender),
	sponsor_id = VALUES(sponsor_id),
	partner_id = VALUES(partner_id),
	raid_pokemon_costume = VALUES(raid_pokemon_costume),
	raid_pokemon_evolution = VALUES(raid_pokemon_evolution),
	ar_scan_eligible = VALUES(ar_scan_eligible),
	power_up_level = VALUES(power_up_level),
	power_up_points = VALUES(power_up_points),
	power_up_end_timestamp = VALUES(power_up_end_timestamp),
	description = VALUES(description),
	rsvps = VALUES(rsvps),
	defenders = VALUES(defenders)
`

const pokemonBatchUpsertQuery = `
INSERT INTO pokemon (
	id, pokemon_id, lat, lon, spawn_id, expire_timestamp, atk_iv, def_iv, sta_iv,
	golbat_internal, iv, move_1, move_2, gender, form, cp, level, strong, weather,
	costume, weight, height, size, display_pokemon_id, is_ditto, pokestop_id,
	updated, first_seen_timestamp, changed, cell_id, expire_timestamp_verified,
	shiny, username, pvp, is_event, seen_type
)
VALUES (
	:id, :pokemon_id, :lat, :lon, :spawn_id, :expire_timestamp, :atk_iv, :def_iv, :sta_iv,
	:golbat_internal, :iv, :move_1, :move_2, :gender, :form, :cp, :level, :strong, :weather,
	:costume, :weight, :height, :size, :display_pokemon_id, :is_ditto, :pokestop_id,
	:updated, :first_seen_timestamp, :changed, :cell_id, :expire_timestamp_verified,
	:shiny, :username, :pvp, :is_event, :seen_type
)
ON DUPLICATE KEY UPDATE
	pokemon_id = VALUES(pokemon_id),
	lat = VALUES(lat),
	lon = VALUES(lon),
	spawn_id = VALUES(spawn_id),
	expire_timestamp = VALUES(expire_timestamp),
	atk_iv = VALUES(atk_iv),
	def_iv = VALUES(def_iv),
	sta_iv = VALUES(sta_iv),
	golbat_internal = VALUES(golbat_internal),
	iv = VALUES(iv),
	move_1 = VALUES(move_1),
	move_2 = VALUES(move_2),
	gender = VALUES(gender),
	form = VALUES(form),
	cp = VALUES(cp),
	level = VALUES(level),
	strong = VALUES(strong),
	weather = VALUES(weather),
	costume = VALUES(costume),
	weight = VALUES(weight),
	height = VALUES(height),
	size = VALUES(size),
	display_pokemon_id = VALUES(display_pokemon_id),
	is_ditto = VALUES(is_ditto),
	pokestop_id = VALUES(pokestop_id),
	updated = VALUES(updated),
	first_seen_timestamp = VALUES(first_seen_timestamp),
	changed = VALUES(changed),
	cell_id = VALUES(cell_id),
	expire_timestamp_verified = VALUES(expire_timestamp_verified),
	shiny = VALUES(shiny),
	username = VALUES(username),
	pvp = COALESCE(VALUES(pvp), pvp),
	is_event = VALUES(is_event),
	seen_type = VALUES(seen_type)
`

const spawnpointBatchUpsertQuery = `
INSERT INTO spawnpoint (
	id, lat, lon, updated, last_seen, despawn_sec
)
VALUES (
	:id, :lat, :lon, :updated, :last_seen, :despawn_sec
)
ON DUPLICATE KEY UPDATE
	lat = VALUES(lat),
	lon = VALUES(lon),
	updated = VALUES(updated),
	last_seen=VALUES(last_seen),
	despawn_sec = VALUES(despawn_sec)
`

const routeBatchUpsertQuery = `
INSERT INTO route (
	id, name, shortcode, description, distance_meters,
	duration_seconds, end_fort_id, end_image, end_lat, end_lon,
	image, image_border_color, reversible, start_fort_id, start_image,
	start_lat, start_lon, tags, type, updated, version, waypoints
)
VALUES (
	:id, :name, :shortcode, :description, :distance_meters,
	:duration_seconds, :end_fort_id, :end_image, :end_lat, :end_lon,
	:image, :image_border_color, :reversible, :start_fort_id, :start_image,
	:start_lat, :start_lon, :tags, :type, :updated, :version, :waypoints
)
ON DUPLICATE KEY UPDATE
	name = VALUES(name),
	shortcode = VALUES(shortcode),
	description = VALUES(description),
	distance_meters = VALUES(distance_meters),
	duration_seconds = VALUES(duration_seconds),
	end_fort_id = VALUES(end_fort_id),
	end_image = VALUES(end_image),
	end_lat = VALUES(end_lat),
	end_lon = VALUES(end_lon),
	image = VALUES(image),
	image_border_color = VALUES(image_border_color),
	reversible = VALUES(reversible),
	start_fort_id = VALUES(start_fort_id),
	start_image = VALUES(start_image),
	start_lat = VALUES(start_lat),
	start_lon = VALUES(start_lon),
	tags = VALUES(tags),
	type = VALUES(type),
	updated = VALUES(updated),
	version = VALUES(version),
	waypoints = VALUES(waypoints)
`

const tappableBatchUpsertQuery = `
INSERT INTO tappable (
	id, lat, lon, fort_id, spawn_id, type, pokemon_id, item_id,
	count, expire_timestamp, expire_timestamp_verified, updated
)
VALUES (
	:id, :lat, :lon, :fort_id, :spawn_id, :type, :pokemon_id, :item_id,
	:count, :expire_timestamp, :expire_timestamp_verified, :updated
)
ON DUPLICATE KEY UPDATE
	lat = VALUES(lat),
	lon = VALUES(lon),
	fort_id = VALUES(fort_id),
	spawn_id = VALUES(spawn_id),
	type = VALUES(type),
	pokemon_id = VALUES(pokemon_id),
	item_id = VALUES(item_id),
	count = VALUES(count),
	expire_timestamp = VALUES(expire_timestamp),
	expire_timestamp_verified = VALUES(expire_timestamp_verified),
	updated = VALUES(updated)
`

const stationBatchUpsertQuery = `
INSERT INTO station (
	id, lat, lon, name, cell_id, start_time, end_time, cooldown_complete,
	is_battle_available, is_inactive, updated, battle_level, battle_start, battle_end,
	battle_pokemon_id, battle_pokemon_form, battle_pokemon_costume, battle_pokemon_gender,
	battle_pokemon_alignment, battle_pokemon_bread_mode, battle_pokemon_move_1, battle_pokemon_move_2,
	battle_pokemon_stamina, battle_pokemon_cp_multiplier, total_stationed_pokemon,
	total_stationed_gmax, stationed_pokemon
)
VALUES (
	:id, :lat, :lon, :name, :cell_id, :start_time, :end_time, :cooldown_complete,
	:is_battle_available, :is_inactive, :updated, :battle_level, :battle_start, :battle_end,
	:battle_pokemon_id, :battle_pokemon_form, :battle_pokemon_costume, :battle_pokemon_gender,
	:battle_pokemon_alignment, :battle_pokemon_bread_mode, :battle_pokemon_move_1, :battle_pokemon_move_2,
	:battle_pokemon_stamina, :battle_pokemon_cp_multiplier, :total_stationed_pokemon,
	:total_stationed_gmax, :stationed_pokemon
)
ON DUPLICATE KEY UPDATE
	lat = VALUES(lat),
	lon = VALUES(lon),
	name = VALUES(name),
	cell_id = VALUES(cell_id),
	start_time = VALUES(start_time),
	end_time = VALUES(end_time),
	cooldown_complete = VALUES(cooldown_complete),
	is_battle_available = VALUES(is_battle_available),
	is_inactive = VALUES(is_inactive),
	updated = VALUES(updated),
	battle_level = VALUES(battle_level),
	battle_start = VALUES(battle_start),
	battle_end = VALUES(battle_end),
	battle_pokemon_id = VALUES(battle_pokemon_id),
	battle_pokemon_form = VALUES(battle_pokemon_form),
	battle_pokemon_costume = VALUES(battle_pokemon_costume),
	battle_pokemon_gender = VALUES(battle_pokemon_gender),
	battle_pokemon_alignment = VALUES(battle_pokemon_alignment),
	battle_pokemon_bread_mode = VALUES(battle_pokemon_bread_mode),
	battle_pokemon_move_1 = VALUES(battle_pokemon_move_1),
	battle_pokemon_move_2 = VALUES(battle_pokemon_move_2),
	battle_pokemon_stamina = VALUES(battle_pokemon_stamina),
	battle_pokemon_cp_multiplier = VALUES(battle_pokemon_cp_multiplier),
	total_stationed_pokemon = VALUES(total_stationed_pokemon),
	total_stationed_gmax = VALUES(total_stationed_gmax),
	stationed_pokemon = VALUES(stationed_pokemon)
`

const incidentBatchUpsertQuery = "INSERT INTO incident (" +
	"id, pokestop_id, start, expiration, display_type, style, `character`, " +
	"updated, confirmed, slot_1_pokemon_id, slot_1_form, slot_2_pokemon_id, " +
	"slot_2_form, slot_3_pokemon_id, slot_3_form" +
	") VALUES (" +
	":id, :pokestop_id, :start, :expiration, :display_type, :style, :character, " +
	":updated, :confirmed, :slot_1_pokemon_id, :slot_1_form, :slot_2_pokemon_id, " +
	":slot_2_form, :slot_3_pokemon_id, :slot_3_form" +
	") ON DUPLICATE KEY UPDATE " +
	"start = VALUES(start), " +
	"expiration = VALUES(expiration), " +
	"display_type = VALUES(display_type), " +
	"style = VALUES(style), " +
	"`character` = VALUES(`character`), " +
	"updated = VALUES(updated), " +
	"confirmed = VALUES(confirmed), " +
	"slot_1_pokemon_id = VALUES(slot_1_pokemon_id), " +
	"slot_1_form = VALUES(slot_1_form), " +
	"slot_2_pokemon_id = VALUES(slot_2_pokemon_id), " +
	"slot_2_form = VALUES(slot_2_form), " +
	"slot_3_pokemon_id = VALUES(slot_3_pokemon_id), " +
	"slot_3_form = VALUES(slot_3_form)"

const s2cellBatchUpsertQuery = `
INSERT INTO s2cell (id, center_lat, center_lon, level, updated)
VALUES (:id, :center_lat, :center_lon, :level, :updated)
ON DUPLICATE KEY UPDATE updated = VALUES(updated)
`
