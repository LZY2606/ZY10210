package ident

import "math"

// fittedModel is the common interface of every candidate family after fitting
// on the preprocessed estimation data. All signals are in processed units.
type fittedModel interface {
	// oneStep computes the one-step-ahead prediction over indices in mask
	// (NaNs where undetermined); yShifted uses the delay-aligned input array.
	oneStep(y, ud []float64, mask []bool) []float64
	// simulate runs a free simulation initialized from observed history.
	simulate(y, ud []float64, mask []bool, startIdx int) []float64
	params(dt float64) Parameters
	nPars() int
	stable() bool
}

// shiftInput returns the input delayed by d samples: out[k] = u[k-d].
func shiftInput(u []float64, d int) []float64 {
	out := make([]float64, len(u))
	for k := range u {
		if k-d >= 0 {
			out[k] = u[k-d]
		}
	}
	return out
}

// delaySteps converts a dead time in seconds to a whole number of grid steps.
// A half-sample rounds to the nearest step (documented in the README).
func delaySteps(delayS, dt float64) int {
	if delayS <= 0 {
		return 0
	}
	return int(math.Round(delayS / dt))
}

// residualSeries extracts residuals r=y-yhat wherever mask and finite y hold.
func residualSeries(y, yhat []float64, mask []bool) []float64 {
	var r []float64
	for i := range y {
		if mask[i] && !math.IsNaN(y[i]) && !math.IsNaN(yhat[i]) {
			r = append(r, y[i]-yhat[i])
		}
	}
	return r
}

// contiguous applies diagnostics only over contiguous valid stretches;
// correlation statistics across a gap would otherwise mix sample distances.
func contiguousSlices(mask []bool, y, yhat, u []float64) (rr, uu [][]float64) {
	n := len(mask)
	i := 0
	for i < n {
		if !mask[i] || math.IsNaN(y[i]) || math.IsNaN(yhat[i]) {
			i++
			continue
		}
		j := i
		for j < n && mask[j] && !math.IsNaN(y[j]) && !math.IsNaN(yhat[j]) {
			j++
		}
		var r1, u1 []float64
		for k := i; k < j; k++ {
			r1 = append(r1, y[k]-yhat[k])
			u1 = append(u1, u[k])
		}
		if len(r1) >= 8 {
			rr = append(rr, r1)
			uu = append(uu, u1)
		}
		i = j
	}
	return rr, uu
}
