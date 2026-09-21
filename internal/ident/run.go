package ident

import (
	"fmt"
	"math"
	"sort"
)

// RunResult 是一次辨识运行的完整结果。
type RunResult struct {
	Series     Series            `json:"series"`
	Segments   Segments          `json:"segments"`
	Prep       PrepReport        `json:"prep"`
	Resampled  bool              `json:"resampled"`
	Overlap    bool              `json:"overlap"`
	Candidates []CandidateResult `json:"candidates"`
	Messages   []string          `json:"messages"`
}

// Run 执行完整辨识流水线。
func Run(raw []RawSample, in RunInput) (*RunResult, error) {
	s, err := BuildSeries(raw, in.TsNominal, in.Prep.Resample)
	if err != nil {
		return nil, err
	}
	rep, err := Preprocess(s, in.Segments, in.Prep)
	if err != nil {
		return nil, err
	}
	res := &RunResult{Series: rep.Series, Segments: in.Segments, Prep: rep,
		Resampled: in.Prep.Resample && s.NonUniformSource, Overlap: in.Segments.Overlap()}

	segRows := func(a, b float64) []int {
		rows := make([]int, 0)
		for i, t := range rep.Series.T {
			if t < a || t >= b || rep.Series.Missing[i] {
				continue
			}
			if rep.Series.Sat[i] && !in.Prep.SatFit {
				continue
			}
			rows = append(rows, i)
		}
		return rows
	}
	estRows := segRows(in.Segments.EstStart, in.Segments.EstEnd)
	valRows := segRows(in.Segments.ValStart, in.Segments.ValEnd)
	if len(estRows) < 8 {
		return nil, fmt.Errorf("估计段有效样本仅 %d 个，不足", len(estRows))
	}
	res.Messages = append(res.Messages, fmt.Sprintf("估计段有效样本 %d，验证段有效样本 %d", len(estRows), len(valRows)))
	satUsed := 0
	for _, k := range estRows {
		if rep.Series.Sat[k] {
			satUsed++
		}
	}
	rep.SatUsedFit = satUsed
	res.Prep = rep
	if rep.SatCount > 0 && !in.Prep.SatFit {
		res.Messages = append(res.Messages, fmt.Sprintf("检出饱和样本 %d 个，默认仅显示/诊断，未参与拟合", rep.SatCount))
	}
	if res.Resampled {
		res.Messages = append(res.Messages, fmt.Sprintf("检测到时钟抖动（最大相对偏差 %.1f%%），已按 %.4g s 重采样", s.Jitter*100, s.Ts))
	}
	if res.Overlap {
		res.Messages = append(res.Messages, "阻断：估计段与验证段重叠，结果只能草稿诊断，禁止发布")
	}

	// 整条序列上的有效性掩码（饱和样本可显示，预测仍计算；指标按段过滤）。
	allValid := make([]bool, len(rep.Series.T))
	for i := range allValid {
		allValid[i] = !rep.Series.Missing[i]
	}
	estMask := maskOf(len(rep.Series.T), estRows)
	valMask := maskOf(len(rep.Series.T), valRows)
	both := orMask(estMask, valMask)

	seen := map[string]bool{}
	for _, spec := range in.Specs {
		key := fmt.Sprintf("%s-%d-%g", spec.Kind, spec.Order, spec.Delay)
		if seen[key] {
			continue
		}
		seen[key] = true
		cr := CandidateResult{Spec: spec}
		cr.Dsteps = int(math.Round(spec.Delay / rep.Series.Ts))
		if cr.Dsteps < 0 {
			cr.Error = "延迟为负"
			res.Candidates = append(res.Candidates, cr)
			continue
		}
		if math.Abs(spec.Delay-float64(cr.Dsteps)*rep.Series.Ts) > 0.25*rep.Series.Ts {
			res.Messages = append(res.Messages, fmt.Sprintf("候选 %s 的延迟 %g s 不是采样步长 %g s 的整数倍，已取整为 %d 拍",
				key, spec.Delay, rep.Series.Ts, cr.Dsteps))
		}
		d := cr.Dsteps
		needHist := func(na, nb int) []int {
			out := make([]int, 0, len(estRows))
			for _, k := range estRows {
				if k >= na && k-d-nb >= 0 {
					out = append(out, k)
				}
			}
			return out
		}
		var m *FitModel
		var params []float64
		var ok bool
		switch spec.Kind {
		case KindARX:
			if spec.Order < 1 {
				cr.Error = "ARX 阶次至少为 1"
				res.Candidates = append(res.Candidates, cr)
				continue
			}
			m, params, ok = FitARX(rep.Series.U, rep.Series.Y, needHist(spec.Order, spec.Order-1), spec.Order, spec.Order-1, d)
		case KindFO:
			m, params, ok = FitFO(rep.Series.U, rep.Series.Y, needHist(1, 0), d)
		case KindSS:
			if spec.Order < 1 {
				cr.Error = "状态空间阶次至少为 1"
				res.Candidates = append(res.Candidates, cr)
				continue
			}
			m, params, ok = FitSS(rep.Series.U, rep.Series.Y, needHist(1, 63), spec.Order, d)
		default:
			cr.Error = "未知模型类型"
			res.Candidates = append(res.Candidates, cr)
			continue
		}
		if !ok {
			cr.Error = "辨识失败：有效样本不足或矩阵病态"
			res.Candidates = append(res.Candidates, cr)
			continue
		}
		fillCandidate(&cr, m, params, rep.Series, allValid, estMask, valMask, both)
		res.Candidates = append(res.Candidates, cr)
	}
	sort.SliceStable(res.Candidates, func(i, j int) bool {
		a, b := res.Candidates[i], res.Candidates[j]
		if a.Error != "" || b.Error != "" {
			return a.Error == "" && b.Error != ""
		}
		return a.BIC < b.BIC
	})
	return res, nil
}

func fillCandidate(cr *CandidateResult, m *FitModel, ssParams []float64, s Series,
	allValid, estMask, valMask, both []bool) {
	yp := m.PredictOneStep(s.U, s.Y, allValid)
	resid := nanSlice(len(s.Y))
	for i := range s.Y {
		if both[i] && !math.IsNaN(yp[i]) {
			resid[i] = s.Y[i] - yp[i]
		}
	}
	// 自由仿真：估计段从段内第 maxorder 点开始，验证段从段首开始（用实测历史暖机）。
	sim := nanSlice(len(s.Y))
	fillSim(sim, m, s.U, s.Y, estMask)
	fillSim(sim, m, s.U, s.Y, valMask)

	cr.Pred = SeriesPred{OneStep: yp, FreeSim: sim, Resid: resid}
	cr.Nres = countTrue(both)
	cr.EstFit = FitPct(s.Y, yp, estMask)
	cr.ValFit = FitPct(s.Y, yp, valMask)
	cr.EstRMSE = rmse(s.Y, yp, estMask)
	cr.ValRMSE = rmse(s.Y, yp, valMask)
	cr.SimFit = FitPct(s.Y, sim, valMask)

	n := cr.Nres
	rss := 0.0
	for i := range resid {
		if both[i] && !math.IsNaN(resid[i]) {
			rss += resid[i] * resid[i]
		}
	}
	cr.Np = m.Na + m.Nb + 1
	cr.AIC = aic(rss, n, cr.Np)
	cr.BIC = bic(rss, n, cr.Np)
	cr.Corr = Correlation(s.U, resid, both, cr.Dsteps)

	cr.Params, cr.ParamNames = describeParams(cr.Spec, m, ssParams, s.Ts)
}

func fillSim(dst []float64, m *FitModel, u, y []float64, mask []bool) {
	start, end := -1, -1
	for i, v := range mask {
		if v && start < 0 {
			start = i
		}
		if v {
			end = i + 1
		}
	}
	if start < 0 {
		return
	}
	order := m.Na
	if m.D+m.Nb > order {
		order = m.D + m.Nb
	}
	start += order
	if start >= end {
		return
	}
	sim := m.FreeRun(u, y, mask, start, end)
	for i := start; i < end; i++ {
		if mask[i] {
			dst[i] = sim[i]
		}
	}
}

func describeParams(spec ModelSpec, m *FitModel, ss []float64, ts float64) ([]float64, []string) {
	switch spec.Kind {
	case KindFO:
		tau, gain, _ := FOParams(m, ts)
		names := []string{"a=y(k-1)系数", "b=u(k-d)系数", "时间常数τ(s)", "稳态增益K"}
		return []float64{m.A[1], m.B[0], tau, gain}, names
	case KindARX:
		p := append([]float64(nil), m.A[1:]...)
		p = append(p, m.B...)
		names := make([]string, 0, len(p))
		for i := 1; i <= m.Na; i++ {
			names = append(names, fmt.Sprintf("a%d", i))
		}
		for j := 0; j <= m.Nb; j++ {
			names = append(names, fmt.Sprintf("b%d(u(k-%d-%d))", j, m.D, j))
		}
		return p, names
	default:
		return ss, ssParamNames(spec.Order)
	}
}

func ssParamNames(n int) []string {
	names := make([]string, 0, n*n+2*n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			names = append(names, fmt.Sprintf("A%d%d", i+1, j+1))
		}
	}
	for i := 0; i < n; i++ {
		names = append(names, fmt.Sprintf("B%d", i+1))
	}
	for i := 0; i < n; i++ {
		names = append(names, fmt.Sprintf("C%d", i+1))
	}
	return names
}

func aic(rss float64, n, p int) float64 {
	if rss <= 0 || n == 0 {
		return math.Inf(-1)
	}
	return float64(n)*math.Log(rss/float64(n)) + 2*float64(p)
}

func bic(rss float64, n, p int) float64 {
	if rss <= 0 || n == 0 {
		return math.Inf(-1)
	}
	return float64(n)*math.Log(rss/float64(n)) + float64(p)*math.Log(float64(n))
}

func maskOf(n int, rows []int) []bool {
	m := make([]bool, n)
	for _, r := range rows {
		m[r] = true
	}
	return m
}

func orMask(a, b []bool) []bool {
	out := make([]bool, len(a))
	for i := range a {
		out[i] = a[i] || b[i]
	}
	return out
}

func countTrue(m []bool) int {
	n := 0
	for _, v := range m {
		if v {
			n++
		}
	}
	return n
}
