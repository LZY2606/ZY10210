package web

import (
	"fmt"
	"math"
	"strings"
)

const (
	svgW = 860.0
	svgH = 240.0
	ml   = 52.0
	mr   = 14.0
	mt   = 16.0
	mb   = 30.0
)

type pt struct{ x, y float64 }

type svgSeries struct {
	label string
	color string
	t, v  []float64
	width float64
	dash  string
}

type svgOptions struct {
	title      string
	xLabel     string
	yLabel     string
	bands      [][2]float64 // shaded vertical bands (estimate/validation)
	bandColors []string
	missing    []bool
	sat        []bool
	zeroLine   bool
	threshold  float64   // horizontal threshold lines +/- this value
	gridT      []float64 // grid times aligned with sat/missing flags
}

func lineChart(t, v []float64, x0, x1, y0, y1, lo, hi, tmin, tmax float64) []pt {
	var pts []pt
	for i := range t {
		if math.IsNaN(v[i]) {
			continue
		}
		x := x0
		if tmax > tmin {
			x = x0 + (t[i]-tmin)/(tmax-tmin)*(x1-x0)
		}
		y := y1 - (v[i]-lo)/(hi-lo)*(y1-y0)
		pts = append(pts, pt{x, y})
	}
	return pts
}

func renderLineChart(title string, tmin, tmax float64, series []svgSeries, opt svgOptions) string {
	w, h := svgW, svgH
	x0, x1 := ml, w-mr
	y0, y1 := mt, h-mb

	lo, hi := math.Inf(1), math.Inf(-1)
	for _, s := range series {
		for _, v := range s.v {
			if math.IsNaN(v) {
				continue
			}
			if v < lo {
				lo = v
			}
			if v > hi {
				hi = v
			}
		}
	}
	if !math.IsInf(lo, 1) {
		pad := (hi - lo) * 0.08
		if pad == 0 {
			pad = 1
		}
		lo -= pad
		hi += pad
	} else {
		lo, hi = -1, 1
	}

	var b strings.Builder
	fmt.Fprintf(&b,
		`<svg viewBox="0 0 %.0f %.0f" xmlns="http://www.w3.org/2000/svg" class="chart" role="img" aria-label="%s">`,
		w, h, title)
	fmt.Fprintf(&b, `<rect x="0" y="0" width="%.0f" height="%.0f" fill="#fff"/>`, w, h)

	// segment bands
	for i, bd := range opt.bands {
		col := "#eef4fb"
		if i < len(opt.bandColors) {
			col = opt.bandColors[i]
		}
		xa := x0 + (bd[0]-tmin)/(tmax-tmin)*(x1-x0)
		xb := x0 + (bd[1]-tmin)/(tmax-tmin)*(x1-x0)
		fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="%s"/>`,
			xa, y0, xb-xa, y1-y0, col)
	}
	// grid + y labels
	for g := 0; g <= 4; g++ {
		yy := y0 + float64(g)/4*(y1-y0)
		val := hi - float64(g)/4*(hi-lo)
		fmt.Fprintf(&b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="#eee"/>`, x0, yy, x1, yy)
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" class="al" text-anchor="end">%.2g</text>`, x0-5, yy+3, val)
	}
	// axes
	fmt.Fprintf(&b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="#333"/>`, x0, y1, x1, y1)
	fmt.Fprintf(&b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="#333"/>`, x0, y0, x0, y1)
	for g := 0; g <= 6; g++ {
		xx := x0 + float64(g)/6*(x1-x0)
		tv := tmin + float64(g)/6*(tmax-tmin)
		fmt.Fprintf(&b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="#333"/>`, xx, y1, xx, y1+4)
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" class="al" text-anchor="middle">%.0f</text>`, xx, y1+17, tv)
	}
	fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" class="tt">%s</text>`, x0, 12.0, title)
	fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" class="al" text-anchor="middle">%s</text>`, (x0+x1)/2, h-3, opt.xLabel)

	// saturation markers aligned to the modeling grid
	if len(opt.sat) > 0 && len(opt.gridT) == len(opt.sat) {
		for i, sat := range opt.sat {
			if !sat {
				continue
			}
			tg := opt.gridT[i]
			if tg < tmin || tg > tmax {
				continue
			}
			xx := x0 + (tg-tmin)/(tmax-tmin)*(x1-x0)
			fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="2" height="%.1f" fill="#e8a03a" opacity="0.65"/>`,
				xx-1, y0, y1-y0)
		}
	}

	// zero line
	if opt.zeroLine && lo < 0 && hi > 0 {
		zy := y1 - (0-lo)/(hi-lo)*(y1-y0)
		fmt.Fprintf(&b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="#999" stroke-dasharray="3 3"/>`,
			x0, zy, x1, zy)
	}
	if opt.threshold > 0 {
		for _, tv := range []float64{-opt.threshold, opt.threshold} {
			if tv > lo && tv < hi {
				yy := y1 - (tv-lo)/(hi-lo)*(y1-y0)
				fmt.Fprintf(&b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="#c63" stroke-dasharray="5 3"/>`,
					x0, yy, x1, yy)
			}
		}
	}

	for _, s := range series {
		pts := lineChart(s.t, s.v, x0, x1, y0, y1, lo, hi, tmin, tmax)
		var d strings.Builder
		mov := true
		lastT := math.NaN()
		for _, p := range pts {
			if !mov && !math.IsNaN(lastT) {
				d.WriteString("L")
			} else {
				d.WriteString("M")
				mov = false
			}
			fmt.Fprintf(&d, "%.1f,%.1f ", p.x, p.y)
			lastT = p.x
		}
		wd := s.width
		if wd == 0 {
			wd = 1.4
		}
		dash := ""
		if s.dash != "" {
			dash = ` stroke-dasharray="` + s.dash + `"`
		}
		fmt.Fprintf(&b, `<path d="%s" fill="none" stroke="%s" stroke-width="%.1f"%s/>`,
			d.String(), s.color, wd, dash)
	}

	// legend
	lx := x1 - 10
	ly := y0 + 4
	for i := len(series) - 1; i >= 0; i-- {
		s := series[i]
		fmt.Fprintf(&b,
			`<rect x="%.1f" y="%.1f" width="10" height="3" fill="%s"/><text x="%.1f" y="%.1f" class="lg" text-anchor="end">%s</text>`,
			lx, ly, s.color, lx-14, ly+5, s.label)
		ly += 13
	}
	b.WriteString(`</svg>`)
	return b.String()
}

// renderCorrChart draws ACF/CCF bars with the +/-1.96/sqrt(N) band.
func renderCorrChart(title string, vals []float64, lagFrom int, band float64) string {
	w, h := svgW, svgH
	x0, x1 := ml, w-mr
	y0, y1 := mt+10, h-mb
	lo, hi := -1.0, 1.0
	var sb strings.Builder
	fmt.Fprintf(&sb,
		`<svg viewBox="0 0 %.0f %.0f" xmlns="http://www.w3.org/2000/svg" class="chart" role="img" aria-label="%s">`,
		w, h, title)
	fmt.Fprintf(&sb, `<rect width="%.0f" height="%.0f" fill="#fff"/>`, w, h)
	// bands
	by := y1 - band/(hi-lo)*(y1-y0)
	by2 := y1 + band/(hi-lo)*(y1-y0)
	fmt.Fprintf(&sb, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="#fdecec"/>`,
		x0, by, x1-x0, by2-by)
	zero := y1
	fmt.Fprintf(&sb, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="#333"/>`, x0, zero, x1, zero)
	n := len(vals)
	bw := (x1 - x0) / float64(n)
	for i, v := range vals {
		yy := y1 - v/(hi-lo)*(y1-y0)
		col := "#3569a8"
		outside := math.Abs(v) > band && i+lagFrom != 0
		if outside {
			col = "#c0392b"
		}
		if i+lagFrom == 0 {
			col = "#888"
		}
		cx := x0 + (float64(i)+0.5)*bw
		fmt.Fprintf(&sb, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="%.1f"/>`,
			cx, zero, cx, yy, col, math.Max(bw*0.6, 1))
	}
	// band lines
	for _, yb := range []float64{by, by2} {
		fmt.Fprintf(&sb, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="#c0392b" stroke-dasharray="4 3"/>`,
			x0, yb, x1, yb)
	}
	// x labels every 5 lags
	for i := 0; i < n; i += 5 {
		cx := x0 + (float64(i)+0.5)*bw
		fmt.Fprintf(&sb, `<text x="%.1f" y="%.1f" class="al" text-anchor="middle">%d</text>`, cx, y1+16, i+lagFrom)
	}
	fmt.Fprintf(&sb, `<text x="%.1f" y="12" class="tt">%s（95%% 置信带 ±%.2f）</text>`, x0, title, band)
	fmt.Fprintf(&sb, `<text x="%.1f" y="%.1f" class="al" text-anchor="middle">滞后（采样步）</text>`, (x0+x1)/2, h-3)
	sb.WriteString(`</svg>`)
	return sb.String()
}
