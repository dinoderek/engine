package ps

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

// once runs a probe the first time it is asked for and remembers the answer.
type once[T any] struct {
	done bool
	val  T
	err  error
}

func (o *once[T]) get(f func() (T, error)) (T, error) {
	if !o.done {
		o.val, o.err = f()
		o.done = true
	}
	return o.val, o.err
}

// probes reads the host once for every claim that needs it, and keeps what
// went wrong.
type probes struct {
	src       Source
	windows   once[[]session.WindowInfo]
	agents    once[[]session.Agent]
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

// window reports whether the session has a live window of this name.
func (p *probes) window(name string) (bool, error) {
	ws, err := p.windows.get(func() ([]session.WindowInfo, error) {
		ws, err := p.src.Windows()
		if err != nil {
			p.warn("tmux", err)
		}
		return ws, err
	})
	for _, w := range ws {
		if w.Name == name && !w.Dead {
			return true, nil
		}
	}
	return false, err
}

// agent finds the live Claude Code session of a claim: the one named like the
// session, else the one working in the claim's branch of its repo.
func (p *probes) agent(t tracker.ClaimedTask) (*session.Agent, error) {
	agents, err := p.agents.get(func() ([]session.Agent, error) {
		agents, err := p.src.Agents()
		if err != nil {
			p.warn("claude agents", err)
		}
		return agents, err
	})
	if err != nil {
		return nil, err
	}
	for i := range agents {
		if agents[i].Name == t.Claim.Name {
			return &agents[i], nil
		}
	}
	for i := range agents {
		ok, err := p.inWorktree(agents[i].CWD, t)
		if err != nil {
			p.warn("git", err)
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
	if t.Claim.Branch == "" || dir == "" {
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

// settle works out a claim on this host: whether its session is alive, and
// what it is doing. A session is alive when its window is open and its
// command has not exited, or when Claude Code lists it. It is dead when
// nothing says so and every probe that could have said so ran.
func (p *probes) settle(row *Row, t tracker.ClaimedTask) {
	var agent *session.Agent
	live, unknown, applicable := false, false, false
	if strings.HasPrefix(t.Claim.Tmux, session.Name+":") {
		applicable = true
		ok, err := p.window(t.Claim.Name)
		live, unknown = live || ok, unknown || err != nil
	}
	if row.Harness == claudeCode {
		applicable = true
		a, err := p.agent(t)
		agent, live, unknown = a, live || a != nil, unknown || err != nil
	}
	yes, no := true, false
	switch {
	case live:
		row.Alive = &yes
		row.apply(agent)
	case applicable && !unknown:
		row.Alive, row.State = &no, Dead
	}
}

// apply takes what Claude Code says about a live session. It refines only
// running: a task that has left running says more than the harness does.
func (r *Row) apply(a *session.Agent) {
	if a == nil || r.State != Running {
		return
	}
	switch a.Status {
	case session.AgentBusy:
		r.State = Working
	case session.AgentWaiting, session.AgentIdle:
		r.State, r.WaitingFor = Waiting, a.WaitingFor
	}
}
