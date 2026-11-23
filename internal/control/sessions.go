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
	// Choices, Recommended and Class are a question's (Update).
	Choices     []string `json:"choices,omitempty"`
	Recommended *int     `json:"recommended,omitempty"`
	Class       string   `json:"class,omitempty"`
}

// SummaryRunes bounds an update summary in the session view.
const SummaryRunes = 240

// humanKinds are the updates a person reads as a message; acknowledgements,
// lifecycle records and the coordinator's automatic notices are left out of
// the session preview.
var humanKinds = []string{"say", "ask", "reply", "resolved", KindPaneAnswer, KindPaneMessage}

// sessionView projects every current, unended attempt, and every vanished
// or early-death one awaiting its dispatch, from one snapshot. Host observations come only
// from the cache, so a request never probes a host.
func (s Service) sessionView(tx *Tx, a Actor) (Response, error) {
	if err := operator(a); err != nil {
		return Response{}, err
	}
	return Response{Sessions: s.sessionRows(tx, tx.SessionAttempts())}, nil
}

// sessionFacts are what the rows of a session list read beyond their items
// and attempts, each fetched once for the whole list.
type sessionFacts struct {
	repos map[string]Item
	// asked is the open question that holds each item, and latest the
	// newest message about it.
	asked, latest map[string]Update
}

// sessionRows are the session rows of the given current attempts, by item
// number: an ended attempt is listed only while its item has vanished.
func (s Service) sessionRows(tx *Tx, attempts []Attempt) []SessionRow {
	ids := make([]string, len(attempts))
	for i, at := range attempts {
		ids[i] = at.Item
	}
	items := tx.ItemsByID(ids)
	var listed []Attempt
	var repos, shown []string
	for _, at := range attempts {
		if item := items[at.Item]; at.State != "ended" || Vanished(tx, item) {
			listed = append(listed, at)
			shown = append(shown, at.Item)
			repos = append(repos, item.Repo)
		}
	}
	facts := sessionFacts{repos: tx.ItemsByID(repos), latest: tx.LatestMessages(shown, humanKinds), asked: map[string]Update{}}
	questions := tx.OwnQuestions(shown)
	byID := make([]int, 0, len(questions))
	for _, q := range questions {
		byID = append(byID, q)
	}
	updates := tx.UpdatesByID(byID)
	for id, q := range questions {
		facts.asked[id] = updates[q]
	}
	var rows []SessionRow
	for _, at := range listed {
		row := s.sessionRow(items[at.Item], at, facts)
		if at.State == "ended" {
			row.Observed = "exited"
		}
		rows = append(rows, row)
	}
	slices.SortFunc(rows, func(x, y SessionRow) int { return Number(x.Item) - Number(y.Item) })
	return rows
}

func (s Service) sessionRow(item Item, at Attempt, facts sessionFacts) SessionRow {
	row := SessionRow{Item: item.ID, Kind: item.Kind, Role: "executor", Title: item.Title, Repo: item.Repo,
		RepoSlug: facts.repos[item.Repo].Slug, Plan: item.Plan, State: item.State, Attempt: at.ID, Host: at.Host,
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
	if q, ok := facts.asked[item.ID]; ok {
		row.Feedback = updateRef(q)
	}
	if u, ok := facts.latest[item.ID]; ok {
		row.Latest = updateRef(u)
	}
	return row
}

func updateRef(u Update) *UpdateRef {
	return &UpdateRef{Item: u.Item, Update: u.ID, Kind: u.Kind, Author: u.Author, At: u.At, Summary: Summary(u.Body),
		Choices: u.Choices, Recommended: u.Recommended, Class: u.Class}
}

// Summary is a body as one line of plain text, cut to SummaryRunes.
func Summary(body string) string {
	text := []rune(strings.Join(strings.Fields(body), " "))
	if len(text) <= SummaryRunes {
		return string(text)
	}
	return strings.TrimSpace(string(text[:SummaryRunes-1])) + "…"
}
