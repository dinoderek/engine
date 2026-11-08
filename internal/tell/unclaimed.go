package tell

import (
	"errors"
	"fmt"
	"strings"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

// needsHuman is the label of a task waiting on the operator, whose session
// only the operator answers.
const needsHuman = "engine:needs-human"

// unclaimed tells a session that has not claimed its task yet, such as one
// engine run started that stopped before its claim. The newest run record
// named name, across the hosts in hosts.toml, names the task, the host and
// the window. The task's issue must be open and not waiting on the operator,
// name must be the task's session name, and the record's window must be a
// live window of that name on its host. Every check runs before the comment
// is posted, so a refusal posts nothing.
func unclaimed(name, text string, d Deps) (Told, error) {
	cfg, local, err := hosts.Open(hosts.ConfigDir(d.Home))
	if err != nil {
		return Told{}, fmt.Errorf("no claimed task named %s, and %w", name, err)
	}
	rec, host, err := newestRun(name, cfg, local, d)
	if err != nil {
		return Told{}, err
	}
	ref, err := checkTask(name, rec, d)
	if err != nil {
		return Told{}, err
	}
	tm, err := d.tmuxOn(host, cfg, local)
	if err != nil {
		return Told{}, err
	}
	sess, id, ok := strings.Cut(rec.Tmux, ":")
	if !ok || sess != session.Name || id == "" {
		return Told{}, fmt.Errorf("the run %s of %s names window %q, not one in the session %s", rec.RunID, name, rec.Tmux, session.Name)
	}
	live, err := tm.LiveWindow(id, name)
	if err != nil {
		return Told{}, fmt.Errorf("listing the windows on %s failed: %w", host, err)
	}
	if !live {
		return Told{}, fmt.Errorf("%s has no claim, and its run %s names window %s on %s, which is not a live window named %s", name, rec.RunID, id, host, name)
	}
	told := Told{Task: ref, Host: host}
	if told.URL, err = tracker.Comment(d.Gh, ref, text); err != nil {
		return told, err
	}
	if err := tm.Type(id, Pointer(told.URL)); err != nil {
		return told, fmt.Errorf("commented at %s, but typing into the window on %s failed: %w", told.URL, host, err)
	}
	told.Window = session.Name + ":" + id
	return told, nil
}

// newestRun is the record of the newest run named name on any host in
// hosts.toml, and its host. A record that names another host is not
// trusted. Hosts whose status or run records cannot be read are named when
// no run is found.
func newestRun(name string, cfg *hosts.Config, local hosts.Host, d Deps) (session.Record, string, error) {
	if d.Client == nil {
		return session.Record{}, "", fmt.Errorf("no claimed task named %s", name)
	}
	var best session.Record
	var host string
	var unread []error
	for _, n := range cfg.HostNames() {
		h := cfg.Hosts[n]
		st, err := d.Client(h, n != local.Name).Status()
		if err != nil {
			unread = append(unread, fmt.Errorf("%s: %w", n, err))
			continue
		}
		if st.RunsError != "" {
			unread = append(unread, fmt.Errorf("%s: run records: %s", n, st.RunsError))
		}
		for _, r := range st.Runs {
			if r.Name == name && r.Record.Name == name && r.Record.Host == n && (host == "" || r.Record.Spawned.After(best.Spawned)) {
				best, host = r.Record, n
			}
		}
	}
	if host == "" {
		err := fmt.Errorf("no claimed task or engine run named %s", name)
		if len(unread) > 0 {
			err = fmt.Errorf("%w; hosts not read: %w", err, errors.Join(unread...))
		}
		return session.Record{}, "", err
	}
	return best, host, nil
}

// checkTask is the task the run record names, refused unless its issue is
// open, not waiting on the operator, and name is its session name.
func checkTask(name string, rec session.Record, d Deps) (tracker.Ref, error) {
	ref, err := tracker.ParseRef(rec.Task)
	if err != nil {
		return tracker.Ref{}, fmt.Errorf("the run %s names task %q: %w", rec.RunID, rec.Task, err)
	}
	repo, err := session.ParseRepo(ref.Repo)
	if err != nil {
		return tracker.Ref{}, fmt.Errorf("the run %s names task %s: %w", rec.RunID, ref, err)
	}
	if want := session.TaskName(repo, ref.Number); want != name {
		return tracker.Ref{}, fmt.Errorf("the run %s is of %s, whose session is %s, not %s", rec.RunID, ref, want, name)
	}
	task, err := tracker.ReadTask(d.Gh, ref)
	switch {
	case err != nil:
		return tracker.Ref{}, err
	case !task.Open:
		return tracker.Ref{}, fmt.Errorf("%s has no claim and %s is closed", name, ref)
	case task.Has(needsHuman):
		return tracker.Ref{}, fmt.Errorf("%s has no claim and %s is %s: the operator answers it", name, ref, needsHuman)
	}
	return ref, nil
}
