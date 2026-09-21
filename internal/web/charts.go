package web

import (
	"fmt"
	"math"
	"strings"
)

// LineSeries 是一条折线。
type LineSeries struct {
	Name   string
	Color  string
	X, Y   []float64
	Width  float64
	Dashes string
}

// Rect 标记一个背景区域（用于估计/验证段、饱和段）。
type Rect struct {
	X0, X1 float64
	Color  string
	Label  string
}

// LineChart 渲染多序列折线 SVG。
func LineChart(title string, w, h int, xs []LineSeries, rects []Rect, ylabel string) string {
	padL, padR, padT, padB := 52.0, 14.0, 30.0, 34.0
	plotW := float64(w) - padL - padR
	plotH := float64(h) - padT - padB
	xmin, xmax, ymin, ymax := axisRange(xs)
	if ymin == ymax {
		ymin -= 1
		ymax += 1
	}
	yp := 0.08 * (ymax - ymin)
	ymin -= yp
	ymax += yp
	X := func(x float64) float64 { return padL + (x-xmin)/(xmax-xmin)*plotW }
	Y := func(y float64) float64 { return padT + (ymax-y)/(ymax-ymin)*plotH }

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" class="chart">`, w, h)
	fmt.Fprintf(&b, `<text x="%f" y="18" class="ctitle">%s</text>`, padL, esc(title))
	// 背景段
	for _, r := range rects {
		fmt.Fprintf(&b, `<rect x="%f" y="%f" width="%f" height="%f" fill="%s" opacity="0.12"/>`,
			X(r.X0), padT, math.Max(0, X(r.X1)-X(r.X0)), plotH, r.Color)
		if r.Label != "" {
			fmt.Fprintf(&b, `<text x="%f" y="%f" class="rlabel">%s</text>`,
				(X(r.X0)+X(r.X1))/2-14, padT+12, esc(r.Label))
		}
	}
	// 网格与坐标
	for i := 0; i <= 4; i++ {
		yy := padT + float64(i)*plotH/4
		val := ymax - float64(i)*(ymax-ymin)/4
		fmt.Fprintf(&b, `<line x1="%f" y1="%f" x2="%f" y2="%f" class="grid"/>`, padL, yy, padL+plotW, yy)
		fmt.Fprintf(&b, `<text x="%f" y="%f" class="tick">%s</text>`, padL-6, yy+3, fnum(val))
	}
	for i := 0; i <= 6; i++ {
		xx := padL + float64(i)*plotW/6
		val := xmin + float64(i)*(xmax-xmin)/6
		fmt.Fprintf(&b, `<text x="%f" y="%f" class="tick">%s</text>`, xx-14, padT+plotH+16, fnum(val))
	}
	fmt.Fprintf(&b, `<line x1="%f" y1="%f" x2="%f" y2="%f" class="axis"/>`, padL, padT, padL, padT+plotH)
	fmt.Fprintf(&b, `<line x1="%f" y1="%f" x2="%f" y2="%f" class="axis"/>`, padL, padT+plotH, padL+plotW, padT+plotH)
	if ylabel != "" {
		fmt.Fprintf(&b, `<text x="12" y="%f" class="ylabel" transform="rotate(-90 12 %f)">%s</text>`,
			padT+plotH/2, padT+plotH/2, esc(ylabel))
	}
	// 折线
	for _, s := range xs {
		wd := s.Width
		if wd == 0 {
			wd = 1.3
		}
		dash := ""
		if s.Dashes != "" {
			dash = ` stroke-dasharray="` + s.Dashes + `"`
		}
		started := false
		fmt.Fprintf(&b, `<path d="`)
		for i := range s.X {
			if math.IsNaN(s.Y[i]) {
				started = false
				continue
			}
			px, py := X(s.X[i]), Y(s.Y[i])
			if !started {
				fmt.Fprintf(&b, "M%f %f ", px, py)
				started = true
			} else {
				fmt.Fprintf(&b, "L%f %f ", px, py)
			}
		}
		fmt.Fprintf(&b, `" fill="none" stroke="%s" stroke-width="%f"%s/>`, s.Color, wd, dash)
	}
	// 图例
	lx := padL + 8
	ly := padT + 4
	for _, s := range xs {
		fmt.Fprintf(&b, `<line x1="%f" y1="%f" x2="%f" y2="%f" stroke="%s" stroke-width="2"/>`,
			lx, ly, lx+16, ly, s.Color)
		fmt.Fprintf(&b, `<text x="%f" y="%f" class="legend">%s</text>`, lx+20, ly+3, esc(s.Name))
		lx += 90
	}
	b.WriteString(`</svg>`)
	return b.String()
}

func axisRange(xs []LineSeries) (xmin, xmax, ymin, ymax float64) {
	first := true
	for _, s := range xs {
		for i := range s.X {
			if math.IsNaN(s.Y[i]) {
				continue
			}
			if first {
				xmin, xmax, ymin, ymax = s.X[i], s.X[i], s.Y[i], s.Y[i]
				first = false
				continue
			}
			if s.X[i] < xmin {
				xmin = s.X[i]
			}
			if s.X[i] > xmax {
				xmax = s.X[i]
			}
			if s.Y[i] < ymin {
				ymin = s.Y[i]
			}
			if s.Y[i] > ymax {
				ymax = s.Y[i]
			}
		}
	}
	return
}

// CorrChart 渲染 ACF 或 CCF 柱状图，含 ±bound 置信界。
func CorrChart(title string, w, h int, lags []int, vals []float64, bound float64) string {
	padL, padR, padT, padB := 48.0, 14.0, 30.0, 34.0
	plotW := float64(w) - padL - padR
	plotH := float64(h) - padT - padB
	lmin, lmax := 0, 1
	vmin, vmax := -1.0, 1.0
	if len(lags) > 0 {
		lmin, lmax = lags[0], lags[len(lags)-1]
	}
	X := func(l int) float64 { return padL + (float64(l-lmin))/(float64(lmax-lmin))*plotW }
	Y := func(v float64) float64 { return padT + (vmax-v)/(vmax-vmin)*plotH }

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" class="chart">`, w, h)
	fmt.Fprintf(&b, `<text x="%f" y="18" class="ctitle">%s</text>`, padL, esc(title))
	for g := 0; g < 10; g++ {
		val := 1 - float64(g)*0.2
		yy := Y(val)
		fmt.Fprintf(&b, `<line x1="%f" y1="%f" x2="%f" y2="%f" class="grid"/>`, padL, yy, padL+plotW, yy)
		if g%2 == 0 {
			fmt.Fprintf(&b, `<text x="%f" y="%f" class="tick">%.1f</text>`, padL-6, yy+3, val)
		}
	}
	zero := Y(0)
	fmt.Fprintf(&b, `<line x1="%f" y1="%f" x2="%f" y2="%f" class="axis"/>`, padL, zero, padL+plotW, zero)
	fmt.Fprintf(&b, `<line x1="%f" y1="%f" x2="%f" y2="%f" stroke="#d33" stroke-dasharray="4 3"/>`,
		padL, Y(bound), padL+plotW, Y(bound))
	fmt.Fprintf(&b, `<line x1="%f" y1="%f" x2="%f" y2="%f" stroke="#d33" stroke-dasharray="4 3"/>`,
		padL, Y(-bound), padL+plotW, Y(-bound))
	bw := plotW / float64(len(lags)) * 0.6
	for i, l := range lags {
		v := vals[i]
		col := "#4a7ec2"
		if math.Abs(v) > bound && l != 0 {
			col = "#d14c4c"
		}
		x := X(l) - bw/2
		y := math.Min(zero, Y(v))
		hh := math.Abs(Y(v) - zero)
		fmt.Fprintf(&b, `<rect x="%f" y="%f" width="%f" height="%f" fill="%s"/>`, x, y, bw, hh, col)
		if l%5 == 0 {
			fmt.Fprintf(&b, `<text x="%f" y="%f" class="tick">%d</text>`, X(l)-6, padT+plotH+16, l)
		}
	}
	b.WriteString(`</svg>`)
	return b.String()
}

func fnum(v float64) string {
	av := math.Abs(v)
	if av != 0 && (av >= 1000 || av < 0.01) {
		return fmt.Sprintf("%.1e", v)
	}
	return fmt.Sprintf("%.2f", v)
}

func esc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;")
	return r.Replace(s)
}
