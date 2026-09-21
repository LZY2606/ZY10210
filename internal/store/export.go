package store

import (
	"encoding/json"
	"io"
	"time"

	"dynid/internal/ident"
)

// Bundle is the portable export/import format. IDs are preserved so that
// cross-run comparison links remain valid after a clear-and-reimport cycle.
type Bundle struct {
	SchemaVersion int                 `json:"schema_version"`
	ExportedAt    string              `json:"exported_at"`
	Datasets      []Dataset           `json:"datasets"`
	Runs          []ident.ModelReport `json:"runs"`
}

// Export serializes all datasets and runs.
func (s *Store) Export(w io.Writer) error {
	ds, err := s.ListDatasets()
	if err != nil {
		return err
	}
	for i := range ds {
		full, err := s.GetDataset(ds[i].ID)
		if err != nil {
			return err
		}
		ds[i].Samples = full.Samples
	}
	runs, err := s.ListRuns(0)
	if err != nil {
		return err
	}
	b := Bundle{
		SchemaVersion: 1,
		ExportedAt:    time.Now().Format(time.RFC3339),
		Datasets:      ds,
		Runs:          runs,
	}
	raw, err := ident.MarshalJSONSafe(&b)
	if err != nil {
		return err
	}
	_, err = w.Write(raw)
	return err
}

// Import clears the database and restores a bundle verbatim. The restored
// reports are the original computed reports — import replays storage, not
// recomputation; re-running identification is available on the page.
func (s *Store) Import(r io.Reader) (int, int, error) {
	var b Bundle
	dec := json.NewDecoder(r)
	if err := dec.Decode(&b); err != nil {
		return 0, 0, err
	}
	for i := range b.Runs {
		if err := ident.UnmarshalJSONSafe(jsonRaw(b.Runs[i]), &b.Runs[i]); err != nil {
			return 0, 0, err
		}
	}
	if err := s.Reset(); err != nil {
		return 0, 0, err
	}
	for _, d := range b.Datasets {
		if err := s.insertDatasetWithID(d.ID, d.Name, d.Note, d.CreatedAt, d.Samples); err != nil {
			return 0, 0, err
		}
	}
	for i := range b.Runs {
		if err := s.insertRunWithID(&b.Runs[i]); err != nil {
			return 0, 0, err
		}
	}
	return len(b.Datasets), len(b.Runs), nil
}

func (s *Store) insertDatasetWithID(id int64, name, note, createdAt string, samples []ident.Sample) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		`INSERT INTO datasets(id,name,note,created_at) VALUES(?,?,?,?)`,
		id, name, note, createdAt); err != nil {
		return err
	}
	stmt, err := tx.Prepare(
		`INSERT INTO samples(dataset_id,idx,t,u,y,sat) VALUES(?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for i, sm := range samples {
		var y any
		if sm.Y != nil {
			y = *sm.Y
		}
		sat := 0
		if sm.Sat {
			sat = 1
		}
		if _, err := stmt.Exec(id, i, sm.T, sm.U, y, sat); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) insertRunWithID(rep *ident.ModelReport) error {
	raw, err := ident.MarshalJSONSafe(rep)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO runs(id,dataset_id,name,created_at,published,blocked,report_json)
		 VALUES(?,?,?,?,?,?,?)`,
		rep.ID, rep.DatasetID, rep.Name,
		rep.CreatedAt.Format("2006-01-02 15:04:05"),
		boolInt(rep.Published), rep.PublishBlocked, string(raw))
	return err
}
