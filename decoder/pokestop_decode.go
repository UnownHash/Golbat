package decoder

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/guregu/null/v6"
	log "github.com/sirupsen/logrus"

	"golbat/pogo"
	"golbat/tz"
	"golbat/util"
)

var LureTime int64 = 1800

func (stop *Pokestop) updatePokestopFromFort(fortData *pogo.PokemonFortProto, cellId uint64, now int64) *Pokestop {
	stop.SetId(fortData.FortId)
	stop.SetLat(fortData.Latitude)
	stop.SetLon(fortData.Longitude)

	stop.SetPartnerId(null.NewString(fortData.PartnerId, fortData.PartnerId != ""))
	stop.SetSponsorId(null.IntFrom(int64(fortData.Sponsor)))
	stop.SetEnabled(null.BoolFrom(fortData.Enabled))
	stop.SetArScanEligible(null.IntFrom(util.BoolToInt[int64](fortData.IsArScanEligible)))
	stop.SetPowerUpPoints(null.IntFrom(int64(fortData.PowerUpProgressPoints)))
	powerUpLevel, powerUpEndTimestamp := calculatePowerUpPoints(fortData)
	stop.SetPowerUpLevel(powerUpLevel)
	stop.SetPowerUpEndTimestamp(powerUpEndTimestamp)

	// lasModifiedMs is also modified when incident happens
	lastModifiedTimestamp := fortData.LastModifiedMs / 1000
	stop.SetLastModifiedTimestamp(null.IntFrom(lastModifiedTimestamp))

	if len(fortData.ActiveFortModifier) > 0 {
		lureId := int16(fortData.ActiveFortModifier[0])
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

	if fortData.ImageUrl != "" {
		stop.SetUrl(null.StringFrom(fortData.ImageUrl))
	}
	stop.SetCellId(null.IntFrom(int64(cellId)))

	if stop.Deleted {
		stop.SetDeleted(false)
		log.Warnf("Cleared Stop with id '%s' is found again in GMO, therefore un-deleted", stop.Id)
		// Restore in fort tracker if enabled
		if fortTracker != nil {
			fortTracker.RestoreFort(stop.Id, cellId, false, time.Now().Unix())
		}
	}
	return stop
}

func (stop *Pokestop) updatePokestopFromQuestProto(questProto *pogo.FortSearchOutProto, haveAr bool) string {

	if questProto.ChallengeQuest == nil {
		log.Debugf("Received blank quest")
		return "Blank quest"
	}
	questData := questProto.ChallengeQuest.Quest
	questTitle := questProto.ChallengeQuest.QuestDisplay.Description
	questType := int64(questData.QuestType)
	questTarget := int64(questData.Goal.Target)
	questTemplate := strings.ToLower(questData.TemplateId)

	conditions := []map[string]any{}
	rewards := []map[string]any{}

	for _, conditionData := range questData.Goal.Condition {
		condition := make(map[string]any)
		infoData := make(map[string]any)
		condition["type"] = int(conditionData.Type)
		switch conditionData.Type {
		case pogo.QuestConditionProto_WITH_BADGE_TYPE:
			info := conditionData.GetWithBadgeType()
			infoData["amount"] = info.Amount
			infoData["badge_rank"] = info.BadgeRank
			badgeTypeById := []int{}
			for _, badge := range info.BadgeType {
				badgeTypeById = append(badgeTypeById, int(badge))
			}
			infoData["badge_types"] = badgeTypeById

		case pogo.QuestConditionProto_WITH_ITEM:
			info := conditionData.GetWithItem()
			if int(info.Item) != 0 {
				infoData["item_id"] = int(info.Item)
			}
		case pogo.QuestConditionProto_WITH_RAID_LEVEL:
			info := conditionData.GetWithRaidLevel()
			raidLevelById := []int{}
			for _, raidLevel := range info.RaidLevel {
				raidLevelById = append(raidLevelById, int(raidLevel))
			}
			infoData["raid_levels"] = raidLevelById
		case pogo.QuestConditionProto_WITH_POKEMON_TYPE:
			info := conditionData.GetWithPokemonType()
			pokemonTypesById := []int{}
			for _, t := range info.PokemonType {
				pokemonTypesById = append(pokemonTypesById, int(t))
			}
			infoData["pokemon_type_ids"] = pokemonTypesById
		case pogo.QuestConditionProto_WITH_POKEMON_CATEGORY:
			info := conditionData.GetWithPokemonCategory()
			if info.CategoryName != "" {
				infoData["category_name"] = info.CategoryName
			}
			pokemonById := []int{}
			for _, pokemon := range info.PokemonIds {
				pokemonById = append(pokemonById, int(pokemon))
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
			infoData["cell_ids"] = info.S2CellId
		case pogo.QuestConditionProto_WITH_DISTANCE:
			info := conditionData.GetWithDistance()
			infoData["distance"] = info.DistanceKm
		case pogo.QuestConditionProto_WITH_POKEMON_ALIGNMENT:
			info := conditionData.GetWithPokemonAlignment()
			alignmentIds := []int{}
			for _, alignment := range info.Alignment {
				alignmentIds = append(alignmentIds, int(alignment))
			}
			infoData["alignment_ids"] = alignmentIds
		case pogo.QuestConditionProto_WITH_INVASION_CHARACTER:
			info := conditionData.GetWithInvasionCharacter()
			characterCategoryIds := []int{}
			for _, characterCategory := range info.Category {
				characterCategoryIds = append(characterCategoryIds, int(characterCategory))
			}
			infoData["character_category_ids"] = characterCategoryIds
		case pogo.QuestConditionProto_WITH_NPC_COMBAT:
			info := conditionData.GetWithNpcCombat()
			infoData["win"] = info.RequiresWin
			infoData["template_ids"] = info.CombatNpcTrainerId
		case pogo.QuestConditionProto_WITH_PLAYER_LEVEL:
			info := conditionData.GetWithPlayerLevel()
			infoData["level"] = info.Level
		case pogo.QuestConditionProto_WITH_BUDDY:
			info := conditionData.GetWithBuddy()
			if info != nil {
				infoData["min_buddy_level"] = int(info.MinBuddyLevel)
				infoData["must_be_on_map"] = info.MustBeOnMap
			} else {
				infoData["min_buddy_level"] = 0
				infoData["must_be_on_map"] = false
			}
		case pogo.QuestConditionProto_WITH_DAILY_BUDDY_AFFECTION:
			info := conditionData.GetWithDailyBuddyAffection()
			infoData["min_buddy_affection_earned_today"] = info.MinBuddyAffectionEarnedToday
		case pogo.QuestConditionProto_WITH_TEMP_EVO_POKEMON:
			info := conditionData.GetWithTempEvoId()
			tempEvoIds := []int{}
			for _, evolution := range info.MegaForm {
				tempEvoIds = append(tempEvoIds, int(evolution))
			}
			infoData["raid_pokemon_evolutions"] = tempEvoIds
		case pogo.QuestConditionProto_WITH_ITEM_TYPE:
			info := conditionData.GetWithItemType()
			itemTypes := []int{}
			for _, itemType := range info.ItemType {
				itemTypes = append(itemTypes, int(itemType))
			}
			infoData["item_type_ids"] = itemTypes
		case pogo.QuestConditionProto_WITH_RAID_ELAPSED_TIME:
			info := conditionData.GetWithElapsedTime()
			infoData["time"] = int64(info.ElapsedTimeMs) / 1000
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
		default:
			break
		}

		if infoData != nil {
			condition["info"] = infoData
		}
		conditions = append(conditions, condition)
	}

	// Extract first reward details for indexed columns
	var rewardType, rewardAmount, rewardItemId, rewardPokemonId, rewardPokemonFormId null.Int

	for i, rewardData := range questData.QuestRewards {
		reward := make(map[string]any)
		infoData := make(map[string]any)
		reward["type"] = int(rewardData.Type)

		// For the first reward, also populate the indexed column values
		isFirst := i == 0
		if isFirst {
			rewardType = null.IntFrom(int64(rewardData.Type))
		}

		switch rewardData.Type {
		case pogo.QuestRewardProto_EXPERIENCE:
			infoData["amount"] = rewardData.GetExp()
			if isFirst {
				rewardAmount = null.IntFrom(int64(rewardData.GetExp()))
			}
		case pogo.QuestRewardProto_ITEM:
			info := rewardData.GetItem()
			infoData["amount"] = info.Amount
			infoData["item_id"] = int(info.Item)
			if isFirst {
				rewardAmount = null.IntFrom(int64(info.Amount))
				rewardItemId = null.IntFrom(int64(info.Item))
			}
		case pogo.QuestRewardProto_STARDUST:
			infoData["amount"] = rewardData.GetStardust()
			if isFirst {
				rewardAmount = null.IntFrom(int64(rewardData.GetStardust()))
			}
		case pogo.QuestRewardProto_CANDY:
			info := rewardData.GetCandy()
			infoData["amount"] = info.Amount
			infoData["pokemon_id"] = int(info.PokemonId)
			if isFirst {
				rewardAmount = null.IntFrom(int64(info.Amount))
				rewardPokemonId = null.IntFrom(int64(info.PokemonId))
			}
		case pogo.QuestRewardProto_XL_CANDY:
			info := rewardData.GetXlCandy()
			infoData["amount"] = info.Amount
			infoData["pokemon_id"] = int(info.PokemonId)
			if isFirst {
				rewardAmount = null.IntFrom(int64(info.Amount))
				rewardPokemonId = null.IntFrom(int64(info.PokemonId))
			}
		case pogo.QuestRewardProto_POKEMON_ENCOUNTER:
			info := rewardData.GetPokemonEncounter()
			if info.IsHiddenDitto {
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
			if info.ShinyProbability > 0.0 {
				infoData["shiny_probability"] = info.ShinyProbability
			}
			if display := info.PokemonDisplay; display != nil {
				if costumeId := int(display.Costume); costumeId != 0 {
					infoData["costume_id"] = costumeId
				}
				if formId := int(display.Form); formId != 0 {
					infoData["form_id"] = formId
					if isFirst {
						rewardPokemonFormId = null.IntFrom(int64(formId))
					}
				}
				if genderId := int(display.Gender); genderId != 0 {
					infoData["gender_id"] = genderId
				}
				if display.Shiny {
					infoData["shiny"] = display.Shiny
				}
				if background := util.ExtractBackgroundFromDisplay(display); background != nil {
					infoData["background"] = background
				}
				if breadMode := int(display.BreadModeEnum); breadMode != 0 {
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
			infoData["amount"] = info.Amount
			infoData["sticker_id"] = info.StickerId
			if isFirst {
				rewardAmount = null.IntFrom(int64(info.Amount))
			}
		case pogo.QuestRewardProto_MEGA_RESOURCE:
			info := rewardData.GetMegaResource()
			infoData["amount"] = info.Amount
			infoData["pokemon_id"] = int(info.PokemonId)
			if isFirst {
				rewardAmount = null.IntFrom(int64(info.Amount))
				rewardPokemonId = null.IntFrom(int64(info.PokemonId))
			}
		case pogo.QuestRewardProto_AVATAR_CLOTHING:
		case pogo.QuestRewardProto_QUEST:
		case pogo.QuestRewardProto_LEVEL_CAP:
		case pogo.QuestRewardProto_INCIDENT:
		case pogo.QuestRewardProto_PLAYER_ATTRIBUTE:
		default:
			break
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

	if questExpiry.Valid == false {
		questExpiry = null.IntFrom(time.Now().Unix() + 24*60*60) // Set expiry to 24 hours from now
	}

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
	}

	return questTitle
}

func (stop *Pokestop) updatePokestopFromFortDetailsProto(fortData *pogo.FortDetailsOutProto) *Pokestop {
	stop.SetId(fortData.Id)
	stop.SetLat(fortData.Latitude)
	stop.SetLon(fortData.Longitude)
	if len(fortData.ImageUrl) > 0 {
		stop.SetUrl(null.StringFrom(fortData.ImageUrl[0]))
	}
	stop.SetName(null.StringFrom(fortData.Name))

	if fortData.Description == "" {
		stop.SetDescription(null.NewString("", false))
	} else {
		stop.SetDescription(null.StringFrom(fortData.Description))
	}

	if fortData.Modifier != nil && len(fortData.Modifier) > 0 {
		// DeployingPlayerCodename contains the name of the player if we want that
		lureId := int16(fortData.Modifier[0].ModifierType)
		lureExpiry := fortData.Modifier[0].ExpirationTimeMs / 1000

		stop.SetLureId(lureId)
		stop.SetLureExpireTimestamp(null.IntFrom(lureExpiry))
	}

	return stop
}

func (stop *Pokestop) updatePokestopFromGetMapFortsOutProto(fortData *pogo.GetMapFortsOutProto_FortProto) *Pokestop {
	stop.SetId(fortData.Id)
	stop.SetLat(fortData.Latitude)
	stop.SetLon(fortData.Longitude)

	if len(fortData.Image) > 0 {
		stop.SetUrl(null.StringFrom(fortData.Image[0].Url))
	}
	stop.SetName(null.StringFrom(fortData.Name))
	if stop.Deleted {
		log.Debugf("Cleared Stop with id '%s' is found again in GMF, therefore kept deleted", stop.Id)
	}
	return stop
}

func (stop *Pokestop) updatePokestopFromGetContestDataOutProto(contest *pogo.ContestProto) {
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

func (stop *Pokestop) updatePokestopFromGetPokemonSizeContestEntryOutProto(contestData *pogo.GetPokemonSizeLeaderboardEntryOutProto) {
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
	j.TotalEntries = int(contestData.TotalEntries)

	for _, entry := range contestData.GetContestEntries() {
		rank := entry.GetRank()
		if rank > 3 {
			break
		}
		j.ContestEntries = append(j.ContestEntries, contestEntry{
			Rank:                  int(rank),
			Score:                 entry.GetScore(),
			PokemonId:             int(entry.GetPokedexId()),
			Form:                  int(entry.GetPokemonDisplay().Form),
			Costume:               int(entry.GetPokemonDisplay().Costume),
			Gender:                int(entry.GetPokemonDisplay().Gender),
			Shiny:                 entry.GetPokemonDisplay().Shiny,
			TempEvolution:         int(entry.GetPokemonDisplay().CurrentTempEvolution),
			TempEvolutionFinishMs: entry.GetPokemonDisplay().TemporaryEvolutionFinishMs,
			Alignment:             int(entry.GetPokemonDisplay().Alignment),
			Badge:                 int(entry.GetPokemonDisplay().PokemonBadge),
			Background:            util.ExtractBackgroundFromDisplay(entry.PokemonDisplay),
		})

	}
	jsonString, _ := json.Marshal(j)
	stop.SetShowcaseRankings(null.StringFrom(string(jsonString)))
}
