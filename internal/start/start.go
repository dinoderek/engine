// Package start is `engine start`: it launches one executor session for a
// task on this machine. It checks everything it can before it changes
// anything, so a refusal leaves the host and the tracker as they were. Then it
// syncs engine's clone, creates the task worktree, opens the task's tmux
// window running the harness with the kickoff prompt, and writes run.json.
package start

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

// The executor's modes. The kickoff names one.
const (
	HandsOff = "hands-off"
	Gated    = "gated"
)

// Harness is the only harness engine start launches.
const Harness = "claude"

// stateLabels are the task states that mean the task is taken or finished
// badly: a task carrying one is not started again.
var stateLabels = append(slices.Clone(tracker.ClaimLabels), "engine:failed")

// approved is the label a standalone task needs before it runs.
const approved = "engine:approved"

// Options are what the operator asks for.
type Options struct {
	Ref tracker.Ref
	// Tier replaces the card's tier when set.
	Tier string
	// Mode is HandsOff or Gated; empty means HandsOff.
	Mode string
}

// Deps are the outside world: the CLIs, the home directory, the environment
// and the clock. Tests replace each one.
type Deps struct {
	// GH runs gh for the tracker reads.
	GH tracker.Runner
	// Run runs git and gh for engine's clone and the worktree.
	Run  session.RunFunc
	Tmux *session.Tmux
	Home string
	// Getenv reads CLAUDE_CONFIG_DIR.
	Getenv func(string) string
	Now    func() time.Time
	// BaseURL is where repos are cloned from; GitHub when empty.
	BaseURL string
}

// RefusedError is a check that failed before engine start changed anything.
type RefusedError struct{ Reason string }

func (e *RefusedError) Error() string { return e.Reason + "; nothing was changed" }

func refuse(format string, args ...any) error {
	return &RefusedError{Reason: fmt.Sprintf(format, args...)}
}

// plan is a start that has passed every check.
type plan struct {
	opts     Options
	token    string
	cfg      *hosts.Config
	host     hosts.Host
	task     tracker.Task
	repo     session.Repo
	name     string
	tier     hosts.Tier
	model    hosts.TierModel
	reviewer string
}

// Started is a session engine start launched.
type Started struct {
	Task     tracker.Ref
	Name     string
	RunID    string
	RunDir   string
	Worktree session.Worktree
	Window   session.WindowInfo
	// AwakeErr is why the host could not be kept awake; the session runs.
	AwakeErr error
}

// Run starts the task's session and prints how to reach it.
func Run(o Options, d Deps, out io.Writer) error {
	p, err := check(o, d)
	if err != nil {
		return err
	}
	s, err := p.launch(d)
	if err != nil {
		return err
	}
	report(out, s)
	return nil
}

// check runs every refusal, in the order a fix would be made, and changes
// nothing: it only reads files, the tracker and tmux's window list.
func check(o Options, d Deps) (plan, error) {
	p := plan{opts: o}
	if err := p.checkOptions(); err != nil {
		return plan{}, err
	}
	if err := p.loadHost(d); err != nil {
		return plan{}, err
	}
	task, err := tracker.ReadTask(d.GH, p.token, o.Ref)
	if err != nil {
		return plan{}, err
	}
	p.task = task
	if err := checkTask(task); err != nil {
		return plan{}, err
	}
	for _, step := range []func(Deps) error{p.pickModel, p.checkBin, p.checkTmux} {
		if err := step(d); err != nil {
			return plan{}, err
		}
	}
	return p, nil
}

func (p *plan) checkOptions() error {
	switch p.opts.Mode {
	case "":
		p.opts.Mode = HandsOff
	case HandsOff, Gated:
	default:
		return refuse("unknown mode %q: want %s or %s", p.opts.Mode, HandsOff, Gated)
	}
	if p.opts.Tier != "" {
		if _, err := hosts.ParseTier(p.opts.Tier); err != nil {
			return refuse("%v", err)
		}
	}
	repo, err := session.ParseRepo(p.opts.Ref.Repo)
	if err != nil {
		return refuse("%v", err)
	}
	p.repo, p.name = repo, session.TaskName(repo, p.opts.Ref.Number)
	return nil
}

// loadHost reads the token and the host config. The token comes from the
// token file only, never GH_TOKEN, because the session reads that file
// itself: engine's clone and the session then act as one machine user.
func (p *plan) loadHost(d Deps) error {
	token, err := tracker.Token(func(string) string { return "" }, d.Home)
	if err != nil {
		return refuse("the session reads the machine user's token from %s: %v", tokenFile(d.Home), err)
	}
	cfg, host, err := hosts.Open(hosts.ConfigDir(d.Home))
	if err != nil {
		return refuse("%v", err)
	}
	p.token, p.cfg, p.host = token, cfg, host
	return nil
}

func tokenFile(home string) string { return filepath.Join(home, filepath.FromSlash(tracker.TokenFile)) }

// checkTask refuses a task that is closed, taken, failed, blocked, or a
// standalone task the operator has not approved.
func checkTask(t tracker.Task) error {
	if !t.Open {
		return refuse("%s is closed", t.Ref)
	}
	for _, label := range stateLabels {
		if t.Has(label) {
			return refuse("%s is %s: another session holds it or it needs the operator", t.Ref, label)
		}
	}
	if t.Parent == nil && !t.Has(approved) {
		return refuse("%s is a standalone task without %s", t.Ref, approved)
	}
	if len(t.OpenBlockers) > 0 {
		names := make([]string, len(t.OpenBlockers))
		for i, b := range t.OpenBlockers {
			names[i] = b.String()
		}
		return refuse("%s is blocked by open %s", t.Ref, strings.Join(names, ", "))
	}
	return nil
}

// pickModel settles the harness and the tier: the card's, unless the
// operator named a tier.
func (p *plan) pickModel(Deps) error {
	harness := p.task.Harness
	if harness == "" {
		harness = p.cfg.DefaultHarness
	}
	if _, ok := p.cfg.Harnesses[harness]; !ok {
		return refuse("%s names harness %q, which hosts.toml does not define (a misspelt tier? tiers are deep, standard and light)", p.task.Ref, harness)
	}
	if harness != Harness {
		return refuse("%s runs on %s; engine start launches %s only", p.task.Ref, harness, Harness)
	}
	if !p.host.Runs(harness) {
		return refuse("host %s does not run %s", p.host.Name, harness)
	}
	tierName := p.task.Tier
	if p.opts.Tier != "" {
		tierName = p.opts.Tier
	}
	tier, err := hosts.ParseTier(tierName)
	if err != nil {
		return refuse("%s: %v", p.task.Ref, err)
	}
	model, err := p.cfg.Model(harness, tier)
	if err != nil {
		return refuse("%v", err)
	}
	reviewer, err := p.cfg.Model(harness, hosts.Deep)
	if err != nil {
		return refuse("%v", err)
	}
	p.tier, p.model, p.reviewer = tier, model, reviewer.Model
	return nil
}

func (p *plan) checkBin(d Deps) error {
	bin, err := p.host.BinPath(Harness, d.Home)
	if err != nil {
		return refuse("%v", err)
	}
	if info, err := os.Stat(bin); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return refuse("%s is not an executable %s binary (hosts.toml, host %s, bin)", bin, Harness, p.host.Name)
	}
	return nil
}

// checkTmux refuses a task that already has a window, and a host whose slots
// are full. A window whose command has exited holds no slot.
func (p *plan) checkTmux(d Deps) error {
	windows, err := d.Tmux.Windows()
	if err != nil {
		return refuse("cannot list the tmux windows: %v", err)
	}
	live := 0
	for _, w := range windows {
		if w.Name == p.name {
			return refuse("the tmux session %s already has a window %s (%s)", session.Name, p.name, w.ID)
		}
		if !w.Dead {
			live++
		}
	}
	if slots := p.host.Slots(); live >= slots {
		return refuse("host %s has no free slot: %d sessions running, %d slots", p.host.Name, live, slots)
	}
	return nil
}

// Kickoff is the first message of the session: the executor's kickoff line,
// plus the values its claim comment records.
func Kickoff(task tracker.Ref, mode, reviewer, runID, name string) string {
	return fmt.Sprintf("Execute %s with the engine executor, %s mode. Reviewer model: %s. run=%s tmux=%s:%s rc=%s",
		task, mode, reviewer, runID, session.Name, name, name)
}

// launch does the work: clone, worktree, trust, window, record.
func (p plan) launch(d Deps) (Started, error) {
	repos := session.Repos{Run: d.Run, Home: d.Home, Token: p.token, BaseURL: d.BaseURL}
	clone, err := repos.Sync(p.repo)
	if err != nil {
		return Started{}, err
	}
	wt, err := repos.CreateWorktree(clone, p.opts.Ref.Number)
	if err != nil {
		return Started{}, err
	}
	now := d.Now() // the run ID and run.json's spawn time are one moment
	s := Started{Task: p.opts.Ref, Name: p.name, Worktree: wt, RunID: session.RunID(p.repo, p.opts.Ref.Number, now)}
	left := fmt.Sprintf("the worktree is left in place; before starting again, remove it with "+
		"git -C %s worktree remove --force %s && git -C %s branch -D %s",
		hosts.ShellWord(clone.Path), hosts.ShellWord(wt.Path), hosts.ShellWord(clone.Path), wt.Branch)
	if err := session.TrustFolder(session.ClaudeConfig(d.Getenv, d.Home), wt.Path); err != nil {
		return Started{}, fmt.Errorf("trust %s for Claude Code: %w; %s", wt.Path, err, left)
	}
	if s.RunDir, err = session.RunDir(d.Home, s.RunID); err != nil {
		return Started{}, fmt.Errorf("%w; %s", err, left)
	}
	command, err := p.command(d, clone, s.RunID)
	if err != nil {
		return Started{}, fmt.Errorf("%w; %s", err, left)
	}
	log := filepath.Join(s.RunDir, session.PaneLog)
	opened, err := d.Tmux.Open(session.Window{Name: p.name, Dir: wt.Path, Command: command, Log: log})
	if err != nil {
		return Started{}, fmt.Errorf("open the tmux window: %w; %s", err, left)
	}
	s.Window, s.AwakeErr = opened.WindowInfo, opened.AwakeErr
	if err := session.WriteRecord(s.RunDir, p.record(s, log, now)); err != nil {
		return Started{}, fmt.Errorf("the session is running in %s:%s, but run.json was not written: %w", session.Name, s.Window.ID, err)
	}
	return s, nil
}

// command is the window's shell command. It runs under /bin/sh, whatever
// tmux's default shell is: export the machine user's token from its file, so
// it never appears on a command line, and the CLAUDE_CONFIG_DIR engine trusted
// the worktree in, then run the launch line with the kickoff as its last
// argument.
func (p plan) command(d Deps, clone session.Clone, runID string) (string, error) {
	line, err := p.cfg.LaunchLine(p.host, hosts.Launch{
		Harness: Harness, Tier: p.tier, Name: p.name, Clone: clone.Path, Home: d.Home,
	})
	if err != nil {
		return "", err
	}
	kickoff := Kickoff(p.opts.Ref, p.opts.Mode, p.reviewer, runID, p.name)
	// An empty token would make gh fall back to the operator's own login.
	script := fmt.Sprintf(`GH_TOKEN="$(cat %s)" && [ -n "$GH_TOKEN" ] && export GH_TOKEN && `, hosts.ShellWord(tokenFile(d.Home)))
	// The tmux server's environment may differ from engine start's.
	if dir := d.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		script += "export CLAUDE_CONFIG_DIR=" + hosts.ShellWord(dir) + " && "
	} else {
		script += "unset CLAUDE_CONFIG_DIR && "
	}
	script += "exec " + line + " " + hosts.ShellWord(kickoff)
	return "exec /bin/sh -c " + hosts.ShellWord(script), nil
}

func (p plan) record(s Started, log string, now time.Time) session.Record {
	rec := session.Record{
		Schema: session.RecordSchema, RunID: s.RunID, Repo: p.repo.String(), Task: p.opts.Ref.String(),
		Host: p.host.Name, Harness: Harness, Tier: string(p.tier), Model: p.model.Model, Effort: p.model.Effort,
		Mode: p.opts.Mode, Name: p.name, Tmux: session.Name + ":" + s.Window.ID,
		Branch: s.Worktree.Branch, Worktree: s.Worktree.Path, PaneLog: log, Spawned: now.UTC(),
	}
	if p.task.Parent != nil {
		rec.Feature = p.task.Parent.String()
	}
	return rec
}

func report(out io.Writer, s Started) {
	target := session.Name + ":" + s.Window.ID
	fmt.Fprintf(out, "Started %s as %s.\n", s.Task, s.Name)
	fmt.Fprintf(out, "  run             %s\n", s.RunID)
	fmt.Fprintf(out, "  run record      %s\n", filepath.Join(s.RunDir, session.RecordFile))
	fmt.Fprintf(out, "  worktree        %s (branch %s)\n", s.Worktree.Path, s.Worktree.Branch)
	fmt.Fprintf(out, "  tmux window     %s (%s)\n", target, s.Name)
	fmt.Fprintf(out, "  Remote Control  %s, in the Claude app and at claude.ai/code\n", s.Name)
	fmt.Fprintf(out, "Attach: engine attach %s (once the executor has posted its claim)\n", s.Name)
	fmt.Fprintf(out, "    or: tmux attach -t '=%s'\n", target)
	if s.AwakeErr != nil {
		fmt.Fprintf(out, "Warning: the host may sleep while the session runs: %v\n", s.AwakeErr)
	}
}
