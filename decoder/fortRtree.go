package decoder

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/guregu/null/v6"
	"github.com/jellydator/ttlcache/v3"
	"github.com/puzpuzpuz/xsync/v4"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/rtree"

	"golbat/config"
)

type FortLookup struct {
	FortType         FortType
	Lat              float64
	Lon              float64
	PowerUpLevel     int8
	IsArScanEligible bool

	// Gym
	AvailableSlots      int8
	TeamId              int8
	RaidEndTimestamp    int64 // used to check expiry at filter time
	RaidBattleTimestamp int64
	RaidLevel           int8
	RaidPokemonId       int16
	RaidPokemonForm     int16

	// Pokestop - quest rewards only (both AR and no-AR stored, filter matches either)
	LureId                     int16
	QuestNoArRewardType        int16
	QuestNoArRewardAmount      int16
	QuestNoArRewardItemId      int16
	QuestNoArRewardPokemonId   int16
	QuestNoArRewardPokemonForm int16
	QuestArRewardType          int16
	QuestArRewardAmount        int16
	QuestArRewardItemId        int16
	QuestArRewardPokemonId     int16
	QuestArRewardPokemonForm   int16

	// Pokestop - incident (first active incident, flat fields)
	IncidentDisplayType int8
	IncidentStyle       int8
	IncidentCharacter   int16
	IncidentPokemonId   int16
	IncidentPokemonForm int16

	// Pokestop - contest
	ContestPokemonId    int16
	ContestPokemonForm  int16
	ContestPokemonType  int8
	ContestTotalEntries int16

	// Station
	BattleEndTimestamp int64 // used to check expiry at filter time
	BattleLevel        int8
	BattlePokemonId    int16
	BattlePokemonForm  int16
	StationBattles     []FortLookupStationBattle
}

var fortLookupCache *xsync.Map[string, FortLookup]
var fortTreeMutex sync.RWMutex
var fortTree rtree.RTreeG[string]

var fortTreeSnapshot atomic.Pointer[treeSnapshot[string]]

// getFortTreeSnapshot: see refreshTreeSnapshot.
func getFortTreeSnapshot() *rtree.RTreeG[string] {
	return refreshTreeSnapshot(&fortTreeSnapshot, &fortTreeMutex, &fortTree)
}

func initFortRtree() {
	fortTreeEvictor = newTreeEvictor[string]("fort", treeEvictorQueueSize, treeEvictorBatchSize, flushFortTreeEvictions)
	fortLookupCache = xsync.NewMap[string, FortLookup]()

	// OnEviction registrations live here, after fortTreeEvictor and
	// fortLookupCache are created (and after pokestopCache/gymCache/
	// stationCache are created by initDataCache before calling this
	// function), so callbacks can never observe a nil evictor or lookup
	// cache. Mirrors the structure of initPokemonRtree.
	if config.Config.FortInMemory {
		pokestopCache.OnEviction(func(ctx context.Context, reason ttlcache.EvictionReason, item *ttlcache.Item[string, *Pokestop]) {
			p := item.Value()
			deferFortEviction(POKESTOP, p.Id, p.Lat, p.Lon)
		})
		gymCache.OnEviction(func(ctx context.Context, reason ttlcache.EvictionReason, item *ttlcache.Item[string, *Gym]) {
			g := item.Value()
			deferFortEviction(GYM, g.Id, g.Lat, g.Lon)
		})
	}

	stationCache.OnEviction(func(ctx context.Context, reason ttlcache.EvictionReason, item *ttlcache.Item[string, *Station]) {
		clearStationBattleState(item.Key())
		if config.Config.FortInMemory {
			s := item.Value()
			deferFortEviction(STATION, s.Id, s.Lat, s.Lon)
		}
	})
}

type IdRecord struct {
	Id string `db:"id"`
}

// genericUpdateFort handles rtree updates for fort location changes and deletions.
func genericUpdateFort(id string, lat float64, lon float64, deleted bool) {
	oldFort, inMap := fortLookupCache.Load(id)

	if deleted {
		if inMap {
			fortLookupCache.Delete(id)
			removeFortFromTree(id, oldFort.Lat, oldFort.Lon)
		}
		return
	}

	if !inMap {
		addFortToTree(id, lat, lon)
	} else if lat != oldFort.Lat || lon != oldFort.Lon {
		removeFortFromTree(id, oldFort.Lat, oldFort.Lon)
		addFortToTree(id, lat, lon)
	}
}

// fortRtreeUpdatePokestopOnSave updates rtree and lookup cache when a pokestop is saved
func fortRtreeUpdatePokestopOnSave(pokestop *Pokestop) {
	genericUpdateFort(pokestop.Id, pokestop.Lat, pokestop.Lon, pokestop.Deleted)
	if !pokestop.Deleted {
		updatePokestopLookup(pokestop)
	}
}

// fortRtreeUpdateGymOnSave updates rtree and lookup cache when a gym is saved
func fortRtreeUpdateGymOnSave(gym *Gym) {
	genericUpdateFort(gym.Id, gym.Lat, gym.Lon, gym.Deleted)
	if !gym.Deleted {
		updateGymLookup(gym)
	}
}

// fortRtreeUpdateStationOnSave updates rtree and lookup cache when a station is saved
func fortRtreeUpdateStationOnSave(station *Station) {
	genericUpdateFort(station.Id, station.Lat, station.Lon, false)
	updateStationLookup(station)
}

// fortRtreeUpdatePokestopOnGet updates rtree when a pokestop is loaded from DB (cache miss)
func fortRtreeUpdatePokestopOnGet(pokestop *Pokestop) {
	_, inMap := fortLookupCache.Load(pokestop.Id)
	if !inMap {
		addFortToTree(pokestop.Id, pokestop.Lat, pokestop.Lon)
		updatePokestopLookup(pokestop)
	}
}

// fortRtreeUpdateGymOnGet updates rtree when a gym is loaded from DB (cache miss)
func fortRtreeUpdateGymOnGet(gym *Gym) {
	_, inMap := fortLookupCache.Load(gym.Id)
	if !inMap {
		addFortToTree(gym.Id, gym.Lat, gym.Lon)
		updateGymLookup(gym)
	}
}

// fortRtreeUpdateStationOnGet updates rtree when a station is loaded from DB (cache miss)
func fortRtreeUpdateStationOnGet(station *Station) {
	_, inMap := fortLookupCache.Load(station.Id)
	if !inMap {
		addFortToTree(station.Id, station.Lat, station.Lon)
		updateStationLookup(station)
	}
}

func updatePokestopLookup(pokestop *Pokestop) {
	// Preserve existing incident fields if present
	var incidentDisplayType int8
	var incidentStyle int8
	var incidentCharacter int16
	var incidentPokemonId int16
	var incidentPokemonForm int16
	if existing, ok := fortLookupCache.Load(pokestop.Id); ok {
		incidentDisplayType = existing.IncidentDisplayType
		incidentStyle = existing.IncidentStyle
		incidentCharacter = existing.IncidentCharacter
		incidentPokemonId = existing.IncidentPokemonId
		incidentPokemonForm = existing.IncidentPokemonForm
	}

	fortLookupCache.Store(pokestop.Id, FortLookup{
		FortType:                   POKESTOP,
		Lat:                        pokestop.Lat,
		Lon:                        pokestop.Lon,
		PowerUpLevel:               int8(valueOrMinus1(pokestop.PowerUpLevel)),
		IsArScanEligible:           pokestop.ArScanEligible.ValueOrZero() == 1,
		LureId:                     pokestop.LureId,
		QuestNoArRewardType:        int16(pokestop.QuestRewardType.ValueOrZero()),
		QuestNoArRewardAmount:      int16(pokestop.QuestRewardAmount.ValueOrZero()),
		QuestNoArRewardItemId:      int16(pokestop.QuestItemId.ValueOrZero()),
		QuestNoArRewardPokemonId:   int16(pokestop.QuestPokemonId.ValueOrZero()),
		QuestNoArRewardPokemonForm: int16(pokestop.QuestPokemonFormId.ValueOrZero()),
		QuestArRewardType:          int16(pokestop.AlternativeQuestRewardType.ValueOrZero()),
		QuestArRewardAmount:        int16(pokestop.AlternativeQuestRewardAmount.ValueOrZero()),
		QuestArRewardItemId:        int16(pokestop.AlternativeQuestItemId.ValueOrZero()),
		QuestArRewardPokemonId:     int16(pokestop.AlternativeQuestPokemonId.ValueOrZero()),
		QuestArRewardPokemonForm:   int16(pokestop.AlternativeQuestPokemonFormId.ValueOrZero()),
		IncidentDisplayType:        incidentDisplayType,
		IncidentStyle:              incidentStyle,
		IncidentCharacter:          incidentCharacter,
		IncidentPokemonId:          incidentPokemonId,
		IncidentPokemonForm:        incidentPokemonForm,
		ContestPokemonId:           int16(pokestop.ShowcasePokemon.ValueOrZero()),
		ContestPokemonForm:         int16(pokestop.ShowcasePokemonForm.ValueOrZero()),
		ContestPokemonType:         int8(pokestop.ShowcasePokemonType.ValueOrZero()),
		ContestTotalEntries:        getContestTotalEntries(pokestop.ShowcaseRankings),
	})
}

func updateGymLookup(gym *Gym) {
	fortLookupCache.Store(gym.Id, FortLookup{
		FortType:            GYM,
		Lat:                 gym.Lat,
		Lon:                 gym.Lon,
		PowerUpLevel:        int8(valueOrMinus1(gym.PowerUpLevel)),
		IsArScanEligible:    gym.ArScanEligible.ValueOrZero() == 1,
		AvailableSlots:      int8(gym.AvailableSlots.ValueOrZero()),
		TeamId:              int8(gym.TeamId.ValueOrZero()),
		RaidEndTimestamp:    gym.RaidEndTimestamp.ValueOrZero(),
		RaidBattleTimestamp: gym.RaidBattleTimestamp.ValueOrZero(),
		RaidLevel:           int8(gym.RaidLevel.ValueOrZero()),
		RaidPokemonId:       int16(gym.RaidPokemonId.ValueOrZero()),
		RaidPokemonForm:     int16(gym.RaidPokemonForm.ValueOrZero()),
	})
}

func updateStationLookup(station *Station) {
	updateStationLookupWithBattles(station, getKnownStationBattles(station.Id, time.Now().Unix()))
}

func updateStationLookupWithBattles(station *Station, stationBattles []StationBattleData) {
	battles := buildFortLookupStationBattlesFromSlice(stationBattles)
	lookup := FortLookup{
		FortType:       STATION,
		Lat:            station.Lat,
		Lon:            station.Lon,
		StationBattles: battles,
	}
	applyTopStationBattleToFortLookup(&lookup, stationBattles)
	fortLookupCache.Store(station.Id, lookup)
}

// updatePokestopIncidentLookup updates the incident fields on a pokestop's FortLookup entry
func updatePokestopIncidentLookup(pokestopId string, incident *Incident) {
	existing, ok := fortLookupCache.Load(pokestopId)
	if !ok {
		return
	}

	existing.IncidentDisplayType = int8(incident.DisplayType)
	existing.IncidentStyle = int8(incident.Style)
	existing.IncidentCharacter = incident.Character
	existing.IncidentPokemonId = int16(incident.Slot1PokemonId.ValueOrZero())
	existing.IncidentPokemonForm = int16(incident.Slot1Form.ValueOrZero())

	fortLookupCache.Store(pokestopId, existing)
}

// getContestTotalEntries parses showcase rankings JSON to get total entries
func getContestTotalEntries(rankingsString null.String) int16 {
	if !rankingsString.Valid {
		return -1
	}

	type contestJson struct {
		TotalEntries int `json:"total_entries"`
	}
	var cj contestJson
	if json.Unmarshal([]byte(rankingsString.String), &cj) == nil {
		return int16(cj.TotalEntries)
	}
	return -1
}

func addFortToTree(id string, lat float64, lon float64) {
	fortTreeMutex.Lock()
	fortTree.Insert([2]float64{lon, lat}, [2]float64{lon, lat}, id)
	fortTreeMutex.Unlock()
}

var fortTreeEvictor *treeEvictor[string]

// flushFortTreeEvictions: see flushTreeEvictions.
func flushFortTreeEvictions(entries []treeEvictionEntry[string]) {
	flushTreeEvictions(&fortTreeMutex, &fortTree, entries)
}

// deferFortEviction is the eviction-callback cleanup path: lookup cache is
// cleared inline (lock-free) so scans skip the fort immediately, tree
// removal is batched. ttlcache runs eviction callbacks on their own
// goroutines, racing fort saves and genericUpdateFort's synchronous
// cleanup, so guard before touching shared state:
//   - lookup entry already gone → a deleted fort; genericUpdateFort already
//     removed the tree point, and enqueueing an unpaired delete here could
//     erase the point of a fort restored in the meantime.
//   - lookup entry belongs to a different fort type → the fort converted
//     (pokestop↔gym) and this is the stale counterpart's cache entry
//     expiring; the live counterpart owns the lookup and tree point now.
func deferFortEviction(expected FortType, fortId string, lat, lon float64) {
	fl, ok := fortLookupCache.Load(fortId)
	if !ok || fl.FortType != expected {
		return
	}
	fortLookupCache.Delete(fortId)
	fortTreeEvictor.Enqueue(fortId, lat, lon)
}

func removeFortFromTree(fortId string, lat, lon float64) {
	fortTreeMutex.Lock()
	beforeLen := fortTree.Len()
	fortTree.Delete([2]float64{lon, lat}, [2]float64{lon, lat}, fortId)
	afterLen := fortTree.Len()
	fortTreeMutex.Unlock()

	if beforeLen != afterLen+1 {
		log.Debugf("FortRtree - removing %s, lat %f lon %f size %d->%d", fortId, lat, lon, beforeLen, afterLen)
	}
}

// GetFortLookup returns the FortLookup for the given fort ID, if it exists
func GetFortLookup(fortId string) (FortLookup, bool) {
	return fortLookupCache.Load(fortId)
}

// GetFortsInBounds returns all fort IDs within the given bounding box
func GetFortsInBounds(minLat, minLon, maxLat, maxLon float64) []string {
	var results []string
	fortTreeMutex.RLock()
	fortTree.Search([2]float64{minLon, minLat}, [2]float64{maxLon, maxLat}, func(min, max [2]float64, data string) bool {
		results = append(results, data)
		return true
	})
	fortTreeMutex.RUnlock()
	return results
}
