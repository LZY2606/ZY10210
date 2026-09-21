package ident

import (
	"math"

	"gonum.org/v1/gonum/mat"
)

// arPoles returns the poles of the recurrence
// y[k] = a1 y[k-1] + ... + an y[k-n] + ..., i.e. roots of
// z^n - a1 z^{n-1} - ... - an, via the companion-matrix eigenvalues.
func arPoles(a []float64) []complex128 {
	n := len(a)
	if n == 0 {
		return nil
	}
	c := mat.NewDense(n, n, nil)
	for j := 0; j < n; j++ {
		c.Set(0, j, a[j])
	}
	for i := 1; i < n; i++ {
		c.Set(i, i-1, 1)
	}
	var e mat.Eigen
	if !e.Factorize(c, mat.EigenNone) {
		return nil
	}
	return e.Values(nil)
}

func toPoles(z []complex128) []Pole {
	out := make([]Pole, len(z))
	for i, v := range z {
		out[i] = Pole{Re: real(v), Im: imag(v), Abs: cmag(v)}
	}
	return out
}

func cmag(z complex128) float64 {
	return math.Hypot(real(z), imag(z))
}

// poleTimeConstants converts stable positive-real poles to continuous time
// constants in seconds; complex poles are skipped in this convenience view.
func poleTimeConstants(p []Pole, dt float64) []float64 {
	var tcs []float64
	for _, q := range p {
		if math.Abs(q.Im) < 1e-9 && q.Abs < 1 && q.Re > 1e-6 {
			tcs = append(tcs, -dt/math.Log(q.Re))
		}
	}
	return tcs
}
