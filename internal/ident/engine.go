package ident

import (
	"math"
	"sort"
	"time"
)

// Run executes one full identification pass and returns a comparable report.
func Run(datasetID int64, name string, samples []Sample, cfg Config, now time.Time) (*ModelReport, error) {
	overlap := cfg.Est.Overlaps(cfg.Val)
	if overlap {
		// We still compute and store the run so the acceptance flow can see
		// the blocked state, but publication remains forbidden (re-checked
		// from the stored config at publish time).
	}
	if len(cfg.Delays) == 0 {
		cfg.Delays = []float64{0}
	}
	if len(cfg.Orders) == 0 {
		cfg.Orders = []int{1, 2, 3}
	}
	uniq := uniqSortedF(cfg.Delays)
	cfg.Delays = uniq
	cfg.Orders = uniqSortedI(cfg.Orders)
	if cfg.Missing == "" {
		cfg.Missing = MissingExclude
	}

	p, tu, tv, err := Prepare(samples, cfg)
	if err != nil {
		return nil, err
	}

	rep := &ModelReport{
		ID:             0, // assigned by storage; set before export
		DatasetID:      datasetID,
		Name:           name,
		CreatedAt:      now,
		Cfg:            cfg,
		Regular:        p.Regular,
		NativeDt:       0,
		UsedDt:         p.Dt,
		Resampled:      cfg.Resample,
		JitterMaxS:     p.Jitter * p.Dt,
		MissingCount:   p.NMissing,
		SaturatedCount: p.NSat,
		SatExcludedFit: p.SatExcluded,
		NEst:           countTrue(p.EstMask),
		NVal:           countTrue(p.ValMask),
		TransformU:     *tu,
		TransformY:     *tv,
		GridT:          p.T,
		GridU:          p.U,
		GridY:          p.Y,
		GridSat:        p.Sat,
		GridValidEst:   p.EstMask,
		GridValidVal:   p.ValMask,
	}
	// NativeDt: detect on raw samples in estimation even when resampled.
	med, jit := detectSpacing(samples, cfg.Est)
	rep.NativeDt = med
	_ = jit

	cid := 0
	for _, delayS := range cfg.Delays {
		dSteps := delaySteps(delayS, p.Dt)
		ud := shiftInput(p.U, dSteps)
		for _, order := range cfg.Orders {
			for _, kind := range []ModelKind{ModelARX, ModelStateSpace, ModelFirstOrder} {
				if kind == ModelFirstOrder && order != 1 {
					continue
				}
				cid++
				cand := &Candidate{
					ID: cid, Kind: kind, DelayS: delayS,
					DelaySteps: dSteps, Order: order,
				}
				fitCandidate(cand, p, ud)
				rep.Candidates = append(rep.Candidates, cand)
			}
		}
	}
	rankCandidates(rep.Candidates)
	if best := pickBest(rep.Candidates); best != nil {
		rep.BestID = best.ID
		attachBestTraces(rep, p, best)
	}
	if overlap {
		rep.PublishBlocked = "估计段与验证段存在时间重叠，禁止发布"
	}
	return rep, nil
}

type fittedEntry struct {
	model fittedModel
	note  string
}

func fitCandidate(cand *Candidate, p *Prepared, ud []float64) {
	var core fittedModel
	var note string
	switch cand.Kind {
	case ModelARX:
		m, n := fitARX(p.Y, ud, p.FitMask, cand.Order)
		if m == nil {
			cand.RejectReason = n
			return
		}
		core, note = m, n
	case ModelFirstOrder:
		m, n := fitFirstOrder(p.Y, ud, p.FitMask)
		if m == nil {
			cand.RejectReason = n
			return
		}
		core, note = m, n
	case ModelStateSpace:
		core0, n := fitStateSpace(p.Y, ud, p.FitMask, cand.Order)
		if core0 == nil {
			cand.RejectReason = n
			return
		}
		m, n2 := wrapSS(core0, p.Y, ud, p.FitMask)
		if m == nil {
			cand.RejectReason = n2
			return
		}
		core, note = m, n
	}

	// Estimation residuals and diagnostics.
	hatEst := core.oneStep(p.Y, ud, p.FitMask)
	diagEst := diagnose(p.Y, hatEst, ud, p.FitMask, core.nPars())

	// Validation: independent fit of initial state for state-space models;
	// other families simulate from validation history (warm-up observations).
	valModel := core

	if cand.Kind == ModelStateSpace {
		w, _ := wrapSS(core.(*ssFitted).core, p.Y, ud, p.ValMask)
		if w != nil {
			valModel = w
		}
	}
	hatVal := valModel.oneStep(p.Y, ud, p.ValMask)
	diagVal := diagnose(p.Y, hatVal, ud, p.ValMask, core.nPars())
	simVal := valModel.simulate(p.Y, ud, p.ValMask, simStart(p, valModel, false))

	tr := nrms(hatEst, p.Y, p.FitMask)
	ov := nrms(hatVal, p.Y, p.ValMask)
	sv := nrms(simVal, p.Y, p.ValMask)
	cand.Metrics = CandidateMetrics{
		NUsed:         countFit(p.FitMask),
		TrainNRMS:     tr,
		OneStepNRMS:   ov,
		SimNRMS:       sv,
		OneStepFitPct: fitPct(ov),
		SimFitPct:     fitPct(sv),
		EstWhiteness:  diagEst.White,
		ValWhiteness:  diagVal.White,
		ACFOutside:    diagVal.ACFOutside,
		CCFOutside:    diagVal.CCFOutside,
	}
	cand.DiagEst = diagEst
	cand.DiagVal = diagVal
	par := core.params(p.Dt)
	par.DelayS = cand.DelayS
	par.Order = cand.Order
	par.Kind = cand.Kind
	if note != "" && par.Detail != "" {
		par.Detail += "；" + note
	} else if note != "" {
		par.Detail = note
	}
	cand.Params = par
}

// simStart returns the index where free simulation begins: the first index
// that has a mask value and (for non-state-space) enough observed history.
func simStart(p *Prepared, m fittedModel, est bool) int {
	mask := p.ValMask
	if est {
		mask = p.EstMask
	}
	first := -1
	for k := range mask {
		if mask[k] {
			first = k
			break
		}
	}
	if first < 0 {
		return 0
	}
	switch m.(type) {
	case *ssFitted:
		return first
	default:
		// AR/FO need one observed output as initial condition; the simulator
		// returns NaN for the warm-up sample itself.
		return first + 1
	}
}

// diagnose computes residual whiteness (ACF) and input-independence (CCF),
// pooling contiguous valid stretches by weighted average.
func diagnose(y, yhat, u []float64, mask []bool, npar int) Diagnostics {
	rr, uu := contiguousSlices(mask, y, yhat, u)
	d := Diagnostics{}
	if len(rr) == 0 {
		d.White = false
		return d
	}
	L := 20
	var all []float64
	for _, r := range rr {
		all = append(all, r...)
	}
	d.ResidStd = std(all)
	band := 1.96 / math.Sqrt(float64(len(all)))

	acfAgg := make([]float64, L+1)
	ccfAgg := make([]float64, 2*L+1)
	totW := 0
	for i, r := range rr {
		w := len(r)
		totW += w
		a := acf(r, L)
		for k := range a {
			acfAgg[k] += float64(w) * a[k]
		}
		c := ccf(uu[i], r, L)
		for k := range c {
			ccfAgg[k] += float64(w) * c[k]
		}
	}
	for k := range acfAgg {
		acfAgg[k] /= float64(totW)
	}
	for k := range ccfAgg {
		ccfAgg[k] /= float64(totW)
	}
	d.ACF = make([]ResidualCheck, L+1)
	for k := 0; k <= L; k++ {
		out := k > 0 && math.Abs(acfAgg[k]) > band
		d.ACF[k] = ResidualCheck{Lag: k, Value: acfAgg[k], Outside: out}
		if out {
			d.ACFOutside++
		}
	}
	d.CCF = make([]ResidualCheck, 2*L+1)
	for k := -L; k <= L; k++ {
		v := ccfAgg[k+L]
		out := math.Abs(v) > band
		d.CCF[k+L] = ResidualCheck{Lag: k, Value: v, Outside: out}
		if out {
			d.CCFOutside++
		}
	}
	// Ljung-Box on the pooled residual stream (lags 1..L).
	q, pv := ljungBox(all, L, npar)
	d.QStat = q
	d.QPValue = pv
	d.White = pv > 0.05 && d.ACFOutside <= 2
	return d
}

// rankCandidates sorts by residual tests and validation error — never by the
// training fit. Complexity breaks ties (fewer parameters preferred).
func rankCandidates(cs []*Candidate) {
	type kv struct {
		c *Candidate
		k [4]float64
	}
	var rows []kv
	for _, c := range cs {
		if c.RejectReason != "" {
			continue
		}
		k := [4]float64{
			whitePenalty(c),
			nanMax(c.Metrics.SimNRMS),
			nanMax(c.Metrics.OneStepNRMS),
			float64(c.Metrics.NUsed) - float64(c.Kind.complexity(c.Order))*1e-6,
		}
		rows = append(rows, kv{c, k})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		for g := 0; g < 3; g++ {
			if rows[i].k[g] != rows[j].k[g] {
				return rows[i].k[g] < rows[j].k[g]
			}
		}
		// equal quality -> fewer parameters wins (higher adjusted key)
		return rows[i].k[3] > rows[j].k[3]
	})
	for i, r := range rows {
		if i == 0 {
			r.c.Ranked = true
		}
	}
}

func (k ModelKind) complexity(order int) int {
	switch k {
	case ModelFirstOrder:
		return 3
	case ModelARX:
		return 2*order + 1
	default:
		return order*order + 2*order + 1
	}
}

func whitePenalty(c *Candidate) float64 {
	p := 0.0
	if !c.Metrics.ValWhiteness {
		p += 10
	}
	p += float64(c.Metrics.CCFOutside) + float64(c.Metrics.ACFOutside)*0.5
	return p
}

func nanMax(x float64) float64 {
	if math.IsNaN(x) {
		return 1e6
	}
	return x
}

func pickBest(cs []*Candidate) *Candidate {
	for _, c := range cs {
		if c.Ranked {
			return c
		}
	}
	return nil
}

// attachBestTraces builds report traces for the winning candidate in
// engineering units.
func attachBestTraces(rep *ModelReport, p *Prepared, best *Candidate) {
	dSteps := best.DelaySteps
	ud := shiftInput(p.U, dSteps)
	var core fittedModel
	switch best.Kind {
	case ModelARX:
		core, _ = fitARX(p.Y, ud, p.FitMask, best.Order)
	case ModelFirstOrder:
		core, _ = fitFirstOrder(p.Y, ud, p.FitMask)
	case ModelStateSpace:
		c0, _ := fitStateSpace(p.Y, ud, p.FitMask, best.Order)
		core, _ = wrapSS(c0, p.Y, ud, p.FitMask)
	}
	if core == nil {
		return
	}
	tv := rep.TransformY
	hatEst := undoTrace(p.T, core.oneStep(p.Y, ud, p.FitMask), tv)
	simEstRaw := core.simulate(p.Y, ud, p.EstMask, simStart(p, core, true))
	simEst := undoTrace(p.T, simEstRaw, tv)

	valModel := core
	if best.Kind == ModelStateSpace {
		w, _ := wrapSS(core.(*ssFitted).core, p.Y, ud, p.ValMask)
		if w != nil {
			valModel = w
		}
	}
	hatVal := undoTrace(p.T, valModel.oneStep(p.Y, ud, p.ValMask), tv)
	simVal := undoTrace(p.T, valModel.simulate(p.Y, ud, p.ValMask, simStart(p, valModel, false)), tv)

	yEng := make([]float64, len(p.Y))
	for i := range p.Y {
		if math.IsNaN(p.Y[i]) {
			yEng[i] = math.NaN()
		} else {
			yEng[i] = tv.undo(p.T[i], p.Y[i])
		}
	}
	rep.GridY = yEng
	rep.OneStepEst = hatEst
	rep.OneStepVal = hatVal
	rep.SimEst = simEst
	rep.SimVal = simVal
	rep.ResidEst = diffTrace(yEng, hatEst, p.EstMask)
	rep.ResidVal = diffTrace(yEng, hatVal, p.ValMask)
}

func undoTrace(t, v []float64, tr Transform) []float64 {
	out := make([]float64, len(v))
	for i := range v {
		if math.IsNaN(v[i]) {
			out[i] = math.NaN()
		} else {
			out[i] = tr.undo(t[i], v[i])
		}
	}
	return out
}

func diffTrace(y, h []float64, mask []bool) []float64 {
	out := make([]float64, len(y))
	for i := range y {
		if mask[i] && !math.IsNaN(y[i]) && !math.IsNaN(h[i]) {
			out[i] = y[i] - h[i]
		} else {
			out[i] = math.NaN()
		}
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

func countFit(m []bool) int { return countTrue(m) }

func uniqSortedF(x []float64) []float64 {
	seen := map[float64]bool{}
	var out []float64
	for _, v := range x {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Float64s(out)
	return out
}

func uniqSortedI(x []int) []int {
	seen := map[int]bool{}
	var out []int
	for _, v := range x {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Ints(out)
	return out
}
