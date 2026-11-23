package control

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"
)

// paneSession is a dispatched task whose session asked a question, and the
// private half of its run's pane key, recorded as the run started.
func paneSession(t *testing.T, s Service) (Item, Actor, Update, ed25519.PrivateKey) {
	t.Helper()
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	worker, claimed := dispatch(t, s, task)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Started(claimed.Attempt.ID, "@4", hex.EncodeToString(pub)); err != nil {
		t.Fatal(err)
	}
	question := call(t, s, worker, Request{Op: "ask", ID: task.ID, Body: "Approve the schema change?"}).Updates[0]
	return task, worker, question, priv
}

// vouched is a pane-input of text signed as the prompt hook signs it, under
// a fresh request key, found in the transcript and typed at at.
func vouched(priv ed25519.PrivateKey, attempt, text string, at time.Time) Request {
	r := Request{Op: "pane-input", Key: Secret(), Body: text, Pane: &PaneInput{Transcript: true, At: at}}
	r.Pane.Signature = hex.EncodeToString(ed25519.Sign(priv, PaneMessage(attempt, r.Key, *r.Pane, text)))
	return r
}

// notes are the coordinator's notes to the operator on item.
func notes(st State, item string) []Update {
	var out []Update
	for _, u := range st.Updates {
		if u.Item == item && u.Author == "coordinator" && u.Kind == "say" && u.Recipient == "operator" {
			out = append(out, u)
		}
	}
	return out
}

func TestASignedPaneAnswerResolvesTheTasksQuestion(t *testing.T) {
	s := service(t)
	task, worker, question, priv := paneSession(t, s)
	const answer = "Yes, approved.\nDrop the old column too. "
	out := call(t, s, worker, vouched(priv, worker.Attempt, answer, time.Time{}))
	if len(out.Updates) != 2 {
		t.Fatalf("updates = %+v, want the answer and the restored state", out.Updates)
	}
	got := out.Updates[0]
	if got.Kind != KindPaneAnswer || got.Body != answer || got.ReplyTo != question.ID || got.Item != task.ID || got.Author != worker.Attempt {
		t.Errorf("answer = %+v, want the text verbatim replying to %d", got, question.ID)
	}
	st := mustSnapshot(t, s)
	if st.Items[task.ID].State != "running" || OwnQuestion(st.LegacyTx(), task.ID) != 0 {
		t.Errorf("task %s, open question %d; want it running with none", st.Items[task.ID].State, OwnQuestion(st.LegacyTx(), task.ID))
	}
	if n := notes(st, task.ID); len(n) != 0 {
		t.Errorf("notes = %+v, want none for a vouched answer", n)
	}
	inbox := call(t, s, op, Request{Op: "inbox"}).Updates
	for _, u := range inbox {
		if u.ID == question.ID {
			t.Error("the answered question is still in the inbox")
		}
	}
}

func TestAPaneAnswerTheHookDidNotVouchForResolvesWithOneNote(t *testing.T) {
	for _, tc := range []struct {
		name   string
		post   func(ed25519.PrivateKey, string) Request
		reason string
	}{
		{"unsigned", func(ed25519.PrivateKey, string) Request {
			return Request{Op: "pane-input", Body: "Approved", Pane: &PaneInput{Transcript: true}}
		}, "it is unsigned"},
		{"no vouch at all", func(ed25519.PrivateKey, string) Request { return Request{Op: "pane-input", Body: "Approved"} }, "it is unsigned"},
		{"signed with another key", func(_ ed25519.PrivateKey, a string) Request {
			_, other, _ := ed25519.GenerateKey(rand.Reader)
			return vouched(other, a, "Approved", time.Time{})
		}, "its signature does not match"},
		{"signed for other text", func(k ed25519.PrivateKey, a string) Request {
			r := vouched(k, a, "Rejected", time.Time{})
			r.Body = "Approved"
			return r
		}, "its signature does not match"},
		{"resent under another request key", func(k ed25519.PrivateKey, a string) Request {
			r := vouched(k, a, "Approved", time.Time{})
			r.Key = Secret()
			return r
		}, "its signature does not match"},
		{"resent with another time", func(k ed25519.PrivateKey, a string) Request {
			r := vouched(k, a, "Approved", time.Time{})
			r.Pane.At = time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
			return r
		}, "its signature does not match"},
		{"missing from the transcript", func(k ed25519.PrivateKey, a string) Request {
			r := vouched(k, a, "Approved", time.Time{})
			r.Pane.Transcript = false
			return r
		}, "its signature does not match the run's pane key, and it is missing from the prompts"},
		{"signed as missing from the transcript", func(k ed25519.PrivateKey, a string) Request {
			r := Request{Op: "pane-input", Key: Secret(), Body: "Approved", Pane: &PaneInput{}}
			r.Pane.Signature = hex.EncodeToString(ed25519.Sign(k, PaneMessage(a, r.Key, *r.Pane, r.Body)))
			return r
		}, "missing from the prompts in the session's transcript"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := service(t)
			task, worker, question, priv := paneSession(t, s)
			out := call(t, s, worker, tc.post(priv, worker.Attempt))
			if out.Updates[0].Kind != KindPaneAnswer || out.Updates[0].Body != "Approved" {
				t.Fatalf("answer = %+v, want it recorded", out.Updates[0])
			}
			st := mustSnapshot(t, s)
			if st.Items[task.ID].State != "running" || OwnQuestion(st.LegacyTx(), task.ID) != 0 {
				t.Errorf("task %s, open question %d; want it resolved all the same", st.Items[task.ID].State, OwnQuestion(st.LegacyTx(), task.ID))
			}
			n := notes(st, task.ID)
			if len(n) != 1 || !strings.Contains(n[0].Body, tc.reason) || !strings.Contains(n[0].Body, fmt.Sprintf("resolved question %d", question.ID)) {
				t.Fatalf("notes = %+v, want one naming %q", n, tc.reason)
			}
			if !strings.Contains(n[0].Body, "engine task stop "+task.ID) || !strings.Contains(n[0].Body, "engine tell "+task.ID) {
				t.Errorf("note %q names no action the operator can take", n[0].Body)
			}
			if n[0].ID != out.Updates[len(out.Updates)-1].ID {
				t.Error("the note is not in the answer's transaction")
			}
		})
	}
}

func TestPaneInputWithNoOpenQuestionIsAMessage(t *testing.T) {
	s := service(t)
	task, worker, question, priv := paneSession(t, s)
	call(t, s, op, Request{Op: "resolve", ReplyTo: question.ID, Body: "Approved"})
	out := call(t, s, worker, vouched(priv, worker.Attempt, "Also bump the version", time.Time{}))
	if len(out.Updates) != 1 || out.Updates[0].Kind != KindPaneMessage || out.Updates[0].ReplyTo != 0 || out.Updates[0].Item != task.ID {
		t.Fatalf("updates = %+v, want one pane message resolving nothing", out.Updates)
	}
	if st := mustSnapshot(t, s); st.Items[task.ID].State != "running" || len(notes(st, task.ID)) != 0 {
		t.Errorf("task %s, notes %+v", st.Items[task.ID].State, notes(st, task.ID))
	}
}

func TestAPaneAnswerResolvesAQuestionTheTaskAskedOnItsPlan(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	worker, _ := dispatch(t, s, task)
	question := call(t, s, worker, Request{Op: "ask", ID: plan.ID, Body: "The unit lane is red on main"}).Updates[0]
	out := call(t, s, worker, Request{Op: "pane-input", Body: "Fixed; merge main in"})
	if out.Updates[0].ReplyTo != question.ID || out.Updates[0].Item != plan.ID {
		t.Fatalf("answer = %+v, want it on the plan, resolving %d", out.Updates[0], question.ID)
	}
	if st := mustSnapshot(t, s); st.Items[task.ID].State != "running" || PlanQuestion(st.LegacyTx(), task.ID) != 0 {
		t.Errorf("task %s, plan question %d", st.Items[task.ID].State, PlanQuestion(st.LegacyTx(), task.ID))
	}
}

func TestOnlyASessionPostsItsOwnPaneInput(t *testing.T) {
	s := service(t)
	task, worker, _, _ := paneSession(t, s)
	repo, plan := repoPlan2(t, s)
	other := newTask(t, s, repo, plan)
	reject(t, s, op, Request{Op: "pane-input", ID: task.ID, Body: "Approved"}, 403)
	reject(t, s, Actor{Name: "coordinator", Role: "coordinator"}, Request{Op: "pane-input", ID: task.ID, Body: "Approved"}, 403)
	reject(t, s, worker, Request{Op: "pane-input", ID: other.ID, Body: "Approved"}, 403)
	reject(t, s, worker, Request{Op: "pane-input", Body: "  \n"}, 400)
}

// repoPlan2 is a second repository and plan beside repoPlan's.
func repoPlan2(t *testing.T, s Service) (string, string) {
	t.Helper()
	repo := *call(t, s, op, Request{Op: "create", Item: Item{Kind: "repo", Title: "Other", Slug: "owner/other", Remote: "git@example.test:owner/other.git"}}).Item
	plan := *call(t, s, op, Request{Op: "create", Item: Item{Kind: "plan", Repo: repo.ID, Title: "Other", Body: "Other"}}).Item
	return repo.ID, plan.ID
}

func TestStartedRecordsTheRunsPaneKey(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	_, claimed := dispatch(t, s, task)
	if err := s.Started(claimed.Attempt.ID, "@9", "abcd"); err != nil {
		t.Fatal(err)
	}
	at := mustSnapshot(t, s).Attempts[claimed.Attempt.ID]
	if at.State != "running" || at.Window != "@9" || at.PaneKey != "abcd" {
		t.Errorf("attempt = %+v", at)
	}
	if err := s.SetAttempt(at.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	if got := mustSnapshot(t, s).Attempts[at.ID].PaneKey; got != "abcd" {
		t.Errorf("a later observation dropped the pane key: %q", got)
	}
}

func TestAPaneAnswerAnswersOnlyAQuestionAskedBeforeItWasTyped(t *testing.T) {
	s := service(t)
	task, worker, question, priv := paneSession(t, s) // asked at the service's clock, 09:00
	// Typed before the question, so not its answer.
	out := call(t, s, worker, vouched(priv, worker.Attempt, "Approved", s.now().Add(-time.Minute)))
	if out.Updates[0].Kind != KindPaneMessage {
		t.Fatalf("a text typed before the question resolved it: %+v", out.Updates[0])
	}
	// Within the host's clock skew.
	if out = call(t, s, worker, vouched(priv, worker.Attempt, "Yes", s.now().Add(-clockSkew))); out.Updates[0].ReplyTo != question.ID {
		t.Errorf("answer %+v, want it to resolve %d despite the skew", out.Updates[0], question.ID)
	}
	if st := mustSnapshot(t, s); st.Items[task.ID].State != "running" {
		t.Errorf("task %s", st.Items[task.ID].State)
	}
}

func TestAPaneAnswerPrefersTheSessionsOwnQuestion(t *testing.T) {
	s := service(t)
	_, plan := repoPlan(t, s)
	approve(t, s, plan)
	planner, _ := dispatch(t, s, plan)
	theirs := call(t, s, op, Request{Op: "ask", ID: plan.ID, Body: "Should the plan change scope?"}).Updates[0]
	own := call(t, s, planner, Request{Op: "ask", ID: plan.ID, Body: "Split the task?"}).Updates[0]
	out := call(t, s, planner, Request{Op: "pane-input", Body: "Yes, split it"})
	if out.Updates[0].ReplyTo != own.ID {
		t.Errorf("the planner's pane answer resolved %d, want its own question %d over the older %d", out.Updates[0].ReplyTo, own.ID, theirs.ID)
	}
	if out = call(t, s, planner, Request{Op: "pane-input", Body: "No, keep the scope"}); out.Updates[0].ReplyTo != theirs.ID {
		t.Errorf("with its own answered, the pane answer resolved %d, want %d", out.Updates[0].ReplyTo, theirs.ID)
	}
}

func TestAHarnessCommandAnswersNothing(t *testing.T) {
	s := service(t)
	task, worker, _, _ := paneSession(t, s)
	out := call(t, s, worker, Request{Op: "pane-input", Body: " /compact"})
	if out.Updates[0].Kind != KindPaneMessage {
		t.Errorf("a slash command answered the question: %+v", out.Updates[0])
	}
	if st := mustSnapshot(t, s); st.Items[task.ID].State != "needs-human" {
		t.Errorf("task %s, want it still held", st.Items[task.ID].State)
	}
	if out = call(t, s, worker, Request{Op: "pane-input", Body: "/tmp/x is fine"}); out.Updates[0].Kind != KindPaneAnswer {
		t.Errorf("an answer starting with a path answered nothing: %+v", out.Updates[0])
	}
}

func TestTheNoteOnAPlannersPaneInputNamesThePlanStop(t *testing.T) {
	s := service(t)
	_, plan := repoPlan(t, s)
	approve(t, s, plan)
	planner, _ := dispatch(t, s, plan)
	call(t, s, planner, Request{Op: "pane-input", Body: "Approved"})
	n := notes(mustSnapshot(t, s), plan.ID)
	if len(n) != 1 || !strings.Contains(n[0].Body, "engine plan stop "+plan.ID) || strings.Contains(n[0].Body, "task stop") {
		t.Errorf("notes %+v, want one naming the plan's stop", n)
	}
}

func TestAPlannersPaneInputNeverAnswersATasksPlanQuestion(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	asker, _ := dispatch(t, s, task)
	theirs := call(t, s, asker, Request{Op: "ask", ID: plan.ID, Body: "Is main red?"}).Updates[0]
	planner := Actor{Name: "a99", Role: "worker", Attempt: "a99"}
	if err := s.Store.Transaction(func(st *State) error {
		st.Attempts[planner.Attempt] = Attempt{ID: planner.Attempt, Item: plan.ID, State: "running"}
		p := st.Items[plan.ID]
		p.Attempt = planner.Attempt
		st.Items[plan.ID] = p
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	out := call(t, s, planner, Request{Op: "pane-input", Body: "Unrelated note"})
	if out.Updates[0].Kind != KindPaneMessage {
		t.Errorf("the planner's pane input %+v answered the task's question %d", out.Updates[0], theirs.ID)
	}
	if st := mustSnapshot(t, s); st.Items[task.ID].State != "needs-human" {
		t.Errorf("task %s, want it still held by its own question", st.Items[task.ID].State)
	}
}
