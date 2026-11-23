package control

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

// toRecords turns s's database back into a records-table database at
// version, as a server before version 7 kept it, and closes s. Every update
// that names an item snapshots it whole, as those servers did. It returns
// the database's path.
func toRecords(t *testing.T, s Service, version int) string {
	t.Helper()
	path := s.Store.path
	if err := s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.Exec(recordsOf + fmt.Sprintf("PRAGMA user_version=%d;", version)); err != nil {
		t.Fatal(err)
	}
	return path
}

// recordsOf is version 7's tables as one records table.
const recordsOf = `CREATE TABLE records (key TEXT PRIMARY KEY, data TEXT NOT NULL);
INSERT INTO records SELECT 'item/' || id, data FROM items;
INSERT INTO records SELECT 'attempt/' || id, data FROM attempts;
INSERT INTO records SELECT printf('update/%012d', u.id),
 CASE WHEN i.id IS NULL THEN u.data ELSE json_set(u.data, '$.snapshot', json(i.data)) END
 FROM updates u LEFT JOIN items i ON i.id = u.item;
INSERT INTO records SELECT 'queue/' || task, data FROM queue;
INSERT INTO records SELECT 'auth/' || hash, data FROM credentials;
INSERT INTO records SELECT 'receipt/' || key, data FROM receipts;
INSERT INTO records SELECT 'host/' || name, data FROM hosts;
INSERT INTO records SELECT 'next', value FROM counters WHERE name = 'next';
INSERT INTO records SELECT name, data FROM marks;
DROP TABLE items; DROP TABLE item_deps; DROP TABLE item_requires; DROP TABLE attempts; DROP TABLE updates;
DROP TABLE queue; DROP TABLE credentials; DROP TABLE receipts; DROP TABLE hosts; DROP TABLE counters; DROP TABLE marks;
`

// rawSQL runs statements on the database at path, which no store owns.
func rawSQL(t *testing.T, path, statements string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.ExecContext(context.Background(), statements); err != nil {
		t.Fatal(err)
	}
}

// userVersion is the schema version of the database at path.
func userVersion(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var v int
	if err = db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// openAt opens the database at path, failing the test on an error.
func openAt(t *testing.T, path string) *Store {
	t.Helper()
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
