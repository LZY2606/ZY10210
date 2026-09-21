// Package store 负责 SQLite 持久化：数据集、原始样本、辨识运行与候选结果。
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	_ "modernc.org/sqlite"
)

type Store struct{ DB *sql.DB }

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) migrate() error {
	_, err := s.DB.Exec(schema)
	return err
}

const schema = `
CREATE TABLE IF NOT EXISTS datasets (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	ts_nominal REAL NOT NULL,
	created_at TEXT NOT NULL DEFAULT (datetime('now')),
	fixture INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS samples (
	dataset_id INTEGER NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
	idx INTEGER NOT NULL,
	t REAL NOT NULL,
	u REAL,
	y REAL,
	sat INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY(dataset_id, idx)
);
CREATE TABLE IF NOT EXISTS runs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	dataset_id INTEGER NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
	name TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'draft',
	published INTEGER NOT NULL DEFAULT 0,
	blocked_reason TEXT NOT NULL DEFAULT '',
	segments TEXT NOT NULL,
	prep TEXT NOT NULL,
	result TEXT,
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_runs_dataset ON runs(dataset_id);
`

// Dataset 为数据集元数据。
type Dataset struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	TsNominal   float64 `json:"ts_nominal"`
	Fixture     bool    `json:"fixture"`
	CreatedAt   string  `json:"created_at"`
	N           int     `json:"n"`
}

func (s *Store) CreateDataset(name, desc string, ts float64, fixture bool) (int64, error) {
	res, err := s.DB.Exec(
		`INSERT INTO datasets(name, description, ts_nominal, fixture) VALUES(?,?,?,?)`,
		name, desc, ts, btoi(fixture))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Sample 与 ident.RawSample 对应。
type Sample struct {
	T   float64  `json:"t"`
	U   *float64 `json:"u,omitempty"`
	Y   *float64 `json:"y,omitempty"`
	Sat bool     `json:"sat"`
}

func (s *Store) BulkAddSamples(datasetID int64, samples []Sample) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO samples(dataset_id,idx,t,u,y,sat) VALUES(?,?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()
	for i, sm := range samples {
		if _, err := stmt.Exec(datasetID, i, sm.T, sm.U, sm.Y, btoi(sm.Sat)); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListDatasets() ([]Dataset, error) {
	rows, err := s.DB.Query(`
		SELECT d.id,d.name,d.description,d.ts_nominal,d.fixture,d.created_at,COUNT(s.idx)
		FROM datasets d LEFT JOIN samples s ON s.dataset_id=d.id
		GROUP BY d.id ORDER BY d.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Dataset, 0)
	for rows.Next() {
		var d Dataset
		var fix int
		if err := rows.Scan(&d.ID, &d.Name, &d.Description, &d.TsNominal, &fix, &d.CreatedAt, &d.N); err != nil {
			return nil, err
		}
		d.Fixture = fix == 1
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) GetDataset(id int64) (*Dataset, error) {
	var d Dataset
	var fix int
	err := s.DB.QueryRow(`SELECT id,name,description,ts_nominal,fixture,created_at FROM datasets WHERE id=?`, id).
		Scan(&d.ID, &d.Name, &d.Description, &d.TsNominal, &fix, &d.CreatedAt)
	if err != nil {
		return nil, err
	}
	d.Fixture = fix == 1
	d.N = s.CountSamples(id)
	return &d, nil
}

func (s *Store) CountSamples(id int64) int {
	var n int
	s.DB.QueryRow(`SELECT COUNT(*) FROM samples WHERE dataset_id=?`, id).Scan(&n)
	return n
}

func (s *Store) LoadSamples(id int64) ([]Sample, error) {
	rows, err := s.DB.Query(`SELECT t,u,y,sat FROM samples WHERE dataset_id=? ORDER BY idx`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Sample, 0)
	for rows.Next() {
		var sm Sample
		var u, y sql.NullFloat64
		var sat int
		if err := rows.Scan(&sm.T, &u, &y, &sat); err != nil {
			return nil, err
		}
		if u.Valid {
			v := u.Float64
			sm.U = &v
		}
		if y.Valid {
			v := y.Float64
			sm.Y = &v
		}
		sm.Sat = sat == 1
		out = append(out, sm)
	}
	return out, rows.Err()
}

// RunRecord 为 runs 表一条记录（结果 JSON 原样存取）。
type RunRecord struct {
	ID            int64           `json:"id"`
	DatasetID     int64           `json:"dataset_id"`
	Name          string          `json:"name"`
	Status        string          `json:"status"`
	Published     bool            `json:"published"`
	BlockedReason string          `json:"blocked_reason"`
	Segments      json.RawMessage `json:"segments"`
	Prep          json.RawMessage `json:"prep"`
	Result        json.RawMessage `json:"result,omitempty"`
	CreatedAt     string          `json:"created_at"`
}

func (s *Store) CreateRun(rec *RunRecord) (int64, error) {
	res, err := s.DB.Exec(
		`INSERT INTO runs(dataset_id,name,status,published,blocked_reason,segments,prep,result)
		 VALUES(?,?,?,?,?,?,?,?)`,
		rec.DatasetID, rec.Name, rec.Status, btoi(rec.Published), rec.BlockedReason,
		string(rec.Segments), string(rec.Prep), nullJSON(rec.Result))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func nullJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

func (s *Store) UpdateRunResult(id int64, result []byte, status, reason string) error {
	_, err := s.DB.Exec(`UPDATE runs SET result=?,status=?,blocked_reason=? WHERE id=?`,
		string(result), status, reason, id)
	return err
}

func (s *Store) SetPublished(id int64, published bool, status, reason string) error {
	_, err := s.DB.Exec(`UPDATE runs SET published=?,status=?,blocked_reason=? WHERE id=?`,
		btoi(published), status, reason, id)
	return err
}

func (s *Store) GetRun(id int64) (*RunRecord, error) {
	var r RunRecord
	var seg, prep, result sql.NullString
	var pub int
	err := s.DB.QueryRow(
		`SELECT id,dataset_id,name,status,published,blocked_reason,segments,prep,result,created_at
		 FROM runs WHERE id=?`, id).
		Scan(&r.ID, &r.DatasetID, &r.Name, &r.Status, &pub, &r.BlockedReason, &seg, &prep, &result, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	r.Published = pub == 1
	r.Segments = []byte(seg.String)
	r.Prep = []byte(prep.String)
	if result.Valid {
		r.Result = []byte(result.String)
	}
	return &r, nil
}

func (s *Store) ListRuns(datasetID int64) ([]RunRecord, error) {
	rows, err := s.DB.Query(
		`SELECT id,dataset_id,name,status,published,blocked_reason,created_at
		 FROM runs WHERE dataset_id=? ORDER BY id DESC`, datasetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]RunRecord, 0)
	for rows.Next() {
		var r RunRecord
		var pub int
		if err := rows.Scan(&r.ID, &r.DatasetID, &r.Name, &r.Status, &pub, &r.BlockedReason, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Published = pub == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// Reset 清空所有业务数据（用于“清空后重新导入复核”）。
func (s *Store) Reset() error {
	if _, err := s.DB.Exec(`DELETE FROM runs; DELETE FROM samples; DELETE FROM datasets;`); err != nil {
		return fmt.Errorf("清空失败: %w", err)
	}
	return nil
}
