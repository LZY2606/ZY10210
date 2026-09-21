package ident

import (
	"math"

	"gonum.org/v1/gonum/mat"
)

// FitSS 使用 FIR 最小二乘 + 特征系统实现算法（ERA）得到 n 阶状态空间实现，
// 再由其 Markov 参数还原统一的 A(q)/B(q) 差分模型，便于预测、仿真与并列比较。
func FitSS(u, y []float64, rows []int, n, d int) (*FitModel, []float64, bool) {
	const firLen = 64
	type row struct {
		k int
	}
	useful := make([]int, 0, len(rows))
	for _, k := range rows {
		if k-d-firLen+1 < 0 {
			continue
		}
		useful = append(useful, k)
	}
	if len(useful) < firLen+10 {
		return nil, nil, false
	}
	X := mat.NewDense(len(useful), firLen, nil)
	tgt := make([]float64, len(useful))
	for ri, k := range useful {
		for j := 0; j < firLen; j++ {
			X.Set(ri, j, u[k-d-j])
		}
		tgt[ri] = y[k]
	}
	h, ok := solveLeastSquares(X, tgt)
	if !ok {
		return nil, nil, false
	}

	p, q := 4*n+8, 4*n+8
	if p+q > firLen {
		p = (firLen - 1) / 2
		q = firLen - 1 - p
	}
	H0 := mat.NewDense(p, q, nil)
	H1 := mat.NewDense(p, q, nil)
	for i := 0; i < p; i++ {
		for j := 0; j < q; j++ {
			if i+j+1 < firLen {
				H0.Set(i, j, h[i+j+1])
			}
			if i+j+2 < firLen {
				H1.Set(i, j, h[i+j+2])
			}
		}
	}
	var svd mat.SVD
	if !svd.Factorize(H0, mat.SVDThin) {
		return nil, nil, false
	}
	sig := svd.Values(nil)
	r := n
	if len(sig) < r {
		r = len(sig)
	}
	for i := 0; i < r; i++ {
		if sig[i] <= 1e-9*sig[0] {
			r = i
			break
		}
	}
	if r < 1 {
		return nil, nil, false
	}
	Ufull := mat.NewDense(p, min(len(sig), q), nil)
	Vfull := mat.NewDense(q, min(len(sig), p), nil)
	svd.UTo(Ufull)
	svd.VTo(Vfull)
	U := mat.NewDense(p, r, nil)
	V := mat.NewDense(q, r, nil)
	for i := 0; i < p; i++ {
		for j := 0; j < r; j++ {
			U.Set(i, j, Ufull.At(i, j))
		}
	}
	for i := 0; i < q; i++ {
		for j := 0; j < r; j++ {
			V.Set(i, j, Vfull.At(i, j))
		}
	}
	sr := make([]float64, r)
	for i := 0; i < r; i++ {
		sr[i] = math.Sqrt(sig[i])
	}
	Sinv := mat.NewDense(r, r, nil)
	for i := 0; i < r; i++ {
		Sinv.Set(i, i, 1/sr[i])
	}
	var uth1v, Ad, tmp mat.Dense
	uth1v.Mul(U.T(), H1)
	tmp.Mul(&uth1v, V)
	Ad.Mul(Sinv, &tmp)
	Ad.Mul(&Ad, Sinv)

	// x(k+1)=Ad x(k)+Bd u(k); y=Cx。Bd = Sr V^T e0；Cd = e0^T U Sr。
	Bd := mat.NewDense(r, 1, nil)
	Cd := mat.NewDense(1, r, nil)
	for i := 0; i < r; i++ {
		Bd.Set(i, 0, sr[i]*V.At(0, i))
		Cd.Set(0, i, U.At(0, i)*sr[i])
	}

	// 由实现重新计算 Markov 参数，再反解差分系数（实现可能为 r≤n 阶）。
	na := r
	nb := r - 1
	maxM := d + na + nb + 20
	if maxM > firLen-1 {
		maxM = firLen - 1
	}
	g := make([]float64, maxM+1)
	pow := mat.DenseCopyOf(&Ad)
	eye := mat.NewDense(r, r, nil)
	for i := 0; i < r; i++ {
		eye.Set(i, i, 1)
	}
	for k := 1; k <= maxM; k++ {
		var cb mat.Dense
		cb.Mul(Cd, pow)
		var cbd mat.Dense
		cbd.Mul(&cb, Bd)
		g[k] = cbd.At(0, 0)
		var npow mat.Dense
		npow.Mul(pow, &Ad)
		pow = &npow
	}
	_ = eye

	// g[k] = sum_{i=1..min(na,k)} a_i g[k-i] + b_{k-d},  取 k 尾部段反解 a。
	lo := d + nb + 4
	hi := maxM
	if hi-lo < na+1 {
		lo = na + 2
		if hi-lo < na+1 {
			return nil, nil, false
		}
	}
	Aeq := mat.NewDense(hi-lo+1, na, nil)
	rhs := make([]float64, hi-lo+1)
	rowI := 0
	for k := lo; k <= hi; k++ {
		for i := 1; i <= na; i++ {
			if k-i >= 0 {
				Aeq.Set(rowI, i-1, g[k-i])
			}
		}
		rhs[rowI] = g[k]
		rowI++
	}
	avec, ok2 := solveLeastSquares(Aeq, rhs)
	if !ok2 {
		return nil, nil, false
	}
	den := make([]float64, na+1)
	den[0] = 1
	copy(den[1:], avec)
	// b_j = g[d+j] - sum_i a_i g[d+j-i]
	num := make([]float64, nb+1)
	for j := 0; j <= nb; j++ {
		k := d + j
		v := 0.0
		if k <= maxM {
			v = g[k]
		}
		for i := 1; i <= na; i++ {
			if k-i >= 0 {
				v -= den[i] * g[k-i]
			}
		}
		num[j] = v
	}
	m := transferToFitModel(den, num, nb, d)
	params := make([]float64, 0, 2*r*r)
	for i := 0; i < r; i++ {
		for j := 0; j < r; j++ {
			params = append(params, Ad.At(i, j))
		}
	}
	for i := 0; i < r; i++ {
		params = append(params, Bd.At(i, 0))
	}
	for i := 0; i < r; i++ {
		params = append(params, Cd.At(0, i))
	}
	return m, params, true
}
