package controlrun

import (
	"errors"
	"fmt"
	"strings"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/run"
)

func (c *Coordinator) dispatch(st control.State, item control.Item, host, harness string, cfg *hosts.Config) error {
	key := fmt.Sprintf("dispatch/%s/%d", item.ID, item.Version)
	out, err := c.Service.Dispatch(item, host, harness, key)
	if err != nil {
		return err
	}
	// Only a new execution releases: a replayed one may have a spawn still
	// in flight on the host, whose window and worktree release would take.
	if _, replay := st.Attempts[out.Attempt.ID]; !replay {
		c.releaseEnded(st, *out.Item, *out.Attempt, c.Client(cfg.Hosts[out.Attempt.Host]))
	}
	return c.launch(st, *out.Item, *out.Attempt, out.Credential, cfg)
}

func (c *Coordinator) launch(st control.State, item control.Item, at control.Attempt, credential string, cfg *hosts.Config) error {
	if err := c.Service.CheckDispatch(item.ID, at.ID); err != nil {
		return err
	}
	tier := item.Tier
	if tier == "" {
		tier = "standard"
	}
	if item.Kind == "plan" {
		tier = "deep"
	}
	model, err := cfg.Model(at.Harness, hosts.Tier(tier))
	if err != nil {
		return err
	}
	reviewer, err := cfg.Model(at.Harness, hosts.Deep)
	if err != nil {
		return err
	}
	repo := st.Items[item.Repo]
	req := hostops.ControlRequest{URL: c.URL, Credential: credential, Attempt: at.ID, Remote: repo.Remote,
		Spawn: hostops.SpawnRequest{Repo: repo.Slug, Task: control.Number(item.ID), Feature: item.Plan, Mode: hostops.HandsOff,
			Harness: at.Harness, Tier: tier, Model: model.Model, Effort: model.Effort, RunID: at.RunID, Heavy: cfg.Heavy(item.Requires),
			Resume: hadEarlierAttempt(st, item.ID, at.ID), Kickoff: kickoff(item, at, reviewer.Model)}}
	spawned, err := remote.ControlSpawn(c.Client(cfg.Hosts[at.Host]), req)
	var refused *hostops.RefusedError
	if errors.As(err, &refused) {
		// One notice per execution: a retry's reason may differ, and the
		// reused key refuses a second body.
		_ = c.notice(item.ID, "refused/"+at.ID, fmt.Sprintf("Host %s refused to start this execution: %s. "+
			"The coordinator tries again each pass until the host accepts it.", at.Host, refused.Reason))
		return err
	}
	if err != nil {
		_ = c.notice(item.ID, "dispatch/"+at.ID, "Dispatch has an unresolved outcome; its execution stays reserved. "+err.Error())
		return err
	}
	return c.Service.SetAttempt(at.ID, "running", spawned.Window)
}

// hadEarlierAttempt reports whether an attempt for item other than current
// ran on a host: a retry or a resume, whose branch is the task's own. An
// attempt that never opened a window, such as one the host refused, does not
// count, so a stray branch it was refused for is never adopted.
func hadEarlierAttempt(st control.State, item, current string) bool {
	for _, at := range st.Attempts {
		if at.Item == item && at.ID != current && at.Window != "" {
			return true
		}
	}
	return false
}

// releaseEnded releases, before a new execution's first launch, the session
// an ended earlier attempt of item left on the execution's host, as the host
// refuses a window name in use and a worktree left on the task's branch. It
// acts only on a successful read of the host that shows the task's window
// dead or absent, and whose newest run under that name is not this
// execution's own; a live window, or one that cannot be read, is left alone.
// Release keeps a worktree with uncommitted changes, and the branch.
func (c *Coordinator) releaseEnded(st control.State, item control.Item, at control.Attempt, client remote.Client) {
	if !endedEarlier(st, item.ID, at) {
		return
	}
	status, err := client.Status()
	if err != nil || status.RunsError != "" {
		return
	}
	name := windowName(st, item)
	for _, w := range status.Windows {
		if w.Name == name && w.Alive {
			return
		}
	}
	for _, r := range status.Runs {
		if r.Name == name && r.Record.RunID == at.RunID {
			return
		}
	}
	if _, err := client.Release(name, false); err != nil {
		c.log("release the ended session %s on %s: %v", name, at.Host, err)
	}
}

// endedEarlier reports whether an attempt of item other than current ran on
// current's host and has ended.
func endedEarlier(st control.State, item string, current control.Attempt) bool {
	for _, at := range st.Attempts {
		if at.Item == item && at.ID != current.ID && at.Host == current.Host && at.State == "ended" && at.Window != "" {
			return true
		}
	}
	return false
}

// windowName is the task's tmux window name on a host, <repo>-<task>.
func windowName(st control.State, item control.Item) string {
	return fmt.Sprintf("%s-%d", strings.Split(st.Items[item.Repo].Slug, "/")[1], control.Number(item.ID))
}

func kickoff(item control.Item, at control.Attempt, reviewer string) string {
	role := "executor"
	if item.Kind == "plan" {
		role = "planner"
	}
	return fmt.Sprintf("Use the engine skill's server %s mode for %s. Read engine %s show %s and its updates. "+
		"All plan/task state, messages, approvals and repository merge locks use the engine server CLI, never GitHub issues. "+
		"Execution: %s. Reviewer model: %s. The repository's tests, review and pinned-head merge rules apply. "+
		"Read the server plan-source reference before working.", role, item.ID, item.Kind, item.ID, at.ID, reviewer)
}

func (c *Coordinator) reconcileDispatches(root string, fleet run.Fleet, cfg *hosts.Config) error {
	st, err := c.Service.Store.Snapshot()
	if err != nil {
		return err
	}
	if !st.Items[root].Coordinating {
		return nil
	}
	for _, item := range st.Items {
		if item.ID != root && item.Plan != root {
			continue
		}
		at, ok := st.Attempts[item.Attempt]
		if !ok || at.State != "dispatching" {
			continue
		}
		for _, h := range fleet.Hosts {
			if h.Name != at.Host || h.Unreachable != "" || h.Status.RunsError != "" {
				continue
			}
			if err = c.reconcileDispatch(st, item, at, h, cfg); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Coordinator) reconcileDispatch(st control.State, item control.Item, at control.Attempt, h run.HostLoad, cfg *hosts.Config) error {
	for _, r := range h.Status.Runs {
		if r.Record.RunID == at.RunID {
			_, window, _ := strings.Cut(r.Record.Tmux, ":")
			if window == "" {
				continue
			}
			return c.Service.SetAttempt(at.ID, "running", window)
		}
	}
	credentials := st.ReceiptsForDispatch(at.ID)
	if len(credentials) != 1 {
		return fmt.Errorf("dispatch %s has no unique credential receipt", at.ID)
	}
	if err := c.launch(st, item, at, credentials[0], cfg); err != nil {
		c.log("recover dispatch %s: %v", at.ID, err)
	}
	return nil
}

// ConfirmEnded refuses to infer death from an outage or an unfinished dispatch.
func (c *Coordinator) ConfirmEnded(id string) error {
	st, err := c.Service.Store.Snapshot()
	if err != nil {
		return err
	}
	at, ok := st.Attempts[id]
	if !ok {
		return fmt.Errorf("unknown execution")
	}
	if at.State == "ended" {
		return nil
	}
	if at.State == "dispatching" {
		return fmt.Errorf("dispatch has not completed; execution is still reserved")
	}
	cfg, _, err := c.configuration()
	if err != nil {
		return err
	}
	host, ok := cfg.Hosts[at.Host]
	if !ok {
		return fmt.Errorf("execution host is not configured")
	}
	status, err := c.Client(host).Status()
	if err != nil {
		return err
	}
	if status.RunsError != "" {
		return fmt.Errorf("host run records are unreadable")
	}
	name := windowName(st, st.Items[at.Item])
	for _, w := range status.Windows {
		if w.Name == name && w.Alive {
			return fmt.Errorf("execution is still alive")
		}
	}
	for _, r := range status.Runs {
		if r.Record.RunID == at.RunID {
			return nil
		}
	}
	return fmt.Errorf("dispatch outcome is unknown; no matching host run record")
}
