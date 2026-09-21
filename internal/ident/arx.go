package ident

import (
	"math"

	"gonum.org/v1/gonum/mat"
)

// FitARX 在指定行集合上最小二乘辨识 A(q)y = B(q)u。
// 行集合 rows 已由调用方按区间、缺失与饱和策略过滤。
func FitARX(u, y []float64, rows []int, na, nb, d int) (*FitModel, []float64, bool) {
	ncol := na + nb + 1
	X := mat.NewDense(len(rows), ncol, nil)
	target := make([]float64, len(rows))
	for ri, k := range rows {
		c := 0
		for j := 1; j <= na; j++ {
			X.Set(ri, c, -y[k-j])
			c++
		}
		for j := 0; j <= nb; j++ {
			X.Set(ri, c, u[k-d-j])
			c++
		}
		target[ri] = y[k]
	}
	theta, ok := solveLeastSquares(X, target)
	if !ok {
		return nil, nil, false
	}
	m := &FitModel{Na: na, Nb: nb, D: d, A: make([]float64, na+1), B: make([]float64, nb+1)}
	m.A[0] = 1
	for i := 0; i < na; i++ {
		m.A[i+1] = -theta[i]
	}
	copy(m.B, theta[na:])
	m.Stable = polesInside(m)
	return m, theta, true
}

// PredictOneStep 在整条序列上计算一步预测（行无效处为 NaN）。
func (m *FitModel) PredictOneStep(u, y []float64, valid []bool) []float64 {
	n := len(u)
	yp := nanSlice(n)
	for k := max(m.Na, m.D+m.Nb); k < n; k++ {
		if !valid[k] {
			continue
		}
		ok := true
		for j := 1; j <= m.Na; j++ {
			if k-j < 0 || !valid[k-j] || math.IsNaN(y[k-j]) {
				ok = false
			}
		}
		for j := 0; j <= m.Nb; j++ {
			if k-m.D-j < 0 || !valid[k-m.D-j] || math.IsNaN(u[k-m.D-j]) {
				ok = false
			}
		}
		if !ok {
			continue
		}
		v := 0.0
		for j := 1; j <= m.Na; j++ {
			v += m.A[j] * y[k-j]
		}
		for j := 0; j <= m.Nb; j++ {
			v += m.B[j] * u[k-m.D-j]
		}
		yp[k] = v
	}
	return yp
}

// FreeRun 从 start 开始自由仿真；start 之前（含历史）使用实测 y 暖机，
// start..end-1 内用模型自身输出反馈。输入始终使用实测 u。
func (m *FitModel) FreeRun(u, y []float64, valid []bool, start, end int) []float64 {
	n := len(u)
	ysim := append([]float64(nil), y...)
	for k := start; k < end && k < n; k++ {
		v := 0.0
		ok := true
		for j := 1; j <= m.Na; j++ {
			idx := k - j
			if idx < 0 {
				ok = false
				break
			}
			if idx >= start {
				v += m.A[j] * ysim[idx]
			} else {
				v += m.A[j] * y[idx]
			}
		}
		for j := 0; j <= m.Nb; j++ {
			idx := k - m.D - j
			if idx < 0 || math.IsNaN(u[idx]) {
				ok = false
				break
			}
			v += m.B[j] * u[idx]
		}
		if !ok {
			ysim[k] = math.NaN()
			continue
		}
		ysim[k] = v
	}
	out := nanSlice(n)
	for k := start; k < end && k < n; k++ {
		if valid[k] {
			out[k] = ysim[k]
		}
	}
	return out
}

// TransferToFitModel 由状态空间实现经 Markov 参数还原的 (A,B) 差分系数构造模型。
func transferToFitModel(den, num []float64, nb, d int) *FitModel {
	na := len(den) - 1
	m := &FitModel{Na: na, Nb: nb, D: d, A: den, B: make([]float64, nb+1)}
	copy(m.B, num)
	m.Stable = polesInside(m)
	return m
}

// polesInside 检查 A(q)=0 的根是否全部在单位圆内（离散稳定）。
func polesInside(m *FitModel) bool {
	n := m.Na
	if n == 0 {
		return true
	}
	// 伴随矩阵特征值 = A(q) 的根
	c := mat.NewDense(n, n, nil)
	for j := 0; j < n; j++ {
		c.Set(0, j, -m.A[j+1])
	}
	for i := 1; i < n; i++ {
		c.Set(i, i-1, 1)
	}
	var e mat.Eigen
	if ok := e.Factorize(c, mat.EigenNone); !ok {
		return false
	}
	vals := e.Values(nil)
	for _, cm := range vals {
		if hyp(real(cm), imag(cm)) > 1.0+1e-8 {
			return false
		}
	}
	return true
}

func hyp(a, b float64) float64 { return math.Sqrt(a*a + b*b) }

func nanSlice(n int) []float64 {
	x := make([]float64, n)
	for i := range x {
		x[i] = math.NaN()
	}
	return x
}
