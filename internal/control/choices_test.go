package control

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

func index(n int) *int { return &n }

func TestQuestionFields(t *testing.T) {
	long := strings.Repeat("é", ChoiceRunes)
	for _, tc := range []struct {
		name string
		r    Request
		want string // a fragment of the refusal; empty for none
	}{
		{"an ask with all three", Request{Op: "ask", Choices: []string{"Yes, a, b", "No"}, Recommended: index(1), Class: "outward or costly"}, ""},
		{"an ask with none", Request{Op: "ask"}, ""},
		{"a class alone", Request{Op: "ask", Class: "repo rule"}, ""},
		{"choices without a recommendation", Request{Op: "ask", Choices: []string{"a"}}, ""},
		{"the most choices", Request{Op: "ask", Choices: []string{"1", "2", "3", "4", "5", "6"}}, ""},
		{"the longest choice", Request{Op: "ask", Choices: []string{long}}, ""},
		{"too many choices", Request{Op: "ask", Choices: []string{"1", "2", "3", "4", "5", "6", "7"}}, "at most 6 choices, not 7"},
		{"a choice too long", Request{Op: "ask", Choices: []string{"ok", long + "x"}}, "choice 1 has 81 characters"},
		{"a blank choice", Request{Op: "ask", Choices: []string{" "}}, "choice 0 is blank"},
		{"a choice of two lines", Request{Op: "ask", Choices: []string{"one\ntwo"}}, "must be one line"},
		{"a repeated choice", Request{Op: "ask", Choices: []string{"a", "b", "a"}}, "choice 2 repeats"},
		{"a recommendation without choices", Request{Op: "ask", Recommended: index(0)}, "needs choices"},
		{"a recommendation past the choices", Request{Op: "ask", Choices: []string{"a", "b"}, Recommended: index(2)}, "from 0 to 1"},
		{"a negative recommendation", Request{Op: "ask", Choices: []string{"a"}, Recommended: index(-1)}, "recommended -1 is not a choice"},
		{"an unknown class", Request{Op: "ask", Class: "taste"}, `class "taste" is not a hard-stop class`},
		{"a class in the reference's case", Request{Op: "ask", Class: "Product"}, "not a hard-stop class"},
		{"choices on say", Request{Op: "say", Choices: []string{"a"}}, "only ask takes"},
		{"a recommendation on reply", Request{Op: "reply", Recommended: index(0)}, "only ask takes"},
		{"a class on resolve", Request{Op: "resolve", Class: "product"}, "only ask takes"},
		{"a class on a read", Request{Op: "get", Class: "product"}, "only ask takes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := questionFields(tc.r)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			var p *Problem
			if !errors.As(err, &p) || p.Status != 400 || !strings.Contains(p.Message, tc.want) {
				t.Fatalf("err = %v, want a 400 containing %q", err, tc.want)
			}
		})
	}
}

// referenceClasses are the numbered bold names under a hard-stop
// reference's "## Classes" heading, in lower case.
func referenceClasses(text string) []string {
	_, section, _ := strings.Cut(text, "\n## Classes\n")
	section, _, _ = strings.Cut(section, "\n## ")
	var out []string
	for _, m := range regexp.MustCompile(`(?m)^\d+\. \*\*([^*]+):\*\*`).FindAllStringSubmatch(section, -1) {
		out = append(out, strings.ToLower(m[1]))
	}
	return out
}

func TestHardStopClassesMatchTheReference(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "plugins", "engine", "skills", "build", "references", "hard-stops.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got := referenceClasses(string(b)); !slices.Equal(got, HardStopClasses) {
		t.Fatalf("hard-stops.md lists %q; HardStopClasses is %q", got, HardStopClasses)
	}
}

func TestReferenceClassesSeesARenamedOrAddedClass(t *testing.T) {
	text := "# Hard stops\n\n## Classes\n\n1. **Product:** a.\n2. **Repo rule:** b\n   wrapped.\n\n## Safe defaults\n\n3. **Not:** c.\n"
	if got := referenceClasses(text); !slices.Equal(got, []string{"product", "repo rule"}) {
		t.Fatalf("classes = %q", got)
	}
	if slices.Equal(referenceClasses(strings.Replace(text, "Product", "Taste", 1)), HardStopClasses[:2]) {
		t.Fatal("a renamed class still matched")
	}
}

func TestAQuestionsChoicesRecommendationAndClassReachEveryRead(t *testing.T) {
	s := service(t)
	s.Observe = func(string) Observation {
		return Observation{State: "waiting", Seen: time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)}
	}
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	worker, _ := dispatch(t, s, task)
	choices := []string{"Drop the column, then backfill", "Keep it"}
	ask := Request{Op: "ask", Choices: choices, Recommended: index(0), Class: "data"}
	want := func(where string, got []string, rec *int, class string) {
		t.Helper()
		if !slices.Equal(got, choices) || rec == nil || *rec != 0 || class != "data" {
			t.Errorf("%s: choices %q, recommended %v, class %q", where, got, rec, class)
		}
	}
	ask.ID, ask.Body = task.ID, "Drop the old column?"
	own := call(t, s, worker, ask).Updates[0]
	ask.ID, ask.Body = plan.ID, "The unit lane is red on main"
	shared := call(t, s, op, ask).Updates[0]
	want("ask", own.Choices, own.Recommended, own.Class)
	reader := Service{Store: reopen(t, s)}
	st, err := reader.Store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	stored := st.Updates[own.ID-1]
	want("stored", stored.Choices, stored.Recommended, stored.Class)
	for _, read := range []Request{{Op: "updates", ID: task.ID}, {Op: "inbox"}} {
		out, err := reader.Execute(op, read)
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(out.Updates, func(u Update) bool { return u.ID == own.ID })
		if i < 0 {
			t.Fatalf("%s lacks the question", read.Op)
		}
		want(read.Op, out.Updates[i].Choices, out.Updates[i].Recommended, out.Updates[i].Class)
	}
	got, err := reader.Execute(op, Request{Op: "get", ID: plan.ID})
	if err != nil || got.Item.Question == nil || got.Item.Question.Update != shared.ID {
		t.Fatalf("get %s: %+v, %v", plan.ID, got.Item, err)
	}
	want("get", got.Item.Question.Choices, got.Item.Question.Recommended, got.Item.Question.Class)
	reader.Observe = s.Observe
	view, err := reader.Execute(op, Request{Op: "session-view"})
	if err != nil || len(view.Sessions) != 1 || view.Sessions[0].Feedback == nil {
		t.Fatalf("session view %+v, %v", view.Sessions, err)
	}
	f := view.Sessions[0].Feedback
	want("session-view", f.Choices, f.Recommended, f.Class)
	plain := call(t, reader, worker, Request{Op: "say", ID: task.ID, Body: "Waiting on the answer"}).Updates[0]
	if plain.Choices != nil || plain.Recommended != nil || plain.Class != "" {
		t.Errorf("a message carries question fields: %+v", plain)
	}
}

// reopen closes s's store and opens its database again, as a restarted
// server would; the test's cleanup closes the new store.
func reopen(t *testing.T, s Service) *Store {
	t.Helper()
	path := s.Store.path
	if err := s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestAskRefusesBadQuestionFieldsAndWritesNothing(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	worker, _ := dispatch(t, s, task)
	before := mustSnapshot(t, s)
	reject(t, s, worker, Request{Op: "ask", ID: task.ID, Body: "Which?", Choices: []string{"a"}, Recommended: index(1)}, 400)
	reject(t, s, worker, Request{Op: "ask", ID: task.ID, Body: "Which?", Class: "taste"}, 400)
	reject(t, s, worker, Request{Op: "say", ID: task.ID, Body: "Note", Class: "product"}, 400)
	after := mustSnapshot(t, s)
	if len(after.Updates) != len(before.Updates) || after.Items[task.ID].State != "running" {
		t.Fatalf("a refused ask wrote: %d updates, task %s", len(after.Updates)-len(before.Updates), after.Items[task.ID].State)
	}
}

func TestResolveRefusesASettledQuestion(t *testing.T) {
	s := service(t)
	task, worker, question, priv := paneSession(t, s)
	first := Request{Op: "resolve", Key: "first", ReplyTo: question.ID, Body: "Approved"}
	resolved := call(t, s, op, first)
	before := mustSnapshot(t, s)
	_, err := s.Execute(op, Request{Op: "resolve", Key: "second", ReplyTo: question.ID, Body: "Rejected"})
	refusedAs(t, err, question.ID, resolved.Updates[0].ID)
	if after := mustSnapshot(t, s); !reflect.DeepEqual(after, before) {
		t.Fatalf("the refused resolve wrote: %d updates, %d receipts", len(after.Updates)-len(before.Updates), len(after.Receipts)-len(before.Receipts))
	}
	if replay := call(t, s, op, first); !reflect.DeepEqual(replay, resolved) {
		t.Fatalf("the replay returned %+v, want the first result %+v", replay, resolved)
	}
	asked := call(t, s, worker, Request{Op: "ask", ID: task.ID, Body: "And the index?", Choices: []string{"Add it", "Leave it"}}).Updates[0]
	answer := call(t, s, worker, vouched(priv, worker.Attempt, "Add it", time.Time{})).Updates[0]
	if answer.Kind != KindPaneAnswer || answer.ReplyTo != asked.ID {
		t.Fatalf("pane input %+v did not answer %d", answer, asked.ID)
	}
	_, err = s.Execute(op, Request{Op: "resolve", Key: "third", ReplyTo: asked.ID, Body: "Leave it"})
	refusedAs(t, err, asked.ID, answer.ID)
}

func refusedAs(t *testing.T, err error, question, by int) {
	t.Helper()
	var p *Problem
	if !errors.As(err, &p) || p.Status != 409 || p.Reason != ReasonAlreadyResolved {
		t.Fatalf("err = %v, want a 409 %s", err, ReasonAlreadyResolved)
	}
	if want := fmt.Sprintf("question %d is already resolved, by update %d", question, by); p.Message != want {
		t.Fatalf("message %q, want %q", p.Message, want)
	}
}

func TestAVersion3DatabaseOpensAndReads(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	worker, _ := dispatch(t, s, task)
	question := call(t, s, worker, Request{Op: "ask", ID: task.ID, Body: "Approve the schema change?"}).Updates[0]
	if _, err := s.Store.sql("PRAGMA user_version=3;"); err != nil {
		t.Fatal(err)
	}
	reader := Service{Store: reopen(t, s)}
	out, err := reader.Execute(op, Request{Op: "inbox"})
	if err != nil || len(out.Updates) != 1 || out.Updates[0].ID != question.ID || out.Updates[0].Body != "Approve the schema change?" {
		t.Fatalf("inbox after the migration: %+v, %v", out.Updates, err)
	}
	version, err := reader.Store.sql("PRAGMA user_version;")
	if err != nil || !strings.Contains(string(version), fmt.Sprintf(`"user_version":%d`, schemaVersion)) {
		t.Fatalf("schema after opening: %s, %v", version, err)
	}
	if _, err := reader.Execute(op, Request{Op: "resolve", Key: Secret(), ReplyTo: question.ID, Body: "Approved"}); err != nil {
		t.Fatalf("resolve after the migration: %v", err)
	}
}
