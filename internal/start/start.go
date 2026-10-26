// Package start is `engine start`: it launches one executor session for a
// task. It checks everything it can before it changes anything, so a refusal
// leaves the host and the tracker as they were. It decides the model, the
// effort and the kickoff, then has the host sync engine's clone and spawn the
// session (internal/hostops), through a remote.Client: this host's, in
// process, unless Deps names another.
package start

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

// The executor's modes. The kickoff names one.
const (
	HandsOff = hostops.HandsOff
	Gated    = hostops.Gated
)

// Harness is the only harness engine start launches.
const Harness = hostops.Claude

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
	// Host names the machine to start on, from hosts.toml; empty is this host.
	Host string
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
	// Sleep waits between the check's polls; nil is time.Sleep.
	Sleep func(time.Duration)
	// Exec runs the host's own CLIs, such as claude agents.
	Exec session.Runner
	// Client is the host the session starts on, for tests; nil is this host,
	// in process, through the fields above, or the named one over SSH.
	Client remote.Client
	// SSH runs ssh to reach a named host; nil starts real processes.
	SSH remote.Exec
}

// HostClient is the host the session starts on: host, reached over SSH when
// remote is set, else this host, in process.
func (d Deps) HostClient(host hosts.Host, remoteHost bool) remote.Client {
	switch {
	case d.Client != nil:
		return d.Client
	case remoteHost:
		run := d.SSH
		if run == nil {
			run = remote.ExecCommand
		}
		return remote.SSH{Name: host.Name, Address: host.Address, Exec: run}
	}
	return remote.Local{Deps: hostops.Deps{
		Run: d.Run, Exec: d.Exec, Tmux: d.Tmux, Home: d.Home, Getenv: d.Getenv, Now: d.Now, BaseURL: d.BaseURL, Sleep: d.Sleep,
	}}
}

// pickHost returns the machine named name, and whether it is another one.
// An empty name, or this host's own, is this host.
func pickHost(cfg *hosts.Config, local hosts.Host, name string) (hosts.Host, bool, error) {
	if name == "" || name == local.Name {
		return local, false, nil
	}
	host, ok := cfg.Hosts[name]
	if !ok {
		return hosts.Host{}, false, refuse("host %q is not in hosts.toml; its machines are %s", name, strings.Join(cfg.HostNames(), ", "))
	}
	return host, true, nil
}

// RefusedError is a check that failed before engine start changed anything.
type RefusedError struct{ Reason string }

func (e *RefusedError) Error() string { return e.Reason + "; nothing was changed" }

func refuse(format string, args ...any) error {
	return &RefusedError{Reason: fmt.Sprintf(format, args...)}
}

// plan is a start that has passed every check.
type plan struct {
	opts  Options
	token string
	cfg   *hosts.Config
	host  hosts.Host
	// remote is true when host is another machine, reached over SSH.
	remote   bool
	task     tracker.Task
	repo     session.Repo
	name     string
	tier     hosts.Tier
	model    hosts.TierModel
	reviewer string
	// planner is true for a feature's planner session, whose kickoff is a
	// breakdown rather than a task.
	planner bool
	// heavy is true when the card requires a heavy capability, so the
	// session takes one of the host's heavy slots.
	heavy bool
}

// Started is a session engine start launched.
type Started struct {
	Task     tracker.Ref
	Name     string
	RunID    string
	RunDir   string
	Worktree session.Worktree
	// Host is the machine the session runs on when it is not this one.
	Host string
	// WindowID is the tmux window's id, such as @3.
	WindowID string
	// Attach is the command that attaches to the window on its host.
	Attach string
	// Warning is a problem the session runs despite, such as a host that
	// may sleep.
	Warning string
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
// nothing: it only reads files, the tracker, tmux's window list and another
// host's status.
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
	for _, step := range []func(Deps) error{p.checkRequires, p.pickModel, p.checkBin, p.checkTmux} {
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
	p.token, p.cfg = token, cfg
	p.host, p.remote, err = pickHost(cfg, host, p.opts.Host)
	return err
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

// checkRequires refuses a host that lacks a capability the card requires,
// even when the operator named it.
func (p *plan) checkRequires(Deps) error {
	var lacks []string
	for _, c := range p.task.Requires {
		if !p.host.Has(c) {
			lacks = append(lacks, c)
		}
	}
	if len(lacks) > 0 {
		return refuse("host %s lacks %s, which %s requires (it has: %s)", p.host.Name, strings.Join(lacks, ", "), p.task.Ref, orNone(p.host.Capabilities))
	}
	p.heavy = p.cfg.Heavy(p.task.Requires)
	return nil
}

func orNone(words []string) string {
	if len(words) == 0 {
		return "none"
	}
	return strings.Join(words, ", ")
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

// checkBin refuses a harness binary that is not executable here. Another
// machine's is its own spawn's refusal.
func (p *plan) checkBin(d Deps) error {
	if p.remote {
		return nil
	}
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
// are full, or whose heavy slots are when the task is heavy. Only a live
// window with a run recorded under its name holds a slot.
func (p *plan) checkTmux(d Deps) error {
	if p.remote {
		return p.checkStatus(d)
	}
	windows, err := d.Tmux.Windows()
	if err != nil {
		return refuse("cannot list the tmux windows: %v", err)
	}
	runs, err := session.LatestRuns(d.Home)
	if err != nil {
		return refuse("cannot read the run records: %v", err)
	}
	for _, w := range windows {
		if w.Name == p.name {
			return refuse("the tmux session %s already has a window %s (%s)", session.Name, p.name, w.ID)
		}
	}
	live, heavy := session.SlotsHeld(windows, runs)
	if slots := p.host.Slots(); live >= slots {
		return refuse("host %s has no free slot: %d sessions running, %d slots", p.host.Name, live, slots)
	}
	if slots := p.host.HeavySlots(); p.heavy && heavy >= slots {
		return refuse("host %s has no free heavy slot for %s: %d heavy sessions running, %d heavy slots", p.host.Name, p.task.Ref, heavy, slots)
	}
	return nil
}

// checkStatus is checkTmux for another machine, from its status: one call
// gives both the windows and the free slots.
func (p *plan) checkStatus(d Deps) error {
	st, err := d.HostClient(p.host, true).Status()
	if err != nil {
		return err
	}
	for _, w := range st.Windows {
		if w.Name == p.name {
			return refuse("host %s: the tmux session %s already has a window %s (%s)", p.host.Name, session.Name, p.name, w.ID)
		}
	}
	if st.FreeSlots < 1 {
		return refuse("host %s has no free slot: %d slots", p.host.Name, st.Slots)
	}
	if p.heavy && st.FreeHeavySlots < 1 {
		return refuse("host %s has no free heavy slot for %s: %d heavy slots", p.host.Name, p.task.Ref, st.HeavySlots)
	}
	return nil
}

// Kickoff is the first message of the session: the executor's kickoff line,
// plus the values its claim comment records.
func Kickoff(task tracker.Ref, mode, reviewer, runID, name string) string {
	return fmt.Sprintf("Execute %s with the engine executor, %s mode. Reviewer model: %s. run=%s tmux=%s:%s rc=%s",
		task, mode, reviewer, runID, session.Name, name, name)
}

// launch has the host sync engine's clone, then spawn the session with the
// model, effort and kickoff decided here.
func (p plan) launch(d Deps) (Started, error) {
	host := d.HostClient(p.host, p.remote)
	if _, err := host.SyncRepo(p.repo.String()); err != nil {
		return Started{}, err
	}
	// Taken once the clone is synced, so the run ID and run.json's spawn
	// time stay seconds apart.
	runID := session.RunID(p.repo, p.opts.Ref.Number, d.Now())
	sp, err := host.Spawn(p.spawnRequest(runID))
	if err != nil {
		return Started{}, err
	}
	s := Started{
		Task: p.opts.Ref, Name: sp.Name, RunID: runID, RunDir: sp.RunDir,
		Worktree: session.Worktree{Path: sp.Worktree, Branch: sp.Branch},
		WindowID: sp.Window, Attach: sp.Attach, Warning: sp.Warning,
	}
	if p.remote {
		// The window is on another machine, so the paths and the attach
		// command are too.
		s.Host, s.Attach = p.host.Name, "ssh -t "+p.host.Address+" "+hosts.ShellWord(sp.Attach)
	}
	return s, nil
}

// spawnRequest is what the host needs to launch the session: the task's
// kickoff, or the planner's, at the chosen model.
func (p plan) spawnRequest(runID string) hostops.SpawnRequest {
	r := hostops.SpawnRequest{
		Repo: p.repo.String(), Task: p.opts.Ref.Number, Mode: p.opts.Mode, Tier: string(p.tier),
		Model: p.model.Model, Effort: p.model.Effort, RunID: runID, Heavy: p.heavy,
		Kickoff: Kickoff(p.opts.Ref, p.opts.Mode, p.reviewer, runID, p.name),
	}
	if p.task.Parent != nil {
		r.Feature = p.task.Parent.String()
	}
	if p.planner {
		r.Feature = p.opts.Ref.String()
		r.Kickoff = PlannerKickoff(p.opts.Ref, runID, p.name)
	}
	return r
}

func report(out io.Writer, s Started) {
	target := session.Name + ":" + s.WindowID
	on := ""
	if s.Host != "" {
		on = " on " + s.Host
	}
	fmt.Fprintf(out, "Started %s as %s%s.\n", s.Task, s.Name, on)
	fmt.Fprintf(out, "  run             %s\n", s.RunID)
	fmt.Fprintf(out, "  run record      %s\n", filepath.Join(s.RunDir, session.RecordFile))
	fmt.Fprintf(out, "  worktree        %s (branch %s)\n", s.Worktree.Path, s.Worktree.Branch)
	fmt.Fprintf(out, "  tmux window     %s (%s)\n", target, s.Name)
	fmt.Fprintf(out, "  Remote Control  %s, in the Claude app and at claude.ai/code\n", s.Name)
	fmt.Fprintf(out, "Attach: engine attach %s (once the executor has posted its claim)\n", s.Name)
	fmt.Fprintf(out, "    or: %s\n", s.Attach)
	if s.Warning != "" {
		fmt.Fprintf(out, "Warning: %s\n", s.Warning)
	}
}

// Check runs the preflight check on host, which is this one when empty, prints
// its report, and reports whether an unattended session there would run without a
// prompt.
func Check(d Deps, host string, out io.Writer) (bool, error) {
	if host == "" {
		return d.HostClient(hosts.Host{}, false).Check(out)
	}
	cfg, local, err := hosts.Open(hosts.ConfigDir(d.Home))
	if err != nil {
		return false, refuse("%v", err)
	}
	target, isRemote, err := pickHost(cfg, local, host)
	if err != nil {
		return false, err
	}
	return d.HostClient(target, isRemote).Check(out)
}
