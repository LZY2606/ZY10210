package ident

import (
	"math"

	"gonum.org/v1/gonum/mat"
)

// foModel is the constrained first-order model
//
//	y[k] = a y[k-1] + b u[k-d] + c,   -1 < a < 1 (stable pole)
//
// "Constrained" means the pole is forced inside the unit circle; the gain is
// otherwise free (b not sign-restricted).
type foModel struct {
	a, b, c float64
}

func fitFirstOrder(y, ud []float64, mask []bool) (*foModel, string) {
	type pt struct {
		a   float64
		row []float64
		rhs float64
	}
	var pts []pt
	for k := 1; k < len(y); k++ {
		if !mask[k] || math.IsNaN(y[k]) || math.IsNaN(y[k-1]) || math.IsNaN(ud[k]) {
			continue
		}
		pts = append(pts, pt{a: y[k-1], row: []float64{ud[k], 1}, rhs: y[k]})
	}
	if len(pts) < 3 {
		return nil, "有效样本不足，无法拟合一阶模型"
	}
	// Unconstrained reference pole.
	A := mat.NewDense(len(pts), 3, nil)
	for i, p := range pts {
		A.Set(i, 0, p.a)
		A.Set(i, 1, p.row[0])
		A.Set(i, 2, p.row[1])
	}
	rhs := make([]float64, len(pts))
	for i, p := range pts {
		rhs[i] = p.rhs
	}
	un, ok := lstsq(A, rhs, 0)
	bestA := 0.5
	note := ""
	if ok && un[0] > -1 && un[0] < 1 {
		bestA = un[0]
	} else {
		note = "无约束极点不稳定，已在单位圆内搜索最接近的稳定极点"
		if ok {
			switch {
			case un[0] >= 1:
				bestA = 0.999
			case un[0] <= -1:
				bestA = -0.999
			}
		}
	}
	// Local refinement: fine grid around bestA, for each pole compute the
	// conditional least squares for (b, c).
	var best *foModel
	bestSSE := math.Inf(1)
	lo, hi := bestA-0.05, bestA+0.05
	if lo < -0.999 {
		lo = -0.999
	}
	if hi > 0.999 {
		hi = 0.999
	}
	const gridN = 2001
	for g := 0; g < gridN; g++ {
		a := lo + (hi-lo)*float64(g)/float64(gridN-1)
		B := mat.NewDense(len(pts), 2, nil)
		resid := make([]float64, len(pts))
		for i, pp := range pts {
			B.Set(i, 0, pp.row[0])
			B.Set(i, 1, pp.row[1])
			resid[i] = pp.rhs - a*pp.a
		}
		bc, ok2 := lstsq(B, resid, 0)
		if !ok2 {
			continue
		}
		sse := 0.0
		for _, pp := range pts {
			e := pp.rhs - a*pp.a - bc[0]*pp.row[0] - bc[1]
			sse += e * e
		}
		if sse < bestSSE {
			bestSSE = sse
			best = &foModel{a: a, b: bc[0], c: bc[1]}
		}
	}
	if best == nil {
		return nil, "受限一阶模型求解失败"
	}
	return best, note
}

func (m *foModel) oneStep(y, ud []float64, mask []bool) []float64 {
	out := make([]float64, len(y))
	for k := range out {
		out[k] = math.NaN()
	}
	for k := 1; k < len(y); k++ {
		if mask[k] && !math.IsNaN(y[k]) && !math.IsNaN(y[k-1]) {
			out[k] = m.a*y[k-1] + m.b*ud[k] + m.c
		}
	}
	return out
}

func (m *foModel) simulate(y, ud []float64, mask []bool, startIdx int) []float64 {
	n := len(y)
	sim := make([]float64, n)
	for k := range sim {
		sim[k] = math.NaN()
	}
	ym := make([]float64, n)
	copy(ym, y)
	for k := startIdx; k < n; k++ {
		if k-1 < 0 || !mask[k] || math.IsNaN(ym[k-1]) {
			continue
		}
		v := m.a*ym[k-1] + m.b*ud[k] + m.c
		sim[k] = v
		ym[k] = v
	}
	return sim
}

func (m *foModel) nPars() int   { return 3 }
func (m *foModel) stable() bool { return m.a > -1 && m.a < 1 }

func (m *foModel) params(dt float64) Parameters {
	gain := m.b / (1 - m.a)
	tc := math.NaN()
	if m.a > 0 && m.a < 1 {
		tc = -dt / math.Log(m.a)
	}
	var tcs []float64
	if !math.IsNaN(tc) {
		tcs = []float64{tc}
	}
	return Parameters{
		Kind: ModelFirstOrder, Order: 1,
		Poles:      []Pole{{Re: m.a, Abs: math.Abs(m.a)}},
		TimeConstS: tcs,
		Gain:       &gain,
		AR:         []float64{m.a}, MA: []float64{m.b}, Intercept: m.c,
		Detail: "受限一阶：稳定极点约束 |a|<1，tau=-T/ln(a)",
	}
}
