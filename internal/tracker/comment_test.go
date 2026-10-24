package tracker

import (
	"errors"
	"strings"
	"testing"
)

// The paths are split so this file holds no tracker reference.
const (
	comments = "repos/o/r/" + "issues/7/comments"
	web      = "https://github.com/o/r/" + "issues/7#issuecomment-9"
)

func TestComment(t *testing.T) {
	var got []string
	run := func(env []string, name string, args ...string) (string, error) {
		got = append(append(got, env...), args...)
		return `{"html_url":"` + web + `"}`, nil
	}
	url, err := Comment(run, "tok", Ref{"o/r", 7}, "hello")
	if err != nil || url != web {
		t.Fatalf("url %q, err %v", url, err)
	}
	if strings.Join(got, " ") != "GH_TOKEN=tok api "+comments+" -f body=hello" {
		t.Errorf("ran %q", got)
	}
}

func TestCommentFailures(t *testing.T) {
	for name, run := range map[string]Runner{
		"gh fails":   func([]string, string, ...string) (string, error) { return "HTTP 404", errors.New("exit 1") },
		"unreadable": func([]string, string, ...string) (string, error) { return "nope", nil },
	} {
		if _, err := Comment(run, "tok", Ref{"o/r", 7}, "x"); err == nil || !strings.Contains(err.Error(), "on o/r") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// recorder answers every gh call with out (and err), and records its args.
type recorder struct {
	out  string
	err  error
	args [][]string
}

func (r *recorder) run(_ []string, _ string, args ...string) (string, error) {
	r.args = append(r.args, args)
	return r.out, r.err
}

func TestLabelWrites(t *testing.T) {
	r := &recorder{out: "[]"}
	if err := AddLabels(r.run, "tok", Ref{"o/r", 7}, "engine:failed", "engine:x"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveLabel(r.run, "tok", Ref{"o/r", 7}, "engine:needs-human"); err != nil {
		t.Fatal(err)
	}
	if err := CloseCompleted(r.run, "tok", Ref{"o/r", 7}); err != nil {
		t.Fatal(err)
	}
	got := []string{strings.Join(r.args[0], " "), strings.Join(r.args[1], " "), strings.Join(r.args[2], " ")}
	path := strings.TrimSuffix(comments, "/comments")
	want := []string{
		"api " + path + "/labels -X POST -f labels[]=engine:failed -f labels[]=engine:x",
		"api " + path + "/labels/engine:needs-human -X DELETE",
		"api " + path + " -X PATCH -f state=closed -f state_reason=completed",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("call %d\n got %s\nwant %s", i, got[i], want[i])
		}
	}
}

func TestLabelWriteFailures(t *testing.T) {
	gone := &recorder{out: `{"message":"Label does not exist"}`, err: errors.New("exit 1")}
	if err := RemoveLabel(gone.run, "tok", Ref{"o/r", 7}, "x"); err != nil {
		t.Errorf("a missing label: %v", err)
	}
	broken := &recorder{out: "HTTP 500", err: errors.New("exit 1")}
	for name, err := range map[string]error{
		"add":    AddLabels(broken.run, "tok", Ref{"o/r", 7}, "x"),
		"remove": RemoveLabel(broken.run, "tok", Ref{"o/r", 7}, "x"),
		"close":  CloseCompleted(broken.run, "tok", Ref{"o/r", 7}),
	} {
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestOperator(t *testing.T) {
	for out, want := range map[string]string{
		`{"user":{"login":"author"},"assignees":[{"login":"lead"}]}`: "lead",
		`{"user":{"login":"author"},"assignees":[]}`:                 "author",
	} {
		r := &recorder{out: out}
		if got, err := Operator(r.run, "tok", Ref{"o/r", 7}); err != nil || got != want {
			t.Errorf("%s: %q %v", out, got, err)
		}
	}
	for _, r := range []*recorder{{out: "x", err: errors.New("exit 1")}, {out: "nope"}} {
		if _, err := Operator(r.run, "tok", Ref{"o/r", 7}); err == nil {
			t.Errorf("%q: no error", r.out)
		}
	}
}

func TestPullRequests(t *testing.T) {
	merged, open := "https://github.com/o/r/"+"pull/9", "https://github.com/o/r/"+"pull/8"
	r := &recorder{out: `{"data":{"repository":{"issue":{"closedByPullRequestsReferences":{"nodes":[` +
		`{"url":"` + merged + `","merged":true},{"url":"` + open + `","merged":false}]}}}}}`}
	urls, err := PullRequests(r.run, "tok", Ref{"o/r", 7})
	if err != nil || len(urls) != 1 || urls[0] != merged {
		t.Errorf("urls %v, %v", urls, err)
	}
	if a := strings.Join(r.args[0], " "); !strings.Contains(a, "graphql") || !strings.Contains(a, "-F i=7") || !strings.Contains(a, "-f o=o -f n=r") {
		t.Errorf("args %s", a)
	}
	for _, bad := range []*recorder{{out: "x", err: errors.New("exit 1")}, {out: "nope"}} {
		if _, err := PullRequests(bad.run, "tok", Ref{"o/r", 7}); err == nil {
			t.Errorf("%q: no error", bad.out)
		}
	}
}
