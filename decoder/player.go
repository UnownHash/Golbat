package decoder

import (
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"golbat/db"
	"golbat/pogo"

	"github.com/guregu/null/v6"
	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
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

	dirty         bool     `db:"-" json:"-"` // Not persisted - tracks if object needs saving
	newRecord     bool     `db:"-" json:"-"` // Not persisted - tracks if this is a new record
	changedFields []string `db:"-" json:"-"` // Track which fields changed (only when dbDebugEnabled)
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
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("FriendshipId:%s->%s", FormatNull(p.FriendshipId), FormatNull(v)))
		}
		p.FriendshipId = v
		p.dirty = true
	}
}

func (p *Player) SetFriendCode(v null.String) {
	if p.FriendCode != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("FriendCode:%s->%s", FormatNull(p.FriendCode), FormatNull(v)))
		}
		p.FriendCode = v
		p.dirty = true
	}
}

func (p *Player) SetTeam(v null.Int) {
	if p.Team != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("Team:%s->%s", FormatNull(p.Team), FormatNull(v)))
		}
		p.Team = v
		p.dirty = true
	}
}

func (p *Player) SetLevel(v null.Int) {
	if p.Level != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("Level:%s->%s", FormatNull(p.Level), FormatNull(v)))
		}
		p.Level = v
		p.dirty = true
	}
}

func (p *Player) SetXp(v null.Int) {
	if p.Xp != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("Xp:%s->%s", FormatNull(p.Xp), FormatNull(v)))
		}
		p.Xp = v
		p.dirty = true
	}
}

func (p *Player) SetBattlesWon(v null.Int) {
	if p.BattlesWon != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("BattlesWon:%s->%s", FormatNull(p.BattlesWon), FormatNull(v)))
		}
		p.BattlesWon = v
		p.dirty = true
	}
}

func (p *Player) SetKmWalked(v null.Float) {
	if !nullFloatAlmostEqual(p.KmWalked, v, 0.001) {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("KmWalked:%s->%s", FormatNull(p.KmWalked), FormatNull(v)))
		}
		p.KmWalked = v
		p.dirty = true
	}
}

func (p *Player) SetCaughtPokemon(v null.Int) {
	if p.CaughtPokemon != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("CaughtPokemon:%s->%s", FormatNull(p.CaughtPokemon), FormatNull(v)))
		}
		p.CaughtPokemon = v
		p.dirty = true
	}
}

func (p *Player) SetGblRank(v null.Int) {
	if p.GblRank != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("GblRank:%s->%s", FormatNull(p.GblRank), FormatNull(v)))
		}
		p.GblRank = v
		p.dirty = true
	}
}

func (p *Player) SetGblRating(v null.Int) {
	if p.GblRating != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("GblRating:%s->%s", FormatNull(p.GblRating), FormatNull(v)))
		}
		p.GblRating = v
		p.dirty = true
	}
}

func (p *Player) SetEventBadges(v null.String) {
	if p.EventBadges != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("EventBadges:%s->%s", FormatNull(p.EventBadges), FormatNull(v)))
		}
		p.EventBadges = v
		p.dirty = true
	}
}

func (p *Player) SetStopsSpun(v null.Int) {
	if p.StopsSpun != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("StopsSpun:%s->%s", FormatNull(p.StopsSpun), FormatNull(v)))
		}
		p.StopsSpun = v
		p.dirty = true
	}
}

func (p *Player) SetEvolved(v null.Int) {
	if p.Evolved != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("Evolved:%s->%s", FormatNull(p.Evolved), FormatNull(v)))
		}
		p.Evolved = v
		p.dirty = true
	}
}

func (p *Player) SetHatched(v null.Int) {
	if p.Hatched != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("Hatched:%s->%s", FormatNull(p.Hatched), FormatNull(v)))
		}
		p.Hatched = v
		p.dirty = true
	}
}

func (p *Player) SetQuests(v null.Int) {
	if p.Quests != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("Quests:%s->%s", FormatNull(p.Quests), FormatNull(v)))
		}
		p.Quests = v
		p.dirty = true
	}
}

func (p *Player) SetTrades(v null.Int) {
	if p.Trades != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("Trades:%s->%s", FormatNull(p.Trades), FormatNull(v)))
		}
		p.Trades = v
		p.dirty = true
	}
}

func (p *Player) SetPhotobombs(v null.Int) {
	if p.Photobombs != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("Photobombs:%s->%s", FormatNull(p.Photobombs), FormatNull(v)))
		}
		p.Photobombs = v
		p.dirty = true
	}
}

func (p *Player) SetPurified(v null.Int) {
	if p.Purified != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("Purified:%s->%s", FormatNull(p.Purified), FormatNull(v)))
		}
		p.Purified = v
		p.dirty = true
	}
}

func (p *Player) SetGruntsDefeated(v null.Int) {
	if p.GruntsDefeated != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("GruntsDefeated:%s->%s", FormatNull(p.GruntsDefeated), FormatNull(v)))
		}
		p.GruntsDefeated = v
		p.dirty = true
	}
}

func (p *Player) SetGymBattlesWon(v null.Int) {
	if p.GymBattlesWon != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("GymBattlesWon:%s->%s", FormatNull(p.GymBattlesWon), FormatNull(v)))
		}
		p.GymBattlesWon = v
		p.dirty = true
	}
}

func (p *Player) SetNormalRaidsWon(v null.Int) {
	if p.NormalRaidsWon != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("NormalRaidsWon:%s->%s", FormatNull(p.NormalRaidsWon), FormatNull(v)))
		}
		p.NormalRaidsWon = v
		p.dirty = true
	}
}

func (p *Player) SetLegendaryRaidsWon(v null.Int) {
	if p.LegendaryRaidsWon != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("LegendaryRaidsWon:%s->%s", FormatNull(p.LegendaryRaidsWon), FormatNull(v)))
		}
		p.LegendaryRaidsWon = v
		p.dirty = true
	}
}

func (p *Player) SetTrainingsWon(v null.Int) {
	if p.TrainingsWon != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("TrainingsWon:%s->%s", FormatNull(p.TrainingsWon), FormatNull(v)))
		}
		p.TrainingsWon = v
		p.dirty = true
	}
}

func (p *Player) SetBerriesFed(v null.Int) {
	if p.BerriesFed != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("BerriesFed:%s->%s", FormatNull(p.BerriesFed), FormatNull(v)))
		}
		p.BerriesFed = v
		p.dirty = true
	}
}

func (p *Player) SetHoursDefended(v null.Int) {
	if p.HoursDefended != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("HoursDefended:%s->%s", FormatNull(p.HoursDefended), FormatNull(v)))
		}
		p.HoursDefended = v
		p.dirty = true
	}
}

func (p *Player) SetBestFriends(v null.Int) {
	if p.BestFriends != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("BestFriends:%s->%s", FormatNull(p.BestFriends), FormatNull(v)))
		}
		p.BestFriends = v
		p.dirty = true
	}
}

func (p *Player) SetBestBuddies(v null.Int) {
	if p.BestBuddies != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("BestBuddies:%s->%s", FormatNull(p.BestBuddies), FormatNull(v)))
		}
		p.BestBuddies = v
		p.dirty = true
	}
}

func (p *Player) SetGiovanniDefeated(v null.Int) {
	if p.GiovanniDefeated != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("GiovanniDefeated:%s->%s", FormatNull(p.GiovanniDefeated), FormatNull(v)))
		}
		p.GiovanniDefeated = v
		p.dirty = true
	}
}

func (p *Player) SetMegaEvos(v null.Int) {
	if p.MegaEvos != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("MegaEvos:%s->%s", FormatNull(p.MegaEvos), FormatNull(v)))
		}
		p.MegaEvos = v
		p.dirty = true
	}
}

func (p *Player) SetCollectionsDone(v null.Int) {
	if p.CollectionsDone != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("CollectionsDone:%s->%s", FormatNull(p.CollectionsDone), FormatNull(v)))
		}
		p.CollectionsDone = v
		p.dirty = true
	}
}

func (p *Player) SetUniqueStopsSpun(v null.Int) {
	if p.UniqueStopsSpun != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("UniqueStopsSpun:%s->%s", FormatNull(p.UniqueStopsSpun), FormatNull(v)))
		}
		p.UniqueStopsSpun = v
		p.dirty = true
	}
}

func (p *Player) SetUniqueMegaEvos(v null.Int) {
	if p.UniqueMegaEvos != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("UniqueMegaEvos:%s->%s", FormatNull(p.UniqueMegaEvos), FormatNull(v)))
		}
		p.UniqueMegaEvos = v
		p.dirty = true
	}
}

func (p *Player) SetUniqueRaidBosses(v null.Int) {
	if p.UniqueRaidBosses != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("UniqueRaidBosses:%s->%s", FormatNull(p.UniqueRaidBosses), FormatNull(v)))
		}
		p.UniqueRaidBosses = v
		p.dirty = true
	}
}

func (p *Player) SetUniqueUnown(v null.Int) {
	if p.UniqueUnown != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("UniqueUnown:%s->%s", FormatNull(p.UniqueUnown), FormatNull(v)))
		}
		p.UniqueUnown = v
		p.dirty = true
	}
}

func (p *Player) SetSevenDayStreaks(v null.Int) {
	if p.SevenDayStreaks != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("SevenDayStreaks:%s->%s", FormatNull(p.SevenDayStreaks), FormatNull(v)))
		}
		p.SevenDayStreaks = v
		p.dirty = true
	}
}

func (p *Player) SetTradeKm(v null.Int) {
	if p.TradeKm != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("TradeKm:%s->%s", FormatNull(p.TradeKm), FormatNull(v)))
		}
		p.TradeKm = v
		p.dirty = true
	}
}

func (p *Player) SetRaidsWithFriends(v null.Int) {
	if p.RaidsWithFriends != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("RaidsWithFriends:%s->%s", FormatNull(p.RaidsWithFriends), FormatNull(v)))
		}
		p.RaidsWithFriends = v
		p.dirty = true
	}
}

func (p *Player) SetCaughtAtLure(v null.Int) {
	if p.CaughtAtLure != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("CaughtAtLure:%s->%s", FormatNull(p.CaughtAtLure), FormatNull(v)))
		}
		p.CaughtAtLure = v
		p.dirty = true
	}
}

func (p *Player) SetWayfarerAgreements(v null.Int) {
	if p.WayfarerAgreements != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("WayfarerAgreements:%s->%s", FormatNull(p.WayfarerAgreements), FormatNull(v)))
		}
		p.WayfarerAgreements = v
		p.dirty = true
	}
}

func (p *Player) SetTrainersReferred(v null.Int) {
	if p.TrainersReferred != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("TrainersReferred:%s->%s", FormatNull(p.TrainersReferred), FormatNull(v)))
		}
		p.TrainersReferred = v
		p.dirty = true
	}
}

func (p *Player) SetRaidAchievements(v null.Int) {
	if p.RaidAchievements != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("RaidAchievements:%s->%s", FormatNull(p.RaidAchievements), FormatNull(v)))
		}
		p.RaidAchievements = v
		p.dirty = true
	}
}

func (p *Player) SetXlKarps(v null.Int) {
	if p.XlKarps != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("XlKarps:%s->%s", FormatNull(p.XlKarps), FormatNull(v)))
		}
		p.XlKarps = v
		p.dirty = true
	}
}

func (p *Player) SetXsRats(v null.Int) {
	if p.XsRats != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("XsRats:%s->%s", FormatNull(p.XsRats), FormatNull(v)))
		}
		p.XsRats = v
		p.dirty = true
	}
}

func (p *Player) SetPikachuCaught(v null.Int) {
	if p.PikachuCaught != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("PikachuCaught:%s->%s", FormatNull(p.PikachuCaught), FormatNull(v)))
		}
		p.PikachuCaught = v
		p.dirty = true
	}
}

func (p *Player) SetLeagueGreatWon(v null.Int) {
	if p.LeagueGreatWon != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("LeagueGreatWon:%s->%s", FormatNull(p.LeagueGreatWon), FormatNull(v)))
		}
		p.LeagueGreatWon = v
		p.dirty = true
	}
}

func (p *Player) SetLeagueUltraWon(v null.Int) {
	if p.LeagueUltraWon != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("LeagueUltraWon:%s->%s", FormatNull(p.LeagueUltraWon), FormatNull(v)))
		}
		p.LeagueUltraWon = v
		p.dirty = true
	}
}

func (p *Player) SetLeagueMasterWon(v null.Int) {
	if p.LeagueMasterWon != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("LeagueMasterWon:%s->%s", FormatNull(p.LeagueMasterWon), FormatNull(v)))
		}
		p.LeagueMasterWon = v
		p.dirty = true
	}
}

func (p *Player) SetTinyPokemonCaught(v null.Int) {
	if p.TinyPokemonCaught != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("TinyPokemonCaught:%s->%s", FormatNull(p.TinyPokemonCaught), FormatNull(v)))
		}
		p.TinyPokemonCaught = v
		p.dirty = true
	}
}

func (p *Player) SetJumboPokemonCaught(v null.Int) {
	if p.JumboPokemonCaught != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("JumboPokemonCaught:%s->%s", FormatNull(p.JumboPokemonCaught), FormatNull(v)))
		}
		p.JumboPokemonCaught = v
		p.dirty = true
	}
}

func (p *Player) SetVivillon(v null.Int) {
	if p.Vivillon != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("Vivillon:%s->%s", FormatNull(p.Vivillon), FormatNull(v)))
		}
		p.Vivillon = v
		p.dirty = true
	}
}

func (p *Player) SetMaxSizeFirstPlace(v null.Int) {
	if p.MaxSizeFirstPlace != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("MaxSizeFirstPlace:%s->%s", FormatNull(p.MaxSizeFirstPlace), FormatNull(v)))
		}
		p.MaxSizeFirstPlace = v
		p.dirty = true
	}
}

func (p *Player) SetTotalRoutePlay(v null.Int) {
	if p.TotalRoutePlay != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("TotalRoutePlay:%s->%s", FormatNull(p.TotalRoutePlay), FormatNull(v)))
		}
		p.TotalRoutePlay = v
		p.dirty = true
	}
}

func (p *Player) SetPartiesCompleted(v null.Int) {
	if p.PartiesCompleted != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("PartiesCompleted:%s->%s", FormatNull(p.PartiesCompleted), FormatNull(v)))
		}
		p.PartiesCompleted = v
		p.dirty = true
	}
}

func (p *Player) SetEventCheckIns(v null.Int) {
	if p.EventCheckIns != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("EventCheckIns:%s->%s", FormatNull(p.EventCheckIns), FormatNull(v)))
		}
		p.EventCheckIns = v
		p.dirty = true
	}
}
func (p *Player) SetDexGen1(v null.Int) {
	if p.DexGen1 != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("DexGen1:%s->%s", FormatNull(p.DexGen1), FormatNull(v)))
		}
		p.DexGen1 = v
		p.dirty = true
	}
}

func (p *Player) SetDexGen2(v null.Int) {
	if p.DexGen2 != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("DexGen2:%s->%s", FormatNull(p.DexGen2), FormatNull(v)))
		}
		p.DexGen2 = v
		p.dirty = true
	}
}

func (p *Player) SetDexGen3(v null.Int) {
	if p.DexGen3 != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("DexGen3:%s->%s", FormatNull(p.DexGen3), FormatNull(v)))
		}
		p.DexGen3 = v
		p.dirty = true
	}
}

func (p *Player) SetDexGen4(v null.Int) {
	if p.DexGen4 != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("DexGen4:%s->%s", FormatNull(p.DexGen4), FormatNull(v)))
		}
		p.DexGen4 = v
		p.dirty = true
	}
}

func (p *Player) SetDexGen5(v null.Int) {
	if p.DexGen5 != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("DexGen5:%s->%s", FormatNull(p.DexGen5), FormatNull(v)))
		}
		p.DexGen5 = v
		p.dirty = true
	}
}

func (p *Player) SetDexGen6(v null.Int) {
	if p.DexGen6 != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("DexGen6:%s->%s", FormatNull(p.DexGen6), FormatNull(v)))
		}
		p.DexGen6 = v
		p.dirty = true
	}
}

func (p *Player) SetDexGen7(v null.Int) {
	if p.DexGen7 != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("DexGen7:%s->%s", FormatNull(p.DexGen7), FormatNull(v)))
		}
		p.DexGen7 = v
		p.dirty = true
	}
}

func (p *Player) SetDexGen8(v null.Int) {
	if p.DexGen8 != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("DexGen8:%s->%s", FormatNull(p.DexGen8), FormatNull(v)))
		}
		p.DexGen8 = v
		p.dirty = true
	}
}

func (p *Player) SetDexGen8A(v null.Int) {
	if p.DexGen8A != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("DexGen8A:%s->%s", FormatNull(p.DexGen8A), FormatNull(v)))
		}
		p.DexGen8A = v
		p.dirty = true
	}
}

func (p *Player) SetDexGen9(v null.Int) {
	if p.DexGen9 != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("DexGen9:%s->%s", FormatNull(p.DexGen9), FormatNull(v)))
		}
		p.DexGen9 = v
		p.dirty = true
	}
}

func (p *Player) SetCaughtNormal(v null.Int) {
	if p.CaughtNormal != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("CaughtNormal:%s->%s", FormatNull(p.CaughtNormal), FormatNull(v)))
		}
		p.CaughtNormal = v
		p.dirty = true
	}
}

func (p *Player) SetCaughtFighting(v null.Int) {
	if p.CaughtFighting != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("CaughtFighting:%s->%s", FormatNull(p.CaughtFighting), FormatNull(v)))
		}
		p.CaughtFighting = v
		p.dirty = true
	}
}

func (p *Player) SetCaughtFlying(v null.Int) {
	if p.CaughtFlying != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("CaughtFlying:%s->%s", FormatNull(p.CaughtFlying), FormatNull(v)))
		}
		p.CaughtFlying = v
		p.dirty = true
	}
}

func (p *Player) SetCaughtPoison(v null.Int) {
	if p.CaughtPoison != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("CaughtPoison:%s->%s", FormatNull(p.CaughtPoison), FormatNull(v)))
		}
		p.CaughtPoison = v
		p.dirty = true
	}
}

func (p *Player) SetCaughtGround(v null.Int) {
	if p.CaughtGround != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("CaughtGround:%s->%s", FormatNull(p.CaughtGround), FormatNull(v)))
		}
		p.CaughtGround = v
		p.dirty = true
	}
}

func (p *Player) SetCaughtRock(v null.Int) {
	if p.CaughtRock != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("CaughtRock:%s->%s", FormatNull(p.CaughtRock), FormatNull(v)))
		}
		p.CaughtRock = v
		p.dirty = true
	}
}

func (p *Player) SetCaughtBug(v null.Int) {
	if p.CaughtBug != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("CaughtBug:%s->%s", FormatNull(p.CaughtBug), FormatNull(v)))
		}
		p.CaughtBug = v
		p.dirty = true
	}
}

func (p *Player) SetCaughtGhost(v null.Int) {
	if p.CaughtGhost != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("CaughtGhost:%s->%s", FormatNull(p.CaughtGhost), FormatNull(v)))
		}
		p.CaughtGhost = v
		p.dirty = true
	}
}

func (p *Player) SetCaughtSteel(v null.Int) {
	if p.CaughtSteel != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("CaughtSteel:%s->%s", FormatNull(p.CaughtSteel), FormatNull(v)))
		}
		p.CaughtSteel = v
		p.dirty = true
	}
}

func (p *Player) SetCaughtFire(v null.Int) {
	if p.CaughtFire != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("CaughtFire:%s->%s", FormatNull(p.CaughtFire), FormatNull(v)))
		}
		p.CaughtFire = v
		p.dirty = true
	}
}

func (p *Player) SetCaughtWater(v null.Int) {
	if p.CaughtWater != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("CaughtWater:%s->%s", FormatNull(p.CaughtWater), FormatNull(v)))
		}
		p.CaughtWater = v
		p.dirty = true
	}
}

func (p *Player) SetCaughtGrass(v null.Int) {
	if p.CaughtGrass != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("CaughtGrass:%s->%s", FormatNull(p.CaughtGrass), FormatNull(v)))
		}
		p.CaughtGrass = v
		p.dirty = true
	}
}

func (p *Player) SetCaughtElectric(v null.Int) {
	if p.CaughtElectric != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("CaughtElectric:%s->%s", FormatNull(p.CaughtElectric), FormatNull(v)))
		}
		p.CaughtElectric = v
		p.dirty = true
	}
}

func (p *Player) SetCaughtPsychic(v null.Int) {
	if p.CaughtPsychic != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("CaughtPsychic:%s->%s", FormatNull(p.CaughtPsychic), FormatNull(v)))
		}
		p.CaughtPsychic = v
		p.dirty = true
	}
}

func (p *Player) SetCaughtIce(v null.Int) {
	if p.CaughtIce != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("CaughtIce:%s->%s", FormatNull(p.CaughtIce), FormatNull(v)))
		}
		p.CaughtIce = v
		p.dirty = true
	}
}

func (p *Player) SetCaughtDragon(v null.Int) {
	if p.CaughtDragon != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("CaughtDragon:%s->%s", FormatNull(p.CaughtDragon), FormatNull(v)))
		}
		p.CaughtDragon = v
		p.dirty = true
	}
}

func (p *Player) SetCaughtDark(v null.Int) {
	if p.CaughtDark != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("CaughtDark:%s->%s", FormatNull(p.CaughtDark), FormatNull(v)))
		}
		p.CaughtDark = v
		p.dirty = true
	}
}

func (p *Player) SetCaughtFairy(v null.Int) {
	if p.CaughtFairy != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("CaughtFairy:%s->%s", FormatNull(p.CaughtFairy), FormatNull(v)))
		}
		p.CaughtFairy = v
		p.dirty = true
	}
}

func (p *Player) SetLastSeen(v int64) {
	if p.LastSeen != v {
		if dbDebugEnabled {
			p.changedFields = append(p.changedFields, fmt.Sprintf("LastSeen:%d->%d", p.LastSeen, v))
		}
		p.LastSeen = v
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

	player.SetLastSeen(time.Now().Unix())

	if dbDebugEnabled {
		if player.IsNewRecord() {
			dbDebugLog("INSERT", "Player", player.Name, player.changedFields)
		} else {
			dbDebugLog("UPDATE", "Player", player.Name, player.changedFields)
		}
	}

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
