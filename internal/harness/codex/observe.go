package codex

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

// Observe reads the account's rate limits, the sign-in of engine's Codex
// home, and each run's state from its hooks' state file, supplemented by the
// newest stored turn of its thread. The rate-limit read and the turn reads
// each have their own deadline; any that fail leave what they would say
// unknown. A host that does not run Codex, or whose binary cannot be found,
// has only the hooks' states: its sessions may outlive a config change.
func (a Adapter) Observe(ctx context.Context, host hosts.Host, runs map[string]session.Run) harness.Snapshot {
	var snap harness.Snapshot
	var turns map[string]json.RawMessage
	if bin, err := host.BinPath(Name, a.env.Home); err == nil && host.Runs(Name) {
		snap.Quota = a.quota(ctx, bin)
		if a.env.Exec != nil {
			// Engine's Codex home: joined sessions use it, and setup checks
			// its sign-in. An embedded session runs in the host's own home,
			// whose sign-in may differ when engine's home keeps a login of
			// its own.
			snap.SignedOut = signedOut(a.env.Exec("env", "CODEX_HOME="+a.engineHome(), bin, "login", "status"))
		}
		turns = a.turns(ctx, bin, runs)
	}
	snap.Sessions = map[string]harness.State{}
	for name, r := range runs {
		if st, ok := probe(r, turns, snap.Quota); ok {
			snap.Sessions[name] = st
		}
	}
	return snap
}

// quota is the account's reading from the current call, never a stale run
// file, or nil when it has no window.
func (a Adapter) quota(ctx context.Context, bin string) *session.Quota {
	ctx, cancel := context.WithTimeout(ctx, appServerTimeout)
	defer cancel()
	at := a.env.Time()
	data, err := a.opts.Quota(ctx, bin, a.engineHome())
	if err != nil {
		return nil
	}
	q := codexQuota(data, at)
	if len(q.Windows) == 0 {
		return nil
	}
	q = session.MergeQuota(a.env.Time(), map[string]session.Quota{Name: q})[Name]
	return &q
}

// turns reads the newest stored turn of each live run's thread, trying
// engine's Codex home first, then the host's own for the threads it lacks:
// embedded sessions use the host's. All the reads share one deadline.
func (a Adapter) turns(ctx context.Context, bin string, runs map[string]session.Run) map[string]json.RawMessage {
	ids := []string{}
	for _, r := range runs {
		if st, ok := session.ReadHarnessState(r.Dir); ok && st.State != session.StateEnded && st.SessionID != "" && !st.At.IsZero() {
			ids = append(ids, st.SessionID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	slices.Sort(ids)
	ctx, cancel := context.WithTimeout(ctx, appServerTimeout)
	defer cancel()
	out := map[string]json.RawMessage{}
	for _, home := range slices.Compact([]string{a.engineHome(), a.home()}) {
		data, _ := a.opts.Turns(ctx, bin, home, ids)
		for id, turn := range data {
			if slices.Contains(ids, id) {
				out[id] = turn
			}
		}
		ids = slices.DeleteFunc(ids, func(id string) bool { return len(out[id]) != 0 })
		if len(ids) == 0 || ctx.Err() != nil {
			break
		}
	}
	return out
}

// probe is a run's state: its hooks' state, made limited by a newer stored
// turn that failed on the usage limit, or by an exhausted quota window while
// the session waits or that turn failed. A missing or unreadable state file
// is unknown, and so is ended while the window lives. Codex's Stop and
// Interrupt hook events carry no limit error, so the stored turn is what
// tells a limited session from a waiting one.
func probe(r session.Run, turns map[string]json.RawMessage, q *session.Quota) (harness.State, bool) {
	st, ok := session.ReadHarnessState(r.Dir)
	if !ok || st.State == session.StateEnded {
		return harness.State{}, false
	}
	out := harness.State{State: st.State}
	if st.State == session.StateLimited {
		out.ResetsAt = st.ResetsAt
		return out, true
	}
	failed := codexStateLimited(turns[st.SessionID], st, r.Record.Spawned)
	if failed {
		out.State = session.StateLimited
	}
	if (st.State != session.StateWaiting && !failed) || q == nil {
		return out, true
	}
	for _, w := range q.Windows {
		if w.Used >= 100 {
			out.State = session.StateLimited
			out.ResetsAt = max(out.ResetsAt, w.ResetsAt)
		}
	}
	return out, true
}

// signedOut reads `codex login status`, which exits non-zero and says "Not
// logged in" when signed out.
func signedOut(out string, err error) bool {
	var exit *session.ExitError
	if !errors.As(err, &exit) {
		return false
	}
	return strings.Contains(strings.ToLower(out+exit.Stderr), "not logged in")
}
