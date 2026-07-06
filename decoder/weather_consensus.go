package decoder

import (
	"golbat/pogoshim"

	"golbat/ottercache"
)

// weatherAlert is the Golbat-owned copy of a single WeatherAlertProto entry.
type weatherAlert struct {
	Severity    int32
	WarnWeather bool
}

// weatherObservation is the Golbat-owned copy of the ClientWeatherProto
// fields consensus and weather updates read. Retaining decoded protos is
// forbidden: hyperpb messages must not outlive their arena.
type weatherObservation struct {
	S2CellId           int64
	GameplayCondition  int32
	WindDirection      int32
	CloudLevel         int32
	RainLevel          int32
	WindLevel          int32
	SnowLevel          int32
	FogLevel           int32
	SpecialEffectLevel int32
	Alerts             []weatherAlert
}

// weatherObservationFromShim copies the fields consensus/weather updates
// need out of a ClientWeatherProto shim into a value type that safely
// outlives the shim's backing arena (hyperpb messages must not be retained
// past the decode call that produced them).
func weatherObservationFromShim(w pogoshim.ClientWeatherProto) weatherObservation {
	display := w.GetDisplayWeather()
	obs := weatherObservation{
		S2CellId:           w.GetS2CellId(),
		GameplayCondition:  int32(w.GetGameplayWeather().GetGameplayCondition()),
		WindDirection:      display.GetWindDirection(),
		CloudLevel:         int32(display.GetCloudLevel()),
		RainLevel:          int32(display.GetRainLevel()),
		WindLevel:          int32(display.GetWindLevel()),
		SnowLevel:          int32(display.GetSnowLevel()),
		FogLevel:           int32(display.GetFogLevel()),
		SpecialEffectLevel: int32(display.GetSpecialEffectLevel()),
	}
	if alerts := w.GetAlerts(); alerts.Len() > 0 {
		obs.Alerts = make([]weatherAlert, alerts.Len())
		for i := range obs.Alerts {
			alert := alerts.At(i)
			obs.Alerts[i] = weatherAlert{
				Severity:    int32(alert.GetSeverity()),
				WarnWeather: alert.GetWarnWeather(),
			}
		}
	}
	return obs
}

type WeatherConsensusState struct {
	HourKey            int64
	Published          bool
	PublishedCondition int32
	VotesByAccount     map[string]int32
	CountsByCondition  map[int32]int
	LastObsByCondition map[int32]weatherObservation
}

func (state *WeatherConsensusState) reset(hourKey int64) {
	state.HourKey = hourKey
	state.Published = false
	state.PublishedCondition = 0
	state.VotesByAccount = make(map[string]int32)
	state.CountsByCondition = make(map[int32]int)
	state.LastObsByCondition = make(map[int32]weatherObservation)
}

func getWeatherConsensusState(cellId int64, hourKey int64) *WeatherConsensusState {
	if weatherConsensusCache == nil {
		return nil
	}
	if state, ok := weatherConsensusCache.Get(cellId); ok {
		if hourKey > state.HourKey {
			state.reset(hourKey)
		}
		// No re-Set needed: this is a touch-on-hit cache, the Get above
		// already re-armed the TTL, and state is a shared pointer.
		return state
	}
	state := &WeatherConsensusState{}
	state.reset(hourKey)
	weatherConsensusCache.Set(cellId, state, ottercache.DefaultTTL)
	return state
}

// applyObservation records a weather observation and decides whether to
// publish an update. Returns publish=true with the most recent observation
// recorded for the winning condition; havePublish reports whether that
// observation was actually found in the retention map (replaces the old
// nil-pointer publish signal now that LastObsByCondition holds values, not
// pointers — callers should fall back to the observation just passed in
// when havePublish is false). When publish is false the other two return
// values are zero.
func (state *WeatherConsensusState) applyObservation(hourKey int64, account string, obs weatherObservation) (publish bool, publishedObs weatherObservation, havePublish bool) {
	if state == nil {
		return false, weatherObservation{}, false
	}
	if hourKey < state.HourKey {
		return false, weatherObservation{}, false
	}
	if hourKey > state.HourKey {
		state.reset(hourKey)
	}

	condition := obs.GameplayCondition
	state.LastObsByCondition[condition] = obs

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
		return false, weatherObservation{}, false
	}
	if !state.Published {
		state.Published = true
		state.PublishedCondition = bestCondition
		winningObs, ok := state.LastObsByCondition[bestCondition]
		return true, winningObs, ok
	}
	if bestCondition == state.PublishedCondition {
		return false, weatherObservation{}, false
	}
	// Only publish a change when the leader is strictly ahead (no tie).
	if bestCount > runnerUpCount {
		state.PublishedCondition = bestCondition
		winningObs, ok := state.LastObsByCondition[bestCondition]
		return true, winningObs, ok
	}
	return false, weatherObservation{}, false
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
