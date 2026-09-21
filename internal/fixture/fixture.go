// Package fixture builds the fixed demonstration dataset used by the
// acceptance flow: PRBS + single pulse + step inputs, a clock-jitter block,
// an output-saturation block and a few missing output samples.
package fixture

import (
	"math"

	"dynid/internal/ident"
)

// lcg is a deterministic linear-congruential generator (Numerical Recipes) so
// the fixture is byte-stable across machines and Go versions.
type lcg struct{ state uint64 }

func (g *lcg) next() uint64 {
	g.state = g.state*6364136223846793005 + 1442695040888963407
	return g.state
}
func (g *lcg) float64() float64 {
	return float64(g.next()>>11) / float64(uint64(1)<<53)
}

// Spec describes the generated fixture in engineering units.
type Spec struct {
	NominalDt float64 // nominal sampling time (seconds)
	DelayS    float64 // true dead time
	TauS      float64 // true first-order time constant
	Gain      float64 // true DC gain
	SatLevel  float64 // |y| clipped beyond this level
}

// DefaultSpec is the fixed acceptance scenario.
func DefaultSpec() Spec {
	return Spec{
		NominalDt: 1.0,
		DelayS:    3.0,
		TauS:      8.0,
		Gain:      2.0,
		SatLevel:  2.0,
	}
}

const (
	nSamples    = 480
	jitterStart = 150
	jitterEnd   = 230
	satStart    = 330
	satEnd      = 410
	stepStart   = 330
	pulseAt     = 100
)

// missingIdx are the deliberately dropped output samples.
var missingIdx = map[int]bool{60: true, 61: true, 260: true}

// Build returns the fixed raw samples in time order.
func Build(sp Spec) []ident.Sample {
	g := &lcg{state: 0xD1_71D_EE5}
	dt := sp.NominalDt
	a := math.Exp(-dt / sp.TauS) // discrete pole on the nominal grid
	delaySteps := int(math.Round(sp.DelayS / dt))

	// Input design: PRBS holding 4..8 samples, a single +1 pulse, then a step.
	u := make([]float64, nSamples)
	for k := 0; k < stepStart; {
		hold := 4 + int(g.float64()*5) // 4..8
		level := 1.0
		if g.float64() < 0.5 {
			level = -1
		}
		for h := 0; h < hold && k < stepStart; h++ {
			u[k] = level
			k++
		}
	}
	u[pulseAt] = 1.0 // single-pulse excitation on top of PRBS
	for k := stepStart; k < nSamples; k++ {
		u[k] = 1.0 // step
	}

	// Clock jitter: one contiguous block with ±35% sampling jitter.
	t := make([]float64, nSamples)
	tt := 0.0
	for k := 0; k < nSamples; k++ {
		if k == jitterStart {
			tt = t[k-1] + dt // re-anchor at the block entry
		}
		t[k] = tt
		if k < nSamples-1 {
			gap := dt
			if k >= jitterStart && k < jitterEnd {
				gap = dt * (0.65 + 0.7*g.float64()) // 0.65..1.35
			}
			tt += gap
		}
	}

	// Simulate the delayed first-order plant on a fine internal timeline.
	y := make([]float64, nSamples)
	prev := 0.0
	for k := 0; k < nSamples; k++ {
		ud := 0.0
		if ki := k - delaySteps; ki >= 0 {
			ud = u[ki]
		}
		v := a*prev + sp.Gain*(1-a)*ud
		y[k] = v
		prev = v
	}
	// Small measurement noise (deterministic, ~1% of signal scale).
	for k := range y {
		y[k] += 0.012 * (g.float64() - 0.5) * 2
	}

	out := make([]ident.Sample, nSamples)
	for k := 0; k < nSamples; k++ {
		sat := k >= satStart && k < satEnd
		s := ident.Sample{T: round3(t[k]), U: round4(u[k]), Sat: sat}
		if !missingIdx[k] {
			val := y[k]
			if sat && val > sp.SatLevel {
				val = sp.SatLevel
			}
			if sat && val < -sp.SatLevel {
				val = -sp.SatLevel
			}
			val = round4(val)
			s.Y = &val
		}
		out[k] = s
	}
	return out
}

func round3(x float64) float64 { return math.Round(x*1000) / 1000 }
func round4(x float64) float64 { return math.Round(x*10000) / 10000 }

// Segments returns suggested estimation/validation windows. The estimation
// window avoids the saturation block; validation includes fresh data.
func Segments() (est, val ident.Segment) {
	return ident.Segment{Start: 20, End: 140}, ident.Segment{Start: 240, End: 325}
}

// OverlappingSegments deliberately overlap; the acceptance test uses them to
// prove the publish blocker fires.
func OverlappingSegments() (est, val ident.Segment) {
	return ident.Segment{Start: 20, End: 140}, ident.Segment{Start: 120, End: 200}
}
