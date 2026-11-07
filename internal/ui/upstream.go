package ui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

// readOps is every control operation the dashboard may forward. All are reads.
var readOps = []string{"list", "plan-view", "attempts"}

// Caller is the control client boundary; control.Client satisfies it.
type Caller interface {
	Call(context.Context, control.Request) (control.Response, error)
}

// upstream holds the operator credential and forwards only read operations.
type upstream struct {
	client  Caller
	timeout time.Duration
	now     func() time.Time
	logf    func(string, ...any)

	mu   sync.Mutex
	last map[string]snapshot
}

// snapshot is the last successful result of one view.
type snapshot struct {
	value   any
	fetched time.Time
}

// Envelope is what every data route returns. On an upstream failure it
// carries the last successful data, marked stale, with the error.
type Envelope struct {
	Data    any       `json:"data"`
	Fetched time.Time `json:"fetched"`
	Stale   bool      `json:"stale,omitempty"`
	Error   string    `json:"error,omitempty"`
}

func newUpstream(client Caller, now func() time.Time, logf func(string, ...any)) *upstream {
	return &upstream{client: client, timeout: 10 * time.Second, now: now, logf: logf, last: map[string]snapshot{}}
}

func (u *upstream) call(ctx context.Context, r control.Request) (control.Response, error) {
	if !slices.Contains(readOps, r.Op) {
		return control.Response{}, fmt.Errorf("operation %q is not a dashboard read", r.Op)
	}
	ctx, cancel := context.WithTimeout(ctx, u.timeout)
	defer cancel()
	return u.client.Call(ctx, r)
}

// view loads one view, remembers it, and falls back to the last success when
// the control server fails. An unknown item is returned as is and forgotten.
func (u *upstream) view(ctx context.Context, key string, load func(context.Context) (any, error)) (Envelope, int) {
	value, err := load(ctx)
	u.mu.Lock()
	defer u.mu.Unlock()
	if err == nil {
		s := snapshot{value: value, fetched: u.now().UTC()}
		u.last[key] = s
		return Envelope{Data: s.value, Fetched: s.fetched}, 200
	}
	status, message := u.failure(err)
	if status == 404 {
		delete(u.last, key)
		return Envelope{Error: message}, status
	}
	s, ok := u.last[key]
	if !ok {
		return Envelope{Error: message}, status
	}
	return Envelope{Data: s.value, Fetched: s.fetched, Stale: true, Error: message}, 200
}

// failure turns an upstream error into a status and a message safe to show.
// Transport details stay in the dashboard's log.
func (u *upstream) failure(err error) (int, string) {
	var p *control.Problem
	if errors.As(err, &p) {
		if p.Status >= 500 {
			return 502, "control server error: " + p.Message
		}
		return p.Status, p.Message
	}
	if u.logf != nil {
		u.logf("control server: %v", err)
	}
	return 502, "control server unreachable"
}

func (u *upstream) plans(ctx context.Context, f Filter) (Envelope, int) {
	env, status := u.view(ctx, "plans", func(ctx context.Context) (any, error) {
		repos, err := u.call(ctx, control.Request{Op: "list", State: "repo"})
		if err != nil {
			return nil, err
		}
		plans, err := u.call(ctx, control.Request{Op: "list", State: "plan"})
		if err != nil {
			return nil, err
		}
		return [2][]control.Item{repos.Items, plans.Items}, nil
	})
	if loaded, ok := env.Data.([2][]control.Item); ok {
		env.Data = planList(loaded[0], loaded[1], f)
	}
	return env, status
}

func (u *upstream) plan(ctx context.Context, id string) (Envelope, int) {
	return u.view(ctx, "plan "+id, func(ctx context.Context) (any, error) {
		view, err := u.call(ctx, control.Request{Op: "plan-view", ID: id})
		if err != nil {
			return nil, err
		}
		attempts, err := u.call(ctx, control.Request{Op: "attempts"})
		if err != nil {
			return nil, err
		}
		return planDetail(id, view.Items, attempts.Attempts), nil
	})
}
