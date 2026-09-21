package ident_test

import (
	"math"
	"testing"
	"time"

	"dynid/internal/fixture"
	"dynid/internal/ident"
)

func baseCfg(est, val ident.Segment, delays []float64) ident.Config {
	return ident.Config{
		Name: "t", Est: est, Val: val,
		Detrend: ident.DetrendMean, Missing: ident.MissingInterpolate,
		Delays: delays, Orders: []int{1, 2},
	}
}

func findBest(rep *ident.ModelReport) *ident.Candidate {
	for _, c := range rep.Candidates {
		if c.ID == rep.BestID {
			return c
		}
	}
	return nil
}

func TestFixtureRecoversTrueDelayAndDynamics(t *testing.T) {
	sp := fixture.DefaultSpec()
	samples := fixture.Build(sp)
	est, val := fixture.Segments()
	cfg := baseCfg(est, val, []float64{0, 1, 2, 3, 4, 5})

	rep, err := ident.Run(1, "t", samples, cfg, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	best := findBest(rep)
	if best == nil {
		t.Fatal("没有最优候选")
	}
	if best.DelayS != sp.DelayS {
		t.Errorf("延迟识别 = %v, 期望 %v", best.DelayS, sp.DelayS)
	}
	if best.Params.Gain == nil || math.Abs(*best.Params.Gain-sp.Gain) > 0.2 {
		t.Errorf("稳态增益 = %v, 期望约 %v", best.Params.Gain, sp.Gain)
	}
	if len(best.Params.TimeConstS) != 1 ||
		math.Abs(best.Params.TimeConstS[0]-sp.TauS) > 2.0 {
		t.Errorf("时间常数 = %v, 期望约 %v", best.Params.TimeConstS, sp.TauS)
	}
	if best.Metrics.SimNRMS > 0.1 || math.IsNaN(best.Metrics.SimNRMS) {
		t.Errorf("验证自由仿真 NRMS = %v, 应 < 0.1", best.Metrics.SimNRMS)
	}
	if !best.Metrics.ValWhiteness {
		t.Errorf("最优候选验证残差应白噪声，ACF=%d CCF=%d p=%v",
			best.Metrics.ACFOutside, best.Metrics.CCFOutside, best.DiagVal.QPValue)
	}
}

func TestWrongDelayHasResidualCorrelation(t *testing.T) {
	samples := fixture.Build(fixture.DefaultSpec())
	est, val := fixture.Segments()
	good, err := ident.Run(1, "g", samples, baseCfg(est, val, []float64{3}), time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	bad, err := ident.Run(1, "b", samples, baseCfg(est, val, []float64{0}), time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	bg, bb := findBest(good), findBest(bad)
	if bb.Metrics.CCFOutside < bg.Metrics.CCFOutside {
		t.Errorf("延迟 0 的 CCF 越界(%d) 不应少于真延迟(%d)",
			bb.Metrics.CCFOutside, bg.Metrics.CCFOutside)
	}
	if bb.Metrics.SimNRMS <= bg.Metrics.SimNRMS {
		t.Errorf("延迟 0 的仿真误差(%v) 不应优于真延迟(%v)",
			bb.Metrics.SimNRMS, bg.Metrics.SimNRMS)
	}
}

func TestOverlapBlocksPublishing(t *testing.T) {
	samples := fixture.Build(fixture.DefaultSpec())
	est, val := fixture.OverlappingSegments()
	rep, err := ident.Run(1, "ov", samples, baseCfg(est, val, []float64{2, 3}), time.Unix(0, 0))
	if err != nil {
		t.Fatalf("重叠时应生成带阻断标记的运行，得到错误: %v", err)
	}
	if rep.PublishBlocked == "" || rep.Published {
		t.Fatal("重叠运行必须处于未发布且带阻断原因的状态")
	}
}

func TestIrregularDataRejectedWithoutResample(t *testing.T) {
	samples := fixture.Build(fixture.DefaultSpec())
	cfg := baseCfg(ident.Segment{Start: 152, End: 220}, ident.Segment{Start: 240, End: 320}, []float64{0, 3})
	if _, err := ident.Run(1, "irr", samples, cfg, time.Unix(0, 0)); err == nil {
		t.Fatal("非等间隔数据未重采样时必须拒绝，而不是假装成等步模型")
	}
	cfg.Resample = true
	cfg.Dt = 1
	rep, err := ident.Run(1, "irr2", samples, cfg, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("显式重采样后应可运行: %v", err)
	}
	if !rep.Resampled || rep.UsedDt != 1 {
		t.Errorf("重采样标记/步长错误: %v %v", rep.Resampled, rep.UsedDt)
	}
}

func TestSaturatedExcludedByDefault(t *testing.T) {
	samples := fixture.Build(fixture.DefaultSpec())
	cfg := baseCfg(ident.Segment{Start: 330, End: 400}, ident.Segment{Start: 240, End: 325}, []float64{3})
	rep, err := ident.Run(1, "sat", samples, cfg, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if rep.SatExcludedFit == 0 {
		t.Errorf("默认应排除饱和样本，排除数=%d", rep.SatExcludedFit)
	}
	cfg.IncludeSaturated = true
	rep2, err := ident.Run(1, "sat2", samples, cfg, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if rep2.SatExcludedFit != 0 {
		t.Errorf("显式纳入后不应排除，排除数=%d", rep2.SatExcludedFit)
	}
}

func TestWinnerMustWhiten(t *testing.T) {
	samples := fixture.Build(fixture.DefaultSpec())
	est, val := fixture.Segments()
	cfg := baseCfg(est, val, []float64{3})
	cfg.Orders = []int{1, 2}
	rep, err := ident.Run(1, "o", samples, cfg, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if best := findBest(rep); !best.Metrics.ValWhiteness {
		t.Fatalf("被选中的候选必须通过验证残差白噪声检验，kind=%v order=%d", best.Kind, best.Order)
	}
}

func TestDelayExpressedInSeconds(t *testing.T) {
	samples := fixture.Build(fixture.DefaultSpec())
	est, val := fixture.Segments()
	rep, err := ident.Run(1, "d", samples, baseCfg(est, val, []float64{3.0}), time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rep.Candidates {
		if c.RejectReason != "" {
			continue
		}
		if c.Params.DelayS != 3.0 || c.DelaySteps != 3 {
			t.Errorf("延迟口径错误: %v秒 / %d步", c.Params.DelayS, c.DelaySteps)
		}
	}
}
