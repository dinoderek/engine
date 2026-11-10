package hostops

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/session"
)

// teardown is a repo's `dev` as release sees it: the usage its probe
// prints, and what its `worktree release` does.
type teardown struct {
	usage    string
	fail     error
	removes  string // the worktree the command deletes, if any
	probed   int
	released []string
}

func (f *teardown) probe(string, []string, string, ...string) (string, error) {
	f.probed++
	return f.usage, errors.New("exit status 2")
}

func (f *teardown) run(dir string, _ []string, name string, args ...string) (string, error) {
	f.released = append(f.released, dir+" "+filepath.Base(name)+" "+strings.Join(args, " "))
	if f.fail != nil {
		return "", f.fail
	}
	if f.removes != "" {
		return "", os.RemoveAll(f.removes)
	}
	return "", nil
}

// doneRelease releases a done run of r-7 on task/r-7 whose worktree is on
// disk unless gone, from a clone whose `dev` is f.
func doneRelease(t *testing.T, f *teardown, outcome Outcome, gone bool) (Released, string, string, *gitFake) {
	t.Helper()
	h := newHome(t, "mac")
	clone := h.clone(t)
	write(t, filepath.Join(clone, "dev"), "#!/bin/sh\n", 0o755)
	worktree := filepath.Join(clone+"-worktrees", "task-r-7")
	if !gone {
		if err := os.MkdirAll(worktree, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if f.removes == "-" {
		f.removes = worktree
	}
	writeRun(t, h, session.Record{RunID: "r-7-a", Repo: "o/r", Name: "r-7", Branch: "task/r-7", Worktree: worktree, Spawned: fixedNow()})
	git := &gitFake{}
	r, err := Release(Deps{Run: git.run, Probe: f.probe, Teardown: f.run, Tmux: (&tmuxFake{}).tmux(), Home: h.dir}, "r-7", outcome)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	return r, clone, worktree, git
}

const releaseUsage = "usage: ./dev <verb>\n  worktree create <branch>\n  worktree release <branch>\n"

func TestReleaseOfADoneRunRunsTheReposWorktreeRelease(t *testing.T) {
	f := &teardown{usage: releaseUsage, removes: "-"}
	r, clone, _, git := doneRelease(t, f, RunDone, false)
	if want := []string{clone + " dev worktree release task/r-7"}; !slices.Equal(f.released, want) {
		t.Errorf("teardown %v, want %v", f.released, want)
	}
	// The repo's command removed the worktree, so engine's own removal has
	// nothing left to do.
	if r != (Released{Name: "r-7", RepoReleased: true}) || len(git.calls) != 0 {
		t.Errorf("released %+v, git %v", r, git.calls)
	}
}

// The repo's command is idempotent, so it runs after the session already
// tore the worktree down, too.
func TestReleaseOfADoneRunRunsTheReposReleaseWhenTheWorktreeIsGone(t *testing.T) {
	f := &teardown{usage: releaseUsage}
	r, _, _, git := doneRelease(t, f, RunDone, true)
	if len(f.released) != 1 || !r.RepoReleased || r.WorktreeKept || r.Note != "" || len(git.calls) != 0 {
		t.Errorf("released %+v, teardown %v, git %v", r, f.released, git.calls)
	}
}

// A failure is a note: the release goes on to engine's own removal.
func TestAFailedRepoReleaseIsANote(t *testing.T) {
	f := &teardown{usage: releaseUsage, fail: errors.New("dev worktree release: no answer within 2m0s")}
	r, _, worktree, git := doneRelease(t, f, RunDone, false)
	if !r.RepoReleased || r.WorktreeKept || r.Worktree != worktree || !strings.Contains(r.Note, "no answer within") {
		t.Errorf("released %+v", r)
	}
	if want := []string{"git worktree remove " + worktree}; !slices.Equal(git.calls, want) {
		t.Errorf("git calls %v, want %v", git.calls, want)
	}
}

// A repo whose `dev` does not list `worktree release` gets only engine's
// own removal; so does a run that is not done, whatever the repo provides.
func TestReleaseRunsOnlyAReleaseTheRepoProvidesForADoneRun(t *testing.T) {
	for name, c := range map[string]struct {
		usage   string
		outcome Outcome
		probed  int
	}{
		"no such verb": {"usage: ./dev worktree create <branch>\n", RunDone, 1},
		"ended run":    {releaseUsage, RunEnded, 0},
	} {
		t.Run(name, func(t *testing.T) {
			f := &teardown{usage: c.usage}
			r, _, worktree, git := doneRelease(t, f, c.outcome, false)
			if len(f.released) != 0 || f.probed != c.probed || r.RepoReleased || r.WorktreeKept {
				t.Errorf("released %+v, teardown %v, probed %d", r, f.released, f.probed)
			}
			if want := []string{"git worktree remove " + worktree}; !slices.Equal(git.calls, want) {
				t.Errorf("git calls %v, want %v", git.calls, want)
			}
		})
	}
}

func TestReleaseOpTakesOneOutcome(t *testing.T) {
	h := newHome(t, "mac")
	d := Deps{Tmux: (&tmuxFake{}).tmux(), Home: h.dir, Run: (&gitFake{}).run}
	code, reply, _ := call(t, d, "", "release", "--json", "--name", "r-7", "--failed", "--done")
	if code != 1 || reply["refused"] != true || !strings.Contains(reply["error"].(string), "cannot both be set") {
		t.Errorf("both outcomes: exit %d, %v", code, reply)
	}
	if code, reply, _ := call(t, d, "", "release", "--json", "--name", "r-7", "--done"); code != 0 || reply["name"] != "r-7" {
		t.Errorf("--done: exit %d, %v", code, reply)
	}
}
