package control

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"unicode"
)

// batchFixture is a database of one plan with three tasks, and the updates
// the batched accessors tell apart.
func batchFixture(t *testing.T) *Tx {
	t.Helper()
	tx := blank(t)
	tx.PutItem(Item{ID: "r1", Kind: "repo", Slug: "owner/demo"})
	tx.PutItem(Item{ID: "p2", Kind: "plan", Repo: "r1", State: "planned"})
	tx.PutItem(Item{ID: "p9", Kind: "plan", Repo: "r1", State: "planned"})
	for _, task := range []struct{ id, state string }{{"t3", "todo"}, {"t4", "needs-human"}, {"t5", "running"}} {
		tx.PutItem(Item{ID: task.id, Kind: "task", Repo: "r1", Plan: "p2", State: task.state})
	}
	return tx
}

func put(tx *Tx, us ...Update) {
	for _, u := range us {
		tx.PutUpdate(u)
	}
}

func TestItemsByIDAreTheItemsOfEachID(t *testing.T) {
	tx := batchFixture(t)
	ids := []string{"t4", "p2", "nothing", "t3", "t4"}
	got := tx.ItemsByID(ids)
	for _, id := range ids {
		want, ok := tx.Item(id)
		if g, found := got[id]; found != ok || g.ID != want.ID || g.State != want.State {
			t.Errorf("%s: got %+v (%v), want %+v (%v)", id, g, found, want, ok)
		}
	}
	if len(got) != 3 || len(tx.ItemsByID(nil)) != 0 {
		t.Errorf("items = %v", slices.Sorted(maps.Keys(got)))
	}
	if tx.err != nil {
		t.Fatal(tx.err)
	}
}

func TestUpdatesByIDAreTheUpdatesOfEachSequence(t *testing.T) {
	tx := batchFixture(t)
	put(tx, Update{ID: 1, Item: "t3", Kind: "say", Body: "a"}, Update{ID: 2, Item: "t4", Kind: "say", Body: "b"})
	got := tx.UpdatesByID([]int{2, 7, 1})
	for _, id := range []int{1, 2, 7} {
		want, ok := tx.Update(id)
		if g, found := got[id]; found != ok || g.Body != want.Body {
			t.Errorf("%d: got %+v (%v), want %+v (%v)", id, g, found, want, ok)
		}
	}
	if len(got) != 2 || len(tx.UpdatesByID(nil)) != 0 {
		t.Errorf("updates = %v", got)
	}
}

func TestTaskStatesOfPlansAreTheStatesOfEachPlan(t *testing.T) {
	tx := batchFixture(t)
	got := tx.TaskStatesOfPlans([]string{"p2", "p9"})
	// p9 has no task, so it is absent as an empty list is not one.
	if _, ok := got["p9"]; ok || len(got) != 1 {
		t.Fatalf("plans = %v", got)
	}
	for _, plan := range []string{"p2", "p9"} {
		var want, have []string
		for _, it := range tx.TaskStatesOfPlan(plan) {
			want = append(want, it.State)
		}
		for _, it := range got[plan] {
			have = append(have, it.State)
		}
		slices.Sort(want)
		slices.Sort(have)
		if !slices.Equal(have, want) {
			t.Errorf("%s: got %v, want %v", plan, have, want)
		}
	}
	if len(tx.TaskStatesOfPlans(nil)) != 0 {
		t.Error("no plans, some states")
	}
}

func TestOwnQuestionsAreTheOwnQuestionOfEachItem(t *testing.T) {
	tx := batchFixture(t)
	put(tx,
		// t3: an answered question, then two open ones: the first open counts.
		Update{ID: 1, Item: "t3", Kind: "ask", RequiresReply: true},
		Update{ID: 2, Item: "t3", Kind: "resolved", ReplyTo: 1},
		Update{ID: 3, Item: "t3", Kind: "ask", RequiresReply: true},
		Update{ID: 4, Item: "t3", Kind: "ask", RequiresReply: true},
		// t4: its only question has a reply and an acknowledgement, which
		// do not resolve it, and a message that asks for no reply.
		Update{ID: 5, Item: "t4", Kind: "ask", RequiresReply: true},
		Update{ID: 6, Item: "t4", Kind: "reply", ReplyTo: 5},
		Update{ID: 7, Item: "t4", Kind: "acknowledged", ReplyTo: 5},
		Update{ID: 8, Item: "t4", Kind: "say"},
		// t5 has no question but p2 has one, asked by t4 on its plan: the
		// plan and t4 are held, t5 is not.
		Update{ID: 9, Item: "p2", Task: "t4", Kind: "ask", RequiresReply: true},
		// p9's question was answered in the pane.
		Update{ID: 10, Item: "p9", Kind: "ask", RequiresReply: true},
		Update{ID: 11, Item: "p9", Kind: KindPaneAnswer, ReplyTo: 10},
	)
	ids := []string{"t3", "t4", "t5", "p2", "p9", "nothing"}
	got := tx.OwnQuestions(ids)
	for _, id := range ids {
		if want := OwnQuestion(tx, id); got[id] != want {
			t.Errorf("%s: got %d, want %d", id, got[id], want)
		}
	}
	if got["t3"] != 3 || got["t4"] != 5 || got["p2"] != 9 || got["t5"] != 0 || got["p9"] != 0 {
		t.Errorf("questions = %v", got)
	}
	// A plan's question held by the task that asked it, for that task only.
	if q := tx.OwnQuestions([]string{"t4", "t5"}); q["t4"] != 5 || len(q) != 1 {
		t.Errorf("questions = %v", q)
	}
	put(tx, Update{ID: 12, Item: "t9", Task: "t4", Kind: "ask", RequiresReply: true})
	if q := tx.OwnQuestions([]string{"t4"}); q["t4"] != 5 {
		t.Errorf("an earlier question is not first: %v", q)
	}
	if len(tx.OwnQuestions(nil)) != 0 || tx.err != nil {
		t.Errorf("questions of no item: %v", tx.err)
	}
}

// newestMessage is the update a session row shows as its latest.
func newestMessage(tx *Tx, id string) (Update, bool) {
	for u := range tx.UpdatesAboutNewestFirst(id) {
		if u.Item == id && u.Author != "coordinator" && slices.Contains(humanKinds, u.Kind) && strings.TrimSpace(u.Body) != "" {
			return u, true
		}
	}
	return Update{}, false
}

func TestLatestMessagesAreTheNewestMessageOfEachItem(t *testing.T) {
	tx := batchFixture(t)
	put(tx,
		Update{ID: 1, Item: "t3", Author: "a1", Kind: "say", Body: "first"},
		Update{ID: 2, Item: "t3", Author: "a1", Kind: "say", Body: "second"},
		// Newer, but the coordinator's, not a message, or blank; and p2's,
		// which t3 asked on its plan: a message about p2, not about t3.
		Update{ID: 3, Item: "t3", Author: "coordinator", Kind: "say", Body: "dispatched"},
		Update{ID: 4, Item: "t3", Author: "a1", Kind: "state", Body: "running"},
		Update{ID: 5, Item: "t3", Author: "a1", Kind: "say", Body: " \n\t"},
		Update{ID: 6, Item: "t3", Author: "a1", Kind: "say", Body: "  　"},
		Update{ID: 7, Item: "p2", Task: "t3", Author: "a1", Kind: "ask", Body: "asked on the plan"},
		Update{ID: 8, Item: "t4", Author: "a2", Kind: "ask", Body: "a question"},
		Update{ID: 9, Item: "t4", Author: "operator", Kind: KindPaneAnswer, Body: "go on"},
		Update{ID: 10, Item: "t5", Author: "coordinator", Kind: "say", Body: "only the coordinator"},
		Update{ID: 11, Item: "p9", Author: "a3", Kind: "say", Body: "quote \" and \\ and\nnewline"},
	)
	ids := []string{"t3", "t4", "t5", "p2", "p9", "nothing"}
	got := tx.LatestMessages(ids, humanKinds)
	for _, id := range ids {
		want, ok := newestMessage(tx, id)
		if g, found := got[id]; found != ok || g.ID != want.ID || g.Body != want.Body {
			t.Errorf("%s: got %+v (%v), want %+v (%v)", id, g, found, want, ok)
		}
	}
	if got["t3"].ID != 2 || got["t4"].ID != 9 || got["p9"].ID != 11 || got["p2"].ID != 7 || len(got) != 4 {
		t.Errorf("messages = %+v", got)
	}
	if len(tx.LatestMessages(nil, humanKinds)) != 0 || tx.err != nil {
		t.Errorf("messages of no item: %v", tx.err)
	}
}

// The SQL test for a blank body trims the characters strings.TrimSpace does.
func TestSpaceCharsAreWhatTrimSpaceTrims(t *testing.T) {
	for r := rune(0); r <= 0x3000; r++ {
		if got, want := strings.ContainsRune(spaceChars, r), unicode.IsSpace(r); got != want && r != '\'' {
			t.Errorf("%U: in the set %v, a space %v", r, got, want)
		}
	}
}
