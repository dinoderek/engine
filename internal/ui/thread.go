package ui

import (
	"slices"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

// pageSize is how many messages one thread read returns.
const pageSize = 50

// ItemDetail is a plan or task for the side panel. Description is empty when
// the item has no body.
type ItemDetail struct {
	ID          string  `json:"id"`
	Kind        string  `json:"kind"`
	Repo        string  `json:"repo"`
	Plan        string  `json:"plan,omitempty"`
	Title       string  `json:"title"`
	State       string  `json:"state"`
	Description []Block `json:"description"`
}

// Message is one human-facing update. Its body is plain text.
type Message struct {
	ID            int       `json:"id"`
	Author        string    `json:"author"`
	At            time.Time `json:"at"`
	Kind          string    `json:"kind"` // say, ask, reply, acknowledged or resolved
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

// messageKinds includes ruling: stored records may hold plan rulings,
// which the server does not write.
var messageKinds = []string{"say", "ask", "reply", "acknowledged", "resolved", "ruling"}

func itemDetail(it control.Item) ItemDetail {
	return ItemDetail{ID: it.ID, Kind: it.Kind, Repo: it.Repo, Plan: it.Plan, Title: it.Title, State: it.State, Description: RenderMarkdown(it.Body)}
}

// threadWindow projects one item's updates, oldest first. It returns the
// messages after the cursor, at most pageSize of them, extended to include
// the anchor update when it falls later in the thread.
func threadWindow(item string, updates []control.Update, after, anchor int) Thread {
	out := Thread{Item: item, Messages: []Message{}, Questions: []Question{}, Next: after}
	var pending []Message
	questions := map[int]*Question{}
	for _, u := range updates {
		if u.Item != item || !slices.Contains(messageKinds, u.Kind) {
			continue
		}
		m := Message{ID: u.ID, Author: u.Author, At: u.At, Kind: u.Kind, Body: u.Body,
			Recipient: u.Recipient, ReplyTo: u.ReplyTo, RequiresReply: u.RequiresReply}
		if q, ok := questions[m.ReplyTo]; ok {
			settle(q, m)
		}
		if m.RequiresReply {
			questions[m.ID] = &Question{ID: m.ID, State: "open"}
			out.Questions = append(out.Questions, Question{ID: m.ID})
		}
		if m.ID > after {
			pending = append(pending, m)
		}
	}
	for i := range out.Questions {
		out.Questions[i] = *questions[out.Questions[i].ID]
	}
	limit := pageSize
	if i := slices.IndexFunc(pending, func(m Message) bool { return m.ID == anchor }); i >= limit {
		limit = i + 1
	}
	if len(pending) > limit {
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
	case "resolved":
		q.State, q.Resolution = "resolved", m.ID
	}
}
