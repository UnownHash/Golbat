package decoder

import (
	"golbat/pogo"

	"github.com/jellydator/ttlcache/v3"
)

type WeatherConsensusState struct {
	HourKey            int64
	Published          bool
	PublishedCondition int32
	VotesByAccount     map[string]int32
	CountsByCondition  map[int32]int
	LastObsByCondition map[int32]*pogo.ClientWeatherProto
}

func (state *WeatherConsensusState) reset(hourKey int64) {
	state.HourKey = hourKey
	state.Published = false
	state.PublishedCondition = 0
	state.VotesByAccount = make(map[string]int32)
	state.CountsByCondition = make(map[int32]int)
	state.LastObsByCondition = make(map[int32]*pogo.ClientWeatherProto)
}

func getWeatherConsensusState(cellId int64, hourKey int64) *WeatherConsensusState {
	if weatherConsensusCache == nil {
		return nil
	}
	item := weatherConsensusCache.Get(cellId)
	if item != nil {
		state := item.Value()
		if hourKey > state.HourKey {
			state.reset(hourKey)
		}
		weatherConsensusCache.Set(cellId, state, ttlcache.DefaultTTL)
		return state
	}
	state := &WeatherConsensusState{}
	state.reset(hourKey)
	weatherConsensusCache.Set(cellId, state, ttlcache.DefaultTTL)
	return state
}

// applyObservation records a weather observation and decides whether to publish an update.
// Returns true with the most recent observation for the winning condition; otherwise false, nil.
func (state *WeatherConsensusState) applyObservation(hourKey int64, account string, weatherProto *pogo.ClientWeatherProto) (bool, *pogo.ClientWeatherProto) {
	if state == nil || weatherProto == nil {
		return false, nil
	}
	if hourKey < state.HourKey {
		return false, nil
	}
	if hourKey > state.HourKey {
		state.reset(hourKey)
	}

	condition := int32(weatherProto.GetGameplayWeather().GetGameplayCondition())
	state.LastObsByCondition[condition] = weatherProto

	if prevCondition, ok := state.VotesByAccount[account]; ok {
		if prevCondition != condition {
			state.CountsByCondition[prevCondition]--
			if state.CountsByCondition[prevCondition] <= 0 {
				delete(state.CountsByCondition, prevCondition)
			}
			state.VotesByAccount[account] = condition
			state.CountsByCondition[condition]++
		}
	} else {
		state.VotesByAccount[account] = condition
		state.CountsByCondition[condition]++
	}

	bestCondition, bestCount, runnerUpCount := state.bestCounts()
	if bestCount == 0 {
		return false, nil
	}
	if !state.Published {
		state.Published = true
		state.PublishedCondition = bestCondition
		return true, state.LastObsByCondition[bestCondition]
	}
	if bestCondition == state.PublishedCondition {
		return false, nil
	}
	// Only publish a change when the leader is strictly ahead (no tie).
	if bestCount > runnerUpCount {
		state.PublishedCondition = bestCondition
		return true, state.LastObsByCondition[bestCondition]
	}
	return false, nil
}

// bestCounts returns the leading condition, its vote count, and the runner-up count.
// The runner-up count equals the best count when there is a tie for first.
func (state *WeatherConsensusState) bestCounts() (int32, int, int) {
	var bestCondition int32
	bestCount := 0
	runnerUpCount := 0
	for condition, count := range state.CountsByCondition {
		switch {
		case count > bestCount:
			runnerUpCount = bestCount
			bestCount = count
			bestCondition = condition
		case count == bestCount:
			runnerUpCount = bestCount
		case count > runnerUpCount:
			runnerUpCount = count
		}
	}
	return bestCondition, bestCount, runnerUpCount
}
