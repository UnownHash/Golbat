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
// REMINDER! Keep hasChangesPlayer updated after making changes
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
		return &player, nil
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

	playerCache.Set(name, player, ttlcache.DefaultTTL)
	return &player, nil
}

// hasChangesPlayer compares two Player structs
// Float tolerance: KmWalked = 0.001
func hasChangesPlayer(old *Player, new *Player) bool {
	return old.Name != new.Name ||
		old.FriendshipId != new.FriendshipId ||
		old.LastSeen != new.LastSeen ||
		old.FriendCode != new.FriendCode ||
		old.Team != new.Team ||
		old.Level != new.Level ||
		old.Xp != new.Xp ||
		old.BattlesWon != new.BattlesWon ||
		old.CaughtPokemon != new.CaughtPokemon ||
		old.GblRank != new.GblRank ||
		old.GblRating != new.GblRating ||
		old.EventBadges != new.EventBadges ||
		old.StopsSpun != new.StopsSpun ||
		old.Evolved != new.Evolved ||
		old.Hatched != new.Hatched ||
		old.Quests != new.Quests ||
		old.Trades != new.Trades ||
		old.Photobombs != new.Photobombs ||
		old.Purified != new.Purified ||
		old.GruntsDefeated != new.GruntsDefeated ||
		old.GymBattlesWon != new.GymBattlesWon ||
		old.NormalRaidsWon != new.NormalRaidsWon ||
		old.LegendaryRaidsWon != new.LegendaryRaidsWon ||
		old.TrainingsWon != new.TrainingsWon ||
		old.BerriesFed != new.BerriesFed ||
		old.HoursDefended != new.HoursDefended ||
		old.BestFriends != new.BestFriends ||
		old.BestBuddies != new.BestBuddies ||
		old.GiovanniDefeated != new.GiovanniDefeated ||
		old.MegaEvos != new.MegaEvos ||
		old.CollectionsDone != new.CollectionsDone ||
		old.UniqueStopsSpun != new.UniqueStopsSpun ||
		old.UniqueMegaEvos != new.UniqueMegaEvos ||
		old.UniqueRaidBosses != new.UniqueRaidBosses ||
		old.UniqueUnown != new.UniqueUnown ||
		old.SevenDayStreaks != new.SevenDayStreaks ||
		old.TradeKm != new.TradeKm ||
		old.RaidsWithFriends != new.RaidsWithFriends ||
		old.CaughtAtLure != new.CaughtAtLure ||
		old.WayfarerAgreements != new.WayfarerAgreements ||
		old.TrainersReferred != new.TrainersReferred ||
		old.RaidAchievements != new.RaidAchievements ||
		old.XlKarps != new.XlKarps ||
		old.XsRats != new.XsRats ||
		old.PikachuCaught != new.PikachuCaught ||
		old.LeagueGreatWon != new.LeagueGreatWon ||
		old.LeagueUltraWon != new.LeagueUltraWon ||
		old.LeagueMasterWon != new.LeagueMasterWon ||
		old.TinyPokemonCaught != new.TinyPokemonCaught ||
		old.JumboPokemonCaught != new.JumboPokemonCaught ||
		old.Vivillon != new.Vivillon ||
		old.MaxSizeFirstPlace != new.MaxSizeFirstPlace ||
		old.TotalRoutePlay != new.TotalRoutePlay ||
		old.PartiesCompleted != new.PartiesCompleted ||
		old.EventCheckIns != new.EventCheckIns ||
		old.DexGen1 != new.DexGen1 ||
		old.DexGen2 != new.DexGen2 ||
		old.DexGen3 != new.DexGen3 ||
		old.DexGen4 != new.DexGen4 ||
		old.DexGen5 != new.DexGen5 ||
		old.DexGen6 != new.DexGen6 ||
		old.DexGen7 != new.DexGen7 ||
		old.DexGen8 != new.DexGen8 ||
		old.DexGen8A != new.DexGen8A ||
		old.DexGen9 != new.DexGen9 ||
		old.CaughtNormal != new.CaughtNormal ||
		old.CaughtFighting != new.CaughtFighting ||
		old.CaughtFlying != new.CaughtFlying ||
		old.CaughtPoison != new.CaughtPoison ||
		old.CaughtGround != new.CaughtGround ||
		old.CaughtRock != new.CaughtRock ||
		old.CaughtBug != new.CaughtBug ||
		old.CaughtGhost != new.CaughtGhost ||
		old.CaughtSteel != new.CaughtSteel ||
		old.CaughtFire != new.CaughtFire ||
		old.CaughtWater != new.CaughtWater ||
		old.CaughtGrass != new.CaughtGrass ||
		old.CaughtElectric != new.CaughtElectric ||
		old.CaughtPsychic != new.CaughtPsychic ||
		old.CaughtIce != new.CaughtIce ||
		old.CaughtDragon != new.CaughtDragon ||
		old.CaughtDark != new.CaughtDark ||
		old.CaughtFairy != new.CaughtFairy ||
		!nullFloatAlmostEqual(old.KmWalked, new.KmWalked, 0.001)
}

func savePlayerRecord(db db.DbDetails, player *Player) {
	oldPlayer, _ := getPlayerRecord(db, player.Name, player.FriendshipId.String, player.FriendCode.String)

	if oldPlayer != nil && !hasChangesPlayer(oldPlayer, player) {
		return
	}

	//log.Traceln(cmp.Diff(oldPlayer, player, transformNullFloats, ignoreApproxFloats))

	player.LastSeen = time.Now().Unix()

	if oldPlayer == nil {
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
			field.Set(reflect.ValueOf(newValue))
		}
	}

	if eventBadges != "" {
		player.EventBadges = null.StringFrom(eventBadges)
	}
}

func UpdatePlayerRecordWithPlayerSummary(db db.DbDetails, playerSummary *pogo.InternalPlayerSummaryProto, publicProfile *pogo.PlayerPublicProfileProto, friendCode string, friendshipId string) error {
	player, err := getPlayerRecord(db, playerSummary.GetCodename(), friendshipId, friendCode)
	if err != nil {
		return err
	}

	if player == nil {
		player = &Player{
			Name: playerSummary.GetCodename(),
		}
	}

	if player.FriendshipId.IsZero() && friendshipId != "" {
		player.FriendshipId = null.StringFrom(friendshipId)
	}
	if player.FriendCode.IsZero() && friendCode != "" {
		player.FriendCode = null.StringFrom(friendCode)
	}

	player.updateFromPublicProfile(publicProfile)
	savePlayerRecord(db, player)
	return nil
}
