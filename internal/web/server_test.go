package web

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"dynidshop/internal/ident"
	"dynidshop/internal/store"
)

func testServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := NewServer(st); err != nil {
		t.Fatal(err)
	}
	// NewServer 用相对路径解析模板；测试 cwd 为包目录，模板就在 templates/ 下。
	srv, err := NewServer(st)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)
	return srv, ts
}

var noRedirect = &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
	return http.ErrUseLastResponse
}}

func postForm(t *testing.T, ts *httptest.Server, path string, form url.Values) *http.Response {
	t.Helper()
	resp, err := noRedirect.Post(ts.URL+path, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func runForm(estLo, estHi, valLo, valHi, delay string, resample bool, kinds ...string) url.Values {
	form := url.Values{}
	form.Set("name", "测试运行")
	form.Set("est_start", estLo)
	form.Set("est_end", estHi)
	form.Set("val_start", valLo)
	form.Set("val_end", valHi)
	form.Set("demean", "est")
	form.Set("detrend", "none")
	form.Set("missing", "interp")
	if resample {
		form.Set("resample", "1")
	}
	for i, k := range kinds {
		form.Add("kind", k)
		order := "1"
		if k == "ARX" || k == "SS" {
			if i == 0 {
				order = "2"
			}
		}
		form.Add("order", order)
		form.Add("delay", delay)
	}
	return form
}

func TestIndexShowsTitle(t *testing.T) {
	_, ts := testServer(t)
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "动态辨识工场") {
		t.Fatal("首页未显示标题")
	}
}

func TestFixtureThenRunAndCharts(t *testing.T) {
	srv, ts := testServer(t)
	id, err := srv.EnsureFixture()
	if err != nil {
		t.Fatal(err)
	}
	resp := postForm(t, ts, "/datasets/1/runs",
		runForm("6", "20", "30", "48", "0.2", true, "ARX", "FO"))
	if resp.StatusCode != 303 {
		t.Fatalf("创建运行状态 %d", resp.StatusCode)
	}
	_ = id
	page := get(t, ts.URL+"/runs/1")
	t.Logf("RUNPAGE contains banner-fail=%v", strings.Contains(page, "未能产生候选"))
	_ = srv
	for _, want := range []string{"候选比较", "残差自相关", "交叉相关", "自由仿真"} {
		if !strings.Contains(page, want) {
			t.Errorf("运行页缺少 %q", want)
		}
	}
	if strings.Count(page, "<svg") < 4 {
		t.Fatalf("SVG=%d len=%d chart=%v cand=%v params=%v", strings.Count(page, "<svg"), len(page),
			strings.Contains(page, "viewBox"), strings.Contains(page, "ARX"), strings.Contains(page, "参数定位"))
	}
}

func TestOverlapBlocksPublish(t *testing.T) {
	srv, ts := testServer(t)
	if _, err := srv.EnsureFixture(); err != nil {
		t.Fatal(err)
	}
	postForm(t, ts, "/datasets/1/runs", runForm("25", "35", "30", "48", "0.2", true, "ARX"))
	// 页面应显示阻断横幅
	page := get(t, ts.URL+"/runs/1")
	t.Logf("RUNPAGE contains banner-fail=%v", strings.Contains(page, "未能产生候选"))
	_ = srv
	if !strings.Contains(page, "禁止发布") {
		t.Error("重叠运行页面未显示阻断")
	}
	// 尝试发布应被服务端拒绝
	resp := postForm(t, ts, "/runs/1/publish", url.Values{})
	if !strings.HasSuffix(resp.Header.Get("Location"), "/runs/1?blocked=1") {
		t.Errorf("发布重定向异常: %s", resp.Header.Get("Location"))
	}
	rec, err := srv.St.GetRun(1)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Published || rec.Status != "blocked" {
		t.Fatalf("重叠运行不应被发布: %+v", rec)
	}
}

func TestNonUniformWithoutResampleIsError(t *testing.T) {
	srv, ts := testServer(t)
	if _, err := srv.EnsureFixture(); err != nil {
		t.Fatal(err)
	}
	postForm(t, ts, "/datasets/1/runs", runForm("25", "29", "31", "40", "0.2", false, "ARX"))
	rec, err := srv.St.GetRun(1)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != "error" || !strings.Contains(rec.BlockedReason, "非等间隔") {
		t.Fatalf("期望非等间隔错误，实际 status=%s reason=%s", rec.Status, rec.BlockedReason)
	}
}

func TestExportResetImportRecompute(t *testing.T) {
	srv, ts := testServer(t)
	if _, err := srv.EnsureFixture(); err != nil {
		t.Fatal(err)
	}
	postForm(t, ts, "/datasets/1/runs", runForm("6", "20", "30", "48", "0.2", true, "ARX", "FO"))
	exported := getBytes(t, ts.URL+"/api/export-run?id=1")
	var bundle struct {
		Run struct {
			Result json.RawMessage `json:"result"`
		} `json:"run"`
	}
	if err := json.Unmarshal(exported, &bundle); err != nil {
		t.Fatal(err)
	}
	var orig ident.RunResult
	if err := json.Unmarshal(bundle.Run.Result, &orig); err != nil {
		t.Fatal(err)
	}
	// 清空数据库
	postForm(t, ts, "/api/reset", url.Values{})
	if dss, _ := srv.St.ListDatasets(); len(dss) != 0 {
		t.Fatal("重置后数据集未清空")
	}
	// 重新导入导出文件，应重算并得到一致候选
	resp, err := uploadJSON(ts.URL+"/api/import-run", exported)
	if err != nil {
		t.Fatal(err)
	}
	finalURL := resp.Request.URL.Path
	resp.Body.Close()
	parts := strings.Split(strings.TrimRight(finalURL, "/"), "/")
	newID, _ := strconv.Atoi(parts[len(parts)-1])
	if resp.StatusCode != 200 || newID == 0 {
		t.Fatalf("导入未到达运行页: status=%d url=%s", resp.StatusCode, finalURL)
	}
	rec, err := srv.St.GetRun(int64(newID))
	if err != nil {
		t.Fatal(err)
	}
	var fresh ident.RunResult
	if err := json.Unmarshal(rec.Result, &fresh); err != nil {
		t.Fatal(err)
	}
	if len(fresh.Candidates) != len(orig.Candidates) {
		t.Fatalf("重算候选数 %d != 导出 %d", len(fresh.Candidates), len(orig.Candidates))
	}
	for i := range orig.Candidates {
		a, b := orig.Candidates[i], fresh.Candidates[i]
		if d := a.ValFit - b.ValFit; d > 1e-6 || d < -1e-6 {
			t.Errorf("候选 %d 验证拟合不一致: %.6f vs %.6f", i, a.ValFit, b.ValFit)
		}
	}
}

func get(t *testing.T, u string) string {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func getBytes(t *testing.T, u string) []byte {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func uploadJSON(u string, body []byte) (*http.Response, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "export.json")
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(body); err != nil {
		return nil, err
	}
	mw.Close()
	return http.Post(u, mw.FormDataContentType(), &buf)
}

func strconvAtoi(s string) (int, error) { return strconv.Atoi(s) }

func lastID(loc string) int64 {
	parts := strings.Split(loc, "/")
	v, _ := strconv.Atoi(parts[len(parts)-1])
	return int64(v)
}
