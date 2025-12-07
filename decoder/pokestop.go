package decoder

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"github.com/paulmach/orb/geojson"
	log "github.com/sirupsen/logrus"
	"gopkg.in/guregu/null.v4"

	"golbat/config"
	"golbat/db"
	"golbat/pogo"
	"golbat/tz"
	"golbat/util"
	"golbat/webhooks"
)

// Pokestop struct.
// REMINDER! Keep hasChangesPokestop updated after making changes
type Pokestop struct {
	Id                         string      `db:"id" json:"id"`
	Lat                        float64     `db:"lat" json:"lat"`
	Lon                        float64     `db:"lon" json:"lon"`
	Name                       null.String `db:"name" json:"name"`
	Url                        null.String `db:"url" json:"url"`
	LureExpireTimestamp        null.Int    `db:"lure_expire_timestamp" json:"lure_expire_timestamp"`
	LastModifiedTimestamp      null.Int    `db:"last_modified_timestamp" json:"last_modified_timestamp"`
	Updated                    int64       `db:"updated" json:"updated"`
	Enabled                    null.Bool   `db:"enabled" json:"enabled"`
	QuestType                  null.Int    `db:"quest_type" json:"quest_type"`
	QuestTimestamp             null.Int    `db:"quest_timestamp" json:"quest_timestamp"`
	QuestTarget                null.Int    `db:"quest_target" json:"quest_target"`
	QuestConditions            null.String `db:"quest_conditions" json:"quest_conditions"`
	QuestRewards               null.String `db:"quest_rewards" json:"quest_rewards"`
	QuestTemplate              null.String `db:"quest_template" json:"quest_template"`
	QuestTitle                 null.String `db:"quest_title" json:"quest_title"`
	QuestExpiry                null.Int    `db:"quest_expiry" json:"quest_expiry"`
	CellId                     null.Int    `db:"cell_id" json:"cell_id"`
	Deleted                    bool        `db:"deleted" json:"deleted"`
	LureId                     int16       `db:"lure_id" json:"lure_id"`
	FirstSeenTimestamp         int16       `db:"first_seen_timestamp" json:"first_seen_timestamp"`
	SponsorId                  null.Int    `db:"sponsor_id" json:"sponsor_id"`
	PartnerId                  null.String `db:"partner_id" json:"partner_id"`
	ArScanEligible             null.Int    `db:"ar_scan_eligible" json:"ar_scan_eligible"` // is an 8
	PowerUpLevel               null.Int    `db:"power_up_level" json:"power_up_level"`
	PowerUpPoints              null.Int    `db:"power_up_points" json:"power_up_points"`
	PowerUpEndTimestamp        null.Int    `db:"power_up_end_timestamp" json:"power_up_end_timestamp"`
	AlternativeQuestType       null.Int    `db:"alternative_quest_type" json:"alternative_quest_type"`
	AlternativeQuestTimestamp  null.Int    `db:"alternative_quest_timestamp" json:"alternative_quest_timestamp"`
	AlternativeQuestTarget     null.Int    `db:"alternative_quest_target" json:"alternative_quest_target"`
	AlternativeQuestConditions null.String `db:"alternative_quest_conditions" json:"alternative_quest_conditions"`
	AlternativeQuestRewards    null.String `db:"alternative_quest_rewards" json:"alternative_quest_rewards"`
	AlternativeQuestTemplate   null.String `db:"alternative_quest_template" json:"alternative_quest_template"`
	AlternativeQuestTitle      null.String `db:"alternative_quest_title" json:"alternative_quest_title"`
	AlternativeQuestExpiry     null.Int    `db:"alternative_quest_expiry" json:"alternative_quest_expiry"`
	Description                null.String `db:"description" json:"description"`
	ShowcaseFocus              null.String `db:"showcase_focus" json:"showcase_focus"`
	ShowcasePokemon            null.Int    `db:"showcase_pokemon_id" json:"showcase_pokemon_id"`
	ShowcasePokemonForm        null.Int    `db:"showcase_pokemon_form_id" json:"showcase_pokemon_form_id"`
	ShowcasePokemonType        null.Int    `db:"showcase_pokemon_type_id" json:"showcase_pokemon_type_id"`
	ShowcaseRankingStandard    null.Int    `db:"showcase_ranking_standard" json:"showcase_ranking_standard"`
	ShowcaseExpiry             null.Int    `db:"showcase_expiry" json:"showcase_expiry"`
	ShowcaseRankings           null.String `db:"showcase_rankings" json:"showcase_rankings"`
	//`id` varchar(35) NOT NULL,
	//`lat` double(18,14) NOT NULL,
	//`lon` double(18,14) NOT NULL,
	//`name` varchar(128) DEFAULT NULL,
	//`url` varchar(200) DEFAULT NULL,
	//`lure_expire_timestamp` int unsigned DEFAULT NULL,
	//`last_modified_timestamp` int unsigned DEFAULT NULL,
	//`updated` int unsigned NOT NULL,
	//`enabled` tinyint unsigned DEFAULT NULL,
	//`quest_type` int unsigned DEFAULT NULL,
	//`quest_timestamp` int unsigned DEFAULT NULL,
	//`quest_target` smallint unsigned DEFAULT NULL,
	//`quest_conditions` text,
	//`quest_rewards` text,
	//`quest_template` varchar(100) DEFAULT NULL,
	//`quest_title` varchar(100) DEFAULT NULL,
	//`quest_reward_type` smallint unsigned GENERATED ALWAYS AS (json_extract(json_extract(`quest_rewards`,_utf8mb4'$[*].type'),_utf8mb4'$[0]')) VIRTUAL,
	//`quest_item_id` smallint unsigned GENERATED ALWAYS AS (json_extract(json_extract(`quest_rewards`,_utf8mb4'$[*].info.item_id'),_utf8mb4'$[0]')) VIRTUAL,
	//`quest_reward_amount` smallint unsigned GENERATED ALWAYS AS (json_extract(json_extract(`quest_rewards`,_utf8mb4'$[*].info.amount'),_utf8mb4'$[0]')) VIRTUAL,
	//`cell_id` bigint unsigned DEFAULT NULL,
	//`deleted` tinyint unsigned NOT NULL DEFAULT '0',
	//`lure_id` smallint DEFAULT '0',
	//`first_seen_timestamp` int unsigned NOT NULL,
	//`sponsor_id` smallint unsigned DEFAULT NULL,
	//`partner_id` varchar(35) DEFAULT NULL,
	//`quest_pokemon_id` smallint unsigned GENERATED ALWAYS AS (json_extract(json_extract(`quest_rewards`,_utf8mb4'$[*].info.pokemon_id'),_utf8mb4'$[0]')) VIRTUAL,
	//`ar_scan_eligible` tinyint unsigned DEFAULT NULL,
	//`power_up_level` smallint unsigned DEFAULT NULL,
	//`power_up_points` int unsigned DEFAULT NULL,
	//`power_up_end_timestamp` int unsigned DEFAULT NULL,
	//`alternative_quest_type` int unsigned DEFAULT NULL,
	//`alternative_quest_timestamp` int unsigned DEFAULT NULL,
	//`alternative_quest_target` smallint unsigned DEFAULT NULL,
	//`alternative_quest_conditions` text,
	//`alternative_quest_rewards` text,
	//`alternative_quest_template` varchar(100) DEFAULT NULL,
	//`alternative_quest_title` varchar(100) DEFAULT NULL,

}

func GetPokestopRecord(ctx context.Context, db db.DbDetails, fortId string) (*Pokestop, error) {
	stop := pokestopCache.Get(fortId)
	if stop != nil {
		pokestop := stop.Value()
		//log.Debugf("GetPokestopRecord %s (from cache)", fortId)
		return &pokestop, nil
	}
	pokestop := Pokestop{}
	err := db.GeneralDb.GetContext(ctx, &pokestop,
		`SELECT pokestop.id, lat, lon, name, url, enabled, lure_expire_timestamp, last_modified_timestamp,
			pokestop.updated, quest_type, quest_timestamp, quest_target, quest_conditions,
			quest_rewards, quest_template, quest_title,
			alternative_quest_type, alternative_quest_timestamp, alternative_quest_target,
			alternative_quest_conditions, alternative_quest_rewards,
			alternative_quest_template, alternative_quest_title, cell_id, deleted, lure_id, sponsor_id, partner_id,
			ar_scan_eligible, power_up_points, power_up_level, power_up_end_timestamp,
			quest_expiry, alternative_quest_expiry, description, showcase_pokemon_id, showcase_pokemon_form_id,
			showcase_pokemon_type_id, showcase_ranking_standard, showcase_expiry, showcase_rankings
			FROM pokestop
			WHERE pokestop.id = ? `, fortId)
	//log.Debugf("GetPokestopRecord %s (from db)", fortId)

	statsCollector.IncDbQuery("select pokestop", err)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	pokestopCache.Set(fortId, pokestop, ttlcache.DefaultTTL)
	if config.Config.TestFortInMemory {
		fortRtreeUpdatePokestopOnGet(&pokestop)
	}
	return &pokestop, nil
}

// hasChangesPokestop compares two Pokestop structs
// Float tolerance: Lat, Lon
func hasChangesPokestop(old *Pokestop, new *Pokestop) bool {
	return old.Id != new.Id ||
		old.Name != new.Name ||
		old.Url != new.Url ||
		old.LureExpireTimestamp != new.LureExpireTimestamp ||
		old.LastModifiedTimestamp != new.LastModifiedTimestamp ||
		old.Updated != new.Updated ||
		old.Enabled != new.Enabled ||
		old.QuestType != new.QuestType ||
		old.QuestTimestamp != new.QuestTimestamp ||
		old.QuestTarget != new.QuestTarget ||
		old.QuestConditions != new.QuestConditions ||
		old.QuestRewards != new.QuestRewards ||
		old.QuestTemplate != new.QuestTemplate ||
		old.QuestTitle != new.QuestTitle ||
		old.QuestExpiry != new.QuestExpiry ||
		old.CellId != new.CellId ||
		old.Deleted != new.Deleted ||
		old.LureId != new.LureId ||
		old.FirstSeenTimestamp != new.FirstSeenTimestamp ||
		old.SponsorId != new.SponsorId ||
		old.PartnerId != new.PartnerId ||
		old.ArScanEligible != new.ArScanEligible ||
		old.PowerUpLevel != new.PowerUpLevel ||
		old.PowerUpPoints != new.PowerUpPoints ||
		old.PowerUpEndTimestamp != new.PowerUpEndTimestamp ||
		old.AlternativeQuestType != new.AlternativeQuestType ||
		old.AlternativeQuestTimestamp != new.AlternativeQuestTimestamp ||
		old.AlternativeQuestTarget != new.AlternativeQuestTarget ||
		old.AlternativeQuestConditions != new.AlternativeQuestConditions ||
		old.AlternativeQuestRewards != new.AlternativeQuestRewards ||
		old.AlternativeQuestTemplate != new.AlternativeQuestTemplate ||
		old.AlternativeQuestTitle != new.AlternativeQuestTitle ||
		old.AlternativeQuestExpiry != new.AlternativeQuestExpiry ||
		old.Description != new.Description ||
		!floatAlmostEqual(old.Lat, new.Lat, floatTolerance) ||
		!floatAlmostEqual(old.Lon, new.Lon, floatTolerance) ||
		old.ShowcaseRankingStandard != new.ShowcaseRankingStandard ||
		old.ShowcaseFocus != new.ShowcaseFocus ||
		old.ShowcaseRankings != new.ShowcaseRankings ||
		old.ShowcaseExpiry != new.ShowcaseExpiry
}

var LureTime int64 = 1800

func (stop *Pokestop) updatePokestopFromFort(fortData *pogo.PokemonFortProto, cellId uint64, now int64) *Pokestop {
	stop.Id = fortData.FortId
	stop.Lat = fortData.Latitude
	stop.Lon = fortData.Longitude

	stop.PartnerId = null.NewString(fortData.PartnerId, fortData.PartnerId != "")
	stop.SponsorId = null.IntFrom(int64(fortData.Sponsor))
	stop.Enabled = null.BoolFrom(fortData.Enabled)
	stop.ArScanEligible = null.IntFrom(util.BoolToInt[int64](fortData.IsArScanEligible))
	stop.PowerUpPoints = null.IntFrom(int64(fortData.PowerUpProgressPoints))
	stop.PowerUpLevel, stop.PowerUpEndTimestamp = calculatePowerUpPoints(fortData)

	// lasModifiedMs is also modified when incident happens
	lastModifiedTimestamp := fortData.LastModifiedMs / 1000
	stop.LastModifiedTimestamp = null.IntFrom(lastModifiedTimestamp)

	if len(fortData.ActiveFortModifier) > 0 {
		lureId := int16(fortData.ActiveFortModifier[0])
		if lureId >= 501 && lureId <= 510 {
			lureEnd := lastModifiedTimestamp + LureTime
			oldLureEnd := stop.LureExpireTimestamp.ValueOrZero()
			if stop.LureId != lureId {
				stop.LureExpireTimestamp = null.IntFrom(lureEnd)
				stop.LureId = lureId
			} else {
				// wait some time after lure end before a restart in case of timing issue
				if now > oldLureEnd+30 {
					for now > lureEnd {
						lureEnd += LureTime
					}
					// lure needs to be restarted
					stop.LureExpireTimestamp = null.IntFrom(lureEnd)
				}
			}
		}
	}

	if fortData.ImageUrl != "" {
		stop.Url = null.StringFrom(fortData.ImageUrl)
	}
	stop.CellId = null.IntFrom(int64(cellId))

	if stop.Deleted {
		stop.Deleted = false
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

	for _, rewardData := range questData.QuestRewards {
		reward := make(map[string]any)
		infoData := make(map[string]any)
		reward["type"] = int(rewardData.Type)
		switch rewardData.Type {
		case pogo.QuestRewardProto_EXPERIENCE:
			infoData["amount"] = rewardData.GetExp()
		case pogo.QuestRewardProto_ITEM:
			info := rewardData.GetItem()
			infoData["amount"] = info.Amount
			infoData["item_id"] = int(info.Item)
		case pogo.QuestRewardProto_STARDUST:
			infoData["amount"] = rewardData.GetStardust()
		case pogo.QuestRewardProto_CANDY:
			info := rewardData.GetCandy()
			infoData["amount"] = info.Amount
			infoData["pokemon_id"] = int(info.PokemonId)
		case pogo.QuestRewardProto_XL_CANDY:
			info := rewardData.GetXlCandy()
			infoData["amount"] = info.Amount
			infoData["pokemon_id"] = int(info.PokemonId)
		case pogo.QuestRewardProto_POKEMON_ENCOUNTER:
			info := rewardData.GetPokemonEncounter()
			if info.IsHiddenDitto {
				infoData["pokemon_id"] = 132
				infoData["pokemon_id_display"] = int(info.GetPokemonId())
			} else {
				infoData["pokemon_id"] = int(info.GetPokemonId())
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
			} else {

			}
		case pogo.QuestRewardProto_POKECOIN:
			infoData["amount"] = rewardData.GetPokecoin()
		case pogo.QuestRewardProto_STICKER:
			info := rewardData.GetSticker()
			infoData["amount"] = info.Amount
			infoData["sticker_id"] = info.StickerId
		case pogo.QuestRewardProto_MEGA_RESOURCE:
			info := rewardData.GetMegaResource()
			infoData["amount"] = info.Amount
			infoData["pokemon_id"] = int(info.PokemonId)
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
		stop.AlternativeQuestType = null.IntFrom(questType)
		stop.AlternativeQuestTarget = null.IntFrom(questTarget)
		stop.AlternativeQuestTemplate = null.StringFrom(questTemplate)
		stop.AlternativeQuestTitle = null.StringFrom(questTitle)
		stop.AlternativeQuestConditions = null.StringFrom(string(questConditions))
		stop.AlternativeQuestRewards = null.StringFrom(string(questRewards))
		stop.AlternativeQuestTimestamp = null.IntFrom(questTimestamp)
		stop.AlternativeQuestExpiry = questExpiry
	} else {
		stop.QuestType = null.IntFrom(questType)
		stop.QuestTarget = null.IntFrom(questTarget)
		stop.QuestTemplate = null.StringFrom(questTemplate)
		stop.QuestTitle = null.StringFrom(questTitle)
		stop.QuestConditions = null.StringFrom(string(questConditions))
		stop.QuestRewards = null.StringFrom(string(questRewards))
		stop.QuestTimestamp = null.IntFrom(questTimestamp)
		stop.QuestExpiry = questExpiry
	}

	return questTitle
}

func (stop *Pokestop) updatePokestopFromFortDetailsProto(fortData *pogo.FortDetailsOutProto) *Pokestop {
	stop.Id = fortData.Id
	stop.Lat = fortData.Latitude
	stop.Lon = fortData.Longitude
	if len(fortData.ImageUrl) > 0 {
		stop.Url = null.StringFrom(fortData.ImageUrl[0])
	}
	stop.Name = null.StringFrom(fortData.Name)

	if fortData.Description == "" {
		stop.Description = null.NewString("", false)
	} else {
		stop.Description = null.StringFrom(fortData.Description)
	}

	if fortData.Modifier != nil && len(fortData.Modifier) > 0 {
		// DeployingPlayerCodename contains the name of the player if we want that
		lureId := int16(fortData.Modifier[0].ModifierType)
		lureExpiry := fortData.Modifier[0].ExpirationTimeMs / 1000

		stop.LureId = lureId
		stop.LureExpireTimestamp = null.IntFrom(lureExpiry)
	}

	return stop
}

func (stop *Pokestop) updatePokestopFromGetMapFortsOutProto(fortData *pogo.GetMapFortsOutProto_FortProto) *Pokestop {
	stop.Id = fortData.Id
	stop.Lat = fortData.Latitude
	stop.Lon = fortData.Longitude

	if len(fortData.Image) > 0 {
		stop.Url = null.StringFrom(fortData.Image[0].Url)
	}
	stop.Name = null.StringFrom(fortData.Name)
	if stop.Deleted {
		log.Debugf("Cleared Stop with id '%s' is found again in GMF, therefore kept deleted", stop.Id)
	}
	return stop
}

func (stop *Pokestop) updatePokestopFromGetContestDataOutProto(contest *pogo.ContestProto) {
	stop.ShowcaseRankingStandard = null.IntFrom(int64(contest.GetMetric().GetRankingStandard()))
	stop.ShowcaseExpiry = null.IntFrom(contest.GetSchedule().GetContestCycle().GetEndTimeMs() / 1000)

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
		stop.ShowcaseFocus = null.StringFrom(string(jsonBytes))
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
	stop.ShowcaseRankings = null.StringFrom(string(jsonString))
}

func createPokestopFortWebhooks(oldStop *Pokestop, stop *Pokestop) {
	fort := InitWebHookFortFromPokestop(stop)
	oldFort := InitWebHookFortFromPokestop(oldStop)
	if oldStop == nil {
		CreateFortWebHooks(oldFort, fort, NEW)
	} else {
		CreateFortWebHooks(oldFort, fort, EDIT)
	}
}

func createPokestopWebhooks(oldStop *Pokestop, stop *Pokestop) {

	areas := MatchStatsGeofence(stop.Lat, stop.Lon)

	if stop.AlternativeQuestType.Valid && (oldStop == nil || stop.AlternativeQuestType != oldStop.AlternativeQuestType) {
		questHook := map[string]any{
			"pokestop_id": stop.Id,
			"latitude":    stop.Lat,
			"longitude":   stop.Lon,
			"pokestop_name": func() string {
				if stop.Name.Valid {
					return stop.Name.String
				} else {
					return "Unknown"
				}
			}(),
			"type":             stop.AlternativeQuestType,
			"target":           stop.AlternativeQuestTarget,
			"template":         stop.AlternativeQuestTemplate,
			"title":            stop.AlternativeQuestTitle,
			"conditions":       json.RawMessage(stop.AlternativeQuestConditions.ValueOrZero()),
			"rewards":          json.RawMessage(stop.AlternativeQuestRewards.ValueOrZero()),
			"updated":          stop.Updated,
			"ar_scan_eligible": stop.ArScanEligible.ValueOrZero(),
			"pokestop_url":     stop.Url.ValueOrZero(),
			"with_ar":          false,
		}
		webhooksSender.AddMessage(webhooks.Quest, questHook, areas)
	}

	if stop.QuestType.Valid && (oldStop == nil || stop.QuestType != oldStop.QuestType) {
		questHook := map[string]any{
			"pokestop_id": stop.Id,
			"latitude":    stop.Lat,
			"longitude":   stop.Lon,
			"pokestop_name": func() string {
				if stop.Name.Valid {
					return stop.Name.String
				} else {
					return "Unknown"
				}
			}(),
			"type":             stop.QuestType,
			"target":           stop.QuestTarget,
			"template":         stop.QuestTemplate,
			"title":            stop.QuestTitle,
			"conditions":       json.RawMessage(stop.QuestConditions.ValueOrZero()),
			"rewards":          json.RawMessage(stop.QuestRewards.ValueOrZero()),
			"updated":          stop.Updated,
			"ar_scan_eligible": stop.ArScanEligible.ValueOrZero(),
			"pokestop_url":     stop.Url.ValueOrZero(),
			"with_ar":          true,
		}
		webhooksSender.AddMessage(webhooks.Quest, questHook, areas)
	}
	if (oldStop == nil && (stop.LureId != 0 || stop.PowerUpEndTimestamp.ValueOrZero() != 0)) || (oldStop != nil && ((stop.LureExpireTimestamp != oldStop.LureExpireTimestamp && stop.LureId != 0) || stop.PowerUpEndTimestamp != oldStop.PowerUpEndTimestamp)) {
		pokestopHook := map[string]any{
			"pokestop_id": stop.Id,
			"latitude":    stop.Lat,
			"longitude":   stop.Lon,
			"name": func() string {
				if stop.Name.Valid {
					return stop.Name.String
				} else {
					return "Unknown"
				}
			}(),
			"url":                       stop.Url.ValueOrZero(),
			"lure_expiration":           stop.LureExpireTimestamp.ValueOrZero(),
			"last_modified":             stop.LastModifiedTimestamp.ValueOrZero(),
			"enabled":                   stop.Enabled.ValueOrZero(),
			"lure_id":                   stop.LureId,
			"ar_scan_eligible":          stop.ArScanEligible.ValueOrZero(),
			"power_up_level":            stop.PowerUpLevel.ValueOrZero(),
			"power_up_points":           stop.PowerUpPoints.ValueOrZero(),
			"power_up_end_timestamp":    stop.PowerUpPoints.ValueOrZero(),
			"updated":                   stop.Updated,
			"showcase_focus":            stop.ShowcaseFocus,
			"showcase_pokemon_id":       stop.ShowcasePokemon,
			"showcase_pokemon_form_id":  stop.ShowcasePokemonForm,
			"showcase_pokemon_type_id":  stop.ShowcasePokemonType,
			"showcase_ranking_standard": stop.ShowcaseRankingStandard,
			"showcase_expiry":           stop.ShowcaseExpiry,
			"showcase_rankings": func() any {
				if !stop.ShowcaseRankings.Valid {
					return nil
				} else {
					return json.RawMessage(stop.ShowcaseRankings.ValueOrZero())
				}
			}(),
		}

		webhooksSender.AddMessage(webhooks.Pokestop, pokestopHook, areas)
	}
}

func savePokestopRecord(ctx context.Context, db db.DbDetails, pokestop *Pokestop) {
	oldPokestop, _ := GetPokestopRecord(ctx, db, pokestop.Id)
	now := time.Now().Unix()
	if oldPokestop != nil && !hasChangesPokestop(oldPokestop, pokestop) {
		if oldPokestop.Updated > now-900 {
			// if a pokestop is unchanged, but we did see it again after 15 minutes, then save again
			return
		}
	}
	pokestop.Updated = now

	//log.Traceln(cmp.Diff(oldPokestop, pokestop))

	if oldPokestop == nil {
		res, err := db.GeneralDb.NamedExecContext(ctx, `
			INSERT INTO pokestop (
				id, lat, lon, name, url, enabled, lure_expire_timestamp, last_modified_timestamp, quest_type,
				quest_timestamp, quest_target, quest_conditions, quest_rewards, quest_template, quest_title,
				alternative_quest_type, alternative_quest_timestamp, alternative_quest_target,
				alternative_quest_conditions, alternative_quest_rewards, alternative_quest_template,
				alternative_quest_title, cell_id, lure_id, sponsor_id, partner_id, ar_scan_eligible,
				power_up_points, power_up_level, power_up_end_timestamp, updated, first_seen_timestamp,
				quest_expiry, alternative_quest_expiry, description, showcase_focus, showcase_pokemon_id,
				showcase_pokemon_form_id, showcase_pokemon_type_id, showcase_ranking_standard, showcase_expiry, showcase_rankings
				)
				VALUES (
				:id, :lat, :lon, :name, :url, :enabled, :lure_expire_timestamp, :last_modified_timestamp, :quest_type,
				:quest_timestamp, :quest_target, :quest_conditions, :quest_rewards, :quest_template, :quest_title,
				:alternative_quest_type, :alternative_quest_timestamp, :alternative_quest_target,
				:alternative_quest_conditions, :alternative_quest_rewards, :alternative_quest_template,
				:alternative_quest_title, :cell_id, :lure_id, :sponsor_id, :partner_id, :ar_scan_eligible,
				:power_up_points, :power_up_level, :power_up_end_timestamp,
				UNIX_TIMESTAMP(), UNIX_TIMESTAMP(),
				:quest_expiry, :alternative_quest_expiry, :description, :showcase_focus, :showcase_pokemon_id,
				:showcase_pokemon_form_id, :showcase_pokemon_type_id, :showcase_ranking_standard, :showcase_expiry, :showcase_rankings)`,
			pokestop)

		statsCollector.IncDbQuery("insert pokestop", err)
		//log.Debugf("Insert pokestop %s %+v", pokestop.Id, pokestop)
		if err != nil {
			log.Errorf("insert pokestop %s: %s", pokestop.Id, err)
			return
		}
		_ = res
	} else {
		res, err := db.GeneralDb.NamedExecContext(ctx, `
			UPDATE pokestop SET
				lat = :lat,
				lon = :lon,
				name = :name,
				url = :url,
				enabled = :enabled,
				lure_expire_timestamp = :lure_expire_timestamp,
				last_modified_timestamp = :last_modified_timestamp,
				updated = :updated,
				quest_type = :quest_type, 
				quest_timestamp = :quest_timestamp, 
				quest_target = :quest_target, 
				quest_conditions = :quest_conditions, 
				quest_rewards = :quest_rewards, 
				quest_template = :quest_template, 
				quest_title = :quest_title,
				alternative_quest_type = :alternative_quest_type, 
				alternative_quest_timestamp = :alternative_quest_timestamp,
				alternative_quest_target = :alternative_quest_target, 
				alternative_quest_conditions = :alternative_quest_conditions, 
				alternative_quest_rewards = :alternative_quest_rewards,
				alternative_quest_template = :alternative_quest_template,
				alternative_quest_title = :alternative_quest_title,
				cell_id = :cell_id,
				lure_id = :lure_id,
				deleted = :deleted,
				sponsor_id = :sponsor_id,
				partner_id = :partner_id,
				ar_scan_eligible = :ar_scan_eligible,
				power_up_points = :power_up_points,
				power_up_level = :power_up_level,
				power_up_end_timestamp = :power_up_end_timestamp,
				quest_expiry = :quest_expiry,
				alternative_quest_expiry = :alternative_quest_expiry,
				description = :description,
				showcase_focus = :showcase_focus,
				showcase_pokemon_id = :showcase_pokemon_id,
				showcase_pokemon_form_id = :showcase_pokemon_form_id,
				showcase_pokemon_type_id = :showcase_pokemon_type_id,
				showcase_ranking_standard = :showcase_ranking_standard,
				showcase_expiry = :showcase_expiry,
				showcase_rankings = :showcase_rankings
			WHERE id = :id`,
			pokestop,
		)
		statsCollector.IncDbQuery("update pokestop", err)
		//log.Debugf("Update pokestop %s %+v", pokestop.Id, pokestop)
		if err != nil {
			log.Errorf("update pokestop %s: %s", pokestop.Id, err)
			return
		}
		_ = res
	}
	pokestopCache.Set(pokestop.Id, *pokestop, ttlcache.DefaultTTL)
	createPokestopWebhooks(oldPokestop, pokestop)
	createPokestopFortWebhooks(oldPokestop, pokestop)
}

func updatePokestopGetMapFortCache(pokestop *Pokestop) {
	storedGetMapFort := getMapFortsCache.Get(pokestop.Id)
	if storedGetMapFort != nil {
		getMapFort := storedGetMapFort.Value()
		getMapFortsCache.Delete(pokestop.Id)
		pokestop.updatePokestopFromGetMapFortsOutProto(getMapFort)
		log.Debugf("Updated Gym using stored getMapFort: %s", pokestop.Id)
	}
}

func UpdatePokestopRecordWithFortDetailsOutProto(ctx context.Context, db db.DbDetails, fort *pogo.FortDetailsOutProto) string {
	pokestopMutex, _ := pokestopStripedMutex.GetLock(fort.Id)
	pokestopMutex.Lock()
	defer pokestopMutex.Unlock()

	pokestop, err := GetPokestopRecord(ctx, db, fort.Id) // should check error
	if err != nil {
		log.Printf("Update pokestop %s", err)
		return fmt.Sprintf("Error %s", err)
	}

	if pokestop == nil {
		pokestop = &Pokestop{}
	}
	pokestop.updatePokestopFromFortDetailsProto(fort)

	updatePokestopGetMapFortCache(pokestop)
	savePokestopRecord(ctx, db, pokestop)
	return fmt.Sprintf("%s %s", fort.Id, fort.Name)
}

func UpdatePokestopWithQuest(ctx context.Context, db db.DbDetails, quest *pogo.FortSearchOutProto, haveAr bool) string {
	haveArStr := "NoAR"
	if haveAr {
		haveArStr = "AR"
	}

	if quest.ChallengeQuest == nil {
		statsCollector.IncDecodeQuest("error", "no_quest")
		return fmt.Sprintf("%s %s Blank quest", quest.FortId, haveArStr)
	}

	statsCollector.IncDecodeQuest("ok", haveArStr)
	pokestopMutex, _ := pokestopStripedMutex.GetLock(quest.FortId)
	pokestopMutex.Lock()
	defer pokestopMutex.Unlock()

	pokestop, err := GetPokestopRecord(ctx, db, quest.FortId)
	if err != nil {
		log.Printf("Update quest %s", err)
		return fmt.Sprintf("error %s", err)
	}

	if pokestop == nil {
		pokestop = &Pokestop{}
	}
	questTitle := pokestop.updatePokestopFromQuestProto(quest, haveAr)

	updatePokestopGetMapFortCache(pokestop)
	savePokestopRecord(ctx, db, pokestop)

	areas := MatchStatsGeofence(pokestop.Lat, pokestop.Lon)
	updateQuestStats(pokestop, haveAr, areas)

	return fmt.Sprintf("%s %s %s", quest.FortId, haveArStr, questTitle)
}

func ClearQuestsWithinGeofence(ctx context.Context, dbDetails db.DbDetails, geofence *geojson.Feature) {
	started := time.Now()
	rows, err := db.RemoveQuests(ctx, dbDetails, geofence)
	if err != nil {
		log.Errorf("ClearQuest: Error removing quests: %s", err)
		return
	}
	ClearPokestopCache()
	log.Infof("ClearQuest: Removed quests from %d pokestops in %s", rows, time.Since(started))
}

func GetQuestStatusWithGeofence(dbDetails db.DbDetails, geofence *geojson.Feature) db.QuestStatus {
	res, err := db.GetQuestStatus(dbDetails, geofence)
	if err != nil {
		log.Errorf("QuestStatus: Error retrieving quests: %s", err)
		return db.QuestStatus{}
	}
	return res
}

func UpdatePokestopRecordWithGetMapFortsOutProto(ctx context.Context, db db.DbDetails, mapFort *pogo.GetMapFortsOutProto_FortProto) (bool, string) {
	pokestopMutex, _ := pokestopStripedMutex.GetLock(mapFort.Id)
	pokestopMutex.Lock()
	defer pokestopMutex.Unlock()

	pokestop, err := GetPokestopRecord(ctx, db, mapFort.Id)
	if err != nil {
		log.Printf("Update pokestop %s", err)
		return false, fmt.Sprintf("Error %s", err)
	}

	if pokestop == nil {
		return false, ""
	}

	pokestop.updatePokestopFromGetMapFortsOutProto(mapFort)
	savePokestopRecord(ctx, db, pokestop)
	return true, fmt.Sprintf("%s %s", mapFort.Id, mapFort.Name)
}

func GetPokestopPositions(details db.DbDetails, geofence *geojson.Feature) ([]db.QuestLocation, error) {
	return db.GetPokestopPositions(details, geofence)
}

func UpdatePokestopWithContestData(ctx context.Context, db db.DbDetails, request *pogo.GetContestDataProto, contestData *pogo.GetContestDataOutProto) string {
	if contestData.ContestIncident == nil || len(contestData.ContestIncident.Contests) == 0 {
		return "No contests found"
	}

	var fortId string
	if request != nil {
		fortId = request.FortId
	} else {
		fortId = getFortIdFromContest(contestData.ContestIncident.Contests[0].ContestId)
	}

	if fortId == "" {
		return "No fortId found"
	}

	if len(contestData.ContestIncident.Contests) > 1 {
		log.Errorf("More than one contest found")
		return fmt.Sprintf("More than one contest found in %s", fortId)
	}

	contest := contestData.ContestIncident.Contests[0]

	pokestopMutex, _ := pokestopStripedMutex.GetLock(fortId)
	pokestopMutex.Lock()
	defer pokestopMutex.Unlock()

	pokestop, err := GetPokestopRecord(ctx, db, fortId)
	if err != nil {
		log.Printf("Get pokestop %s", err)
		return "Error getting pokestop"
	}

	if pokestop == nil {
		log.Infof("Contest data for pokestop %s not found", fortId)
		return fmt.Sprintf("Contest data for pokestop %s not found", fortId)
	}

	pokestop.updatePokestopFromGetContestDataOutProto(contest)
	savePokestopRecord(ctx, db, pokestop)

	return fmt.Sprintf("Contest %s", fortId)
}

func getFortIdFromContest(id string) string {
	return strings.Split(id, "-")[0]
}

func UpdatePokestopWithPokemonSizeContestEntry(ctx context.Context, db db.DbDetails, request *pogo.GetPokemonSizeLeaderboardEntryProto, contestData *pogo.GetPokemonSizeLeaderboardEntryOutProto) string {
	fortId := getFortIdFromContest(request.GetContestId())

	pokestopMutex, _ := pokestopStripedMutex.GetLock(fortId)
	pokestopMutex.Lock()
	defer pokestopMutex.Unlock()

	pokestop, err := GetPokestopRecord(ctx, db, fortId)
	if err != nil {
		log.Printf("Get pokestop %s", err)
		return "Error getting pokestop"
	}

	if pokestop == nil {
		log.Infof("Contest data for pokestop %s not found", fortId)
		return fmt.Sprintf("Contest data for pokestop %s not found", fortId)
	}

	pokestop.updatePokestopFromGetPokemonSizeContestEntryOutProto(contestData)
	savePokestopRecord(ctx, db, pokestop)

	return fmt.Sprintf("Contest Detail %s", fortId)
}
