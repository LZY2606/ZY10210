package store

import "testing"

func TestCRUDAndReset(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id, err := st.CreateDataset("d", "desc", 0.1, false)
	if err != nil {
		t.Fatal(err)
	}
	v := 1.5
	if err := st.BulkAddSamples(id, []Sample{
		{T: 0, U: &v, Y: &v}, {T: 0.1, U: nil, Y: &v},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := st.LoadSamples(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].U != nil {
		t.Fatalf("样本/缺失存取错误: %+v", got)
	}
	if err := st.Reset(); err != nil {
		t.Fatal(err)
	}
	if dss, _ := st.ListDatasets(); len(dss) != 0 {
		t.Fatalf("清空后仍有 %d 个数据集", len(dss))
	}
}
