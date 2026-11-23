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

// Store allows one server process per database, and serializes its transactions.
// The OS releases its process lock on a crash. As the database's only owner
// it reads the records once, at open: state is then the authority, and
// SQLite its durable copy.
type Store struct {
	mu      sync.RWMutex
	path    string
	lock    *os.File
	writer  *sql.DB
	readers *sql.DB
	commit  func(*sql.Tx) error
	// state never leaves the store: callers get clones, and a commit
	// installs records decoded from what it wrote.
	state state
}

// Open creates a private database and refuses a database already owned by a server.
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
	s := &Store{path: path, lock: f, commit: (*sql.Tx).Commit}
	if err = s.openDatabases(); err == nil {
		err = s.initialize()
	}
	if err == nil {
		s.state, _, err = s.load()
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
		return errors.New("unsupported control database schema")
	}
	if v := version; v == 1 || v == 2 {
		return s.migrate()
	}
	return s.write(nil, "CREATE TABLE IF NOT EXISTS records (key TEXT PRIMARY KEY, data TEXT NOT NULL); "+migratePlanStates+telegramSchema+fmt.Sprintf("PRAGMA user_version=%d;", schemaVersion))
}

// migratePlanStates moves a plan stored in a state version 1 allowed, and
// later versions derive, back to planned. It is a no-op once applied.
const migratePlanStates = "UPDATE records SET data = json_set(data, '$.state', 'planned') " +
	"WHERE key LIKE 'item/%' AND json_extract(data, '$.kind') = 'plan' " +
	"AND json_extract(data, '$.state') IN ('executing', 'verifying', 'blocked'); "

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

// modify commits all changed records together; an error leaves history
// unchanged, in memory and on disk. fn works on a copy of the state.
func (s *Store) modify(fn func(*state) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return errors.New("store is closed")
	}
	work := s.state.clone()
	if err := fn(&work); err != nil {
		return err
	}
	changes, err := diff(&s.state, &work)
	if err != nil || len(changes) == 0 {
		return err
	}
	// Decoding before the write keeps a record SQLite took from failing to
	// reach memory.
	patch, err := decode(changes)
	if err != nil {
		return err
	}
	if err = s.write(changes, ""); err != nil {
		// A write can fail after SQLite committed it; disk then decides.
		if disk, _, lerr := s.load(); lerr == nil {
			s.state = disk
		}
		return err
	}
	s.state.install(changes, patch)
	return nil
}

// write sends changes, then tail, to SQLite in one transaction.
func (s *Store) write(changes []change, tail string) error {
	return s.transaction(func(tx *sql.Tx) error {
		for _, c := range changes {
			var err error
			if c.del {
				_, err = tx.Exec("DELETE FROM records WHERE key=?", c.key)
			} else {
				_, err = tx.Exec("INSERT INTO records(key,data) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET data=excluded.data", c.key, c.data)
			}
			if err != nil {
				return err
			}
		}
		if tail != "" {
			_, err := tx.Exec(tail)
			return err
		}
		return nil
	})
}

// snapshot returns a detached consistent view, without reading SQLite.
// Snapshots run beside each other, and wait only for a commit.
func (s *Store) snapshot() (state, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lock == nil {
		return state{}, errors.New("store is closed")
	}
	return s.state.clone(), nil
}

// view runs fn on a snapshot and commits nothing.
func (s *Store) view(fn func(*state) error) error {
	st, err := s.snapshot()
	if err != nil {
		return err
	}
	return fn(&st)
}

// Backup takes a transactionally consistent SQLite copy; it never overwrites a file.
func (s *Store) Backup(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return errors.New("store is closed")
	}
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
