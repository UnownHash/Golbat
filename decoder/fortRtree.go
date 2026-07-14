package decoder

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/guregu/null/v6"
	"github.com/puzpuzpuz/xsync/v4"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/rtree"

	"golbat/config"

	"golbat/ottercache"
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
	LureExpireTimestamp        int64 // used to check expiry at filter time
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

	// Pokestop - incidents (all active incidents; slot1 only — slots 2/3 are unused).
	// Mirrors the StationBattles slice pattern.
	Incidents []FortLookupIncident

	// Pokestop - contest
	ContestPokemonId    int16
	ContestPokemonForm  int16
	ContestPokemonType  int8
	ContestTotalEntries int16
	ShowcaseExpiry      int64 // used to check expiry at filter time

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
	// Fort tree churn is a fraction of pokemon's, and this evictor's only
	// producer drops on full (deferFortEviction) — 64k of headroom is
	// plenty and saves ~7.5 MiB vs sharing the pokemon constant.
	fortTreeEvictor = newTreeEvictor[string]("fort", 65536, treeEvictorBatchSize, flushFortTreeEvictions)
	fortLookupCache = xsync.NewMap[string, FortLookup]()
	initQuestConditions()

	// OnEviction registrations live here, after fortTreeEvictor and
	// fortLookupCache are created (and after pokestopCache/gymCache/
	// stationCache are created by initDataCache before calling this
	// function), so callbacks can never observe a nil evictor or lookup
	// cache. Mirrors the structure of initPokemonRtree.
	if config.Config.FortInMemory {
		pokestopCache.OnEviction(func(_ string, p *Pokestop, _ ottercache.EvictionReason) {
			deferFortEviction(POKESTOP, p.Id, p.Lat, p.Lon)
		})
		gymCache.OnEviction(func(_ string, g *Gym, _ ottercache.EvictionReason) {
			deferFortEviction(GYM, g.Id, g.Lat, g.Lon)
		})
	}

	stationCache.OnEviction(func(stationId string, s *Station, _ ottercache.EvictionReason) {
		clearStationBattleState(stationId)
		if config.Config.FortInMemory {
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
	} else {
		// A deleted pokestop drops out of the lookup cache (genericUpdateFort
		// above) without a matching updatePokestopLookup, so reconcile its
		// quest contribution to zero here.
		removeFortQuestConditions(pokestop.Id)
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
	// Atomic per-key read-modify-write via Compute: this writer (under the
	// POKESTOP entity lock) and updatePokestopIncidentLookup (under the
	// INCIDENT entity lock) update the same key from different lock domains,
	// each preserving the other's fields. A plain Load->Store pair can
	// interleave and clobber. Keep the callback to field copies — the
	// showcase-rankings JSON parse is hoisted out.
	contestTotalEntries := getContestTotalEntries(pokestop.ShowcaseRankings)
	fortLookupCache.Compute(pokestop.Id, func(existing FortLookup, loaded bool) (FortLookup, xsync.ComputeOp) {
		nl := FortLookup{
			FortType:                   POKESTOP,
			Lat:                        pokestop.Lat,
			Lon:                        pokestop.Lon,
			PowerUpLevel:               int8(valueOrMinus1(pokestop.PowerUpLevel)),
			IsArScanEligible:           pokestop.ArScanEligible.ValueOrZero() == 1,
			LureId:                     pokestop.LureId,
			LureExpireTimestamp:        pokestop.LureExpireTimestamp.ValueOrZero(),
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
			ContestPokemonId:           int16(pokestop.ShowcasePokemon.ValueOrZero()),
			ContestPokemonForm:         int16(pokestop.ShowcasePokemonForm.ValueOrZero()),
			ContestPokemonType:         int8(pokestop.ShowcasePokemonType.ValueOrZero()),
			ContestTotalEntries:        contestTotalEntries,
			ShowcaseExpiry:             pokestop.ShowcaseExpiry.ValueOrZero(),
		}
		if loaded {
			nl.Incidents = existing.Incidents // preserve the incident writer's slice
		}
		return nl, xsync.UpdateOp
	})

	// This is the sole writer of a pokestop's FortLookup entry, so it is also
	// the single place quest-condition counts are reconciled: it fires on
	// cache-miss load, every save (incl. quest change), and startup preload.
	// FortLookup omits quest title/target, so the previous keys are recovered
	// from questFortKeys rather than the overwritten FortLookup.
	reconcileFortQuestConditions(pokestop.Id, questConditionKeysFromPokestop(pokestop))
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

// updatePokestopIncidentLookup upserts the observed incident into a pokestop's FortLookup
// incidents slice (keyed by DisplayType+Character, unique per active incident on a stop),
// pruning any expired entries in the same pass.
func updatePokestopIncidentLookup(pokestopId string, incident *Incident) {
	now := time.Now().Unix()
	updated := FortLookupIncident{
		DisplayType:     int8(incident.DisplayType),
		Style:           int8(incident.Style),
		Character:       incident.Character,
		Confirmed:       incident.Confirmed,
		Slot1PokemonId:  int16(incident.Slot1PokemonId.ValueOrZero()),
		Slot1Form:       int16(incident.Slot1Form.ValueOrZero()),
		ExpireTimestamp: incident.ExpirationTime,
	}
	// Atomic per-key read-modify-write via Compute — see updatePokestopLookup
	// for the cross-lock-domain clobber this prevents.
	fortLookupCache.Compute(pokestopId, func(existing FortLookup, loaded bool) (FortLookup, xsync.ComputeOp) {
		if !loaded {
			return existing, xsync.CancelOp
		}
		out := existing.Incidents[:0:0] // fresh backing array; never mutate a shared slice in place
		replaced := false
		for _, inc := range existing.Incidents {
			if inc.ExpireTimestamp <= now {
				continue // prune expired
			}
			if inc.DisplayType == updated.DisplayType && inc.Character == updated.Character {
				out = append(out, updated) // replace the same incident
				replaced = true
			} else {
				out = append(out, inc)
			}
		}
		if !replaced && updated.ExpireTimestamp > now {
			out = append(out, updated)
		}
		existing.Incidents = out
		return existing, xsync.UpdateOp
	})
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
// removal is batched. Eviction callbacks arrive on the cache dispatcher
// goroutine — async relative to fort saves and genericUpdateFort's
// synchronous cleanup — so guard before touching shared state:
//   - lookup entry already gone → a deleted fort; genericUpdateFort already
//     removed the tree point, and enqueueing an unpaired delete here could
//     erase the point of a fort restored in the meantime.
//   - lookup entry belongs to a different fort type → the fort converted
//     (pokestop↔gym) and this is the stale counterpart's cache entry
//     expiring; the live counterpart owns the lookup and tree point now.
func deferFortEviction(expected FortType, fortId string, lat, lon float64) {
	// A pokestop leaving the cache must drop its quest-condition contribution
	// whether its FortLookup entry is still the resident pokestop one (matched,
	// deleted just below), was overwritten by a converted gym/station
	// counterpart (mismatch), or is already gone (deleted). questFortKeys only
	// ever holds pokestop entries, so this is a no-op miss for gyms/stations and
	// for pokestops already reconciled to no-quest. Because deferFortEviction
	// deletes the matched FortLookup entry, removing the count here keeps the
	// aggregate in lockstep with lookup-cache membership.
	if expected == POKESTOP {
		removeFortQuestConditions(fortId)
	}

	fl, ok := fortLookupCache.Load(fortId)
	if !ok || fl.FortType != expected {
		return
	}
	fortLookupCache.Delete(fortId)
	// Non-blocking for the same reason as the pokemon eviction callback:
	// the cache's eviction dispatcher must never park on a full channel.
	fortTreeEvictor.TryEnqueue(fortId, lat, lon)
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
