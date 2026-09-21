package ident

import (
	"math"

	"gonum.org/v1/gonum/mat"
)

// ssModel is an n-state deterministic state-space model
//
//	x[k+1] = A x[k] + B u[k]
//	y[k]   = C x[k] + c
//
// fitted by MOESP. One-step prediction adds a FIR residual observer fitted
// from estimation residuals (see predictBasis).
type ssModel struct {
	n          int
	a, b, cmat *mat.Dense
	intercept  float64
	kfir       []float64 // observer FIR on past residuals
}

// longestRun returns [start,end) of the longest contiguous fit-able stretch.
func longestRun(y, u []float64, mask []bool) (int, int) {
	bestS, bestL, s := -1, 0, -1
	for k := 0; k <= len(y); k++ {
		good := k < len(y) && mask[k] && !math.IsNaN(y[k]) && !math.IsNaN(u[k])
		if good {
			if s < 0 {
				s = k
			}
			continue
		}
		if s >= 0 && k-s > bestL {
			bestS, bestL = s, k-s
		}
		s = -1
	}
	return bestS, bestS + bestL
}

// moesp identifies (A,C) from a contiguous block of data using the MOESP
// deterministic subspace method. The future-output subspace is obtained by
// an orthogonal projection that removes future inputs.
func moesp(y, u []float64, s0, s1, order, iHankel int) (*mat.Dense, *mat.Dense, string) {
	nh := s1 - s0
	if nh <= 2*iHankel+order+2 {
		return nil, nil, "连续有效样本不足，无法构造子空间 Hankel 矩阵"
	}
	j := nh - 2*iHankel + 1
	yf := hankel(y[s0+iHankel:s1], iHankel, j)
	uf := hankel(u[s0+iHankel:s1], iHankel, j)
	past := mat.NewDense(2*iHankel, j, nil)
	for col := 0; col < j; col++ {
		for r := 0; r < iHankel; r++ {
			past.Set(r, col, u[s0+col+r])
			past.Set(iHankel+r, col, y[s0+col+r])
		}
	}
	// MOESP: remove only the future-input block Hankel Uf from future outputs.
	// Past Up/Yp must NOT be projected out: they carry the state, so removing
	// them would delete the observability signal itself.
	_ = past
	zz := mat.NewDense(iHankel, iHankel, nil)
	zz.Product(uf, uf.T())
	piz := pinv(zz)
	t1 := mat.NewDense(iHankel, iHankel, nil)
	t1.Product(yf, uf.T())
	t2 := mat.NewDense(iHankel, iHankel, nil)
	t2.Product(t1, piz)
	proj := mat.NewDense(iHankel, j, nil)
	proj.Product(t2, uf)
	neg := mat.NewDense(iHankel, j, nil)
	neg.Scale(-1, proj)
	proj.Add(yf, neg)

	var svd mat.SVD
	if !svd.Factorize(proj, mat.SVDThin) {
		return nil, nil, "子空间 SVD 分解失败"
	}
	vals := svd.Values(nil)
	if len(vals) < order || vals[0] == 0 {
		return nil, nil, "可辨识阶次不足（奇异值个数不够）"
	}
	var Um mat.Dense
	svd.UTo(&Um)
	Gamma := Um.Slice(0, iHankel, 0, order).(*mat.Dense)
	C := Gamma.Slice(0, 1, 0, order).(*mat.Dense)
	g1 := Gamma.Slice(0, iHankel-1, 0, order).(*mat.Dense)
	g2 := Gamma.Slice(1, iHankel, 0, order).(*mat.Dense)
	A := mat.NewDense(order, order, nil)
	A.Product(pinv(g1), g2)
	return A, mat.DenseCopyOf(C), ""
}

// hankel builds the i x j Hankel matrix of a series: H(r,col)=v[col+r].
func hankel(v []float64, i, j int) *mat.Dense {
	H := mat.NewDense(i, j, nil)
	for col := 0; col < j; col++ {
		for r := 0; r < i; r++ {
			H.Set(r, col, v[col+r])
		}
	}
	return H
}

// simulateState runs x[k+1]=Ax[k]+B u[k] from x0 over the whole range and
// returns Cx+c (NaN before startIdx).
func (m *ssModel) simulateState(ud []float64, startIdx int, x0 []float64) []float64 {
	n := len(ud)
	out := make([]float64, n)
	for k := range out {
		out[k] = math.NaN()
	}
	x := make([]float64, m.n)
	copy(x, x0)
	for k := startIdx; k < n; k++ {
		out[k] = dotRow(m.cmat, 0, x) + m.intercept
		xn := matVec(m.a, x)
		for r := 0; r < m.n; r++ {
			xn[r] += m.b.At(r, 0) * ud[k]
		}
		x = xn
	}
	return out
}

// freeSimEstimate fits x0 and, when fitDyn is true (estimation segment),
// B and the intercept jointly by least squares. On validation segments B/c
// stay fixed and only x0 is re-estimated.
func (m *ssModel) freeSimEstimate(y, ud []float64, mask []bool, fitDyn bool) ([]float64, int, []float64) {
	start := -1
	for k := range y {
		if mask[k] && !math.IsNaN(y[k]) && !math.IsNaN(ud[k]) {
			start = k
			break
		}
	}
	if start < 0 {
		return nil, 0, nil
	}
	var rows []int
	for k := start; k < len(y); k++ {
		if mask[k] && !math.IsNaN(y[k]) && !math.IsNaN(ud[k]) {
			rows = append(rows, k)
		}
	}
	// Initial-state basis: x0 response columns C A^{k-start} e_r.
	x0basis := make([][][]float64, m.n)
	for r := 0; r < m.n; r++ {
		xv := make([]float64, m.n)
		xv[r] = 1
		x0basis[r] = make([][]float64, len(y))
		for k := start; k < len(y); k++ {
			x0basis[r][k] = append([]float64(nil), xv...)
			xv = matVec(m.a, xv)
		}
	}
	ncol := m.n
	if fitDyn {
		ncol = 2*m.n + 1
	}
	M := mat.NewDense(len(rows), ncol, nil)
	rhs := make([]float64, len(rows))
	// Optional B-direction basis and intercept, only on estimation segment.
	var bbasis [][]float64
	if fitDyn {
		bbasis = make([][]float64, m.n)
		for r := 0; r < m.n; r++ {
			state := make([]float64, m.n)
			col := make([]float64, len(y))
			for k := start; k < len(y); k++ {
				if !math.IsNaN(ud[k]) {
					state[r] += ud[k]
				}
				state = matVec(m.a, state)
				if k+1 < len(y) {
					col[k+1] = dotRow(m.cmat, 0, state)
				}
			}
			bbasis[r] = col
		}
	}
	for row, k := range rows {
		for r := 0; r < m.n; r++ {
			M.Set(row, r, dotRow(m.cmat, 0, x0basis[r][k]))
		}
		rhs[row] = y[k]
		if fitDyn {
			for r := 0; r < m.n; r++ {
				M.Set(row, m.n+r, bbasis[r][k])
			}
			M.Set(row, 2*m.n, 1)
		}
	}
	theta, ok := lstsq(M, rhs, 0)
	if !ok {
		x0 := make([]float64, m.n)
		return x0, start, m.simulateState(ud, start, x0)
	}
	x0 := append([]float64(nil), theta[:m.n]...)
	if fitDyn {
		for r := 0; r < m.n; r++ {
			m.b.Set(r, 0, theta[m.n+r])
		}
		m.intercept = theta[2*m.n]
	}
	return x0, start, m.simulateState(ud, start, x0)
}

// fitObserver fits a FIR correction from past free-run residuals so that
// one-step prediction becomes a genuine predictor rather than a simulation.
func (m *ssModel) fitObserver(y, free []float64, mask []bool, start int) {
	q := min(10, max(3, m.n*2))
	// residuals on estimation segment
	var kr []int
	var e []float64
	for k := start; k < len(y); k++ {
		if mask[k] && !math.IsNaN(y[k]) && !math.IsNaN(free[k]) {
			kr = append(kr, k)
			e = append(e, y[k]-free[k])
		}
	}
	if len(kr) < q+5 {
		m.kfir = nil
		return
	}
	idxOf := make(map[int]int)
	for i, k := range kr {
		idxOf[k] = i
	}
	M := mat.NewDense(len(kr), q, nil)
	rhs := make([]float64, len(kr))
	for i, k := range kr {
		for j := 1; j <= q; j++ {
			if v, ok := idxOf[k-j]; ok {
				M.Set(i, j-1, e[v])
			}
		}
		rhs[i] = e[i]
	}
	theta, ok := lstsq(M, rhs, 0)
	if ok {
		m.kfir = theta
	}
}

// predictOne produces one-step predictions using free simulation + FIR
// observer on past residuals.
func (m *ssModel) predictOne(y, free []float64, mask []bool, start int) []float64 {
	n := len(y)
	out := make([]float64, n)
	for k := range out {
		out[k] = math.NaN()
	}
	q := len(m.kfir)
	if q == 0 {
		for k := start; k < n; k++ {
			if mask[k] && !math.IsNaN(y[k]) {
				out[k] = free[k]
			}
		}
		return out
	}
	for k := start; k < n; k++ {
		if !mask[k] || math.IsNaN(y[k]) {
			continue
		}
		corr := 0.0
		for j := 1; j <= q; j++ {
			if k-j >= start && mask[k-j] && !math.IsNaN(y[k-j]) && !math.IsNaN(free[k-j]) {
				corr += m.kfir[j-1] * (y[k-j] - free[k-j])
			}
		}
		out[k] = free[k] + corr
	}
	return out
}

func (m *ssModel) nPars() int { return m.n*m.n + m.n + m.n + 1 + len(m.kfir) }

func (m *ssModel) stable() bool {
	var e mat.Eigen
	if !e.Factorize(m.a, mat.EigenNone) {
		return false
	}
	for _, z := range e.Values(nil) {
		if cmag(z) >= 1 {
			return false
		}
	}
	return true
}

func (m *ssModel) params(dt float64) Parameters {
	var e mat.Eigen
	e.Factorize(m.a, mat.EigenNone)
	poles := toPoles(e.Values(nil))
	d := cGain(m.a, m.b, m.cmat)
	return Parameters{
		Kind: ModelStateSpace, Order: m.n, Poles: poles,
		TimeConstS: poleTimeConstants(poles, dt),
		Gain:       &d, Intercept: m.intercept,
		Detail: "状态空间(MOESP) n=" + itoa(m.n) + "，含残差观测器 FIR 阶数 " + itoa(len(m.kfir)),
	}
}

func cGain(a, b, cmat *mat.Dense) float64 {
	n, _ := a.Dims()
	I := mat.NewDense(n, n, nil)
	for i := 0; i < n; i++ {
		I.Set(i, i, 1)
	}
	mi := mat.NewDense(n, n, nil)
	mi.Copy(I)
	mi.Sub(mi, a)
	inv := pinv(mi)
	t := mat.NewDense(n, 1, nil)
	t.Product(inv, b)
	return dotRow(cmat, 0, matCol(t, 0))
}

func dotRow(m *mat.Dense, r int, x []float64) float64 {
	_, c := m.Dims()
	s := 0.0
	for j := 0; j < c; j++ {
		s += m.At(r, j) * x[j]
	}
	return s
}

func matVec(m *mat.Dense, x []float64) []float64 {
	r, c := m.Dims()
	out := make([]float64, r)
	for i := 0; i < r; i++ {
		s := 0.0
		for j := 0; j < c; j++ {
			s += m.At(i, j) * x[j]
		}
		out[i] = s
	}
	return out
}

func matCol(m *mat.Dense, c int) []float64 {
	r, _ := m.Dims()
	out := make([]float64, r)
	for i := 0; i < r; i++ {
		out[i] = m.At(i, c)
	}
	return out
}

// fitStateSpace is the model constructor used by the engine.
func fitStateSpace(y, ud []float64, mask []bool, order int) (*ssModel, string) {
	if order < 1 {
		return nil, "状态空间阶次至少为 1"
	}
	s0, s1 := longestRun(y, ud, mask)
	if s1-s0 < 3*order+8 {
		return nil, "连续有效样本不足，无法辨识该阶次状态空间模型"
	}
	iHankel := 2*order + 4
	if iHankel > (s1-s0)/3 {
		iHankel = (s1 - s0) / 3
	}
	if iHankel < order+2 {
		return nil, "连续有效样本不足，无法辨识该阶次状态空间模型"
	}
	A, C, errMsg := moesp(y, ud, s0, s1, order, iHankel)
	if errMsg != "" {
		return nil, errMsg
	}
	m := &ssModel{
		n: order, a: A, cmat: C,
		b: mat.NewDense(order, 1, nil),
	}
	x0, start, free := m.freeSimEstimate(y, ud, mask, true)
	_ = x0
	if free == nil {
		return nil, "状态空间自由仿真初始化失败"
	}
	m.fitObserver(y, free, mask, start)
	note := ""
	if !m.stable() {
		note = "状态矩阵含不稳定特征值，自由仿真结果仅供参考"
	}
	return m, note
}

// ssFitted adapts an identified core (A,B,C,c + observer FIR) to fittedModel.
// A separate initial state is estimated for each simulated segment, matching
// the documented rule that the validation segment is not warm-started from
// estimation data.
type ssFitted struct {
	core  *ssModel
	free  []float64
	start int
}

func (w *ssFitted) oneStep(y, ud []float64, mask []bool) []float64 {
	return w.core.predictOne(y, w.free, mask, w.start)
}

func (w *ssFitted) simulate(y, ud []float64, mask []bool, startIdx int) []float64 {
	if startIdx == w.start {
		return w.free
	}
	x0, st, free := w.core.freeSimEstimate(y, ud, mask, false)
	_ = x0
	_ = st
	return free
}

func (w *ssFitted) nPars() int                   { return w.core.nPars() }
func (w *ssFitted) stable() bool                 { return w.core.stable() }
func (w *ssFitted) params(dt float64) Parameters { return w.core.params(dt) }

// wrapSS estimates the segment-specific initial state and free response.
func wrapSS(core *ssModel, y, ud []float64, mask []bool) (*ssFitted, string) {
	_, start, free := core.freeSimEstimate(y, ud, mask, true)
	if free == nil {
		return nil, "状态空间在该段无法初始化"
	}
	return &ssFitted{core: core, free: free, start: start}, ""
}
