package encounter

import (
	"testing"

	"golbat/derive/javarandom"
)

func TestMiniorCoreRollIsFirstFloatOfRawSeed(t *testing.T) {
	for _, id := range []uint64{0, 1, 977401, 1 << 47, 0xFFFFFFFFFFFFFFFF} {
		if got, want := MiniorCoreRoll(id), javarandom.New(id).Float(); got != want {
			t.Errorf("id %d: roll %v, want %v", id, got, want)
		}
	}
}

func TestMiniorCoreFormPicksByCumulativeWeight(t *testing.T) {
	const green, yellow = 2709, 2714
	table := []CoreWeight{{green, 5}, {yellow, 95}}
	for id := uint64(0); id < 20000; id++ {
		form, ok := MiniorCoreForm(id, table)
		if !ok {
			t.Fatalf("id %d: not ok", id)
		}
		want := uint16(yellow)
		if MiniorCoreRoll(id) < 0.05 {
			want = green
		}
		if form != want {
			t.Fatalf("id %d: roll %v gave %d, want %d", id, MiniorCoreRoll(id), form, want)
		}
	}
}

func TestMiniorCoreFormRejectsUnusableTables(t *testing.T) {
	for name, table := range map[string][]CoreWeight{
		"nil":      nil,
		"zero":     {{2709, 0}},
		"negative": {{2709, -1}, {2714, 0}},
	} {
		if _, ok := MiniorCoreForm(1, table); ok {
			t.Errorf("%s: ok for an unusable table", name)
		}
	}
}

func TestMiniorCoreFormSkipsNonPositiveWeights(t *testing.T) {
	table := []CoreWeight{{2709, 0}, {2714, 1}, {2710, -3}}
	for id := uint64(0); id < 1000; id++ {
		if form, ok := MiniorCoreForm(id, table); !ok || form != 2714 {
			t.Fatalf("id %d: got %d, %v", id, form, ok)
		}
	}
}
