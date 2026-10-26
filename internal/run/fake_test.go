package run

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/ps"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

var t0 = time.Date(2026, 10, 24, 13, 0, 0, 0, time.UTC)

func ref(n int) tracker.Ref { return tracker.Ref{Repo: "o/r", Number: n} }

// cardBody is a well-formed card of the given tier.
func cardBody(n int, tier string) string {
	return fmt.Sprintf("## Outcome\nx\n## Tests\ny\n## Requires\nnone\n## Harness / tier\n%s\n<!-- engine:card k%d -->\n", tier, n)
}

// behaviour is how a fake executor runs its task, in fake minutes after its
// start: when it claims, opens its PR and merges, and whether it dies or
// waits instead. A zero step never happens.
type behaviour struct {
	claim, review, merge int
	dieAt, waitAt        int
	// linger keeps the window open after the merge, as an executor does that
	// reports instead of closing it; waitAt then says when it goes quiet.
	linger bool
	// gated starts the session in gated mode.
	gated bool
}

var smooth = behaviour{claim: 1, review: 3, merge: 4}

// executor is a session the fake world started.
type executor struct {
	ref     tracker.Ref
	host    string
	started time.Time
	do      behaviour
	window  session.WindowInfo
	dir     string
	status  string
	heavy   bool
	// harness is the card's, or claude.
	harness string
}

// fakeHost is a host of the fake world.
type fakeHost struct {
	name      string
	slots     int
	caps      []string
	harnesses []string
	// heavy caps the host's heavy sessions; 0 is every slot.
	heavy int
	// down is why its status fails, when it does, and agentsDown why its
	// claude agents does.
	down, agentsDown string
}

// world is a fake World: an in-memory feature, hosts, a clock, and fake
// executors that move their tasks along as the clock runs.
type world struct {
	t       *testing.T
	home    string
	now     time.Time
	feature tracker.Feature
	// slots are this host's, mac, when hosts is empty.
	slots int
	hosts []fakeHost
	// limit caps claude across the hosts; 0 is 8.
	limit    int
	placed   []string       // each start, as <task>@<host>
	plans    func(w *world) // the planner's work, when it is started
	refuse   func(tracker.Ref) error
	failNext map[string]error // the next call of a write fails: "comment", "close", "release"
	killed   []string         // the ids of the windows released
	released []string         // each release, as <name>@<host>
	keep     string           // why release keeps a worktree, when it does
	attempts int              // planner starts tried, refused or not
	history  []string         // the labels set on the feature, in order
	do       map[int]behaviour
	execs    map[tracker.Ref]*executor
	nextID   int
	starts   []tracker.Ref
	comments map[tracker.Ref][]string
	closed   bool
	log      []string
}

func newWorld(t *testing.T, labels []string, cards ...tracker.Card) *world {
	return &world{t: t, home: t.TempDir(), now: t0, slots: 3, do: map[int]behaviour{},
		feature:  tracker.Feature{Ref: ref(1), Title: "F", Open: true, Labels: labels, Tasks: cards},
		execs:    map[tracker.Ref]*executor{},
		comments: map[tracker.Ref][]string{}}
}

func card(n int, tier string, blockedBy ...int) tracker.Card {
	c := tracker.Card{Ref: ref(n), Title: fmt.Sprintf("task %d", n), Open: true, Body: cardBody(n, tier)}
	for _, b := range blockedBy {
		c.BlockedBy = append(c.BlockedBy, tracker.Blocker{Ref: ref(b), Open: true})
	}
	return c
}

func (w *world) card(r tracker.Ref) *tracker.Card {
	for i := range w.feature.Tasks {
		if w.feature.Tasks[i].Ref == r {
			return &w.feature.Tasks[i]
		}
	}
	if r == w.feature.Ref {
		return &tracker.Card{Ref: r, Labels: w.feature.Labels}
	}
	w.t.Fatalf("no card %s", r)
	return nil
}

func (w *world) Feature(tracker.Ref) (tracker.Feature, error) {
	f := w.feature
	f.Labels = slices.Clone(f.Labels)
	f.Tasks = slices.Clone(f.Tasks)
	for i := range f.Tasks {
		f.Tasks[i].Labels = slices.Clone(f.Tasks[i].Labels)
		f.Tasks[i].BlockedBy = slices.Clone(f.Tasks[i].BlockedBy)
	}
	return f, nil
}

func (w *world) Task(r tracker.Ref) (tracker.Card, tracker.Ref, error) {
	return *w.card(r), w.feature.Ref, nil
}

// Rows settles claims as engine ps does: a claim on a host that cannot be
// read stays running, with liveness unknown.
func (w *world) Rows(f Fleet) ([]ps.Row, error) {
	var rows []ps.Row
	for _, c := range w.feature.Tasks {
		e, ok := w.execs[c.Ref]
		if !ok || !c.Open || !hasAny(c.Labels, claimStates) {
			continue
		}
		state := ps.Running
		if _, err := f.status(e.host); err == nil && e.window.Dead {
			state = ps.Dead
		}
		rows = append(rows, ps.Row{Task: c.Ref.String(), Session: e.window.Name, Host: e.host, State: state})
	}
	return rows, nil
}

func (w *world) fleet() []fakeHost {
	if len(w.hosts) == 0 {
		return []fakeHost{{name: "mac", slots: w.slots, caps: []string{"macos"}, harnesses: []string{"claude"}}}
	}
	return w.hosts
}

// live names the live windows on host.
func (w *world) live(host string) []string {
	var names []string
	for _, e := range w.execs {
		if e.host == host && e.window.ID != "" && !e.window.Dead {
			names = append(names, e.window.Name)
		}
	}
	return names
}

// liveHeavy counts the live heavy sessions on host.
func (w *world) liveHeavy(host string) int {
	n := 0
	for _, e := range w.execs {
		if e.host == host && e.heavy && e.window.ID != "" && !e.window.Dead {
			n++
		}
	}
	return n
}

// heavySlots is the host's heavy cap.
func (h fakeHost) heavySlots() int { return cmp.Or(h.heavy, h.slots) }

// heavyCaps are the fake world's heavy capabilities.
var heavyCaps = []string{"ios", "vm"}

// harnessOf is the harness a session of r runs: its card's, else claude. A
// session started outside the feature runs claude.
func (w *world) harnessOf(r tracker.Ref) string {
	for _, c := range w.feature.Tasks {
		if c.Ref == r {
			if h, _, err := tracker.ParseHarnessTier(c.Body); err == nil && h != "" {
				return h
			}
		}
	}
	return hostops.Claude
}

// heavy reports whether r is a task of the feature that requires a heavy
// capability. A session started outside the feature is not.
func (w *world) heavy(r tracker.Ref) bool {
	for _, c := range w.feature.Tasks {
		if c.Ref == r {
			return hosts.IsHeavy(c.Requires, heavyCaps)
		}
	}
	return false
}

func (w *world) Fleet() Fleet {
	f := Fleet{Local: "mac", DefaultHarness: "claude", Caps: map[string]int{"claude": cmp.Or(w.limit, 8), "codex": 2}, Heavy: heavyCaps}
	for _, h := range w.fleet() {
		var err error
		if h.down != "" {
			err = errors.New(h.down)
		}
		f.Hosts = append(f.Hosts, load(hosts.Host{Name: h.name, Harnesses: h.harnesses, Capabilities: h.caps}, w.status(h), err))
	}
	slices.SortFunc(f.Hosts, func(a, b HostLoad) int { return cmp.Compare(a.Name, b.Name) })
	return f
}

// status is what a host's status op says: its windows, with the state and
// the screen of each whose session is not working, its live Claude Code
// sessions and its newest runs.
func (w *world) status(h fakeHost) hostops.Status {
	st := hostops.Status{Slots: h.slots, FreeSlots: max(0, h.slots-len(w.live(h.name))), HeavySlots: h.heavySlots(), AgentsError: h.agentsDown}
	st.FreeHeavySlots = min(max(0, h.heavySlots()-w.liveHeavy(h.name)), st.FreeSlots)
	for _, e := range w.execs {
		if e.host != h.name || e.window.ID == "" {
			continue
		}
		win := hostops.Window{Name: e.window.Name, ID: e.window.ID, Alive: !e.window.Dead, Harness: e.harness}
		// A Codex window's state is its state file's, whatever Claude Code's
		// list says; Claude Code's list holds Claude sessions only.
		switch agent := (session.Agent{Name: e.window.Name, Status: e.status}); {
		case e.window.Dead:
		case e.harness == hostops.Codex:
			win.State = session.AgentState(agent)
		case h.agentsDown == "":
			st.Agents = append(st.Agents, agent)
			win.State = session.AgentState(agent)
		}
		if win.State != session.StateWorking {
			win.Screen = "Do you want to proceed?\n"
		}
		st.Windows = append(st.Windows, win)
	}
	runs, err := session.LatestRuns(w.hostHome(h.name))
	if err != nil {
		w.t.Fatal(err)
	}
	for name, r := range runs {
		st.Runs = append(st.Runs, hostops.Run{Name: name, Dir: r.Dir, Record: r.Record})
	}
	return st
}

// hostHome is a host's home directory: its run directories are there.
func (w *world) hostHome(host string) string {
	if host == "mac" {
		return w.home
	}
	return filepath.Join(w.home, "hosts", host)
}

func (w *world) NoteDir(r tracker.Ref) (string, error) {
	return filepath.Join(w.home, "notes", fmt.Sprint(r.Number)), nil
}

func (w *world) launch(r tracker.Ref, host string, do behaviour) error {
	name := fmt.Sprintf("r-%d", r.Number)
	w.nextID++
	heavy := w.heavy(r)
	e := &executor{ref: r, host: host, started: w.now, do: do, status: session.AgentBusy, heavy: heavy, harness: w.harnessOf(r),
		window: session.WindowInfo{ID: fmt.Sprintf("@%d", w.nextID), Name: name}}
	// Another host's run directory is on that host.
	dir, err := session.RunDir(w.hostHome(host), session.RunID(session.Repo{Owner: "o", Name: "r"}, r.Number, w.now))
	if err != nil {
		return err
	}
	e.dir = dir
	w.execs[r] = e
	mode := "hands-off"
	if do.gated {
		mode = "gated"
	}
	return session.WriteRecord(dir, session.Record{RunID: filepath.Base(dir), Task: r.String(), Name: name,
		Mode: mode, Heavy: heavy, Harness: e.harness, Tmux: "engine:" + e.window.ID, Spawned: w.now})
}

// Start refuses as engine start does: an unknown host, a window of that
// name, live or dead, or no free slot on the host, or no free heavy slot for
// a heavy task.
func (w *world) Start(r tracker.Ref, host string) error {
	i := slices.IndexFunc(w.fleet(), func(h fakeHost) bool { return h.name == host })
	if i < 0 {
		return fmt.Errorf("host %q is not in hosts.toml", host)
	}
	for _, e := range w.execs {
		if e.window.ID != "" && e.window.Name == fmt.Sprintf("r-%d", r.Number) {
			return fmt.Errorf("the tmux session engine already has a window r-%d", r.Number)
		}
	}
	h := w.fleet()[i]
	if len(w.live(host)) >= h.slots {
		return fmt.Errorf("no free slot")
	}
	if w.heavy(r) && w.liveHeavy(host) >= h.heavySlots() {
		return fmt.Errorf("no free heavy slot")
	}
	if w.refuse != nil {
		if err := w.refuse(r); err != nil {
			return err
		}
	}
	w.starts = append(w.starts, r)
	w.placed = append(w.placed, fmt.Sprintf("%d@%s", r.Number, host))
	return w.launch(r, host, w.do[r.Number])
}

func (w *world) StartPlanner(r tracker.Ref) error {
	w.attempts++
	if w.refuse != nil {
		if err := w.refuse(r); err != nil {
			return err
		}
	}
	w.starts = append(w.starts, r)
	return w.launch(r, "mac", behaviour{})
}

func (w *world) AddLabels(r tracker.Ref, labels ...string) error {
	if r == w.feature.Ref {
		w.history = append(w.history, labels...)
	}
	c := w.labels(r)
	for _, l := range labels {
		if !slices.Contains(*c, l) {
			*c = append(*c, l)
		}
	}
	return nil
}

func (w *world) RemoveLabel(r tracker.Ref, label string) error {
	c := w.labels(r)
	*c = slices.DeleteFunc(*c, func(l string) bool { return l == label })
	return nil
}

func (w *world) labels(r tracker.Ref) *[]string {
	if r == w.feature.Ref {
		return &w.feature.Labels
	}
	return &w.card(r).Labels
}

func (w *world) fails(call string) error {
	err := w.failNext[call]
	delete(w.failNext, call)
	return err
}

func (w *world) Comment(r tracker.Ref, body string) error {
	if err := w.fails("comment"); err != nil {
		return err
	}
	w.comments[r] = append(w.comments[r], body)
	return nil
}

func (w *world) Close(tracker.Ref) error {
	if err := w.fails("close"); err != nil {
		return err
	}
	w.closed, w.feature.Open = true, false
	return nil
}

// Operator is "op" on the feature and the planner's machine user on a task,
// which the planner wrote.
func (w *world) Operator(r tracker.Ref) (string, error) {
	if r == w.feature.Ref {
		return "op", nil
	}
	return "bot", nil
}

func (w *world) PullRequests(r tracker.Ref) ([]string, error) {
	return []string{fmt.Sprintf("https://example.test/o/r/pull/%d", 100+r.Number)}, nil
}

// Release closes every window of the name on the host, as the release op
// does.
func (w *world) Release(host, name string) (hostops.Released, error) {
	if err := w.fails("release"); err != nil {
		return hostops.Released{}, err
	}
	w.released = append(w.released, name+"@"+host)
	r := hostops.Released{Name: name}
	if w.keep != "" {
		r.Worktree, r.WorktreeKept, r.Note = "/wt/"+name, true, w.keep
	}
	for _, e := range w.execs {
		if e.host == host && e.window.ID != "" && e.window.Name == name {
			w.killed = append(w.killed, e.window.ID)
			e.window, r.WindowClosed = session.WindowInfo{}, true
		}
	}
	return r, nil
}

func (w *world) Now() time.Time { return w.now }

func (w *world) Logf(format string, args ...any) { w.log = append(w.log, fmt.Sprintf(format, args...)) }

// sleep runs the clock forward a minute at a time, and each executor's
// script with it.
func (w *world) sleep(d time.Duration) {
	for end := w.now.Add(d); w.now.Before(end); {
		w.now = w.now.Add(time.Minute)
		for _, e := range w.execs {
			w.step(e)
		}
	}
	if w.now.Sub(t0) > 24*time.Hour {
		w.t.Fatalf("the run never ended:\n%s", strings.Join(w.log, "\n"))
	}
}

// step moves one executor along its script.
func (w *world) step(e *executor) {
	if e.window.ID == "" || e.window.Dead {
		return
	}
	if e.ref == w.feature.Ref {
		if w.plans != nil {
			w.plans(w)
			w.plans = nil
		}
		e.window = session.WindowInfo{}
		return
	}
	at := int(w.now.Sub(e.started) / time.Minute)
	c := w.card(e.ref)
	if !c.Open {
		if e.do.waitAt != 0 && at >= e.do.waitAt {
			e.status = session.AgentWaiting
		}
		return
	}
	switch {
	case e.do.dieAt != 0 && at >= e.do.dieAt:
		e.window.Dead = true
	case e.do.waitAt != 0 && at >= e.do.waitAt:
		e.status = session.AgentWaiting
	case e.do.merge != 0 && at >= e.do.merge && c.Open:
		w.merge(c)
		if !e.do.linger {
			e.window = session.WindowInfo{}
		}
	case e.do.review != 0 && at >= e.do.review && !c.Has(InReview):
		_ = w.RemoveLabel(c.Ref, Running)
		_ = w.AddLabels(c.Ref, InReview)
	case e.do.claim != 0 && at >= e.do.claim && !hasAny(c.Labels, claimStates):
		_ = w.AddLabels(c.Ref, Running)
	}
}

// merge closes a task and unblocks its dependants, as a merged PR does.
func (w *world) merge(c *tracker.Card) {
	c.Open = false
	c.Labels = slices.DeleteFunc(c.Labels, func(l string) bool { return l == InReview || l == Running })
	for i := range w.feature.Tasks {
		for j := range w.feature.Tasks[i].BlockedBy {
			if w.feature.Tasks[i].BlockedBy[j].Ref == c.Ref {
				w.feature.Tasks[i].BlockedBy[j].Open = false
			}
		}
	}
}

func mkdir(dir string) error { return os.MkdirAll(dir, 0o755) }
