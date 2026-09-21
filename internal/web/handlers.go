package web

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"

	"dynidshop/internal/fixture"
	"dynidshop/internal/ident"
	"dynidshop/internal/store"
)

func logReq(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		h.ServeHTTP(w, r)
	})
}

func parseSpecs(r *http.Request) []ident.ModelSpec {
	kinds := r.Form["kind"]
	orders := r.Form["order"]
	delays := r.Form["delay"]
	specs := make([]ident.ModelSpec, 0, len(kinds))
	for i, k := range kinds {
		var order int
		var delay float64
		if i < len(orders) {
			order, _ = strconv.Atoi(orders[i])
		}
		if i < len(delays) {
			delay, _ = strconv.ParseFloat(delays[i], 64)
		}
		specs = append(specs, ident.ModelSpec{Kind: ident.ModelKind(k), Order: order, Delay: delay})
	}
	return specs
}

func ingestCSV(st *store.Store, id int64, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	cr := csv.NewReader(strings.NewReader(text))
	cr.FieldsPerRecord = -1
	samples := make([]store.Sample, 0)
	lineNo := 0
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		lineNo++
		if lineNo == 1 {
			// 允许首行为表头（包含非数字）
			if _, e := strconv.ParseFloat(strings.TrimSpace(rec[0]), 64); e != nil {
				continue
			}
		}
		if len(rec) < 3 {
			return fmt.Errorf("第 %d 行至少需要 t,u,y 三列", lineNo)
		}
		t, err := strconv.ParseFloat(strings.TrimSpace(rec[0]), 64)
		if err != nil {
			return fmt.Errorf("第 %d 行时间无法解析: %w", lineNo, err)
		}
		sm := store.Sample{T: t}
		sm.U = parseCell(rec[1])
		sm.Y = parseCell(rec[2])
		if len(rec) >= 4 {
			v, _ := strconv.Atoi(strings.TrimSpace(rec[3]))
			sm.Sat = v == 1
		}
		samples = append(samples, sm)
	}
	return st.BulkAddSamples(id, samples)
}

func parseCell(s string) *float64 {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "nan") || s == "NA" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &v
}

func (s *Server) handleImportFixture(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	id, err := s.fixtureDataset()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/datasets/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (s *Server) fixtureDataset() (int64, error) {
	raw := fixture.Generate()
	id, err := s.St.CreateDataset(
		"固定 fixture：一阶惯性+纯延迟",
		fmt.Sprintf("G(s)=%.1f e^(-%.1fs)/(%.1fs+1)；阶跃/PRBS/扫频，含时钟抖动、饱和与缺失",
			fixture.TrueGain, fixture.TrueDelay, fixture.TrueTau),
		fixture.Ts, true)
	if err != nil {
		return 0, err
	}
	samples := make([]store.Sample, len(raw))
	for i, x := range raw {
		samples[i] = store.Sample{T: x.T, U: x.U, Y: x.Y, Sat: x.Sat}
	}
	if err := s.St.BulkAddSamples(id, samples); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	if err := s.St.Reset(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// ExportBundle 是一次运行的完整导出（含数据集与输入），用于清空后重新导入复核。
type ExportBundle struct {
	Version int           `json:"version"`
	Dataset DatasetExport `json:"dataset"`
	Run     RunExport     `json:"run"`
}

type DatasetExport struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	TsNominal   float64        `json:"ts_nominal"`
	Fixture     bool           `json:"fixture"`
	Samples     []store.Sample `json:"samples"`
}

type RunExport struct {
	Name     string                  `json:"name"`
	Status   string                  `json:"status"`
	Segments ident.Segments          `json:"segments"`
	Prep     ident.PreprocessOptions `json:"prep"`
	Specs    []ident.ModelSpec       `json:"specs"`
	Result   json.RawMessage         `json:"result"`
}

func (s *Server) handleExportRun(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	rec, err := s.St.GetRun(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ds, err := s.St.GetDataset(rec.DatasetID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	samples, _ := s.St.LoadSamples(rec.DatasetID)
	var seg ident.Segments
	var prep ident.PreprocessOptions
	_ = json.Unmarshal(rec.Segments, &seg)
	_ = json.Unmarshal(rec.Prep, &prep)
	bundle := ExportBundle{
		Version: 1,
		Dataset: DatasetExport{
			Name: ds.Name, Description: ds.Description, TsNominal: ds.TsNominal,
			Fixture: ds.Fixture, Samples: samples,
		},
		Run: RunExport{
			Name: rec.Name, Status: rec.Status, Segments: seg, Prep: prep,
			Result: rec.Result,
		},
	}
	// specs 从 result 中提取
	var res ident.RunResult
	if err := json.Unmarshal(rec.Result, &res); err == nil {
		for _, c := range res.Candidates {
			bundle.Run.Specs = append(bundle.Run.Specs, c.Spec)
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="run-%d-export.json"`, id))
	writeSafeJSON(w, bundle)
}

func (s *Server) handleExportDataset(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	ds, err := s.St.GetDataset(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	samples, _ := s.St.LoadSamples(id)
	bundle := ExportBundle{Version: 1, Dataset: DatasetExport{
		Name: ds.Name, Description: ds.Description, TsNominal: ds.TsNominal,
		Fixture: ds.Fixture, Samples: samples,
	}}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="dataset-%d-export.json"`, id))
	writeSafeJSON(w, bundle)
}

func (s *Server) handleImportRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		http.Error(w, "表单解析失败: "+err.Error(), 400)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "缺少导出文件", 400)
		return
	}
	defer file.Close()
	var b ExportBundle
	if err := json.NewDecoder(file).Decode(&b); err != nil {
		http.Error(w, "导出文件无法解析: "+err.Error(), 400)
		return
	}
	if len(b.Dataset.Samples) == 0 {
		http.Error(w, "导出文件不含样本数据，无法复核", 400)
		return
	}
	dsID, err := s.St.CreateDataset(b.Dataset.Name+"（导入）", b.Dataset.Description, b.Dataset.TsNominal, b.Dataset.Fixture)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := s.St.BulkAddSamples(dsID, b.Dataset.Samples); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	// 重新计算并与导出结果对照
	in := ident.RunInput{Name: b.Run.Name, Segments: b.Run.Segments, Prep: b.Run.Prep,
		Specs: b.Run.Specs, TsNominal: b.Dataset.TsNominal}
	raw := samplesToRaw(b.Dataset.Samples)
	segJSON, _ := json.Marshal(b.Run.Segments)
	prepJSON, _ := json.Marshal(b.Run.Prep)
	rec := &store.RunRecord{DatasetID: dsID, Name: b.Run.Name + "（导入复核）",
		Segments: segJSON, Prep: prepJSON}
	res, runErr := ident.Run(raw, in)
	status, reason := "draft", ""
	var resultJSON []byte
	if runErr != nil {
		status, reason = "error", runErr.Error()
		resultJSON = []byte(`{"error":` + strconv.Quote(runErr.Error()) + `}`)
	} else {
		if res.Overlap {
			status, reason = "blocked", "估计段与验证段重叠：禁止发布"
		}
		resultJSON, _ = ident.MarshalJSONSafe(res)
	}
	rid, err := s.St.CreateRun(rec)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.St.UpdateRunResult(rid, resultJSON, status, reason)
	http.Redirect(w, r, "/runs/"+strconv.FormatInt(rid, 10), http.StatusSeeOther)
}

func (s *Server) compare(w http.ResponseWriter, r *http.Request) {
	ids := r.URL.Query()["id"]
	type item struct {
		Rec *store.RunRecord
		DS  *store.Dataset
		Res *ident.RunResult
	}
	items := make([]item, 0, len(ids))
	for _, idStr := range ids {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			continue
		}
		rec, err := s.St.GetRun(id)
		if err != nil {
			continue
		}
		ds, _ := s.St.GetDataset(rec.DatasetID)
		var res ident.RunResult
		_ = json.Unmarshal(rec.Result, &res)
		items = append(items, item{rec, ds, &res})
	}
	s.render(w, "compare", map[string]any{"Items": items})
}

func rawOverviewChart(samples []store.Sample, ts float64) string {
	u := LineSeries{Name: "u 输入", Color: "#888", X: make([]float64, 0), Y: make([]float64, 0)}
	y := LineSeries{Name: "y 输出", Color: "#2a6fb0", X: make([]float64, 0), Y: make([]float64, 0)}
	var sat Rect
	for _, sm := range samples {
		u.X = append(u.X, sm.T)
		y.X = append(y.X, sm.T)
		if sm.U == nil {
			u.Y = append(u.Y, math.NaN())
		} else {
			u.Y = append(u.Y, *sm.U)
		}
		if sm.Y == nil {
			y.Y = append(y.Y, math.NaN())
		} else {
			y.Y = append(y.Y, *sm.Y)
		}
		if sm.Sat {
			if sat.X1 == 0 {
				sat.X0 = sm.T
			}
			sat.X1 = sm.T + ts
			sat.Color = "#e0a030"
			sat.Label = "饱和"
		}
	}
	rects := []Rect{}
	if sat.X1 > 0 {
		rects = append(rects, sat)
	}
	return LineChart("原始数据（标记饱和段）", 900, 260, []LineSeries{u, y}, rects, "幅值")
}

// EnsureFixture 在数据库为空时导入固定 fixture，返回数据集 ID。
func (s *Server) EnsureFixture() (int64, error) {
	dss, err := s.St.ListDatasets()
	if err != nil {
		return 0, err
	}
	if len(dss) > 0 {
		return dss[0].ID, nil
	}
	return s.fixtureDataset()
}

func writeSafeJSON(w http.ResponseWriter, v any) {
	b, err := ident.MarshalJSONSafe(v)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Write(b)
}
