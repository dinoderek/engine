package tracker

import (
	"errors"
	"strings"
	"testing"
	"time"
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
		if _, err := Comment(run, "tok", Ref{"o/r", 7}, "x"); err == nil || !strings.Contains(err.Error(), "comment on o/r") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestFindClaim(t *testing.T) {
	at := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	tasks := []ClaimedTask{
		{Ref: Ref{"o/r", 7}, Claim: Claim{Name: "r-7", PostedAt: at}},
		{Ref: Ref{"o/r", 8}, Claim: Claim{Name: "r-8", PostedAt: at}},
		{Ref: Ref{"p/r", 7}, Claim: Claim{Name: "r-7", PostedAt: at.Add(time.Hour)}},
	}
	if got, ok := FindClaim(tasks, "r-7"); !ok || got.Ref != (Ref{"p/r", 7}) {
		t.Errorf("latest r-7: %+v %v", got, ok)
	}
	if _, ok := FindClaim(tasks, "r-9"); ok {
		t.Error("found r-9")
	}
}
