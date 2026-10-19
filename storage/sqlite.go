package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/john-nash-rs/captaincode/core"
	_ "modernc.org/sqlite"
)

type DB struct {
	sql  *sql.DB
	lock *os.File
}

func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	lock, err := lockFile(path + ".lock")
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		lock.Close()
		return nil, err
	}
	file.Close()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		lock.Close()
		return nil, err
	}
	database.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var version int
	if err := database.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil || version > 2 {
		database.Close()
		lock.Close()
		return nil, errors.New("unsupported or unreadable database schema version")
	}
	_, err = database.ExecContext(ctx, `PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS runs(id TEXT PRIMARY KEY, snapshot BLOB NOT NULL, updated TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS events(sequence INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL, kind TEXT NOT NULL, recorded TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS turns(run_id TEXT NOT NULL, ordinal INTEGER NOT NULL, question TEXT NOT NULL, answer TEXT NOT NULL, recorded TEXT NOT NULL, PRIMARY KEY(run_id, ordinal));
PRAGMA user_version=2;`)
	if err != nil {
		database.Close()
		lock.Close()
		return nil, err
	}
	return &DB{sql: database, lock: lock}, nil
}

func (d *DB) Close() error { return errors.Join(d.sql.Close(), d.lock.Close()) }

func (d *DB) Save(ctx context.Context, run core.Run, kind string) error {
	data, err := json.Marshal(run)
	if err != nil {
		return err
	}
	transaction, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = transaction.ExecContext(ctx, `INSERT INTO runs(id,snapshot,updated) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET snapshot=excluded.snapshot,updated=excluded.updated`, run.ID, data, now)
	if err != nil {
		return err
	}
	_, err = transaction.ExecContext(ctx, `INSERT INTO events(run_id,kind,recorded) VALUES(?,?,?)`, run.ID, kind, now)
	if err != nil {
		return err
	}
	return transaction.Commit()
}

func (d *DB) Load(ctx context.Context, id string) (core.Run, error) {
	var data []byte
	var run core.Run
	if err := d.sql.QueryRowContext(ctx, `SELECT snapshot FROM runs WHERE id=?`, id).Scan(&data); err != nil {
		return run, err
	}
	err := json.Unmarshal(data, &run)
	return run, err
}

func (d *DB) Bind(ctx context.Context, root string) error {
	if _, err := d.sql.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS workspace(singleton INTEGER PRIMARY KEY CHECK(singleton=1),root TEXT NOT NULL)`); err != nil {
		return err
	}
	if _, err := d.sql.ExecContext(ctx, `INSERT OR IGNORE INTO workspace(singleton,root) VALUES(1,?)`, root); err != nil {
		return err
	}
	var existing string
	if err := d.sql.QueryRowContext(ctx, `SELECT root FROM workspace WHERE singleton=1`).Scan(&existing); err != nil {
		return err
	}
	if existing != root {
		return errors.New("database belongs to a different workspace")
	}
	return nil
}
