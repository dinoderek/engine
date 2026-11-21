package control

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTelegramMigrationAndDurableState(t *testing.T) {
	s := service(t)
	_, plan := repoPlan(t, s)
	call(t, s, op, Request{Op: "say", ID: plan.ID, Body: "kept"})
	if _, err := s.Store.sql("DROP TABLE telegram_deliveries; DROP TABLE telegram_state; PRAGMA user_version=4;"); err != nil {
		t.Fatal(err)
	}
	path := s.Store.path
	if err := s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 1, 1, 0, 0, 0, 123, time.UTC)
	want := []TelegramDelivery{{Update: 10, Message: 99, Text: "quote ' ;\ntext", Settled: true}, {Update: 11}}
	if err = store.SaveTelegram(want, 42, at); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveTelegram(nil, 2, time.Time{}); err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(t.TempDir(), "backup.db")
	if err = store.Backup(copyPath); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	store, err = Open(copyPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, offset, digest, err := store.TelegramState()
	if err != nil || !reflect.DeepEqual(got, want) || offset != 42 || !digest.Equal(at) {
		t.Fatalf("state %+v %d %v %v", got, offset, digest, err)
	}
	st, err := store.Snapshot()
	if err != nil || len(st.Updates) != 3 {
		t.Fatalf("migration lost commands: %+v %v", st, err)
	}
	_ = store.Close()
	if _, _, _, err = store.TelegramState(); err == nil {
		t.Fatal("closed read")
	}
	if err = store.SaveTelegram(nil, 0, time.Time{}); err == nil {
		t.Fatal("closed write")
	}
}

func TestTelegramRecordsStayOutsideCommandRecords(t *testing.T) {
	s := service(t)
	if err := s.Store.SaveTelegram([]TelegramDelivery{{Update: 7, Text: "private"}}, 0, time.Time{}); err != nil {
		t.Fatal(err)
	}
	_, plan := repoPlan(t, s)
	call(t, s, op, Request{Op: "say", ID: plan.ID, Body: "command"})
	rows, _, _, err := s.Store.TelegramState()
	if err != nil || len(rows) != 1 || rows[0].Text != "private" {
		t.Fatalf("%+v %v", rows, err)
	}
	out, err := s.Store.sql("PRAGMA user_version;")
	if err != nil || !strings.Contains(string(out), `"user_version":5`) {
		t.Fatalf("%s %v", out, err)
	}
}
