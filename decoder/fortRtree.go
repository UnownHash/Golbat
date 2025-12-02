package decoder

import (
	"encoding/json"
	"runtime"
	"sync"
	"sync/atomic"

	"golbat/db"

	"github.com/puzpuzpuz/xsync/v3"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/rtree"
	"gopkg.in/guregu/null.v4"
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

	AvailableSlots  int8
	TeamId          int8
	InBattle        bool
	RaidLevel       int8
	RaidPokemonId   int16
	RaidPokemonForm int16

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

func LoadAllPokestops(details db.DbDetails) {
	rows, err := details.GeneralDb.Queryx(`
		SELECT id, lat, lon, name, url, enabled, lure_expire_timestamp, last_modified_timestamp,
			updated, quest_type, quest_timestamp, quest_target, quest_conditions,
			quest_rewards, quest_template, quest_title,
			alternative_quest_type, alternative_quest_timestamp, alternative_quest_target,
			alternative_quest_conditions, alternative_quest_rewards,
			alternative_quest_template, alternative_quest_title, cell_id, deleted, lure_id, sponsor_id, partner_id,
			ar_scan_eligible, power_up_points, power_up_level, power_up_end_timestamp,
			quest_expiry, alternative_quest_expiry, description, showcase_pokemon_id, showcase_pokemon_form_id,
			showcase_pokemon_type_id, showcase_ranking_standard, showcase_expiry, showcase_rankings
		FROM pokestop`)
	if err != nil {
		log.Errorf("FortRTree: Load Pokestops %s", err)
		return
	}
	defer rows.Close()

	numWorkers := runtime.NumCPU()
	jobs := make(chan Pokestop, 100)
	var wg sync.WaitGroup
	var count int32

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for pokestop := range jobs {
				if pokestop.QuestRewards.Valid {
					var rewards []map[string]interface{}
					if json.Unmarshal([]byte(pokestop.QuestRewards.String), &rewards) == nil && len(rewards) > 0 {
						reward := rewards[0]
						if rType, ok := reward["type"].(float64); ok {
							pokestop.questRewardType = null.IntFrom(int64(rType))
						}
						if info, ok := reward["info"].(map[string]interface{}); ok {
							if fAmount, ok := info["amount"].(float64); ok {
								pokestop.questRewardAmount = null.IntFrom(int64(fAmount))
							}
							if fItemId, ok := info["item_id"].(float64); ok {
								pokestop.questRewardItemId = null.IntFrom(int64(fItemId))
							}
							if fPokemonId, ok := info["pokemon_id"].(float64); ok {
								pokestop.questRewardPokemonId = null.IntFrom(int64(fPokemonId))
							}
							if fFormId, ok := info["form_id"].(float64); ok {
								pokestop.questRewardPokemonForm = null.IntFrom(int64(fFormId))
							}
						}
					}
				}

				if pokestop.AlternativeQuestRewards.Valid {
					var rewards []map[string]interface{}
					if json.Unmarshal([]byte(pokestop.AlternativeQuestRewards.String), &rewards) == nil && len(rewards) > 0 {
						reward := rewards[0]
						if rType, ok := reward["type"].(float64); ok {
							pokestop.alternativeQuestRewardType = null.IntFrom(int64(rType))
						}
						if info, ok := reward["info"].(map[string]interface{}); ok {
							if fAmount, ok := info["amount"].(float64); ok {
								pokestop.alternativeQuestRewardAmount = null.IntFrom(int64(fAmount))
							}
							if fItemId, ok := info["item_id"].(float64); ok {
								pokestop.alternativeQuestRewardItemId = null.IntFrom(int64(fItemId))
							}
							if fPokemonId, ok := info["pokemon_id"].(float64); ok {
								pokestop.alternativeQuestRewardPokemonId = null.IntFrom(int64(fPokemonId))
							}
							if fFormId, ok := info["form_id"].(float64); ok {
								pokestop.alternativeQuestRewardPokemonForm = null.IntFrom(int64(fFormId))
							}
						}
					}
				}

				if pokestop.ShowcaseRankings.Valid {
					type contestJson struct {
						TotalEntries int `json:"total_entries"`
					}
					var cj contestJson
					if json.Unmarshal([]byte(pokestop.ShowcaseRankings.String), &cj) == nil {
						pokestop.showcaseTotalEntries = null.IntFrom(int64(cj.TotalEntries))
					}
				}

				fortRtreeUpdatePokestopOnSave(&pokestop)
				atomic.AddInt32(&count, 1)
				if c := atomic.LoadInt32(&count); c%10000 == 0 {
					log.Infof("Loaded %d pokestops", c)
				}
			}
		}()
	}

	for rows.Next() {
		var pokestop Pokestop
		err := rows.StructScan(&pokestop)
		if err != nil {
			log.Errorf("FortRTree: pokestop struct scan %s", err)
			continue
		}
		jobs <- pokestop
	}
	close(jobs)
	wg.Wait()

	log.Infof("Loaded %d pokestops [finished]", count)
}

func LoadAllGyms(details db.DbDetails) {
	rows, err := details.GeneralDb.Queryx(`
		SELECT id, lat, lon, name, url, last_modified_timestamp, raid_end_timestamp, raid_spawn_timestamp,
			raid_battle_timestamp, updated, raid_pokemon_id, guarding_pokemon_id, guarding_pokemon_display,
			available_slots, team_id, raid_level, enabled, ex_raid_eligible, in_battle, raid_pokemon_move_1,
			raid_pokemon_move_2, raid_pokemon_form, raid_pokemon_alignment, raid_pokemon_cp, raid_is_exclusive,
			cell_id, deleted, total_cp, first_seen_timestamp, raid_pokemon_gender, sponsor_id, partner_id,
			raid_pokemon_costume, raid_pokemon_evolution, ar_scan_eligible, power_up_level, power_up_points,
			power_up_end_timestamp, description, defenders, rsvps
		FROM gym`)
	if err != nil {
		log.Errorf("FortRTree: Load Gyms %s", err)
		return
	}
	defer rows.Close()

	numWorkers := runtime.NumCPU()
	jobs := make(chan Gym, 100)
	var wg sync.WaitGroup
	var count int32

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for gym := range jobs {
				fortRtreeUpdateGymOnSave(&gym)
				atomic.AddInt32(&count, 1)
				if c := atomic.LoadInt32(&count); c%10000 == 0 {
					log.Infof("Loaded %d gyms", c)
				}
			}
		}()
	}

	for rows.Next() {
		var gym Gym
		err := rows.StructScan(&gym)
		if err != nil {
			log.Errorf("FortRTree: gym struct scan %s", err)
			continue
		}
		jobs <- gym
	}
	close(jobs)
	wg.Wait()

	log.Infof("Loaded %d gyms [finished]", count)
}

func genericUpdateFort(id string, lat float64, lon float64, deleted bool) {
	oldFort, inMap := fortLookupCache.Load(id)

	if deleted {
		if inMap {
			fortLookupCache.Delete(id)
			removeFortFromTree(id, lat, lon)
		}
		return
	}

	if !inMap {
		addFortToTree(id, lat, lon)
	} else if lat != oldFort.Lat || lon != oldFort.Lon {
		removeFortFromTree(id, lat, lon)
		addFortToTree(id, lat, lon)
	}
}

func fortRtreeUpdatePokestopOnSave(pokestop *Pokestop) {
	genericUpdateFort(pokestop.Id, pokestop.Lat, pokestop.Lon, pokestop.Deleted)
	updatePokestopLookup(pokestop)
}

func fortRtreeUpdateGymOnSave(gym *Gym) {
	genericUpdateFort(gym.Id, gym.Lat, gym.Lon, gym.Deleted)
	updateGymLookup(gym)
}

func getQuestRewards(rewardsString null.String) (int16, int16, int16, int16, int16) {
	if !rewardsString.Valid {
		return 0, 0, 0, 0, 0
	}

	var rewards []map[string]interface{}
	err := json.Unmarshal([]byte(rewardsString.String), &rewards)
	if err != nil {
		return 0, 0, 0, 0, 0
	}

	if len(rewards) > 0 {
		reward := rewards[0]
		var rewardType, amount, itemId, pokemonId, formId int16

		if rType, ok := reward["type"].(float64); ok {
			rewardType = int16(rType)
		}
		if info, ok := reward["info"].(map[string]interface{}); ok {
			if fAmount, ok := info["amount"].(float64); ok {
				amount = int16(fAmount)
			}
			if fItemId, ok := info["item_id"].(float64); ok {
				itemId = int16(fItemId)
			}
			if fPokemonId, ok := info["pokemon_id"].(float64); ok {
				pokemonId = int16(fPokemonId)
			}
			if fFormId, ok := info["form_id"].(float64); ok {
				formId = int16(fFormId)
			}
		}
		return rewardType, amount, itemId, pokemonId, formId
	}
	return 0, 0, 0, 0, 0
}

func stringOrEmpty(s null.String) string {
	if s.Valid {
		return s.String
	}
	return ""
}

func updatePokestopLookup(pokestop *Pokestop) {

	fortLookupCache.Store(pokestop.Id, FortLookup{
		FortType:                   POKESTOP,
		PowerUpLevel:               int8(valueOrMinus1(pokestop.PowerUpLevel)),
		IsArScanEligible:           pokestop.ArScanEligible.ValueOrZero() == 1,
		Lat:                        pokestop.Lat,
		Lon:                        pokestop.Lon,
		LureId:                     pokestop.LureId,
		QuestArRewardType:          int16(valueOrMinus1(pokestop.alternativeQuestRewardType)),
		QuestArRewardAmount:        int16(valueOrMinus1(pokestop.alternativeQuestRewardAmount)),
		QuestArRewardItemId:        int16(valueOrMinus1(pokestop.alternativeQuestRewardItemId)),
		QuestArRewardPokemonId:     int16(valueOrMinus1(pokestop.alternativeQuestRewardPokemonId)),
		QuestArRewardPokemonForm:   int16(valueOrMinus1(pokestop.alternativeQuestRewardPokemonForm)),
		QuestArType:                int16(valueOrMinus1(pokestop.AlternativeQuestType)),
		QuestArTarget:              int16(valueOrMinus1(pokestop.AlternativeQuestTarget)),
		QuestArTemplate:            stringOrEmpty(pokestop.AlternativeQuestTemplate),
		QuestNoArRewardType:        int16(valueOrMinus1(pokestop.questRewardType)),
		QuestNoArRewardAmount:      int16(valueOrMinus1(pokestop.questRewardAmount)),
		QuestNoArRewardItemId:      int16(valueOrMinus1(pokestop.questRewardItemId)),
		QuestNoArRewardPokemonId:   int16(valueOrMinus1(pokestop.questRewardPokemonId)),
		QuestNoArRewardPokemonForm: int16(valueOrMinus1(pokestop.questRewardPokemonForm)),
		QuestNoArType:              int16(valueOrMinus1(pokestop.QuestType)),
		QuestNoArTarget:            int16(valueOrMinus1(pokestop.QuestTarget)),
		QuestNoArTemplate:          stringOrEmpty(pokestop.QuestTemplate),
		ContestPokemonId:           int16(pokestop.ShowcasePokemon.ValueOrZero()),
		ContestPokemonForm:         int16(pokestop.ShowcasePokemonForm.ValueOrZero()),
		ContestPokemonType1:        int8(pokestop.ShowcasePokemonType.ValueOrZero()),
		ContestPokemonType2:        -1, // TODO: this should probably be saved in the db
		ContestRankingStandard:     int8(pokestop.ShowcaseRankingStandard.ValueOrZero()),
		ContestTotalEntries:        int16(valueOrMinus1(pokestop.showcaseTotalEntries)),
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
	fortLookupCache.Delete(fortId)

	if beforeLen != afterLen+1 {
		log.Infof("FortRtree - UNEXPECTED removing %s, lat %f lon %f size %d->%d Map Len %d", fortId, lat, lon, beforeLen, afterLen, fortLookupCache.Size())
	}
}
