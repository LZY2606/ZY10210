package ident

import "math"

// FitFO 辨识受限一阶模型 y(k)=a y(k-1)+b u(k-d)，
// a 约束在 [-0.999,0.999] 内（稳定）。对 a 做一维黄金分割，b 解析求解。
func FitFO(u, y []float64, rows []int, d int) (*FitModel, []float64, bool) {
	var syy, syu, syyl, sylu, suu float64
	ok := false
	for _, k := range rows {
		if k-d < 0 {
			continue
		}
		ok = true
		yk, yl, uk := y[k], y[k-1], u[k-d]
		syy += yk * yk
		syu += yk * uk
		syyl += yk * yl
		sylu += yl * uk
		suu += uk * uk
	}
	if !ok || suu <= 0 {
		return nil, nil, false
	}
	// r = y - a yl ; b(a) = (syu - a*sylu)/suu
	// SSE(a) 为 a 的二次函数，在无约束区间内求驻留解再裁剪到稳定区间。
	syl2 := 0.0
	for _, k := range rows {
		if k-d >= 0 {
			syl2 += y[k-1] * y[k-1]
		}
	}
	b0 := syu / suu
	b1 := sylu / suu
	// SSE = syy - 2a syyl - 2b syu + a² syl2 + 2ab sylu + b² suu, b=b0-a b1
	// 对 a 求导：
	q00 := syl2 - 2*b1*sylu + b1*b1*suu
	q01 := -syyl + b1*syu + b0*sylu - b0*b1*suu
	astar := -q01 / q00
	if math.IsNaN(astar) || math.IsInf(astar, 0) || q00 <= 0 {
		astar = 0
	}
	astar = clip(astar, -0.999, 0.999)
	b := b0 - astar*b1
	m := &FitModel{Na: 1, Nb: 0, D: d, A: []float64{1, astar}, B: []float64{b}}
	m.Stable = polesInside(m)
	return m, []float64{astar, b}, true
}

func clip(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

// FOParams 返回一阶模型的连续域解释参数。
func FOParams(m *FitModel, ts float64) (tau, gain float64, ok bool) {
	if m.Na != 1 {
		return 0, 0, false
	}
	a, b := m.A[1], m.B[0]
	if a > 0 && a < 1 {
		tau = -ts / math.Log(a)
	}
	gain = b / (1 - a)
	return tau, gain, true
}
