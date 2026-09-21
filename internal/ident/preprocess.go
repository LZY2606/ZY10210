package ident

import (
	"errors"
	"math"
	"sort"
)

const jitterTol = 0.02 // 相对偏差超过 2% 即视为非等间隔

// BuildSeries 把原始观测构建为（名义）等间隔序列。
// tsNominal>0 时作为重采样网格步长，否则取原始时间间隔中位数。
// resample=false 且数据非等间隔时返回 ErrNonUniform，防止假装成离散等步模型。
func BuildSeries(raw []RawSample, tsNominal float64, resample bool) (Series, error) {
	if len(raw) < 4 {
		return Series{}, errors.New("样本过少，至少需要 4 条")
	}
	d := make([]RawSample, len(raw))
	copy(d, raw)
	sort.SliceStable(d, func(i, j int) bool { return d[i].T < d[j].T })

	deltas := make([]float64, 0, len(d)-1)
	for i := 1; i < len(d); i++ {
		deltas = append(deltas, d[i].T-d[i-1].T)
	}
	median := medianF(deltas)
	if tsNominal <= 0 {
		tsNominal = median
	}
	jit := 0.0
	for _, dl := range deltas {
		dev := math.Abs(dl-median) / median
		if dev > jit {
			jit = dev
		}
	}
	uniform := jit <= jitterTol

	if !uniform && !resample {
		return Series{}, ErrNonUniform
	}

	if uniform {
		s := Series{T0: d[0].T, Ts: median, Uniform: true, Jitter: jit}
		for _, r := range d {
			s.T = append(s.T, r.T)
			s.U = append(s.U, ptrOrNaN(r.U))
			s.Y = append(s.Y, ptrOrNaN(r.Y))
			s.Sat = append(s.Sat, r.Sat)
			s.Missing = append(s.Missing, r.U == nil || r.Y == nil)
		}
		return s, nil
	}

	t0, t1 := d[0].T, d[len(d)-1].T
	n := int(math.Round((t1-t0)/tsNominal)) + 1
	s := Series{T0: t0, Ts: tsNominal, Uniform: true, Jitter: jit, NonUniformSource: true}
	for k := 0; k < n; k++ {
		tg := t0 + float64(k)*tsNominal
		s.T = append(s.T, tg)
		yu, okU := gridInput(d, tg, tsNominal)
		yv, sat, okY := gridOutput(d, tg, tsNominal)
		s.U = append(s.U, yu)
		s.Y = append(s.Y, yv)
		s.Sat = append(s.Sat, sat)
		s.Missing = append(s.Missing, !okU || !okY)
	}
	return s, nil
}

// gridInput 把零阶保持输入对齐到网格点：取时刻不晚于目标的最近一个样本。
// 距最近样本超过 0.75 步长时视为缺失。
func gridInput(d []RawSample, tg, ts float64) (float64, bool) {
	i := sort.Search(len(d), func(i int) bool { return d[i].T > tg })
	lo := i - 1
	nearest := lo
	if i < len(d) && (lo < 0 || math.Abs(d[i].T-tg) < math.Abs(d[lo].T-tg)) {
		nearest = i
	}
	if nearest < 0 {
		return math.NaN(), false
	}
	r := d[nearest]
	if math.Abs(r.T-tg) > 0.75*ts || r.U == nil {
		return math.NaN(), false
	}
	return *r.U, true
}

// gridOutput 把输出线性插值到网格点；超过 1.5 个步长的缺口记为缺失。
func gridOutput(d []RawSample, tg, ts float64) (float64, bool, bool) {
	i := sort.Search(len(d), func(i int) bool { return d[i].T >= tg })
	if i < len(d) && d[i].T == tg {
		r := d[i]
		return ptrOrNaN(r.Y), r.Sat, r.Y != nil
	}
	if i == 0 || i == len(d) {
		return math.NaN(), false, false
	}
	lo, hi := d[i-1], d[i]
	if hi.T-lo.T > 1.5*ts || lo.Y == nil || hi.Y == nil {
		return math.NaN(), lo.Sat || hi.Sat, false
	}
	w := (tg - lo.T) / (hi.T - lo.T)
	return *lo.Y + w*(*hi.Y-*lo.Y), lo.Sat || hi.Sat, true
}

func ptrOrNaN(p *float64) float64 {
	if p == nil {
		return math.NaN()
	}
	return *p
}

// Preprocess 在序列上执行缺失处理与去均值/去趋势，返回处理后的副本及说明。
type PrepReport struct {
	Series       Series  `json:"series"`
	MissingFixed int     `json:"missing_fixed"`
	MissingLeft  int     `json:"missing_left"`
	YOffset      float64 `json:"y_offset"`
	YSlope       float64 `json:"y_slope"`
	UOffset      float64 `json:"u_offset"`
	USlope       float64 `json:"u_slope"`
	SatCount     int     `json:"sat_count"`
	SatUsedFit   int     `json:"sat_used_fit"`
}

func Preprocess(s Series, seg Segments, opt PreprocessOptions) (PrepReport, error) {
	out := PrepReport{Series: cloneSeries(s)}
	for _, sat := range s.Sat {
		if sat {
			out.SatCount++
		}
	}

	switch opt.Missing {
	case "interp":
		fixMissingLinear(out.Series.T, out.Series.U, out.Series.Missing, true)
		fixMissingLinear(out.Series.T, out.Series.Y, out.Series.Missing, false)
	case "ffill":
		fixMissingFFill(out.Series.U, out.Series.Missing, true)
		fixMissingFFill(out.Series.Y, out.Series.Missing, false)
	case "error", "":
		if anyMissing(s.Missing) {
			return PrepReport{}, errors.New("存在缺失样本：请选择插值或前向填充的缺失处理版本")
		}
	default:
		return PrepReport{}, errors.New("未知缺失处理方式: " + opt.Missing)
	}
	out.MissingLeft = countMissing(out.Series.Missing)
	out.MissingFixed = len(s.Missing) - out.MissingLeft

	inRange := func(t float64, a, b float64) bool { return t >= a && t < b }
	pick := func(which string) func(int) bool {
		switch which {
		case "est":
			return func(i int) bool { return inRange(s.T[i], seg.EstStart, seg.EstEnd) && !s.Missing[i] }
		case "all":
			return func(i int) bool { return !s.Missing[i] }
		default:
			return func(i int) bool { return false }
		}
	}
	applyAffine := func(x []float64, sel func(int) bool, detrend bool) (float64, float64) {
		var sx, st, stt, stx float64
		n := 0
		for i := range x {
			if !sel(i) || math.IsNaN(x[i]) {
				continue
			}
			sx += x[i]
			st += s.T[i]
			stt += s.T[i] * s.T[i]
			stx += s.T[i] * x[i]
			n++
		}
		if n == 0 {
			return 0, 0
		}
		mean := sx / float64(n)
		if !detrend {
			for i := range x {
				x[i] -= mean
			}
			return mean, 0
		}
		tm := st / float64(n)
		den := stt - float64(n)*tm*tm
		slope := 0.0
		if den != 0 {
			slope = (stx - float64(n)*tm*mean) / den
		}
		off := mean - slope*tm
		for i := range x {
			x[i] -= off + slope*s.T[i]
		}
		return off, slope
	}
	out.UOffset, out.USlope = applyAffine(out.Series.U, pick(opt.Demean), false)
	out.YOffset, out.YSlope = applyAffine(out.Series.Y, pick(opt.Detrend), opt.Detrend != "none" && opt.Detrend != "")
	return out, nil
}

func fixMissingLinear(t, x []float64, miss []bool, isU bool) {
	for i := range x {
		if math.IsNaN(x[i]) {
			l, r := i-1, i+1
			for l >= 0 && math.IsNaN(x[l]) {
				l--
			}
			for r < len(x) && math.IsNaN(x[r]) {
				r++
			}
			if l >= 0 && r < len(x) {
				w := (t[i] - t[l]) / (t[r] - t[l])
				x[i] = x[l] + w*(x[r]-x[l])
				miss[i] = false
			} else if l >= 0 {
				x[i] = x[l]
				miss[i] = false
			} else if r < len(x) {
				x[i] = x[r]
				miss[i] = false
			}
		}
	}
}

func fixMissingFFill(x []float64, miss []bool, isU bool) {
	last := math.NaN()
	for i := range x {
		if math.IsNaN(x[i]) {
			if !math.IsNaN(last) {
				x[i] = last
				miss[i] = false
			}
		} else {
			last = x[i]
		}
	}
}

func cloneSeries(s Series) Series {
	c := s
	c.T = append([]float64(nil), s.T...)
	c.U = append([]float64(nil), s.U...)
	c.Y = append([]float64(nil), s.Y...)
	c.Sat = append([]bool(nil), s.Sat...)
	c.Missing = append([]bool(nil), s.Missing...)
	return c
}

func anyMissing(m []bool) bool {
	for _, v := range m {
		if v {
			return true
		}
	}
	return false
}

func countMissing(m []bool) int {
	n := 0
	for _, v := range m {
		if v {
			n++
		}
	}
	return n
}

func medianF(v []float64) float64 {
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	n := len(c)
	if n%2 == 1 {
		return c[n/2]
	}
	return (c[n/2-1] + c[n/2]) / 2
}
