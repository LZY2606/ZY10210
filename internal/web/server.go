package web

import (
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"

	"dynidshop/internal/fixture"
	"dynidshop/internal/ident"
	"dynidshop/internal/store"
)

type Server struct {
	St       *store.Store
	Tmpl     *template.Template
	FixtureN int
}

func NewServer(st *store.Store) (*Server, error) {
	t, err := template.New("").Funcs(template.FuncMap{
		"f2":   func(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) },
		"f3":   func(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) },
		"f4":   func(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) },
		"json": func(v any) (template.JS, error) { b, err := json.Marshal(v); return template.JS(b), err },
	}).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	s := &Server{St: st, Tmpl: t, FixtureN: fixture.N}
	return s, nil
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/datasets", s.handleDatasets)
	mux.HandleFunc("/datasets/", s.handleDatasetSub)
	mux.HandleFunc("/runs/", s.handleRunSub)
	mux.HandleFunc("/api/import-fixture", s.handleImportFixture)
	mux.HandleFunc("/api/reset", s.handleReset)
	mux.HandleFunc("/api/export-run", s.handleExportRun)
	mux.HandleFunc("/api/import-run", s.handleImportRun)
	mux.HandleFunc("/api/export-dataset", s.handleExportDataset)
	return logReq(mux)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	dss, err := s.St.ListDatasets()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, "index", map[string]any{"Title": "动态辨识工场", "Datasets": dss})
}

func (s *Server) handleDatasets(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		name := r.FormValue("name")
		desc := r.FormValue("description")
		ts, _ := strconv.ParseFloat(r.FormValue("ts"), 64)
		if name == "" || ts <= 0 {
			http.Error(w, "名称与采样周期必填且为正", 400)
			return
		}
		id, err := s.St.CreateDataset(name, desc, ts, false)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		// 简单 CSV 导入：每行 t,u,y[,sat]，空字段为缺失
		if err := ingestCSV(s.St, id, r.FormValue("csv")); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		http.Redirect(w, r, "/datasets/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
		return
	}
	http.Error(w, "method not allowed", 405)
}

func (s *Server) handleDatasetSub(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Path[len("/datasets/"):]
	// /datasets/{id} 或 /datasets/{id}/runs
	id, err := strconv.ParseInt(idSegment(idStr), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if idStr == idSegment(idStr)+"/runs" && r.Method == http.MethodPost {
		s.createRun(w, r, id)
		return
	}
	s.datasetPage(w, r, id)
}

func idSegment(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return s[:i]
		}
	}
	return s
}

func (s *Server) datasetPage(w http.ResponseWriter, r *http.Request, id int64) {
	ds, err := s.St.GetDataset(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	samples, err := s.St.LoadSamples(id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	runs, err := s.St.ListRuns(id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	chart := rawOverviewChart(samples, ds.TsNominal)
	s.render(w, "dataset", map[string]any{
		"Title": "动态辨识工场", "DS": ds, "Runs": runs, "Chart": template.HTML(chart),
		"N": len(samples),
	})
}

func (s *Server) createRun(w http.ResponseWriter, r *http.Request, dsID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	f := func(k string) float64 {
		v, _ := strconv.ParseFloat(r.FormValue(k), 64)
		return v
	}
	name := r.FormValue("name")
	seg := ident.Segments{
		EstStart: f("est_start"), EstEnd: f("est_end"),
		ValStart: f("val_start"), ValEnd: f("val_end"),
	}
	prep := ident.PreprocessOptions{
		Demean:   r.FormValue("demean"),
		Detrend:  r.FormValue("detrend"),
		Missing:  r.FormValue("missing"),
		Resample: r.FormValue("resample") == "1",
		SatFit:   r.FormValue("sat_fit") == "1",
	}
	specs := parseSpecs(r)
	in := ident.RunInput{DatasetID: dsID, Name: name, Segments: seg, Prep: prep, Specs: specs,
		TsNominal: 0}
	ds, err := s.St.GetDataset(dsID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	in.TsNominal = ds.TsNominal
	samples, err := s.St.LoadSamples(dsID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	raw := samplesToRaw(samples)

	segJSON, _ := json.Marshal(seg)
	prepJSON, _ := json.Marshal(prep)
	rec := &store.RunRecord{DatasetID: dsID, Name: name, Status: "draft",
		Segments: segJSON, Prep: prepJSON}
	result, runErr := ident.Run(raw, in)
	status, reason := "draft", ""
	var resultJSON []byte
	if runErr != nil {
		status = "error"
		reason = runErr.Error()
		resultJSON = []byte(`{"error":` + strconv.Quote(runErr.Error()) + `}`)
	} else {
		if result.Overlap {
			status = "blocked"
			reason = "估计段与验证段重叠：禁止发布"
		}
		resultJSON, _ = ident.MarshalJSONSafe(result)
	}
	id, err := s.St.CreateRun(rec)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := s.St.UpdateRunResult(id, resultJSON, status, reason); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/runs/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (s *Server) handleRunSub(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path[len("/runs/"):]
	idStr := idSegment(path)
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case path == idStr+"/publish":
		s.publish(w, r, id)
	case path == idStr+"/unpublish":
		s.unpublish(w, r, id)
	case path == idStr+"/compare":
		s.compare(w, r)
	default:
		s.runPage(w, r, id)
	}
}

func (s *Server) publish(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	rec, err := s.St.GetRun(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// 服务端强制阻断：重叠区间永远不能发布，即使绕过页面。
	var res ident.RunResult
	_ = json.Unmarshal(rec.Result, &res)
	if res.Overlap {
		s.St.SetPublished(id, false, "blocked", "估计段与验证段重叠：禁止发布")
		http.Redirect(w, r, "/runs/"+strconv.FormatInt(id, 10)+"?blocked=1", http.StatusSeeOther)
		return
	}
	s.St.SetPublished(id, true, "published", "")
	http.Redirect(w, r, "/runs/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (s *Server) unpublish(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	s.St.SetPublished(id, false, "draft", "")
	http.Redirect(w, r, "/runs/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (s *Server) runPage(w http.ResponseWriter, r *http.Request, id int64) {
	rec, err := s.St.GetRun(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ds, _ := s.St.GetDataset(rec.DatasetID)
	var res ident.RunResult
	_ = json.Unmarshal(rec.Result, &res)
	candIdx := 0
	if v := r.URL.Query().Get("cand"); v != "" {
		candIdx, _ = strconv.Atoi(v)
	}
	view := buildRunView(s, rec, &res, ds, candIdx)
	view.BlockedFlash = r.URL.Query().Get("blocked") == "1"
	s.render(w, "run", view)
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if data == nil {
		data = map[string]any{}
	}
	if err := s.Tmpl.ExecuteTemplate(w, name+".html", data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

func samplesToRaw(sm []store.Sample) []ident.RawSample {
	out := make([]ident.RawSample, len(sm))
	for i, x := range sm {
		out[i] = ident.RawSample{T: x.T, U: x.U, Y: x.Y, Sat: x.Sat}
	}
	return out
}
