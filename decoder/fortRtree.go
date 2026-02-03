package decoder

import (
	"encoding/json"
	"sync"

	"github.com/guregu/null/v6"
	"github.com/puzpuzpuz/xsync/v3"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/rtree"
)

type IncidentLookup struct {
	DisplayType      int8
	Style            int8
	Character        int16
	Slot1PokemonId   int16
	Slot1PokemonForm int16
	Slot2PokemonId   int16
	Slot2PokemonForm int16
	Slot3PokemonId   int16
	Slot3PokemonForm int16
}

type FortLookup struct {
	FortType         FortType
	PowerUpLevel     int8
	IsArScanEligible bool
	Lat              float64
	Lon              float64

	// Gym-specific fields
	AvailableSlots  int8
	TeamId          int8
	InBattle        bool
	RaidLevel       int8
	RaidPokemonId   int16
	RaidPokemonForm int16

	// Pokestop-specific fields
	LureId                     int16
	QuestNoArRewardType        int16
	QuestNoArRewardAmount      int16
	QuestNoArRewardItemId      int16
	QuestNoArRewardPokemonId   int16
	QuestNoArRewardPokemonForm int16
	QuestNoArType              int16
	QuestNoArTarget            int16
	QuestNoArTemplate          string
	QuestArRewardType          int16
	QuestArRewardAmount        int16
	QuestArRewardItemId        int16
	QuestArRewardPokemonId     int16
	QuestArRewardPokemonForm   int16
	QuestArType                int16
	QuestArTarget              int16
	QuestArTemplate            string
	Incidents                  []*IncidentLookup
	ContestPokemonId           int16
	ContestPokemonForm         int16
	ContestPokemonType1        int8
	ContestPokemonType2        int8
	ContestRankingStandard     int8
	ContestTotalEntries        int16
}

var fortLookupCache *xsync.MapOf[string, FortLookup]
var fortTreeMutex sync.RWMutex
var fortTree rtree.RTreeG[string]

func initFortRtree() {
	fortLookupCache = xsync.NewMapOf[string, FortLookup]()
}

type IdRecord struct {
	Id string `db:"id"`
}

// genericUpdateFort handles rtree updates for fort location changes and deletions
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
	updatePokestopLookup(pokestop)
}

// fortRtreeUpdateGymOnSave updates rtree and lookup cache when a gym is saved
func fortRtreeUpdateGymOnSave(gym *Gym) {
	genericUpdateFort(gym.Id, gym.Lat, gym.Lon, gym.Deleted)
	updateGymLookup(gym)
}

// fortRtreeUpdatePokestopOnGet updates rtree when a pokestop is loaded from DB (legacy pattern)
func fortRtreeUpdatePokestopOnGet(pokestop *Pokestop) {
	_, inMap := fortLookupCache.Load(pokestop.Id)
	if !inMap {
		addFortToTree(pokestop.Id, pokestop.Lat, pokestop.Lon)
		updatePokestopLookup(pokestop)
	}
}

// fortRtreeUpdateGymOnGet updates rtree when a gym is loaded from DB (legacy pattern)
func fortRtreeUpdateGymOnGet(gym *Gym) {
	_, inMap := fortLookupCache.Load(gym.Id)
	if !inMap {
		addFortToTree(gym.Id, gym.Lat, gym.Lon)
		updateGymLookup(gym)
	}
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

func updatePokestopLookup(pokestop *Pokestop) {
	contestTotalEntries := getContestTotalEntries(pokestop.ShowcaseRankings)

	fortLookupCache.Store(pokestop.Id, FortLookup{
		FortType:                   POKESTOP,
		PowerUpLevel:               int8(valueOrMinus1(pokestop.PowerUpLevel)),
		IsArScanEligible:           pokestop.ArScanEligible.ValueOrZero() == 1,
		Lat:                        pokestop.Lat,
		Lon:                        pokestop.Lon,
		LureId:                     pokestop.LureId,
		QuestNoArRewardType:        int16(pokestop.QuestRewardType.ValueOrZero()),
		QuestNoArRewardAmount:      int16(pokestop.QuestRewardAmount.ValueOrZero()),
		QuestNoArRewardItemId:      int16(pokestop.QuestItemId.ValueOrZero()),
		QuestNoArRewardPokemonId:   int16(pokestop.QuestPokemonId.ValueOrZero()),
		QuestNoArRewardPokemonForm: int16(pokestop.QuestPokemonFormId.ValueOrZero()),
		QuestNoArType:              int16(valueOrMinus1(pokestop.QuestType)),
		QuestNoArTarget:            int16(valueOrMinus1(pokestop.QuestTarget)),
		QuestNoArTemplate:          pokestop.QuestTemplate.ValueOrZero(),
		QuestArRewardType:          int16(pokestop.AlternativeQuestRewardType.ValueOrZero()),
		QuestArRewardAmount:        int16(pokestop.AlternativeQuestRewardAmount.ValueOrZero()),
		QuestArRewardItemId:        int16(pokestop.AlternativeQuestItemId.ValueOrZero()),
		QuestArRewardPokemonId:     int16(pokestop.AlternativeQuestPokemonId.ValueOrZero()),
		QuestArRewardPokemonForm:   int16(pokestop.AlternativeQuestPokemonFormId.ValueOrZero()),
		QuestArType:                int16(valueOrMinus1(pokestop.AlternativeQuestType)),
		QuestArTarget:              int16(valueOrMinus1(pokestop.AlternativeQuestTarget)),
		QuestArTemplate:            pokestop.AlternativeQuestTemplate.ValueOrZero(),
		ContestPokemonId:           int16(pokestop.ShowcasePokemon.ValueOrZero()),
		ContestPokemonForm:         int16(pokestop.ShowcasePokemonForm.ValueOrZero()),
		ContestPokemonType1:        int8(pokestop.ShowcasePokemonType.ValueOrZero()),
		ContestPokemonType2:        -1, // TODO: this should probably be saved in the db
		ContestRankingStandard:     int8(pokestop.ShowcaseRankingStandard.ValueOrZero()),
		ContestTotalEntries:        contestTotalEntries,
	})
}

func updateGymLookup(gym *Gym) {
	fortLookupCache.Store(gym.Id, FortLookup{
		FortType:         GYM,
		PowerUpLevel:     int8(valueOrMinus1(gym.PowerUpLevel)),
		Lat:              gym.Lat,
		Lon:              gym.Lon,
		IsArScanEligible: gym.ArScanEligible.ValueOrZero() == 1,
		AvailableSlots:   int8(gym.AvailableSlots.ValueOrZero()),
		TeamId:           int8(gym.TeamId.ValueOrZero()),
		InBattle:         gym.InBattle.ValueOrZero() == 1,
		RaidLevel:        int8(gym.RaidLevel.ValueOrZero()),
		RaidPokemonId:    int16(gym.RaidPokemonId.ValueOrZero()),
		RaidPokemonForm:  int16(gym.RaidPokemonForm.ValueOrZero()),
	})
}

func addFortToTree(id string, lat float64, lon float64) {
	fortTreeMutex.Lock()
	fortTree.Insert([2]float64{lon, lat}, [2]float64{lon, lat}, id)
	fortTreeMutex.Unlock()
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
