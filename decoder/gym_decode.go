package decoder

import (
	"cmp"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/guregu/null/v6"
	log "github.com/sirupsen/logrus"

	"golbat/pogo"
	"golbat/pogoshim"
	"golbat/util"
)

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

func calculatePowerUpPoints(fortData pogoshim.PokemonFortProto) (null.Int, null.Int) {
	now := time.Now().Unix()
	powerUpLevelExpirationMs := fortData.GetPowerUpLevelExpirationMs() / 1000
	powerUpPoints := int64(fortData.GetPowerUpProgressPoints())
	var powerUpLevel null.Int
	powerUpEndTimestamp := null.NewInt(0, false)
	if powerUpPoints < 50 {
		powerUpLevel = null.IntFrom(0)
	} else if powerUpPoints < 100 && powerUpLevelExpirationMs > now {
		powerUpLevel = null.IntFrom(1)
		powerUpEndTimestamp = null.IntFrom(powerUpLevelExpirationMs)
	} else if powerUpPoints < 150 && powerUpLevelExpirationMs > now {
		powerUpLevel = null.IntFrom(2)
		powerUpEndTimestamp = null.IntFrom(powerUpLevelExpirationMs)
	} else if powerUpLevelExpirationMs > now {
		powerUpLevel = null.IntFrom(3)
		powerUpEndTimestamp = null.IntFrom(powerUpLevelExpirationMs)
	} else {
		powerUpLevel = null.IntFrom(0)
	}

	return powerUpLevel, powerUpEndTimestamp
}

func (gym *Gym) updateGymFromFort(fortData pogoshim.PokemonFortProto, cellId uint64, timestampMs int64) *Gym {
	type pokemonDisplay struct {
		Form                  int    `json:"form,omitempty"`
		Costume               int    `json:"costume,omitempty"`
		Gender                int    `json:"gender"`
		Shiny                 bool   `json:"shiny,omitempty"`
		TempEvolution         int    `json:"temp_evolution,omitempty"`
		TempEvolutionFinishMs int64  `json:"temp_evolution_finish_ms,omitempty"`
		Alignment             int    `json:"alignment,omitempty"`
		Badge                 int    `json:"badge,omitempty"`
		Background            *int64 `json:"background,omitempty"`
	}
	gym.SetId(fortData.GetFortId())
	gym.SetLat(fortData.GetLatitude())
	gym.SetLon(fortData.GetLongitude())
	gym.SetEnabled(null.IntFrom(util.BoolToInt[int64](fortData.GetEnabled())))
	gym.SetGuardingPokemonId(null.IntFrom(int64(fortData.GetGuardPokemonId())))
	if !fortData.HasGuardPokemonDisplay() {
		gym.SetGuardingPokemonDisplay(null.NewString("", false))
	} else {
		guardDisplay := fortData.GetGuardPokemonDisplay()
		display, _ := json.Marshal(pokemonDisplay{
			Form:                  int(guardDisplay.GetForm()),
			Costume:               int(guardDisplay.GetCostume()),
			Gender:                int(guardDisplay.GetGender()),
			Shiny:                 guardDisplay.GetShiny(),
			TempEvolution:         int(guardDisplay.GetCurrentTempEvolution()),
			TempEvolutionFinishMs: guardDisplay.GetTemporaryEvolutionFinishMs(),
			Alignment:             int(guardDisplay.GetAlignment()),
			Badge:                 int(guardDisplay.GetPokemonBadge()),
			Background:            util.ExtractBackgroundFromDisplayShim(guardDisplay),
		})
		gym.SetGuardingPokemonDisplay(null.StringFrom(string(display)))
	}
	gym.SetTeamId(null.IntFrom(int64(fortData.GetTeam())))
	if fortData.HasGymDisplay() {
		gym.SetAvailableSlots(null.IntFrom(int64(fortData.GetGymDisplay().GetSlotsAvailable())))
	} else {
		gym.SetAvailableSlots(null.IntFrom(6)) // this may be an incorrect assumption
	}
	gym.SetLastModifiedTimestamp(null.IntFrom(fortData.GetLastModifiedMs() / 1000))
	gym.SetExRaidEligible(null.IntFrom(util.BoolToInt[int64](fortData.GetIsExRaidEligible())))

	if fortData.GetImageUrl() != "" {
		gym.SetUrl(null.StringFrom(fortData.GetImageUrl()))
	}
	gym.SetInBattle(null.IntFrom(util.BoolToInt[int64](fortData.GetIsInBattle())))
	gym.SetArScanEligible(null.IntFrom(util.BoolToInt[int64](fortData.GetIsArScanEligible())))
	gym.SetPowerUpPoints(null.IntFrom(int64(fortData.GetPowerUpProgressPoints())))

	powerUpLevel, powerUpEndTimestamp := calculatePowerUpPoints(fortData)
	gym.SetPowerUpLevel(powerUpLevel)
	gym.SetPowerUpEndTimestamp(powerUpEndTimestamp)

	if fortData.GetPartnerId() == "" {
		gym.SetPartnerId(null.NewString("", false))
	} else {
		gym.SetPartnerId(null.StringFrom(fortData.GetPartnerId()))
	}

	if fortData.GetImageUrl() != "" {
		gym.SetUrl(null.StringFrom(fortData.GetImageUrl()))
	}
	if fortData.GetTeam() == 0 { // check!!
		gym.SetTotalCp(null.IntFrom(0))
	} else {
		if fortData.HasGymDisplay() {
			totalCp := int64(fortData.GetGymDisplay().GetTotalGymCp())
			if gym.TotalCp.Int64-totalCp > 100 || totalCp-gym.TotalCp.Int64 > 100 {
				gym.SetTotalCp(null.IntFrom(totalCp))
			}
		} else {
			gym.SetTotalCp(null.IntFrom(0))
		}
	}

	if fortData.HasRaidInfo() {
		raidInfo := fortData.GetRaidInfo()
		gym.SetRaidEndTimestamp(null.IntFrom(raidInfo.GetRaidEndMs() / 1000))
		gym.SetRaidSpawnTimestamp(null.IntFrom(raidInfo.GetRaidSpawnMs() / 1000))
		gym.SetRaidSeed(null.IntFrom(raidInfo.GetRaidSeed()))
		raidBattleTimestamp := raidInfo.GetRaidBattleMs() / 1000

		if gym.RaidBattleTimestamp.ValueOrZero() != raidBattleTimestamp {
			// We are reporting a new raid, clear rsvp data
			gym.SetRsvps(null.NewString("", false))
		}
		gym.SetRaidBattleTimestamp(null.IntFrom(raidBattleTimestamp))

		gym.SetRaidLevel(null.IntFrom(int64(raidInfo.GetRaidLevel())))
		if raidInfo.HasRaidPokemon() {
			raidPokemon := raidInfo.GetRaidPokemon()
			raidPokemonDisplay := raidPokemon.GetPokemonDisplay()
			gym.SetRaidPokemonId(null.IntFrom(int64(raidPokemon.GetPokemonId())))
			gym.SetRaidPokemonMove1(null.IntFrom(int64(raidPokemon.GetMove1())))
			gym.SetRaidPokemonMove2(null.IntFrom(int64(raidPokemon.GetMove2())))
			gym.SetRaidPokemonForm(null.IntFrom(int64(raidPokemonDisplay.GetForm())))
			gym.SetRaidPokemonAlignment(null.IntFrom(int64(raidPokemonDisplay.GetAlignment())))
			gym.SetRaidPokemonCp(null.IntFrom(int64(raidPokemon.GetCp())))
			gym.SetRaidPokemonGender(null.IntFrom(int64(raidPokemonDisplay.GetGender())))
			gym.SetRaidPokemonCostume(null.IntFrom(int64(raidPokemonDisplay.GetCostume())))
			gym.SetRaidPokemonEvolution(null.IntFrom(int64(raidPokemonDisplay.GetCurrentTempEvolution())))
		} else {
			gym.SetRaidPokemonId(null.IntFrom(0))
			gym.SetRaidPokemonMove1(null.IntFrom(0))
			gym.SetRaidPokemonMove2(null.IntFrom(0))
			gym.SetRaidPokemonForm(null.IntFrom(0))
			gym.SetRaidPokemonAlignment(null.IntFrom(0))
			gym.SetRaidPokemonCp(null.IntFrom(0))
			gym.SetRaidPokemonGender(null.IntFrom(0))
			gym.SetRaidPokemonCostume(null.IntFrom(0))
			gym.SetRaidPokemonEvolution(null.IntFrom(0))
		}

		gym.SetRaidIsExclusive(null.IntFrom(0)) //null.IntFrom(util.BoolToInt[int64](fortData.RaidInfo.IsExclusive))
	}

	if cellId != 0 {
		gym.SetCellId(null.IntFrom(int64(cellId)))
	}

	if gym.Deleted {
		gym.SetDeleted(false)
		log.Warnf("Cleared Gym with id '%s' is found again in GMO, therefore un-deleted", gym.Id)
		// Restore in fort tracker if enabled
		if fortTracker != nil {
			fortTracker.RestoreFort(gym.Id, cellId, true, timestampMs)
		}
	}

	return gym
}

func (gym *Gym) updateGymFromFortProto(fortData pogoshim.FortDetailsOutProto) *Gym {
	gym.SetId(fortData.GetId())
	gym.SetLat(fortData.GetLatitude())
	gym.SetLon(fortData.GetLongitude())
	if imageUrls := fortData.GetImageUrl(); imageUrls.Len() > 0 {
		gym.SetUrl(null.StringFrom(imageUrls.At(0).String()))
	}
	gym.SetName(null.StringFrom(fortData.GetName()))

	return gym
}

func (gym *Gym) updateGymFromGymInfoOutProto(gymData pogoshim.GymGetInfoOutProto) *Gym {
	status := gymData.GetGymStatusAndDefenders()
	fortProto := status.GetPokemonFortProto()
	gym.SetId(fortProto.GetFortId())
	gym.SetLat(fortProto.GetLatitude())
	gym.SetLon(fortProto.GetLongitude())

	// This will have gym defenders in it...
	if url := gymData.GetUrl(); url != "" {
		gym.SetUrl(null.StringFrom(url))
	}
	gym.SetName(null.StringFrom(gymData.GetName()))

	if gymData.GetDescription() == "" {
		gym.SetDescription(null.NewString("", false))
	} else {
		gym.SetDescription(null.StringFrom(gymData.GetDescription()))
	}

	if gymData.HasGymStatusAndDefenders() {
		type pokemonGymDefender struct {
			PokemonId             int                `json:"pokemon_id,omitempty"`
			Form                  int                `json:"form,omitempty"`
			Costume               int                `json:"costume,omitempty"`
			Gender                int                `json:"gender"`
			Shiny                 bool               `json:"shiny,omitempty"`
			TempEvolution         int                `json:"temp_evolution,omitempty"`
			TempEvolutionFinishMs int64              `json:"temp_evolution_finish_ms,omitempty"`
			Alignment             int                `json:"alignment,omitempty"`
			Badge                 int                `json:"badge,omitempty"`
			Background            *int64             `json:"background,omitempty"`
			DeployedMs            int64              `json:"deployed_ms,omitempty"`
			DeployedTime          int64              `json:"deployed_time,omitempty"`
			BattlesWon            int32              `json:"battles_won"`
			BattlesLost           int32              `json:"battles_lost"`
			TimesFed              int32              `json:"times_fed"`
			MotivationNow         util.RoundedFloat4 `json:"motivation_now"`
			CpNow                 int32              `json:"cp_now"`
			CpWhenDeployed        int32              `json:"cp_when_deployed"`
		}

		var defenders []pokemonGymDefender
		now := time.Now()
		for protoDefender := range status.GetGymDefender().All() {
			motivatedPokemon := protoDefender.GetMotivatedPokemon()
			pokemonDisplay := motivatedPokemon.GetPokemon().GetPokemonDisplay()
			deploymentTotals := protoDefender.GetDeploymentTotals()
			defender := pokemonGymDefender{
				DeployedMs: deploymentTotals.GetDeploymentDurationMs(),
				DeployedTime: now.
					Add(-1 * time.Millisecond * time.Duration(deploymentTotals.GetDeploymentDurationMs())).
					Unix(), // This will only be approximately correct
				BattlesLost:           deploymentTotals.GetBattlesLost(),
				BattlesWon:            deploymentTotals.GetBattlesWon(),
				TimesFed:              deploymentTotals.GetTimesFed(),
				PokemonId:             int(motivatedPokemon.GetPokemon().GetPokemonId()),
				Form:                  int(pokemonDisplay.GetForm()),
				Costume:               int(pokemonDisplay.GetCostume()),
				Gender:                int(pokemonDisplay.GetGender()),
				TempEvolution:         int(pokemonDisplay.GetCurrentTempEvolution()),
				TempEvolutionFinishMs: pokemonDisplay.GetTemporaryEvolutionFinishMs(),
				Alignment:             int(pokemonDisplay.GetAlignment()),
				Badge:                 int(pokemonDisplay.GetPokemonBadge()),
				Background:            util.ExtractBackgroundFromDisplayShim(pokemonDisplay),
				Shiny:                 pokemonDisplay.GetShiny(),
				MotivationNow:         util.RoundedFloat4(motivatedPokemon.GetMotivationNow()),
				CpNow:                 motivatedPokemon.GetCpNow(),
				CpWhenDeployed:        motivatedPokemon.GetCpWhenDeployed(),
			}
			defenders = append(defenders, defender)
		}
		bDefenders, _ := json.Marshal(defenders)
		gym.SetDefenders(null.StringFrom(string(bDefenders)))

		if status.HasPokemonFortProto() {
			gym.updateGymFromFort(status.GetPokemonFortProto(), 0, 0)
		}
	}

	return gym
}

func (gym *Gym) updateGymFromMapFortSummary(fortData mapFortSummary, skipName bool) *Gym {
	gym.SetId(fortData.Id)
	gym.SetLat(fortData.Latitude)
	gym.SetLon(fortData.Longitude)

	if fortData.ImageUrl != "" {
		gym.SetUrl(null.StringFrom(fortData.ImageUrl))
	}
	if !skipName {
		gym.SetName(null.StringFrom(fortData.Name))
	}

	if gym.Deleted {
		log.Debugf("Cleared Gym with id '%s' is found again in GMF, therefore kept deleted", gym.Id)
	}

	return gym
}

func (gym *Gym) updateGymFromRsvpProto(fortData *pogo.GetEventRsvpsOutProto) *Gym {
	type rsvpTimeslot struct {
		Timeslot   int64 `json:"timeslot"`
		GoingCount int32 `json:"going_count"`
		MaybeCount int32 `json:"maybe_count"`
	}

	timeslots := make([]rsvpTimeslot, 0)

	for _, timeslot := range fortData.RsvpTimeslots {
		if timeslot.GoingCount > 0 || timeslot.MaybeCount > 0 {
			timeslots = append(timeslots, rsvpTimeslot{
				Timeslot:   timeslot.TimeSlot,
				GoingCount: timeslot.GoingCount,
				MaybeCount: timeslot.MaybeCount,
			})
		}
	}

	if len(timeslots) == 0 {
		gym.SetRsvps(null.NewString("", false))
	} else {
		slices.SortFunc(timeslots, func(a, b rsvpTimeslot) int {
			return cmp.Compare(a.Timeslot, b.Timeslot)
		})

		bRsvps, _ := json.Marshal(timeslots)
		gym.SetRsvps(null.StringFrom(string(bRsvps)))
	}

	return gym
}
