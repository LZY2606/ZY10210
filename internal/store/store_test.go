package store_test

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"

	"dynid/internal/fixture"
	"dynid/internal/ident"
	"dynid/internal/store"
)

func tempStore(t *testing.T) *store.Store {
	t.Helper()
	p := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func newRun(t *testing.T, datasetID int64, delays []float64, est, val ident.Segment) *ident.ModelReport {
	t.Helper()
	samples := fixture.Build(fixture.DefaultSpec())
	cfg := ident.Config{
		Name: "rt", Est: est, Val: val, Detrend: ident.DetrendMean,
		Missing: ident.MissingInterpolate, Delays: delays, Orders: []int{1, 2},
	}
	rep, err := ident.Run(datasetID, "rt", samples, cfg, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestInsertAndReload(t *testing.T) {
	st := tempStore(t)
	id, err := st.InsertDataset("d", "n", "2026-01-01 00:00:00",
		fixture.Build(fixture.DefaultSpec()))
	if err != nil {
		t.Fatal(err)
	}
	est, val := fixture.Segments()
	rep := newRun(t, id, []float64{3}, est, val)
	if _, err := st.InsertRun(rep); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetRun(rep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.BestID != rep.BestID || got.Cfg.Delays[0] != 3 {
		t.Fatalf("回读不一致: best=%d delays=%v", got.BestID, got.Cfg.Delays)
	}
}

func TestExportClearImportReplay(t *testing.T) {
	st := tempStore(t)
	id, err := st.InsertDataset("d", "n", "2026-01-01 00:00:00",
		fixture.Build(fixture.DefaultSpec()))
	if err != nil {
		t.Fatal(err)
	}
	est, val := fixture.Segments()
	rep := newRun(t, id, []float64{2, 3}, est, val)
	if _, err := st.InsertRun(rep); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPublished(rep.ID, true, ""); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := st.Export(&buf); err != nil {
		t.Fatal(err)
	}
	if err := st.Reset(); err != nil {
		t.Fatal(err)
	}
	if ds, _ := st.ListDatasets(); len(ds) != 0 {
		t.Fatal("清空后不应有数据集")
	}
	nd, nr, err := st.Import(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if nd != 1 || nr != 1 {
		t.Fatalf("导入数量错误: %d %d", nd, nr)
	}
	got, err := st.GetRun(rep.ID)
	if err != nil {
		t.Fatalf("导入后按原 ID 取运行失败: %v", err)
	}
	if !got.Published || got.BestID != rep.BestID {
		t.Fatal("导入后发布状态/最优候选未原样恢复")
	}
	d, err := st.GetDataset(id)
	if err != nil || len(d.Samples) == 0 {
		t.Fatal("导入后数据集/样本缺失")
	}
}

func TestOverlapRunPersistsBlocked(t *testing.T) {
	st := tempStore(t)
	id, _ := st.InsertDataset("d", "", "2026-01-01 00:00:00",
		fixture.Build(fixture.DefaultSpec()))
	est, val := fixture.OverlappingSegments()
	rep := newRun(t, id, []float64{3}, est, val)
	if rep.PublishBlocked == "" {
		t.Fatal("重叠运行必须带阻断标记")
	}
	if _, err := st.InsertRun(rep); err != nil {
		t.Fatal(err)
	}
}
