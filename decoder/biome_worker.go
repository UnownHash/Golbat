package decoder

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"

	"golbat/util"

	"github.com/golang/geo/s2"
	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"
)

const (
	biomeWindow     = time.Hour
	biomeRefresh    = 5 * time.Minute
	biomeRetention  = 3 * time.Hour
	biomeWeatherAge = 15 * time.Minute
	biomeBatchSize  = 250
)

type biomeEventKind uint8

const (
	biomeCellEvent biomeEventKind = iota
	biomeWeatherEvent
	biomeSpawnEvent
)

type biomeEvent struct {
	kind         biomeEventKind
	cellID       uint64
	atMS         int64
	geography    biomeGeography
	weather      int32
	pokemonID    int32
	form         int32
	encounterKey [16]byte
}

// Weather observations expire at the hour boundary, even if only seconds old.
func (event biomeEvent) coversSpawn(atMS int64) bool {
	hour := time.Hour.Milliseconds()
	return event.weather > 0 && event.atMS <= atMS &&
		atMS-event.atMS <= biomeWeatherAge.Milliseconds() && event.atMS/hour == atMS/hour
}

type biomeWorker struct {
	db       *sqlx.DB
	queue    chan biomeEvent
	weather  map[uint64]biomeEvent
	lastLoss atomic.Int64
	drops    util.DropReporter
}

func (worker *biomeWorker) enqueue(event biomeEvent) {
	select {
	case worker.queue <- event:
	default:
		worker.lastLoss.Store(time.Now().UnixMilli())
		worker.drops.Report(func(dropped int64) {
			log.Warnf("[BIOMES] dropped %d observations because the queue is full", dropped)
		})
	}
}

func (worker *biomeWorker) run(ctx context.Context) {
	backfilled := make(chan struct{})
	go func() {
		defer close(backfilled)
		worker.backfillScopes(ctx)
	}()
	defer func() { <-backfilled }()
	flushTimer := time.NewTicker(10 * time.Second)
	refreshTimer := time.NewTicker(biomeRefresh)
	defer flushTimer.Stop()
	defer refreshTimer.Stop()
	batch := make([]biomeEvent, 0, biomeBatchSize)
	invalidatedThrough := int64(0)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := worker.persist(writeCtx, batch); err != nil {
			worker.lastLoss.Store(time.Now().UnixMilli())
			log.Warnf("[BIOMES] could not persist observations: %v", err)
		}
		clear(batch)
		batch = batch[:0]
	}

	for {
		select {
		case <-ctx.Done():
			return
		case event := <-worker.queue:
			if event.kind == biomeWeatherEvent {
				if event.atMS >= worker.weather[event.cellID].atMS {
					worker.weather[event.cellID] = event
				}
				continue
			}
			if event.kind == biomeSpawnEvent {
				parent := uint64(s2.CellID(event.cellID).Parent(10))
				weather := worker.weather[parent]
				if !weather.coversSpawn(event.atMS) {
					continue
				}
				event.weather = weather.weather
			}
			batch = append(batch, event)
			if len(batch) == biomeBatchSize {
				flush()
			}
		case <-flushTimer.C:
			flush()
			if lastLoss := worker.lastLoss.Load(); lastLoss > invalidatedThrough {
				invalidCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				_, err := worker.db.ExecContext(invalidCtx,
					"UPDATE biome_spawn_group SET valid_until_ms = ? WHERE valid_until_ms > ?", lastLoss, lastLoss)
				cancel()
				if err != nil {
					log.Warnf("[BIOMES] could not expire incomplete spawn statistics: %v", err)
				} else {
					invalidatedThrough = lastLoss
				}
			}
		case <-refreshTimer.C:
			flush()
			now := time.Now()
			for id, weather := range worker.weather {
				if now.UnixMilli()-weather.atMS > biomeWeatherAge.Milliseconds() {
					delete(worker.weather, id)
				}
			}
			refreshCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			err := worker.refresh(refreshCtx, now)
			cancel()
			if err != nil {
				log.Warnf("[BIOMES] could not refresh spawn statistics: %v", err)
			}
		}
	}
}

func (worker *biomeWorker) persist(ctx context.Context, batch []biomeEvent) error {
	cells := make([]map[string]any, 0, len(batch))
	wild := make([]biomeEvent, 0, len(batch))
	for _, event := range batch {
		if event.kind == biomeSpawnEvent {
			wild = append(wild, event)
			continue
		}
		fields, err := json.Marshal(event.geography)
		if err != nil {
			return err
		}
		cells = append(cells, map[string]any{
			"cell": event.cellID, "geography": string(fields), "biome": event.geography.signature(),
			"scope": biomeSpawnScope(s2.CellID(event.cellID)), "at": event.atMS,
		})
	}
	if len(cells) != 0 {
		_, err := worker.db.NamedExecContext(ctx, `
			INSERT INTO biome_cell (cell_id, geography, biome_key, spawn_scope, observed_at_ms, geography_changed_at_ms)
			VALUES (:cell, :geography, :biome, :scope, :at, :at)
			ON DUPLICATE KEY UPDATE
			 geography_changed_at_ms = IF(VALUES(observed_at_ms) > observed_at_ms AND
			     (BINARY geography <> BINARY VALUES(geography) OR spawn_scope <> VALUES(spawn_scope)),
			     VALUES(observed_at_ms), geography_changed_at_ms),
			 geography = IF(VALUES(observed_at_ms) > observed_at_ms, VALUES(geography), geography),
			 biome_key = IF(VALUES(observed_at_ms) > observed_at_ms, VALUES(biome_key), biome_key),
			 spawn_scope = IF(VALUES(observed_at_ms) > observed_at_ms, VALUES(spawn_scope), spawn_scope),
			 observed_at_ms = GREATEST(observed_at_ms, VALUES(observed_at_ms))`, cells)
		if err != nil {
			return err
		}
	}
	if len(wild) == 0 {
		return nil
	}
	return worker.persistWild(ctx, wild)
}

func (worker *biomeWorker) persistWild(ctx context.Context, wild []biomeEvent) error {
	type cellProfile struct {
		Cell      uint64 `db:"cell_id"`
		Key       []byte `db:"biome_key"`
		Scope     string `db:"spawn_scope"`
		ChangedAt int64  `db:"geography_changed_at_ms"`
	}
	ids := make([]uint64, 0, len(wild))
	for _, event := range wild {
		ids = append(ids, event.cellID)
	}
	query, args, err := sqlx.In(`
		SELECT cell_id, biome_key, spawn_scope, geography_changed_at_ms
		FROM biome_cell WHERE cell_id IN (?) AND biome_key IS NOT NULL`, ids)
	if err != nil {
		return err
	}
	var profiles []cellProfile
	if err = worker.db.SelectContext(ctx, &profiles, query, args...); err != nil {
		return err
	}
	byID := make(map[uint64]cellProfile, len(profiles))
	for _, profile := range profiles {
		byID[profile.Cell] = profile
	}
	rows := make([]map[string]any, 0, len(wild))
	for _, event := range wild {
		profile, ok := byID[event.cellID]
		if !ok || event.atMS < profile.ChangedAt {
			continue
		}
		rows = append(rows, map[string]any{
			"id": event.encounterKey[:], "cell": event.cellID, "biome": profile.Key, "scope": profile.Scope,
			"weather": event.weather, "pokemon": event.pokemonID, "form": event.form, "at": event.atMS,
		})
	}
	if len(rows) == 0 {
		return nil
	}
	// Only the first usable observation of a wild encounter contributes.
	_, err = worker.db.NamedExecContext(ctx, `
		INSERT INTO biome_spawn_observation
		    (encounter_key, cell_id, biome_key, spawn_scope, weather, pokemon_id, form, observed_at_ms)
		VALUES (:id, :cell, :biome, :scope, :weather, :pokemon, :form, :at)
		ON DUPLICATE KEY UPDATE encounter_key = biome_spawn_observation.encounter_key`, rows)
	return err
}
