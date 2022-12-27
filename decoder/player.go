package decoder

import (
	"database/sql"
	"encoding/json"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
	"golbat/db"
	"golbat/pogo"
	"gopkg.in/guregu/null.v4"
	"strconv"
	"time"
)

type Player struct {
	Id                 null.String `db:"id" json:"-"`
	Name               string      `db:"name" json:"-"`
	LastSeen           int64       `db:"last_seen" json:"-"`
	FriendCode         null.String `db:"friend_code" json:"-"`
	Team               null.Int    `db:"team" json:"-"`
	Level              null.Int    `db:"level" json:"-"`
	Xp                 null.Int    `db:"xp" json:"-"`
	BattlesWon         null.Int    `db:"battles_won" json:"-"`
	KmWalked           null.Float  `db:"km_walked" json:"-"`
	CaughtPokemon      null.Int    `db:"caught_pokemon" json:"-"`
	GblRank            null.Int    `db:"gbl_rank" json:"-"`
	GblRating          null.Int    `db:"gbl_rating" json:"-"`
	EventBadges        null.String `db:"event_badges" json:"eventBadges"`
	StopsSpun          null.Int    `db:"stops_spun" json:"stopsSpun"`
	Evolved            null.Int    `db:"evolved" json:"evolved"`
	Hatched            null.Int    `db:"hatched" json:"hatched"`
	Quests             null.Int    `db:"quests" json:"quests"`
	Trades             null.Int    `db:"trades" json:"trades"`
	Photobombs         null.Int    `db:"photobombs" json:"photobombs"`
	Purified           null.Int    `db:"purified" json:"purified"`
	GruntsDefeated     null.Int    `db:"grunts_defeated" json:"gruntsDefeated"`
	GymBattlesWon      null.Int    `db:"gym_battles_won" json:"gymBattlesWon"`
	NormalRaidsWon     null.Int    `db:"normal_raids_won" json:"normalRaidsWon"`
	LegendaryRaidsWon  null.Int    `db:"legendary_raids_won" json:"legendaryRaidsWon"`
	TrainingsWon       null.Int    `db:"trainings_won" json:"trainingsWon"`
	BerriesFed         null.Int    `db:"berries_fed" json:"berriesFed"`
	HoursDefended      null.Int    `db:"hours_defended" json:"hoursDefended"`
	BestFriends        null.Int    `db:"best_friends" json:"bestFriends"`
	BestBuddies        null.Int    `db:"best_buddies" json:"bestBuddies"`
	GiovanniDefeated   null.Int    `db:"giovanni_defeated" json:"giovanniDefeated"`
	MegaEvos           null.Int    `db:"mega_evos" json:"megaEvos"`
	CollectionsDone    null.Int    `db:"collections_done" json:"collectionsDone"`
	UniqueStopsSpun    null.Int    `db:"unique_stops_spun" json:"uniqueStopsSpun"`
	UniqueMegaEvos     null.Int    `db:"unique_mega_evos" json:"uniqueMegaEvos"`
	UniqueRaidBosses   null.Int    `db:"unique_raid_bosses" json:"uniqueRaidBosses"`
	UniqueUnown        null.Int    `db:"unique_unown" json:"uniqueUnown"`
	SevenDayStreaks    null.Int    `db:"seven_day_streaks" json:"sevenDayStreaks"`
	TradeKm            null.Int    `db:"trade_km" json:"tradeKm"`
	RaidsWithFriends   null.Int    `db:"raids_with_friends" json:"raidsWithFriends"`
	CaughtAtLure       null.Int    `db:"caught_at_lure" json:"caughtAtLure"`
	WayfarerAgreements null.Int    `db:"wayfarer_agreements" json:"wayfarerAgreements"`
	TrainersReferred   null.Int    `db:"trainers_referred" json:"trainersReferred"`
	RaidAchievements   null.Int    `db:"raid_achievements" json:"raidAchievements"`
	XlKarps            null.Int    `db:"xl_karps" json:"xlKarps"`
	XsRats             null.Int    `db:"xs_rats" json:"xsRats"`
	PikachuCaught      null.Int    `db:"pikachu_caught" json:"pikachuCaught"`
	LeagueGreatWon     null.Int    `db:"league_great_won" json:"leagueGreatWon"`
	LeagueUltraWon     null.Int    `db:"league_ultra_won" json:"leagueUltraWon"`
	LeagueMasterWon    null.Int    `db:"league_master_won" json:"leagueMasterWon"`
	TinyPokemonCaught  null.Int    `db:"tiny_pokemon_caught" json:"tinyPokemonCaught"`
	JumboPokemonCaught null.Int    `db:"jumbo_pokemon_caught" json:"jumboPokemonCaught"`
	Vivillon           null.Int    `db:"vivillon" json:"vivillon"`
	DexGen1            null.Int    `db:"dex_gen1" json:"dexGen1"`
	DexGen2            null.Int    `db:"dex_gen2" json:"dexGen2"`
	DexGen3            null.Int    `db:"dex_gen3" json:"dexGen3"`
	DexGen4            null.Int    `db:"dex_gen4" json:"dexGen4"`
	DexGen5            null.Int    `db:"dex_gen5" json:"dexGen5"`
	DexGen6            null.Int    `db:"dex_gen6" json:"dexGen6"`
	DexGen7            null.Int    `db:"dex_gen7" json:"dexGen7"`
	DexGen8            null.Int    `db:"dex_gen8" json:"dexGen8"`
	DexGen8A           null.Int    `db:"dex_gen8a" json:"dexGen8A"`
	CaughtNormal       null.Int    `db:"caught_normal" json:"caughtNormal"`
	CaughtFighting     null.Int    `db:"caught_fighting" json:"caughtFighting"`
	CaughtFlying       null.Int    `db:"caught_flying" json:"caughtFlying"`
	CaughtPoison       null.Int    `db:"caught_poison" json:"caughtPoison"`
	CaughtGround       null.Int    `db:"caught_ground" json:"caughtGround"`
	CaughtRock         null.Int    `db:"caught_rock" json:"caughtRock"`
	CaughtBug          null.Int    `db:"caught_bug" json:"caughtBug"`
	CaughtGhost        null.Int    `db:"caught_ghost" json:"caughtGhost"`
	CaughtSteel        null.Int    `db:"caught_steel" json:"caughtSteel"`
	CaughtFire         null.Int    `db:"caught_fire" json:"caughtFire"`
	CaughtWater        null.Int    `db:"caught_water" json:"caughtWater"`
	CaughtGrass        null.Int    `db:"caught_grass" json:"caughtGrass"`
	CaughtElectric     null.Int    `db:"caught_electric" json:"caughtElectric"`
	CaughtPsychic      null.Int    `db:"caught_psychic" json:"caughtPsychic"`
	CaughtIce          null.Int    `db:"caught_ice" json:"caughtIce"`
	CaughtDragon       null.Int    `db:"caught_dragon" json:"caughtDragon"`
	CaughtDark         null.Int    `db:"caught_dark" json:"caughtDark"`
	CaughtFairy        null.Int    `db:"caught_fairy" json:"caughtFairy"`
}

var badgeTypeToPlayerKey = map[pogo.HoloBadgeType]string{
	//pogo.HoloBadgeType_BADGE_TRAVEL_KM:       "kmWalked",
	pogo.HoloBadgeType_BADGE_POKEDEX_ENTRIES: "dexGen1",
	//pogo.HoloBadgeType_BADGE_CAPTURE_TOTAL:                "3",
	//pogo.HoloBadgeType_BADGE_DEFEATED_FORT:                "4",
	pogo.HoloBadgeType_BADGE_EVOLVED_TOTAL: "evolved",
	pogo.HoloBadgeType_BADGE_HATCHED_TOTAL: "hatched",
	//pogo.HoloBadgeType_BADGE_ENCOUNTERED_TOTAL:            "7",
	pogo.HoloBadgeType_BADGE_POKESTOPS_VISITED: "stopsSpun",
	pogo.HoloBadgeType_BADGE_UNIQUE_POKESTOPS:  "uniqueStopsSpun",
	//pogo.HoloBadgeType_BADGE_POKEBALL_THROWN:              "10",
	pogo.HoloBadgeType_BADGE_BIG_MAGIKARP: "xlKarps",
	//pogo.HoloBadgeType_BADGE_DEPLOYED_TOTAL:               "12",
	pogo.HoloBadgeType_BADGE_BATTLE_ATTACK_WON:   "gymBattlesWon",
	pogo.HoloBadgeType_BADGE_BATTLE_TRAINING_WON: "trainingsWon",
	//pogo.HoloBadgeType_BADGE_BATTLE_DEFEND_WON:            "15",
	//pogo.HoloBadgeType_BADGE_PRESTIGE_RAISED:              "16",
	//pogo.HoloBadgeType_BADGE_PRESTIGE_DROPPED:             "17",
	pogo.HoloBadgeType_BADGE_TYPE_NORMAL:          "caughtNormal",
	pogo.HoloBadgeType_BADGE_TYPE_FIGHTING:        "caughtFighting",
	pogo.HoloBadgeType_BADGE_TYPE_FLYING:          "caughtFlying",
	pogo.HoloBadgeType_BADGE_TYPE_POISON:          "caughtPoison",
	pogo.HoloBadgeType_BADGE_TYPE_GROUND:          "caughtGround",
	pogo.HoloBadgeType_BADGE_TYPE_ROCK:            "caughtRock",
	pogo.HoloBadgeType_BADGE_TYPE_BUG:             "caughtBug",
	pogo.HoloBadgeType_BADGE_TYPE_GHOST:           "caughtGhost",
	pogo.HoloBadgeType_BADGE_TYPE_STEEL:           "caughtSteel",
	pogo.HoloBadgeType_BADGE_TYPE_FIRE:            "caughtFire",
	pogo.HoloBadgeType_BADGE_TYPE_WATER:           "caughtWater",
	pogo.HoloBadgeType_BADGE_TYPE_GRASS:           "caughtGrass",
	pogo.HoloBadgeType_BADGE_TYPE_ELECTRIC:        "caughtElectric",
	pogo.HoloBadgeType_BADGE_TYPE_PSYCHIC:         "caughtPsychic",
	pogo.HoloBadgeType_BADGE_TYPE_ICE:             "caughtIce",
	pogo.HoloBadgeType_BADGE_TYPE_DRAGON:          "caughtDragon",
	pogo.HoloBadgeType_BADGE_TYPE_DARK:            "caughtDark",
	pogo.HoloBadgeType_BADGE_TYPE_FAIRY:           "caughtFairy",
	pogo.HoloBadgeType_BADGE_SMALL_RATTATA:        "xsRats",
	pogo.HoloBadgeType_BADGE_PIKACHU:              "pikachuCaught",
	pogo.HoloBadgeType_BADGE_UNOWN:                "uniqueUnown",
	pogo.HoloBadgeType_BADGE_POKEDEX_ENTRIES_GEN2: "dexGen2",
	pogo.HoloBadgeType_BADGE_RAID_BATTLE_WON:      "normalRaidsWon",
	pogo.HoloBadgeType_BADGE_LEGENDARY_BATTLE_WON: "legendaryRaidsWon",
	pogo.HoloBadgeType_BADGE_BERRIES_FED:          "berriesFed",
	pogo.HoloBadgeType_BADGE_HOURS_DEFENDED:       "hoursDefended",
	//pogo.HoloBadgeType_BADGE_PLACE_HOLDER:                 "44",
	pogo.HoloBadgeType_BADGE_POKEDEX_ENTRIES_GEN3: "dexGen3",
	pogo.HoloBadgeType_BADGE_CHALLENGE_QUESTS:     "quests",
	//pogo.HoloBadgeType_BADGE_MEW_ENCOUNTER:                "47",
	pogo.HoloBadgeType_BADGE_MAX_LEVEL_FRIENDS:            "bestFriends",
	pogo.HoloBadgeType_BADGE_TRADING:                      "trades",
	pogo.HoloBadgeType_BADGE_TRADING_DISTANCE:             "tradeKm",
	pogo.HoloBadgeType_BADGE_POKEDEX_ENTRIES_GEN4:         "dexGen4",
	pogo.HoloBadgeType_BADGE_GREAT_LEAGUE:                 "leagueGreatWon",
	pogo.HoloBadgeType_BADGE_ULTRA_LEAGUE:                 "leagueUltraWon",
	pogo.HoloBadgeType_BADGE_MASTER_LEAGUE:                "leagueMasterWon",
	pogo.HoloBadgeType_BADGE_PHOTOBOMB:                    "photobombs",
	pogo.HoloBadgeType_BADGE_POKEDEX_ENTRIES_GEN5:         "dexGen5",
	pogo.HoloBadgeType_BADGE_POKEMON_PURIFIED:             "purified",
	pogo.HoloBadgeType_BADGE_ROCKET_GRUNTS_DEFEATED:       "gruntsDefeated",
	pogo.HoloBadgeType_BADGE_ROCKET_GIOVANNI_DEFEATED:     "giovanniDefeated",
	pogo.HoloBadgeType_BADGE_BUDDY_BEST:                   "bestBuddies",
	pogo.HoloBadgeType_BADGE_POKEDEX_ENTRIES_GEN6:         "dexGen6",
	pogo.HoloBadgeType_BADGE_POKEDEX_ENTRIES_GEN7:         "dexGen7",
	pogo.HoloBadgeType_BADGE_POKEDEX_ENTRIES_GEN8:         "dexGen8",
	pogo.HoloBadgeType_BADGE_7_DAY_STREAKS:                "sevenDayStreaks",
	pogo.HoloBadgeType_BADGE_UNIQUE_RAID_BOSSES_DEFEATED:  "uniqueRaidBosses",
	pogo.HoloBadgeType_BADGE_RAIDS_WITH_FRIENDS:           "raidsWithFriends",
	pogo.HoloBadgeType_BADGE_POKEMON_CAUGHT_AT_YOUR_LURES: "caughtAtLure",
	pogo.HoloBadgeType_BADGE_WAYFARER:                     "wayfarerAgreements",
	pogo.HoloBadgeType_BADGE_TOTAL_MEGA_EVOS:              "megaEvos",
	pogo.HoloBadgeType_BADGE_UNIQUE_MEGA_EVOS:             "uniqueMegaEvos",
	//pogo.HoloBadgeType_DEPRECATED_0:                       "71",
	//pogo.HoloBadgeType_BADGE_ROUTE_ACCEPTED:               "72",
	pogo.HoloBadgeType_BADGE_TRAINERS_REFERRED: "trainersReferred",
	//pogo.HoloBadgeType_BADGE_POKESTOPS_SCANNED:            "74",
	pogo.HoloBadgeType_BADGE_RAID_BATTLE_STAT: "raidAchievements",
	//pogo.HoloBadgeType_BADGE_TOTAL_ROUTE_PLAY:             "77",
	//pogo.HoloBadgeType_BADGE_UNIQUE_ROUTE_PLAY:            "78",
	pogo.HoloBadgeType_BADGE_POKEDEX_ENTRIES_GEN8A: "dexGen8A",
	pogo.HoloBadgeType_BADGE_CAPTURE_SMALL_POKEMON: "tinyPokemonCaught",
	pogo.HoloBadgeType_BADGE_CAPTURE_LARGE_POKEMON: "jumboPokemonCaught",
	pogo.HoloBadgeType_BADGE_MINI_COLLECTION:       "collectionsDone",
	pogo.HoloBadgeType_BADGE_BUTTERFLY_COLLECTOR:   "vivillon",
}

func getPlayerRecord(db db.DbDetails, name string) (*Player, error) {
	inMemoryPlayer := playerCache.Get(name)
	if inMemoryPlayer != nil {
		player := inMemoryPlayer.Value()
		return &player, nil
	}

	player := Player{}
	err := db.GeneralDb.Get(&player,
		`
		SELECT id,
			   name,
			   last_seen,
			   friend_code,
			   team,
			   level,
			   xp,
			   battles_won,
			   km_walked,
			   caught_pokemon,
			   gbl_rank,
			   gbl_rating,
			   event_badges,
			   stops_spun,
			   evolved,
			   hatched,
			   quests,
			   trades,
			   photobombs,
			   purified,
			   grunts_defeated,
			   gym_battles_won,
			   normal_raids_won,
			   legendary_raids_won,
			   trainings_won,
			   berries_fed,
			   hours_defended,
			   best_friends,
			   best_buddies,
			   giovanni_defeated,
			   mega_evos,
			   unique_stops_spun,
			   unique_mega_evos,
			   unique_raid_bosses,
			   unique_unown,
			   seven_day_streaks,
			   trade_km,
			   raids_with_friends,
			   caught_at_lure,
			   wayfarer_agreements,
			   trainers_referred,
			   raid_achievements,
			   xl_karps,
			   xs_rats,
			   pikachu_caught,
			   league_great_won,
			   league_ultra_won,
			   league_master_won,
			   tiny_pokemon_caught,
			   jumbo_pokemon_caught,
			   collections_done,
			   vivillon,
			   dex_gen1,
			   dex_gen2,
			   dex_gen3,
			   dex_gen4,
			   dex_gen5,
			   dex_gen6,
			   dex_gen7,
			   dex_gen8,
			   dex_gen8a,
			   caught_normal,
			   caught_fighting,
			   caught_flying,
			   caught_poison,
			   caught_ground,
			   caught_rock,
			   caught_bug,
			   caught_ghost,
			   caught_steel,
			   caught_fire,
			   caught_water,
			   caught_grass,
			   caught_electric,
			   caught_psychic,
			   caught_ice,
			   caught_dragon,
			   caught_dark,
			   caught_fairy
		FROM player
		WHERE player.name = ? 
		`,
		name,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	playerCache.Set(name, player, ttlcache.DefaultTTL)
	return &player, nil
}

var ignoreApproxFloats = cmpopts.EquateApprox(0, 0.001)

// This transformer allows to use the Approx comparator
var transformNullFloats = cmp.Transformer("transformNullFloats", func(x null.Float) float64 {
	return x.Float64
})

func hasChangesPlayer(old *Player, new *Player) bool {
	return !cmp.Equal(old, new, transformNullFloats, ignoreApproxFloats)
}

func savePlayerRecord(db db.DbDetails, player *Player) {
	oldPlayer, _ := getPlayerRecord(db, player.Name)

	if oldPlayer != nil && !hasChangesPlayer(oldPlayer, player) {
		return
	}

	log.Traceln(cmp.Diff(oldPlayer, player, transformNullFloats, ignoreApproxFloats))

	player.LastSeen = time.Now().Unix()

	if oldPlayer == nil {
		_, err := db.GeneralDb.NamedExec(
			`
			INSERT INTO player (id, name, last_seen, team, level, xp, battles_won, km_walked, caught_pokemon, gbl_rank, gbl_rating,
								event_badges, stops_spun, evolved, hatched, quests, trades, photobombs, purified, grunts_defeated,
								gym_battles_won, normal_raids_won, legendary_raids_won, trainings_won, berries_fed, hours_defended,
								best_friends, best_buddies, giovanni_defeated, mega_evos, collections_done, unique_stops_spun,
								unique_mega_evos, unique_raid_bosses, unique_unown, seven_day_streaks, trade_km, raids_with_friends,
								caught_at_lure, wayfarer_agreements, trainers_referred, raid_achievements, xl_karps, xs_rats,
								pikachu_caught, league_great_won, league_ultra_won, league_master_won, tiny_pokemon_caught,
								jumbo_pokemon_caught, vivillon, dex_gen1, dex_gen2, dex_gen3, dex_gen4, dex_gen5, dex_gen6,
								dex_gen7, dex_gen8, dex_gen8a, caught_normal, caught_fighting, caught_flying, caught_poison,
								caught_ground, caught_rock, caught_bug, caught_ghost, caught_steel, caught_fire, caught_water,
								caught_grass, caught_electric, caught_psychic, caught_ice, caught_dragon, caught_dark, caught_fairy)
			VALUES (:id, :name, :last_seen, :team, :level, :xp, :battles_won, :km_walked, :caught_pokemon, :gbl_rank, :gbl_rating,
					:event_badges, :stops_spun, :evolved, :hatched, :quests, :trades, :photobombs, :purified, :grunts_defeated,
					:gym_battles_won, :normal_raids_won, :legendary_raids_won, :trainings_won, :berries_fed, :hours_defended,
					:best_friends, :best_buddies, :giovanni_defeated, :mega_evos, :collections_done, :unique_stops_spun,
					:unique_mega_evos, :unique_raid_bosses, :unique_unown, :seven_day_streaks, :trade_km, :raids_with_friends,
					:caught_at_lure, :wayfarer_agreements, :trainers_referred, :raid_achievements, :xl_karps, :xs_rats,
					:pikachu_caught, :league_great_won, :league_ultra_won, :league_master_won, :tiny_pokemon_caught,
					:jumbo_pokemon_caught, :vivillon, :dex_gen1, :dex_gen2, :dex_gen3, :dex_gen4, :dex_gen5, :dex_gen6, :dex_gen7,
					:dex_gen8, :dex_gen8a, :caught_normal, :caught_fighting, :caught_flying, :caught_poison, :caught_ground,
					:caught_rock, :caught_bug, :caught_ghost, :caught_steel, :caught_fire, :caught_water, :caught_grass,
					:caught_electric, :caught_psychic, :caught_ice, :caught_dragon, :caught_dark, :caught_fairy)
			`,
			player,
		)

		if err != nil {
			log.Errorf("insert player error: %s", err)
			return
		}
	} else {
		_, err := db.GeneralDb.NamedExec(
			`UPDATE player SET
				id = :id, 
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
				dex_gen1 = :dex_gen1, 
				dex_gen2 = :dex_gen2, 
				dex_gen3 = :dex_gen3, 
				dex_gen4 = :dex_gen4, 
				dex_gen5 = :dex_gen5, 
				dex_gen6 = :dex_gen6, 
				dex_gen7 = :dex_gen7, 
				dex_gen8 = :dex_gen8, 
				dex_gen8a = :dex_gen8a, 
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

		if err != nil {
			log.Errorf("Update player error %s", err)
		}
	}

	playerCache.Set(player.Name, *player, ttlcache.DefaultTTL)
}

func (player *Player) updateFromPublicProfile(publicProfile *pogo.PlayerPublicProfileProto) {
	player.Name = publicProfile.GetName()
	player.Team = null.IntFrom(int64(publicProfile.GetTeam()))
	player.Level = null.IntFrom(int64(publicProfile.GetLevel()))
	player.Xp = null.IntFrom(publicProfile.GetExperience())
	player.BattlesWon = null.IntFrom(int64(publicProfile.GetBattlesWon()))
	player.KmWalked = null.FloatFrom(float64(publicProfile.GetKmWalked()))
	player.CaughtPokemon = null.IntFrom(int64(publicProfile.GetCaughtPokemon()))
	player.GblRank = null.IntFrom(int64(publicProfile.GetCombatRank()))
	player.GblRating = null.IntFrom(int64(publicProfile.GetCombatRating()))

	eventBadges := ""
	badges := map[string]null.Int{}

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

		badges[playerKey] = null.IntFrom(int64(badge.GetCurrentValue()))
	}

	if eventBadges != "" {
		player.EventBadges = null.StringFrom(eventBadges)
	}

	playerJson, err := json.Marshal(badges)

	if err != nil {
		log.Errorf("Failed to parse badges %s", err)
		return
	}

	json.Unmarshal(playerJson, player)
}

func UpdatePlayerRecordWithPlayerSummary(db db.DbDetails, playerSummary *pogo.PlayerSummaryProto, publicProfile *pogo.PlayerPublicProfileProto) error {
	player, err := getPlayerRecord(db, playerSummary.GetCodename())
	if err != nil {
		return err
	}

	if player == nil {
		player = &Player{
			Name: playerSummary.GetCodename(),
		}
	}
	if player.Id.IsZero() {
		player.Id = null.StringFrom(playerSummary.GetPlayerId())
	}
	player.updateFromPublicProfile(publicProfile)
	savePlayerRecord(db, player)
	return nil
}
