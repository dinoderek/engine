package controlcmd

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

// The retry of a command that cannot reach the server. A session's default
// window stays under a harness's usual tool timeout, so a longer outage ends
// in the error, not a killed command.
const (
	// DefaultRetryFor is how long a session's command retries.
	DefaultRetryFor = 90 * time.Second
	// RetryForEnv overrides the window, as a Go duration; 0 turns retrying off.
	RetryForEnv  = "ENGINE_RETRY_FOR"
	firstBackoff = time.Second
	lastBackoff  = 15 * time.Second
	// jitterShare is the share of a wait added at random, so sessions that
	// lost the server together do not retry together.
	jitterShare = 0.2
)

// Retry is the clock, sleep and jitter the retry uses; tests replace them. A
// nil field is the real one.
type Retry struct {
	Now    func() time.Time
	Sleep  func(context.Context, time.Duration) error
	Jitter func(time.Duration) time.Duration
}

func (r *Retry) now() time.Time {
	if r != nil && r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Retry) sleep(ctx context.Context, d time.Duration) error {
	if r != nil && r.Sleep != nil {
		return r.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Retry) jitter(d time.Duration) time.Duration {
	if r != nil && r.Jitter != nil {
		return r.Jitter(d)
	}
	return time.Duration(rand.Float64() * jitterShare * float64(d))
}

// retryWindow is how long this command retries: ENGINE_RETRY_FOR if set, else
// DefaultRetryFor for a session's command (one with an attempt) and none for
// the operator's, which fails at once when the server is down.
func retryWindow(e Env) (time.Duration, error) {
	raw := e.Getenv(RetryForEnv)
	if raw == "" {
		if e.Getenv("ENGINE_ATTEMPT_ID") != "" {
			return DefaultRetryFor, nil
		}
		return 0, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("%s takes a duration such as 90s, or 0 for none, not %q", RetryForEnv, raw)
	}
	return d, nil
}

// caller sends a request to the server, retrying as the environment says.
type caller func(context.Context, control.Client, control.Request) (control.Response, error)

// newCaller reads the retry window once, so a bad ENGINE_RETRY_FOR is a
// usage error before any request goes out.
func newCaller(e Env) (caller, error) {
	window, err := retryWindow(e)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, client control.Client, req control.Request) (control.Response, error) {
		return callRetrying(ctx, e, client, req, window)
	}, nil
}

// transient reports whether a failed call may succeed later: the server did
// not answer, or failed (5xx). A 4xx is the server's answer and never retries.
func transient(err error) bool {
	var refused *control.Problem
	return !errors.As(err, &refused) || refused.Status >= 500
}

// callRetrying sends req and, while the server is unreachable or failing,
// sends it again under the same request key, with a wait that starts at one
// second and doubles to fifteen, until window has passed. The first retry
// prints one line naming the key. When the window ends, the error is the
// last the server gave.
func callRetrying(ctx context.Context, e Env, client control.Client, req control.Request, window time.Duration) (control.Response, error) {
	if req.Key == "" {
		req.Key = control.Secret()
	}
	if window <= 0 {
		return client.Call(ctx, req)
	}
	deadline := e.Retry.now().Add(window)
	delay := firstBackoff
	var last error
	for announced := false; ; announced = true {
		// A try is cut at the deadline, so the window bounds the whole command.
		try, cancel := context.WithTimeout(ctx, max(deadline.Sub(e.Retry.now()), time.Millisecond))
		out, err := client.Call(try, req)
		timedOut := try.Err() != nil
		cancel()
		if err == nil {
			return out, nil
		}
		if timedOut && ctx.Err() == nil && last != nil {
			err = last // the cut-off try says nothing the earlier one did not
		}
		wait := min(delay+e.Retry.jitter(delay), lastBackoff)
		if !transient(err) || ctx.Err() != nil || !e.Retry.now().Add(wait).Before(deadline) {
			return out, err
		}
		last = err
		if !announced {
			fmt.Fprintf(e.Err, "engine server unreachable; retrying until %s (request key %s)\n", deadline.Format("15:04:05"), req.Key)
		}
		if err = e.Retry.sleep(ctx, wait); err != nil {
			return out, last
		}
		delay = min(2*delay, lastBackoff)
	}
}
