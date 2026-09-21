// Package fixture 生成“动态辨识工场”的固定演示数据。
// 固定随机种子保证每次导入完全一致。
//
// 数据口径：被控对象为连续一阶惯性加纯延迟 G(s)=K e^{-Ls}/(Ts+1)，
// 输入为零阶保持。先在细网格（h=Ts/200）上物理仿真，再在记录时刻采样；
// 其中一段记录时钟有 ±12% 抖动，因此原始数据不是等间隔的，
// 必须显式重采样后才能作为离散等步模型处理。
package fixture

import (
	"math"
	"math/rand"

	"dynidshop/internal/ident"
)

const (
	// Ts 为名义采样周期（秒）。
	Ts = 0.1
	// N 为样本总数（按名义网格）。
	N = 480
	// TrueTau / TrueGain / TrueDelay 为被控对象真值，供页面与 README 对照。
	TrueTau   = 0.5
	TrueGain  = 2.0
	TrueDelay = 0.2
	// SatLimit 为输出饱和幅值。
	SatLimit = 1.6
	// DefaultEst / DefaultVal 为默认估计段与验证段（无重叠，可发布）。
	DefaultEstLo, DefaultEstHi = 0.0, 30.0
	DefaultValLo, DefaultValHi = 30.0, 48.0
)

// piece 表示输入在 [t0,t1) 上为零阶保持值 val。
type piece struct{ t0, t1, val float64 }

// Generate 生成固定 fixture（含一个时钟抖动段、一段输出饱和、两个缺失）。
func Generate() []ident.RawSample {
	rng := rand.New(rand.NewSource(20260922))

	// 1) 构造分段常数输入（连续时间轴）。
	pieces := make([]piece, 0)
	add := func(t0, t1, v float64) { pieces = append(pieces, piece{t0, t1, v}) }
	add(0, 6, 0)
	add(6, 10, 1)
	kEnd := 300
	pv := 1
	for k := 100; k < kEnd; k += 3 {
		pv++
		v := 0.8
		if pv%2 == 0 {
			v = -0.8
		}
		t0 := float64(k) * Ts
		t1 := float64(k+3) * Ts
		if t1 > 30 {
			t1 = 30
		}
		add(t0, t1, v)
	}
	// chirp 30–48 s
	tt := func(k int) float64 { return float64(k)*Ts - 30 }
	for k := 300; k < N; k++ {
		ttk := tt(k)
		f0, f1 := 0.05, 0.8
		ph := 2 * math.Pi * (f0*ttk + (f1-f0)/(2*18)*ttk*ttk)
		v := 0.7 * math.Sin(ph)
		t0 := float64(k) * Ts
		t1 := float64(k+1) * Ts
		if t1 > 48 {
			t1 = 48
		}
		add(t0, t1, v)
	}
	uAt := func(tm float64) float64 {
		for _, p := range pieces {
			if tm >= p.t0 && tm < p.t1 {
				return p.val
			}
		}
		if len(pieces) > 0 {
			return pieces[len(pieces)-1].val
		}
		return 0
	}

	// 2) 在细网格上对一阶惯性 + 输入延迟物理仿真（ZOH 精确递推）。
	const sub = 200
	h := Ts / sub
	total := float64(N) * Ts
	nStep := int(math.Round(total/h)) + 1
	state := 0.0
	delaySteps := int(math.Round(TrueDelay / h))
	buf := make([]float64, delaySteps+1)
	aH := math.Exp(-h / TrueTau)
	denseT := make([]float64, nStep)
	denseY := make([]float64, nStep)
	for i := 0; i < nStep; i++ {
		tm := float64(i) * h
		ud := buf[delaySteps]
		for j := delaySteps; j > 0; j-- {
			buf[j] = buf[j-1]
		}
		buf[0] = uAt(tm)
		state = aH*state + TrueGain*(1-aH)*ud
		denseT[i] = tm
		denseY[i] = state + 0.004*rng.NormFloat64()
	}
	sample := func(tm float64) float64 {
		idx := tm / h
		i := int(math.Floor(idx))
		if i >= len(denseY)-1 {
			return denseY[len(denseY)-1]
		}
		w := idx - float64(i)
		return denseY[i]*(1-w) + denseY[i+1]*w
	}

	// 3) 记录时刻：名义网格，但 k=200..240 有 ±12% 确定性抖动。
	recT := make([]float64, N)
	cur := 0.0
	for k := 0; k < N; k++ {
		if k == 0 {
			recT[k] = 0
			continue
		}
		step := Ts
		if k >= 200 && k <= 240 {
			step = Ts * (1 + 0.12*math.Sin(float64(k)*1.7))
		}
		cur += step
		recT[k] = cur
	}

	// 4) 在记录时刻采样输入/输出；输出在 k=260..290 饱和钳位；两个固定缺失。
	sat := make([]bool, N)
	missing := map[int]string{350: "y", 420: "u"}
	out := make([]ident.RawSample, N)
	for k := 0; k < N; k++ {
		tm := recT[k]
		uu := uAt(tm)
		yy := sample(tm)
		if k >= 260 && k <= 290 {
			sat[k] = true
			if yy > SatLimit {
				yy = SatLimit
			}
			if yy < -SatLimit {
				yy = -SatLimit
			}
		}
		var up, yp *float64
		switch missing[k] {
		case "u":
			up, yp = nil, &yy
		case "y":
			up, yp = &uu, nil
		default:
			up, yp = &uu, &yy
		}
		out[k] = ident.RawSample{T: tm, U: up, Y: yp, Sat: sat[k]}
	}
	return out
}
