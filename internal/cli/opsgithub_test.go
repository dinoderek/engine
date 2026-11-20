package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// devScript is the repo's own ./dev: a usage that offers doctor, and a
// doctor that fails with the text of ~/doctor.fail on a host that has one.
const devScript = `#!/bin/sh
case "$1" in
doctor)
  if [ -f "$HOME/doctor.fail" ]; then cat "$HOME/doctor.fail"; exit 1; fi
  echo "doctor: every tool is installed"; exit 0 ;;
"")
  echo "usage: ./dev <verb>"
  echo "  doctor   install the tools and report any gap"
  echo "  test     run the gates"
  exit 0 ;;
*) echo "unknown verb $1" >&2; exit 2 ;;
esac
`

// ghCheck is one entry of a PR's status check rollup.
type ghCheck struct {
	Type       string    `json:"__typename"`
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	StartedAt  time.Time `json:"startedAt"`
}

// ghPR is a pull request of the fleet's repo.
type ghPR struct {
	Number                   int
	Branch, Head, Title      string
	Body, State              string
	Checks                   []ghCheck
	MergedSubject, MergeBody string
}

// fakeGitHub is GitHub for the fleet's one repo: its origin is a bare
// repository on disk, which every host clones and pushes to with real git,
// and its pull requests live here. A merge squashes the PR's head onto main
// in the bare repository, with the subject and body gh pr merge was given.
type fakeGitHub struct {
	f      *opsFleet
	slug   string
	origin string
	mu     sync.Mutex
	prs    map[int]*ghPR
	next   int
}

// newGitHub makes the bare origin, with main holding a README and devScript.
func newGitHub(f *opsFleet, slug string, h *opsHost) *fakeGitHub {
	g := &fakeGitHub{f: f, slug: slug, origin: filepath.Join(f.t.TempDir(), "app.git"), prs: map[int]*ghPR{}, next: 40}
	h.git("", "init", "--quiet", "--bare", "-b", "main", g.origin)
	seed := filepath.Join(f.t.TempDir(), "seed")
	h.git("", "clone", "--quiet", g.origin, seed)
	writeFile(f.t, filepath.Join(seed, "README.md"), "# App\n")
	writeExec(f.t, filepath.Join(seed, "dev"), devScript)
	h.git(seed, "add", ".")
	h.git(seed, "commit", "--quiet", "-m", "Start the app")
	h.git(seed, "push", "--quiet", "origin", "main")
	return g
}

// originGit runs git in the bare origin.
func (g *fakeGitHub) originGit(args ...string) string {
	return g.f.hosts[g.f.names[0]].git(g.origin, args...)
}

// open opens a PR of branch at head and returns its number.
func (g *fakeGitHub) open(branch, head, title, body string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.next++
	g.prs[g.next] = &ghPR{Number: g.next, Branch: branch, Head: head, Title: title, Body: body, State: "OPEN"}
	return g.next
}

func (g *fakeGitHub) url(n int) string { return fmt.Sprintf("https://github.com/%s/pull/%d", g.slug, n) }

// check adds a run of the check called name to PR n: pending, or completed
// with conclusion, started at.
func (g *fakeGitHub) check(n int, name, conclusion string, at time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	c := ghCheck{Type: "CheckRun", Name: name, Status: "COMPLETED", Conclusion: conclusion, StartedAt: at}
	if conclusion == "" {
		c.Status = "IN_PROGRESS"
	}
	g.prs[n].Checks = append(g.prs[n].Checks, c)
}

// finish completes the newest run of check name on PR n with conclusion.
func (g *fakeGitHub) finish(n int, name, conclusion string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	checks := g.prs[n].Checks
	for i := len(checks) - 1; i >= 0; i-- {
		if checks[i].Name == name {
			checks[i].Status, checks[i].Conclusion = "COMPLETED", conclusion
			return
		}
	}
}

func (g *fakeGitHub) pr(n int) ghPR {
	g.mu.Lock()
	defer g.mu.Unlock()
	return *g.prs[n]
}

// run answers the gh command lines engine runs.
func (g *fakeGitHub) run(args ...string) (string, error) {
	line := strings.Join(args, " ")
	switch {
	case len(args) > 4 && args[0] == "pr" && args[1] == "view" && args[3] == "-R" && args[4] == g.slug:
		return g.view(args[2])
	case len(args) == 4 && args[0] == "api" && strings.HasPrefix(args[1], "repos/"+g.slug+"/compare/main..."):
		head := strings.TrimPrefix(args[1], "repos/"+g.slug+"/compare/main...")
		return g.originGit("rev-list", "--count", head+"..main") + "\n", nil
	case len(args) > 4 && args[0] == "pr" && args[1] == "merge" && args[4] == g.slug:
		return g.merge(args)
	}
	g.f.t.Errorf("unexpected gh %s", line)
	return "", fmt.Errorf("gh %s: not in this fleet", line)
}

func (g *fakeGitHub) view(number string) (string, error) {
	n, _ := strconv.Atoi(number)
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.prs[n]
	if !ok {
		return "", fmt.Errorf("gh pr view %s: no pull request", number)
	}
	data, err := json.Marshal(map[string]any{"state": p.State, "headRefOid": p.Head, "baseRefName": "main", "mergeable": "MERGEABLE",
		"mergeStateStatus": "CLEAN", "reviewDecision": "", "statusCheckRollup": p.Checks, "title": p.Title, "body": p.Body})
	return string(data), err
}

// merge squashes the PR's head onto main: a new commit with the head's tree,
// main as its parent, and the subject and body as its message.
func (g *fakeGitHub) merge(args []string) (string, error) {
	n, _ := strconv.Atoi(args[2])
	flag := func(name string) string { return args[slicesIndex(args, name)+1] }
	g.mu.Lock()
	p := g.prs[n]
	g.mu.Unlock()
	if p == nil || p.State != "OPEN" || p.Head != flag("--match-head-commit") || args[5] != "--squash" {
		return "", fmt.Errorf("gh pr merge %d: refused %q", n, args)
	}
	msg := filepath.Join(g.f.t.TempDir(), "message")
	writeFile(g.f.t, msg, flag("--subject")+"\n\n"+flag("--body")+"\n")
	commit := g.originGit("commit-tree", p.Head+"^{tree}", "-p", "main", "-F", msg)
	g.originGit("update-ref", "refs/heads/main", commit)
	_ = os.Remove(msg)
	g.mu.Lock()
	defer g.mu.Unlock()
	p.State, p.MergedSubject, p.MergeBody = "MERGED", flag("--subject"), flag("--body")
	return "", nil
}
