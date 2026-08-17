package decoder

import (
	"encoding/json"
	"io"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"

	"golbat/util"
)

// Spawnpoint observation export: raw, append-only JSONL capture of every
// verified-TTH wild sighting, for offline analysis of the spawnpoint id →
// despawn time relationship. The spawnpoint table is unusable for that
// analysis — despawn_sec is ms-truncated, reduced mod-3600, written with a
// 2-second tolerance, and coalesced to one row per spawnpoint — so this log
// preserves the observations exactly as they arrive, before any of that
// processing. Repeat sightings of the same encounter are deliberately kept
// (they measure server-side jitter).

// spawnpointObservation is one exported row. uint64 identifiers are emitted
// as decimal strings: JSON tooling commonly decodes large integers via
// float64, which silently corrupts them as group-by keys.
type spawnpointObservation struct {
	ReceivedMs     int64   `json:"received_ms"`      // wall clock at decode
	CellId         string  `json:"cell_id"`          // S2 cell the wild arrived in
	CellAsOfMs     int64   `json:"cell_as_of_ms"`    // mapCell.AsOfTimeMs, the base TTH is added to
	LastModifiedMs int64   `json:"last_modified_ms"` // WildPokemonProto.LastModifiedMs, unprocessed
	SpawnIdHex     string  `json:"spawn_id_hex"`     // unparsed wire string
	Lat            float64 `json:"lat"`
	Lon            float64 `json:"lon"`
	TthMs          int32   `json:"tth_ms"`
	DespawnMs      int64   `json:"despawn_ms"` // cell_as_of_ms + tth_ms, full precision
	EncounterId    string  `json:"encounter_id"`
	PokemonId      int32   `json:"pokemon_id"`
	FirstSeen      int64   `json:"first_seen"` // when Golbat first saw this encounter (upper bound on spawn start)
}

// buildSpawnpointObservation maps a wild sighting to an export row. ok is
// false outside the client-verified TTH window (0, 90000] — the same window
// spawnpointUpdateFromWild trusts.
func buildSpawnpointObservation(wild RawWildPokemonData, firstSeen, receivedMs int64) (spawnpointObservation, bool) {
	tth := wild.Data.TimeTillHiddenMs
	if tth <= 0 || tth > 90000 {
		return spawnpointObservation{}, false
	}
	var pokemonId int32
	if wild.Data.Pokemon != nil {
		pokemonId = int32(wild.Data.Pokemon.PokemonId)
	}
	return spawnpointObservation{
		ReceivedMs:     receivedMs,
		CellId:         strconv.FormatUint(wild.Cell, 10),
		CellAsOfMs:     wild.Timestamp,
		LastModifiedMs: wild.Data.LastModifiedMs,
		SpawnIdHex:     wild.Data.SpawnPointId,
		Lat:            wild.Data.Latitude,
		Lon:            wild.Data.Longitude,
		TthMs:          tth,
		DespawnMs:      wild.Timestamp + int64(tth),
		EncounterId:    strconv.FormatUint(wild.Data.EncounterId, 10),
		PokemonId:      pokemonId,
		FirstSeen:      firstSeen,
	}, true
}

// spawnpointObservationLogger owns the export channel and its single writer
// goroutine. record never blocks (decode-path invariant: no blocking sends
// to workers) — a full channel drops the row and counts it.
type spawnpointObservationLogger struct {
	ch           chan spawnpointObservation
	w            io.Writer
	wg           sync.WaitGroup
	drops        util.DropReporter
	droppedTotal atomic.Int64
}

func newSpawnpointObservationLogger(w io.Writer, capacity int) *spawnpointObservationLogger {
	l := &spawnpointObservationLogger{
		ch: make(chan spawnpointObservation, capacity),
		w:  w,
	}
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		for obs := range l.ch {
			line, err := json.Marshal(obs)
			if err != nil {
				log.Errorf("spawnpoint observation marshal: %s", err)
				continue
			}
			if _, err := l.w.Write(append(line, '\n')); err != nil {
				log.Errorf("spawnpoint observation write: %s", err)
			}
		}
	}()
	return l
}

func (l *spawnpointObservationLogger) record(obs spawnpointObservation) {
	select {
	case l.ch <- obs:
	default:
		l.droppedTotal.Add(1)
		l.drops.Report(func(dropped int64) {
			log.Warnf("spawnpoint observation log: dropped %d rows (writer stalled)", dropped)
		})
	}
}

// close drains everything already recorded and stops the worker. Callers
// must not record after close.
func (l *spawnpointObservationLogger) close() {
	close(l.ch)
	l.wg.Wait()
	if c, ok := l.w.(io.Closer); ok {
		_ = c.Close()
	}
}

// spawnpointObsLogger is nil unless spawnpoint_observation_log is configured.
// Set once at startup (pre-traffic) and read per wild sighting on the decode
// path, so a plain pointer with an atomic accessor pair keeps the hook cheap.
var spawnpointObsLogger atomic.Pointer[spawnpointObservationLogger]

func setSpawnpointObservationLogger(l *spawnpointObservationLogger) {
	spawnpointObsLogger.Store(l)
}

// InitSpawnpointObservationLog opens path in append mode and starts the
// export worker. Call from main() after config load, before traffic.
func InitSpawnpointObservationLog(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	setSpawnpointObservationLogger(newSpawnpointObservationLogger(f, 4096))
	log.Infof("Spawnpoint observation log enabled: %s", path)
	return nil
}

// maybeExportSpawnpointObservation is the decode-path hook: one atomic load
// when disabled, one row when the sighting's TTH is verified.
func maybeExportSpawnpointObservation(wild RawWildPokemonData, firstSeen int64) {
	l := spawnpointObsLogger.Load()
	if l == nil {
		return
	}
	if obs, ok := buildSpawnpointObservation(wild, firstSeen, time.Now().UnixMilli()); ok {
		l.record(obs)
	}
}
