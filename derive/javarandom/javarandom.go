// Package javarandom is a bit-exact port of java.util.Random, sufficient for
// the operations this service needs. Results match the reference
// implementation exactly for every method here.
package javarandom

import "math"

const (
	multiplier = 0x5DEECE66D
	addend     = 0xB
	mask       = (1 << 48) - 1
)

type Random struct {
	state         uint64
	haveNextGauss bool
	nextGauss     float64
}

func New(seed uint64) *Random {
	return &Random{state: (seed ^ multiplier) & mask}
}

func (r *Random) Next(bits uint) uint32 {
	r.state = (r.state*multiplier + addend) & mask
	return uint32(r.state >> (48 - bits))
}

// Int returns a value in [0, bound). Powers of two use the multiply-shift
// path; every other bound uses the rejection loop, whose test is evaluated in
// signed 32-bit arithmetic.
func (r *Random) Int(bound int32) int32 {
	if bound <= 0 {
		panic("javarandom: bound must be positive")
	}
	if bound&(bound-1) == 0 {
		return int32((int64(bound) * int64(r.Next(31))) >> 31)
	}
	for {
		bits := int32(r.Next(31))
		val := bits % bound
		if bits-val+(bound-1) >= 0 {
			return val
		}
	}
}

func (r *Random) Float() float32 {
	return float32(r.Next(24)) / (1 << 24)
}

func (r *Random) Double() float64 {
	hi := int64(r.Next(26)) << 27
	lo := int64(r.Next(27))
	return float64(hi+lo) / (1 << 53)
}

// Gaussian uses the polar method: two Double draws (four generator advances)
// per attempt, retrying while the radius falls outside the unit circle, and
// caching the second variate.
func (r *Random) Gaussian() float64 {
	if r.haveNextGauss {
		r.haveNextGauss = false
		return r.nextGauss
	}
	for {
		v1 := 2*r.Double() - 1
		v2 := 2*r.Double() - 1
		s := v1*v1 + v2*v2
		if s >= 1 || s == 0 {
			continue
		}
		m := math.Sqrt(-2 * math.Log(s) / s)
		r.nextGauss = v2 * m
		r.haveNextGauss = true
		return v1 * m
	}
}
