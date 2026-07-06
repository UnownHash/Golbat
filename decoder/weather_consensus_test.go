package decoder

import (
	"reflect"
	"testing"
)

// TestApplyObservationConsensusAndRetention locks in the pre-refactor
// consensus semantics through the weatherObservation value-struct boundary
// fix: three accounts vote across two conditions, and the publish decision
// (initial publish / no-op tie / leader flip) must be unchanged. It also
// locks in that the *full* observation (not just the vote-tallying
// GameplayCondition) survives the map[int32]weatherObservation retention —
// the whole point of the boundary fix is that display-level fields must not
// be silently dropped when we stop retaining the proto pointer.
func TestApplyObservationConsensusAndRetention(t *testing.T) {
	state := &WeatherConsensusState{}
	state.reset(100)

	// accountA is the first ever vote for condition 1: must publish
	// immediately, carrying every display field of its own observation.
	obsA := weatherObservation{
		S2CellId:           555,
		GameplayCondition:  1,
		WindDirection:      45,
		CloudLevel:         2,
		RainLevel:          1,
		WindLevel:          0,
		SnowLevel:          0,
		FogLevel:           0,
		SpecialEffectLevel: 0,
		Alerts:             []weatherAlert{{Severity: 1, WarnWeather: false}},
	}
	publish, obs, havePublish := state.applyObservation(100, "accountA", obsA)
	if !publish || !havePublish {
		t.Fatalf("first-ever vote must publish: publish=%v havePublish=%v", publish, havePublish)
	}
	if !reflect.DeepEqual(obs, obsA) {
		t.Errorf("published observation = %+v, want %+v (verbatim first observation)", obs, obsA)
	}

	// accountB votes for condition 2: ties the count 1-1, so the leader
	// (condition 1) does not change and nothing should publish.
	obsB := weatherObservation{S2CellId: 555, GameplayCondition: 2, CloudLevel: 5}
	publish, _, _ = state.applyObservation(100, "accountB", obsB)
	if publish {
		t.Errorf("tied vote must not publish a leader change")
	}

	// accountC also votes for condition 2: it now leads 2-1, strictly ahead
	// of condition 1, so this must publish a leader flip carrying accountC's
	// own display levels (the winning condition's most recent observation).
	obsC := weatherObservation{S2CellId: 555, GameplayCondition: 2, CloudLevel: 9, RainLevel: 3}
	publish, obs, havePublish = state.applyObservation(100, "accountC", obsC)
	if !publish || !havePublish {
		t.Fatalf("strict leader flip must publish: publish=%v havePublish=%v", publish, havePublish)
	}
	if obs.GameplayCondition != 2 {
		t.Errorf("published GameplayCondition = %d, want 2", obs.GameplayCondition)
	}
	if obs.CloudLevel != 9 || obs.RainLevel != 3 {
		t.Errorf("published display levels = (cloud=%d rain=%d), want (cloud=9 rain=3) — "+
			"the winning condition's last observation must survive the value-struct retention",
			obs.CloudLevel, obs.RainLevel)
	}

	// A further vote that doesn't change the leader must not publish again.
	obsA2 := weatherObservation{S2CellId: 555, GameplayCondition: 1, CloudLevel: 20}
	publish, _, _ = state.applyObservation(100, "accountA", obsA2)
	if publish {
		t.Errorf("re-vote for the non-leading condition must not publish")
	}
}

// TestApplyObservationNewHourResetsState confirms the hour-key rollover
// still resets vote tallies before applying the new observation (unchanged
// by the value-struct refactor).
func TestApplyObservationNewHourResetsState(t *testing.T) {
	state := &WeatherConsensusState{}
	state.reset(100)

	obs1 := weatherObservation{GameplayCondition: 1}
	if publish, _, _ := state.applyObservation(100, "accountA", obs1); !publish {
		t.Fatal("first vote of the hour must publish")
	}

	// Next hour: a single vote for a new condition must publish again
	// immediately, exactly like a fresh cell.
	obs2 := weatherObservation{GameplayCondition: 2, CloudLevel: 7}
	publish, obs, havePublish := state.applyObservation(101, "accountA", obs2)
	if !publish || !havePublish {
		t.Fatalf("first vote of a new hour must publish: publish=%v havePublish=%v", publish, havePublish)
	}
	if obs.CloudLevel != 7 {
		t.Errorf("CloudLevel = %d, want 7", obs.CloudLevel)
	}

	// A stale observation for the previous hour must be ignored entirely.
	publish, _, havePublish = state.applyObservation(100, "accountB", weatherObservation{GameplayCondition: 1})
	if publish || havePublish {
		t.Errorf("stale hourKey must never publish: publish=%v havePublish=%v", publish, havePublish)
	}
}
