package ident

import (
	"math"

	"gonum.org/v1/gonum/mat"
)

// solveLeastSquares 求解 X θ = y（超定）。使用 SVD 伪逆，
// 即使设计矩阵因真值低阶而列秩亏（高阶候选），也能返回最小范数解，
// 以便并列比较不同阶次。
func solveLeastSquares(X *mat.Dense, y []float64) ([]float64, bool) {
	r, c := X.Dims()
	if r < c+2 {
		return nil, false
	}
	pinv := pinvSVD(X)
	if pinv == nil {
		return nil, false
	}
	yv := mat.NewVecDense(len(y), y)
	th := mat.NewVecDense(c, nil)
	th.MulVec(pinv, yv)
	out := make([]float64, c)
	for i := range out {
		v := th.AtVec(i)
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, false
		}
		out[i] = v
	}
	return out, true
}

// pinvSVD 返回 X 的 Moore–Penrose 伪逆。
func pinvSVD(X *mat.Dense) *mat.Dense {
	r, c := X.Dims()
	var svd mat.SVD
	ok := svd.Factorize(X, mat.SVDThin)
	if !ok {
		return nil
	}
	s := svd.Values(nil)
	smax := 0.0
	for _, v := range s {
		if v > smax {
			smax = v
		}
	}
	tol := 1e-10 * float64(max(r, c)) * smax
	U := mat.NewDense(r, min(len(s), c), nil)
	svd.UTo(U)
	Vt := mat.NewDense(min(len(s), c), c, nil)
	svd.VTo(Vt)
	k := min(len(s), c)
	var Sinv mat.Dense
	Sinv.Apply(func(i, j int, v float64) float64 {
		if i == j && i < len(s) && s[i] > tol {
			return 1 / s[i]
		}
		return 0
	}, mat.NewDense(k, k, nil))
	// pinv = V S^-1 U^T
	var vs, prod mat.Dense
	vt := Vt.T()
	vs.Mul(vt, &Sinv)
	ut := U.T()
	prod.Mul(&vs, ut)
	return &prod
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
