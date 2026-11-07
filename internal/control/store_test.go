package control

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreRestartBackupAndExclusiveOwnership(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	call(t, s, op, Request{Op: "say", ID: plan.ID, Body: "persisted"})
	path := s.Store.path
	if _, err := Open(path, nil); err == nil {
		t.Fatal("second server opened database")
	}
	backup := filepath.Join(t.TempDir(), "copy.db")
	if err := s.Store.Backup(backup); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.Backup(backup); err == nil {
		t.Fatal("backup overwrote existing file")
	}
	if err := s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.Snapshot(); err == nil {
		t.Fatal("closed store accepted command")
	}
	reopened, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	st, err := reopened.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if st.Items[repo.ID].Slug != "owner/demo" || len(st.Updates) != 3 {
		t.Fatalf("restart lost state: %+v", st)
	}
	copyStore, err := Open(backup, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = copyStore.Close() }()
	copied, err := copyStore.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if fingerprint(st) != fingerprint(copied) {
		t.Fatal("backup differs")
	}
	info, err := os.Stat(backup)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("backup is not private")
	}
}

func TestSQLiteFailureRollsBackStateAndHistory(t *testing.T) {
	s := service(t)
	_, plan := repoPlan(t, s)
	before, _ := s.Store.Snapshot()
	fail := true
	s.Store.run = func(ctx context.Context, path, sql string) ([]byte, error) {
		if fail && strings.Contains(sql, "INSERT INTO records") {
			return SQLite(ctx, path, strings.Replace(sql, "COMMIT;", "INVALID SQL; COMMIT;", 1))
		}
		return SQLite(ctx, path, sql)
	}
	_, err := s.Execute(op, Request{Op: "say", ID: plan.ID, Body: "not committed", Key: "retry"})
	if err == nil {
		t.Fatal("invalid transaction succeeded")
	}
	fail = false
	after, _ := s.Store.Snapshot()
	if fingerprint(before) != fingerprint(after) {
		t.Fatal("partial transaction committed")
	}
	call(t, s, op, Request{Op: "say", ID: plan.ID, Body: "not committed", Key: "retry"})
	st, _ := s.Store.Snapshot()
	if len(st.Updates) != len(before.Updates)+1 {
		t.Fatal("failed request could not retry")
	}
}

func TestSQLiteValuesCannotBecomeSQLOrDotCommands(t *testing.T) {
	s := service(t)
	text := "quote ' ; DROP TABLE records;\n.shell echo forbidden\n\x00 unicode 🍝"
	item := call(t, s, op, Request{Op: "create", Item: Item{Kind: "repo", Title: text, Slug: "x/y", Remote: "ssh://example.test/y"}}).Item
	got := call(t, s, op, Request{Op: "get", ID: item.ID}).Item
	if got.Title != text {
		t.Fatalf("text changed: %q", got.Title)
	}
	if len(call(t, s, op, Request{Op: "list"}).Items) != 1 {
		t.Fatal("table destroyed")
	}
}

func TestStoreRefusesUnknownSchemaAndCorruption(t *testing.T) {
	s := service(t)
	path := s.Store.path
	if _, err := s.Store.sql("PRAGMA user_version=999;"); err != nil {
		t.Fatal(err)
	}
	_ = s.Store.Close()
	if _, err := Open(path, nil); err == nil {
		t.Fatal("future schema accepted")
	}
	if _, err := Open("", nil); err == nil {
		t.Fatal("empty database path accepted")
	}
	if _, err := Open(filepath.Join(t.TempDir(), "db"), func(context.Context, string, string) ([]byte, error) { return nil, errors.New("missing sqlite") }); err == nil {
		t.Fatal("sqlite failure accepted")
	}
	for _, key := range []string{"unknown/x", "item/x", "attempt/x", "update/x", "auth/x", "receipt/x", "lock/x", "next"} {
		st := emptyState()
		if err := loadRecord(&st, key, []byte("invalid")); err == nil {
			t.Fatalf("%s corruption accepted", key)
		}
	}
}
