package hostops

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

// The readiness checks, by the name a report and a note give them.
const (
	GHCheck     = "gh auth status"
	DoctorCheck = "./dev doctor"
)

// ReadyTTL is how long a probe's result answers for an unchanged head and
// host config.
const ReadyTTL = 6 * time.Hour

// readyDir is where this host keeps a repo's readiness: the cached result,
// the probe's step files and, while it runs, its scratch worktree.
const readyDir = ".local/share/engine/readiness"

// ReadyRequest asks whether sessions of Repo can work on this host. Remote is
// where a first clone comes from; without it the repo must be cloned here
// already. Fresh probes again even when a cached result would answer.
type ReadyRequest struct {
	Repo   string
	Remote string
	Fresh  bool
}

// Readiness is the ready op's reply: the probe's checks on the default
// branch's head, under one hosts.toml, and when it ran.
type Readiness struct {
	ProbedOn string       `json:"probed_on"`
	Repo     string       `json:"repo"`
	Head     string       `json:"head"`
	Config   string       `json:"config"`
	At       time.Time    `json:"at"`
	Checks   []ReadyCheck `json:"checks"`
	// Cached is true when an earlier probe's result answered.
	Cached bool `json:"cached,omitempty"`
}

// ReadyCheck is one check of the probe: whether it passed, and the tail of
// what it printed.
type ReadyCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Output string `json:"output,omitempty"`
}

// Failed are the checks that did not pass.
func (r Readiness) Failed() []ReadyCheck {
	var out []ReadyCheck
	for _, c := range r.Checks {
		if !c.OK {
			out = append(out, c)
		}
	}
	return out
}

// OK reports whether every check passed.
func (r Readiness) OK() bool { return len(r.Checks) > 0 && len(r.Failed()) == 0 }

// readyKey is what a cached result answers for.
type readyKey struct{ host, repo, head, config string }

func (r Readiness) key() readyKey { return readyKey{r.ProbedOn, r.Repo, r.Head, r.Config} }

// reusable reports whether cached answers for key at now: the same host,
// repo, head and config, probed less than ReadyTTL ago.
func reusable(cached Readiness, key readyKey, now time.Time) bool {
	age := now.Sub(cached.At)
	return cached.key() == key && age >= 0 && age < ReadyTTL
}

// Ready syncs engine's clone of the repo and answers whether its sessions can
// work here, from the cached result when it still answers, else by probing
// the default branch's head (probeReady) and caching the result.
func Ready(d Deps, r ReadyRequest) (Readiness, error) {
	repo, err := session.ParseRepo(r.Repo)
	if err != nil {
		return Readiness{}, refuse("%v", err)
	}
	_, host, err := openHost(d.Home)
	if err != nil {
		return Readiness{}, err
	}
	config, err := configDigest(d.Home)
	if err != nil {
		return Readiness{}, refuse("%v", err)
	}
	repos := session.Repos{Run: d.Run, Probe: d.Probe, Home: d.Home, BaseURL: d.BaseURL, Control: true, RemoteURL: r.Remote}
	if repos.RemoteURL == "" {
		if repos.RemoteURL, err = repos.Origin(repo); err != nil {
			return Readiness{}, refuse("%v; pass --remote to clone it", err)
		}
	}
	clone, err := repos.Sync(repo)
	if err != nil {
		return Readiness{}, err
	}
	head, err := repos.Head(clone)
	if err != nil {
		return Readiness{}, err
	}
	dir := filepath.Join(d.Home, filepath.FromSlash(readyDir), repo.Owner, repo.Name)
	unlock, err := session.Lock(dir + ".lock")
	if err != nil {
		return Readiness{}, err
	}
	defer unlock()
	key := readyKey{host.Name, repo.String(), head, config}
	if cached, ok := readReadiness(dir); ok && !r.Fresh && reusable(cached, key, d.Now()) {
		cached.Cached = true
		return cached, nil
	}
	checks, err := probeReady(d, host, readyProbe{repos: repos, clone: clone, head: head, dir: dir})
	if err != nil {
		return Readiness{}, err
	}
	out := Readiness{ProbedOn: host.Name, Repo: repo.String(), Head: head, Config: config, At: d.Now().UTC(), Checks: checks}
	return out, writeReadiness(dir, out)
}

// configDigest names this host's hosts.toml by its content.
func configDigest(home string) (string, error) {
	data, err := os.ReadFile(filepath.Join(hosts.ConfigDir(home), "hosts.toml"))
	if err != nil {
		return "", fmt.Errorf("read host config: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8]), nil
}

// readyFile is the cached result in a repo's readiness directory.
const readyFile = "result.json"

func readReadiness(dir string) (Readiness, bool) {
	data, err := os.ReadFile(filepath.Join(dir, readyFile))
	if err != nil {
		return Readiness{}, false
	}
	var r Readiness
	if json.Unmarshal(data, &r) != nil {
		return Readiness{}, false
	}
	return r, true
}

func writeReadiness(dir string, r Readiness) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(dir, readyFile+".new")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, readyFile))
}

// RunReady probes the repo afresh, prints the report to out, and reports
// whether every check passed.
func RunReady(d Deps, repo string, out io.Writer) (bool, error) {
	r, err := Ready(d, ReadyRequest{Repo: repo, Fresh: true})
	if err != nil {
		return false, err
	}
	ReportReady(out, r)
	return r.OK(), nil
}

// ReportReady prints a probe's checks, with the output of each that failed.
func ReportReady(out io.Writer, r Readiness) {
	mark := map[bool]string{true: "ok  ", false: "FAIL"}
	for _, c := range r.Checks {
		fmt.Fprintf(out, "%s %s: %s at %s\n", mark[c.OK], r.Repo, c.Name, short(r.Head))
		if !c.OK {
			fmt.Fprintln(out, indent(c.Output))
		}
	}
	if len(r.Checks) == 1 {
		fmt.Fprintf(out, "note %s's dev offers no doctor, so only gh was checked\n", r.Repo)
	}
}

// short is a commit as a report shows it.
func short(sha string) string { return sha[:min(len(sha), 12)] }
