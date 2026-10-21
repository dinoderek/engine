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

func comments(bodies ...string) string {
	var items []string
	for i, b := range bodies {
		item := `{"body":` + quote(b) + `,"html_url":"https://github.com/o/r/c` + string(rune('1'+i)) +
			`","created_at":"2026-10-21T20:0` + string(rune('0'+i)) + `:00Z","user":{"login":"bot"}}`
		items = append(items, item)
	}
	return "[" + strings.Join(items, ",") + "]"
}

func quote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + r.Replace(s) + `"`
}

func TestLatestClaimTakesTheNewest(t *testing.T) {
	older := strings.Replace(fullClaim, "Host: mac", "Host: devbox", 1)
	page1 := comments("hello", older, "Ruling (risk: med): x")
	page2 := comments(fullClaim, "done")
	gh := &fakeGH{replies: map[string]string{commentsPath(21): page1 + "\n" + page2}}
	c, err := LatestClaim(gh.run, "tok", Ref{"o/r", 21})
	if err != nil || c == nil {
		t.Fatalf("got %v, %v", c, err)
	}
	if c.Host != "mac" || c.Author != "bot" || !strings.HasSuffix(c.URL, "/c1") ||
		!c.PostedAt.Equal(time.Date(2026, 10, 21, 20, 0, 0, 0, time.UTC)) {
		t.Errorf("got %+v", c)
	}
	if !strings.Contains(gh.calls[0], "--paginate") || !strings.Contains(gh.calls[0], "per_page=100") {
		t.Errorf("call %q", gh.calls[0])
	}
}

func TestLatestClaimNone(t *testing.T) {
	gh := &fakeGH{replies: map[string]string{commentsPath(21): comments("hello") + "[]"}}
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
