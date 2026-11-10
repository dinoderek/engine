package control

import "testing"

func TestCleanedNeedsTheTasksOwnExecutionAndADoneTask(t *testing.T) {
	s := service(t)
	task, a, _ := reviewed(t, s)
	cleaned := Request{Op: "cleaned", ID: task.ID}
	reject(t, s, a, cleaned, 409)
	if err := s.RecordMerged(task.ID, good().PR, "merged"); err != nil {
		t.Fatal(err)
	}
	reject(t, s, op, cleaned, 403)
	reject(t, s, a, Request{Op: "cleaned", ID: "t99"}, 403)

	first := call(t, s, a, cleaned).Updates[0]
	if first.Kind != Cleaned || first.Left != "" || first.Body != "Cleaned up after the merge." || first.Item != task.ID {
		t.Fatalf("cleaned = %+v", first)
	}
	again := call(t, s, a, Request{Op: "cleaned", ID: task.ID, Body: "the db volume"}).Updates[0]
	if again.Left != "the db volume" || again.Body != "Cleaned up after the merge, but left: the db volume" {
		t.Fatalf("cleaned --left = %+v", again)
	}
	var newest Update
	var ok bool
	if err := s.Store.Transaction(func(st *State) error {
		newest, ok = CleanedUp(st, task.ID)
		if _, other := CleanedUp(st, "t99"); other {
			t.Error("a task with no report has one")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !ok || newest.ID != again.ID {
		t.Fatalf("CleanedUp = %+v %v, want the newest report", newest, ok)
	}
	if got := call(t, s, op, Request{Op: "get", ID: task.ID}).Item.State; got != "done" {
		t.Fatalf("the task is %s after its report, want done", got)
	}
}
