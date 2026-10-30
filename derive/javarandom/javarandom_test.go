package javarandom

import (
	"math"
	"testing"
)

// Golden vectors computed from the reference algorithm. Bound 100 is not a
// power of two, so this exercises the general (modulo) path.
func TestKnownJavaSequence(t *testing.T) {
	r := New(42)
	for i, want := range []int32{30, 63, 48, 84, 70} {
		if got := r.Int(100); got != want {
			t.Fatalf("draw %d: got %d want %d", i, got, want)
		}
	}
}

// The two Int paths must be pinned separately, because a port that routes
// every bound through one of them still passes range checks and still looks
// plausible — it is only wrong on specific seeds.
func TestIntPowerOfTwoPath(t *testing.T) {
	r := New(42)
	for i, want := range []int32{11, 0, 10} {
		if got := r.Int(16); got != want {
			t.Fatalf("bound 16 draw %d: got %d want %d", i, got, want)
		}
	}
}

func TestIntModuloPath(t *testing.T) {
	r := New(12345)
	for i, want := range []int32{7, 4, 9} {
		if got := r.Int(12); got != want {
			t.Fatalf("bound 12 draw %d: got %d want %d", i, got, want)
		}
	}
}

func TestIntBoundPathsAgreeOnRange(t *testing.T) {
	for _, bound := range []int32{12, 16, 30} {
		r := New(12345)
		for i := 0; i < 10000; i++ {
			if v := r.Int(bound); v < 0 || v >= bound {
				t.Fatalf("bound %d draw %d out of range: %d", bound, i, v)
			}
		}
	}
}

func TestNextFloatIsUnitInterval(t *testing.T) {
	r := New(7)
	for i := 0; i < 1000; i++ {
		if v := r.Float(); v < 0 || v >= 1 {
			t.Fatalf("draw %d out of [0,1): %v", i, v)
		}
	}
}

// Float (nextFloat: next(24)/(1<<24) as a float32) matches values computed
// independently of this package, by a Python port of the LCG from the
// specification. decoder's dittoSuspectRoll compares a Float result against
// 0.02 as float32, so a Float with the wrong bit width or rounding would move
// that boundary. Compared by exact bit pattern, so a formatting coincidence
// cannot mask a difference.
func TestFloatMatchesJavaReferenceVector(t *testing.T) {
	r := New(42)
	for i, want := range []uint32{0x3f3a419d, 0x3d5fe8a0, 0x3f2ee7bb, 0x3d445c00, 0x3e9e1078} {
		if got := math.Float32bits(r.Float()); got != want {
			t.Fatalf("draw %d: got %#08x want %#08x", i, got, want)
		}
	}
}

// The polar method consumes a variable number of advances. This pins, by
// exact bit pattern, that a Gaussian draw followed by a Float matches the
// reference implementation. Scaling Float by anything (e.g. 1-1e-6) would
// pass a range check but fails this exact comparison.
func TestGaussianThenFloatAdvancesCorrectly(t *testing.T) {
	r := New(0xDEADBEEF)

	g := math.Float64bits(r.Gaussian())
	if g != 0xbfec1295ff10fcfe {
		t.Fatalf("gaussian: got %#016x want 0xbfec1295ff10fcfe", g)
	}

	got := math.Float32bits(r.Float())
	if want := uint32(0x3f0d67b3); got != want {
		t.Fatalf("float after gaussian: got %#08x want %#08x", got, want)
	}
}

// Two successive Gaussian draws from seed 0, compared by exact bit pattern.
// The second must come from the cached second variate WITHOUT advancing the
// generator: the polar method produces two values per accepted pair, and a
// port that recomputes instead of caching burns two extra Double draws (four
// generator advances), silently desynchronizing the generator's state from
// every draw that follows.
func TestGaussianPairAndCache(t *testing.T) {
	r := New(0)

	first := math.Float64bits(r.Gaussian())
	if first != 0x3FE9AE59D1D6F861 {
		t.Fatalf("first gaussian: got %#016x want 0x3FE9AE59D1D6F861", first)
	}

	stateAfterFirst := r.state

	second := math.Float64bits(r.Gaussian())
	if second != 0xBFECD9772EB2E0C8 {
		t.Fatalf("second gaussian: got %#016x want 0xBFECD9772EB2E0C8", second)
	}
	if r.state != stateAfterFirst {
		t.Fatal("the cached second variate must not advance the generator")
	}
}

// Small bounds leave a rejection tail of roughly 8 values in 2^31, so the
// branch is effectively unreachable with them. This bound is 2^30+1, whose
// rejected tail is ~50% of the range, so the branch fires on roughly every
// other draw. Without this, an int/int64 typing mistake in the overflow test
// passes every other test in this file.
//
// An implementation whose arithmetic does not wrap — and so never rejects —
// returns 488689305 first instead of 117392763.
func TestIntRejectionBranchActuallyFires(t *testing.T) {
	const bound = int32(1<<30 + 1)

	r := New(42)
	for i, want := range []int32{117392763, 102948884, 662969970, 595021505, 196118093, 969067502} {
		if got := r.Int(bound); got != want {
			t.Fatalf("draw %d: got %d want %d", i, got, want)
		}
	}
}
