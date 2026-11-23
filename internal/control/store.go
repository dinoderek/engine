package control

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Store allows one server process per database. The OS releases its process
// lock on a crash. Reads run in WAL read transactions on a small read-only
// pool, beside the one writer connection (Store.Read, Store.Write).
type Store struct {
	// mu guards lock and the handles against a concurrent Close.
	mu      sync.RWMutex
	path    string
	lock    *os.File
	writer  *sql.DB
	readers *sql.DB
	commit  func(*sql.Tx) error
	// writeTimeout bounds each Store.Write.
	writeTimeout time.Duration
}

// Open creates a private database and refuses a database already owned by a
// server. It migrates an older schema (migrate) and refuses a newer one; a
// current database opens without reading any record.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("a database path is required")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("database already owned: %w", err)
	}
	s := &Store{path: path, lock: f, commit: (*sql.Tx).Commit, writeTimeout: writeTimeout}
	if err = s.openDatabases(); err == nil {
		err = s.initialize()
	}
	if err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) initialize() error {
	var version int
	if err := s.writer.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > schemaVersion {
		return fmt.Errorf("control database schema %d is newer than this server's %d", version, schemaVersion)
	}
	if version == schemaVersion {
		return nil
	}
	var records int
	if err := s.writer.QueryRow("SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='records'").Scan(&records); err != nil {
		return err
	}
	if records > 0 {
		return s.migrate(version)
	}
	if version != 0 {
		return fmt.Errorf("control database schema %d has no records table", version)
	}
	return s.transaction(func(tx *sql.Tx) error {
		_, err := tx.Exec(schema + telegramSchema + fmt.Sprintf("PRAGMA user_version=%d;", schemaVersion))
		return err
	})
}

// Close releases this process's ownership. It must follow HTTP and coordinator shutdown.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return nil
	}
	err := errors.Join(s.closeDatabases(), s.lock.Close())
	s.lock = nil
	return err
}

// Backup takes a transactionally consistent SQLite copy; it never overwrites a file.
func (s *Store) Backup(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return errors.New("store is closed")
	}
	return s.vacuumInto(path)
}

// vacuumInto writes an owner-only copy of the database to path, published
// whole or not at all, and refuses to replace any existing file.
func (s *Store) vacuumInto(path string) error {
	dir, err := os.MkdirTemp(filepath.Dir(path), ".engine-backup-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	tmp := filepath.Join(dir, "control.db")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err = s.writer.ExecContext(ctx, "VACUUM INTO ?", tmp); err != nil {
		return err
	}
	if err = os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	// Linking publishes atomically and refuses to replace any existing target.
	return os.Link(tmp, path)
}

func (s *Store) privateFile() error {
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if err = f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return nil
}
