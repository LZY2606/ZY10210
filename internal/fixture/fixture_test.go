package fixture

import (
	"math"
	"testing"

	"dynidshop/internal/ident"
)

func TestFixtureShapeAndTruth(t *testing.T) {
	raw := Generate()
	if len(raw) != N {
		t.Fatalf("样本数=%d 期望 %d", len(raw), N)
	}
	var sat, miss int
	for _, r := range raw {
		if r.Sat {
			sat++
		}
		if r.U == nil || r.Y == nil {
			miss++
		}
	}
	if sat == 0 {
		t.Error("fixture 应包含饱和段")
	}
	if miss == 0 {
		t.Error("fixture 应包含缺失样本")
	}
	// 未重采样应被拒绝
	_, err := ident.BuildSeries(raw, Ts, false)
	if err != ident.ErrNonUniform {
		t.Errorf("抖动数据未重采样应拒绝，得到 %v", err)
	}
	// 干净 PRBS 段重采样后 FO 应恢复接近真值的参数
	res, err := ident.Run(raw, ident.RunInput{TsNominal: Ts,
		Segments: ident.Segments{EstStart: 10, EstEnd: 19.5, ValStart: 31, ValEnd: 40},
		Prep:     ident.PreprocessOptions{Demean: "none", Missing: "interp", Resample: true},
		Specs:    []ident.ModelSpec{{Kind: ident.KindFO, Delay: TrueDelay}}})
	if err != nil {
		t.Fatal(err)
	}
	c := res.Candidates[0]
	if len(c.Params) < 4 {
		t.Fatalf("FO 参数不足: %v", c.Params)
	}
	tau, gain := c.Params[2], c.Params[3]
	if math.Abs(tau-TrueTau) > 0.15 {
		t.Errorf("时间常数 %.3f 偏离真值 %.3f", tau, TrueTau)
	}
	if math.Abs(gain-TrueGain) > 0.25 {
		t.Errorf("增益 %.3f 偏离真值 %.3f", gain, TrueGain)
	}
}
