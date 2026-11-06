package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const memoryPath = ":memory:"

type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database at path, or an in-memory one for ":memory:", and brings it to the latest schema.
func Open(path string) (*Store, error) {
	return openWith(path, migrations)
}

func openWith(path string, migs []string) (*Store, error) {
	if path != memoryPath {
		if err := preparePrivateFile(path); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", dataSourceName(path))
	if err != nil {
		return nil, err
	}
	if path == memoryPath {
		// Every new connection would otherwise get its own empty in-memory database.
		db.SetMaxOpenConns(1)
	}
	if err := migrate(db, migs); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) SchemaVersion() (int, error) {
	var version int
	err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&version)
	return version, err
}

// preparePrivateFile makes sure the file exists as 0600 before SQLite creates its -wal and -shm files, which copy that mode.
func preparePrivateFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func dataSourceName(path string) string {
	query := url.Values{"_pragma": {"foreign_keys(1)", "journal_mode(WAL)", "busy_timeout(5000)"}}
	name := url.URL{Scheme: "file", Opaque: url.PathEscape(path), RawQuery: query.Encode()}
	if path == memoryPath {
		name.Opaque = memoryPath
	}
	return name.String()
}

func migrate(db *sql.DB, migs []string) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER PRIMARY KEY,
		applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		return err
	}
	var current int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&current); err != nil {
		return err
	}
	if current > len(migs) {
		return fmt.Errorf("database schema version %d is newer than this build supports (%d); upgrade shh", current, len(migs))
	}
	for i := current; i < len(migs); i++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migs[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_version (version) VALUES (?)`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
