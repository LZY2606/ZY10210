package ident

import "math"

// FitPct 返回归一化拟合度：100*(1 - ||e||/||y-mean(y)||)。
func FitPct(yy, hh []float64, vv []bool) float64 {
	var se, sy, sum float64
	n := 0
	for i := range yy {
		if vv[i] && !math.IsNaN(yy[i]) && !math.IsNaN(hh[i]) {
			sum += yy[i]
			n++
		}
	}
	if n == 0 {
		return math.NaN()
	}
	mean := sum / float64(n)
	for i := range yy {
		if vv[i] && !math.IsNaN(yy[i]) && !math.IsNaN(hh[i]) {
			e := yy[i] - hh[i]
			se += e * e
			sy += (yy[i] - mean) * (yy[i] - mean)
		}
	}
	if sy == 0 {
		if se == 0 {
			return 100
		}
		return 0
	}
	return 100 * (1 - math.Sqrt(se/sy))
}

func rmse(yy, hh []float64, vv []bool) float64 {
	var se float64
	n := 0
	for i := range yy {
		if vv[i] && !math.IsNaN(yy[i]) && !math.IsNaN(hh[i]) {
			e := yy[i] - hh[i]
			se += e * e
			n++
		}
	}
	if n == 0 {
		return math.NaN()
	}
	return math.Sqrt(se / float64(n))
}

// runsOf 返回掩码中连续 true 段的起止索引。
func runsOf(mask []bool) [][2]int {
	runs := make([][2]int, 0)
	i := 0
	for i < len(mask) {
		if !mask[i] {
			i++
			continue
		}
		j := i
		for j < len(mask) && mask[j] {
			j++
		}
		runs = append(runs, [2]int{i, j})
		i = j
	}
	return runs
}

// Correlation 计算残差 ACF 与 u-残差 CCF。按连续有效片段分别去均值后聚合，
// 避免被饱和段/缺失段隔开的残差产生伪相关。显著性界 ±1.96/sqrt(N)。
// CCF 定义为 corr(u(t-lag), e(t))。
func Correlation(u, resid []float64, valid []bool, d int) CorrTest {
	maxLag := 25
	type segData struct {
		start int
		ud    []float64
		ed    []float64
	}
	segs := make([]segData, 0)
	N := 0
	var ce, cu float64
	for _, r := range runsOf(valid) {
		idx := make([]int, 0, r[1]-r[0])
		var me, mu float64
		for t := r[0]; t < r[1]; t++ {
			if math.IsNaN(resid[t]) || math.IsNaN(u[t]) {
				continue
			}
			idx = append(idx, t)
			me += resid[t]
			mu += u[t]
		}
		if len(idx) < 4 {
			continue
		}
		me /= float64(len(idx))
		mu /= float64(len(idx))
		sd := segData{start: idx[0], ud: make([]float64, 0, len(idx)), ed: make([]float64, 0, len(idx))}
		var sse, ssu float64
		for _, t := range idx {
			ed := resid[t] - me
			ud := u[t] - mu
			sd.ed = append(sd.ed, ed)
			sd.ud = append(sd.ud, ud)
			sse += ed * ed
			ssu += ud * ud
		}
		segs = append(segs, sd)
		ce += sse
		cu += ssu
		N += len(idx)
	}
	res := CorrTest{Lags: make([]int, 0, 2*maxLag+1),
		ACF: make([]float64, 0, 2*maxLag+1), CCF: make([]float64, 0, 2*maxLag+1)}
	if N < 8 {
		return res
	}
	res.Bound = 1.96 / math.Sqrt(float64(N))
	denAll := math.Sqrt(cu * ce)
	ccfPeak, ccfPeakLag := 0.0, 0
	for lag := -maxLag; lag <= maxLag; lag++ {
		var ac, cc float64
		nC := 0
		for _, sd := range segs {
			for i := 0; i < len(sd.ed); i++ {
				j := i - lag
				if j < 0 || j >= len(sd.ed) {
					continue
				}
				ac += sd.ed[j] * sd.ed[i]
				cc += sd.ud[j] * sd.ed[i]
				nC++
			}
		}
		acf, ccf := 0.0, 0.0
		if ce > 0 && nC > 0 {
			acf = ac * float64(N) / (ce * float64(nC))
		}
		if denAll > 0 && nC > 0 {
			ccf = cc * float64(N) / (denAll * float64(nC))
		}
		res.Lags = append(res.Lags, lag)
		res.ACF = append(res.ACF, acf)
		res.CCF = append(res.CCF, ccf)
		if lag >= 1 {
			res.ACFTotal++
			if math.Abs(acf) > res.Bound {
				res.ACFInside++
				if math.Abs(float64(lag)) > math.Abs(float64(res.ACFMaxLag)) {
					res.ACFMaxLag = lag
				}
			}
		}
		res.CCFTotal++
		if math.Abs(ccf) > res.Bound {
			res.CCFInside++
		}
		if math.Abs(ccf) > math.Abs(ccfPeak) {
			ccfPeak = ccf
			ccfPeakLag = lag
		}
	}
	res.Whiteness = whitenessLabel(res.ACFInside, res.ACFTotal)
	res.Exogeneity = exogeneityLabel(res.CCFInside, res.CCFTotal)
	res.DelayHint = delayHint(ccfPeakLag, d)
	return res
}
func whitenessLabel(out, total int) string {
	if total == 0 {
		return "不可判定"
	}
	rate := float64(out) / float64(total)
	switch {
	case out == 0:
		return "通过：滞后 1+ 的残差自相关均在 95% 界内"
	case rate <= 0.15:
		return "基本通过：少量越界，可接受"
	default:
		return "未通过：残差仍含可建模动态，考虑提高阶次或检查延迟"
	}
}

func exogeneityLabel(out, total int) string {
	switch {
	case total == 0:
		return "不可判定"
	case out == 0:
		return "通过：输入与残差不相关（外生）"
	case out <= 2:
		return "基本通过：个别交叉相关越界"
	default:
		return "未通过：输入仍残留在残差中，模型结构/延迟存疑"
	}
}

// delayHint 根据 CCF 峰值所在滞后给出延迟方向提示。
func delayHint(peakLag, d int) string {
	switch {
	case peakLag < -1:
		return "CCF 峰值在负滞后 " + itoa(peakLag) + " 拍，疑似延迟被高估"
	case peakLag > 1:
		return "CCF 峰值在正滞后 " + itoa(peakLag) + " 拍，疑似延迟被低估"
	default:
		return "CCF 峰值与声明延迟（" + itoa(d) + " 拍）基本一致"
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	b := make([]byte, 0, 4)
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}
