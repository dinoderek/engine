package control

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// SQLRunner executes SQLite with SQL on stdin, never on the process command line.
type SQLRunner func(context.Context, string, string) ([]byte, error)

// SQLite is the real CLI boundary. -init prevents loading the user's sqliterc.
func SQLite(ctx context.Context, path, sql string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "sqlite3", "-init", "/dev/null", "-batch", "-bail", "-json", path)
	cmd.Stdin = strings.NewReader("PRAGMA foreign_keys=ON; PRAGMA synchronous=FULL;\n" + sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("sqlite3: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// Store allows one server process per database, and serializes its transactions.
// The OS releases its process lock on a crash. As the database's only owner
// it reads the records once, at open: state is then the authority, and
// SQLite its durable copy.
type Store struct {
	mu   sync.RWMutex
	path string
	lock *os.File
	run  SQLRunner
	// state never leaves the store: callers get clones, and a commit
	// installs records decoded from what it wrote.
	state State
}

// Open creates a private database and refuses a database already owned by a server.
func Open(path string, run SQLRunner) (*Store, error) {
	if path == "" {
		return nil, errors.New("a database path is required")
	}
	if run == nil {
		run = SQLite
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
	s := &Store{path: path, lock: f, run: run}
	if err = s.initialize(); err == nil {
		s.state, _, err = s.load()
	}
	if err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) initialize() error {
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
	out, err := s.sql("PRAGMA user_version;")
	if err != nil {
		return err
	}
	var version []struct {
		Version int `json:"user_version"`
	}
	if err = json.Unmarshal(out, &version); err != nil {
		return err
	}
	if len(version) != 1 || version[0].Version > schemaVersion {
		return errors.New("unsupported control database schema")
	}
	if v := version[0].Version; v == 1 || v == 2 {
		return s.migrate()
	}
	_, err = s.sql("BEGIN IMMEDIATE; CREATE TABLE IF NOT EXISTS records (key TEXT PRIMARY KEY, data TEXT NOT NULL); " + migratePlanStates + telegramSchema + fmt.Sprintf("PRAGMA user_version=%d; COMMIT;", schemaVersion))
	return err
}

// migratePlanStates moves a plan stored in a state version 1 allowed, and
// later versions derive, back to planned. It is a no-op once applied.
const migratePlanStates = "UPDATE records SET data = json_set(data, '$.state', 'planned') " +
	"WHERE key LIKE 'item/%' AND json_extract(data, '$.kind') = 'plan' " +
	"AND json_extract(data, '$.state') IN ('executing', 'verifying', 'blocked'); "

func (s *Store) sql(sql string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return s.run(ctx, s.path, sql)
}

// Close releases this process's ownership. It must follow HTTP and coordinator shutdown.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return nil
	}
	err := s.lock.Close()
	s.lock = nil
	return err
}

// Transaction commits all changed records together; an error leaves history
// unchanged, in memory and on disk. fn works on a copy of the state.
func (s *Store) Transaction(fn func(*State) error) error {
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
		return err
	}
	s.state.install(changes, patch)
	return nil
}

// write sends changes, then tail, to SQLite in one transaction.
func (s *Store) write(changes []change, tail string) error {
	var sql strings.Builder
	sql.WriteString("BEGIN IMMEDIATE;\n")
	for _, c := range changes {
		if c.del {
			fmt.Fprintf(&sql, "DELETE FROM records WHERE key=%s;\n", literal(c.key))
			continue
		}
		fmt.Fprintf(&sql, "INSERT INTO records(key,data) VALUES(%s,%s) ON CONFLICT(key) DO UPDATE SET data=excluded.data;\n", literal(c.key), literal(c.data))
	}
	sql.WriteString(tail)
	sql.WriteString("COMMIT;\n")
	_, err := s.sql(sql.String())
	return err
}

// Snapshot returns a detached consistent view, without reading SQLite.
// Snapshots run beside each other, and wait only for a commit.
func (s *Store) Snapshot() (State, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lock == nil {
		return State{}, errors.New("store is closed")
	}
	return s.state.clone(), nil
}

// View runs fn on a snapshot and commits nothing.
func (s *Store) View(fn func(*State) error) error {
	st, err := s.Snapshot()
	if err != nil {
		return err
	}
	return fn(&st)
}

func literal(s string) string { return "CAST(X'" + hex.EncodeToString([]byte(s)) + "' AS TEXT)" }

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
	if _, err = s.sql("VACUUM INTO " + literal(tmp) + ";"); err != nil {
		return err
	}
	if err = os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	// Linking publishes atomically and refuses to replace any existing target.
	return os.Link(tmp, path)
}
