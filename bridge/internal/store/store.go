package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"specwire/bridge/internal/domain"

	_ "modernc.org/sqlite"
)

// The migration directory is embedded so a Bridge binary always carries the
// schema it knows how to operate.  Migrations are append-only and applied in
// filename order.
//
//go:embed migrations/*.sql
var migrationFS embed.FS

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("%w: database path is required", domain.ErrInvalid)
	}
	dsn := sqliteDSN(path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	} else {
		db.SetMaxOpenConns(8)
		db.SetMaxIdleConns(8)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func sqliteDSN(path string) string {
	pragmas := "_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)"
	if path == ":memory:" {
		return "file:specwire-foundation-memory?mode=memory&cache=shared&" + pragmas
	}
	// A file: URI preserves absolute paths.  Escape only the query-sensitive
	// characters in the path while leaving slashes intact.
	clean := filepath.Clean(path)
	if strings.HasPrefix(clean, "file:") {
		return clean + "&" + pragmas
	}
	return "file:" + strings.ReplaceAll(url.PathEscape(clean), "%2F", "/") + "?" + pragmas
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, err := migrationVersion(entry.Name())
		if err != nil {
			return err
		}
		var applied int
		err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&applied)
		if err != nil {
			return fmt.Errorf("check migration %s: %w", entry.Name(), err)
		}
		if applied != 0 {
			continue
		}
		sqlBytes, err := migrationFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", entry.Name(), err)
		}
		if _, err := tx.ExecContext(ctx, string(sqlBytes)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", entry.Name(), err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, ?)`, version, entry.Name(), nowText()); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", entry.Name(), err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func migrationVersion(name string) (int, error) {
	parts := strings.SplitN(name, "_", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid migration filename %q", name)
	}
	v, err := strconv.Atoi(parts[0])
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("invalid migration version %q", name)
	}
	return v, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) now() time.Time { return time.Now().UTC() }

func nowText() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func decodeTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}

func decodeOptionalTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid || value.String == "" {
		return nil, nil
	}
	t, err := decodeTime(value.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func marshalJSON(value any, fallback string) (string, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	if len(b) == 0 || string(b) == "null" {
		return fallback, nil
	}
	return string(b), nil
}

// IsNotFound and IsConflict let callers keep transport-specific error mapping
// out of repository code.
func IsNotFound(err error) bool { return errors.Is(err, domain.ErrNotFound) }
func IsConflict(err error) bool { return errors.Is(err, domain.ErrConflict) }
