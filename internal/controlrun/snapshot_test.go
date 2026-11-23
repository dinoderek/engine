package controlrun

import (
	"path/filepath"
	"testing"

	"github.com/dinoderek/engine/internal/control"
)

// snapshot is a copy of the records a test inspects, read through Store.Read.
type snapshot struct {
	Items    map[string]control.Item
	Attempts map[string]control.Attempt
	Updates  []control.Update
	Queue    map[string]control.QueueEntry
	Hosts    map[string]control.HostRecord
}

// snapshotOf reads every record of s.
func snapshotOf(s *control.Store) (snapshot, error) {
	snap := snapshot{Items: map[string]control.Item{}, Attempts: map[string]control.Attempt{}, Queue: map[string]control.QueueEntry{}}
	err := read(s, func(tx *control.Tx) error {
		for _, item := range tx.ItemsByKindUnder("", "") {
			snap.Items[item.ID] = item
		}
		for _, at := range tx.AttemptsFor("") {
			snap.Attempts[at.ID] = at
		}
		for u := range tx.UpdatesAfter(0, "") {
			snap.Updates = append(snap.Updates, u)
		}
		for _, e := range tx.QueueEntries() {
			snap.Queue[e.Task] = e
		}
		snap.Hosts = tx.HostRecords()
		return nil
	})
	return snap, err
}

// inTx runs fn on a read view of the records of s.
func inTx(t *testing.T, s *control.Store, fn func(*control.Tx)) {
	t.Helper()
	if err := read(s, func(tx *control.Tx) error { fn(tx); return nil }); err != nil {
		t.Fatal(err)
	}
}

// mutate runs fn in a write transaction on s.
func mutate(t *testing.T, s *control.Store, fn func(*control.Tx) error) {
	t.Helper()
	if err := write(s, fn); err != nil {
		t.Fatal(err)
	}
}

// emptyStore is a store with no records, closed with the test.
func emptyStore(t *testing.T) *control.Store {
	t.Helper()
	store, err := control.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// seeded is a store holding exactly these items and attempts.
func seeded(t *testing.T, items []control.Item, attempts []control.Attempt) *control.Store {
	t.Helper()
	store := emptyStore(t)
	mutate(t, store, func(tx *control.Tx) error {
		for _, item := range items {
			tx.PutItem(item)
		}
		for _, at := range attempts {
			tx.PutAttempt(at)
		}
		return nil
	})
	return store
}
