package ident

// genTestSeries 生成由 y(k)=0.7 y(k-1)+0.3 u(k-d) 驱动的等步序列。
// 输入为确定性 PRBS（每 2 拍切换）。
func genTestSeries(n, d int) ([]float64, []float64) {
	u := make([]float64, n)
	reg := uint8(0x7F)
	for k := 0; k < n; k++ {
		if k%4 == 0 {
			v := (reg >> 6) & 1
			fb := v ^ ((reg >> 1) & 1)
			reg = (reg << 1) | fb
			if v == 1 {
				u[k] = 1
			} else {
				u[k] = -1
			}
		} else {
			u[k] = u[k-1]
		}
	}
	y := make([]float64, n)
	for k := 0; k < n; k++ {
		var ud float64
		if k-d >= 0 {
			ud = u[k-d]
		}
		var yl float64
		if k > 0 {
			yl = y[k-1]
		}
		y[k] = 0.7*yl + 0.3*ud
	}
	return u, y
}

func toRaw(u, y []float64, ts float64) []RawSample {
	out := make([]RawSample, len(u))
	for k := range u {
		uu, yy := u[k], y[k]
		out[k] = RawSample{T: float64(k) * ts}
		if !isNaN(uu) {
			v := uu
			out[k].U = &v
		}
		if !isNaN(yy) {
			v := yy
			out[k].Y = &v
		}
	}
	return out
}

func isNaN(f float64) bool { return f != f }

// jitterRaw 生成非等间隔时间戳的一阶数据（时钟在中段抖动）。
func jitterRaw(n int) []RawSample {
	u, y := genTestSeries(n, 1)
	raw := toRaw(u, y, 0.1)
	cur := 0.0
	for k := 0; k < n; k++ {
		if k == 0 {
			raw[k].T = 0
			continue
		}
		step := 0.1
		if k >= 40 && k <= 70 {
			step *= 1 + 0.15*float64((k%3)-1)
		}
		cur += step
		raw[k].T = cur
	}
	return raw
}
