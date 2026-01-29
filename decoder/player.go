package decoder

import (
	"database/sql"
	"reflect"
	"strconv"
	"time"

	"golbat/db"
	"golbat/pogo"

	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
	"gopkg.in/guregu/null.v4"
)

// Player struct. Name is the primary key.
// REMINDER! Dirty flag pattern - use setter methods to modify fields
type Player struct {
	// Name is the primary key
	Name               string      `db:"name"`
	FriendshipId       null.String `db:"friendship_id"`
	LastSeen           int64       `db:"last_seen"`
	FriendCode         null.String `db:"friend_code"`
	Team               null.Int    `db:"team"`
	Level              null.Int    `db:"level"`
	Xp                 null.Int    `db:"xp"`
	BattlesWon         null.Int    `db:"battles_won"`
	KmWalked           null.Float  `db:"km_walked"`
	CaughtPokemon      null.Int    `db:"caught_pokemon"`
	GblRank            null.Int    `db:"gbl_rank"`
	GblRating          null.Int    `db:"gbl_rating"`
	EventBadges        null.String `db:"event_badges"`
	StopsSpun          null.Int    `db:"stops_spun"`
	Evolved            null.Int    `db:"evolved"`
	Hatched            null.Int    `db:"hatched"`
	Quests             null.Int    `db:"quests"`
	Trades             null.Int    `db:"trades"`
	Photobombs         null.Int    `db:"photobombs"`
	Purified           null.Int    `db:"purified"`
	GruntsDefeated     null.Int    `db:"grunts_defeated"`
	GymBattlesWon      null.Int    `db:"gym_battles_won"`
	NormalRaidsWon     null.Int    `db:"normal_raids_won"`
	LegendaryRaidsWon  null.Int    `db:"legendary_raids_won"`
	TrainingsWon       null.Int    `db:"trainings_won"`
	BerriesFed         null.Int    `db:"berries_fed"`
	HoursDefended      null.Int    `db:"hours_defended"`
	BestFriends        null.Int    `db:"best_friends"`
	BestBuddies        null.Int    `db:"best_buddies"`
	GiovanniDefeated   null.Int    `db:"giovanni_defeated"`
	MegaEvos           null.Int    `db:"mega_evos"`
	CollectionsDone    null.Int    `db:"collections_done"`
	UniqueStopsSpun    null.Int    `db:"unique_stops_spun"`
	UniqueMegaEvos     null.Int    `db:"unique_mega_evos"`
	UniqueRaidBosses   null.Int    `db:"unique_raid_bosses"`
	UniqueUnown        null.Int    `db:"unique_unown"`
	SevenDayStreaks    null.Int    `db:"seven_day_streaks"`
	TradeKm            null.Int    `db:"trade_km"`
	RaidsWithFriends   null.Int    `db:"raids_with_friends"`
	CaughtAtLure       null.Int    `db:"caught_at_lure"`
	WayfarerAgreements null.Int    `db:"wayfarer_agreements"`
	TrainersReferred   null.Int    `db:"trainers_referred"`
	RaidAchievements   null.Int    `db:"raid_achievements"`
	XlKarps            null.Int    `db:"xl_karps"`
	XsRats             null.Int    `db:"xs_rats"`
	PikachuCaught      null.Int    `db:"pikachu_caught"`
	LeagueGreatWon     null.Int    `db:"league_great_won"`
	LeagueUltraWon     null.Int    `db:"league_ultra_won"`
	LeagueMasterWon    null.Int    `db:"league_master_won"`
	TinyPokemonCaught  null.Int    `db:"tiny_pokemon_caught"`
	JumboPokemonCaught null.Int    `db:"jumbo_pokemon_caught"`
	Vivillon           null.Int    `db:"vivillon"`
	MaxSizeFirstPlace  null.Int    `db:"showcase_max_size_first_place"`
	TotalRoutePlay     null.Int    `db:"total_route_play"`
	PartiesCompleted   null.Int    `db:"parties_completed"`
	EventCheckIns      null.Int    `db:"event_check_ins"`
	DexGen1            null.Int    `db:"dex_gen1"`
	DexGen2            null.Int    `db:"dex_gen2"`
	DexGen3            null.Int    `db:"dex_gen3"`
	DexGen4            null.Int    `db:"dex_gen4"`
	DexGen5            null.Int    `db:"dex_gen5"`
	DexGen6            null.Int    `db:"dex_gen6"`
	DexGen7            null.Int    `db:"dex_gen7"`
	DexGen8            null.Int    `db:"dex_gen8"`
	DexGen8A           null.Int    `db:"dex_gen8a"`
	DexGen9            null.Int    `db:"dex_gen9"`
	CaughtNormal       null.Int    `db:"caught_normal"`
	CaughtFighting     null.Int    `db:"caught_fighting"`
	CaughtFlying       null.Int    `db:"caught_flying"`
	CaughtPoison       null.Int    `db:"caught_poison"`
	CaughtGround       null.Int    `db:"caught_ground"`
	CaughtRock         null.Int    `db:"caught_rock"`
	CaughtBug          null.Int    `db:"caught_bug"`
	CaughtGhost        null.Int    `db:"caught_ghost"`
	CaughtSteel        null.Int    `db:"caught_steel"`
	CaughtFire         null.Int    `db:"caught_fire"`
	CaughtWater        null.Int    `db:"caught_water"`
	CaughtGrass        null.Int    `db:"caught_grass"`
	CaughtElectric     null.Int    `db:"caught_electric"`
	CaughtPsychic      null.Int    `db:"caught_psychic"`
	CaughtIce          null.Int    `db:"caught_ice"`
	CaughtDragon       null.Int    `db:"caught_dragon"`
	CaughtDark         null.Int    `db:"caught_dark"`
	CaughtFairy        null.Int    `db:"caught_fairy"`

	dirty     bool `db:"-" json:"-"` // Not persisted - tracks if object needs saving
	newRecord bool `db:"-" json:"-"` // Not persisted - tracks if this is a new record
}

// IsDirty returns true if any field has been modified
func (p *Player) IsDirty() bool {
	return p.dirty
}

// ClearDirty resets the dirty flag (call after saving to DB)
func (p *Player) ClearDirty() {
	p.dirty = false
}

// IsNewRecord returns true if this is a new record (not yet in DB)
func (p *Player) IsNewRecord() bool {
	return p.newRecord
}

// setFieldDirty marks the dirty flag. Used by reflection-based updates.
func (p *Player) setFieldDirty() {
	p.dirty = true
}

// --- Set methods with dirty tracking ---

func (p *Player) SetFriendshipId(v null.String) {
	if p.FriendshipId != v {
		p.FriendshipId = v
		p.dirty = true
	}
}

func (p *Player) SetFriendCode(v null.String) {
	if p.FriendCode != v {
		p.FriendCode = v
		p.dirty = true
	}
}

func (p *Player) SetTeam(v null.Int) {
	if p.Team != v {
		p.Team = v
		p.dirty = true
	}
}

func (p *Player) SetLevel(v null.Int) {
	if p.Level != v {
		p.Level = v
		p.dirty = true
	}
}

func (p *Player) SetXp(v null.Int) {
	if p.Xp != v {
		p.Xp = v
		p.dirty = true
	}
}

func (p *Player) SetBattlesWon(v null.Int) {
	if p.BattlesWon != v {
		p.BattlesWon = v
		p.dirty = true
	}
}

func (p *Player) SetKmWalked(v null.Float) {
	if !nullFloatAlmostEqual(p.KmWalked, v, 0.001) {
		p.KmWalked = v
		p.dirty = true
	}
}

func (p *Player) SetCaughtPokemon(v null.Int) {
	if p.CaughtPokemon != v {
		p.CaughtPokemon = v
		p.dirty = true
	}
}

func (p *Player) SetGblRank(v null.Int) {
	if p.GblRank != v {
		p.GblRank = v
		p.dirty = true
	}
}

func (p *Player) SetGblRating(v null.Int) {
	if p.GblRating != v {
		p.GblRating = v
		p.dirty = true
	}
}

func (p *Player) SetEventBadges(v null.String) {
	if p.EventBadges != v {
		p.EventBadges = v
		p.dirty = true
	}
}

func (p *Player) SetStopsSpun(v null.Int) {
	if p.StopsSpun != v {
		p.StopsSpun = v
		p.dirty = true
	}
}
func (p *Player) SetEvolved(v null.Int) {
	if p.Evolved != v {
		p.Evolved = v
		p.dirty = true
	}
}
func (p *Player) SetHatched(v null.Int) {
	if p.Hatched != v {
		p.Hatched = v
		p.dirty = true
	}
}
func (p *Player) SetQuests(v null.Int) {
	if p.Quests != v {
		p.Quests = v
		p.dirty = true
	}
}
func (p *Player) SetTrades(v null.Int) {
	if p.Trades != v {
		p.Trades = v
		p.dirty = true
	}
}
func (p *Player) SetPhotobombs(v null.Int) {
	if p.Photobombs != v {
		p.Photobombs = v
		p.dirty = true
	}
}
func (p *Player) SetPurified(v null.Int) {
	if p.Purified != v {
		p.Purified = v
		p.dirty = true
	}
}
func (p *Player) SetGruntsDefeated(v null.Int) {
	if p.GruntsDefeated != v {
		p.GruntsDefeated = v
		p.dirty = true
	}
}
func (p *Player) SetGymBattlesWon(v null.Int) {
	if p.GymBattlesWon != v {
		p.GymBattlesWon = v
		p.dirty = true
	}
}
func (p *Player) SetNormalRaidsWon(v null.Int) {
	if p.NormalRaidsWon != v {
		p.NormalRaidsWon = v
		p.dirty = true
	}
}
func (p *Player) SetLegendaryRaidsWon(v null.Int) {
	if p.LegendaryRaidsWon != v {
		p.LegendaryRaidsWon = v
		p.dirty = true
	}
}
func (p *Player) SetTrainingsWon(v null.Int) {
	if p.TrainingsWon != v {
		p.TrainingsWon = v
		p.dirty = true
	}
}
func (p *Player) SetBerriesFed(v null.Int) {
	if p.BerriesFed != v {
		p.BerriesFed = v
		p.dirty = true
	}
}
func (p *Player) SetHoursDefended(v null.Int) {
	if p.HoursDefended != v {
		p.HoursDefended = v
		p.dirty = true
	}
}
func (p *Player) SetBestFriends(v null.Int) {
	if p.BestFriends != v {
		p.BestFriends = v
		p.dirty = true
	}
}
func (p *Player) SetBestBuddies(v null.Int) {
	if p.BestBuddies != v {
		p.BestBuddies = v
		p.dirty = true
	}
}
func (p *Player) SetGiovanniDefeated(v null.Int) {
	if p.GiovanniDefeated != v {
		p.GiovanniDefeated = v
		p.dirty = true
	}
}
func (p *Player) SetMegaEvos(v null.Int) {
	if p.MegaEvos != v {
		p.MegaEvos = v
		p.dirty = true
	}
}
func (p *Player) SetCollectionsDone(v null.Int) {
	if p.CollectionsDone != v {
		p.CollectionsDone = v
		p.dirty = true
	}
}
func (p *Player) SetUniqueStopsSpun(v null.Int) {
	if p.UniqueStopsSpun != v {
		p.UniqueStopsSpun = v
		p.dirty = true
	}
}
func (p *Player) SetUniqueMegaEvos(v null.Int) {
	if p.UniqueMegaEvos != v {
		p.UniqueMegaEvos = v
		p.dirty = true
	}
}
func (p *Player) SetUniqueRaidBosses(v null.Int) {
	if p.UniqueRaidBosses != v {
		p.UniqueRaidBosses = v
		p.dirty = true
	}
}
func (p *Player) SetUniqueUnown(v null.Int) {
	if p.UniqueUnown != v {
		p.UniqueUnown = v
		p.dirty = true
	}
}
func (p *Player) SetSevenDayStreaks(v null.Int) {
	if p.SevenDayStreaks != v {
		p.SevenDayStreaks = v
		p.dirty = true
	}
}
func (p *Player) SetTradeKm(v null.Int) {
	if p.TradeKm != v {
		p.TradeKm = v
		p.dirty = true
	}
}
func (p *Player) SetRaidsWithFriends(v null.Int) {
	if p.RaidsWithFriends != v {
		p.RaidsWithFriends = v
		p.dirty = true
	}
}
func (p *Player) SetCaughtAtLure(v null.Int) {
	if p.CaughtAtLure != v {
		p.CaughtAtLure = v
		p.dirty = true
	}
}
func (p *Player) SetWayfarerAgreements(v null.Int) {
	if p.WayfarerAgreements != v {
		p.WayfarerAgreements = v
		p.dirty = true
	}
}
func (p *Player) SetTrainersReferred(v null.Int) {
	if p.TrainersReferred != v {
		p.TrainersReferred = v
		p.dirty = true
	}
}
func (p *Player) SetRaidAchievements(v null.Int) {
	if p.RaidAchievements != v {
		p.RaidAchievements = v
		p.dirty = true
	}
}
func (p *Player) SetXlKarps(v null.Int) {
	if p.XlKarps != v {
		p.XlKarps = v
		p.dirty = true
	}
}
func (p *Player) SetXsRats(v null.Int) {
	if p.XsRats != v {
		p.XsRats = v
		p.dirty = true
	}
}
func (p *Player) SetPikachuCaught(v null.Int) {
	if p.PikachuCaught != v {
		p.PikachuCaught = v
		p.dirty = true
	}
}
func (p *Player) SetLeagueGreatWon(v null.Int) {
	if p.LeagueGreatWon != v {
		p.LeagueGreatWon = v
		p.dirty = true
	}
}
func (p *Player) SetLeagueUltraWon(v null.Int) {
	if p.LeagueUltraWon != v {
		p.LeagueUltraWon = v
		p.dirty = true
	}
}
func (p *Player) SetLeagueMasterWon(v null.Int) {
	if p.LeagueMasterWon != v {
		p.LeagueMasterWon = v
		p.dirty = true
	}
}
func (p *Player) SetTinyPokemonCaught(v null.Int) {
	if p.TinyPokemonCaught != v {
		p.TinyPokemonCaught = v
		p.dirty = true
	}
}
func (p *Player) SetJumboPokemonCaught(v null.Int) {
	if p.JumboPokemonCaught != v {
		p.JumboPokemonCaught = v
		p.dirty = true
	}
}
func (p *Player) SetVivillon(v null.Int) {
	if p.Vivillon != v {
		p.Vivillon = v
		p.dirty = true
	}
}
func (p *Player) SetMaxSizeFirstPlace(v null.Int) {
	if p.MaxSizeFirstPlace != v {
		p.MaxSizeFirstPlace = v
		p.dirty = true
	}
}
func (p *Player) SetTotalRoutePlay(v null.Int) {
	if p.TotalRoutePlay != v {
		p.TotalRoutePlay = v
		p.dirty = true
	}
}
func (p *Player) SetPartiesCompleted(v null.Int) {
	if p.PartiesCompleted != v {
		p.PartiesCompleted = v
		p.dirty = true
	}
}
func (p *Player) SetEventCheckIns(v null.Int) {
	if p.EventCheckIns != v {
		p.EventCheckIns = v
		p.dirty = true
	}
}
func (p *Player) SetDexGen1(v null.Int) {
	if p.DexGen1 != v {
		p.DexGen1 = v
		p.dirty = true
	}
}
func (p *Player) SetDexGen2(v null.Int) {
	if p.DexGen2 != v {
		p.DexGen2 = v
		p.dirty = true
	}
}
func (p *Player) SetDexGen3(v null.Int) {
	if p.DexGen3 != v {
		p.DexGen3 = v
		p.dirty = true
	}
}
func (p *Player) SetDexGen4(v null.Int) {
	if p.DexGen4 != v {
		p.DexGen4 = v
		p.dirty = true
	}
}
func (p *Player) SetDexGen5(v null.Int) {
	if p.DexGen5 != v {
		p.DexGen5 = v
		p.dirty = true
	}
}
func (p *Player) SetDexGen6(v null.Int) {
	if p.DexGen6 != v {
		p.DexGen6 = v
		p.dirty = true
	}
}
func (p *Player) SetDexGen7(v null.Int) {
	if p.DexGen7 != v {
		p.DexGen7 = v
		p.dirty = true
	}
}
func (p *Player) SetDexGen8(v null.Int) {
	if p.DexGen8 != v {
		p.DexGen8 = v
		p.dirty = true
	}
}
func (p *Player) SetDexGen8A(v null.Int) {
	if p.DexGen8A != v {
		p.DexGen8A = v
		p.dirty = true
	}
}
func (p *Player) SetDexGen9(v null.Int) {
	if p.DexGen9 != v {
		p.DexGen9 = v
		p.dirty = true
	}
}
func (p *Player) SetCaughtNormal(v null.Int) {
	if p.CaughtNormal != v {
		p.CaughtNormal = v
		p.dirty = true
	}
}
func (p *Player) SetCaughtFighting(v null.Int) {
	if p.CaughtFighting != v {
		p.CaughtFighting = v
		p.dirty = true
	}
}
func (p *Player) SetCaughtFlying(v null.Int) {
	if p.CaughtFlying != v {
		p.CaughtFlying = v
		p.dirty = true
	}
}
func (p *Player) SetCaughtPoison(v null.Int) {
	if p.CaughtPoison != v {
		p.CaughtPoison = v
		p.dirty = true
	}
}
func (p *Player) SetCaughtGround(v null.Int) {
	if p.CaughtGround != v {
		p.CaughtGround = v
		p.dirty = true
	}
}
func (p *Player) SetCaughtRock(v null.Int) {
	if p.CaughtRock != v {
		p.CaughtRock = v
		p.dirty = true
	}
}
func (p *Player) SetCaughtBug(v null.Int) {
	if p.CaughtBug != v {
		p.CaughtBug = v
		p.dirty = true
	}
}
func (p *Player) SetCaughtGhost(v null.Int) {
	if p.CaughtGhost != v {
		p.CaughtGhost = v
		p.dirty = true
	}
}
func (p *Player) SetCaughtSteel(v null.Int) {
	if p.CaughtSteel != v {
		p.CaughtSteel = v
		p.dirty = true
	}
}
func (p *Player) SetCaughtFire(v null.Int) {
	if p.CaughtFire != v {
		p.CaughtFire = v
		p.dirty = true
	}
}
func (p *Player) SetCaughtWater(v null.Int) {
	if p.CaughtWater != v {
		p.CaughtWater = v
		p.dirty = true
	}
}
func (p *Player) SetCaughtGrass(v null.Int) {
	if p.CaughtGrass != v {
		p.CaughtGrass = v
		p.dirty = true
	}
}
func (p *Player) SetCaughtElectric(v null.Int) {
	if p.CaughtElectric != v {
		p.CaughtElectric = v
		p.dirty = true
	}
}
func (p *Player) SetCaughtPsychic(v null.Int) {
	if p.CaughtPsychic != v {
		p.CaughtPsychic = v
		p.dirty = true
	}
}
func (p *Player) SetCaughtIce(v null.Int) {
	if p.CaughtIce != v {
		p.CaughtIce = v
		p.dirty = true
	}
}
func (p *Player) SetCaughtDragon(v null.Int) {
	if p.CaughtDragon != v {
		p.CaughtDragon = v
		p.dirty = true
	}
}
func (p *Player) SetCaughtDark(v null.Int) {
	if p.CaughtDark != v {
		p.CaughtDark = v
		p.dirty = true
	}
}
func (p *Player) SetCaughtFairy(v null.Int) {
	if p.CaughtFairy != v {
		p.CaughtFairy = v
		p.dirty = true
	}
}

var badgeTypeToPlayerKey = map[pogo.HoloBadgeType]string{
	//pogo.HoloBadgeType_BADGE_TRAVEL_KM:       "KmWalked",
	pogo.HoloBadgeType_BADGE_POKEDEX_ENTRIES: "DexGen1",
	//pogo.HoloBadgeType_BADGE_CAPTURE_TOTAL:                "3",
	//pogo.HoloBadgeType_BADGE_DEFEATED_FORT:                "4",
	pogo.HoloBadgeType_BADGE_EVOLVED_TOTAL: "Evolved",
	pogo.HoloBadgeType_BADGE_HATCHED_TOTAL: "Hatched",
	//pogo.HoloBadgeType_BADGE_ENCOUNTERED_TOTAL:            "7",
	pogo.HoloBadgeType_BADGE_POKESTOPS_VISITED: "StopsSpun",
	pogo.HoloBadgeType_BADGE_UNIQUE_POKESTOPS:  "UniqueStopsSpun",
	//pogo.HoloBadgeType_BADGE_POKEBALL_THROWN:              "10",
	pogo.HoloBadgeType_BADGE_BIG_MAGIKARP: "XlKarps",
	//pogo.HoloBadgeType_BADGE_DEPLOYED_TOTAL:               "12",
	pogo.HoloBadgeType_BADGE_BATTLE_ATTACK_WON:   "GymBattlesWon",
	pogo.HoloBadgeType_BADGE_BATTLE_TRAINING_WON: "TrainingsWon",
	//pogo.HoloBadgeType_BADGE_BATTLE_DEFEND_WON:            "15",
	//pogo.HoloBadgeType_BADGE_PRESTIGE_RAISED:              "16",
	//pogo.HoloBadgeType_BADGE_PRESTIGE_DROPPED:             "17",
	pogo.HoloBadgeType_BADGE_TYPE_NORMAL:          "CaughtNormal",
	pogo.HoloBadgeType_BADGE_TYPE_FIGHTING:        "CaughtFighting",
	pogo.HoloBadgeType_BADGE_TYPE_FLYING:          "CaughtFlying",
	pogo.HoloBadgeType_BADGE_TYPE_POISON:          "CaughtPoison",
	pogo.HoloBadgeType_BADGE_TYPE_GROUND:          "CaughtGround",
	pogo.HoloBadgeType_BADGE_TYPE_ROCK:            "CaughtRock",
	pogo.HoloBadgeType_BADGE_TYPE_BUG:             "CaughtBug",
	pogo.HoloBadgeType_BADGE_TYPE_GHOST:           "CaughtGhost",
	pogo.HoloBadgeType_BADGE_TYPE_STEEL:           "CaughtSteel",
	pogo.HoloBadgeType_BADGE_TYPE_FIRE:            "CaughtFire",
	pogo.HoloBadgeType_BADGE_TYPE_WATER:           "CaughtWater",
	pogo.HoloBadgeType_BADGE_TYPE_GRASS:           "CaughtGrass",
	pogo.HoloBadgeType_BADGE_TYPE_ELECTRIC:        "CaughtElectric",
	pogo.HoloBadgeType_BADGE_TYPE_PSYCHIC:         "CaughtPsychic",
	pogo.HoloBadgeType_BADGE_TYPE_ICE:             "CaughtIce",
	pogo.HoloBadgeType_BADGE_TYPE_DRAGON:          "CaughtDragon",
	pogo.HoloBadgeType_BADGE_TYPE_DARK:            "CaughtDark",
	pogo.HoloBadgeType_BADGE_TYPE_FAIRY:           "CaughtFairy",
	pogo.HoloBadgeType_BADGE_SMALL_RATTATA:        "XsRats",
	pogo.HoloBadgeType_BADGE_PIKACHU:              "PikachuCaught",
	pogo.HoloBadgeType_BADGE_UNOWN:                "UniqueUnown",
	pogo.HoloBadgeType_BADGE_POKEDEX_ENTRIES_GEN2: "DexGen2",
	pogo.HoloBadgeType_BADGE_RAID_BATTLE_WON:      "NormalRaidsWon",
	pogo.HoloBadgeType_BADGE_LEGENDARY_BATTLE_WON: "LegendaryRaidsWon",
	pogo.HoloBadgeType_BADGE_BERRIES_FED:          "BerriesFed",
	pogo.HoloBadgeType_BADGE_HOURS_DEFENDED:       "HoursDefended",
	//pogo.HoloBadgeType_BADGE_PLACE_HOLDER:                 "44",
	pogo.HoloBadgeType_BADGE_POKEDEX_ENTRIES_GEN3: "DexGen3",
	pogo.HoloBadgeType_BADGE_CHALLENGE_QUESTS:     "Quests",
	//pogo.HoloBadgeType_BADGE_MEW_ENCOUNTER:                "47",
	pogo.HoloBadgeType_BADGE_MAX_LEVEL_FRIENDS:            "BestFriends",
	pogo.HoloBadgeType_BADGE_TRADING:                      "Trades",
	pogo.HoloBadgeType_BADGE_TRADING_DISTANCE:             "TradeKm",
	pogo.HoloBadgeType_BADGE_POKEDEX_ENTRIES_GEN4:         "DexGen4",
	pogo.HoloBadgeType_BADGE_GREAT_LEAGUE:                 "LeagueGreatWon",
	pogo.HoloBadgeType_BADGE_ULTRA_LEAGUE:                 "LeagueUltraWon",
	pogo.HoloBadgeType_BADGE_MASTER_LEAGUE:                "LeagueMasterWon",
	pogo.HoloBadgeType_BADGE_PHOTOBOMB:                    "Photobombs",
	pogo.HoloBadgeType_BADGE_POKEDEX_ENTRIES_GEN5:         "DexGen5",
	pogo.HoloBadgeType_BADGE_POKEMON_PURIFIED:             "Purified",
	pogo.HoloBadgeType_BADGE_ROCKET_GRUNTS_DEFEATED:       "GruntsDefeated",
	pogo.HoloBadgeType_BADGE_ROCKET_GIOVANNI_DEFEATED:     "GiovanniDefeated",
	pogo.HoloBadgeType_BADGE_BUDDY_BEST:                   "BestBuddies",
	pogo.HoloBadgeType_BADGE_POKEDEX_ENTRIES_GEN6:         "DexGen6",
	pogo.HoloBadgeType_BADGE_POKEDEX_ENTRIES_GEN7:         "DexGen7",
	pogo.HoloBadgeType_BADGE_POKEDEX_ENTRIES_GEN8:         "DexGen8",
	pogo.HoloBadgeType_BADGE_7_DAY_STREAKS:                "SevenDayStreaks",
	pogo.HoloBadgeType_BADGE_UNIQUE_RAID_BOSSES_DEFEATED:  "UniqueRaidBosses",
	pogo.HoloBadgeType_BADGE_RAIDS_WITH_FRIENDS:           "RaidsWithFriends",
	pogo.HoloBadgeType_BADGE_POKEMON_CAUGHT_AT_YOUR_LURES: "CaughtAtLure",
	pogo.HoloBadgeType_BADGE_WAYFARER:                     "WayfarerAgreements",
	pogo.HoloBadgeType_BADGE_TOTAL_MEGA_EVOS:              "MegaEvos",
	pogo.HoloBadgeType_BADGE_UNIQUE_MEGA_EVOS:             "UniqueMegaEvos",
	pogo.HoloBadgeType_BADGE_TRAINERS_REFERRED:            "TrainersReferred",
	//pogo.HoloBadgeType_BADGE_POKESTOPS_SCANNED:            "74",
	pogo.HoloBadgeType_BADGE_RAID_BATTLE_STAT:           "RaidAchievements",
	pogo.HoloBadgeType_BADGE_TOTAL_ROUTE_PLAY:           "TotalRoutePlay",
	pogo.HoloBadgeType_BADGE_POKEDEX_ENTRIES_GEN8A:      "DexGen8A",
	pogo.HoloBadgeType_BADGE_CAPTURE_SMALL_POKEMON:      "TinyPokemonCaught",
	pogo.HoloBadgeType_BADGE_CAPTURE_LARGE_POKEMON:      "JumboPokemonCaught",
	pogo.HoloBadgeType_BADGE_POKEDEX_ENTRIES_GEN9:       "DexGen9",
	pogo.HoloBadgeType_BADGE_PARTY_CHALLENGES_COMPLETED: "PartiesCompleted",
	pogo.HoloBadgeType_BADGE_CHECK_INS:                  "EventCheckIns",
	pogo.HoloBadgeType_BADGE_MINI_COLLECTION:            "CollectionsDone",
	pogo.HoloBadgeType_BADGE_BUTTERFLY_COLLECTOR:        "Vivillon",
	pogo.HoloBadgeType_BADGE_MAX_SIZE_FIRST_PLACE_WIN:   "MaxSizeFirstPlace",
}

func getPlayerRecord(db db.DbDetails, name string, friendshipId string, friendCode string) (*Player, error) {
	inMemoryPlayer := playerCache.Get(name)
	if inMemoryPlayer != nil {
		player := inMemoryPlayer.Value()
		return player, nil
	}

	player := Player{}
	err := db.GeneralDb.Get(&player,
		`
		SELECT *
		FROM player
		WHERE player.name = ?
		`,
		name,
	)
	statsCollector.IncDbQuery("select player_name", err)
	if err == sql.ErrNoRows {
		if friendshipId != "" {
			err = db.GeneralDb.Get(&player,
				`
				SELECT *
				FROM player
				WHERE player.friendship_id = ?
				`,
				friendshipId,
			)
			statsCollector.IncDbQuery("select player_friendship_id", err)
		} else if friendCode != "" {
			err = db.GeneralDb.Get(&player,
				`
				SELECT *
				FROM player
				WHERE player.friend_code = ?
				`,
				friendCode,
			)
			statsCollector.IncDbQuery("select player_friend_code", err)
		}

		if err == sql.ErrNoRows {
			return nil, nil
		} else if err != nil {
			return nil, err
		}

		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	playerCache.Set(name, &player, ttlcache.DefaultTTL)
	return &player, nil
}

func savePlayerRecord(db db.DbDetails, player *Player) {
	// Skip save if not dirty and not new
	if !player.IsDirty() && !player.IsNewRecord() {
		return
	}

	player.LastSeen = time.Now().Unix()

	if player.IsNewRecord() {
		_, err := db.GeneralDb.NamedExec(
			`
			INSERT INTO player (name, friendship_id, friend_code, last_seen, team, level, xp, battles_won, km_walked, caught_pokemon, gbl_rank, gbl_rating,
								event_badges, stops_spun, evolved, hatched, quests, trades, photobombs, purified, grunts_defeated,
								gym_battles_won, normal_raids_won, legendary_raids_won, trainings_won, berries_fed, hours_defended,
								best_friends, best_buddies, giovanni_defeated, mega_evos, collections_done, unique_stops_spun,
								unique_mega_evos, unique_raid_bosses, unique_unown, seven_day_streaks, trade_km, raids_with_friends,
								caught_at_lure, wayfarer_agreements, trainers_referred, raid_achievements, xl_karps, xs_rats,
								pikachu_caught, league_great_won, league_ultra_won, league_master_won, tiny_pokemon_caught,
								jumbo_pokemon_caught, vivillon, showcase_max_size_first_place, total_route_play, parties_completed, event_check_ins, dex_gen1, dex_gen2, dex_gen3, dex_gen4, dex_gen5, dex_gen6,
								dex_gen7, dex_gen8, dex_gen8a, dex_gen9, caught_normal, caught_fighting, caught_flying, caught_poison,
								caught_ground, caught_rock, caught_bug, caught_ghost, caught_steel, caught_fire, caught_water,
								caught_grass, caught_electric, caught_psychic, caught_ice, caught_dragon, caught_dark, caught_fairy)
			VALUES (:name, :friendship_id, :friend_code, :last_seen, :team, :level, :xp, :battles_won, :km_walked, :caught_pokemon, :gbl_rank, :gbl_rating,
					:event_badges, :stops_spun, :evolved, :hatched, :quests, :trades, :photobombs, :purified, :grunts_defeated,
					:gym_battles_won, :normal_raids_won, :legendary_raids_won, :trainings_won, :berries_fed, :hours_defended,
					:best_friends, :best_buddies, :giovanni_defeated, :mega_evos, :collections_done, :unique_stops_spun,
					:unique_mega_evos, :unique_raid_bosses, :unique_unown, :seven_day_streaks, :trade_km, :raids_with_friends,
					:caught_at_lure, :wayfarer_agreements, :trainers_referred, :raid_achievements, :xl_karps, :xs_rats,
					:pikachu_caught, :league_great_won, :league_ultra_won, :league_master_won, :tiny_pokemon_caught,
					:jumbo_pokemon_caught, :vivillon, :showcase_max_size_first_place, :total_route_play, :parties_completed, :event_check_ins, :dex_gen1, :dex_gen2, :dex_gen3, :dex_gen4, :dex_gen5, :dex_gen6, :dex_gen7,
					:dex_gen8, :dex_gen8a, :dex_gen9, :caught_normal, :caught_fighting, :caught_flying, :caught_poison, :caught_ground,
					:caught_rock, :caught_bug, :caught_ghost, :caught_steel, :caught_fire, :caught_water, :caught_grass,
					:caught_electric, :caught_psychic, :caught_ice, :caught_dragon, :caught_dark, :caught_fairy)
			`,
			player,
		)

		statsCollector.IncDbQuery("insert player", err)
		if err != nil {
			log.Errorf("insert player error: %s", err)
			return
		}
	} else {
		_, err := db.GeneralDb.NamedExec(
			`UPDATE player SET
				friendship_id = :friendship_id,
				last_seen = :last_seen,
				team = :team,
				level = :level,
				xp = :xp,
				battles_won = :battles_won,
				km_walked = :km_walked,
				caught_pokemon = :caught_pokemon,
				gbl_rank = :gbl_rank,
				gbl_rating = :gbl_rating,
				event_badges = :event_badges,
				stops_spun = :stops_spun,
				evolved = :evolved,
				hatched = :hatched,
				quests = :quests,
				trades = :trades,
				photobombs = :photobombs,
				purified = :purified,
				grunts_defeated = :grunts_defeated,
				gym_battles_won = :gym_battles_won,
				normal_raids_won = :normal_raids_won,
				legendary_raids_won = :legendary_raids_won,
				trainings_won = :trainings_won,
				berries_fed = :berries_fed,
				hours_defended = :hours_defended,
				best_friends = :best_friends,
				best_buddies = :best_buddies,
				giovanni_defeated = :giovanni_defeated,
				mega_evos = :mega_evos,
				collections_done = :collections_done,
				unique_stops_spun = :unique_stops_spun,
				unique_mega_evos = :unique_mega_evos,
				unique_raid_bosses = :unique_raid_bosses,
				unique_unown = :unique_unown,
				seven_day_streaks = :seven_day_streaks,
				trade_km = :trade_km,
				raids_with_friends = :raids_with_friends,
				caught_at_lure = :caught_at_lure,
				wayfarer_agreements = :wayfarer_agreements,
				trainers_referred = :trainers_referred,
				raid_achievements = :raid_achievements,
				xl_karps = :xl_karps,
				xs_rats = :xs_rats,
				pikachu_caught = :pikachu_caught,
				league_great_won = :league_great_won,
				league_ultra_won = :league_ultra_won,
				league_master_won = :league_master_won,
				tiny_pokemon_caught = :tiny_pokemon_caught,
				jumbo_pokemon_caught = :jumbo_pokemon_caught,
				vivillon = :vivillon,
				showcase_max_size_first_place = :showcase_max_size_first_place,
				total_route_play = :total_route_play,
				parties_completed = :parties_completed,
				event_check_ins = :event_check_ins,
				dex_gen1 = :dex_gen1,
				dex_gen2 = :dex_gen2,
				dex_gen3 = :dex_gen3,
				dex_gen4 = :dex_gen4,
				dex_gen5 = :dex_gen5,
				dex_gen6 = :dex_gen6,
				dex_gen7 = :dex_gen7,
				dex_gen8 = :dex_gen8,
				dex_gen8a = :dex_gen8a,
				dex_gen9 = :dex_gen9,
				caught_normal = :caught_normal,
				caught_fighting = :caught_fighting,
				caught_flying = :caught_flying,
				caught_poison = :caught_poison,
				caught_ground = :caught_ground,
				caught_rock = :caught_rock,
				caught_bug = :caught_bug,
				caught_ghost = :caught_ghost,
				caught_steel = :caught_steel,
				caught_fire = :caught_fire,
				caught_water = :caught_water,
				caught_grass = :caught_grass,
				caught_electric = :caught_electric,
				caught_psychic = :caught_psychic,
				caught_ice = :caught_ice,
				caught_dragon = :caught_dragon,
				caught_dark = :caught_dark,
				caught_fairy = :caught_fairy
				WHERE name = :name`,
			player,
		)

		statsCollector.IncDbQuery("update player", err)
		if err != nil {
			log.Errorf("Update player error %s", err)
		}
	}

	player.ClearDirty()
	if player.IsNewRecord() {
		player.newRecord = false
		playerCache.Set(player.Name, player, ttlcache.DefaultTTL)
	}
}

func (player *Player) updateFromPublicProfile(publicProfile *pogo.PlayerPublicProfileProto) {
	player.Name = publicProfile.GetName() // Name is primary key, don't track as dirty
	player.SetTeam(null.IntFrom(int64(publicProfile.GetTeam())))
	player.SetLevel(null.IntFrom(int64(publicProfile.GetLevel())))
	player.SetXp(null.IntFrom(publicProfile.GetExperience()))
	player.SetBattlesWon(null.IntFrom(int64(publicProfile.GetBattlesWon())))
	player.SetKmWalked(null.FloatFrom(float64(publicProfile.GetKmWalked())))
	player.SetCaughtPokemon(null.IntFrom(int64(publicProfile.GetCaughtPokemon())))
	player.SetGblRank(null.IntFrom(int64(publicProfile.GetCombatRank())))
	player.SetGblRating(null.IntFrom(int64(publicProfile.GetCombatRating())))

	eventBadges := ""

	for _, badge := range publicProfile.GetBadges() {
		if badge.GetBadgeType() > pogo.HoloBadgeType_BADGE_EVENT_MIN {
			if badge.GetCurrentValue() > 0 {
				if len(eventBadges) > 0 {
					eventBadges = eventBadges + ","
				}
				eventBadges = eventBadges + strconv.FormatInt(int64(badge.GetBadgeType()), 10)
			}

			continue
		}

		playerKey, isAKnownBadge := badgeTypeToPlayerKey[badge.GetBadgeType()]

		if !isAKnownBadge {
			continue
		}

		newValue := null.IntFrom(int64(badge.GetCurrentValue()))

		field := reflect.ValueOf(player).Elem().FieldByName(playerKey)
		if field.IsValid() && field.CanSet() {
			oldValue := field.Interface().(null.Int)
			if oldValue != newValue {
				field.Set(reflect.ValueOf(newValue))
				player.setFieldDirty()
			}
		}
	}

	player.SetEventBadges(null.StringFrom(eventBadges))
}

func UpdatePlayerRecordWithPlayerSummary(db db.DbDetails, playerSummary *pogo.InternalPlayerSummaryProto, publicProfile *pogo.PlayerPublicProfileProto, friendCode string, friendshipId string) error {
	player, err := getPlayerRecord(db, playerSummary.GetCodename(), friendshipId, friendCode)
	if err != nil {
		return err
	}

	if player == nil {
		player = &Player{
			Name:      playerSummary.GetCodename(),
			newRecord: true,
		}
	}

	if player.FriendshipId.IsZero() && friendshipId != "" {
		player.SetFriendshipId(null.StringFrom(friendshipId))
	}
	if player.FriendCode.IsZero() && friendCode != "" {
		player.SetFriendCode(null.StringFrom(friendCode))
	}

	player.updateFromPublicProfile(publicProfile)
	savePlayerRecord(db, player)
	return nil
}
