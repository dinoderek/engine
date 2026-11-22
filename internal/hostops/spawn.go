package hostops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

// SpawnRequest is what the caller decided for a session: the task, the
// mode, the harness, the model and the kickoff. The tier, the feature and
// whether the task is heavy are only recorded in run.json.
type SpawnRequest struct {
	control *ControlRequest
	Repo    string // owner/name
	Task    int
	Feature string // owner/repo#n, or empty
	// Title is how the harness apps show the session (session.Title); empty
	// shows its name. It is omitted when empty, like Resume.
	Title string `json:",omitempty"`
	Mode  string
	// Harness names an adapter; empty is the default.
	Harness string
	Tier    string
	Model   string
	Effort  string
	RunID   string
	Kickoff string
	// Heavy is true when the card requires a heavy capability.
	Heavy bool
	// Resume is true when the task had an earlier attempt, so a branch
	// origin already has is the task's own work and the worktree opens on
	// it. It is omitted when false, so a host that predates it still takes
	// every first attempt.
	Resume bool `json:",omitempty"`
}

// Spawned is spawn's reply.
type Spawned struct {
	Name     string `json:"name"`
	Window   string `json:"window"` // tmux's window id, such as @3
	RunDir   string `json:"run_dir"`
	Worktree string `json:"worktree"`
	Branch   string `json:"branch"`
	// Attach is the command that attaches to the window on this host.
	Attach string `json:"attach"`
	// Warning is a problem the session runs despite, such as a host that
	// may sleep.
	Warning string `json:"warning,omitempty"`
	// PaneKey is the public key, in hex, of a server run's pane key.
	PaneKey string `json:"pane_key,omitempty"`
}

// spawn is a request that has passed every check.
type spawn struct {
	SpawnRequest
	repo   session.Repo
	name   string
	runDir string
	cfg    *hosts.Config
	host   hosts.Host
	h      harness.Adapter
	l      harness.Launcher // the session's launch
	bin    string           // the harness binary
	// stale is a leftover run directory with no run.json and no window of
	// the run's name, which Spawn clears before it starts.
	stale bool
}

// Spawn creates the task's worktree in engine's clone, which sync-repo has
// synced, trusts it for the harness, opens the task's tmux window running
// the harness with the kickoff, and writes run.json. It refuses, changing
// nothing, a request it cannot launch, and a name whose window exists. A
// failure before the window opens removes what this call created (the run
// directory, the harness's trust, and the worktree and branch if it made
// them), so the next dispatch starts clean; its error names anything left.
func Spawn(d Deps, r SpawnRequest) (Spawned, error) {
	s, err := checkSpawn(d, r)
	if err != nil {
		return Spawned{}, err
	}
	if s.stale {
		if err := os.RemoveAll(s.runDir); err != nil {
			return Spawned{}, fmt.Errorf("clear the leftover run directory %s: %w", s.runDir, err)
		}
	}
	// Teardown bounds the repo's own release, which a failed spawn's undo runs.
	repos := session.Repos{Run: d.Run, Probe: d.Probe, Teardown: d.Teardown, Home: d.Home, BaseURL: d.BaseURL}
	if s.control != nil {
		repos.Control = true
		repos.RemoteURL = s.control.Remote
	}
	clone, err := repos.Open(s.repo)
	if err != nil {
		return Spawned{}, refuse("%v", err)
	}
	u := &undo{repos: repos, clone: clone, runDir: s.runDir, l: s.l, removeAll: os.RemoveAll}
	wt, err := repos.CreateWorktree(clone, s.Task, s.Resume, s.RunID)
	u.wt = wt
	var taken *session.BranchTakenError
	if errors.As(err, &taken) {
		return Spawned{}, refuse("%v", err)
	}
	if err != nil {
		return Spawned{}, u.run(err)
	}
	out, err := s.open(d, clone, wt, u)
	if err != nil {
		return Spawned{}, u.run(err)
	}
	return out, nil
}

// open trusts the worktree, writes the run's files and opens its window.
// Once the window is open u has nothing to undo.
func (s spawn) open(d Deps, clone session.Clone, wt session.Worktree, u *undo) (Spawned, error) {
	line, err := s.cfg.LaunchLine(s.host, hosts.Launch{
		Harness: s.h.Name(), Name: s.name, Title: s.Title, Clone: clone.Path, Home: d.Home,
		Model: hosts.TierModel{Model: s.Model, Effort: s.Effort},
	})
	if err != nil {
		return Spawned{}, err
	}
	path, err := s.host.PathPrefix(d.Home)
	if err != nil {
		return Spawned{}, err
	}
	joined, joinWarning := s.join(d, line)
	if err := s.l.Trust(wt.Path, clone.Path, joined); err != nil {
		return Spawned{}, err
	}
	u.trusted = true
	log := filepath.Join(s.runDir, session.PaneLog)
	// A joined session's hooks find their run by its worktree in run.json, so
	// it is there before the session starts; the window is added once open.
	if joined {
		if err := session.WriteRecord(s.runDir, s.record(wt, "", log, d)); err != nil {
			return Spawned{}, fmt.Errorf("write run.json: %w", err)
		}
	}
	paneKey := ""
	if s.control != nil {
		if err := writeControl(s.runDir, *s.control); err != nil {
			return Spawned{}, err
		}
		if paneKey, err = session.NewPaneKey(s.runDir); err != nil {
			return Spawned{}, err
		}
	}
	kickoff := adoptedKickoff(s.Kickoff, wt)
	if err := logTyped(s.runDir, kickoff); err != nil {
		return Spawned{}, err
	}
	command := launchCommand(s.h.Name(), s.l, launch{line: line, dir: wt.Path, runDir: s.runDir, mode: s.Mode, kickoff: kickoff, joined: joined, control: s.control, path: path})
	opened, err := d.Tmux.Open(session.Window{Name: s.name, Dir: wt.Path, Command: command, Log: log})
	if err != nil {
		return Spawned{}, fmt.Errorf("open the tmux window: %w", err)
	}
	u.opened = true
	if err := session.WriteRecord(s.runDir, s.record(wt, opened.ID, log, d)); err != nil {
		return Spawned{}, fmt.Errorf("the session is running in %s:%s, but run.json was not written: %w", session.Name, opened.ID, err)
	}
	out := Spawned{Name: s.name, Window: opened.ID, RunDir: s.runDir, Worktree: wt.Path, Branch: wt.Branch, PaneKey: paneKey,
		Attach: "tmux attach -t '=" + session.Name + ":" + opened.ID + "'"}
	var warnings []string
	if opened.AwakeErr != nil {
		warnings = append(warnings, "the host may sleep while the session runs: "+opened.AwakeErr.Error())
	}
	if joinWarning != "" {
		warnings = append(warnings, joinWarning)
	}
	out.Warning = strings.Join(warnings, "; ")
	return out, nil
}

// adoptedKickoff is the kickoff, naming the commit to resume from when the
// worktree opened on a branch an earlier attempt left.
func adoptedKickoff(kickoff string, wt session.Worktree) string {
	if !wt.Adopted || wt.Head == "" {
		return kickoff
	}
	out := kickoff + " The task's branch " + wt.Branch + " is at " + wt.Head + ", where an earlier attempt left it: resume from that commit, " +
		"and keep any change it left uncommitted in the worktree."
	if wt.StaleLock {
		out += " A stale .git/index.lock, which no running process held, was removed before resuming."
	}
	return out
}

// undo is what one Spawn created before its window opened, so a failure
// removes it and the next dispatch of the run starts clean.
type undo struct {
	repos   session.Repos
	clone   session.Clone
	wt      session.Worktree // removed only as far as wt says this call made it
	runDir  string           // checkRequest saw it absent, so it is this call's
	l       harness.Launcher
	trusted bool
	opened  bool // the window is open: the session runs, so nothing is undone
	// removeAll removes the run directory: os.RemoveAll.
	removeAll func(string) error
}

// run removes what u holds and returns cause, naming each leftover it could
// not remove with the command that removes it.
func (u *undo) run(cause error) error {
	if u.opened {
		return cause
	}
	var left []string
	if u.trusted {
		if err := u.l.Release(u.wt.Path); err != nil {
			left = append(left, "the harness's trust for "+u.wt.Path+": "+err.Error())
		}
	}
	if err := u.repos.RemoveWorktree(u.clone, u.wt); err != nil {
		advice := "git -C " + hosts.ShellWord(u.clone.Path) + " worktree remove --force " + hosts.ShellWord(u.wt.Path)
		what := "the worktree " + u.wt.Path
		if u.wt.NewBranch {
			advice += " && git -C " + hosts.ShellWord(u.clone.Path) + " branch -D " + u.wt.Branch
			what += " and branch " + u.wt.Branch
		}
		left = append(left, fmt.Sprintf("%s (%v); remove it with %s", what, err, advice))
	}
	if err := u.removeAll(u.runDir); err != nil {
		left = append(left, fmt.Sprintf("the run directory %s (%v); remove it with rm -rf %s", u.runDir, err, hosts.ShellWord(u.runDir)))
	}
	if len(left) == 0 {
		return cause
	}
	// On its own line, after the cause, which may end in tmux's pane list.
	return fmt.Errorf("%w\nleft in place, to remove before starting again: %s", cause, strings.Join(left, "; "))
}

// join asks the harness whether the session joins the host's remote
// control, when the caller lets sessions join. It only reads.
func (s spawn) join(d Deps, line string) (bool, string) {
	if !d.JoinRemote {
		return false, ""
	}
	return s.l.Join(s.bin, line)
}

// checkSpawn runs spawn's refusals. It only reads files and tmux's window
// list; a leftover run directory it accepts is cleared by Spawn.
func checkSpawn(d Deps, r SpawnRequest) (spawn, error) {
	s := spawn{SpawnRequest: r}
	if err := s.checkRequest(d.Home); err != nil {
		return spawn{}, err
	}
	var err error
	if s.h, err = adapterFor(d, s.Harness); err != nil {
		return spawn{}, err
	}
	s.l = s.h.Session(s.name)
	if s.cfg, s.host, err = openHost(d.Home); err != nil {
		return spawn{}, err
	}
	if s.bin, err = checkHarness(s.host, s.h.Name(), d.Home); err != nil {
		return spawn{}, err
	}
	taken, err := d.Tmux.HasWindow(s.name)
	if err != nil {
		return spawn{}, refuse("cannot list the tmux windows: %v", err)
	}
	if taken {
		return spawn{}, refuse("the tmux session %s already has a window %s", session.Name, s.name)
	}
	return s, nil
}

// checkRequest refuses a request that is incomplete or malformed.
func (s *spawn) checkRequest(home string) error {
	var err error
	if s.repo, err = session.ParseRepo(s.Repo); err != nil {
		return refuse("%v", err)
	}
	var problems []string
	if s.Task < 1 {
		problems = append(problems, fmt.Sprintf("task number must be positive, got %d", s.Task))
	}
	if s.Mode != HandsOff && s.Mode != Gated {
		problems = append(problems, fmt.Sprintf("unknown mode %q: want %s or %s", s.Mode, HandsOff, Gated))
	}
	if s.Model == "" || s.Effort == "" {
		problems = append(problems, "a model and an effort are required")
	}
	if strings.TrimSpace(s.Kickoff) == "" {
		problems = append(problems, "the kickoff is empty")
	}
	if s.Tier != "" {
		if _, err := hosts.ParseTier(s.Tier); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if s.Feature != "" && s.control == nil {
		if _, err := session.ParseRef(s.Feature); err != nil {
			problems = append(problems, "feature: "+err.Error())
		}
	}
	s.name = session.TaskName(s.repo, s.Task)
	s.Title = session.CleanTitle(s.Title)
	if s.runDir, err = session.RunDir(home, s.RunID); err != nil {
		problems = append(problems, err.Error())
	} else if !strings.HasPrefix(s.RunID, s.name+"-") {
		problems = append(problems, fmt.Sprintf("run id %s is not a run of %s", s.RunID, s.name))
	} else if _, err := os.Stat(filepath.Join(s.runDir, session.RecordFile)); err == nil {
		problems = append(problems, "run "+s.RunID+" already has a run record, "+filepath.Join(s.runDir, session.RecordFile))
	} else if _, err := os.Stat(s.runDir); err == nil {
		// A dispatch that died before its window opened left it; checkSpawn
		// refuses it still when a window has the run's name.
		s.stale = true
	}
	if len(problems) > 0 {
		return refuse("%s", strings.Join(problems, "; "))
	}
	return nil
}

func (s spawn) record(wt session.Worktree, window, log string, d Deps) session.Record {
	return session.Record{
		Control: s.control != nil,
		Schema:  session.RecordSchema, RunID: s.RunID, Repo: s.repo.String(),
		Task: session.Ref{Repo: s.repo.String(), Number: s.Task}.String(), Feature: s.Feature,
		Host: s.host.Name, Harness: s.h.Name(), Tier: s.Tier, Model: s.Model, Effort: s.Effort,
		Mode: s.Mode, Heavy: s.Heavy, Name: s.name, Title: s.Title, Tmux: session.Name + ":" + window,
		Branch: wt.Branch, Worktree: wt.Path, PaneLog: log, Spawned: d.Now().UTC(),
	}
}
