package main

import (
	"math"
	"os"
	"path/filepath"
	"runtime/metrics"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"protobench/pogo"
)

func TestRunSmoke(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "ENCOUNTER"), 0o755); err != nil {
		t.Fatal(err)
	}
	enc := pogo.EncounterOutProto_builder{
		Pokemon: pogo.WildPokemonProto_builder{EncounterId: 9}.Build(),
	}.Build()
	raw, err := proto.Marshal(enc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ENCOUNTER", "1_10.bin"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := run(runConfig{
		corpusDir: dir,
		workers:   4,
		duration:  200 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.decodes == 0 {
		t.Fatal("no decodes performed")
	}
	if rep.gcCPUShare < 0 || rep.gcCPUShare > 1 {
		t.Fatalf("gcCPUShare out of range: %v", rep.gcCPUShare)
	}
}

// TestHistPercentileSingleSampleMidBucket guards against the truncating
// target computation (uint64(q * float64(total))) that made any percentile
// of a single-sample histogram resolve to the first bucket regardless of
// where the sample actually landed: with total==1, both q=0.50 and q=0.99
// truncate to target 0, and cum >= 0 is trivially true on the very first
// bucket.
func TestHistPercentileSingleSampleMidBucket(t *testing.T) {
	buckets := []float64{0, 0.001, 0.002, 0.003, 0.004}
	before := &metrics.Float64Histogram{
		Counts:  []uint64{0, 0, 0, 0},
		Buckets: buckets,
	}
	// The one sample falls in bucket index 1 ([0.001, 0.002)), not bucket 0.
	after := &metrics.Float64Histogram{
		Counts:  []uint64{0, 1, 0, 0},
		Buckets: buckets,
	}
	want := time.Duration(0.002 * float64(time.Second)) // bucket 1's upper edge
	if got := histPercentile(before, after, 0.50); got != want {
		t.Errorf("p50 = %v, want %v (bucket 1's upper edge, not bucket 0's)", got, want)
	}
	if got := histPercentile(before, after, 0.99); got != want {
		t.Errorf("p99 = %v, want %v (bucket 1's upper edge, not bucket 0's)", got, want)
	}
}

// TestHistPercentileFinalInfBucket guards against converting a +Inf bucket
// boundary straight to time.Duration (implementation-defined garbage per the
// Go spec's float-to-int conversion rules). Float64Histogram documents that
// the last bucket boundary may be +Inf, so when the matching sample falls in
// that open-ended bucket, histPercentile must fall back to the bucket's
// finite lower edge instead.
//
// Buckets are {0, 0.0005, 0.001, +Inf} rather than {0, 0.001, +Inf}: with the
// latter, the pre-fix first-bucket-return bug (target truncates to 0, so
// cum >= target is trivially true at the very first bucket) happens to
// return the same boundary (0.001) as the correct answer, so it wouldn't
// have failed. With the extra 0.0005 boundary, the buggy path returns the
// first bucket's upper edge (0.0005) while the correct path returns the
// actual (final, open-ended) bucket's finite lower edge (0.001) -- distinct
// values, so this test would catch a regression of that bug.
func TestHistPercentileFinalInfBucket(t *testing.T) {
	buckets := []float64{0, 0.0005, 0.001, math.Inf(1)}
	before := &metrics.Float64Histogram{
		Counts:  []uint64{0, 0, 0},
		Buckets: buckets,
	}
	// The one sample falls in the final, open-ended bucket [0.001, +Inf).
	after := &metrics.Float64Histogram{
		Counts:  []uint64{0, 0, 1},
		Buckets: buckets,
	}
	want := time.Duration(0.001 * float64(time.Second)) // bucket's lower (finite) edge
	got := histPercentile(before, after, 0.99)
	if got != want {
		t.Errorf("p99 = %v, want %v (finite lower edge of the +Inf bucket)", got, want)
	}
}
