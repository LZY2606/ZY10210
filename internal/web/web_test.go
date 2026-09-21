package web_test

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"dynid/internal/store"
	"dynid/internal/web"
)

func newTestServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv, err := web.NewServer(st)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)
	return ts, st
}

func get(t *testing.T, ts *httptest.Server, path string) (int, string) {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

var noRedirect = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func postForm(t *testing.T, ts *httptest.Server, path string, v url.Values) (int, string, string) {
	t.Helper()
	resp, err := noRedirect.PostForm(ts.URL+path, v)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Location"), string(b)
}

func TestHomeShowsWorkshopTitle(t *testing.T) {
	ts, _ := newTestServer(t)
	code, body := get(t, ts, "/")
	if code != 200 || !strings.Contains(body, "动态辨识工场") {
		t.Fatal("首页应显示“动态辨识工场”")
	}
}

func loadFixture(t *testing.T, ts *httptest.Server) {
	t.Helper()
	code, loc, _ := postForm(t, ts, "/datasets/load-fixture", url.Values{})
	if code != 303 || !strings.HasPrefix(loc, "/datasets/") {
		t.Fatalf("载入 fixture 失败: %d %s", code, loc)
	}
}

func formFor(name, es, ee, vs, ve, delays, orders string, extra url.Values) url.Values {
	v := url.Values{}
	v.Set("name", name)
	v.Set("est_start", es)
	v.Set("est_end", ee)
	v.Set("val_start", vs)
	v.Set("val_end", ve)
	v.Set("detrend", "mean")
	v.Set("missing", "interpolate")
	v.Set("delays", delays)
	v.Set("orders", orders)
	for k := range extra {
		v.Set(k, extra.Get(k))
	}
	return v
}

func TestEndToEndIdentificationAndCharts(t *testing.T) {
	ts, _ := newTestServer(t)
	loadFixture(t, ts)
	v := formFor("主运行", "20", "140", "240", "325", "0,1,2,3,4,5", "1,2,3", nil)
	code, loc, _ := postForm(t, ts, "/datasets/1/runs", v)
	if code != 303 || !strings.HasPrefix(loc, "/runs/") {
		t.Fatalf("创建运行失败: %d %s", code, loc)
	}
	_, body := get(t, ts, loc)
	for _, want := range []string{
		"最优候选", "死区延迟 3.00 秒", "残差自相关", "交叉相关",
		"自由仿真", "一步预测", "<svg",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("运行页缺少 %q", want)
		}
	}
}

func TestOverlapShowsPublishingBlocker(t *testing.T) {
	ts, _ := newTestServer(t)
	loadFixture(t, ts)
	v := formFor("重叠", "20", "140", "120", "200", "2,3", "1", nil)
	code, loc, _ := postForm(t, ts, "/datasets/1/runs", v)
	if code != 303 || !strings.HasPrefix(loc, "/runs/") {
		t.Fatalf("重叠运行仍应入库为阻断态: %d %s", code, loc)
	}
	_, body := get(t, ts, loc)
	if !strings.Contains(body, "禁止发布") {
		t.Fatal("重叠运行页必须显示发布阻断")
	}
	// publish attempt must bounce back with err and not succeed.
	resp, err := noRedirect.PostForm(ts.URL+loc+"/publish?action=publish", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !strings.Contains(resp.Header.Get("Location"), "err=") {
		t.Fatal("重叠运行发布必须被拒绝")
	}
}

func TestIrregularWithoutResampleRejected(t *testing.T) {
	ts, _ := newTestServer(t)
	loadFixture(t, ts)
	v := formFor("抖动", "152", "220", "240", "320", "0,3", "1", nil)
	code, loc, _ := postForm(t, ts, "/datasets/1/runs", v)
	if code != 303 || !strings.Contains(loc, "err=") {
		t.Fatalf("非等间隔未重采样必须拒绝: %d %s", code, loc)
	}
	// Explicit resample succeeds.
	extra := url.Values{}
	extra.Set("resample", "1")
	extra.Set("dt", "1")
	v2 := formFor("抖动重采样", "152", "220", "240", "320", "0,1,2,3,4", "1,2", extra)
	code, loc, _ = postForm(t, ts, "/datasets/1/runs", v2)
	if code != 303 || strings.Contains(loc, "err=") {
		t.Fatalf("显式重采样应成功: %d %s", code, loc)
	}
}

func TestCompareTwoDelayCandidates(t *testing.T) {
	ts, _ := newTestServer(t)
	loadFixture(t, ts)
	postForm(t, ts, "/datasets/1/runs",
		formFor("真延迟", "20", "140", "240", "325", "3", "1", nil))
	postForm(t, ts, "/datasets/1/runs",
		formFor("零延迟", "20", "140", "240", "325", "0", "1", nil))
	_, body := get(t, ts, "/compare?a=1&b=2")
	if !strings.Contains(body, "延迟（秒 / 步）") || !strings.Contains(body, "3.00 / 3") {
		t.Fatal("对比页应展示两条运行的延迟参数差异")
	}
	if !strings.Contains(body, "残差检验优先") {
		t.Fatal("对比页应给出基于残差检验的建议")
	}
}

func TestExportClearImportReplay(t *testing.T) {
	ts, _ := newTestServer(t)
	loadFixture(t, ts)
	postForm(t, ts, "/datasets/1/runs",
		formFor("主运行", "20", "140", "240", "325", "3", "1", nil))
	resp, err := http.Get(ts.URL + "/export")
	if err != nil {
		t.Fatal(err)
	}
	bundle, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	w, _ := mw.CreateFormFile("bundle", "export.json")
	w.Write(bundle)
	mw.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	// missing confirm -> reject
	r1, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r1.Body.Close()
	if !strings.Contains(r1.Header.Get("Location"), "err=") {
		t.Fatal("未勾选清空确认时必须拒绝导入")
	}

	buf.Reset()
	mw = multipart.NewWriter(&buf)
	w, _ = mw.CreateFormFile("bundle", "export.json")
	w.Write(bundle)
	mw.WriteField("confirm_clear", "1")
	mw.Close()
	req, _ = http.NewRequest("POST", ts.URL+"/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	r2, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	// original run id=1 must replay
	_, body := get(t, ts, "/runs/1")
	if !strings.Contains(body, "死区延迟 3.00 秒") {
		t.Fatal("清空后重新导入应按原 ID 复核到原运行结果")
	}
}

func TestRunJSONExportable(t *testing.T) {
	ts, _ := newTestServer(t)
	loadFixture(t, ts)
	postForm(t, ts, "/datasets/1/runs",
		formFor("主运行", "20", "140", "240", "325", "3", "1", nil))
	code, body := get(t, ts, "/runs/1/export")
	if code != 200 || !strings.Contains(body, `"best_id"`) {
		t.Fatalf("运行 JSON 导出失败 code=%d", code)
	}
}
