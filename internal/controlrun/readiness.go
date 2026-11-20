package controlrun

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/reconcile"
	"github.com/dinoderek/engine/internal/remote"
)

// ReadyEvery is how often the coordinator asks a host about a repo. The host
// answers from its cache until the repo's head or hosts.toml changes, or
// hostops.ReadyTTL passes, so most asks cost one fetch.
const ReadyEvery = 5 * time.Minute

// Readiness asks each host, in the background, whether each coordinated
// repo's sessions can work there (the ready op), and keeps the answers for
// placement. A pass never waits for a probe.
type Readiness struct {
	Client func(hosts.Host) remote.Client
	Now    func() time.Time
	Logf   func(string, ...any)

	mu      sync.Mutex
	entries map[readyAt]*readyEntry
	noted   map[string]bool
	reads   sync.WaitGroup
}

// readyAt is a host and a repo ID.
type readyAt struct{ host, repo string }

// readyEntry is the latest answer for a host and repo: the probe's result,
// or why the probe could not run, which replaces the result until one comes.
type readyEntry struct {
	slug     string
	result   *hostops.Readiness
	err      string
	asked    time.Time
	inflight bool
}

func (r *Readiness) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Ask starts a probe of every repo on every host whose last ask is
// ReadyEvery old, unless one is in flight.
func (r *Readiness) Ask(cfg *hosts.Config, repos []control.Item) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries, r.noted = map[readyAt]*readyEntry{}, map[string]bool{}
	}
	for _, name := range cfg.HostNames() {
		for _, repo := range repos {
			key := readyAt{name, repo.ID}
			e := r.entries[key]
			if e == nil {
				e = &readyEntry{}
				r.entries[key] = e
			}
			if e.inflight || !e.asked.IsZero() && r.now().Sub(e.asked) < ReadyEvery {
				continue
			}
			e.inflight, e.asked, e.slug = true, r.now(), repo.Slug
			r.reads.Add(1)
			go r.read(cfg.Hosts[name], key, hostops.ReadyRequest{Repo: repo.Slug, Remote: repo.Remote})
		}
	}
}

// read asks one host. An unreachable host leaves the answer as it was: its
// status already keeps it from taking work.
func (r *Readiness) read(h hosts.Host, key readyAt, req hostops.ReadyRequest) {
	defer r.reads.Done()
	res, err := remote.Ready(r.Client(h), req)
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entries[key]
	e.inflight = false
	var unreachable *remote.UnreachableError
	switch {
	case errors.As(err, &unreachable):
	case err != nil:
		e.err = err.Error()
		if r.Logf != nil {
			r.Logf("readiness of %s on %s: %v", req.Repo, h.Name, err)
		}
	default:
		e.result, e.err = &res, ""
	}
}

// Settle waits for the probes in flight.
func (r *Readiness) Settle() { r.reads.Wait() }

// For is host's readiness for each repo asked about, for placement: a repo
// with no answer yet is unknown.
func (r *Readiness) For(host string) map[string]reconcile.Readiness {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]reconcile.Readiness{}
	for key, e := range r.entries {
		if key.host != host {
			continue
		}
		switch {
		case e.err != "":
			out[key.repo] = reconcile.Readiness{Known: true, Unready: "its readiness probe could not run: " + e.err}
		case e.result != nil:
			out[key.repo] = reconcile.Readiness{Known: true, Unready: unready(*e.result)}
		}
	}
	return out
}

// unready says which checks failed, or nothing when every one passed.
func unready(res hostops.Readiness) string {
	var names []string
	for _, c := range res.Failed() {
		names = append(names, c.Name)
	}
	if len(names) == 0 {
		return ""
	}
	return strings.Join(names, " and ") + " failed (the readiness note has the output)"
}

// readyNote is one note for the operator: the item it is on, its key and its
// text.
type readyNote struct{ item, key, body string }

// notes are the notes for every failure seen and not yet noted: one per host,
// repo, check and output, or per probe error, so a repeat posts nothing. A
// host whose tmux server's directory is gone gets one per server.
func (r *Readiness) notes(fleet reconcile.Fleet) []readyNote {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []readyNote
	add := func(n readyNote) {
		if !r.noted[n.key] {
			out = append(out, n)
		}
	}
	for key, e := range r.entries {
		switch {
		case e.err != "":
			add(readyNote{key.repo, "readiness/" + key.host + "/" + key.repo + "/error/" + digest(e.err), fmt.Sprintf(
				"The readiness probe of %s could not run on %s: %s\nNo task of %s is placed on %s until it does.", e.slug, key.host, e.err, e.slug, key.host)})
		case e.result != nil:
			for _, c := range e.result.Failed() {
				add(readyNote{key.repo, "readiness/" + key.host + "/" + key.repo + "/" + digest(c.Name+"\n"+c.Output), failedText(key.host, *e.result, c)})
			}
		}
	}
	for _, h := range fleet.Hosts {
		if gone := h.Status.TmuxCwdGone; gone != nil && h.Unreachable == "" {
			add(readyNote{"", fmt.Sprintf("readiness/%s/tmux-cwd/%d", h.Name, gone.Pid), "Host " + h.Name + " is unready: " + gone.Reason})
		}
	}
	return out
}

// failedText is a failed check's note: the host, the repo, the check, its
// output, and how to probe again.
func failedText(host string, res hostops.Readiness, c hostops.ReadyCheck) string {
	return fmt.Sprintf("Host %s is unready for %s: `%s` failed on the default branch's head %s:\n%s\n"+
		"No task of %s is placed on %s until it passes. Once it is fixed, run `engine setup --host %s` to probe again; "+
		"otherwise the next probe runs on a new head, a hosts.toml change, or after %s.",
		host, res.Repo, c.Name, shortSHA(res.Head), c.Output, res.Repo, host, host, hostops.ReadyTTL)
}

// postNotes sends every new note. One that fails is retried next pass. A
// key the server already holds, as after a restart, was posted: its body may
// differ only in the head, which the key leaves out.
func (c *Coordinator) postNotes(fleet reconcile.Fleet) {
	for _, n := range c.Ready.notes(fleet) {
		if err := c.notice(n.item, n.key, n.body); err != nil && !keyUsed(err) {
			c.log("readiness note %s: %v", n.key, err)
			continue
		}
		c.Ready.mu.Lock()
		if c.Ready.noted == nil {
			c.Ready.noted = map[string]bool{}
		}
		c.Ready.noted[n.key] = true
		c.Ready.mu.Unlock()
	}
}

// askReady asks about the repo of every coordinated plan.
func (c *Coordinator) askReady(st control.State, ids []string, cfg *hosts.Config) {
	var repos []control.Item
	seen := map[string]bool{}
	for _, id := range ids {
		repo := st.Items[st.Items[id].Repo]
		if repo.ID != "" && !seen[repo.ID] {
			seen[repo.ID] = true
			repos = append(repos, repo)
		}
	}
	c.Ready.Ask(cfg, repos)
}

// keyUsed reports a request key the server holds for another body.
func keyUsed(err error) bool {
	var p *control.Problem
	return errors.As(err, &p) && p.Status == 409 && strings.Contains(p.Message, "request key was already used")
}

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:6])
}

func shortSHA(sha string) string { return sha[:min(len(sha), 12)] }
