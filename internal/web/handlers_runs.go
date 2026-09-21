package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"dynid/internal/ident"
)

func parseFloats(s string) []float64 {
	var out []float64
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == ';' || r == '\n' }) {
		if v, err := strconv.ParseFloat(strings.TrimSpace(p), 64); err == nil {
			out = append(out, v)
		}
	}
	return out
}

func parseInts(s string) []int {
	var out []int
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == ';' || r == '\n' }) {
		if v, err := strconv.Atoi(strings.TrimSpace(p)); err == nil && v >= 1 {
			out = append(out, v)
		}
	}
	return out
}

func (s *Server) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	d, err := s.st.GetDataset(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	f := func(k string) float64 {
		v, _ := strconv.ParseFloat(strings.TrimSpace(r.FormValue(k)), 64)
		return v
	}
	cfg := ident.Config{
		DatasetID:        id,
		Name:             r.FormValue("name"),
		Est:              ident.Segment{Start: f("est_start"), End: f("est_end")},
		Val:              ident.Segment{Start: f("val_start"), End: f("val_end")},
		Detrend:          ident.DetrendMode(r.FormValue("detrend")),
		Missing:          ident.MissingPolicy(r.FormValue("missing")),
		Resample:         r.FormValue("resample") == "1",
		Dt:               f("dt"),
		Delays:           parseFloats(r.FormValue("delays")),
		Orders:           parseInts(r.FormValue("orders")),
		Note:             r.FormValue("note"),
		IncludeSaturated: r.FormValue("include_sat") == "1",
	}
	if cfg.Name == "" {
		cfg.Name = "辨识运行"
	}
	rep, err := ident.Run(id, cfg.Name, d.Samples, cfg, time.Now())
	if err != nil {
		// Technical failure (e.g. irregular data without explicit resample):
		// bounce back with the reason, nothing is persisted.
		msg := err.Error()
		http.Redirect(w, r, "/datasets/"+strconv.FormatInt(id, 10)+"?err="+urlQuery(msg), http.StatusSeeOther)
		return
	}
	rid, err := s.st.InsertRun(rep)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/runs/"+strconv.FormatInt(rid, 10), http.StatusSeeOther)
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	rep, err := s.st.GetRun(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	d, _ := s.st.GetDataset(rep.DatasetID)
	view := buildRunView(rep)
	view.DatasetName = ""
	if d != nil {
		view.DatasetName = d.Name
	}
	view.Flash = flashFromQuery(r)
	s.render(w, "run.html", view)
}

func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	rep, err := s.st.GetRun(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	action := r.URL.Query().Get("action")
	if action == "unpublish" {
		_ = s.st.SetPublished(id, false, "")
		rep.Published = false
		rep.PublishBlocked = ""
		http.Redirect(w, r, "/runs/"+strconv.FormatInt(id, 10)+"?flash=unpublished", http.StatusSeeOther)
		return
	}
	// Gate re-evaluated from stored configuration, not from UI state.
	if rep.Cfg.Est.Overlaps(rep.Cfg.Val) {
		rep.Published = false
		rep.PublishBlocked = "估计段与验证段重叠，禁止发布"
		_ = s.st.SetPublished(id, false, rep.PublishBlocked)
		http.Redirect(w, r, "/runs/"+strconv.FormatInt(id, 10)+"?err="+urlQuery(rep.PublishBlocked), http.StatusSeeOther)
		return
	}
	if rep.BestID == 0 {
		msg := "没有可发布的合格候选（全部被拒或无法辨识）"
		_ = s.st.SetPublished(id, false, msg)
		http.Redirect(w, r, "/runs/"+strconv.FormatInt(id, 10)+"?err="+urlQuery(msg), http.StatusSeeOther)
		return
	}
	rep.Published = true
	rep.PublishBlocked = ""
	if err := s.st.SetPublished(id, true, ""); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/runs/"+strconv.FormatInt(id, 10)+"?flash=published", http.StatusSeeOther)
}

func (s *Server) handleRunExport(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	rep, err := s.st.GetRun(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition",
		"attachment; filename=\"run-"+strconv.FormatInt(id, 10)+".json\"")
	raw, err := ident.MarshalJSONSafe(rep)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_, _ = w.Write(raw)
}

func (s *Server) handleCompare(w http.ResponseWriter, r *http.Request) {
	a, _ := strconv.ParseInt(r.URL.Query().Get("a"), 10, 64)
	b, _ := strconv.ParseInt(r.URL.Query().Get("b"), 10, 64)
	all, err := s.st.ListRuns(0)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	data := map[string]any{"All": all, "AID": a, "BID": b}
	if a > 0 && b > 0 {
		ra, e1 := s.st.GetRun(a)
		rb, e2 := s.st.GetRun(b)
		if e1 == nil && e2 == nil {
			data["Cmp"] = compareRuns(ra, rb)
		}
	}
	s.render(w, "compare.html", data)
}
