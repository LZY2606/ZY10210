package ident

import "math"

// Prepared is the modeling grid after preprocessing.
type Prepared struct {
	T           []float64 // grid times in seconds
	U           []float64 // input in processed units
	Y           []float64 // output in processed units (NaNs preserved for missing/excluded)
	Yraw        []float64 // output in engineering units (NaNs preserved)
	Sat         []bool
	EstMask     []bool
	ValMask     []bool
	FitMask     []bool // samples allowed into parameter fitting
	Dt          float64
	Regular     bool
	NMissing    int
	NSat        int
	SatExcluded int
	Jitter      float64
}

const (
	// regularityTol is the relative spacing tolerance used to decide whether
	// raw timestamps are equally spaced.
	regularityTol = 0.02
	// snapTol lets a requested resample grid point snap to a raw timestamp.
	snapTol = 0.25
)

// detectSpacing reports median dt and max relative jitter of consecutive
// gaps within [t0,t1).
func detectSpacing(samples []Sample, seg Segment) (float64, float64) {
	var gaps []float64
	prev := math.NaN()
	for _, s := range samples {
		if !seg.contains(s.T) {
			continue
		}
		if !math.IsNaN(prev) {
			gaps = append(gaps, s.T-prev)
		}
		prev = s.T
	}
	if len(gaps) == 0 {
		return 0, 0
	}
	// median
	mid := len(gaps) / 2
	for i := 0; i < mid; i++ {
		for j := i + 1; j < len(gaps); j++ {
			if gaps[j] < gaps[i] {
				gaps[i], gaps[j] = gaps[j], gaps[i]
			}
		}
	}
	var med float64
	if len(gaps)%2 == 1 {
		med = gaps[mid]
	} else {
		med = (gaps[mid-1] + gaps[mid]) / 2
	}
	jit := 0.0
	for _, g := range gaps {
		r := math.Abs(g-med) / med
		if r > jit {
			jit = r
		}
	}
	return med, jit
}

// Prepare builds the modeling grid and applies all preprocessing choices.
// It returns an error (not a silent fake) when irregular data is used as a
// discrete equal-step model without an explicit resample step.
func Prepare(samples []Sample, cfg Config) (*Prepared, *Transform, *Transform, error) {
	if len(samples) == 0 {
		return nil, nil, nil, &IdentError{"数据为空，无法辨识"}
	}
	dtRaw, jitter := detectSpacing(samples, cfg.Est)
	regular := jitter <= regularityTol

	dt := dtRaw
	if !regular {
		if !cfg.Resample {
			return nil, nil, nil, &IdentError{
				"原始数据非等间隔（最大时钟抖动 " + pct(jitter) +
					"）。未显式选择重采样时，不能将其当作离散等步模型；请在配置中勾选重采样"}
		}
		if cfg.Dt > 0 {
			dt = cfg.Dt
		}
	} else if cfg.Resample && cfg.Dt > 0 {
		dt = cfg.Dt
	}
	if dt <= 0 {
		return nil, nil, nil, &IdentError{"无法确定采样时间"}
	}

	// Build the regular grid over the union of both segments.
	start := cfg.Est.Start
	end := cfg.Est.End
	if cfg.Val.Start < start {
		start = cfg.Val.Start
	}
	if cfg.Val.End > end {
		end = cfg.Val.End
	}
	nGrid := int(math.Floor((end-start)/dt+1e-9)) + 1
	t := make([]float64, nGrid)
	for i := range t {
		t[i] = start + float64(i)*dt
	}

	p := &Prepared{
		T:       t,
		U:       make([]float64, nGrid),
		Y:       make([]float64, nGrid),
		Yraw:    make([]float64, nGrid),
		Sat:     make([]bool, nGrid),
		EstMask: make([]bool, nGrid),
		ValMask: make([]bool, nGrid),
		FitMask: make([]bool, nGrid),
		Dt:      dt,
		Regular: regular,
		Jitter:  jitter,
	}

	// Interpolate raw channels onto the grid (snap when coincident).
	for i, tg := range t {
		p.U[i] = interpU(samples, tg, dt)
		y, sat, have := interpY(samples, tg, dt)
		p.Yraw[i] = y
		p.Sat[i] = sat
		if have {
			p.Y[i] = y
		} else {
			p.Y[i] = math.NaN()
			p.NMissing++
		}
		if sat {
			p.NSat++
		}
	}

	for i := range t {
		p.EstMask[i] = cfg.Est.contains(t[i])
		p.ValMask[i] = cfg.Val.contains(t[i])
	}

	// Missing-output policy on the grid.
	switch cfg.Missing {
	case MissingZero:
		for i := range p.Y {
			if math.IsNaN(p.Y[i]) {
				p.Y[i] = 0
				p.Yraw[i] = 0
			}
		}
	case MissingInterpolate:
		fillNaN(p.Y, t)
		fillNaN(p.Yraw, t)
	case MissingExclude, "":
		// NaNs stay and are excluded from fit/metrics masks.
	default:
		return nil, nil, nil, &IdentError{"未知的缺失处理方式: " + string(cfg.Missing)}
	}

	// Fit mask: estimation segment, non-missing, saturated only on opt-in.
	for i := range p.Y {
		if !p.EstMask[i] {
			continue
		}
		if math.IsNaN(p.Y[i]) {
			continue
		}
		if p.Sat[i] && !cfg.IncludeSaturated {
			p.SatExcluded++
			continue
		}
		p.FitMask[i] = true
	}

	// Preprocessing transforms are estimated from the ESTIMATION segment only
	// (fit-eligible samples for Y, all present samples for U).
	tu := fitTransform(samplesOnGrid(p, t, p.U, true), t, p.EstMask, cfg.Detrend)
	tv := fitTransform(p.Y, t, p.FitMask, cfg.Detrend)
	for i := range t {
		p.U[i] = tu.apply(t[i], p.U[i])
		if !math.IsNaN(p.Y[i]) {
			p.Y[i] = tv.apply(t[i], p.Y[i])
		}
	}
	return p, &tu, &tv, nil
}

func samplesOnGrid(p *Prepared, t, v []float64, _ bool) []float64 { return v }

func fitTransform(x, t []float64, mask []bool, mode DetrendMode) Transform {
	tr := Transform{Mode: mode}
	var xs, ts []float64
	for i := range x {
		if mask[i] && !math.IsNaN(x[i]) {
			xs = append(xs, x[i])
			ts = append(ts, t[i])
		}
	}
	if len(xs) == 0 {
		return tr
	}
	switch mode {
	case DetrendMean:
		tr.Mean = mean(xs)
	case DetrendLinear:
		tm, xm := mean(ts), mean(xs)
		var num, den float64
		for i := range xs {
			num += (ts[i] - tm) * (xs[i] - xm)
			den += (ts[i] - tm) * (ts[i] - tm)
		}
		if den > 0 {
			tr.Slope = num / den
		}
		tr.Intercept = xm - tr.Slope*tm
	}
	return tr
}

// fillNaN replaces interior NaNs by linear interpolation in time; leading and
// trailing NaNs are filled with the nearest finite endpoint value.
func fillNaN(v, t []float64) {
	n := len(v)
	for i := 0; i < n; i++ {
		if !math.IsNaN(v[i]) {
			continue
		}
		l := i - 1
		for l >= 0 && math.IsNaN(v[l]) {
			l--
		}
		r := i + 1
		for r < n && math.IsNaN(v[r]) {
			r++
		}
		switch {
		case l >= 0 && r < n:
			v[i] = v[l] + (v[r]-v[l])*(t[i]-t[l])/(t[r]-t[l])
		case l >= 0:
			v[i] = v[l]
		case r < n:
			v[i] = v[r]
		}
	}
}

// interpU linearly interpolates the (ZOH-like step/PRBS) input at time tg.
func interpU(s []Sample, tg, dt float64) float64 {
	lo, hi := -1, -1
	for i := range s {
		if s[i].T <= tg+1e-9 {
			lo = i
		}
		if s[i].T >= tg-1e-9 {
			hi = i
			break
		}
	}
	if lo < 0 {
		return s[hi].U
	}
	if hi < 0 {
		return s[lo].U
	}
	if lo == hi {
		return s[lo].U
	}
	if math.Abs(s[lo].T-tg) <= snapTol*dt || s[hi].T == s[lo].T {
		return s[lo].U
	}
	w := (tg - s[lo].T) / (s[hi].T - s[lo].T)
	return s[lo].U*(1-w) + s[hi].U*w
}

// interpY interpolates the output; have=false when bracketed by missing
// samples on both sides. Saturation is propagated by weighted majority.
func interpY(s []Sample, tg, dt float64) (y float64, sat, have bool) {
	lo, hi := -1, -1
	for i := range s {
		if s[i].T <= tg+1e-9 {
			lo = i
		}
		if s[i].T >= tg-1e-9 {
			hi = i
			break
		}
	}
	if lo < 0 && hi >= 0 {
		lo = hi
	}
	if hi < 0 && lo >= 0 {
		hi = lo
	}
	if lo < 0 || hi < 0 {
		return 0, false, false
	}
	if lo == hi {
		if s[lo].Y == nil {
			return 0, s[lo].Sat, false
		}
		return *s[lo].Y, s[lo].Sat, true
	}
	if s[lo].Y == nil && s[hi].Y == nil {
		return 0, s[lo].Sat || s[hi].Sat, false
	}
	w := (tg - s[lo].T) / (s[hi].T - s[lo].T)
	var yl, yr float64
	if s[lo].Y != nil {
		yl = *s[lo].Y
	} else {
		yl = *s[hi].Y
		w = 0
	}
	if s[hi].Y != nil {
		yr = *s[hi].Y
	} else {
		yr = yl
		w = 0
	}
	satw := 0.0
	if s[lo].Sat {
		satw += 1 - w
	}
	if s[hi].Sat {
		satw += w
	}
	return yl*(1-w) + yr*w, satw >= 0.5, true
}

func pct(x float64) string {
	// avoid importing fmt in error strings used in templates comparisons
	return strconvFormat(100*x) + "%"
}

// IdentError is a user-facing identification error.
type IdentError struct{ Msg string }

func (e *IdentError) Error() string { return e.Msg }
