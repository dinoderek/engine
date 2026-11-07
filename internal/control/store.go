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
// The OS releases its process lock on a crash; persisted merge locks remain.
type Store struct {
	mu   sync.Mutex
	path string
	lock *os.File
	run  SQLRunner
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
	if err = s.initialize(); err != nil {
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
	if len(version) != 1 || version[0].Version > 1 {
		return errors.New("unsupported control database schema")
	}
	_, err = s.sql("BEGIN IMMEDIATE; CREATE TABLE IF NOT EXISTS records (key TEXT PRIMARY KEY, data TEXT NOT NULL); PRAGMA user_version=1; COMMIT;")
	return err
}

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

// Transaction commits all changed records together; an error leaves history unchanged.
func (s *Store) Transaction(fn func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return errors.New("store is closed")
	}
	state, err := s.load()
	if err != nil {
		return err
	}
	before := flatten(state)
	if err = fn(&state); err != nil {
		return err
	}
	after := flatten(state)
	var sql strings.Builder
	sql.WriteString("BEGIN IMMEDIATE;\n")
	changed := false
	for key, data := range after {
		if before[key] == data {
			continue
		}
		changed = true
		fmt.Fprintf(&sql, "INSERT INTO records(key,data) VALUES(%s,%s) ON CONFLICT(key) DO UPDATE SET data=excluded.data;\n", literal(key), literal(data))
	}
	for key := range before {
		if _, ok := after[key]; !ok {
			changed = true
			fmt.Fprintf(&sql, "DELETE FROM records WHERE key=%s;\n", literal(key))
		}
	}
	if !changed {
		return nil
	}
	sql.WriteString("COMMIT;\n")
	_, err = s.sql(sql.String())
	return err
}

// Snapshot returns a detached consistent view.
func (s *Store) Snapshot() (State, error) {
	var st State
	err := s.Transaction(func(v *State) error { st = *v; return nil })
	return st, err
}

func literal(s string) string { return "CAST(X'" + hex.EncodeToString([]byte(s)) + "' AS TEXT)" }

func flatten(s State) map[string]string {
	records := map[string]string{}
	put := func(key string, v any) { b, _ := json.Marshal(v); records[key] = string(b) }
	put("next", s.Next)
	for k, v := range s.Items {
		put("item/"+k, v)
	}
	for k, v := range s.Attempts {
		put("attempt/"+k, v)
	}
	for _, v := range s.Updates {
		put(fmt.Sprintf("update/%012d", v.ID), v)
	}
	for k, v := range s.Locks {
		put("lock/"+k, v)
	}
	for k, v := range s.Auth {
		put("auth/"+k, v)
	}
	for k, v := range s.Receipts {
		put("receipt/"+k, v)
	}
	return records
}

func (s *Store) load() (State, error) {
	st := emptyState()
	out, err := s.sql("SELECT key,data FROM records ORDER BY key;")
	if err != nil {
		return st, err
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		return st, nil
	}
	var rows []struct {
		Key  string
		Data string
	}
	if err = json.Unmarshal(out, &rows); err != nil {
		return st, err
	}
	for _, row := range rows {
		if err = loadRecord(&st, row.Key, []byte(row.Data)); err != nil {
			return st, fmt.Errorf("control record %s: %w", row.Key, err)
		}
	}
	return st, nil
}

func loadRecord(s *State, key string, b []byte) error {
	kind, id, _ := strings.Cut(key, "/")
	switch kind {
	case "next":
		return json.Unmarshal(b, &s.Next)
	case "item":
		var v Item
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		s.Items[id] = v
	case "attempt":
		var v Attempt
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		s.Attempts[id] = v
	case "update":
		var v Update
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		s.Updates = append(s.Updates, v)
	case "lock":
		var v Lock
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		s.Locks[id] = v
	case "auth":
		var v Actor
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		s.Auth[id] = v
	case "receipt":
		var v receipt
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		s.Receipts[id] = v
	default:
		return fmt.Errorf("unknown record kind %q", kind)
	}
	return nil
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
	if _, err = s.sql("VACUUM INTO " + literal(tmp) + ";"); err != nil {
		return err
	}
	if err = os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	// Linking publishes atomically and refuses to replace any existing target.
	return os.Link(tmp, path)
}
