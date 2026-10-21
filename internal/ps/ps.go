// Package ps lists the tasks that executors hold, with the state of each
// session. A task's state comes from its tracker labels. For a claim on this
// host the session layer adds whether the session is alive (tmux, and the
// harness's own list of live sessions) and whether it is working or waiting.
// A claim on another host shows its label state, with liveness unknown.
package ps

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

// The states a row shows.
const (
	Working    = "working"     // alive, and the harness says it is busy
	Waiting    = "waiting"     // alive, and the harness says it waits for input
	Running    = "running"     // holds the task; nothing says more
	NeedsHuman = "needs-human" // stopped at a hard stop
	InReview   = "in-review"   // its PR is open
	Dead       = "dead"        // its window is gone, and nothing else says it lives
)

// isClaude reports whether a claim's harness line names Claude Code, the
// harness whose sessions `claude agents` lists. Claims spell it claude,
// claude-code or Claude Code.
func isClaude(harness string) bool {
	return strings.HasPrefix(strings.ToLower(harness), "claude")
}

// Row is one claimed task.
type Row struct {
	Task    string `json:"task"` // owner/repo#n
	Title   string `json:"title"`
	Session string `json:"session"` // <repo>-<task>: the tmux window and the name to attach to
	Run     string `json:"run,omitempty"`
	Host    string `json:"host,omitempty"`
	Harness string `json:"harness,omitempty"`
	Branch  string `json:"branch,omitempty"`
	State   string `json:"state"`
	// Alive is null when it is unknown: the claim is on another host, or a
	// probe failed.
	Alive      *bool  `json:"alive"`
	WaitingFor string `json:"waiting_for,omitempty"`
	ClaimedAt  string `json:"claimed_at"`
	AgeSeconds int64  `json:"age_seconds"`
}

// Source is where a listing reads from. Each probe runs at most once, and
// only when a claim on this host needs it.
type Source struct {
	// Claimed lists the claimed tasks.
	Claimed func() ([]tracker.ClaimedTask, error)
	// Local is this host's name in its host file.
	Local string
	// Windows lists the windows of engine's tmux session.
	Windows func() ([]session.WindowInfo, error)
	// Agents lists the live Claude Code sessions on this host.
	Agents func() ([]session.Agent, error)
	// Checkout reads the git checkout a directory is in.
	Checkout func(dir string) (session.Checkout, error)
	Now      func() time.Time
}

// Listing is the rows, ordered by repo and task number, and a warning for each
// probe that failed. A failed probe leaves the liveness it would have settled
// unknown.
type Listing struct {
	Rows     []Row
	Warnings []string
}

// List reads every claimed task and works out its state.
func List(src Source) (Listing, error) {
	tasks, err := src.Claimed()
	if err != nil {
		return Listing{}, err
	}
	slices.SortFunc(tasks, func(a, b tracker.ClaimedTask) int {
		return cmp.Or(cmp.Compare(a.Ref.Repo, b.Ref.Repo), cmp.Compare(a.Ref.Number, b.Ref.Number))
	})
	p := &probes{src: src}
	now := src.Now()
	var l Listing
	for _, t := range tasks {
		row := claimRow(t, now)
		if t.Claim.Host != "" && t.Claim.Host == src.Local {
			p.settle(&row, t)
		}
		l.Rows = append(l.Rows, row)
	}
	l.Warnings = p.warnings
	return l, nil
}

// labelState is the state the task's labels give, the most advanced first: an
// executor that has set needs-human or in-review has left running behind.
func labelState(t tracker.ClaimedTask) string {
	switch {
	case t.Has("engine:needs-human"):
		return NeedsHuman
	case t.Has("engine:in-review"):
		return InReview
	}
	return Running
}

func claimRow(t tracker.ClaimedTask, now time.Time) Row {
	return Row{
		Task: t.Ref.String(), Title: t.Title, Session: t.Claim.Name, Run: t.Claim.Run,
		Host: t.Claim.Host, Harness: t.Claim.HarnessName(), Branch: t.Claim.Branch,
		State: labelState(t), ClaimedAt: t.Claim.PostedAt.UTC().Format(time.RFC3339),
		AgeSeconds: max(0, int64(now.Sub(t.Claim.PostedAt).Seconds())),
	}
}
