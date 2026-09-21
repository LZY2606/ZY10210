package ident

import (
	"math"

	"gonum.org/v1/gonum/mat"
)

// lstsq solves min ||A x - b|| using SVD; returns nil if the problem is
// numerically rank deficient beyond tol.
func lstsq(a *mat.Dense, b []float64, tol float64) ([]float64, bool) {
	r, c := a.Dims()
	var svd mat.SVD
	if !svd.Factorize(a, mat.SVDThin) {
		return nil, false
	}
	vals := svd.Values(nil)
	if len(vals) == 0 {
		return nil, false
	}
	smax := vals[0]
	if smax == 0 {
		return nil, false
	}
	if tol <= 0 {
		tol = 1e-11 * smax * float64(max(r, c))
	}
	var u, v mat.Dense
	svd.UTo(&u)
	svd.VTo(&v)
	// x = V Σ+ U^T b
	utb := make([]float64, len(vals))
	for i := 0; i < len(vals); i++ {
		dot := 0.0
		for k := 0; k < r; k++ {
			dot += u.At(k, i) * b[k]
		}
		if vals[i] > tol {
			utb[i] = dot / vals[i]
		}
	}
	x := make([]float64, c)
	for j := 0; j < c; j++ {
		dot := 0.0
		for i := 0; i < len(vals); i++ {
			dot += v.At(j, i) * utb[i]
		}
		x[j] = dot
	}
	return x, true
}

// pinv returns the Moore-Penrose pseudoinverse of a via SVD.
func pinv(a *mat.Dense) *mat.Dense {
	r, c := a.Dims()
	var svd mat.SVD
	ok := svd.Factorize(a, mat.SVDThin)
	if !ok {
		return mat.NewDense(c, r, nil)
	}
	vals := svd.Values(nil)
	var u, v mat.Dense
	svd.UTo(&u)
	svd.VTo(&v)
	tol := 1e-12 * vals[0] * float64(max(r, c))
	// pinv = V Σ+ U^T
	_, vc := v.Dims()
	ur, _ := u.Dims()
	tmp := mat.NewDense(vc, ur, nil)
	for i := 0; i < len(vals); i++ {
		inv := 0.0
		if vals[i] > tol {
			inv = 1 / vals[i]
		}
		for k := 0; k < ur; k++ {
			tmp.Set(i, k, inv*u.At(k, i))
		}
	}
	res := mat.NewDense(c, r, nil)
	res.Product(&v, tmp)
	return res
}

func mean(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	s := 0.0
	for _, v := range x {
		s += v
	}
	return s / float64(len(x))
}

func std(x []float64) float64 {
	if len(x) < 2 {
		return 0
	}
	m := mean(x)
	s := 0.0
	for _, v := range x {
		s += (v - m) * (v - m)
	}
	return math.Sqrt(s / float64(len(x)-1))
}

func rms(x []float64) float64 {
	if len(x) == 0 {
		return math.NaN()
	}
	s := 0.0
	for _, v := range x {
		s += v * v
	}
	return math.Sqrt(s / float64(len(x)))
}

// nrms returns RMS(pred-true)/std(true); NaN when undefined.
func nrms(pred, truth []float64, mask []bool) float64 {
	var d, t []float64
	for i := range truth {
		if mask != nil && !mask[i] {
			continue
		}
		if math.IsNaN(pred[i]) {
			continue
		}
		d = append(d, pred[i]-truth[i])
		t = append(t, truth[i])
	}
	if len(t) < 2 {
		return math.NaN()
	}
	sd := std(t)
	if sd == 0 {
		return math.NaN()
	}
	return rms(d) / sd
}

func fitPct(n float64) float64 {
	if math.IsNaN(n) {
		return math.NaN()
	}
	f := 100 * (1 - n)
	if f < -1e4 {
		f = -1e4
	}
	return f
}

// acf returns the autocorrelation of x for lags 0..L, normalized by lag-0.
func acf(x []float64, L int) []float64 {
	n := len(x)
	if n == 0 {
		return nil
	}
	s0 := 0.0
	for _, v := range x {
		s0 += v * v
	}
	if s0 == 0 {
		return make([]float64, L+1)
	}
	out := make([]float64, L+1)
	for k := 0; k <= L; k++ {
		s := 0.0
		for i := k; i < n; i++ {
			s += x[i] * x[i-k]
		}
		out[k] = s / s0
	}
	return out
}

// ccf returns cross-correlation of x with y at lags -L..L:
// ccf(lag) = sum_t x[t] y[t-lag] / (||x|| ||y||).
// A spike at positive lag means x leads y by that many samples.
func ccf(x, y []float64, L int) []float64 {
	if len(x) == 0 || len(y) == 0 {
		return nil
	}
	n := len(x)
	if len(y) < n {
		n = len(y)
	}
	sx, sy := 0.0, 0.0
	for i := 0; i < n; i++ {
		sx += x[i] * x[i]
		sy += y[i] * y[i]
	}
	if sx == 0 || sy == 0 {
		return make([]float64, 2*L+1)
	}
	den := math.Sqrt(sx * sy)
	out := make([]float64, 2*L+1)
	for k := -L; k <= L; k++ {
		s := 0.0
		for i := 0; i < n; i++ {
			j := i - k
			if j >= 0 && j < n {
				s += x[i] * y[j]
			}
		}
		out[k+L] = s / den
	}
	return out
}

// boxPierceQ computes the Ljung-Box Q statistic and its upper-tail p-value
// against chi-square with (L - npar) degrees of freedom.
func ljungBox(x []float64, L, npar int) (q, p float64) {
	n := len(x)
	if n <= L+1 {
		return 0, math.NaN()
	}
	s0 := 0.0
	for _, v := range x {
		s0 += v * v
	}
	if s0 == 0 {
		return 0, 1
	}
	for k := 1; k <= L; k++ {
		s := 0.0
		for i := k; i < n; i++ {
			s += x[i] * x[i-k]
		}
		rk := s / s0
		q += float64(n) / float64(n-k) * rk * rk
	}
	q *= float64(n)
	df := L - npar
	if df < 1 {
		df = 1
	}
	return q, chiSquarePValue(q, df)
}

// chiSquareUpperTail for integer/half-integer df, via regularized gamma.
func chiSquarePValue(q float64, df int) float64 {
	if q <= 0 {
		return 1
	}
	a := float64(df) / 2
	x := q / 2
	// regularized upper incomplete gamma via series / continued fraction
	return gammQ(a, x)
}

func gammQ(a, x float64) float64 {
	if x < 0 {
		return 1
	}
	if x == 0 {
		return 1
	}
	if x < a+1 {
		return 1 - gammPseries(a, x)
	}
	return gammQcf(a, x)
}

func gammPseries(a, x float64) float64 {
	ap := a
	sum := 1 / a
	del := sum
	for i := 0; i < 200; i++ {
		ap++
		del *= x / ap
		sum += del
		if math.Abs(del) < math.Abs(sum)*1e-14 {
			break
		}
	}
	return sum * math.Exp(-x+a*math.Log(x)-lgamma(a))
}

func gammQcf(a, x float64) float64 {
	b := x + 1 - a
	c := 1e300
	d := 1 / b
	h := d
	for i := 1; i <= 200; i++ {
		an := -float64(i) * (float64(i) - a)
		b += 2
		d = an*d + b
		if math.Abs(d) < 1e-300 {
			d = 1e-300
		}
		c = b + an/c
		if math.Abs(c) < 1e-300 {
			c = 1e-300
		}
		d = 1 / d
		del := d * c
		h *= del
		if math.Abs(del-1) < 1e-14 {
			break
		}
	}
	return math.Exp(-x+a*math.Log(x)-lgamma(a)) * h
}

func lgamma(x float64) float64 {
	c := []float64{76.18009172947146, -86.50532032941677, 24.01409824083091,
		-1.231739572450155, 0.1208650973866179e-2, -0.5395239384953e-5}
	xx := x
	y := x
	tmp := xx + 5.5
	tmp -= (xx + 0.5) * math.Log(tmp)
	ser := 1.000000000190015
	for j := 0; j < 6; j++ {
		y++
		ser += c[j] / y
	}
	return -tmp + math.Log(2.5066282746310005*ser/xx)
}

// normQuantile is the inverse standard-normal CDF (Acklam approximation).
func normQuantile(p float64) float64 {
	a := []float64{-3.969683028665376e+01, 2.209460984245205e+02,
		-2.759285104469687e+02, 1.383577518672690e+02,
		-3.066479806614716e+01, 2.506628277459239e+00}
	b := []float64{-5.447609879822406e+01, 1.615858368580409e+02,
		-1.556989798598866e+02, 6.680131188771972e+01,
		-1.328068155288572e+01}
	c := []float64{-7.784894002430293e-03, -3.223964580411365e-01,
		-2.400758277161838e+00, -2.549732539343734e+00,
		4.374664141464968e+00, 2.938163982698783e+00}
	d := []float64{7.784695709041462e-03, 3.224671290700398e-01,
		2.445134137142996e+00, 3.754408661907416e+00}
	plow, phigh := 0.02425, 1-0.02425
	var q, r float64
	switch {
	case p < plow:
		q = math.Sqrt(-2 * math.Log(p))
		return (((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) /
			((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	case p <= phigh:
		q = p - 0.5
		r = q * q
		return (((((a[0]*r+a[1])*r+a[2])*r+a[3])*r+a[4])*r + a[5]) * q /
			(((((b[0]*r+b[1])*r+b[2])*r+b[3])*r+b[4])*r + 1)
	default:
		q = math.Sqrt(-2 * math.Log(1-p))
		return -(((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) /
			((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	}
}

func linspace(a, b float64, n int) []float64 {
	out := make([]float64, n)
	if n == 1 {
		out[0] = a
		return out
	}
	for i := 0; i < n; i++ {
		out[i] = a + (b-a)*float64(i)/float64(n-1)
	}
	return out
}
