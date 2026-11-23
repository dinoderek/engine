package ui

import (
	"context"
	"net/url"
	"slices"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

// inboxPage is how many updates one inbox read returns; a full page means
// more may follow.
const inboxPage = 200

// maxInboxReads bounds how many pages one inbox request reads.
const maxInboxReads = 100

// Card kinds: a question needs an answer, a message only an acknowledgement.
const (
	cardQuestion = "question"
	cardMessage  = "message"
)

// InboxCard is one thing waiting for the operator: an open question or an
// unacknowledged message, with what the phone needs to act on it. Its body
// is plain text.
type InboxCard struct {
	Update      int                `json:"update"`
	Kind        string             `json:"kind"`
	Item        string             `json:"item"`
	Title       string             `json:"title"`
	Repo        string             `json:"repo,omitempty"`
	RepoSlug    string             `json:"repo_slug,omitempty"`
	State       string             `json:"state,omitempty"`
	Task        string             `json:"task,omitempty"` // the task whose session asked a plan question
	Host        string             `json:"host,omitempty"` // the host a fleet question, which has no item, is about
	Author      string             `json:"author"`
	At          time.Time          `json:"at"`
	Class       string             `json:"class,omitempty"`
	Body        string             `json:"body"`
	Choices     []string           `json:"choices,omitempty"`
	Recommended *int               `json:"recommended,omitempty"`
	PR          string             `json:"pr,omitempty"`
	Latest      *control.UpdateRef `json:"latest,omitempty"`
}

// InboxList is the Inbox tab: open questions, oldest first, then
// unacknowledged messages, oldest first, across repositories.
type InboxList struct {
	Cards     []InboxCard `json:"cards"`
	Questions int         `json:"questions"`
	Messages  int         `json:"messages"`
}

// inboxKinds are the updates a person reads as a message in the inbox.
// Acknowledgements and resolutions are records of an answer, not something
// to answer.
var inboxKinds = []string{"say", "ask", "reply", "ruling"}

// inboxList joins the operator's open updates with their items' titles, the
// repositories' names and the sessions' latest lines.
func inboxList(updates []control.Update, items, repos []control.Item, rows []control.SessionRow) InboxList {
	byID := map[string]control.Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	slugs := map[string]string{}
	for _, r := range repos {
		slugs[r.ID] = r.Slug
	}
	latest := map[string]*control.UpdateRef{}
	for _, r := range rows {
		latest[r.Item] = r.Latest
	}
	out := InboxList{Cards: []InboxCard{}}
	for _, u := range updates {
		if !slices.Contains(inboxKinds, u.Kind) {
			continue
		}
		it := byID[u.Item]
		c := InboxCard{Update: u.ID, Kind: cardMessage, Item: u.Item, Title: it.Title, Repo: it.Repo, RepoSlug: slugs[it.Repo],
			State: it.State, Task: u.Task, Author: u.Author, At: u.At, Body: u.Body}
		if u.RequiresReply {
			c.Kind, c.Class, c.Choices, c.Recommended = cardQuestion, u.Class, u.Choices, u.Recommended
		}
		if u.Host != "" {
			c.Host, c.Title = u.Host, "Host "+u.Host
		}
		// A plan question a task's session asked belongs to that session.
		session := u.Item
		if u.Task != "" {
			session = u.Task
		}
		c.PR = prLink(byID[session].PR)
		c.Latest = latest[session]
		out.Cards = append(out.Cards, c)
		if c.Kind == cardQuestion {
			out.Questions++
		} else {
			out.Messages++
		}
	}
	// Questions lead; each group keeps the server's order, oldest first.
	slices.SortStableFunc(out.Cards, func(a, b InboxCard) int {
		switch {
		case a.Kind == cardQuestion && b.Kind != cardQuestion:
			return -1
		case a.Kind != cardQuestion && b.Kind == cardQuestion:
			return 1
		}
		return a.Update - b.Update
	})
	return out
}

// prLink is a pull request URL the page may link: absolute http or https.
func prLink(s string) string {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return u.String()
}

// inbox loads the Inbox tab. Like every view it falls back to its last
// success, marked stale, when the control server fails.
func (u *upstream) inbox(ctx context.Context) (Envelope, int) {
	return u.view(ctx, "inbox", func(ctx context.Context) (any, error) {
		var updates []control.Update
		for cursor, reads := 0, 0; ; reads++ {
			if reads == maxInboxReads {
				return nil, &control.Problem{Status: 502, Message: "inbox too long to show"}
			}
			page, err := u.call(ctx, control.Request{Op: "inbox", After: cursor})
			if err != nil {
				return nil, err
			}
			updates = append(updates, page.Updates...)
			if len(page.Updates) < inboxPage {
				break
			}
			cursor = page.Updates[len(page.Updates)-1].ID
		}
		if len(updates) == 0 {
			return inboxList(nil, nil, nil, nil), nil
		}
		var lists [3][]control.Item
		for i, kind := range []string{"task", "plan", "repo"} {
			got, err := u.call(ctx, control.Request{Op: "list", State: kind})
			if err != nil {
				return nil, err
			}
			lists[i] = got.Items
		}
		sessions, err := u.call(ctx, control.Request{Op: "session-view"})
		if err != nil {
			return nil, err
		}
		return inboxList(updates, append(lists[0], lists[1]...), lists[2], sessions.Sessions), nil
	})
}
