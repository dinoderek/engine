package control

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// fixtureDB is a new version 6 database holding testdata/records-v6.sql,
// which a version 6 server wrote, with every record kind.
func fixtureDB(t *testing.T) string {
	t.Helper()
	script, err := os.ReadFile("testdata/records-v6.sql")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "control.db")
	rawSQL(t, path, string(script))
	return path
}

// fixtureNow is the clock the version 6 server answered the fixture's reads by.
var fixtureNow = func() time.Time { return time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC) }

// fixtureReads are the fixture's read requests and the version 6 server's
// answers to them, every update's item snapshot slimmed.
func fixtureReads(t *testing.T) []fixtureRead {
	t.Helper()
	b, err := os.ReadFile("testdata/records-v6-reads.json")
	if err != nil {
		t.Fatal(err)
	}
	var reads []fixtureRead
	if err = json.Unmarshal(b, &reads); err != nil {
		t.Fatal(err)
	}
	for i := range reads {
		for j, u := range reads[i].Response.Updates {
			if u.Snapshot != nil {
				reads[i].Response.Updates[j].Snapshot = slim(*u.Snapshot)
			}
		}
	}
	return reads
}

type fixtureRead struct {
	Request  Request  `json:"request"`
	Response Response `json:"response"`
}

func TestMigratingTheVersion6FixtureAnswersEveryReadAsBefore(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	reads := fixtureReads(t)
	ops := map[string]bool{}
	s := Service{Store: openAt(t, fixtureDB(t)), Now: fixtureNow}
	for _, r := range reads {
		ops[r.Request.Op] = true
		got, err := s.Execute(op, r.Request)
		if err != nil {
			t.Fatalf("%+v: %v", r.Request, err)
		}
		if r.Request.Op == "plan-view" {
			// A version 6 server listed a plan's tasks in map order.
			byID := func(a, b Item) int { return cmp.Compare(a.ID, b.ID) }
			slices.SortFunc(got.Items, byID)
			slices.SortFunc(r.Response.Items, byID)
		}
		if g, w := fingerprint(got), fingerprint(r.Response); g != w {
			a, _ := json.Marshal(got)
			b, _ := json.Marshal(r.Response)
			t.Errorf("%+v after the migration:\n got %s\nwant %s", r.Request, a, b)
		}
	}
	for _, name := range readOps {
		if !ops[name] {
			t.Errorf("the fixture has no %s read", name)
		}
	}
}

func TestMigratingTheVersion6FixtureKeepsEveryRecord(t *testing.T) {
	path := fixtureDB(t)
	var rows []struct{ Key, Data string }
	out, err := fixtureSQL(t.Context(), path, "SELECT key, data FROM records ORDER BY key")
	if err == nil {
		err = json.Unmarshal(out, &rows)
	}
	if err != nil {
		t.Fatal(err)
	}
	store := openAt(t, path)
	st, err := store.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, row := range rows {
		kind, id, _ := strings.Cut(row.Key, "/")
		kinds[kind]++
		want, got := migrated(t, st, kind, id, row.Data)
		if !reflect.DeepEqual(want, got) {
			t.Errorf("%s migrated to %+v, want %+v", row.Key, got, want)
		}
	}
	for _, kind := range []string{"item", "attempt", "update", "queue", "auth", "receipt", "host", "next", "pass", "upgrade"} {
		if kinds[kind] == 0 {
			t.Errorf("the fixture has no %s record", kind)
		}
	}
	if len(st.Items) != kinds["item"] || len(st.Attempts) != kinds["attempt"] || len(st.Updates) != kinds["update"] ||
		len(st.Queue) != kinds["queue"] || len(st.Auth) != kinds["auth"] || len(st.Receipts) != kinds["receipt"] || len(st.Hosts) != kinds["host"] {
		t.Errorf("the migration kept %d items, %d attempts, %d updates, %d queue entries, %d credentials, %d receipts and %d hosts of %v",
			len(st.Items), len(st.Attempts), len(st.Updates), len(st.Queue), len(st.Auth), len(st.Receipts), len(st.Hosts), kinds)
	}
	telegram, offset, digest, err := store.TelegramState()
	if err != nil || len(telegram) != 1 || offset != 12 || digest.IsZero() {
		t.Errorf("Telegram state %+v %d %v %v", telegram, offset, digest, err)
	}
}

// migrated is a record of kind as the version 6 server wrote it, its update
// snapshot slimmed, beside what the migrated database holds.
func migrated(t *testing.T, st records, kind, id, data string) (any, any) {
	t.Helper()
	switch kind {
	case "item":
		return decoded[Item](t, data), st.Items[id]
	case "attempt":
		return decoded[Attempt](t, data), st.Attempts[id]
	case "update":
		u := decoded[Update](t, data)
		if u.Snapshot != nil {
			u.Snapshot = slim(*u.Snapshot)
		}
		return u, st.Updates[u.ID-1]
	case "queue":
		return decoded[QueueEntry](t, data), st.Queue[id]
	case "auth":
		return decoded[Actor](t, data), st.Auth[id]
	case "receipt":
		return decoded[receipt](t, data), st.Receipts[id]
	case "host":
		return decoded[HostRecord](t, data), st.Hosts[id]
	case "next":
		return decoded[int](t, data), st.Next
	case "pass":
		return decoded[time.Time](t, data), st.PassAt
	case "upgrade":
		return decoded[UpgradeRecord](t, data), st.Upgrade
	}
	t.Fatalf("unknown record kind %s", kind)
	return nil, nil
}

func decoded[T any](t *testing.T, data string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(data), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestTheMigrationSlimsEveryUpdatesSnapshot(t *testing.T) {
	store := openAt(t, fixtureDB(t))
	s := Service{Store: store, Now: fixtureNow}
	call(t, s, op, Request{Op: "say", ID: "t3", Body: "after the migration"})
	out, err := store.sql("SELECT count(*) AS n FROM updates WHERE json_type(data, '$.snapshot') = 'object';" +
		"SELECT DISTINCT j.key FROM updates, json_each(data, '$.snapshot') j ORDER BY j.key")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `[{"key":"id"},{"key":"kind"},{"key":"state"},{"key":"title"},{"key":"version"}]`) ||
		strings.Contains(string(out), `"n":0`) {
		t.Fatalf("snapshots keep %s; want only id, kind, state, title and version", out)
	}
}

func TestTheMigrationFirstWritesAPrivateBackupOfTheOldDatabase(t *testing.T) {
	path := fixtureDB(t)
	openAt(t, path)
	backup := path + ".pre-v7"
	info, err := os.Stat(backup)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("backup %v %v; want an owner-only file", info, err)
	}
	if v := userVersion(t, backup); v != 6 {
		t.Fatalf("backup at version %d, want 6", v)
	}
	count := "SELECT count(*) AS n FROM records"
	got, err := fixtureSQL(t.Context(), backup, count)
	want, _ := fixtureSQL(t.Context(), fixtureDB(t), count)
	if err != nil || string(got) != string(want) {
		t.Fatalf("backup records %s, want %s (%v)", got, want, err)
	}
	if v := userVersion(t, path); v != schemaVersion {
		t.Fatalf("database at version %d, want %d", v, schemaVersion)
	}
}

func TestAnExistingBackupStopsTheMigrationAndIsNamed(t *testing.T) {
	path := fixtureDB(t)
	backup := path + ".pre-v7"
	if err := os.WriteFile(backup, []byte("an earlier backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil || !strings.Contains(err.Error(), backup) {
		t.Fatalf("open with a backup present: %v; want a refusal naming %s", err, backup)
	}
	if b, _ := os.ReadFile(backup); string(b) != "an earlier backup" {
		t.Fatal("the migration overwrote an existing backup")
	}
	if v := userVersion(t, path); v != 6 {
		t.Fatalf("database at version %d, want it left at 6", v)
	}
}

func TestAFailedMigrationLeavesTheDatabaseAtVersion6(t *testing.T) {
	for name, bad := range map[string]string{
		"an unknown record":            "INSERT INTO records VALUES('unknown/x', '{}')",
		"a record not JSON":            "INSERT INTO records VALUES('item/x', 'invalid')",
		"an item with no kind":         `INSERT INTO records VALUES('item/x', '{"id":"x"}')`,
		"a corrupt next record":        "UPDATE records SET data='[' WHERE key='next'",
		"an item of the wrong shape":   `INSERT INTO records VALUES('item/x', '{"id":"x","kind":"task","depends":"t3"}')`,
		"a receipt of the wrong shape": `INSERT INTO records VALUES('receipt/x', '{"digest":7}')`,
	} {
		t.Run(name, func(t *testing.T) {
			path := fixtureDB(t)
			rawSQL(t, path, bad)
			if _, err := Open(path); err == nil {
				t.Fatal("a migration with a bad record succeeded")
			}
			out, err := fixtureSQL(t.Context(), path, "SELECT count(*) AS n FROM sqlite_schema WHERE name IN ('records', 'items')")
			if err != nil || string(out) != `[{"n":1}]` || userVersion(t, path) != 6 {
				t.Fatalf("after a failed migration: tables %s %v, version %d", out, err, userVersion(t, path))
			}
		})
	}
}

func TestANewerDatabaseIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.db")
	if err := openAt(t, path).Close(); err != nil {
		t.Fatal(err)
	}
	rawSQL(t, path, fmt.Sprintf("PRAGMA user_version=%d", schemaVersion+1))
	_, err := Open(path)
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("schema %d is newer", schemaVersion+1)) {
		t.Fatalf("open of a newer database: %v", err)
	}
}

func TestACurrentDatabaseOpensWithoutReadingARecordsTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.db")
	s := Service{Store: openAt(t, path), Now: fixtureNow}
	repoPlan(t, s)
	if err := s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	// A records table a current open read would fail on.
	rawSQL(t, path, "CREATE TABLE records (key TEXT PRIMARY KEY, data TEXT NOT NULL); INSERT INTO records VALUES('unknown/x', 'invalid')")
	store := openAt(t, path)
	if got := len(mustSnapshot(t, Service{Store: store}).Items); got != 2 {
		t.Fatalf("%d items after reopening", got)
	}
	if _, err := os.Stat(path + backupSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a current open wrote a migration backup: %v", err)
	}
}
