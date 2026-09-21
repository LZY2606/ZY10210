package web

import (
	"encoding/json"
	"html/template"
	"math"

	"dynidshop/internal/ident"
	"dynidshop/internal/store"
)

// RunView 聚合运行页所需数据。
type RunView struct {
	Title        string
	Rec          *store.RunRecord
	DS           *store.Dataset
	Res          *ident.RunResult
	CandIdx      int
	Cand         *ident.CandidateResult
	ChartFit     template.HTML
	ChartSim     template.HTML
	ChartACF     template.HTML
	ChartCCF     template.HTML
	SpecsJSON    string
	BlockedFlash bool
	IsError      bool
}

func buildRunView(_ *Server, rec *store.RunRecord, res *ident.RunResult, ds *store.Dataset, idx int) RunView {
	v := RunView{Title: "动态辨识工场", Rec: rec, DS: ds, Res: res, CandIdx: idx}
	if len(res.Candidates) == 0 {
		v.IsError = true
		return v
	}
	if idx < 0 || idx >= len(res.Candidates) {
		idx = 0
		v.CandIdx = 0
	}
	c := &res.Candidates[idx]
	v.Cand = c

	s := res.Series
	estR := Rect{X0: res.Segments.EstStart, X1: res.Segments.EstEnd, Color: "#3a9", Label: "估计"}
	valR := Rect{X0: res.Segments.ValStart, X1: res.Segments.ValEnd, Color: "#a3c7f5", Label: "验证"}
	satRects := []Rect{estR, valR}
	for i, sat := range s.Sat {
		if sat {
			satRects = append(satRects, Rect{X0: s.T[i] - s.Ts/2, X1: s.T[i] + s.Ts/2, Color: "#e0a030"})
		}
	}
	meas := LineSeries{Name: "实测 y", Color: "#222", X: s.T, Y: s.Y, Width: 1.4}
	pred := LineSeries{Name: "一步预测", Color: "#2a6fb0", X: s.T, Y: c.Pred.OneStep, Dashes: "4 2"}
	sim := LineSeries{Name: "自由仿真", Color: "#c0392b", X: s.T, Y: c.Pred.FreeSim, Width: 1.6}
	uu := LineSeries{Name: "输入 u", Color: "#999", X: s.T, Y: s.U, Width: 1}
	v.ChartFit = template.HTML(LineChart("拟合与一步预测（背景：估计/验证/饱和段）", 940, 240,
		[]LineSeries{uu, meas, pred}, satRects, "幅值"))
	v.ChartSim = template.HTML(LineChart("自由仿真（段内暖机后全反馈）", 940, 220,
		[]LineSeries{meas, sim}, []Rect{estR, valR}, "幅值"))
	if len(c.Corr.Lags) > 0 {
		v.ChartACF = template.HTML(CorrChart("残差自相关 ACF（红线为 ±95% 界）", 460, 200,
			c.Corr.Lags, c.Corr.ACF, c.Corr.Bound))
		v.ChartCCF = template.HTML(CorrChart("输入-残差交叉相关 CCF", 460, 200,
			c.Corr.Lags, c.Corr.CCF, c.Corr.Bound))
	}
	specs := make([]map[string]any, 0)
	for _, c2 := range res.Candidates {
		specs = append(specs, map[string]any{
			"kind": string(c2.Spec.Kind), "order": c2.Spec.Order,
			"delay": c2.Spec.Delay, "dsteps": c2.Dsteps,
		})
	}
	b, _ := json.Marshal(specs)
	v.SpecsJSON = string(b)
	return v
}

func fnumFixed(v float64) string {
	av := math.Abs(v)
	switch {
	case av >= 100:
		return formatN(v, 0)
	case av >= 10:
		return formatN(v, 1)
	default:
		return formatN(v, 3)
	}
}
