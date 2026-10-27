// Package ps lists the tasks that executors hold, with the state of each
// session. A task's state comes from its tracker labels. The host a claim
// names adds, from its status, one call per host, whether the session is
// alive and whether it is working or waiting: its window's state, or for a
// session started by hand, Claude Code's list of live sessions. When the
// call fails, or the host cannot be asked, the claim shows its label state,
// with liveness unknown.
package ps

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

// The states a row shows.
const (
	Limited    = "limited"     // stopped on a harness usage limit
	Working    = "working"     // alive, and the harness says it is working
	Waiting    = "waiting"     // alive, and the harness says it waits for input
	Running    = "running"     // holds the task; nothing says more
	NeedsHuman = "needs-human" // stopped at a hard stop
	InReview   = "in-review"   // its PR is open
	Dead       = "dead"        // its window is gone, and nothing else says it lives
)

// isClaude reports whether a claim's harness line names Claude Code, the
// harness whose sessions started by hand `claude agents` lists. Claims spell
// it claude, claude-code or Claude Code.
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
	// Alive is null when nothing settled it: the host's status call failed,
	// the claim names a host that cannot be asked, or the session was
	// started by hand and Claude Code does not list it.
	Alive      *bool  `json:"alive"`
	WaitingFor string `json:"waiting_for,omitempty"`
	ResetsAt   int64  `json:"resets_at,omitempty"`
	ClaimedAt  string `json:"claimed_at"`
	AgeSeconds int64  `json:"age_seconds"`
}

// Source is where a listing reads from. Each host's status is read at most
// once, including hosts with no claim.
type Source struct {
	// Hosts lists configured hosts, including ones with no claim.
	Hosts []string
	// Claimed lists the claimed tasks.
	Claimed func() ([]tracker.ClaimedTask, error)
	// Local is this host's name in its host file.
	Local string
	// Status reads a host's status by name, this host's too. Nil leaves
	// every claim unsettled.
	Status func(host string) (hostops.Status, error)
	// Checkout reads the git checkout a directory on this host is in, so a
	// session started by hand here is found by its worktree.
	Checkout func(dir string) (session.Checkout, error)
	Now      func() time.Time
}

// Listing is the rows, ordered by repo and task number, and a warning for each
// probe that failed. A failed probe leaves the liveness it would have settled
// unknown.
type Listing struct {
	Quota    map[string]session.Quota `json:"quota"`
	Rows     []Row                    `json:"sessions"`
	Warnings []string                 `json:"-"`
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
	var views map[string]view
	if src.Status != nil {
		names := slices.Clone(src.Hosts)
		for _, name := range claimHosts(tasks) {
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
		slices.Sort(names)
		views = p.statuses(names)
	}
	now := src.Now()
	l := Listing{Quota: map[string]session.Quota{}}
	for _, v := range views {
		if v.err == nil {
			l.Quota = session.MergeQuota(now, l.Quota, v.status.Quota)
		}
	}
	for _, t := range tasks {
		row := claimRow(t, now)
		if v, ok := views[t.Claim.Host]; ok {
			settle(v, &row, t)
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
