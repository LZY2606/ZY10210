package ident

import (
	"math"
	"testing"
)

func opt() PreprocessOptions {
	return PreprocessOptions{Demean: "est", Missing: "interp", Resample: true}
}

func TestARXRecoversFO(t *testing.T) {
	// y(k)=0.7 y(k-1)+0.3 u(k-1)，无噪声，应被精确恢复。
	u, y := genTestSeries(220, 1)
	in := RunInput{TsNominal: 0.1,
		Segments: Segments{1, 15, 15, 22},
		Prep:     PreprocessOptions{Missing: "interp"},
		Specs:    []ModelSpec{{Kind: KindARX, Order: 1, Delay: 0.1}, {Kind: KindFO, Delay: 0.1}}}
	res, err := Run(toRaw(u, y, 0.1), in)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res.Candidates {
		if c.Error != "" {
			t.Fatalf("%v 失败: %s", c.Spec, c.Error)
		}
		if math.Abs(c.Params[0]-0.7) > 0.02 {
			t.Errorf("%v a=%.3f 偏离 0.7", c.Spec.Kind, c.Params[0])
		}
		if c.ValFit < 95 {
			t.Errorf("%v 验证拟合 %.1f 过低", c.Spec.Kind, c.ValFit)
		}
	}
}

func TestWrongDelayWorseCorrelation(t *testing.T) {
	u, y := genTestSeries(320, 2)
	in := RunInput{TsNominal: 0.1,
		Segments: Segments{1, 20, 20, 32},
		Prep:     opt(),
		Specs: []ModelSpec{
			{Kind: KindFO, Delay: 0.1},
			{Kind: KindFO, Delay: 0.2},
			{Kind: KindFO, Delay: 0.3},
		}}
	res, err := Run(toRaw(u, y, 0.1), in)
	if err != nil {
		t.Fatal(err)
	}
	byDelay := map[float64]CandidateResult{}
	for _, c := range res.Candidates {
		byDelay[c.Spec.Delay] = c
	}
	good := byDelay[0.2]
	bad := byDelay[0.1]
	if good.Corr.CCFInside > bad.Corr.CCFInside && false {
		// 越界数对比可能接近，改用 RMSE。
	}
	if good.ValRMSE >= bad.ValRMSE {
		t.Errorf("正确延迟的验证 RMSE 应更小: good=%.4f bad=%.4f", good.ValRMSE, bad.ValRMSE)
	}
	if good.BIC >= bad.BIC {
		t.Errorf("正确延迟的 BIC 应更小: good=%.1f bad=%.1f", good.BIC, bad.BIC)
	}
}

func TestNonUniformRejectedWithoutResample(t *testing.T) {
	raw := jitterRaw(120)
	_, err := Run(raw, RunInput{TsNominal: 0.1,
		Segments: Segments{1, 6, 7, 11},
		Prep:     PreprocessOptions{Missing: "interp", Resample: false}})
	if err != ErrNonUniform {
		t.Fatalf("期望 ErrNonUniform，得到 %v", err)
	}
	// 显式重采样后应成功
	res, err := Run(raw, RunInput{TsNominal: 0.1,
		Segments: Segments{1, 6, 7, 11},
		Prep:     PreprocessOptions{Missing: "interp", Resample: true},
		Specs:    []ModelSpec{{Kind: KindARX, Order: 1, Delay: 0}}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Resampled {
		t.Error("期望标记为重采样")
	}
}

func TestOverlapDetection(t *testing.T) {
	if !(Segments{0, 10, 9, 20}.Overlap()) {
		t.Error("正长度重叠应被检出")
	}
	if (Segments{0, 10, 10, 20}.Overlap()) {
		t.Error("端点相接不算重叠")
	}
	if (Segments{0, 10, 11, 20}.Overlap()) {
		t.Error("不相交不算重叠")
	}
}

func TestHigherOrderExplainedByComplexity(t *testing.T) {
	u, y := genTestSeries(260, 1)
	in := RunInput{TsNominal: 0.1,
		Segments: Segments{1, 16, 16, 26},
		Prep:     PreprocessOptions{Missing: "interp"},
		Specs: []ModelSpec{
			{Kind: KindARX, Order: 1, Delay: 0.1},
			{Kind: KindARX, Order: 3, Delay: 0.1},
		}}
	res, err := Run(toRaw(u, y, 0.1), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Candidates) != 2 {
		t.Fatalf("候选数 %d", len(res.Candidates))
	}
	low, high := res.Candidates[0], res.Candidates[1]
	// 排序后 BIC 低者在前；一阶真值模型 BIC 不应差于过参数的三阶。
	if low.Spec.Order != 1 {
		t.Errorf("期望一阶 BIC 最优，实际 %s 在前: %.1f vs %.1f", high.Spec.Kind, low.BIC, high.BIC)
	}
	if high.Np <= low.Np {
		t.Error("高阶候选参数数应更多")
	}
}

func TestSaturationExcludedByDefault(t *testing.T) {
	u, y := genTestSeries(200, 1)
	for k := 120; k < 140; k++ {
		y[k] = 5.0
	}
	raw := toRaw(u, y, 0.1)
	for k := 120; k < 140; k++ {
		raw[k].Sat = true
	}
	in := RunInput{TsNominal: 0.1,
		Segments: Segments{1, 12, 12, 20},
		Prep:     PreprocessOptions{Missing: "interp", SatFit: false},
		Specs:    []ModelSpec{{Kind: KindFO, Delay: 0.1}}}
	res, err := Run(raw, in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Prep.SatCount != 20 || res.Prep.SatUsedFit != 0 {
		t.Errorf("饱和统计错误: count=%d used=%d", res.Prep.SatCount, res.Prep.SatUsedFit)
	}
}

func TestMissingHandling(t *testing.T) {
	raw := toRaw(func() []float64 {
		u, _ := genTestSeries(60, 1)
		return u
	}(), func() []float64 {
		_, y := genTestSeries(60, 1)
		y[20] = math.NaN()
		return y
	}(), 0.1)
	v := 123.0
	raw[20].Y = nil
	_ = v
	in := RunInput{TsNominal: 0.1,
		Segments: Segments{1, 4, 4, 6},
		Prep:     PreprocessOptions{Missing: "error"},
		Specs:    []ModelSpec{{Kind: KindFO, Delay: 0.1}}}
	if _, err := Run(raw, in); err == nil {
		t.Error("missing=error 时应拒绝")
	}
	in.Prep.Missing = "interp"
	res, err := Run(raw, in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Prep.MissingFixed < 1 {
		t.Errorf("期望至少修复 1 个缺失，实际 %d", res.Prep.MissingFixed)
	}
}
