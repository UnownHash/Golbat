package decoder

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"golbat/config"
	"golbat/geo"
	"golbat/pogo"

	"github.com/golang/geo/s2"
	"github.com/jmoiron/sqlx"
)

var biomeCollector *biomeWorker

type biomeGeography struct {
	Landform    int32   `json:"landform,omitempty"`
	Landcover   int32   `json:"landcover,omitempty"`
	Temperature int32   `json:"temperature,omitempty"`
	Moisture    int32   `json:"moisture"`
	Features    []int32 `json:"features"`
	IsInPark    bool    `json:"is_in_park"`
	ParkStatus  int32   `json:"park_status"`
	Settlement  int32   `json:"settlement"`
}

func InitBiomeCollector(db *sqlx.DB) func(context.Context) {
	if !config.Config.Biomes.Enabled {
		return nil
	}
	names := slices.Clone(config.Config.Biomes.SharedAreas)
	slices.Sort(names)
	names = slices.Compact(names)
	digest := sha256.Sum256([]byte(strings.Join(names, "\x00")))
	biomeCollector = &biomeWorker{
		db:          db,
		queue:       make(chan biomeEvent, 32768),
		areas:       config.Config.BiomeAreaNames(),
		sharedScope: "shared:" + hex.EncodeToString(digest[:]),
		weather:     make(map[uint64]biomeEvent),
	}
	return biomeCollector.run
}

func biomeLocation(mon *pogo.WildPokemonProto) (s2.CellID, bool) {
	if mon == nil {
		return 0, false
	}
	lat, lon := mon.Latitude, mon.Longitude
	if lat == 0 && lon == 0 {
		spawnID, err := strconv.ParseInt(mon.SpawnPointId, 16, 64)
		if err != nil {
			return 0, false
		}
		var ok bool
		lat, lon, ok = wildPokemonLocation(spawnID, mon)
		if !ok {
			return 0, false
		}
	}
	point := s2.LatLngFromDegrees(lat, lon)
	if !point.IsValid() || math.IsNaN(lat) || math.IsNaN(lon) {
		return 0, false
	}
	return s2.CellIDFromLatLng(point).Parent(16), true
}

func makeBiomeGeography(detail *pogo.BackgroundVisualDetailProto) biomeGeography {
	features := make([]int32, 0, len(detail.Features))
	for _, feature := range detail.Features {
		features = append(features, int32(feature))
	}
	return biomeGeography{
		Landform: int32(detail.Landform), Landcover: int32(detail.Landcover),
		Temperature: int32(detail.Temperature), Moisture: int32(detail.Moisture),
		Features: features, IsInPark: detail.GetIsInPark(),
		ParkStatus: int32(detail.ParkStatus), Settlement: int32(detail.Settlement),
	}
}

func (fields biomeGeography) signature() []byte {
	if fields.Landform <= 0 || fields.Landcover <= 0 || fields.Temperature <= 0 {
		return nil
	}
	// Feature order is retained in geography JSON, but does not split a spawn group.
	fields.Features = slices.Clone(fields.Features)
	slices.Sort(fields.Features)
	fields.Features = slices.Compact(fields.Features)
	if len(fields.Features) == 0 {
		fields.Features = []int32{}
	}
	key, _ := json.Marshal(fields)
	digest := sha256.Sum256(key)
	return digest[:]
}

func (worker *biomeWorker) spawnScope(id s2.CellID) string {
	if len(worker.areas) != 0 {
		center := id.LatLng()
		matches := MatchStatsGeofenceWithCell(center.Lat.Degrees(), center.Lng.Degrees(), uint64(id))
		if geo.AreaMatchWithWildcards(matches, worker.areas) {
			return worker.sharedScope
		}
	}
	return strconv.FormatUint(uint64(id.Parent(10)), 10)
}

func ObserveBiomeEncounter(encounter *pogo.EncounterOutProto, receivedAtMS int64) {
	worker := biomeCollector
	if worker == nil || encounter.GetBackgroundVisualDetail() == nil {
		return
	}
	cell, ok := biomeLocation(encounter.GetPokemon())
	if !ok {
		return
	}
	detail := encounter.GetBackgroundVisualDetail()
	if receivedAtMS <= 0 {
		receivedAtMS = time.Now().UnixMilli()
	}
	worker.enqueue(biomeEvent{kind: biomeCellEvent, cellID: uint64(cell), atMS: receivedAtMS, geography: makeBiomeGeography(detail)})
}

func ObserveBiomeWildBatch(parameters ScanParameters, wild []RawWildPokemonData, weather []*pogo.ClientWeatherProto, receivedAtMS int64) {
	worker := biomeCollector
	if worker == nil || !parameters.ProcessWeather {
		return
	}
	if receivedAtMS <= 0 {
		receivedAtMS = time.Now().UnixMilli()
	}
	for _, item := range weather {
		cell := s2.CellID(item.GetS2CellId())
		condition := item.GetGameplayWeather().GetGameplayCondition()
		if cell.IsValid() && cell.Level() == 10 && condition > pogo.GameplayWeatherProto_NONE {
			worker.enqueue(biomeEvent{kind: biomeWeatherEvent, cellID: uint64(cell), atMS: receivedAtMS, weather: int32(condition)})
		}
	}
	if !parameters.ProcessPokemon || !parameters.ProcessWild {
		return
	}
	for _, item := range wild {
		pokemon := item.Data.GetPokemon()
		if item.Data.GetEncounterId() == 0 || pokemon.GetPokemonId() <= 0 {
			continue
		}
		cell, ok := biomeLocation(item.Data)
		if !ok {
			continue
		}
		form := int32(-1)
		if display := pokemon.GetPokemonDisplay(); display != nil {
			form = int32(display.GetForm())
		}
		var encoded [8]byte
		binary.BigEndian.PutUint64(encoded[:], item.Data.GetEncounterId())
		digest := sha256.Sum256(encoded[:])
		worker.enqueue(biomeEvent{
			kind: biomeSpawnEvent, cellID: uint64(cell), atMS: receivedAtMS,
			pokemonID: int32(pokemon.GetPokemonId()), form: form, encounterKey: [16]byte(digest[:16]),
		})
	}
}
