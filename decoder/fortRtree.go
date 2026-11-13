package decoder

import (
	"sync"
	"sync/atomic"
	"time"

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
	IsArScanEligible bool

	// Gym
	AvailableSlots       int8
	TeamId               int8
	RaidEndTimestamp     int64 // used to check expiry at filter time
	RaidBattleTimestamp  int64
	RaidLevel            int8
	RaidPokemonEvolution int8 // tiny enum; int8 in RaidLevel's pad byte keeps the struct at its pre-enrichment size
	RaidPokemonId        int16
	RaidPokemonForm      int16

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
	ContestPokemonId        int16
	ContestPokemonForm      int16
	ContestPokemonType      int8
	ShowcaseRankingStandard int8  // tiny enum; int8 shares the pad before ShowcaseExpiry — zero struct growth
	ShowcaseBuddyMinLevel   int8  // Buddy focus projection; same pad — zero struct growth
	ShowcaseExpiry          int64 // used to check expiry at filter time

	// Station
	StationEndTimestamp int64 // station end_time; liveness gate at filter time
	BattleEndTimestamp  int64 // used to check expiry at filter time
	BattleLevel         int8
	BattlePokemonId     int16
	BattlePokemonForm   int16
	StationBattles      []FortLookupStationBattle
	TotalStationedGmax  int16
	// The three fields below occupy the struct's tail padding (offsets
	// 178–183 after TotalStationedGmax), so they add no bytes per fort;
	// TestFortLookupSizeUnchangedByStationAvailability pins that.
	BattleAvailable       bool   // is_battle_available as last decoded (battle_available filter; says nothing about liveness)
	StationInactive       bool   // is_inactive as last decoded (part of station_active)
	StationStartTimestamp uint32 // station start_time, unix seconds (part of station_active)
}

var fortLookupCache *xsync.Map[FortId, FortLookup]
var fortTreeMutex sync.RWMutex
var fortTree rtree.RTreeG[FortId]

var fortTreeSnapshot atomic.Pointer[treeSnapshot[FortId]]

// getFortTreeSnapshot: see refreshTreeSnapshot.
func getFortTreeSnapshot() *rtree.RTreeG[FortId] {
	return refreshTreeSnapshot(&fortTreeSnapshot, &fortTreeMutex, &fortTree)
}

func initFortRtree() {
	// Fort tree churn is a fraction of pokemon's, and this evictor's only
	// producer drops on full (deferFortEviction) — 64k of headroom is
	// plenty and saves ~7.5 MiB vs sharing the pokemon constant.
	fortTreeEvictor = newTreeEvictor[FortId]("fort", 65536, treeEvictorBatchSize, flushFortTreeEvictions)
	fortLookupCache = xsync.NewMap[FortId, FortLookup]()
	initQuestConditions()
	initFortAvailability()

	// OnEviction registrations live here, after fortTreeEvictor and
	// fortLookupCache are created (and after pokestopCache/gymCache/
	// stationCache are created by initDataCache before calling this
	// function), so callbacks can never observe a nil evictor or lookup
	// cache. Mirrors the structure of initPokemonRtree.
	//
	// Pokestop/gym/station cache keys are all FortId natively, so eviction
	// callbacks hand the key straight to the FortId-keyed eviction path.
	if config.Config.FortInMemory {
		pokestopCache.OnEviction(func(id FortId, _ *Pokestop, _ ottercache.EvictionReason) {
			deferFortEviction(POKESTOP, id)
		})
		gymCache.OnEviction(func(id FortId, _ *Gym, _ ottercache.EvictionReason) {
			deferFortEviction(GYM, id)
		})
	}

	stationCache.OnEviction(func(stationId FortId, _ *Station, _ ottercache.EvictionReason) {
		clearStationBattleState(stationId)
		if config.Config.FortInMemory {
			deferFortEviction(STATION, stationId)
		}
	})
}

// storeFortIndex serializes ownership changes with removals from the shared
// spatial index. The entity's caller holds its record lock, or is preloading.
func storeFortIndex(id FortId, next FortLookup, onlyIfMissing bool, questKeys []questConditionKey) bool {
	fortTreeMutex.Lock()
	defer fortTreeMutex.Unlock()

	old, found := fortLookupCache.Load(id)
	if found && onlyIfMissing {
		return false
	}

	if !found || old.Lat != next.Lat || old.Lon != next.Lon {
		if found {
			point := [2]float64{old.Lon, old.Lat}
			fortTree.Delete(point, point, id)
		}
		point := [2]float64{next.Lon, next.Lat}
		// An eviction may have removed the lookup before its tree delete
		// runs. Replace that leftover point when restoring at the same spot.
		fortTree.Delete(point, point, id)
		fortTree.Insert(point, point, id)
	}

	if next.FortType == POKESTOP {
		// Incident writers use the lookup's per-key lock independently of
		// the tree. Keep their fields when refreshing an existing PokéStop.
		fortLookupCache.Compute(id, func(current FortLookup, loaded bool) (FortLookup, xsync.ComputeOp) {
			if loaded && current.FortType == POKESTOP {
				next.Incidents = current.Incidents
			}
			return next, xsync.UpdateOp
		})
		reconcileFortQuestConditions(id, questKeys)
	} else {
		fortLookupCache.Store(id, next)
		if found && old.FortType == POKESTOP {
			removeFortQuestConditions(id)
		}
	}
	return true
}

// removeFortIndexLocked removes the lookup only when it still belongs to kind.
// The caller holds fortTreeMutex through removal or enqueue of the tree point.
func removeFortIndexLocked(kind FortType, id FortId) (FortLookup, bool) {
	current, found := fortLookupCache.Load(id)
	if !found || current.FortType != kind {
		return FortLookup{}, false
	}
	if kind == POKESTOP {
		removeFortQuestConditions(id)
	}
	fortLookupCache.Delete(id)
	return current, true
}

func removeFortIndex(kind FortType, id FortId) {
	fortTreeMutex.Lock()
	defer fortTreeMutex.Unlock()

	if old, removed := removeFortIndexLocked(kind, id); removed {
		point := [2]float64{old.Lon, old.Lat}
		fortTree.Delete(point, point, id)
	}
}

// fortRtreeUpdatePokestopOnSave updates rtree and lookup cache when a pokestop is saved.
func fortRtreeUpdatePokestopOnSave(pokestop *Pokestop) {
	if pokestop.Deleted {
		removeFortIndex(POKESTOP, pokestop.Id)
		return
	}
	updatePokestopLookup(pokestop, false)
}

// fortRtreeUpdateGymOnSave updates rtree and lookup cache when a gym is saved.
func fortRtreeUpdateGymOnSave(gym *Gym) {
	if gym.Deleted {
		removeFortIndex(GYM, gym.Id)
		return
	}
	updateGymLookup(gym, false)
}

func fortRtreeUpdateStationOnSave(station *Station) {
	updateStationLookupWithBattles(station.Id, station, getKnownStationBattles(station.Id, time.Now().Unix()), false)
}

func fortRtreeUpdatePokestopOnGet(pokestop *Pokestop) {
	if !pokestop.Deleted {
		updatePokestopLookup(pokestop, true)
	}
}

func fortRtreeUpdateGymOnGet(gym *Gym) {
	if !gym.Deleted {
		updateGymLookup(gym, true)
	}
}

func fortRtreeUpdateStationOnGet(station *Station) {
	updateStationLookupWithBattles(station.Id, station, getKnownStationBattles(station.Id, time.Now().Unix()), true)
}

func updatePokestopLookup(pokestop *Pokestop, onlyIfMissing bool) {
	id := pokestop.Id

	var showcaseFocus *ApiShowcaseFocus
	var showcaseBuddyMinLevel int8
	if pokestop.ShowcaseFocus.Valid {
		var err error
		showcaseFocus, showcaseBuddyMinLevel, err = parseShowcaseFocus(pokestop.ShowcaseFocus.ValueOrZero())
		if err != nil {
			log.Warnf("SHOWCASE: Stop '%s' - Invalid showcase_focus: %v", pokestop.Id, err)
		}
	}

	lookup := FortLookup{
		FortType:                   POKESTOP,
		Lat:                        pokestop.Lat,
		Lon:                        pokestop.Lon,
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
		ShowcaseRankingStandard:    int8(pokestop.ShowcaseRankingStandard.ValueOrZero()),
		ShowcaseBuddyMinLevel:      showcaseBuddyMinLevel,
		ShowcaseExpiry:             pokestop.ShowcaseExpiry.ValueOrZero(),
	}
	if storeFortIndex(id, lookup, onlyIfMissing, questConditionKeysFromPokestop(pokestop)) {
		observePokestop(&lookup, showcaseFocus, time.Now().Unix())
	}
}

func updateGymLookup(gym *Gym, onlyIfMissing bool) {
	id := gym.Id
	now := time.Now().Unix()
	fl := FortLookup{
		FortType:             GYM,
		Lat:                  gym.Lat,
		Lon:                  gym.Lon,
		IsArScanEligible:     gym.ArScanEligible.ValueOrZero() == 1,
		AvailableSlots:       int8(gym.AvailableSlots.ValueOrZero()),
		TeamId:               int8(gym.TeamId.ValueOrZero()),
		RaidEndTimestamp:     gym.RaidEndTimestamp.ValueOrZero(),
		RaidBattleTimestamp:  gym.RaidBattleTimestamp.ValueOrZero(),
		RaidLevel:            int8(gym.RaidLevel.ValueOrZero()),
		RaidPokemonId:        int16(gym.RaidPokemonId.ValueOrZero()),
		RaidPokemonForm:      int16(gym.RaidPokemonForm.ValueOrZero()),
		RaidPokemonEvolution: int8(gym.RaidPokemonEvolution.ValueOrZero()),
	}
	if storeFortIndex(id, fl, onlyIfMissing, nil) {
		observeRaid(&fl, now)
	}
}

func updateStationLookupWithBattles(id FortId, station *Station, stationBattles []StationBattleData, onlyIfMissing bool) {
	battles := buildFortLookupStationBattlesFromSlice(stationBattles)
	lookup := FortLookup{
		FortType:              STATION,
		Lat:                   station.Lat,
		Lon:                   station.Lon,
		StationBattles:        battles,
		TotalStationedGmax:    int16(station.TotalStationedGmax.ValueOrZero()),
		StationEndTimestamp:   station.EndTime,
		BattleAvailable:       station.IsBattleAvailable,
		StationInactive:       station.IsInactive,
		StationStartTimestamp: uint32(max(station.StartTime, 0)),
	}
	applyTopStationBattleToFortLookup(&lookup, stationBattles)
	if storeFortIndex(id, lookup, onlyIfMissing, nil) {
		observeStationBattles(&lookup, time.Now().Unix())
	}
}

// updatePokestopIncidentLookup upserts the observed incident into a pokestop's FortLookup
// incidents slice (keyed by DisplayType+Character, unique per active incident on a stop),
// pruning any expired entries in the same pass.
func updatePokestopIncidentLookup(pokestopId FortId, incident *Incident) {
	now := time.Now().Unix()
	updated := FortLookupIncident{
		Id:              incident.Id,
		DisplayType:     int8(incident.DisplayType),
		Character:       incident.Character,
		Confirmed:       incident.Confirmed,
		Slot1PokemonId:  int16(incident.Slot1PokemonId.ValueOrZero()),
		Slot1Form:       int16(incident.Slot1Form.ValueOrZero()),
		Slot2PokemonId:  int16(incident.Slot2PokemonId.ValueOrZero()),
		Slot2Form:       int16(incident.Slot2Form.ValueOrZero()),
		Slot3PokemonId:  int16(incident.Slot3PokemonId.ValueOrZero()),
		Slot3Form:       int16(incident.Slot3Form.ValueOrZero()),
		ExpireTimestamp: incident.ExpirationTime,
	}
	observeInvasion(&updated, now)
	// Incident updates run outside fortTreeMutex. Compute serializes this
	// write with storeFortIndex, which preserves Incidents on PokéStop refresh.
	fortLookupCache.Compute(pokestopId, func(existing FortLookup, loaded bool) (FortLookup, xsync.ComputeOp) {
		if !loaded || existing.FortType != POKESTOP {
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

var fortTreeEvictor *treeEvictor[FortId]

func flushFortTreeEvictions(entries []treeEvictionEntry[FortId]) {
	fortTreeMutex.Lock()
	defer fortTreeMutex.Unlock()

	for _, entry := range entries {
		if current, found := fortLookupCache.Load(entry.id); found && current.Lat == entry.lat && current.Lon == entry.lon {
			continue // A live record reclaimed this point since the delete was queued.
		}
		point := [2]float64{entry.lon, entry.lat}
		fortTree.Delete(point, point, entry.id)
	}
}

// Cache callbacks are dispatched asynchronously. A newer cache entry may
// already exist by the time the old one's eviction is delivered.
func deferFortEviction(kind FortType, id FortId) {
	fortTreeMutex.Lock()
	defer fortTreeMutex.Unlock()

	switch kind {
	case POKESTOP:
		if pokestopCache.Has(id) {
			return
		}
	case GYM:
		if gymCache.Has(id) {
			return
		}
	case STATION:
		if stationCache.Has(id) {
			return
		}
	}

	if old, removed := removeFortIndexLocked(kind, id); removed {
		fortTreeEvictor.TryEnqueue(id, old.Lat, old.Lon)
	}
}

// GetFortLookup returns the FortLookup for the given fort ID, if it exists
func GetFortLookup(fortId FortId) (FortLookup, bool) {
	return fortLookupCache.Load(fortId)
}

// GetFortsInBounds returns all fort IDs within the given bounding box
func GetFortsInBounds(minLat, minLon, maxLat, maxLon float64) []FortId {
	var results []FortId
	fortTreeMutex.RLock()
	fortTree.Search([2]float64{minLon, minLat}, [2]float64{maxLon, maxLat}, func(min, max [2]float64, data FortId) bool {
		results = append(results, data)
		return true
	})
	fortTreeMutex.RUnlock()
	return results
}
