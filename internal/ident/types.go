// Package ident implements a small dynamic system-identification workshop:
// preprocessing, dead-time and order candidates, ARX / state-space /
// constrained first-order models, and residual-based diagnostics.
package ident

import "time"

// Sample is one raw (possibly irregularly spaced) observation.
// A nil Y means a missing output sample.
type Sample struct {
	T   float64  `json:"t"`
	U   float64  `json:"u"`
	Y   *float64 `json:"y"`
	Sat bool     `json:"sat"`
}

// Segment is a user-selected time interval [Start, End) in seconds.
type Segment struct {
	Start float64
	End   float64
}

// Overlaps reports whether two half-open intervals share any time.
func (s Segment) Overlaps(o Segment) bool {
	return s.Start < o.End && o.Start < s.End
}

func (s Segment) contains(t float64) bool { return t >= s.Start && t < s.End }

// Contains reports whether t lies in the half-open segment.
func (s Segment) Contains(t float64) bool { return s.contains(t) }

// MissingPolicy selects how missing output samples are treated.
type MissingPolicy string

const (
	MissingExclude     MissingPolicy = "exclude"     // mask those samples out
	MissingInterpolate MissingPolicy = "interpolate" // linear interpolation in time
	MissingZero        MissingPolicy = "zero"        // keep y=0 (explicit, documented)
)

// DetrendMode selects input/output level handling.
type DetrendMode string

const (
	DetrendNone   DetrendMode = "none"
	DetrendMean   DetrendMode = "mean"
	DetrendLinear DetrendMode = "linear"
)

// ModelKind enumerates the supported model families.
type ModelKind string

const (
	ModelARX        ModelKind = "arx"
	ModelStateSpace ModelKind = "statespace"
	ModelFirstOrder ModelKind = "firstorder"
)

// Config is the user configuration for one identification run.
type Config struct {
	DatasetID        int64
	Name             string
	Est              Segment
	Val              Segment
	Detrend          DetrendMode
	Missing          MissingPolicy
	IncludeSaturated bool
	Resample         bool      // explicit opt-in: irregular data -> regular grid
	Dt               float64   // resample/grid spacing in seconds (0 => estimate median)
	Delays           []float64 // candidate dead times expressed in seconds
	Orders           []int     // candidate model orders
	Note             string
}

// Transform records the preprocessing applied to a channel so predictions
// can be projected back onto engineering units:
//
//	mean:   xProc = x - Mean
//	linear: xProc = x - (Slope*t + Intercept)
type Transform struct {
	Mode      DetrendMode
	Mean      float64
	Slope     float64
	Intercept float64
}

func (tr Transform) apply(t, x float64) float64 {
	switch tr.Mode {
	case DetrendMean:
		return x - tr.Mean
	case DetrendLinear:
		return x - (tr.Slope*t + tr.Intercept)
	default:
		return x
	}
}

func (tr Transform) undo(t, x float64) float64 {
	switch tr.Mode {
	case DetrendMean:
		return x + tr.Mean
	case DetrendLinear:
		return x + tr.Slope*t + tr.Intercept
	default:
		return x
	}
}

// ResidualCheck is one lag of an autocorrelation / cross-correlation test.
type ResidualCheck struct {
	Lag     int     `json:"lag"`
	Value   float64 `json:"value"`
	Outside bool    `json:"outside"`
}

// Diagnostics are residual (not training-fit) based checks.
type Diagnostics struct {
	ResidStd   float64         `json:"resid_std"`
	ACF        []ResidualCheck `json:"acf"`
	CCF        []ResidualCheck `json:"ccf"`
	ACFOutside int             `json:"acf_outside"`
	CCFOutside int             `json:"ccf_outside"`
	QStat      float64         `json:"q_stat"`
	QPValue    float64         `json:"q_pvalue"`
	White      bool            `json:"white"`
}

// Pole is one (possibly complex) discrete-time pole.
type Pole struct {
	Re  float64 `json:"re"`
	Im  float64 `json:"im"`
	Abs float64 `json:"abs"`
}

// Parameters are human-oriented model parameters located per run.
type Parameters struct {
	Kind       ModelKind `json:"kind"`
	DelayS     float64   `json:"delay_s"`
	Order      int       `json:"order"`
	Poles      []Pole    `json:"poles,omitempty"`
	TimeConstS []float64 `json:"time_const_s,omitempty"`
	Gain       *float64  `json:"gain,omitempty"`
	AR         []float64 `json:"ar,omitempty"`
	MA         []float64 `json:"ma,omitempty"` // ARX input coefficients
	Intercept  float64   `json:"intercept"`
	Detail     string    `json:"detail"`
}

// CandidateMetrics holds comparisons computed on the validation segment.
type CandidateMetrics struct {
	NUsed         int     `json:"n_used"`
	TrainNRMS     float64 `json:"train_nrms"`
	OneStepNRMS   float64 `json:"onestep_nrms"`
	SimNRMS       float64 `json:"sim_nrms"`
	OneStepFitPct float64 `json:"onestep_fit_pct"`
	SimFitPct     float64 `json:"sim_fit_pct"`
	EstWhiteness  bool    `json:"est_white"`
	ValWhiteness  bool    `json:"val_white"`
	ACFOutside    int     `json:"acf_outside"`
	CCFOutside    int     `json:"ccf_outside"`
}

// Candidate is one (delay, order, model-family) combination and its verdict.
type Candidate struct {
	ID           int              `json:"id"`
	Kind         ModelKind        `json:"kind"`
	DelayS       float64          `json:"delay_s"`
	DelaySteps   int              `json:"delay_steps"`
	Order        int              `json:"order"`
	Params       Parameters       `json:"params"`
	Metrics      CandidateMetrics `json:"metrics"`
	DiagEst      Diagnostics      `json:"diag_est"`
	DiagVal      Diagnostics      `json:"diag_val"`
	Ranked       bool             `json:"ranked"`
	RejectReason string           `json:"reject_reason,omitempty"`
}

// Series is one plotted time series in engineering units.
type Series struct {
	Label string    `json:"label"`
	T     []float64 `json:"t"`
	V     []float64 `json:"v"`
}

// ModelReport is the persisted, comparable outcome of a run.
type ModelReport struct {
	ID             int64        `json:"id"`
	DatasetID      int64        `json:"dataset_id"`
	Name           string       `json:"name"`
	CreatedAt      time.Time    `json:"created_at"`
	Cfg            Config       `json:"cfg"`
	Regular        bool         `json:"regular"`
	NativeDt       float64      `json:"native_dt"`
	UsedDt         float64      `json:"used_dt"`
	Resampled      bool         `json:"resampled"`
	JitterMaxS     float64      `json:"jitter_max_s"`
	MissingCount   int          `json:"missing_count"`
	SaturatedCount int          `json:"saturated_count"`
	SatExcludedFit int          `json:"sat_excluded_fit"`
	NEst           int          `json:"n_est"`
	NVal           int          `json:"n_val"`
	TransformU     Transform    `json:"transform_u"`
	TransformY     Transform    `json:"transform_y"`
	Candidates     []*Candidate `json:"candidates"`
	BestID         int          `json:"best_id"`
	Published      bool         `json:"published"`
	PublishBlocked string       `json:"publish_blocked,omitempty"`
	Note           string       `json:"note"`
	// Series in engineering units on the modeling grid.
	GridT        []float64 `json:"grid_t"`
	GridU        []float64 `json:"grid_u"`
	GridY        []float64 `json:"grid_y"`
	GridSat      []bool    `json:"grid_sat"`
	GridValidEst []bool    `json:"grid_valid_est"`
	GridValidVal []bool    `json:"grid_valid_val"`
	// Best-model traces.
	OneStepEst []float64 `json:"onestep_est"`
	OneStepVal []float64 `json:"onestep_val"`
	SimEst     []float64 `json:"sim_est"`
	SimVal     []float64 `json:"sim_val"`
	ResidEst   []float64 `json:"resid_est"`
	ResidVal   []float64 `json:"resid_val"`
}

// PublishError blocks publishing (e.g. overlapping segments).
type PublishError struct{ Msg string }

func (e *PublishError) Error() string { return e.Msg }
