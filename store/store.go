// Package store is the SQLite persistence layer shared by the daemon, the CLI
// and the TUI. The daemon is the only writer of synced data; consumers only
// write read_state.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store wraps one SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database at path and applies migrations.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the underlying handle for ad-hoc read queries.
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return err
	}
	applied := map[int]bool{}
	rows, err := s.db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	rows.Close()
	for _, m := range migrationFiles() {
		if applied[m.version] {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + m.name)
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES (?, unixepoch('subsec')*1000)`, m.version); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

type migration struct {
	version int
	name    string
}

func migrationFiles() []migration {
	entries, _ := fs.ReadDir(migrations, "migrations")
	var out []migration
	for _, e := range entries {
		var v int
		if _, err := fmt.Sscanf(e.Name(), "%d_", &v); err == nil {
			out = append(out, migration{version: v, name: e.Name()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out
}

// Schema returns the DDL of every migration, in order, for consumers that
// read the database directly.
func Schema() string {
	var b strings.Builder
	for _, m := range migrationFiles() {
		body, _ := migrations.ReadFile("migrations/" + m.name)
		fmt.Fprintf(&b, "-- %s\n%s\n", m.name, body)
	}
	return b.String()
}

// GetState reads a sync_state value; ok is false when the key is absent.
func (s *Store) GetState(ctx context.Context, key string) (value string, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT value FROM sync_state WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return value, err == nil, err
}

// SetState writes a sync_state value.
func (s *Store) SetState(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sync_state(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// Run is one row of sync_runs.
type Run struct {
	ID         int64  `json:"id"`
	Kind       string `json:"kind"`
	StartedAt  int64  `json:"started_at"`
	FinishedAt int64  `json:"finished_at"`
	OK         bool   `json:"ok"`
	Fetched    int    `json:"fetched"`
	Upserted   int    `json:"upserted"`
	Error      string `json:"error"`
}

// RecordRun appends a run and prunes the table to the newest 1000 rows.
func (s *Store) RecordRun(ctx context.Context, r Run) error {
	if _, err := s.db.ExecContext(ctx, `INSERT INTO sync_runs(kind, started_at, finished_at, ok, fetched, upserted, error) VALUES (?,?,?,?,?,?,?)`,
		r.Kind, r.StartedAt, r.FinishedAt, r.OK, r.Fetched, r.Upserted, r.Error); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM sync_runs WHERE id < (SELECT id FROM sync_runs ORDER BY id DESC LIMIT 1 OFFSET 999)`)
	return err
}

// LastRuns returns the newest n runs, newest first.
func (s *Store) LastRuns(ctx context.Context, n int) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, started_at, finished_at, ok, fetched, upserted, error FROM sync_runs ORDER BY id DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.Kind, &r.StartedAt, &r.FinishedAt, &r.OK, &r.Fetched, &r.Upserted, &r.Error); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Counts summarizes table sizes for status output.
type Counts struct {
	Chats    int64 `json:"chats"`
	Messages int64 `json:"messages"`
	Rendered int64 `json:"rendered"`
}

// Counts reports row counts.
func (s *Store) Counts(ctx context.Context) (Counts, error) {
	var c Counts
	if err := s.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM chats WHERE left_at = 0), (SELECT count(*) FROM messages), (SELECT count(*) FROM messages WHERE rendered_at <> 0)`).Scan(&c.Chats, &c.Messages, &c.Rendered); err != nil {
		return c, err
	}
	return c, nil
}
