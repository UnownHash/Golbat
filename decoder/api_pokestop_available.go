package decoder

import (
	"time"

	log "github.com/sirupsen/logrus"
)

// ApiPokestopQuestAvailable is one distinct quest option (reward + title +
// target, AR/no-AR distinguished by WithAr) currently offered by resident
// pokestops, with how many forts offer it. Sourced solely from the maintained
// quest-conditions map (Task 3) — FortLookup itself omits title/target.
type ApiPokestopQuestAvailable struct {
	WithAr     bool   `json:"with_ar"`
	RewardType int16  `json:"reward_type"`
	ItemId     int16  `json:"item_id"`
	Amount     int16  `json:"amount"`
	PokemonId  int16  `json:"pokemon_id"`
	FormId     int16  `json:"form_id"`
	Title      string `json:"title"`
	Target     int32  `json:"target"`
	Count      int    `json:"count"`
}

// ApiPokestopInvasionAvailable is one distinct active invasion (grunt/leader/
// showcase character + slot1 reward) currently present on resident
// pokestops, with how many forts carry it. Sourced from FortLookup.Incidents.
type ApiPokestopInvasionAvailable struct {
	Character      int16 `json:"character"`
	DisplayType    int16 `json:"display_type"`
	Confirmed      bool  `json:"confirmed"`
	Slot1PokemonId int16 `json:"slot1_pokemon_id"`
	Slot1Form      int16 `json:"slot1_form"`
	Count          int   `json:"count"`
}

// ApiPokestopLureAvailable is one distinct active lure type, with how many
// resident pokestops currently carry it.
type ApiPokestopLureAvailable struct {
	LureId int16 `json:"lure_id"`
	Count  int   `json:"count"`
}

// ApiPokestopShowcaseAvailable is one distinct active showcase contest
// (pokemon/form/type), with how many resident pokestops currently run it.
type ApiPokestopShowcaseAvailable struct {
	PokemonId int16 `json:"pokemon_id"`
	Form      int16 `json:"form"`
	TypeId    int8  `json:"type_id"`
	Count     int   `json:"count"`
}

// ApiAvailablePokestops is the whole-instance snapshot served by
// GET /api/pokestop/available.
type ApiAvailablePokestops struct {
	Quests    []ApiPokestopQuestAvailable    `json:"quests"`
	Invasions []ApiPokestopInvasionAvailable `json:"invasions"`
	Lures     []ApiPokestopLureAvailable     `json:"lures"`
	Showcases []ApiPokestopShowcaseAvailable `json:"showcases"`
}

// GetAvailablePokestops returns the distinct lures, active showcases, active
// invasions, and available quest options currently offered by resident
// pokestops, each with a count of how many forts carry it. Quests are sourced
// solely from the maintained quest-conditions map (Task 3); lures, showcases,
// and invasions come from a single fortLookupCache.Range pass. The same pass
// tallies FortLookup's own quest-reward fields and cross-checks that tally
// against the maintained map (verifyQuestAggregate) to catch reconciliation
// drift between the two.
func GetAvailablePokestops(now int64) *ApiAvailablePokestops {
	start := time.Now()
	res := &ApiAvailablePokestops{}
	forts, incidents := 0, 0
	lures := map[int16]int{}
	shows := map[ApiPokestopShowcaseAvailable]int{} // key without Count
	inv := map[ApiPokestopInvasionAvailable]int{}   // key without Count

	// Quests (rewards + title/target) come solely from the maintained conditions map — distinct+counted.
	// ApiQuestConditionResult and ApiPokestopQuestAvailable share identical fields (name/order/type), so
	// a direct conversion carries every field without restating them.
	for _, c := range GetAvailableQuestConditions() {
		res.Quests = append(res.Quests, ApiPokestopQuestAvailable(c))
	}

	// ONE range: lures + showcases + invasions (response) + a quest-reward tally (verification only).
	rewards := map[questRewardKey]int{} // direct FortLookup reward count — cross-checks the maintained map
	fortLookupCache.Range(func(_ string, fl FortLookup) bool {
		if fl.FortType != POKESTOP {
			return true
		}
		forts++
		if fl.LureId != 0 && fl.LureExpireTimestamp > now {
			lures[fl.LureId]++
		}
		if fl.ContestPokemonId != 0 && fl.ShowcaseExpiry > now {
			shows[ApiPokestopShowcaseAvailable{PokemonId: fl.ContestPokemonId, Form: fl.ContestPokemonForm, TypeId: fl.ContestPokemonType}]++
		}
		for _, in := range fl.Incidents {
			if in.ExpireTimestamp <= now {
				continue
			}
			incidents++
			inv[ApiPokestopInvasionAvailable{
				Character: in.Character, DisplayType: int16(in.DisplayType), Confirmed: in.Confirmed,
				Slot1PokemonId: in.Slot1PokemonId, Slot1Form: in.Slot1Form,
			}]++
		}
		if fl.QuestNoArRewardType != 0 {
			rewards[questRewardKey{false, fl.QuestNoArRewardType, fl.QuestNoArRewardItemId, fl.QuestNoArRewardAmount, fl.QuestNoArRewardPokemonId, fl.QuestNoArRewardPokemonForm}]++
		}
		if fl.QuestArRewardType != 0 {
			rewards[questRewardKey{true, fl.QuestArRewardType, fl.QuestArRewardItemId, fl.QuestArRewardAmount, fl.QuestArRewardPokemonId, fl.QuestArRewardPokemonForm}]++
		}
		return true
	})

	for id, n := range lures {
		res.Lures = append(res.Lures, ApiPokestopLureAvailable{LureId: id, Count: n})
	}
	for k, n := range shows {
		k.Count = n
		res.Showcases = append(res.Showcases, k)
	}
	for k, n := range inv {
		k.Count = n
		res.Invasions = append(res.Invasions, k)
	}

	verifyQuestAggregate(rewards) // alert if the maintained map drifted from the direct FortLookup tally
	logAvailablePokestops(time.Since(start), forts, incidents, res)
	return res
}

// questRewardKey is the reward signature shared by the maintained conditions map (minus title/target)
// and the FortLookup reward tally used to detect reconciliation drift.
type questRewardKey struct {
	WithAr                                        bool
	RewardType, ItemId, Amount, PokemonId, FormId int16
}

// verifyQuestAggregate is a Debug-level diagnostic, not a production alarm. It cross-checks the
// maintained conditions map against a direct FortLookup tally. Invariant: for each reward signature,
// sum(map counts over title/target) == resident forts carrying it. In practice benign, transient
// divergences occur routinely and are indistinguishable here from a real reconciliation bug:
//
//   - Read-skew: the FortLookup reward tally and GetAvailableQuestConditions() are read at different
//     instants, while updatePokestopLookup does its Store(FortLookup) then reconcile(map) non-atomically.
//   - Pokestop→gym conversion lag: the maintained map keeps a converted stop's quest count until the
//     stale pokestopCache entry evicts (up to the fort TTL, ~25h), but this range no longer tallies it.
//
// So a persistent mismatch is not reliably distinguishable from noise at this log level; a proper
// metric-based drift alarm (excluding converted stops from the comparison) is a documented follow-up.
func verifyQuestAggregate(fortRewards map[questRewardKey]int) {
	mapRewards := map[questRewardKey]int{}
	for _, c := range GetAvailableQuestConditions() {
		mapRewards[questRewardKey{c.WithAr, c.RewardType, c.ItemId, c.Amount, c.PokemonId, c.FormId}] += c.Count
	}
	desync := 0
	for k, fortN := range fortRewards {
		if mapRewards[k] != fortN {
			desync++
			log.Debugf("quest aggregate desync %+v: fortLookup=%d map=%d", k, fortN, mapRewards[k])
		}
	}
	for k := range mapRewards {
		if _, ok := fortRewards[k]; !ok {
			desync++
			log.Debugf("quest aggregate desync %+v: fortLookup=0 map=%d", k, mapRewards[k])
		}
	}
	if desync > 0 {
		log.Debugf("quest aggregate desync: %d reward signatures differ (FortLookup tally vs maintained map)", desync)
	}
}

// logAvailablePokestops records the available-pokestops build time in the
// api_scan_duration histogram (StatsCollector.ObserveApiScan) and logs a
// summary of the scan.
func logAvailablePokestops(dur time.Duration, forts, incidents int, res *ApiAvailablePokestops) {
	if statsCollector != nil {
		statsCollector.ObserveApiScan("available-pokestops", dur.Seconds())
	}
	log.Infof("available-pokestops built in %s: scanned %d forts / %d incidents -> %d quests, %d invasions, %d lures, %d showcases",
		dur, forts, incidents, len(res.Quests), len(res.Invasions), len(res.Lures), len(res.Showcases))
}
