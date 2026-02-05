package decoder

import (
	"context"

	log "github.com/sirupsen/logrus"

	"golbat/db"
	"golbat/decoder/writebehind"
)

// RegisterBatchWriters registers all batch writers with the write-behind queue
func RegisterBatchWriters(queue *writebehind.Queue) {
	queue.RegisterBatchWriter("pokestop", flushPokestopBatch)
	queue.RegisterBatchWriter("gym", flushGymBatch)
	queue.RegisterBatchWriter("pokemon", flushPokemonBatch)
	queue.RegisterBatchWriter("spawnpoint", flushSpawnpointBatch)

	log.Info("Write-behind batch writers registered for: pokestop, gym, pokemon, spawnpoint")
}

// flushPokestopBatch writes a batch of pokestops using INSERT ... ON DUPLICATE KEY UPDATE
func flushPokestopBatch(ctx context.Context, dbDetails db.DbDetails, entries []*writebehind.QueueEntry) error {
	pokestops := make([]*Pokestop, len(entries))
	for i, e := range entries {
		pokestops[i] = e.Entity.(*Pokestop)
	}

	return writebehind.ExecuteBatchUpsert(
		ctx,
		dbDetails.GeneralDb,
		pokestopBatchUpsertQuery,
		pokestops,
		func() func() {
			// Lock all pokestops
			for _, p := range pokestops {
				p.Lock()
			}
			// Return unlock function
			return func() {
				for _, p := range pokestops {
					p.Unlock()
				}
			}
		},
	)
}

// flushGymBatch writes a batch of gyms using INSERT ... ON DUPLICATE KEY UPDATE
func flushGymBatch(ctx context.Context, dbDetails db.DbDetails, entries []*writebehind.QueueEntry) error {
	gyms := make([]*Gym, len(entries))
	for i, e := range entries {
		gyms[i] = e.Entity.(*Gym)
	}

	return writebehind.ExecuteBatchUpsert(
		ctx,
		dbDetails.GeneralDb,
		gymBatchUpsertQuery,
		gyms,
		func() func() {
			for _, g := range gyms {
				g.Lock()
			}
			return func() {
				for _, g := range gyms {
					g.Unlock()
				}
			}
		},
	)
}

// flushPokemonBatch writes a batch of pokemon using INSERT ... ON DUPLICATE KEY UPDATE
func flushPokemonBatch(ctx context.Context, dbDetails db.DbDetails, entries []*writebehind.QueueEntry) error {
	pokemon := make([]*Pokemon, len(entries))
	for i, e := range entries {
		pokemon[i] = e.Entity.(*Pokemon)
	}

	return writebehind.ExecuteBatchUpsert(
		ctx,
		dbDetails.PokemonDb,
		pokemonBatchUpsertQuery,
		pokemon,
		func() func() {
			for _, p := range pokemon {
				p.Lock()
			}
			return func() {
				for _, p := range pokemon {
					p.Unlock()
				}
			}
		},
	)
}

// flushSpawnpointBatch writes a batch of spawnpoints using INSERT ... ON DUPLICATE KEY UPDATE
func flushSpawnpointBatch(ctx context.Context, dbDetails db.DbDetails, entries []*writebehind.QueueEntry) error {
	spawnpoints := make([]*Spawnpoint, len(entries))
	for i, e := range entries {
		spawnpoints[i] = e.Entity.(*Spawnpoint)
	}

	return writebehind.ExecuteBatchUpsert(
		ctx,
		dbDetails.GeneralDb,
		spawnpointBatchUpsertQuery,
		spawnpoints,
		func() func() {
			for _, s := range spawnpoints {
				s.Lock()
			}
			return func() {
				for _, s := range spawnpoints {
					s.Unlock()
				}
			}
		},
	)
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
	pvp = VALUES(pvp),
	is_event = VALUES(is_event),
	seen_type = VALUES(seen_type)
`

const spawnpointBatchUpsertQuery = `
INSERT INTO spawnpoint (
	id, lat, lon, updated, despawn_sec
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
