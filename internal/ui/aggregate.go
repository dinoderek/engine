package ui

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

// aggregateRetry is how long the dashboard goes without asking a control
// server that did not know overview and plan-detail, so one that is upgraded
// meanwhile is found again.
const aggregateRetry = 10 * time.Minute

// aggregates is what the dashboard has learned about its control server: one
// answer per server, not per call.
type aggregates struct {
	mu     sync.Mutex
	legacy bool      // the server did not know them
	since  time.Time // when it said so
}

// ask reports whether to try the aggregate reads: until the server is known
// to lack them, and again once aggregateRetry has passed.
func (u *upstream) ask() bool {
	u.agg.mu.Lock()
	defer u.agg.mu.Unlock()
	return !u.agg.legacy || u.now().Sub(u.agg.since) >= aggregateRetry
}

// learn records whether the server knows the aggregate reads.
func (u *upstream) learn(knows bool) {
	u.agg.mu.Lock()
	defer u.agg.mu.Unlock()
	u.agg.legacy, u.agg.since = !knows, u.now()
}

// unknownOp reports whether err says the control server does not know op: a
// 400, or a 404 that is not plan-detail's own "unknown plan".
func unknownOp(op string, err error) bool {
	var p *control.Problem
	if !errors.As(err, &p) {
		return false
	}
	return p.Status == 400 || p.Status == 404 && (op != "plan-detail" || p.Message != "unknown plan")
}

// aggregate makes one aggregate read. ok is false when the server does not
// know the operation, which is learned once; every other failure is err.
func (u *upstream) aggregate(ctx context.Context, r control.Request) (got control.Response, ok bool, err error) {
	if !u.ask() {
		return got, false, nil
	}
	got, err = u.call(ctx, r)
	switch {
	case err == nil:
		u.learn(true)
		return got, true, nil
	case unknownOp(r.Op, err):
		u.learn(false)
		return control.Response{}, false, nil
	}
	return got, false, err
}

// overviewRead is the one overview read that the views of a request share.
type overviewRead struct {
	u    *upstream
	once sync.Once
	resp control.Response
	ok   bool
	err  error
}

func (u *upstream) newRead() *overviewRead { return &overviewRead{u: u} }

// get is the overview, read once however many views ask. ok is false when
// the server has none, and each view then reads what it needs itself.
func (o *overviewRead) get(ctx context.Context) (control.Response, bool, error) {
	o.once.Do(func() { o.resp, o.ok, o.err = o.u.aggregate(ctx, control.Request{Op: "overview"}) })
	return o.resp, o.ok, o.err
}

// planDetailRead is a plan's detail read, ok false when the server has none.
func (u *upstream) planDetailRead(ctx context.Context, id string) (control.Response, bool, error) {
	return u.aggregate(ctx, control.Request{Op: "plan-detail", ID: id})
}

// Bundle is the three views every page refresh needs, each an envelope as
// its own route would answer, from one control server read.
type Bundle struct {
	Inbox    Envelope `json:"inbox"`
	Sessions Envelope `json:"sessions"`
	Plans    Envelope `json:"plans"`
}

// overview loads the inbox, the sessions and the plan list together. A view
// that fails answers its envelope's error, or its last success marked stale.
func (u *upstream) overview(ctx context.Context, f Filter) Bundle {
	read := u.newRead()
	var b Bundle
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); b.Inbox, _ = u.inboxFrom(ctx, read) }()
	go func() { defer wg.Done(); b.Sessions, _ = u.sessionsFrom(ctx, read) }()
	go func() { defer wg.Done(); b.Plans, _ = u.plansFrom(ctx, read, f, false) }()
	wg.Wait()
	return b
}
