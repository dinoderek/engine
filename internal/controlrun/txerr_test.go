package controlrun

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/reconcile"
)

// TestAPassActsOnNoViewAQueryFailedToRead corrupts an update only the pass's
// view reads: the pass must fail before deciding, never dispatch on what it
// could not read.
func TestAPassActsOnNoViewAQueryFailedToRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	c, h, _, _, task := setupAt(t, path)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.Exec("UPDATE updates SET requires_reply=1, data='not json' WHERE item=?", task.ID); err != nil {
		t.Fatal(err)
	}
	if err = c.Pass(context.Background()); err == nil {
		t.Fatal("a pass over a corrupt update succeeded")
	}
	if h.calls != 0 || len(h.launches) != 0 {
		t.Fatalf("the pass acted: %d host calls, launches %v", h.calls, h.launches)
	}
	// Deciding on a failed view returns its error instead of acting.
	err = read(c.Service.Store, func(tx *control.Tx) error {
		for range tx.QuestionsAbout(task.ID) {
		}
		if tx.Err() == nil {
			t.Fatal("a corrupt question read")
		}
		if err := c.decideAndApply(context.Background(), tx, task.Plan, reconcile.Fleet{}, c.Config); err == nil {
			t.Error("a pass decided on a failed read")
		}
		return nil
	})
	if err == nil {
		t.Fatal("a failed read returned no error")
	}
}
