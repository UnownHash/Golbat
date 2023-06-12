package decoder

import (
	"context"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/rtree"
	"golbat/db"
	"sync"
)

type FortLookup struct {
	IsGym           bool
	Lure            int16
	RaidLevel       int8
	RaidPokemonId   int16
	QuestRewardType int16
	QuestRewardId   int16
}

var fortLookupCache map[string]FortLookup
var fortTreeMutex sync.RWMutex
var fortTree rtree.RTreeG[string]

func initFortRtree() {
	fortLookupCache = make(map[string]FortLookup)
}

type IdRecord struct {
	Id string `db:"id"`
}

func LoadAllPokestops(details db.DbDetails) {
	var place IdRecord
	rows, err := details.GeneralDb.Queryx("SELECT id FROM pokestop")
	count := 0
	if err != nil {
		log.Errorf("FortRTree: Load Pokestops %s", err)
		return
	}
	for rows.Next() {
		if count%1000 == 0 {
			log.Infof("Loaded %d pokestops", count)
		}
		count++
		err := rows.StructScan(&place)
		if err != nil {
			log.Fatalln(err)
		}
		GetPokestopRecord(context.Background(), details, place.Id)
	}
	log.Infof("Loaded %d pokestops [finished]", count)
}

func LoadAllGyms(details db.DbDetails) {
	var place IdRecord
	rows, err := details.GeneralDb.Queryx("SELECT id FROM gym")
	count := 0
	if err != nil {
		log.Errorf("FortRTree: Load Gyms %s", err)
		return
	}
	for rows.Next() {
		if count%1000 == 0 {
			log.Infof("Loaded %d gyms", count)
		}
		count++
		err := rows.StructScan(&place)
		if err != nil {
			log.Fatalln(err)
		}
		getGymRecord(context.Background(), details, place.Id)
	}
	log.Infof("Loaded %d gyms [finished]", count)
}

func fortRtreeUpdatePokestopOnGet(pokestop *Pokestop) {
	fortTreeMutex.RLock()
	_, inMap := fortLookupCache[pokestop.Id]
	fortTreeMutex.RUnlock()
	if !inMap {
		addPokestopToTree(pokestop)
		// assumes lat,lon unchanged since ejected from cache, so do not add to rtree
		updatePokestopLookup(pokestop)
	}
}

func fortRtreeUpdateGymOnGet(gym *Gym) {
	fortTreeMutex.RLock()
	_, inMap := fortLookupCache[gym.Id]
	fortTreeMutex.RUnlock()
	if !inMap {
		addGymToTree(gym)
		// assumes lat,lon unchanged since ejected from cache, so do not add to rtree
		updateGymLookup(gym)
	}
}

func updatePokestopLookup(pokestop *Pokestop) {
	fortTreeMutex.Lock()
	fortLookupCache[pokestop.Id] = FortLookup{
		IsGym: false,
		Lure:  pokestop.LureId,
		//		RaidLevel:       pokestop.RaidLevel,
		//		RaidPokemonId:   pokestop.RaidPokemonId,
		//		QuestRewardType: pokestop.QuestRewardType,
		//		QuestRewardId:   pokestop.QuestRewardId,
	}
	fortTreeMutex.Unlock()
}

func updateGymLookup(gym *Gym) {
	fortTreeMutex.Lock()
	fortLookupCache[gym.Id] = FortLookup{
		IsGym:         true,
		RaidLevel:     int8(gym.RaidLevel.ValueOrZero()),
		RaidPokemonId: int16(gym.RaidPokemonId.ValueOrZero()),
	}
	fortTreeMutex.Unlock()
}

func addPokestopToTree(pokestop *Pokestop) {
	log.Infof("FortRtree - add pokestop %s, lat %f lon %f", pokestop.Id, pokestop.Lat, pokestop.Lon)

	fortTreeMutex.Lock()
	fortTree.Insert([2]float64{pokestop.Lon, pokestop.Lat}, [2]float64{pokestop.Lon, pokestop.Lat}, pokestop.Id)
	fortTreeMutex.Unlock()
}

func addGymToTree(gym *Gym) {
	log.Infof("FortRtree - add gym %s, lat %f lon %f", gym.Id, gym.Lat, gym.Lon)

	fortTreeMutex.Lock()
	fortTree.Insert([2]float64{gym.Lon, gym.Lat}, [2]float64{gym.Lon, gym.Lat}, gym.Id)
	fortTreeMutex.Unlock()
}
