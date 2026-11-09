package control

import (
	"slices"
	"strings"
	"time"
)

// Observation is the cached host view of one execution attempt.
type Observation struct {
	// State is working, waiting, limited, live, exited or unknown.
	State string
	// Seen is the last time the host reported the attempt's window live.
	Seen time.Time
}

// SessionRow is one active planner or executor attempt. Its fields are the
// session view's whole contract: no credential, screen or run file is added.
type SessionRow struct {
	Item     string     `json:"item"`
	Kind     string     `json:"kind"`
	Role     string     `json:"role"`
	Title    string     `json:"title"`
	Repo     string     `json:"repo"`
	RepoSlug string     `json:"repo_slug"`
	Plan     string     `json:"plan,omitempty"`
	State    string     `json:"state"`
	Attempt  string     `json:"attempt"`
	Host     string     `json:"host"`
	Harness  string     `json:"harness"`
	Started  time.Time  `json:"started"`
	Observed string     `json:"observed"`
	LastSeen time.Time  `json:"last_seen,omitzero"`
	Feedback *UpdateRef `json:"feedback,omitempty"`
	Latest   *UpdateRef `json:"latest,omitempty"`
}

// UpdateRef points at one update by item and sequence, with a short plain-text summary.
type UpdateRef struct {
	Item    string    `json:"item"`
	Update  int       `json:"update"`
	Kind    string    `json:"kind"`
	Author  string    `json:"author"`
	At      time.Time `json:"at"`
	Summary string    `json:"summary"`
}

// SummaryRunes bounds an update summary in the session view.
const SummaryRunes = 240

// humanKinds are the updates a person reads as a message; acknowledgements,
// lifecycle records and the coordinator's automatic notices are left out of
// the session preview.
var humanKinds = []string{"say", "ask", "reply", "resolved"}

// sessionView projects every current, unended attempt, and every vanished
// one awaiting its resume, from one snapshot. Host observations come only
// from the cache, so a request never probes a host.
func (s Service) sessionView(st *State, a Actor) (Response, error) {
	if err := operator(a); err != nil {
		return Response{}, err
	}
	out := Response{}
	for _, at := range st.Attempts {
		item, ok := st.Items[at.Item]
		if !ok || item.Attempt != at.ID || at.State == "ended" && !Vanished(st, item) {
			continue
		}
		row := s.sessionRow(st, item, at)
		if at.State == "ended" {
			row.Observed = "exited"
		}
		out.Sessions = append(out.Sessions, row)
	}
	slices.SortFunc(out.Sessions, func(x, y SessionRow) int { return Number(x.Item) - Number(y.Item) })
	return out, nil
}

func (s Service) sessionRow(st *State, item Item, at Attempt) SessionRow {
	row := SessionRow{Item: item.ID, Kind: item.Kind, Role: "executor", Title: item.Title, Repo: item.Repo,
		RepoSlug: st.Items[item.Repo].Slug, Plan: item.Plan, State: item.State, Attempt: at.ID, Host: at.Host,
		Harness: at.Harness, Started: at.Started, Observed: "unknown"}
	if item.Kind == "plan" {
		row.Role, row.Plan = "planner", item.ID
	}
	if s.Observe != nil {
		obs := s.Observe(at.ID)
		row.LastSeen = obs.Seen
		if obs.State != "" {
			row.Observed = obs.State
		}
	}
	if id := PendingQuestion(st, item.ID); id > 0 {
		row.Feedback = updateRef(st.Updates[id-1])
	}
	for i := len(st.Updates) - 1; i >= 0; i-- {
		u := st.Updates[i]
		if u.Item == item.ID && u.Author != "coordinator" && slices.Contains(humanKinds, u.Kind) && strings.TrimSpace(u.Body) != "" {
			row.Latest = updateRef(u)
			break
		}
	}
	return row
}

func updateRef(u Update) *UpdateRef {
	return &UpdateRef{Item: u.Item, Update: u.ID, Kind: u.Kind, Author: u.Author, At: u.At, Summary: Summary(u.Body)}
}

// Summary is a body as one line of plain text, cut to SummaryRunes.
func Summary(body string) string {
	text := []rune(strings.Join(strings.Fields(body), " "))
	if len(text) <= SummaryRunes {
		return string(text)
	}
	return strings.TrimSpace(string(text[:SummaryRunes-1])) + "…"
}
