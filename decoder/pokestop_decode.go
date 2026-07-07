package decoder

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/guregu/null/v6"
	log "github.com/sirupsen/logrus"

	"golbat/pogo"
	"golbat/pogoshim"
	"golbat/tz"
	"golbat/util"
)

var LureTime int64 = 1800

func (stop *Pokestop) updatePokestopFromFort(fortData pogoshim.PokemonFortProto, cellId uint64, now int64) *Pokestop {
	stop.SetId(fortData.GetFortId())
	stop.SetLat(fortData.GetLatitude())
	stop.SetLon(fortData.GetLongitude())

	partnerId := fortData.GetPartnerId()
	stop.SetPartnerId(null.NewString(partnerId, partnerId != ""))
	stop.SetSponsorId(null.IntFrom(int64(fortData.GetSponsor())))
	stop.SetEnabled(null.BoolFrom(fortData.GetEnabled()))
	stop.SetArScanEligible(null.IntFrom(util.BoolToInt[int64](fortData.GetIsArScanEligible())))
	stop.SetPowerUpPoints(null.IntFrom(int64(fortData.GetPowerUpProgressPoints())))
	powerUpLevel, powerUpEndTimestamp := calculatePowerUpPoints(fortData)
	stop.SetPowerUpLevel(powerUpLevel)
	stop.SetPowerUpEndTimestamp(powerUpEndTimestamp)

	// lasModifiedMs is also modified when incident happens
	lastModifiedTimestamp := fortData.GetLastModifiedMs() / 1000
	stop.SetLastModifiedTimestamp(null.IntFrom(lastModifiedTimestamp))

	activeFortModifier := fortData.GetActiveFortModifier()
	if activeFortModifier.Len() > 0 {
		lureId := int16(activeFortModifier.At(0).Enum())
		if lureId >= 501 && lureId <= 510 {
			lureEnd := lastModifiedTimestamp + LureTime
			oldLureEnd := stop.LureExpireTimestamp.ValueOrZero()
			if stop.LureId != lureId {
				stop.SetLureExpireTimestamp(null.IntFrom(lureEnd))
				stop.SetLureId(lureId)
			} else {
				// wait some time after lure end before a restart in case of timing issue
				if now > oldLureEnd+30 {
					for now > lureEnd {
						lureEnd += LureTime
					}
					// lure needs to be restarted
					stop.SetLureExpireTimestamp(null.IntFrom(lureEnd))
				}
			}
		}
	}

	if imageUrl := fortData.GetImageUrl(); imageUrl != "" {
		stop.SetUrl(null.StringFrom(imageUrl))
	}
	stop.SetCellId(null.IntFrom(int64(cellId)))

	if stop.Deleted {
		stop.SetDeleted(false)
		log.Warnf("Cleared Stop with id '%s' is found again in GMO, therefore un-deleted", stop.Id)
		// Restore in fort tracker if enabled
		if fortTracker != nil {
			fortTracker.RestoreFort(stop.Id, cellId, false, now*1000)
		}
	}
	return stop
}

func (stop *Pokestop) updatePokestopFromQuestProto(questProto pogoshim.FortSearchOutProto, haveAr bool) string {

	if !questProto.HasChallengeQuest() {
		log.Debugf("Received blank quest")
		return "Blank quest"
	}
	challengeQuest := questProto.GetChallengeQuest()
	questData := challengeQuest.GetQuest()
	questTitle := challengeQuest.GetQuestDisplay().GetDescription()
	goal := questData.GetGoal()
	questType := int64(questData.GetQuestType())
	questTarget := int64(goal.GetTarget())
	questTemplate := strings.ToLower(questData.GetTemplateId())

	conditions := []map[string]any{}
	rewards := []map[string]any{}

	for conditionData := range goal.GetCondition().All() {
		condition := make(map[string]any)
		infoData := make(map[string]any)
		condition["type"] = int(conditionData.GetType())
		switch conditionData.GetType() {
		case pogo.QuestConditionProto_WITH_BADGE_TYPE:
			info := conditionData.GetWithBadgeType()
			infoData["amount"] = info.GetAmount()
			infoData["badge_rank"] = info.GetBadgeRank()
			badgeTypeById := []int{}
			badgeTypes := info.GetBadgeType()
			for i := 0; i < badgeTypes.Len(); i++ {
				badgeTypeById = append(badgeTypeById, int(badgeTypes.At(i).Enum()))
			}
			infoData["badge_types"] = badgeTypeById

		case pogo.QuestConditionProto_WITH_ITEM:
			info := conditionData.GetWithItem()
			if int(info.GetItem()) != 0 {
				infoData["item_id"] = int(info.GetItem())
			}
		case pogo.QuestConditionProto_WITH_RAID_LEVEL:
			info := conditionData.GetWithRaidLevel()
			raidLevelById := []int{}
			raidLevels := info.GetRaidLevel()
			for i := 0; i < raidLevels.Len(); i++ {
				raidLevelById = append(raidLevelById, int(raidLevels.At(i).Enum()))
			}
			infoData["raid_levels"] = raidLevelById
		case pogo.QuestConditionProto_WITH_POKEMON_TYPE:
			info := conditionData.GetWithPokemonType()
			pokemonTypesById := []int{}
			types := info.GetPokemonType()
			for i := 0; i < types.Len(); i++ {
				pokemonTypesById = append(pokemonTypesById, int(types.At(i).Enum()))
			}
			infoData["pokemon_type_ids"] = pokemonTypesById
		case pogo.QuestConditionProto_WITH_POKEMON_CATEGORY:
			info := conditionData.GetWithPokemonCategory()
			if info.GetCategoryName() != "" {
				infoData["category_name"] = info.GetCategoryName()
			}
			pokemonById := []int{}
			pokemonIds := info.GetPokemonIds()
			for i := 0; i < pokemonIds.Len(); i++ {
				pokemonById = append(pokemonById, int(pokemonIds.At(i).Enum()))
			}
			infoData["pokemon_ids"] = pokemonById
		case pogo.QuestConditionProto_WITH_WIN_RAID_STATUS:
		case pogo.QuestConditionProto_WITH_THROW_TYPE:
			info := conditionData.GetWithThrowType()
			if int(info.GetThrowType()) != 0 { // TODO: RDM has ThrowType here, ensure it is the same thing
				infoData["throw_type_id"] = int(info.GetThrowType())
			}
			infoData["hit"] = info.GetHit()
		case pogo.QuestConditionProto_WITH_THROW_TYPE_IN_A_ROW:
			info := conditionData.GetWithThrowType()
			if int(info.GetThrowType()) != 0 {
				infoData["throw_type_id"] = int(info.GetThrowType())
			}
			infoData["hit"] = info.GetHit()
		case pogo.QuestConditionProto_WITH_LOCATION:
			info := conditionData.GetWithLocation()
			// Raw slice assignment (not the []int{}-then-append idiom used
			// above) -- preserve nil-vs-empty exactly: json.Marshal renders
			// a nil slice as null and a zero-length slice as [], and the
			// pre-shim code passed the proto's own (possibly nil) []int64
			// straight through.
			var cellIds []int64
			if ids := info.GetS2CellId(); ids.Len() > 0 {
				cellIds = make([]int64, ids.Len())
				for i := range cellIds {
					cellIds[i] = ids.At(i).Int()
				}
			}
			infoData["cell_ids"] = cellIds
		case pogo.QuestConditionProto_WITH_DISTANCE:
			info := conditionData.GetWithDistance()
			infoData["distance"] = info.GetDistanceKm()
		case pogo.QuestConditionProto_WITH_POKEMON_ALIGNMENT:
			info := conditionData.GetWithPokemonAlignment()
			alignmentIds := []int{}
			alignments := info.GetAlignment()
			for i := 0; i < alignments.Len(); i++ {
				alignmentIds = append(alignmentIds, int(alignments.At(i).Enum()))
			}
			infoData["alignment_ids"] = alignmentIds
		case pogo.QuestConditionProto_WITH_INVASION_CHARACTER:
			info := conditionData.GetWithInvasionCharacter()
			characterCategoryIds := []int{}
			categories := info.GetCategory()
			for i := 0; i < categories.Len(); i++ {
				characterCategoryIds = append(characterCategoryIds, int(categories.At(i).Enum()))
			}
			infoData["character_category_ids"] = characterCategoryIds
		case pogo.QuestConditionProto_WITH_NPC_COMBAT:
			info := conditionData.GetWithNpcCombat()
			infoData["win"] = info.GetRequiresWin()
			// Same nil-vs-empty preservation as WITH_LOCATION above.
			var templateIds []string
			if ids := info.GetCombatNpcTrainerId(); ids.Len() > 0 {
				templateIds = make([]string, ids.Len())
				for i := range templateIds {
					templateIds[i] = ids.At(i)
				}
			}
			infoData["template_ids"] = templateIds
		case pogo.QuestConditionProto_WITH_PLAYER_LEVEL:
			info := conditionData.GetWithPlayerLevel()
			infoData["level"] = info.GetLevel()
		case pogo.QuestConditionProto_WITH_BUDDY:
			info := conditionData.GetWithBuddy()
			if !info.IsZero() {
				infoData["min_buddy_level"] = int(info.GetMinBuddyLevel())
				infoData["must_be_on_map"] = info.GetMustBeOnMap()
			} else {
				infoData["min_buddy_level"] = 0
				infoData["must_be_on_map"] = false
			}
		case pogo.QuestConditionProto_WITH_DAILY_BUDDY_AFFECTION:
			info := conditionData.GetWithDailyBuddyAffection()
			infoData["min_buddy_affection_earned_today"] = info.GetMinBuddyAffectionEarnedToday()
		case pogo.QuestConditionProto_WITH_TEMP_EVO_POKEMON:
			info := conditionData.GetWithTempEvoId()
			tempEvoIds := []int{}
			forms := info.GetMegaForm()
			for i := 0; i < forms.Len(); i++ {
				tempEvoIds = append(tempEvoIds, int(forms.At(i).Enum()))
			}
			infoData["raid_pokemon_evolutions"] = tempEvoIds
		case pogo.QuestConditionProto_WITH_ITEM_TYPE:
			info := conditionData.GetWithItemType()
			itemTypes := []int{}
			types := info.GetItemType()
			for i := 0; i < types.Len(); i++ {
				itemTypes = append(itemTypes, int(types.At(i).Enum()))
			}
			infoData["item_type_ids"] = itemTypes
		case pogo.QuestConditionProto_WITH_RAID_ELAPSED_TIME:
			info := conditionData.GetWithElapsedTime()
			infoData["time"] = info.GetElapsedTimeMs() / 1000
		case pogo.QuestConditionProto_WITH_WIN_GYM_BATTLE_STATUS:
		case pogo.QuestConditionProto_WITH_SUPER_EFFECTIVE_CHARGE:
		case pogo.QuestConditionProto_WITH_UNIQUE_POKESTOP:
		case pogo.QuestConditionProto_WITH_QUEST_CONTEXT:
		case pogo.QuestConditionProto_WITH_WIN_BATTLE_STATUS:
		case pogo.QuestConditionProto_WITH_CURVE_BALL:
		case pogo.QuestConditionProto_WITH_NEW_FRIEND:
		case pogo.QuestConditionProto_WITH_DAYS_IN_A_ROW:
		case pogo.QuestConditionProto_WITH_WEATHER_BOOST:
		case pogo.QuestConditionProto_WITH_DAILY_CAPTURE_BONUS:
		case pogo.QuestConditionProto_WITH_DAILY_SPIN_BONUS:
		case pogo.QuestConditionProto_WITH_UNIQUE_POKEMON:
		case pogo.QuestConditionProto_WITH_BUDDY_INTERESTING_POI:
		case pogo.QuestConditionProto_WITH_POKEMON_LEVEL:
		case pogo.QuestConditionProto_WITH_SINGLE_DAY:
		case pogo.QuestConditionProto_WITH_UNIQUE_POKEMON_TEAM:
		case pogo.QuestConditionProto_WITH_MAX_CP:
		case pogo.QuestConditionProto_WITH_LUCKY_POKEMON:
		case pogo.QuestConditionProto_WITH_LEGENDARY_POKEMON:
		case pogo.QuestConditionProto_WITH_GBL_RANK:
		case pogo.QuestConditionProto_WITH_CATCHES_IN_A_ROW:
		case pogo.QuestConditionProto_WITH_ENCOUNTER_TYPE:
		case pogo.QuestConditionProto_WITH_COMBAT_TYPE:
		case pogo.QuestConditionProto_WITH_GEOTARGETED_POI:
		case pogo.QuestConditionProto_WITH_FRIEND_LEVEL:
		case pogo.QuestConditionProto_WITH_STICKER:
		case pogo.QuestConditionProto_WITH_POKEMON_CP:
		case pogo.QuestConditionProto_WITH_RAID_LOCATION:
		case pogo.QuestConditionProto_WITH_FRIENDS_RAID:
		case pogo.QuestConditionProto_WITH_POKEMON_COSTUME:
		}

		condition["info"] = infoData
		conditions = append(conditions, condition)
	}

	// Extract first reward details for indexed columns
	var rewardType, rewardAmount, rewardItemId, rewardPokemonId, rewardPokemonFormId null.Int

	rewardsList := questData.GetQuestRewards()
	for i := 0; i < rewardsList.Len(); i++ {
		rewardData := rewardsList.At(i)
		reward := make(map[string]any)
		infoData := make(map[string]any)
		reward["type"] = int(rewardData.GetType())

		// For the first reward, also populate the indexed column values
		isFirst := i == 0
		if isFirst {
			rewardType = null.IntFrom(int64(rewardData.GetType()))
		}

		switch rewardData.GetType() {
		case pogo.QuestRewardProto_EXPERIENCE:
			infoData["amount"] = rewardData.GetExp()
			if isFirst {
				rewardAmount = null.IntFrom(int64(rewardData.GetExp()))
			}
		case pogo.QuestRewardProto_ITEM:
			info := rewardData.GetItem()
			infoData["amount"] = info.GetAmount()
			infoData["item_id"] = int(info.GetItem())
			if isFirst {
				rewardAmount = null.IntFrom(int64(info.GetAmount()))
				rewardItemId = null.IntFrom(int64(info.GetItem()))
			}
		case pogo.QuestRewardProto_STARDUST:
			infoData["amount"] = rewardData.GetStardust()
			if isFirst {
				rewardAmount = null.IntFrom(int64(rewardData.GetStardust()))
			}
		case pogo.QuestRewardProto_CANDY:
			info := rewardData.GetCandy()
			infoData["amount"] = info.GetAmount()
			infoData["pokemon_id"] = int(info.GetPokemonId())
			if isFirst {
				rewardAmount = null.IntFrom(int64(info.GetAmount()))
				rewardPokemonId = null.IntFrom(int64(info.GetPokemonId()))
			}
		case pogo.QuestRewardProto_XL_CANDY:
			info := rewardData.GetXlCandy()
			infoData["amount"] = info.GetAmount()
			infoData["pokemon_id"] = int(info.GetPokemonId())
			if isFirst {
				rewardAmount = null.IntFrom(int64(info.GetAmount()))
				rewardPokemonId = null.IntFrom(int64(info.GetPokemonId()))
			}
		case pogo.QuestRewardProto_POKEMON_ENCOUNTER:
			info := rewardData.GetPokemonEncounter()
			if info.GetIsHiddenDitto() {
				infoData["pokemon_id"] = 132
				infoData["pokemon_id_display"] = int(info.GetPokemonId())
				if isFirst {
					rewardPokemonId = null.IntFrom(132)
				}
			} else {
				infoData["pokemon_id"] = int(info.GetPokemonId())
				if isFirst {
					rewardPokemonId = null.IntFrom(int64(info.GetPokemonId()))
				}
			}
			if info.GetShinyProbability() > 0.0 {
				infoData["shiny_probability"] = info.GetShinyProbability()
			}
			if info.HasPokemonDisplay() {
				display := info.GetPokemonDisplay()
				if costumeId := int(display.GetCostume()); costumeId != 0 {
					infoData["costume_id"] = costumeId
				}
				if formId := int(display.GetForm()); formId != 0 {
					infoData["form_id"] = formId
					if isFirst {
						rewardPokemonFormId = null.IntFrom(int64(formId))
					}
				}
				if genderId := int(display.GetGender()); genderId != 0 {
					infoData["gender_id"] = genderId
				}
				if display.GetShiny() {
					infoData["shiny"] = display.GetShiny()
				}
				if background := util.ExtractBackgroundFromDisplayShim(display); background != nil {
					infoData["background"] = background
				}
				if breadMode := int(display.GetBreadModeEnum()); breadMode != 0 {
					infoData["bread_mode"] = breadMode
				}
			}
		case pogo.QuestRewardProto_POKECOIN:
			infoData["amount"] = rewardData.GetPokecoin()
			if isFirst {
				rewardAmount = null.IntFrom(int64(rewardData.GetPokecoin()))
			}
		case pogo.QuestRewardProto_STICKER:
			info := rewardData.GetSticker()
			infoData["amount"] = info.GetAmount()
			infoData["sticker_id"] = info.GetStickerId()
			if isFirst {
				rewardAmount = null.IntFrom(int64(info.GetAmount()))
			}
		case pogo.QuestRewardProto_MEGA_RESOURCE:
			info := rewardData.GetMegaResource()
			infoData["amount"] = info.GetAmount()
			infoData["pokemon_id"] = int(info.GetPokemonId())
			if isFirst {
				rewardAmount = null.IntFrom(int64(info.GetAmount()))
				rewardPokemonId = null.IntFrom(int64(info.GetPokemonId()))
			}
		case pogo.QuestRewardProto_AVATAR_CLOTHING:
		case pogo.QuestRewardProto_QUEST:
		case pogo.QuestRewardProto_LEVEL_CAP:
		case pogo.QuestRewardProto_INCIDENT:
		case pogo.QuestRewardProto_PLAYER_ATTRIBUTE:
		}
		reward["info"] = infoData
		rewards = append(rewards, reward)
	}

	questConditions, _ := json.Marshal(conditions)
	questRewards, _ := json.Marshal(rewards)
	questTimestamp := time.Now().Unix()

	questExpiry := null.NewInt(0, false)

	stopTimezone := tz.SearchTimezone(stop.Lat, stop.Lon)
	if stopTimezone != "" {
		loc, err := time.LoadLocation(stopTimezone)
		if err != nil {
			log.Warnf("Unrecognised time zone %s at %f,%f", stopTimezone, stop.Lat, stop.Lon)
		} else {
			year, month, day := time.Now().In(loc).Date()
			t := time.Date(year, month, day, 0, 0, 0, 0, loc).AddDate(0, 0, 1)
			unixTime := t.Unix()
			questExpiry = null.IntFrom(unixTime)
		}
	}

	if !questExpiry.Valid {
		questExpiry = null.IntFrom(time.Now().Unix() + 24*60*60) // Set expiry to 24 hours from now
	}

	questSeed := null.IntFrom(questData.GetQuestSeed())

	if !haveAr {
		stop.SetAlternativeQuestType(null.IntFrom(questType))
		stop.SetAlternativeQuestTarget(null.IntFrom(questTarget))
		stop.SetAlternativeQuestTemplate(null.StringFrom(questTemplate))
		stop.SetAlternativeQuestTitle(null.StringFrom(questTitle))
		stop.SetAlternativeQuestConditions(null.StringFrom(string(questConditions)))
		stop.SetAlternativeQuestRewards(null.StringFrom(string(questRewards)))
		stop.SetAlternativeQuestTimestamp(null.IntFrom(questTimestamp))
		stop.SetAlternativeQuestExpiry(questExpiry)
		stop.SetAlternativeQuestRewardType(rewardType)
		stop.SetAlternativeQuestItemId(rewardItemId)
		stop.SetAlternativeQuestRewardAmount(rewardAmount)
		stop.SetAlternativeQuestPokemonId(rewardPokemonId)
		stop.SetAlternativeQuestPokemonFormId(rewardPokemonFormId)
		stop.SetAlternativeQuestSeed(questSeed)
	} else {
		stop.SetQuestType(null.IntFrom(questType))
		stop.SetQuestTarget(null.IntFrom(questTarget))
		stop.SetQuestTemplate(null.StringFrom(questTemplate))
		stop.SetQuestTitle(null.StringFrom(questTitle))
		stop.SetQuestConditions(null.StringFrom(string(questConditions)))
		stop.SetQuestRewards(null.StringFrom(string(questRewards)))
		stop.SetQuestTimestamp(null.IntFrom(questTimestamp))
		stop.SetQuestExpiry(questExpiry)
		stop.SetQuestRewardType(rewardType)
		stop.SetQuestItemId(rewardItemId)
		stop.SetQuestRewardAmount(rewardAmount)
		stop.SetQuestPokemonId(rewardPokemonId)
		stop.SetQuestPokemonFormId(rewardPokemonFormId)
		stop.SetQuestSeed(questSeed)
	}

	return questTitle
}

func (stop *Pokestop) updatePokestopFromFortDetailsProto(fortData pogoshim.FortDetailsOutProto) *Pokestop {
	stop.SetId(fortData.GetId())
	stop.SetLat(fortData.GetLatitude())
	stop.SetLon(fortData.GetLongitude())
	if imageUrls := fortData.GetImageUrl(); imageUrls.Len() > 0 {
		stop.SetUrl(null.StringFrom(imageUrls.At(0)))
	}
	stop.SetName(null.StringFrom(fortData.GetName()))

	if fortData.GetDescription() == "" {
		stop.SetDescription(null.NewString("", false))
	} else {
		stop.SetDescription(null.StringFrom(fortData.GetDescription()))
	}

	if modifiers := fortData.GetModifier(); modifiers.Len() > 0 {
		// DeployingPlayerCodename contains the name of the player if we want that
		modifier := modifiers.At(0)
		lureId := int16(modifier.GetModifierType())
		lureExpiry := modifier.GetExpirationTimeMs() / 1000

		stop.SetLureId(lureId)
		stop.SetLureExpireTimestamp(null.IntFrom(lureExpiry))
	}

	return stop
}

func (stop *Pokestop) updatePokestopFromMapFortSummary(fortData mapFortSummary) *Pokestop {
	stop.SetId(fortData.Id)
	stop.SetLat(fortData.Latitude)
	stop.SetLon(fortData.Longitude)

	if fortData.ImageUrl != "" {
		stop.SetUrl(null.StringFrom(fortData.ImageUrl))
	}
	stop.SetName(null.StringFrom(fortData.Name))
	if stop.Deleted {
		log.Debugf("Cleared Stop with id '%s' is found again in GMF, therefore kept deleted", stop.Id)
	}
	return stop
}

func (stop *Pokestop) updatePokestopFromGetContestDataOutProto(contest pogoshim.ContestProto) {
	stop.SetShowcaseRankingStandard(null.IntFrom(int64(contest.GetMetric().GetRankingStandard())))
	stop.SetShowcaseExpiry(null.IntFrom(contest.GetSchedule().GetContestCycle().GetEndTimeMs() / 1000))

	focusStore := createFocusStoreFromContestProto(contest)

	if len(focusStore) > 1 {
		log.Warnf("SHOWCASE: we got more than one showcase focus: %v", focusStore)
	}

	for key, focus := range focusStore {
		focus["type"] = key
		jsonBytes, err := json.Marshal(focus)
		if err != nil {
			log.Errorf("SHOWCASE: Stop '%s' - Focus '%v' marshalling failed: %s", stop.Id, focus, err)
		}
		stop.SetShowcaseFocus(null.StringFrom(string(jsonBytes)))
		// still support old format - probably still required to filter in external tools
		stop.extractShowcasePokemonInfoDeprecated(key, focus)
	}
}

func (stop *Pokestop) updatePokestopFromGetPokemonSizeContestEntryOutProto(contestData pogoshim.GetPokemonSizeLeaderboardEntryOutProto) {
	type contestEntry struct {
		Rank                  int     `json:"rank"`
		Score                 float64 `json:"score"`
		PokemonId             int     `json:"pokemon_id"`
		Form                  int     `json:"form"`
		Costume               int     `json:"costume"`
		Gender                int     `json:"gender"`
		Shiny                 bool    `json:"shiny"`
		TempEvolution         int     `json:"temp_evolution"`
		TempEvolutionFinishMs int64   `json:"temp_evolution_finish_ms"`
		Alignment             int     `json:"alignment"`
		Badge                 int     `json:"badge"`
		Background            *int64  `json:"background,omitempty"`
	}
	type contestJson struct {
		TotalEntries   int            `json:"total_entries"`
		LastUpdate     int64          `json:"last_update"`
		ContestEntries []contestEntry `json:"contest_entries"`
	}

	j := contestJson{LastUpdate: time.Now().Unix()}
	j.TotalEntries = int(contestData.GetTotalEntries())

	var newTopScore null.Float
	var newTopPokemonId null.Int
	for entry := range contestData.GetContestEntries().All() {
		rank := entry.GetRank()
		if rank > 3 {
			break
		}
		if rank == 1 {
			newTopScore = null.FloatFrom(entry.GetScore())
			newTopPokemonId = null.IntFrom(int64(entry.GetPokedexId()))
		}
		display := entry.GetPokemonDisplay()
		j.ContestEntries = append(j.ContestEntries, contestEntry{
			Rank:                  int(rank),
			Score:                 entry.GetScore(),
			PokemonId:             int(entry.GetPokedexId()),
			Form:                  int(display.GetForm()),
			Costume:               int(display.GetCostume()),
			Gender:                int(display.GetGender()),
			Shiny:                 display.GetShiny(),
			TempEvolution:         int(display.GetCurrentTempEvolution()),
			TempEvolutionFinishMs: display.GetTemporaryEvolutionFinishMs(),
			Alignment:             int(display.GetAlignment()),
			Badge:                 int(display.GetPokemonBadge()),
			Background:            util.ExtractBackgroundFromDisplayShim(display),
		})

	}

	// Detect rank-1 leaderboard movement against the running top stored in
	// oldValues. The previous top is seeded from the loaded rankings JSON
	// in afterLoadFromDB and updated here from the parsed proto entries —
	// no further JSON re-parsing in any hot path.
	if newTopScore != stop.oldValues.ShowcaseTopScore || newTopPokemonId != stop.oldValues.ShowcaseTopPokemonId {
		stop.pokestopWebhookRequired = true
		stop.oldValues.ShowcaseTopScore = newTopScore
		stop.oldValues.ShowcaseTopPokemonId = newTopPokemonId
	}

	jsonString, _ := json.Marshal(j)
	stop.SetShowcaseRankings(null.StringFrom(string(jsonString)))
}
