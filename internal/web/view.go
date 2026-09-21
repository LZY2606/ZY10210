package web

import (
	"fmt"
	"html/template"
	"math"

	"dynid/internal/ident"
)

// RunView is the template payload for a single run.
type RunView struct {
	Rep         *ident.ModelReport
	DatasetName string
	Best        *ident.Candidate
	TrainSVG    template.HTML
	OneStepSVG  template.HTML
	SimSVG      template.HTML
	ACFEstSVG   template.HTML
	ACFValSVG   template.HTML
	CCFEstSVG   template.HTML
	CCFValSVG   template.HTML
	Flash       struct{ Kind, Msg string }
}

func safeHTML(s string) template.HTML { return template.HTML(s) }

func buildRunView(rep *ident.ModelReport) *RunView {
	v := &RunView{Rep: rep}
	tmin, tmax := rep.GridT[0], rep.GridT[len(rep.GridT)-1]
	bands := [][2]float64{{rep.Cfg.Est.Start, rep.Cfg.Est.End},
		{rep.Cfg.Val.Start, rep.Cfg.Val.End}}
	bandColors := []string{"#eef4fb", "#fdf3e2"}
	bandOpt := svgOptions{
		xLabel: "时间 t（秒）", bands: bands, bandColors: bandColors,
		gridT: rep.GridT, sat: rep.GridSat,
	}
	// input on top of every time chart for reference
	inputS := svgSeries{label: "u", color: "#9ec9a3", t: rep.GridT, v: rep.GridU, width: 0.8, dash: "3 3"}

	// Data + fit (one-step), est & val
	v.TrainSVG = safeHTML(renderLineChart("数据与一步预测拟合（蓝=输出，红=一步预测，绿虚线=输入）", tmin, tmax,
		[]svgSeries{
			inputS,
			{label: "y", color: "#1f4e8c", t: rep.GridT, v: rep.GridY},
			{label: "一步预测(估计)", color: "#c0392b", t: rep.GridT, v: rep.OneStepEst, width: 1.7},
			{label: "一步预测(验证)", color: "#8e44ad", t: rep.GridT, v: rep.OneStepVal, width: 1.7},
		}, bandOpt))
	v.OneStepSVG = safeHTML(renderLineChart("验证段一步预测（放大全部时间轴，仅验证段有值）", tmin, tmax,
		[]svgSeries{
			{label: "y", color: "#1f4e8c", t: rep.GridT, v: rep.GridY},
			{label: "一步预测", color: "#8e44ad", t: rep.GridT, v: rep.OneStepVal, width: 1.8},
		}, bandOpt))
	v.SimSVG = safeHTML(renderLineChart("自由仿真（橙=估计段，红=验证段；只由输入驱动）", tmin, tmax,
		[]svgSeries{
			{label: "y", color: "#1f4e8c", t: rep.GridT, v: rep.GridY},
			{label: "自由仿真(估计)", color: "#e67e22", t: rep.GridT, v: rep.SimEst, width: 1.6},
			{label: "自由仿真(验证)", color: "#c0392b", t: rep.GridT, v: rep.SimVal, width: 1.8},
		}, bandOpt))

	// Diagnostics for the best candidate.
	var best *ident.Candidate
	for _, c := range rep.Candidates {
		if c.ID == rep.BestID {
			best = c
			break
		}
	}
	v.Best = best
	if best != nil {
		band := 0.0
		if n := countResid(rep.ResidEst); n > 0 {
			band = 1.96 / math.Sqrt(float64(n))
		}
		v.ACFEstSVG = corrChartFromDiag("最优候选 · 估计段残差自相关 ACF", best.DiagEst.ACF, band)
		v.CCFEstSVG = ccfChartFromDiag("最优候选 · 估计段输入-残差交叉相关 CCF", best.DiagEst.CCF, band)
		bandV := 0.0
		if n := countResid(rep.ResidVal); n > 0 {
			bandV = 1.96 / math.Sqrt(float64(n))
		}
		v.ACFValSVG = corrChartFromDiag("最优候选 · 验证段残差自相关 ACF", best.DiagVal.ACF, bandV)
		v.CCFValSVG = ccfChartFromDiag("最优候选 · 验证段输入-残差交叉相关 CCF", best.DiagVal.CCF, bandV)
	}
	return v
}

func countResid(r []float64) int {
	n := 0
	for _, x := range r {
		if !math.IsNaN(x) {
			n++
		}
	}
	return n
}

func corrChartFromDiag(title string, checks []ident.ResidualCheck, band float64) template.HTML {
	if len(checks) == 0 {
		return ""
	}
	vals := make([]float64, len(checks))
	for i, c := range checks {
		vals[i] = c.Value
	}
	return template.HTML(renderCorrChart(title, vals, 0, band))
}

func ccfChartFromDiag(title string, checks []ident.ResidualCheck, band float64) template.HTML {
	if len(checks) == 0 {
		return ""
	}
	vals := make([]float64, len(checks))
	for i, c := range checks {
		vals[i] = c.Value
	}
	lagFrom := -(len(checks) - 1) / 2
	return template.HTML(renderCorrChart(title, vals, lagFrom, band))
}

// CompareResult explains parameter and sample differences between two runs.
type CompareResult struct {
	A, B           *ident.ModelReport
	BestA, BestB   *ident.Candidate
	ConfigRows     []CompareRow
	SampleRows     []CompareRow
	BestRows       []CompareRow
	SameDataset    bool
	OverlapAcross  bool
	Recommendation string
}

type CompareRow struct {
	Label     string
	A, B      string
	Different bool
}

func bestOf(r *ident.ModelReport) *ident.Candidate {
	for _, c := range r.Candidates {
		if c.ID == r.BestID {
			return c
		}
	}
	return nil
}

func compareRuns(a, b *ident.ModelReport) *CompareResult {
	res := &CompareResult{A: a, B: b, BestA: bestOf(a), BestB: bestOf(b), SameDataset: a.DatasetID == b.DatasetID}
	res.ConfigRows = []CompareRow{
		{"数据集 ID", fmt.Sprintf("%d", a.DatasetID), fmt.Sprintf("%d", b.DatasetID), a.DatasetID != b.DatasetID},
		{"去均值/去趋势", string(a.Cfg.Detrend), string(b.Cfg.Detrend), a.Cfg.Detrend != b.Cfg.Detrend},
		{"缺失处理", string(a.Cfg.Missing), string(b.Cfg.Missing), a.Cfg.Missing != b.Cfg.Missing},
		{"饱和段参与拟合", yn(a.Cfg.IncludeSaturated), yn(b.Cfg.IncludeSaturated), a.Cfg.IncludeSaturated != b.Cfg.IncludeSaturated},
		{"显式重采样", yn(a.Cfg.Resample), yn(b.Cfg.Resample), a.Cfg.Resample != b.Cfg.Resample},
		{"使用步长 dt（秒）", fmt.Sprintf("%.3f", a.UsedDt), fmt.Sprintf("%.3f", b.UsedDt), a.UsedDt != b.UsedDt},
		{"估计段（秒）", fmt.Sprintf("[%.1f, %.1f)", a.Cfg.Est.Start, a.Cfg.Est.End),
			fmt.Sprintf("[%.1f, %.1f)", b.Cfg.Est.Start, b.Cfg.Est.End), a.Cfg.Est != b.Cfg.Est},
		{"验证段（秒）", fmt.Sprintf("[%.1f, %.1f)", a.Cfg.Val.Start, a.Cfg.Val.End),
			fmt.Sprintf("[%.1f, %.1f)", b.Cfg.Val.Start, b.Cfg.Val.End), a.Cfg.Val != b.Cfg.Val},
		{"延迟候选（秒）", fmt.Sprint(a.Cfg.Delays), fmt.Sprint(b.Cfg.Delays), !eqFloats(a.Cfg.Delays, b.Cfg.Delays)},
		{"阶次候选", fmt.Sprint(a.Cfg.Orders), fmt.Sprint(b.Cfg.Orders), !eqInts(a.Cfg.Orders, b.Cfg.Orders)},
	}
	res.SampleRows = []CompareRow{
		{"原始数据是否等间隔", yn(a.Regular), yn(b.Regular), a.Regular != b.Regular},
		{"最大时钟抖动（秒）", fmt.Sprintf("%.3f", a.JitterMaxS), fmt.Sprintf("%.3f", b.JitterMaxS), a.JitterMaxS != b.JitterMaxS},
		{"缺失样本数", fmt.Sprintf("%d", a.MissingCount), fmt.Sprintf("%d", b.MissingCount), a.MissingCount != b.MissingCount},
		{"饱和样本数", fmt.Sprintf("%d", a.SaturatedCount), fmt.Sprintf("%d", b.SaturatedCount), a.SaturatedCount != b.SaturatedCount},
		{"被排除出拟合的饱和样本", fmt.Sprintf("%d", a.SatExcludedFit), fmt.Sprintf("%d", b.SatExcludedFit), a.SatExcludedFit != b.SatExcludedFit},
		{"估计/验证有效点数", fmt.Sprintf("%d / %d", a.NEst, a.NVal), fmt.Sprintf("%d / %d", b.NEst, b.NVal),
			a.NEst != b.NEst || a.NVal != b.NVal},
	}
	if res.BestA != nil && res.BestB != nil {
		ga, gb := math.NaN(), math.NaN()
		if res.BestA.Params.Gain != nil {
			ga = *res.BestA.Params.Gain
		}
		if res.BestB.Params.Gain != nil {
			gb = *res.BestB.Params.Gain
		}
		res.BestRows = []CompareRow{
			{"模型族", string(res.BestA.Kind), string(res.BestB.Kind), res.BestA.Kind != res.BestB.Kind},
			{"延迟（秒 / 步）", fmt.Sprintf("%.2f / %d", res.BestA.DelayS, res.BestA.DelaySteps),
				fmt.Sprintf("%.2f / %d", res.BestB.DelayS, res.BestB.DelaySteps),
				res.BestA.DelayS != res.BestB.DelayS},
			{"阶次", fmt.Sprintf("%d", res.BestA.Order), fmt.Sprintf("%d", res.BestB.Order), res.BestA.Order != res.BestB.Order},
			{"稳态增益", fmt.Sprintf("%.3f", ga), fmt.Sprintf("%.3f", gb), math.Abs(ga-gb) > 1e-9},
			{"极点", polesStr(res.BestA), polesStr(res.BestB), polesStr(res.BestA) != polesStr(res.BestB)},
			{"时间常数（秒）", fmt.Sprint(res.BestA.Params.TimeConstS), fmt.Sprint(res.BestB.Params.TimeConstS), false},
			{"验证一步预测 NRMS", fmt.Sprintf("%.3f", res.BestA.Metrics.OneStepNRMS),
				fmt.Sprintf("%.3f", res.BestB.Metrics.OneStepNRMS), res.BestA.Metrics.OneStepNRMS != res.BestB.Metrics.OneStepNRMS},
			{"验证自由仿真 NRMS", fmt.Sprintf("%.3f", res.BestA.Metrics.SimNRMS),
				fmt.Sprintf("%.3f", res.BestB.Metrics.SimNRMS), res.BestA.Metrics.SimNRMS != res.BestB.Metrics.SimNRMS},
			{"验证残差白噪声", yn(res.BestA.Metrics.ValWhiteness), yn(res.BestB.Metrics.ValWhiteness),
				res.BestA.Metrics.ValWhiteness != res.BestB.Metrics.ValWhiteness},
			{"ACF/CCF 越界数", fmt.Sprintf("%d / %d", res.BestA.Metrics.ACFOutside, res.BestA.Metrics.CCFOutside),
				fmt.Sprintf("%d / %d", res.BestB.Metrics.ACFOutside, res.BestB.Metrics.CCFOutside),
				res.BestA.Metrics.ACFOutside != res.BestB.Metrics.ACFOutside ||
					res.BestA.Metrics.CCFOutside != res.BestB.Metrics.CCFOutside},
			{"Ljung-Box p", fmt.Sprintf("%.3f", res.BestA.DiagVal.QPValue),
				fmt.Sprintf("%.3f", res.BestB.DiagVal.QPValue), res.BestA.DiagVal.QPValue != res.BestB.DiagVal.QPValue},
		}
		res.Recommendation = recommend(res.BestA, res.BestB, a.Name, b.Name)
	}
	// Cross-run overlap of their estimation windows across datasets is only
	// meaningful on the same time basis; flag when datasets match.
	if res.SameDataset && a.Cfg.Est.Overlaps(b.Cfg.Est) {
		res.OverlapAcross = true
	}
	return res
}

func recommend(a, b *ident.Candidate, na, nb string) string {
	sa := scoreCandidate(a)
	sb := scoreCandidate(b)
	if math.Abs(sa-sb) < 1e-9 {
		return "两运行的最优候选在残差检验与验证误差上基本等价；阶次更高者无统计收益时，按奥卡姆原则优先低阶。"
	}
	win, other := na, nb
	if sb < sa {
		win, other = nb, na
	}
	return fmt.Sprintf("按“残差检验优先、验证误差其次、复杂度兜底”的规则，运行「%s」的最优候选优于「%s」。", win, other)
}

// lower is better: whiteness failures dominate.
func scoreCandidate(c *ident.Candidate) float64 {
	s := 0.0
	if !c.Metrics.ValWhiteness {
		s += 1000
	}
	s += float64(c.Metrics.CCFOutside) * 50
	s += float64(c.Metrics.ACFOutside) * 20
	sim := c.Metrics.SimNRMS
	if math.IsNaN(sim) {
		sim = 1e3
	}
	one := c.Metrics.OneStepNRMS
	if math.IsNaN(one) {
		one = 1e3
	}
	s += sim * 100
	s += one * 50
	s += float64(c.Order) * 0.01
	return s
}

func yn(b bool) string {
	if b {
		return "是"
	}
	return "否"
}

func eqFloats(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func eqInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func polesStr(c *ident.Candidate) string {
	var s string
	for i, p := range c.Params.Poles {
		if i > 0 {
			s += ", "
		}
		if math.Abs(p.Im) < 1e-9 {
			s += fmt.Sprintf("%.3f", p.Re)
		} else {
			s += fmt.Sprintf("%.3f%+.3fi", p.Re, p.Im)
		}
	}
	return s
}
