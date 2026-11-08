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
var readOps = []string{"list", "plan-view", "attempts", "get", "updates"}

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

	mu     sync.Mutex
	last   map[string]snapshot
	failed map[string]string // the latest failure of a view that has a snapshot
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
	return &upstream{client: client, timeout: 10 * time.Second, now: now, logf: logf, last: map[string]snapshot{}, failed: map[string]string{}}
}

func (u *upstream) call(ctx context.Context, r control.Request) (control.Response, error) {
	if !slices.Contains(readOps, r.Op) {
		return control.Response{}, fmt.Errorf("operation %q is not a dashboard read", r.Op)
	}
	if (r.Op == "get" || r.Op == "updates") && r.ID == "" {
		return control.Response{}, fmt.Errorf("operation %q reads one item", r.Op)
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
		delete(u.failed, key)
		return Envelope{Data: s.value, Fetched: s.fetched}, 200
	}
	status, message := u.failure(err)
	if status == 404 {
		delete(u.last, key)
		delete(u.failed, key)
		return Envelope{Error: message}, status
	}
	s, ok := u.last[key]
	if !ok {
		return Envelope{Error: message}, status
	}
	u.failed[key] = message
	return Envelope{Data: s.value, Fetched: s.fetched, Stale: true, Error: message}, 200
}

// cached returns the last view without calling the control server, and
// whether there was one. It stays stale while the latest load failed.
func (u *upstream) cached(key string) (Envelope, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	s, ok := u.last[key]
	failure := u.failed[key]
	return Envelope{Data: s.value, Fetched: s.fetched, Stale: failure != "", Error: failure}, ok
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

// plans filters the plan list. A cached request, made when only the filter
// changed, reuses the last list when there is one.
func (u *upstream) plans(ctx context.Context, f Filter, cached bool) (Envelope, int) {
	env, ok := u.cached("plans")
	status := 200
	if !cached || !ok {
		env, status = u.load(ctx)
	}
	if loaded, ok := env.Data.([2][]control.Item); ok {
		env.Data = planList(loaded[0], loaded[1], f)
	}
	return env, status
}

func (u *upstream) load(ctx context.Context) (Envelope, int) {
	return u.view(ctx, "plans", func(ctx context.Context) (any, error) {
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

// fresh loads a view the page keeps for itself: nothing is remembered here,
// and a failure is only reported, so the page marks what it holds stale.
func (u *upstream) fresh(ctx context.Context, load func(context.Context) (any, error)) (Envelope, int) {
	value, err := load(ctx)
	if err != nil {
		status, message := u.failure(err)
		return Envelope{Error: message}, status
	}
	return Envelope{Data: value, Fetched: u.now().UTC()}, 200
}

// item loads a plan or task with its description rendered.
func (u *upstream) item(ctx context.Context, id string) (Envelope, int) {
	return u.fresh(ctx, func(ctx context.Context) (any, error) {
		got, err := u.call(ctx, control.Request{Op: "get", ID: id})
		if err != nil {
			return nil, err
		}
		if got.Item == nil {
			return nil, errors.New("the control server sent no item")
		}
		return itemDetail(*got.Item), nil
	})
}

// maxThreadReads bounds how many 200-update reads one thread request makes.
const maxThreadReads = 100

// thread loads a window of an item's messages. It reads the item's updates
// from the start, because the status of a question depends on updates after it.
func (u *upstream) thread(ctx context.Context, id string, after, anchor int) (Envelope, int) {
	return u.fresh(ctx, func(ctx context.Context) (any, error) {
		if _, err := u.call(ctx, control.Request{Op: "get", ID: id}); err != nil {
			return nil, err
		}
		var all []control.Update
		for cursor, reads := 0, 0; ; reads++ {
			if reads == maxThreadReads {
				return nil, &control.Problem{Status: 502, Message: "thread too long to show"}
			}
			page, err := u.call(ctx, control.Request{Op: "updates", ID: id, After: cursor})
			if err != nil {
				return nil, err
			}
			all = append(all, page.Updates...)
			if len(page.Updates) < 200 {
				return threadWindow(id, all, after, anchor), nil
			}
			cursor = page.Updates[len(page.Updates)-1].ID
		}
	})
}
