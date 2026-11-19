package decoder

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"math"
	"slices"
	"strconv"
	"time"

	"golbat/config"
	"golbat/pogo"
	"golbat/tz"

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
	biomeCollector = &biomeWorker{
		db:      db,
		queue:   make(chan biomeEvent, 32768),
		weather: make(map[uint64]biomeEvent),
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
	// The first matching vista is the habitat. Cells without a named vista
	// use land cover; keep the namespaces distinct even when enum IDs match.
	kind, habitat := uint32(0), fields.Landcover
	for _, vista := range [...]pogo.BackgroundVisualDetailProto_VistaFeature{
		pogo.BackgroundVisualDetailProto_VISTA_BEACH, pogo.BackgroundVisualDetailProto_VISTA_OCEAN,
		pogo.BackgroundVisualDetailProto_VISTA_RIVER, pogo.BackgroundVisualDetailProto_VISTA_LAKE,
		pogo.BackgroundVisualDetailProto_VISTA_DESERT, pogo.BackgroundVisualDetailProto_VISTA_CITY,
	} {
		if slices.Contains(fields.Features, int32(vista)) {
			kind, habitat = 1, int32(vista)
			break
		}
	}
	var key [12]byte
	binary.BigEndian.PutUint32(key[:4], uint32(fields.Temperature))
	binary.BigEndian.PutUint32(key[4:8], kind)
	binary.BigEndian.PutUint32(key[8:], uint32(habitat))
	digest := sha256.Sum256(key[:])
	return digest[:]
}

func biomeSpawnScope(id s2.CellID) string {
	center := id.LatLng()
	if zone := tz.SearchTimezone(center.Lat.Degrees(), center.Lng.Degrees()); zone != "" {
		return "tz:" + zone
	}
	return "cell:" + strconv.FormatUint(uint64(id), 10)
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
