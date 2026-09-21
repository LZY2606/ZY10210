// Package store persists datasets, raw samples and identification runs in
// SQLite. The database file is local; export/import JSON lets a cleared
// database be re-populated and re-reviewed.
package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"

	"dynid/internal/ident"
)

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS datasets (
  id         INTEGER PRIMARY KEY,
  name       TEXT NOT NULL,
  note       TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS samples (
  id         INTEGER PRIMARY KEY,
  dataset_id INTEGER NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
  idx        INTEGER NOT NULL,
  t          REAL NOT NULL,
  u          REAL NOT NULL,
  y          REAL,
  sat        INTEGER NOT NULL DEFAULT 0,
  UNIQUE(dataset_id, idx)
);
CREATE TABLE IF NOT EXISTS runs (
  id          INTEGER PRIMARY KEY,
  dataset_id  INTEGER NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  created_at  TEXT NOT NULL,
  published   INTEGER NOT NULL DEFAULT 0,
  blocked     TEXT NOT NULL DEFAULT '',
  report_json TEXT NOT NULL
);`)
	return err
}

// Dataset is a dataset header with its raw samples.
type Dataset struct {
	ID        int64          `json:"id"`
	Name      string         `json:"name"`
	Note      string         `json:"note"`
	CreatedAt string         `json:"created_at"`
	Samples   []ident.Sample `json:"samples"`
}

func (s *Store) InsertDataset(name, note, createdAt string, samples []ident.Sample) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(
		`INSERT INTO datasets(name,note,created_at) VALUES(?,?,?)`, name, note, createdAt)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	stmt, err := tx.Prepare(
		`INSERT INTO samples(dataset_id,idx,t,u,y,sat) VALUES(?,?,?,?,?,?)`)
	if err != nil {
		return 0, err
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
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Store) ListDatasets() ([]Dataset, error) {
	rows, err := s.db.Query(
		`SELECT id,name,note,created_at FROM datasets ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Dataset
	for rows.Next() {
		var d Dataset
		if err := rows.Scan(&d.ID, &d.Name, &d.Note, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) GetDataset(id int64) (*Dataset, error) {
	d := &Dataset{ID: id}
	err := s.db.QueryRow(
		`SELECT name,note,created_at FROM datasets WHERE id=?`, id).
		Scan(&d.Name, &d.Note, &d.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("数据集不存在")
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(
		`SELECT t,u,y,sat FROM samples WHERE dataset_id=? ORDER BY idx`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t, u float64
		var y sql.NullFloat64
		var sat int
		if err := rows.Scan(&t, &u, &y, &sat); err != nil {
			return nil, err
		}
		sm := ident.Sample{T: t, U: u, Sat: sat == 1}
		if y.Valid {
			v := y.Float64
			sm.Y = &v
		}
		d.Samples = append(d.Samples, sm)
	}
	return d, rows.Err()
}

// InsertRun persists a full report. For rep.ID==0 the next rowid is used and
// written back into rep.ID (and into the stored JSON), so export/import keep
// stable IDs.
func (s *Store) InsertRun(rep *ident.ModelReport) (int64, error) {
	var (
		res sql.Result
		err error
	)
	if rep.ID <= 0 {
		res, err = s.db.Exec(
			`INSERT INTO runs(dataset_id,name,created_at,published,blocked,report_json)
			 VALUES(?,?,?,?,?,'')`,
			rep.DatasetID, rep.Name, rep.CreatedAt.Format("2006-01-02 15:04:05"),
			boolInt(rep.Published), rep.PublishBlocked)
	} else {
		res, err = s.db.Exec(
			`INSERT INTO runs(id,dataset_id,name,created_at,published,blocked)
			 VALUES(?,?,?,?,?,?)`,
			rep.ID, rep.DatasetID, rep.Name, rep.CreatedAt.Format("2006-01-02 15:04:05"),
			boolInt(rep.Published), rep.PublishBlocked)
	}
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	rep.ID = id
	b, err := ident.MarshalJSONSafe(rep)
	if err != nil {
		return 0, err
	}
	if _, err := s.db.Exec(`UPDATE runs SET report_json=? WHERE id=?`, string(b), id); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Store) ListRuns(datasetID int64) ([]ident.ModelReport, error) {
	q := `SELECT report_json FROM runs`
	var args []any
	if datasetID > 0 {
		q += ` WHERE dataset_id=?`
		args = append(args, datasetID)
	}
	q += ` ORDER BY id`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ident.ModelReport
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var rep ident.ModelReport
		if err := ident.UnmarshalJSONSafe([]byte(raw), &rep); err != nil {
			return nil, err
		}
		out = append(out, rep)
	}
	return out, rows.Err()
}

func (s *Store) GetRun(id int64) (*ident.ModelReport, error) {
	var raw string
	err := s.db.QueryRow(`SELECT report_json FROM runs WHERE id=?`, id).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("运行记录不存在")
	}
	if err != nil {
		return nil, err
	}
	var rep ident.ModelReport
	if err := ident.UnmarshalJSONSafe([]byte(raw), &rep); err != nil {
		return nil, err
	}
	return &rep, nil
}

func (s *Store) SetPublished(id int64, published bool, blocked string) error {
	rep, err := s.GetRun(id)
	if err != nil {
		return err
	}
	rep.Published = published
	rep.PublishBlocked = blocked
	raw, err := MarshalReport(rep)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`UPDATE runs SET published=?,blocked=?,report_json=? WHERE id=?`,
		boolInt(published), blocked, string(raw), id)
	return err
}

// Reset wipes every dataset and run (used by re-import).
func (s *Store) Reset() error {
	_, err := s.db.Exec(`DELETE FROM runs; DELETE FROM samples; DELETE FROM datasets;`)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
