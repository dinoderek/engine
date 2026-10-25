package hostops

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

// tmpReads lets an executor read back what it or its tests wrote to /tmp,
// such as screenshots, without a prompt. macOS resolves /tmp to /private/tmp.
var tmpReads = []string{"Read(//tmp/**)", "Read(//private/tmp/**)"}

// SpawnRequest is what the caller decided for a session: the task, the
// mode, the model and the kickoff. The tier and the feature are only
// recorded in run.json.
type SpawnRequest struct {
	Repo    string // owner/name
	Task    int
	Feature string // owner/repo#n, or empty
	Mode    string
	Tier    string
	Model   string
	Effort  string
	RunID   string
	Kickoff string
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
}

// spawn is a request that has passed every check.
type spawn struct {
	SpawnRequest
	repo   session.Repo
	name   string
	runDir string
	token  string
	cfg    *hosts.Config
	host   hosts.Host
}

// Spawn creates the task's worktree in engine's clone, which sync-repo has
// synced, trusts it for Claude Code, opens the task's tmux window running
// the harness with the kickoff, and writes run.json. It refuses, changing
// nothing, a request it cannot launch, and a name whose window exists.
func Spawn(d Deps, r SpawnRequest) (Spawned, error) {
	s, err := checkSpawn(d, r)
	if err != nil {
		return Spawned{}, err
	}
	repos := session.Repos{Run: d.Run, Home: d.Home, Token: s.token, BaseURL: d.BaseURL}
	clone, err := repos.Open(s.repo)
	if err != nil {
		return Spawned{}, refuse("%v", err)
	}
	wt, err := repos.CreateWorktree(clone, s.Task)
	var taken *session.BranchTakenError
	if errors.As(err, &taken) {
		return Spawned{}, refuse("%v", err)
	}
	if err != nil {
		return Spawned{}, err
	}
	left := fmt.Sprintf("the worktree is left in place; before starting again, remove it with "+
		"git -C %s worktree remove --force %s && git -C %s branch -D %s",
		hosts.ShellWord(clone.Path), hosts.ShellWord(wt.Path), hosts.ShellWord(clone.Path), wt.Branch)
	if err := session.TrustFolder(session.ClaudeConfig(d.Getenv, d.Home), wt.Path); err != nil {
		return Spawned{}, fmt.Errorf("trust %s for Claude Code: %w; %s", wt.Path, err, left)
	}
	line, err := s.cfg.LaunchLine(s.host, hosts.Launch{
		Harness: Harness, Name: s.name, Clone: clone.Path, Home: d.Home,
		Model: hosts.TierModel{Model: s.Model, Effort: s.Effort},
	})
	if err != nil {
		return Spawned{}, fmt.Errorf("%w; %s", err, left)
	}
	log := filepath.Join(s.runDir, session.PaneLog)
	command := launchCommand(d, line, s.runDir, s.Mode, s.Kickoff)
	opened, err := d.Tmux.Open(session.Window{Name: s.name, Dir: wt.Path, Command: command, Log: log})
	if err != nil {
		return Spawned{}, fmt.Errorf("open the tmux window: %w; %s", err, left)
	}
	if err := session.WriteRecord(s.runDir, s.record(wt, opened.ID, log, d)); err != nil {
		return Spawned{}, fmt.Errorf("the session is running in %s:%s, but run.json was not written: %w", session.Name, opened.ID, err)
	}
	out := Spawned{Name: s.name, Window: opened.ID, RunDir: s.runDir, Worktree: wt.Path, Branch: wt.Branch,
		Attach: "tmux attach -t '=" + session.Name + ":" + opened.ID + "'"}
	if opened.AwakeErr != nil {
		out.Warning = "the host may sleep while the session runs: " + opened.AwakeErr.Error()
	}
	return out, nil
}

// checkSpawn runs spawn's refusals. It only reads files and tmux's window
// list.
func checkSpawn(d Deps, r SpawnRequest) (spawn, error) {
	s := spawn{SpawnRequest: r}
	if err := s.checkRequest(d.Home); err != nil {
		return spawn{}, err
	}
	var err error
	if s.token, err = readToken(d.Home); err != nil {
		return spawn{}, err
	}
	if s.cfg, s.host, err = openHost(d.Home); err != nil {
		return spawn{}, err
	}
	if !s.host.Runs(Harness) {
		return spawn{}, refuse("host %s does not run %s", s.host.Name, Harness)
	}
	if err := checkBin(s.host, d.Home); err != nil {
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
	if s.Feature != "" {
		if _, err := tracker.ParseRef(s.Feature); err != nil {
			problems = append(problems, "feature: "+err.Error())
		}
	}
	if s.runDir, err = session.RunDir(home, s.RunID); err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		return refuse("%s", strings.Join(problems, "; "))
	}
	s.name = session.TaskName(s.repo, s.Task)
	return nil
}

func (s spawn) record(wt session.Worktree, window, log string, d Deps) session.Record {
	return session.Record{
		Schema: session.RecordSchema, RunID: s.RunID, Repo: s.repo.String(),
		Task: tracker.Ref{Repo: s.repo.String(), Number: s.Task}.String(), Feature: s.Feature,
		Host: s.host.Name, Harness: Harness, Tier: s.Tier, Model: s.Model, Effort: s.Effort,
		Mode: s.Mode, Name: s.name, Tmux: session.Name + ":" + window,
		Branch: wt.Branch, Worktree: wt.Path, PaneLog: log, Spawned: d.Now().UTC(),
	}
}

// launchCommand is a window's shell command. It runs under /bin/sh, whatever
// tmux's default shell is: export the machine user's token from its file, so
// it never appears on a command line, the run directory and, hands-off, the
// unattended mark, and the CLAUDE_CONFIG_DIR engine trusted the folder in,
// then run the launch line. Claude's file tools ask before reading outside
// the worktree, which would stop an unattended executor, so it adds the
// engine plugin's directory with --add-dir and allows reads in /tmp with
// --allowedTools. /tmp only gets a read rule: Claude loads skills from every
// --add-dir directory, and anyone on the host can write to /tmp. The kickoff
// comes last, after --, because both options take every argument up to the
// next option.
func launchCommand(d Deps, line, runDir, mode, kickoff string) string {
	// An empty token would make gh fall back to the operator's own login.
	script := fmt.Sprintf(`GH_TOKEN="$(cat %s)" && [ -n "$GH_TOKEN" ] && export GH_TOKEN && `, hosts.ShellWord(tokenFile(d.Home)))
	// The permission hook logs to the run directory and, unattended, denies.
	script += "export " + session.RunDirVar + "=" + hosts.ShellWord(runDir) + " && "
	if mode == HandsOff {
		script += "export " + session.UnattendedVar + "=1 && "
	} else {
		script += "unset " + session.UnattendedVar + " && "
	}
	// The tmux server's environment may differ from the caller's.
	if dir := d.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		script += "export CLAUDE_CONFIG_DIR=" + hosts.ShellWord(dir) + " && "
	} else {
		script += "unset CLAUDE_CONFIG_DIR && "
	}
	plugin := session.PluginDir(d.Getenv, d.Home)
	script += "exec " + line + " --add-dir " + hosts.ShellWord(plugin) + " --allowedTools"
	for _, rule := range tmpReads {
		script += " " + hosts.ShellWord(rule)
	}
	script += " -- " + hosts.ShellWord(kickoff)
	return "exec /bin/sh -c " + hosts.ShellWord(script)
}
