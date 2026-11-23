package control

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRestartBackupAndExclusiveOwnership(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	call(t, s, op, Request{Op: "say", ID: plan.ID, Body: "persisted"})
	path := s.Store.path
	if _, err := Open(path); err == nil {
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
	if _, err := s.Store.snapshot(); err == nil {
		t.Fatal("closed store accepted command")
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	st, err := reopened.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if st.Items[repo.ID].Slug != "owner/demo" || len(st.Updates) != 3 {
		t.Fatalf("restart lost state: %+v", st)
	}
	copyStore, err := Open(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = copyStore.Close() }()
	copied, err := copyStore.snapshot()
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
	before, _ := s.Store.snapshot()
	if _, err := s.Store.writer.Exec("CREATE TRIGGER fail_insert BEFORE INSERT ON receipts BEGIN SELECT RAISE(ABORT, 'disk full'); END;"); err != nil {
		t.Fatal(err)
	}
	_, err := s.Execute(op, Request{Op: "say", ID: plan.ID, Body: "not committed", Key: "retry"})
	if err == nil {
		t.Fatal("invalid transaction succeeded")
	}
	if _, err := s.Store.writer.Exec("DROP TRIGGER fail_insert"); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Store.snapshot()
	if fingerprint(before) != fingerprint(after) {
		t.Fatal("partial transaction committed")
	}
	call(t, s, op, Request{Op: "say", ID: plan.ID, Body: "not committed", Key: "retry"})
	st, _ := s.Store.snapshot()
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
	if _, err := Open(path); err == nil {
		t.Fatal("future schema accepted")
	}
	if _, err := Open(""); err == nil {
		t.Fatal("empty database path accepted")
	}
	invalid := filepath.Join(t.TempDir(), "db")
	if err := os.WriteFile(invalid, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(invalid); err == nil {
		t.Fatal("corrupt database accepted")
	}
}
