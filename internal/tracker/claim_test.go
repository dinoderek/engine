package tracker

import (
	"strings"
	"testing"
	"time"
)

const fullClaim = `Claim: a-21
Run: a-21-20260101T000000Z
Host: mac
Harness: claude-code 2.1.302 (claude-sonnet-5-5)
Mode: hands-off
Started by: hand
Tmux: none
Remote Control: none
Branch: feat/a-21-x`

func TestParseClaim(t *testing.T) {
	c, ok := ParseClaim(fullClaim)
	if !ok {
		t.Fatal("not a claim")
	}
	want := Claim{
		Name: "a-21", Run: "a-21-20260101T000000Z", Host: "mac",
		Harness: "claude-code 2.1.302 (claude-sonnet-5-5)", Mode: "hands-off",
		StartedBy: "hand", Tmux: "", RemoteControl: "", Branch: "feat/a-21-x",
	}
	if c != want {
		t.Errorf("got  %+v\nwant %+v", c, want)
	}
	if c.HarnessName() != "claude-code" {
		t.Errorf("harness name %q", c.HarnessName())
	}
}

func TestParseClaimValues(t *testing.T) {
	cases := []struct {
		name, body string
		check      func(Claim) bool
	}{
		{"tmux target keeps its colon", "Claim: a-1\nTmux: engine:a-1\nRemote Control: a-1", func(c Claim) bool {
			return c.Tmux == "engine:a-1" && c.RemoteControl == "a-1"
		}},
		{"none in any case", "Claim: a-1\nHost: None\nBranch: NONE", func(c Claim) bool { return c.Host == "" && c.Branch == "" }},
		{"a line is missing", "Claim: a-1\nRun: a-1-x", func(c Claim) bool {
			return c.Run == "a-1-x" && c.Host == "" && c.Harness == "" && c.HarnessName() == ""
		}},
		{"an extra line is ignored", "Claim: a-1\nColour: red\nHost: mac\nfree text: here\nno colon", func(c Claim) bool { return c.Host == "mac" }},
		{"crlf and padding", "\n  Claim: a-1  \r\nHost:   mac  \r\n", func(c Claim) bool { return c.Name == "a-1" && c.Host == "mac" }},
		{"first value wins", "Claim: a-1\nHost: mac\nHost: devbox", func(c Claim) bool { return c.Host == "mac" }},
		{"keys are exact", "Claim: a-1\nhost: mac\nStarted By: hand", func(c Claim) bool { return c.Host == "" && c.StartedBy == "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, ok := ParseClaim(tc.body)
			if !ok || !tc.check(c) {
				t.Errorf("got %+v, %v", c, ok)
			}
		})
	}
}

func TestParseClaimRejects(t *testing.T) {
	for _, body := range []string{"", "Ruling (risk: med): x", "Run: a\nClaim: a-1", "Claim:", "Claim: none", "claim: a-1", "a claim: x\nClaim: a-1"} {
		if c, ok := ParseClaim(body); ok {
			t.Errorf("%q parsed as %+v", body, c)
		}
	}
}

func commentsPath(n int) string { return issuePath(n, "comments") }

// cm is one comment: who wrote it, and what.
type cm struct{ by, body string }

// thread renders one page of comments as the API does, oldest first. Comment
// i is posted i+start minutes past the hour.
func thread(start int, cms ...cm) string {
	var items []string
	for i, c := range cms {
		n := start + i
		items = append(items, `{"body":`+quote(c.body)+`,"html_url":"https://github.com/o/r/c`+string(rune('0'+n))+
			`","created_at":"2026-10-21T20:0`+string(rune('0'+n))+`:00Z","user":{"login":"`+c.by+`"}}`)
	}
	return "[" + strings.Join(items, ",") + "]"
}

func quote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + r.Replace(s) + `"`
}

func TestLatestClaimTakesTheNewest(t *testing.T) {
	older := strings.Replace(fullClaim, "Host: mac", "Host: devbox", 1)
	page1 := thread(0, cm{machine, "hello"}, cm{machine, older}, cm{machine, "Ruling (risk: med): x"})
	page2 := thread(3, cm{machine, fullClaim}, cm{machine, "done"})
	gh := &fakeGH{replies: map[string]string{commentsPath(21): page1 + "\n" + page2}}
	c, err := LatestClaim(gh.run, "tok", Ref{"o/r", 21})
	if err != nil || c == nil {
		t.Fatalf("got %v, %v", c, err)
	}
	if c.Host != "mac" || c.Author != machine || !strings.HasSuffix(c.URL, "/c3") ||
		!c.PostedAt.Equal(time.Date(2026, 10, 21, 20, 3, 0, 0, time.UTC)) {
		t.Errorf("got %+v", c)
	}
	if !strings.Contains(gh.calls[0], "--paginate") || !strings.Contains(gh.calls[0], "per_page=100") {
		t.Errorf("call %q", gh.calls[0])
	}
}

func TestLatestClaimIgnoresStrangers(t *testing.T) {
	stranger := strings.Replace(fullClaim, "Host: mac", "Host: evil", 1)
	gh := &fakeGH{replies: map[string]string{commentsPath(21): thread(0,
		cm{machine, fullClaim}, cm{"stranger", stranger}, cm{machine + "-2", stranger}, cm{"", stranger})}}
	c, err := LatestClaim(gh.run, "tok", Ref{"o/r", 21})
	if err != nil || c == nil || c.Host != "mac" || c.Author != machine {
		t.Errorf("a later stranger replaced the claim: %+v, %v", c, err)
	}
	gh = &fakeGH{replies: map[string]string{commentsPath(21): thread(0, cm{"stranger", stranger})}}
	if c, err := LatestClaim(gh.run, "tok", Ref{"o/r", 21}); c != nil || err != nil {
		t.Errorf("a stranger's claim counted: %+v, %v", c, err)
	}
}

func TestLatestClaimAcceptsTheOperator(t *testing.T) {
	byHand := strings.Replace(fullClaim, "Host: mac", "Host: laptop", 1)
	gh := &fakeGH{replies: map[string]string{commentsPath(21): thread(0, cm{machine, fullClaim}, cm{operator, byHand})}}
	c, err := LatestClaim(gh.run, "tok", Ref{"o/r", 21})
	if err != nil || c == nil || c.Host != "laptop" || c.Author != operator {
		t.Errorf("got %+v, %v", c, err)
	}
}

func TestLatestClaimNone(t *testing.T) {
	gh := &fakeGH{replies: map[string]string{commentsPath(21): thread(0, cm{machine, "hello"}) + "[]"}}
	if c, err := LatestClaim(gh.run, "tok", Ref{"o/r", 21}); c != nil || err != nil {
		t.Errorf("got %v, %v", c, err)
	}
}

func TestLatestClaimFailures(t *testing.T) {
	gh := &fakeGH{replies: map[string]string{commentsPath(21): "<html>"}}
	if _, err := LatestClaim(gh.run, "tok", Ref{"o/r", 21}); err == nil || !strings.Contains(err.Error(), "comments on "+short("o/r", 21)+": unreadable") {
		t.Errorf("bad JSON: %v", err)
	}
	gh = &fakeGH{}
	if _, err := LatestClaim(gh.run, "tok", Ref{"o/r", 21}); err == nil || !strings.Contains(err.Error(), "comments on "+short("o/r", 21)) {
		t.Errorf("gh failure: %v", err)
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
