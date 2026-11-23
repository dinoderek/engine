package ui

import (
	"cmp"
	"slices"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

// pageSize is how many messages one thread read returns.
const pageSize = 50

// ItemDetail is a plan or task for the side panel. DescriptionHTML is its
// body rendered by RenderMarkdown, empty when it has none.
type ItemDetail struct {
	ID              string `json:"id"`
	Kind            string `json:"kind"`
	Repo            string `json:"repo"`
	Plan            string `json:"plan,omitempty"`
	Title           string `json:"title"`
	State           string `json:"state"`
	DescriptionHTML string `json:"description_html"`
	// Held is a todo task the operator stopped.
	Held bool `json:"held,omitempty"`
	Controls
}

// Message is one human-facing update. Its body is plain text.
type Message struct {
	ID            int       `json:"id"`
	Author        string    `json:"author"`
	At            time.Time `json:"at"`
	Kind          string    `json:"kind"` // one of messageKinds
	Body          string    `json:"body"`
	Recipient     string    `json:"recipient,omitempty"`
	ReplyTo       int       `json:"reply_to,omitempty"`
	RequiresReply bool      `json:"requires_reply,omitempty"`
}

// Question is the current status of one question in the thread. It is open
// until a resolution is recorded; acknowledgements and replies leave it open.
type Question struct {
	ID           int    `json:"id"`
	State        string `json:"state"` // open or resolved
	Acknowledged bool   `json:"acknowledged,omitempty"`
	Replies      int    `json:"replies,omitempty"`
	Resolution   int    `json:"resolution,omitempty"`
}

// Thread is a window of one item's messages. Next is the cursor to pass as
// after for the following window; More says whether messages remain beyond
// it. Questions covers every question in the thread, not only this window,
// because a later update can change an earlier question.
type Thread struct {
	Item      string     `json:"item"`
	Messages  []Message  `json:"messages"`
	Questions []Question `json:"questions"`
	Next      int        `json:"next"`
	More      bool       `json:"more"`
}

// messageKinds includes ruling, a plan update kind stored records may hold.
var messageKinds = []string{"say", "ask", "reply", "acknowledged", "resolved", "ruling", control.KindPaneAnswer, control.KindPaneMessage}

// itemDetail shows a plan's status, as the plan list does, and a task's state.
// active is a task's live execution, which only its controls use.
func itemDetail(it control.Item, active bool) ItemDetail {
	return ItemDetail{ID: it.ID, Kind: it.Kind, Repo: it.Repo, Plan: it.Plan, Title: it.Title, State: cmp.Or(it.Status, it.State),
		DescriptionHTML: RenderMarkdown(it.Body), Held: it.Held, Controls: itemControls(it, active)}
}

// threadWindow projects one item's updates, oldest first. It returns the
// messages after the cursor, at most pageSize of them, extended to include
// the anchor update when it falls later in the thread.
func threadWindow(item string, updates []control.Update, after, anchor int) Thread {
	msgs := messagesOf(item, updates)
	questions := map[int]*Question{}
	var order []int
	for _, m := range msgs {
		if q, ok := questions[m.ReplyTo]; ok {
			settle(q, m)
		}
		if m.RequiresReply {
			questions[m.ID] = &Question{ID: m.ID, State: "open"}
			order = append(order, m.ID)
		}
	}
	settled := make([]Question, 0, len(order))
	for _, id := range order {
		settled = append(settled, *questions[id])
	}
	return windowOf(item, msgs, settled, after, anchor)
}

// messagesOf are the updates a person reads as messages in item's thread.
func messagesOf(item string, updates []control.Update) []Message {
	var out []Message
	for _, u := range updates {
		if u.Item == item && slices.Contains(messageKinds, u.Kind) {
			out = append(out, Message{ID: u.ID, Author: u.Author, At: u.At, Kind: u.Kind, Body: u.Body,
				Recipient: u.Recipient, ReplyTo: u.ReplyTo, RequiresReply: u.RequiresReply})
		}
	}
	return out
}

// statusesOf are the server's question statuses as the thread carries them.
func statusesOf(statuses []control.QuestionStatus) []Question {
	out := make([]Question, 0, len(statuses))
	for _, q := range statuses {
		out = append(out, Question{ID: q.ID, State: q.State, Acknowledged: q.Acknowledged, Replies: q.Replies, Resolution: q.Resolution})
	}
	return out
}

// windowSize is how many messages the window holds: pageSize, or enough to
// reach the anchor when it is one of msgs.
func windowSize(msgs []Message, anchor int) int {
	if i := slices.IndexFunc(msgs, func(m Message) bool { return m.ID == anchor }); i >= pageSize {
		return i + 1
	}
	return pageSize
}

// windowOf is the thread's window: the messages after the cursor, at most
// windowSize of them, with the questions as given.
func windowOf(item string, msgs []Message, questions []Question, after, anchor int) Thread {
	out := Thread{Item: item, Messages: []Message{}, Questions: questions, Next: after}
	pending := slices.DeleteFunc(slices.Clone(msgs), func(m Message) bool { return m.ID <= after })
	if limit := windowSize(pending, anchor); len(pending) > limit {
		pending, out.More = pending[:limit], true
	}
	if len(pending) > 0 {
		out.Messages = pending
		out.Next = pending[len(pending)-1].ID
	}
	return out
}

// settle folds one reply, acknowledgement or resolution into its question.
func settle(q *Question, m Message) {
	switch m.Kind {
	case "reply":
		q.Replies++
	case "acknowledged":
		q.Acknowledged = true
	case "resolved", control.KindPaneAnswer:
		q.State, q.Resolution = "resolved", m.ID
	}
}
