package ps

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

// probes reads each host once for every claim that needs it, and keeps what
// went wrong.
type probes struct {
	src       Source
	checkouts map[string]checkout
	warnings  []string
}

type checkout struct {
	session.Checkout
	err error
}

func (p *probes) warn(what string, err error) {
	msg := fmt.Sprintf("%s: %v (liveness it would settle is unknown)", what, err)
	if !slices.Contains(p.warnings, msg) {
		p.warnings = append(p.warnings, msg)
	}
}

// view is what one host's status says about its sessions. A failed call is
// an error, and an error is unknown, never dead.
type view struct {
	p      *probes
	host   string
	local  bool // this host: a session started by hand is also found by its worktree
	status hostops.Status
	err    error
}

// window finds the window the claim names, by name: tmux reuses window ids
// after a restart. Nil means the host has no such window.
func (v view) window(name string) (*hostops.Window, error) {
	if v.err != nil {
		return nil, v.err
	}
	for i, w := range v.status.Windows {
		if w.Name == name {
			return &v.status.Windows[i], nil
		}
	}
	return nil, nil
}

// agent finds the live Claude Code session of a claim started by hand: the
// one named like the session, else, on this host, the one working in the
// claim's branch of its repo. Another host's checkouts are not part of its
// status, so there a session renamed by hand stays unknown.
func (v view) agent(t tracker.ClaimedTask) (*session.Agent, error) {
	if v.err != nil {
		return nil, v.err
	}
	if v.status.AgentsError != "" {
		err := errors.New(v.status.AgentsError)
		v.p.warn("claude agents on "+v.host, err)
		return nil, err
	}
	agents := v.status.Agents
	for i := range agents {
		if agents[i].Name == t.Claim.Name {
			return &agents[i], nil
		}
	}
	if !v.local {
		return nil, nil
	}
	for i := range agents {
		ok, err := v.p.inWorktree(agents[i].CWD, t)
		if err != nil {
			v.p.warn("git", err)
			return nil, err
		}
		if ok {
			return &agents[i], nil
		}
	}
	return nil, nil
}

// inWorktree reports whether dir is a checkout of the claim's branch in the
// claim's repo. A directory outside any checkout is not an error: sessions
// run anywhere. Only a git that cannot run is.
func (p *probes) inWorktree(dir string, t tracker.ClaimedTask) (bool, error) {
	if t.Claim.Branch == "" || dir == "" || p.src.Checkout == nil {
		return false, nil
	}
	co, ok := p.checkouts[dir]
	if !ok {
		co.Checkout, co.err = p.src.Checkout(dir)
		if p.checkouts == nil {
			p.checkouts = map[string]checkout{}
		}
		p.checkouts[dir] = co
	}
	var exit *session.ExitError
	switch {
	case errors.As(co.err, &exit):
		return false, nil
	case co.err != nil:
		return false, co.err
	}
	return co.Branch == t.Claim.Branch && co.IsRepo(t.Ref.Repo), nil
}

// settle works out a claim from its host's view: whether its session is
// alive, and what it is doing. A claim in engine's tmux session is settled
// by its window alone, whatever the harness: alive while the window's
// command runs, with the state status gives it, and dead once the window is
// gone or its command exited. A session started by hand may run anywhere and
// under any name: Claude Code's list can show it alive, but failing to find
// it proves nothing and leaves liveness unknown.
func settle(v view, row *Row, t tracker.ClaimedTask) {
	yes, no := true, false
	if sess, win, ok := strings.Cut(t.Claim.Tmux, ":"); ok && sess == session.Name {
		w, err := v.window(cmp.Or(win, t.Claim.Name))
		switch {
		case err != nil:
		case w == nil || !w.Alive:
			row.Alive, row.State = &no, Dead
		default:
			row.Alive = &yes
			row.apply(w.State, w.WaitingFor)
		}
		return
	}
	if !isClaude(t.Claim.Harness) {
		return
	}
	if a, _ := v.agent(t); a != nil {
		row.Alive = &yes
		row.apply(session.AgentState(*a), a.WaitingFor)
	}
}

// apply takes the state status, or Claude Code, gives a live session. It
// refines only running: a task that has left running says more than the
// harness does.
func (r *Row) apply(state, waitingFor string) {
	if r.State != Running {
		return
	}
	switch state {
	case session.StateWorking:
		r.State = Working
	case session.StateWaiting:
		r.State, r.WaitingFor = Waiting, waitingFor
	}
}
