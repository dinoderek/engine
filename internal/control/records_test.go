package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// records is every record of a store, read back in one read transaction for
// a test's assertions.
type records struct {
	store    *Store
	Items    map[string]Item
	Attempts map[string]Attempt
	Updates  []Update
	Queue    map[string]QueueEntry
	Auth     map[string]Actor
	Receipts map[string]receipt
	Hosts    map[string]HostRecord
	Next     int
	PassAt   time.Time
	Upgrade  UpgradeRecord
}

// snapshot reads every record.
func (s *Store) snapshot() (records, error) {
	r := records{store: s, Items: map[string]Item{}, Attempts: map[string]Attempt{}, Queue: map[string]QueueEntry{},
		Auth: map[string]Actor{}, Receipts: map[string]receipt{}}
	err := s.Read(context.Background(), func(tx *Tx) error {
		for _, item := range tx.ItemsByKindUnder("", "") {
			r.Items[item.ID] = item
		}
		for _, at := range tx.AttemptsFor("") {
			r.Attempts[at.ID] = at
		}
		r.Updates = slices.Collect(tx.UpdatesAfter(0, ""))
		for _, e := range tx.QueueEntries() {
			r.Queue[e.Task] = e
		}
		keyed(tx, "SELECT hash, data FROM credentials", r.Auth)
		keyed(tx, "SELECT key, data FROM receipts", r.Receipts)
		r.Hosts = tx.HostRecords()
		r.PassAt, r.Upgrade = tx.PassAt(), tx.Upgrade()
		tx.fail(tx.q.QueryRowContext(tx.ctx, "SELECT value FROM counters WHERE name='next'").Scan(&r.Next))
		return nil
	})
	return r, err
}

func keyed[T any](tx *Tx, query string, into map[string]T) {
	tx.rows(func(rows *sql.Rows) error {
		var key, data string
		var v T
		if err := rows.Scan(&key, &data); err != nil {
			return err
		}
		err := json.Unmarshal([]byte(data), &v)
		into[key] = v
		return err
	}, query)
}

// tx is a read Tx on the store outside any transaction: each of its queries
// sees the newest commit.
func (r records) tx() *Tx {
	return &Tx{ctx: context.Background(), q: r.store.readers}
}

// blank is a writable Tx on a new, empty database, outside any transaction:
// each write commits at once.
func blank(t *testing.T) *Tx {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &Tx{ctx: context.Background(), q: store.writer, write: true}
}

// mustSnapshot reads every record of s's store.
func mustSnapshot(t *testing.T, s Service) records {
	t.Helper()
	st, err := s.Store.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// edit runs fn in one write on s, failing the test on an error.
func edit(t *testing.T, s *Store, fn func(*Tx)) {
	t.Helper()
	if err := s.Write(context.Background(), func(tx *Tx) error { fn(tx); return nil }); err != nil {
		t.Fatal(err)
	}
}

// editAttempt changes the stored attempt id with fn.
func editAttempt(t *testing.T, s *Store, id string, fn func(*Attempt)) {
	t.Helper()
	edit(t, s, func(tx *Tx) {
		at := tx.attempt(id)
		fn(&at)
		tx.PutAttempt(at)
	})
}

// editItem changes the stored item id with fn.
func editItem(t *testing.T, s *Store, id string, fn func(*Item)) {
	t.Helper()
	edit(t, s, func(tx *Tx) {
		item := tx.item(id)
		fn(&item)
		tx.PutItem(item)
	})
}

// newRecords is an empty set of records, for a test to fill and load.
func newRecords() records {
	return records{Items: map[string]Item{}, Attempts: map[string]Attempt{}, Queue: map[string]QueueEntry{},
		Auth: map[string]Actor{}, Receipts: map[string]receipt{}, Hosts: map[string]HostRecord{}, Next: 1}
}

// load writes r into a new, empty database and returns a writable Tx on it
// (blank).
func (r records) load(t *testing.T) *Tx {
	t.Helper()
	tx := blank(t)
	for _, item := range r.Items {
		tx.PutItem(item)
	}
	for _, at := range r.Attempts {
		tx.PutAttempt(at)
	}
	for i, u := range r.Updates {
		if u.ID == 0 {
			u.ID = i + 1
		}
		tx.PutUpdate(u)
	}
	for _, e := range r.Queue {
		tx.PutQueueEntry(e)
	}
	for hash, a := range r.Auth {
		tx.PutCredential(hash, a)
	}
	for key, rec := range r.Receipts {
		tx.PutReceipt(key, rec)
	}
	for name, rec := range r.Hosts {
		tx.PutHost(name, rec)
	}
	if !r.PassAt.IsZero() {
		tx.SetPassAt(r.PassAt)
	}
	if !r.Upgrade.Start.IsZero() {
		tx.SetUpgrade(r.Upgrade)
	}
	tx.exec("UPDATE counters SET value=? WHERE name='next'", max(r.Next, 1))
	if tx.err != nil {
		t.Fatal(tx.err)
	}
	return tx
}
