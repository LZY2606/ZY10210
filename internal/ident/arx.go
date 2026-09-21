package ident

import (
	"math"

	"gonum.org/v1/gonum/mat"
)

// arxModel implements A(q) y[k] = B(q) u[k] + c + e[k] with
// A(q) = 1 + a1 q^-1 + ... + ana q^-na and na output / nb input terms.
type arxModel struct {
	na, nb int
	a      []float64 // a[0..na-1] coefficients on past y
	b      []float64 // b[0..nb-1] coefficients on past (delayed) input
	c      float64
}

type arxSpec struct {
	na, nb int
}

func fitARX(y, u []float64, mask []bool, order int) (*arxModel, string) {
	na := order
	nb := order
	if na < 1 {
		return nil, "ARX 阶次至少为 1"
	}
	start := max(na, nb-1)
	if start < 0 {
		start = 0
	}
	var rows [][]float64
	var rhs []float64
	for k := start; k < len(y); k++ {
		if !mask[k] || math.IsNaN(y[k]) || math.IsNaN(u[k]) {
			continue
		}
		ok := true
		row := make([]float64, 0, na+nb+1)
		for i := 1; i <= na; i++ {
			if math.IsNaN(y[k-i]) {
				ok = false
			}
			row = append(row, y[k-i])
		}
		for j := 0; j < nb; j++ {
			if math.IsNaN(u[k-j]) {
				ok = false
			}
			row = append(row, u[k-j])
		}
		if !ok {
			continue
		}
		row = append(row, 1)
		rows = append(rows, row)
		rhs = append(rhs, y[k])
	}
	npar := na + nb + 1
	if len(rows) < npar+2 {
		return nil, "有效样本不足，无法拟合该阶次"
	}
	A := mat.NewDense(len(rows), npar, nil)
	for i, row := range rows {
		for j, v := range row {
			A.Set(i, j, v)
		}
	}
	theta, ok := lstsq(A, rhs, 0)
	if !ok {
		return nil, "最小二乘求解失败（矩阵奇异）"
	}
	m := &arxModel{na: na, nb: nb, a: theta[:na], b: theta[na : na+nb], c: theta[na+nb]}
	if !m.stable() {
		return m, "模型含不稳定极点（|极点| >= 1），自由仿真结果仅供参考"
	}
	return m, ""
}

func (m *arxModel) oneStep(y, ud []float64, mask []bool) []float64 {
	out := make([]float64, len(y))
	for k := range out {
		out[k] = math.NaN()
	}
	start := max(m.na, m.nb-1)
	for k := start; k < len(y); k++ {
		if !mask[k] || math.IsNaN(y[k]) {
			continue
		}
		v := m.c
		for i := 1; i <= m.na; i++ {
			if math.IsNaN(y[k-i]) {
				v = math.NaN()
				break
			}
			v += m.a[i-1] * y[k-i]
		}
		if math.IsNaN(v) {
			continue
		}
		for j := 0; j < m.nb; j++ {
			v += m.b[j] * ud[k-j]
		}
		out[k] = v
	}
	return out
}

func (m *arxModel) simulate(y, ud []float64, mask []bool, startIdx int) []float64 {
	n := len(y)
	sim := make([]float64, n)
	for k := range sim {
		sim[k] = math.NaN()
	}
	// Backfill simulated outputs with observed history up to startIdx-1.
	ym := make([]float64, n)
	copy(ym, y)
	for k := startIdx; k < n; k++ {
		if !mask[k] {
			continue
		}
		v := m.c
		ok := true
		for i := 1; i <= m.na; i++ {
			if k-i < 0 {
				ok = false
				break
			}
			yi := ym[k-i]
			if math.IsNaN(yi) {
				ok = false
				break
			}
			v += m.a[i-1] * yi
		}
		if !ok {
			continue
		}
		if math.IsNaN(v) {
			continue
		}
		for j := 0; j < m.nb; j++ {
			v += m.b[j] * ud[k-j]
		}
		sim[k] = v
		ym[k] = v
	}
	return sim
}

func (m *arxModel) nPars() int { return m.na + m.nb + 1 }

func (m *arxModel) stable() bool {
	for _, p := range arPoles(m.a) {
		if cmag(p) >= 1 {
			return false
		}
	}
	return true
}

func (m *arxModel) params(dt float64) Parameters {
	ps := toPoles(arPoles(m.a))
	gain := arxGain(m.a, m.b, m.c)
	return Parameters{
		Kind: ModelARX, Order: m.na, Poles: ps,
		TimeConstS: poleTimeConstants(ps, dt),
		Gain:       &gain, AR: append([]float64(nil), m.a...),
		MA: append([]float64(nil), m.b...), Intercept: m.c,
		Detail: "ARX: na=nb=" + itoa(m.na) + "，含常数项",
	}
}

func arxGain(a, b []float64, c float64) float64 {
	sumA := 1.0
	for _, v := range a {
		sumA -= v
	}
	sumB := 0.0
	for _, v := range b {
		sumB += v
	}
	if math.Abs(sumA) < 1e-12 {
		return math.NaN()
	}
	// c handles the residual offset after demeaning; DC gain of dynamic part:
	return sumB / sumA
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		p--
		b[p] = '-'
	}
	return string(b[p:])
}
