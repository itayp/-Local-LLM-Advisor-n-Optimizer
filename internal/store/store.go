// Package store is the daemon's only persistence: one SQLite file, opened
// through modernc.org/sqlite (pure Go, no cgo — the reason one binary per
// OS cross-compiles from one machine).
//
// Schema changes are numbered SQL files under migrations/, embedded in the
// binary and applied in order inside a transaction; schema_migrations
// records what ran. A shipped migration is never edited: a change is a new
// file. Step 1 ships 0001_schema_v0.sql.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Store wraps the database handle.
type Store struct {
	db   *sql.DB
	path string
}

// DefaultDataDir is where the daemon keeps everything it writes — the
// database now; logs, a user-space Ollama install (step 3), notifications
// state later. It follows each OS's convention for application data:
//
//	Linux    $XDG_DATA_HOME/advisor, default ~/.local/share/advisor
//	macOS    ~/Library/Application Support/Advisor
//	Windows  %LOCALAPPDATA%\Advisor
//
// ADVISOR_DATA_DIR overrides all three (used by tests and by `make dev`).
func DefaultDataDir() (string, error) {
	if v := strings.TrimSpace(os.Getenv("ADVISOR_DATA_DIR")); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("store: cannot find the home directory: %w", err)
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Advisor"), nil
	case "windows":
		if v := os.Getenv("LOCALAPPDATA"); v != "" {
			return filepath.Join(v, "Advisor"), nil
		}
		return filepath.Join(home, "AppData", "Local", "Advisor"), nil
	default:
		if v := os.Getenv("XDG_DATA_HOME"); v != "" {
			return filepath.Join(v, "advisor"), nil
		}
		return filepath.Join(home, ".local", "share", "advisor"), nil
	}
}

// DefaultPath is the database file inside DefaultDataDir.
func DefaultPath() (string, error) {
	dir, err := DefaultDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "advisor.db"), nil
}

// Open opens (creating if needed) the database at path, applies pending
// migrations, and returns the Store. The parent directory is created.
func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("store: creating %s: %w", filepath.Dir(path), err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	// One connection: SQLite has one writer, and a single connection makes
	// "database is locked" impossible from inside this process. The daemon's
	// load is tiny; this is simplicity, not a bottleneck.
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)

	s := &Store{db: db, path: path}
	for _, pragma := range []string{
		"PRAGMA journal_mode = WAL",  // readers never block the writer
		"PRAGMA busy_timeout = 5000", // wait, don't fail, on a lock from another process
		"PRAGMA foreign_keys = ON",   // the schema relies on it
		"PRAGMA synchronous = NORMAL",
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("store: %s: %w", pragma, err)
		}
	}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// DB exposes the handle for the packages that own tables. Step 2+ add
// typed methods here instead of writing SQL elsewhere.
func (s *Store) DB() *sql.DB { return s.db }

// Path is the database file.
func (s *Store) Path() string { return s.path }

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Ping verifies the connection.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// SchemaVersion is the highest applied migration number.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v sql.NullInt64
	err := s.db.QueryRowContext(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&v)
	if err != nil {
		return 0, err
	}
	return int(v.Int64), nil
}

// Tables lists the user tables in the database, sorted.
func (s *Store) Tables(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// migration is one embedded SQL file: NNNN_name.sql.
type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, err
	}
	var out []migration
	seen := map[int]string{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		num, _, ok := strings.Cut(strings.TrimSuffix(name, ".sql"), "_")
		if !ok {
			return nil, fmt.Errorf("store: migration %q is not named NNNN_name.sql", name)
		}
		v, err := strconv.Atoi(num)
		if err != nil || v <= 0 {
			return nil, fmt.Errorf("store: migration %q has no numeric prefix", name)
		}
		if prev, dup := seen[v]; dup {
			return nil, fmt.Errorf("store: migrations %q and %q share version %d", prev, name, v)
		}
		seen[v] = name
		body, err := fs.ReadFile(migrationFiles, "migrations/"+name)
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: v, name: name, sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			name       TEXT NOT NULL,
			applied_at TEXT NOT NULL
		)`); err != nil {
		return fmt.Errorf("store: schema_migrations: %w", err)
	}
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	applied := map[int]bool{}
	rows, err := s.db.QueryContext(ctx, "SELECT version FROM schema_migrations")
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
	if err := rows.Err(); err != nil {
		return err
	}

	for _, m := range migrations {
		if applied[m.version] {
			continue
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: migration %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)",
			m.version, m.name, time.Now().UTC().Format(time.RFC3339)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: recording migration %s: %w", m.name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("store: committing migration %s: %w", m.name, err)
		}
	}
	return nil
}

// ErrNotFound is returned by typed getters (added from step 2 on) when a row
// does not exist. Defined here so every package uses the same one.
var ErrNotFound = errors.New("store: not found")

// Now is the timestamp format every table uses.
func Now() string { return time.Now().UTC().Format(time.RFC3339) }
