package controlrun

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/reconcile"
)

// fixtureRecords are the IDs of every repository, plan, task and attempt in
// the version 6 fixture database. Its one coordinating plan is p2; p15 is a
// plan of the same repository still being prepared.
var fixtureRecords = []string{"r1", "p2", "p15", "t3", "t5", "t7", "t9", "t10", "t11", "t13", "a4", "a6", "a8", "a12", "a14"}

// migratedFixture is a store on a copy of the version 6 fixture database,
// which a version 6 server wrote, migrated as the store opens it.
func migratedFixture(t *testing.T) (string, *control.Store) {
	t.Helper()
	script, err := os.ReadFile(filepath.Join("..", "control", "testdata", "records-v6.sql"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "control.db")
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path}).String())
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(context.Background(), string(script))
	if cerr := db.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
	store, err := control.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return path, store
}

// readsNone reports each query of reads given one of ids.
func readsNone(t *testing.T, plan string, reads []planRead, ids []string) {
	t.Helper()
	for _, r := range reads {
		for _, arg := range r.args {
			for _, id := range ids {
				if arg == id {
					t.Errorf("the View of %s read %s: %s", plan, id, r.query)
				}
			}
		}
	}
}

// On the migrated fixture with a second coordinating plan in a second
// repository, each plan's View reads only its own plan's records, walks no
// table, and holds only that plan's tasks; the fixture plan's View is the one
// it had alone. A whole pass over both plans then walks no table either.
func TestAPassOverTwoPlansOfTheMigratedFixtureReadsEachPlansOwnRecords(t *testing.T) {
	path, store := migratedFixture(t)
	c, _ := coordinatorOn(t, store)
	var alone reconcile.View
	inTx(t, store, func(tx *control.Tx) { alone = c.view(tx, "p2", reconcile.Fleet{}) })
	if len(alone.Tasks) != 7 {
		t.Fatalf("the fixture plan's View has %d tasks, want 7", len(alone.Tasks))
	}
	other := otherPlan(t, c)
	foreign := map[string][]string{"p2": append([]string{"p15"}, other...), other[1]: fixtureRecords}

	for plan, ids := range foreign {
		var v reconcile.View
		reads := tracedRead(t, path, store, func(tx *control.Tx) { v = c.view(tx, plan, reconcile.Fleet{}) })
		readsNone(t, plan, reads, ids)
		if walked := scans(reads); len(walked) > 0 {
			t.Errorf("the View of %s scans tables: %v", plan, walked)
		}
		if v.Root.ID != plan {
			t.Errorf("the View of %s has root %s", plan, v.Root.ID)
		}
		for _, task := range v.Tasks {
			if task.Plan != plan {
				t.Errorf("the View of %s holds %s of %s", plan, task.ID, task.Plan)
			}
		}
		if plan == "p2" && (!reflect.DeepEqual(v.Root, alone.Root) || !reflect.DeepEqual(v.Tasks, alone.Tasks) || !reflect.DeepEqual(v.Lint, alone.Lint)) {
			t.Errorf("another plan changed the View of p2\n got %+v\nwant %+v", v.Tasks, alone.Tasks)
		}
		if plan == other[1] && (len(v.Tasks) != 1 || v.Tasks[0].Merge == nil || v.Tasks[0].Question == 0) {
			t.Errorf("the second plan's View lacks its task's merge request or question: %+v", v.Tasks)
		}
	}

	reads := tracedRead(t, path, store, func(*control.Tx) { pass(t, c) })
	if walked := scans(reads); len(walked) > 0 {
		t.Errorf("a pass over both plans scans tables: %v", walked)
	}
}
