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

func (state *WeatherConsensusState) applyObservation(hourKey int64, account string, weatherProto *pogo.ClientWeatherProto) (bool, int32, *pogo.ClientWeatherProto) {
	if state == nil || weatherProto == nil {
		return false, 0, nil
	}
	if hourKey < state.HourKey {
		return false, 0, nil
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

	bestCondition, bestCount, secondCount := state.bestCounts()
	if bestCount == 0 {
		return false, 0, nil
	}
	if !state.Published {
		state.Published = true
		state.PublishedCondition = bestCondition
		return true, bestCondition, state.LastObsByCondition[bestCondition]
	}
	if bestCondition == state.PublishedCondition {
		return false, 0, nil
	}
	if bestCount > secondCount {
		state.PublishedCondition = bestCondition
		return true, bestCondition, state.LastObsByCondition[bestCondition]
	}
	return false, 0, nil
}

func (state *WeatherConsensusState) bestCounts() (int32, int, int) {
	var bestCondition int32
	bestCount := 0
	secondCount := 0
	for condition, count := range state.CountsByCondition {
		if count > bestCount {
			secondCount = bestCount
			bestCount = count
			bestCondition = condition
			continue
		}
		if count > secondCount {
			secondCount = count
		}
	}
	return bestCondition, bestCount, secondCount
}
