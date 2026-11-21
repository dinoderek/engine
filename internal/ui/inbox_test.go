package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

func inboxUpdate(id int, kind, item, body string, extra func(*control.Update)) control.Update {
	u := control.Update{ID: id, Item: item, Kind: kind, Author: "t8", At: time.Date(2026, 11, 7, 9, 0, id, 0, time.UTC), Body: body, Recipient: "operator"}
	if extra != nil {
		extra(&u)
	}
	return u
}

func questioning(u *control.Update) { u.RequiresReply = true }

func inboxItems() (items, repos []control.Item) {
	items = []control.Item{
		{ID: "t8", Kind: "task", Repo: "r1", Plan: "p3", Title: "API", State: "in-review", PR: "https://example.test/o/r/change/8"},
		{ID: "t9", Kind: "task", Repo: "r2", Plan: "p4", Title: "Docs", State: "running", PR: "javascript:alert(1)"},
		{ID: "p3", Kind: "plan", Repo: "r1", Title: "Rollout", State: "executing"},
	}
	repos = []control.Item{{ID: "r1", Kind: "repo", Slug: "o/alpha"}, {ID: "r2", Kind: "repo", Slug: "o/beta"}}
	return items, repos
}

func TestInboxListOrdersJoinsAndFilters(t *testing.T) {
	items, repos := inboxItems()
	rec := 1
	updates := []control.Update{
		inboxUpdate(3, "say", "t9", "Build is green", nil),
		inboxUpdate(5, "ask", "t8", "Merge it?", func(u *control.Update) {
			questioning(u)
			u.Class, u.Choices, u.Recommended = "review", []string{"No", "Yes"}, &rec
		}),
		inboxUpdate(7, "acknowledged", "t8", "", nil),
		inboxUpdate(9, "ask", "p3", "Plan question", func(u *control.Update) { questioning(u); u.Task = "t8" }),
		inboxUpdate(11, "say", "t404", "Orphan", nil),
		inboxUpdate(12, "merge-request", "t8", "not a message", nil),
	}
	rows := []control.SessionRow{
		{Item: "t8", Latest: &control.UpdateRef{Item: "t8", Update: 5, Author: "t8", Summary: "Merge it?"}},
		{Item: "t9", Latest: &control.UpdateRef{Item: "t9", Update: 3, Summary: "Build is green"}},
		{Item: "p3"},
	}
	got := inboxList(updates, items, repos, rows)
	var order []string
	for _, c := range got.Cards {
		order = append(order, fmt.Sprintf("%s%d", c.Kind[:1], c.Update))
	}
	if strings.Join(order, " ") != "q5 q9 m3 m11" || got.Questions != 2 || got.Messages != 2 {
		t.Fatalf("cards %v, counts %d/%d; want questions first, each oldest first, and only messages", order, got.Questions, got.Messages)
	}
	q := got.Cards[0]
	if q.Title != "API" || q.RepoSlug != "o/alpha" || q.Class != "review" || q.Body != "Merge it?" || len(q.Choices) != 2 || q.Recommended == nil || *q.Recommended != 1 ||
		q.PR != "https://example.test/o/r/change/8" || q.Latest == nil || q.Latest.Update != 5 || q.State != "in-review" {
		t.Errorf("question card = %+v", q)
	}
	if p := got.Cards[1]; p.Title != "Rollout" || p.Task != "t8" || p.PR != "https://example.test/o/r/change/8" || p.Latest == nil || p.Latest.Item != "t8" {
		t.Errorf("a plan question belongs to its asking session: %+v", p)
	}
	if m := got.Cards[2]; m.Kind != "message" || m.PR != "" || m.Class != "" || m.Latest == nil || m.RepoSlug != "o/beta" {
		t.Errorf("a message card keeps an unsafe PR link out and carries no class: %+v", m)
	}
	if o := got.Cards[3]; o.Title != "" || o.Item != "t404" {
		t.Errorf("an unknown item still shows: %+v", o)
	}
	if empty := inboxList(nil, nil, nil, nil); empty.Cards == nil || len(empty.Cards) != 0 {
		t.Errorf("an empty inbox must encode as [], got %+v", empty)
	}
}

// inboxFake serves the inbox in pages, with the lists and sessions it joins.
func inboxFake(pages [][]control.Update) *recorder {
	items, repos := inboxItems()
	reads := 0
	return &recorder{reply: func(r control.Request) (control.Response, error) {
		switch {
		case r.Op == "inbox":
			if reads >= len(pages) {
				return control.Response{}, nil
			}
			reads++
			return control.Response{Updates: pages[reads-1]}, nil
		case r.Op == "list" && r.State == "repo":
			return control.Response{Items: repos}, nil
		case r.Op == "list":
			return control.Response{Items: items}, nil
		case r.Op == "session-view":
			return control.Response{Sessions: []control.SessionRow{{Item: "t8", Latest: &control.UpdateRef{Item: "t8", Update: 99, Summary: "Pushing"}}}}, nil
		}
		return control.Response{}, errors.New("unexpected " + r.Op)
	}}
}

func TestInboxRoutePagesThroughEveryUpdate(t *testing.T) {
	first := make([]control.Update, inboxPage)
	for i := range first {
		first[i] = inboxUpdate(i+1, "say", "t8", "m", nil)
	}
	c := inboxFake([][]control.Update{first, {inboxUpdate(inboxPage+1, "ask", "t8", "Last", questioning)}})
	_, body := newDashboard(c).get(t, "GET", "127.0.0.1:7332", "/api/inbox")
	var env struct {
		Data InboxList `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil || len(env.Data.Cards) != inboxPage+1 || env.Data.Questions != 1 {
		t.Fatalf("inbox = %d cards, %v; want %d across two pages", len(env.Data.Cards), err, inboxPage+1)
	}
	var after []int
	for _, r := range c.sent() {
		if r.Op == "inbox" {
			after = append(after, r.After)
		}
	}
	if fmt.Sprint(after) != fmt.Sprint([]int{0, inboxPage}) {
		t.Fatalf("pages were read after %v, want 0 then the last update of the page", after)
	}
}

func TestInboxRouteReadsNothingElseWhenEmpty(t *testing.T) {
	c := inboxFake(nil)
	_, body := newDashboard(c).get(t, "GET", "127.0.0.1:7332", "/api/inbox")
	if !strings.Contains(body, `"cards":[]`) || len(c.sent()) != 1 {
		t.Fatalf("empty inbox = %s after %d calls, want [] from one read", body, len(c.sent()))
	}
}

func TestInboxRouteKeepsTheLastInboxDuringAnOutage(t *testing.T) {
	c := inboxFake([][]control.Update{{inboxUpdate(5, "ask", "t8", "Merge it?", questioning)}})
	d := newDashboard(c)
	if _, body := d.get(t, "GET", "127.0.0.1:7332", "/api/inbox"); decode(t, body).Stale || !strings.Contains(body, "Merge it?") {
		t.Fatalf("fresh inbox = %s", body)
	}
	c.reply = func(control.Request) (control.Response, error) {
		return control.Response{}, &control.Problem{Status: 500, Message: "db down"}
	}
	res, body := d.get(t, "GET", "127.0.0.1:7332", "/api/inbox")
	if env := decode(t, body); res.StatusCode != 200 || !env.Stale || !strings.Contains(env.Error, "db down") || !strings.Contains(body, "Merge it?") {
		t.Fatalf("inbox during an outage = %d %s, want the last inbox marked stale", res.StatusCode, body)
	}
}

func TestInboxRouteWithoutDataReportsTheFailure(t *testing.T) {
	c := &recorder{reply: func(control.Request) (control.Response, error) { return control.Response{}, context.DeadlineExceeded }}
	if res, body := newDashboard(c).get(t, "GET", "127.0.0.1:7332", "/api/inbox"); res.StatusCode != 502 || decode(t, body).Error == "" {
		t.Fatalf("first load during an outage = %d %s, want 502 with a message", res.StatusCode, body)
	}
}

func TestInboxIsReadOnlyAndPageAssetsAreServed(t *testing.T) {
	d := newDashboard(inboxFake(nil))
	for target, kind := range map[string]string{
		"/inbox.js": "text/javascript", "/manifest.webmanifest": "application/manifest+json", "/icon-192.png": "image/png", "/icon-512.png": "image/png",
	} {
		res, body := d.get(t, "GET", "127.0.0.1:7332", target)
		if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), kind) || body == "" {
			t.Errorf("GET %s = %d %s", target, res.StatusCode, res.Header.Get("Content-Type"))
		}
	}
	_, body := d.get(t, "GET", "127.0.0.1:7332", "/manifest.webmanifest")
	var m struct {
		Name, StartURL, Display string
		Icons                   []struct{ Src, Sizes string }
	}
	if err := json.Unmarshal([]byte(strings.ReplaceAll(body, "start_url", "startURL")), &m); err != nil || m.Name == "" || m.Display != "standalone" || len(m.Icons) < 2 {
		t.Errorf("manifest = %s (%v)", body, err)
	}
	if !strings.Contains(policy, "default-src 'self'") {
		t.Errorf("the CSP must keep default-src 'self': %s", policy)
	}
}
