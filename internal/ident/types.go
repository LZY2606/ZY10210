package ident

// RawSample 是数据库中保存的一条原始观测：时间戳（秒）、输入、输出、输出饱和标志。
type RawSample struct {
	T   float64
	U   *float64
	Y   *float64
	Sat bool
}

// Series 是一条等间隔（名义）采样序列，由 RawSample 重采样/网格化得到。
// U/Y 中的 NaN 表示缺失。
type Series struct {
	T0      float64   `json:"t0"`
	Ts      float64   `json:"ts"`
	T       []float64 `json:"t"`
	U       []float64 `json:"u"`
	Y       []float64 `json:"y"`
	Sat     []bool    `json:"sat"`
	Missing []bool    `json:"missing"`
	Uniform bool      `json:"uniform"`
	Jitter  float64   `json:"jitter"`
	// NonUniformSource 标记原始数据非等间隔（即使已重采样为等步网格）。
	NonUniformSource bool `json:"non_uniform_source"`
}

// Segments 描述估计段与验证段（以时间秒表示，左闭右开）。
type Segments struct {
	EstStart float64 `json:"est_start"`
	EstEnd   float64 `json:"est_end"`
	ValStart float64 `json:"val_start"`
	ValEnd   float64 `json:"val_end"`
}

// Overlap 返回估计段与验证段是否有正长度重叠。
func (s Segments) Overlap() bool {
	l := s.EstStart
	if s.ValStart > l {
		l = s.ValStart
	}
	r := s.EstEnd
	if s.ValEnd < r {
		r = s.ValEnd
	}
	return r-l > 0
}

// PreprocessOptions 为用户选择的预处理版本。
type PreprocessOptions struct {
	Demean   string `json:"demean"`   // "none" | "est" | "all"
	Detrend  string `json:"detrend"`  // "none" | "est" | "all"
	Missing  string `json:"missing"`  // "interp" | "ffill" | "error"
	Resample bool   `json:"resample"` // 非等间隔时是否重采样为等步网格
	SatFit   bool   `json:"sat_fit"`  // 饱和样本是否参与拟合（默认 false）
}

// ModelKind 为候选模型类型。
type ModelKind string

const (
	KindARX ModelKind = "ARX"
	KindSS  ModelKind = "SS"
	KindFO  ModelKind = "FO"
)

// ModelSpec 描述一个候选：类型、阶次、延迟（秒）。
type ModelSpec struct {
	Kind  ModelKind `json:"kind"`
	Order int       `json:"order"`
	Delay float64   `json:"delay"` // 秒
}

// SeriesPred 保存候选在整段网格上的预测、仿真与残差。
type SeriesPred struct {
	OneStep []float64 `json:"one_step"`
	FreeSim []float64 `json:"free_sim"`
	Resid   []float64 `json:"resid"`
}

// CorrTest 保存残差自相关（ACF）与输入-残差交叉相关（CCF）结果。
type CorrTest struct {
	Lags       []int     `json:"lags"`
	ACF        []float64 `json:"acf"`
	CCF        []float64 `json:"ccf"`
	Bound      float64   `json:"bound"`
	ACFInside  int       `json:"acf_inside"`
	ACFTotal   int       `json:"acf_total"`
	CCFInside  int       `json:"ccf_inside"`
	CCFTotal   int       `json:"ccf_total"`
	ACFMaxLag  int       `json:"acf_max_lag"`
	CCFPeakLag int       `json:"ccf_peak_lag"`
	Whiteness  string    `json:"whiteness"`
	Exogeneity string    `json:"exogeneity"`
	DelayHint  string    `json:"delay_hint"`
}

// CandidateResult 为一个候选模型的辨识结果。
type CandidateResult struct {
	Spec       ModelSpec  `json:"spec"`
	Dsteps     int        `json:"dsteps"`
	Params     []float64  `json:"params"`
	ParamNames []string   `json:"param_names"`
	Np         int        `json:"np"`
	Nres       int        `json:"nres"`
	EstFit     float64    `json:"est_fit"`
	ValFit     float64    `json:"val_fit"`
	EstRMSE    float64    `json:"est_rmse"`
	ValRMSE    float64    `json:"val_rmse"`
	SimFit     float64    `json:"sim_fit"`
	AIC        float64    `json:"aic"`
	BIC        float64    `json:"bic"`
	Whiteness  string     `json:"whiteness"`
	Exogeneity string     `json:"exogeneity"`
	DelayHint  string     `json:"delay_hint"`
	Pred       SeriesPred `json:"pred"`
	Corr       CorrTest   `json:"corr"`
	Error      string     `json:"error,omitempty"`
}

// FitModel 是统一的差分模型：A(q)y = B(q)u + e，用于预测/仿真/参数展示。
type FitModel struct {
	Na     int       `json:"na"`
	Nb     int       `json:"nb"`
	D      int       `json:"d"`
	A      []float64 `json:"a"` // 长度 Na+1, A[0]=1
	B      []float64 `json:"b"` // 长度 Nb+1, B[0] 对应 u(k-D)
	Stable bool      `json:"stable"`
}

// RunInput 为一次辨识运行的全部用户输入。
type RunInput struct {
	DatasetID int64             `json:"dataset_id"`
	Name      string            `json:"name"`
	Segments  Segments          `json:"segments"`
	Prep      PreprocessOptions `json:"prep"`
	Specs     []ModelSpec       `json:"specs"`
	TsNominal float64           `json:"ts_nominal"`
}
