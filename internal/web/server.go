// Package web provides the local HTTP operation pages and JSON API for the
// dynamic identification workshop.
package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dynid/internal/fixture"
	"dynid/internal/ident"
	"dynid/internal/store"
)

//go:embed templates static
var assets embed.FS

type Server struct {
	st  *store.Store
	tpl *template.Template
}

func NewServer(st *store.Store) (*Server, error) {
	tpl := template.New("").Funcs(template.FuncMap{
		"fmtFloat": func(v float64) string {
			if v != v {
				return "—"
			}
			return strconv.FormatFloat(v, 'f', 3, 64)
		},
		"fmtFloat2": func(v float64) string {
			if v != v {
				return "—"
			}
			return strconv.FormatFloat(v, 'f', 2, 64)
		},
		"fmtFloat3": func(v float64) string {
			if v != v {
				return "—"
			}
			return strconv.FormatFloat(v, 'f', 3, 64)
		},
		"fmtSprintf": fmt.Sprint,
		"yn": func(b bool) string {
			if b {
				return "是"
			}
			return "否"
		},
		"complexityOf": func(c *ident.Candidate) int {
			switch c.Kind {
			case ident.ModelFirstOrder:
				return 3
			case ident.ModelARX:
				return 2*c.Order + 1
			default:
				return c.Order*c.Order + 2*c.Order + 1
			}
		},
		"stableOf": func(c *ident.Candidate) string {
			if c.RejectReason != "" {
				return "—"
			}
			for _, p := range c.Params.Poles {
				if p.Abs >= 1 {
					return "否"
				}
			}
			return "是"
		},
		"kindName": func(k ident.ModelKind) string {
			switch k {
			case ident.ModelARX:
				return "ARX"
			case ident.ModelStateSpace:
				return "状态空间"
			case ident.ModelFirstOrder:
				return "受限一阶"
			}
			return string(k)
		},
		"jsonHTML": func(v any) (template.JS, error) {
			b, err := json.Marshal(v)
			return template.JS(b), err
		},
		"add": func(a, b int) int { return a + b },
		"lt":  func(a, b float64) bool { return a < b },
	})
	if _, err := tpl.ParseFS(assets, "templates/*.html"); err != nil {
		return nil, err
	}
	return &Server{st: st, tpl: tpl}, nil
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /static/", func(w http.ResponseWriter, r *http.Request) {
		http.FileServerFS(assets).ServeHTTP(w, r)
	})
	mux.HandleFunc("GET /", s.handleHome)
	mux.HandleFunc("POST /datasets/load-fixture", s.handleLoadFixture)
	mux.HandleFunc("GET /datasets/{id}", s.handleDataset)
	mux.HandleFunc("POST /datasets/{id}/runs", s.handleCreateRun)
	mux.HandleFunc("GET /runs/{id}", s.handleRun)
	mux.HandleFunc("POST /runs/{id}/publish", s.handlePublish)
	mux.HandleFunc("GET /runs/{id}/export", s.handleRunExport)
	mux.HandleFunc("GET /compare", s.handleCompare)
	mux.HandleFunc("GET /export", s.handleExport)
	mux.HandleFunc("POST /import", s.handleImport)
	mux.HandleFunc("POST /reset", s.handleReset)
	return logRequests(mux)
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "模板渲染失败: "+err.Error(), 500)
	}
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	dss, err := s.st.ListDatasets()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	runs, err := s.st.ListRuns(0)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	type flash struct{ Kind, Msg string }
	s.render(w, "home.html", map[string]any{
		"Datasets": dss, "Runs": runs,
		"Flash": flashFromQuery(r),
	})
}

func (s *Server) handleLoadFixture(w http.ResponseWriter, r *http.Request) {
	sp := fixture.DefaultSpec()
	samples := fixture.Build(sp)
	note := fmt.Sprintf(
		"固定 fixture：标称采样 %.0fs，真延迟 %.0fs，时间常数 %.0fs，增益 %.1f；含 PRBS、单脉冲、阶跃、时钟抖动段、饱和段与缺失样本",
		sp.NominalDt, sp.DelayS, sp.TauS, sp.Gain)
	id, err := s.st.InsertDataset("固定辨识数据集", note,
		time.Now().Format("2006-01-02 15:04:05"), samples)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/datasets/%d?flash=fixture-loaded", id), http.StatusSeeOther)
}

func (s *Server) handleDataset(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	d, err := s.st.GetDataset(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	runs, err := s.st.ListRuns(id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	est, val := fixture.Segments()
	// Raw data SVG
	tmin, tmax := d.Samples[0].T, d.Samples[len(d.Samples)-1].T
	ts := make([]float64, len(d.Samples))
	us := make([]float64, len(d.Samples))
	ys := make([]float64, len(d.Samples))
	sat := make([]bool, len(d.Samples))
	for i, sm := range d.Samples {
		ts[i], us[i], sat[i] = sm.T, sm.U, sm.Sat
		if sm.Y != nil {
			ys[i] = *sm.Y
		} else {
			ys[i] = math.NaN()
		}
	}
	inputSVG := renderLineChart("输入 u（阶跃 / PRBS）", tmin, tmax, []svgSeries{
		{label: "u", color: "#2e7d32", t: ts, v: us},
	}, svgOptions{xLabel: "时间 t（秒）", gridT: ts, sat: sat,
		bands:      [][2]float64{{est.Start, est.End}, {val.Start, val.End}},
		bandColors: []string{"#eef4fb", "#fdf3e2"}})
	outputSVG := renderLineChart("输出 y（含饱和段与缺失）", tmin, tmax, []svgSeries{
		{label: "y", color: "#1f4e8c", t: ts, v: ys},
	}, svgOptions{xLabel: "时间 t（秒）", gridT: ts, sat: sat, zeroLine: true,
		bands:      [][2]float64{{est.Start, est.End}, {val.Start, val.End}},
		bandColors: []string{"#eef4fb", "#fdf3e2"}})

	// irregularity info on the suggested estimation window
	med, jit := spacingInfo(d.Samples, est)
	s.render(w, "dataset.html", map[string]any{
		"D": d, "Runs": runs, "Est": est, "Val": val,
		"MedDt": med, "Jitter": jit,
		"InputSVG": template.HTML(inputSVG), "OutputSVG": template.HTML(outputSVG),
		"Flash": flashFromQuery(r),
	})
}

func flashFromQuery(r *http.Request) struct{ Kind, Msg string } {
	switch r.URL.Query().Get("flash") {
	case "fixture-loaded":
		return struct{ Kind, Msg string }{"ok", "固定 fixture 已导入，可直接新建辨识运行。"}
	case "imported":
		return struct{ Kind, Msg string }{"ok", r.URL.Query().Get("info")}
	}
	if e := r.URL.Query().Get("err"); e != "" {
		return struct{ Kind, Msg string }{"err", e}
	}
	return struct{ Kind, Msg string }{}
}

func spacingInfo(sm []ident.Sample, seg ident.Segment) (float64, float64) {
	var gaps []float64
	prev := -1.0
	for _, s := range sm {
		if !seg.Contains(s.T) {
			continue
		}
		if prev >= 0 {
			gaps = append(gaps, s.T-prev)
		}
		prev = s.T
	}
	if len(gaps) == 0 {
		return 0, 0
	}
	med := median(gaps)
	jit := 0.0
	for _, g := range gaps {
		if r := absF(g-med) / med; r > jit {
			jit = r
		}
	}
	return med, jit
}

func median(x []float64) float64 {
	c := append([]float64(nil), x...)
	for i := 0; i < len(c); i++ {
		for j := i + 1; j < len(c); j++ {
			if c[j] < c[i] {
				c[i], c[j] = c[j], c[i]
			}
		}
	}
	if len(c)%2 == 1 {
		return c[len(c)/2]
	}
	return (c[len(c)/2-1] + c[len(c)/2]) / 2
}

func absF(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r)
	})
}

var _ = io.EOF
var _ = strings.TrimSpace
