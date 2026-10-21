package session

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseRepo(t *testing.T) {
	tests := []struct {
		in   string
		want Repo
		ok   bool
	}{
		{"dinoderek/engine", Repo{"dinoderek", "engine"}, true},
		{"o/.github", Repo{"o", ".github"}, true},
		{"a_b/c.d-e", Repo{"a_b", "c.d-e"}, true},
		{"engine", Repo{}, false},
		{"/engine", Repo{}, false},
		{"o/", Repo{}, false},
		{"o/r/x", Repo{}, false},
		{"../r", Repo{}, false},
		{"o/..", Repo{}, false},
		{"./r", Repo{}, false},
		{"o/a b", Repo{}, false},
		{"o/r;rm", Repo{}, false},
	}
	for _, tt := range tests {
		got, err := ParseRepo(tt.in)
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("ParseRepo(%q) = %v, %v; want %v, ok=%v", tt.in, got, err, tt.want, tt.ok)
		}
	}
	if s := (Repo{"o", "r"}).String(); s != "o/r" {
		t.Errorf("String() = %q", s)
	}
}

func TestPathAndURL(t *testing.T) {
	repo := mustRepo(t, "o/r")
	r := Repos{Home: "/home/m"}
	path, err := r.Path(repo)
	if err != nil || path != "/home/m/.local/share/engine/repos/o/r" {
		t.Errorf("Path = %q, %v", path, err)
	}
	if _, err := (Repos{}).Path(repo); err == nil {
		t.Error("no home: want an error")
	}
	if got := r.url(repo); got != "https://github.com/o/r.git" {
		t.Errorf("default url = %q", got)
	}
	r.BaseURL = "file:///srv/git/"
	if got := r.url(repo); got != "file:///srv/git/o/r.git" {
		t.Errorf("custom url = %q", got)
	}
}

func TestSyncFirstClone(t *testing.T) {
	f := &fake{replies: syncReplies()}
	r := newRepos(t, f)
	repo := mustRepo(t, "o/r")
	clone, err := r.Sync(repo)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.Home, ".local/share/engine/repos/o/r")
	if clone != (Clone{Repo: repo, Path: path, Default: "main"}) {
		t.Errorf("clone = %+v", clone)
	}
	want := []string{
		"git -c credential.helper= -c credential.helper=!gh auth git-credential clone --quiet https://github.com/o/r.git " + path,
		"gh api user --jq .login + \" \" + (.id | tostring)",
		"git config --local --replace-all credential.helper ",
		"git config --local --add credential.helper !gh auth git-credential",
		"git config --local user.name testbot",
		"git config --local user.email 42+testbot@users.noreply.github.com",
		"git fetch --quiet --prune origin",
		"git remote set-head origin --auto",
		"git symbolic-ref --short refs/remotes/origin/HEAD",
		"git checkout --quiet --detach origin/main",
	}
	if got := f.lines(); !reflect.DeepEqual(got, want) {
		t.Errorf("commands:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, c := range f.calls {
		if !reflect.DeepEqual(c.env, []string{"GH_TOKEN=tok", "GIT_TERMINAL_PROMPT=0"}) {
			t.Errorf("%q ran with env %v", c.line, c.env)
		}
	}
	if f.calls[0].dir != "" || f.calls[2].dir != path {
		t.Errorf("clone ran in %q and config in %q", f.calls[0].dir, f.calls[2].dir)
	}
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Errorf("no lock file beside the clone: %v", err)
	}
}

func TestSyncExistingCloneFetchesWithoutCloning(t *testing.T) {
	f := &fake{replies: syncReplies()}
	r := newRepos(t, f)
	repo := mustRepo(t, "o/r")
	path, _ := r.Path(repo)
	if err := os.MkdirAll(filepath.Join(path, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Sync(repo); err != nil {
		t.Fatal(err)
	}
	if f.has(" clone ") || !f.has("fetch --quiet --prune origin") || !f.has("remote get-url origin") {
		t.Errorf("commands: %v", f.lines())
	}
	// A second sync takes the lock again: the first released it.
	if _, err := r.Sync(repo); err != nil {
		t.Errorf("second sync: %v", err)
	}
}

func TestSyncFollowsTheDefaultBranch(t *testing.T) {
	f := &fake{replies: append([]reply{{key: "symbolic-ref", out: "origin/trunk\n"}}, syncReplies()...)}
	clone, err := newRepos(t, f).Sync(mustRepo(t, "o/r"))
	if err != nil || clone.Default != "trunk" || !f.has("detach origin/trunk") {
		t.Errorf("clone %+v, err %v, commands %v", clone, err, f.lines())
	}
}

func TestSyncFailures(t *testing.T) {
	repo := mustRepo(t, "o/r")
	tests := []struct {
		name    string
		key     string
		out     string
		pre     func(path string) error
		wantErr string
	}{
		{"clone fails", " clone ", "fatal: repository not found", nil, "clone o/r"},
		{"machine user unreadable", "gh api user", "HTTP 401", nil, "machine user"},
		{"machine user malformed", "gh api user", "", nil, "machine user"},
		{"config fails", "config --local user.email", "", nil, "configure"},
		{"fetch fails", "git fetch", "", nil, "fetch o/r"},
		{"set-head fails", "git remote set-head", "", nil, "find the default branch"},
		{"no default branch", "symbolic-ref", "refs/remotes/other/HEAD", nil, "find the default branch"},
		{"checkout fails", "git checkout", "", nil, "check out origin/main"},
		{"other origin", "remote get-url", "https://github.com/x/y.git", func(path string) error {
			return os.MkdirAll(filepath.Join(path, ".git"), 0o755)
		}, "is not a clone of https://github.com/o/r.git"},
		{"directory in the way", "", "", func(path string) error {
			return os.MkdirAll(path, 0o755)
		}, "exists and is not a git clone"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fake{replies: append([]reply{{key: tt.key, out: tt.out, err: errBoom}}, syncReplies()...)}
			if tt.key == "" {
				f.replies = syncReplies()
			}
			r := newRepos(t, f)
			path, _ := r.Path(repo)
			if tt.pre != nil {
				if err := tt.pre(path); err != nil {
					t.Fatal(err)
				}
			}
			_, err := r.Sync(repo)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestSyncRefusesAnUnexpectedDefaultBranchRef(t *testing.T) {
	for _, ref := range []string{"refs/remotes/other/HEAD", "origin/", ""} {
		f := &fake{replies: append([]reply{{key: "symbolic-ref", out: ref}}, syncReplies()...)}
		_, err := newRepos(t, f).Sync(mustRepo(t, "o/r"))
		if err == nil || !strings.Contains(err.Error(), "unexpected") {
			t.Errorf("ref %q: err = %v", ref, err)
		}
	}
}

func TestSyncRefusesAMalformedMachineUser(t *testing.T) {
	for _, out := range []string{"", "onlyone", "a b c"} {
		f := &fake{replies: append([]reply{{key: "gh api user", out: out}}, syncReplies()...)}
		_, err := newRepos(t, f).Sync(mustRepo(t, "o/r"))
		if err == nil || !strings.Contains(err.Error(), "unexpected") || f.has("config --local user") {
			t.Errorf("gh said %q: err = %v", out, err)
		}
	}
}

func TestSyncRemovesAFailedClone(t *testing.T) {
	f := &fake{replies: []reply{{key: " clone ", err: errBoom}}}
	f.onRun = func(c call) {
		if strings.Contains(c.line, " clone ") { // git leaves a partial directory
			_ = os.MkdirAll(strings.Fields(c.line)[len(strings.Fields(c.line))-1], 0o755)
		}
	}
	r := newRepos(t, f)
	repo := mustRepo(t, "o/r")
	if _, err := r.Sync(repo); err == nil {
		t.Fatal("want an error")
	}
	path, _ := r.Path(repo)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("partial clone left at %s: %v", path, err)
	}
}

func TestSyncRefusesWithoutAHome(t *testing.T) {
	f := &fake{}
	if _, err := (Repos{Run: f.run}).Sync(mustRepo(t, "o/r")); err == nil || len(f.calls) != 0 {
		t.Errorf("err %v, commands %v", err, f.lines())
	}
}

func TestSyncFailsWhenTheLockCannotBeTaken(t *testing.T) {
	f := &fake{replies: syncReplies()}
	r := newRepos(t, f)
	// A file where the owner's directory belongs: nothing can be created below it.
	if err := os.MkdirAll(filepath.Join(r.Home, ".local/share/engine/repos"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Home, ".local/share/engine/repos/o"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Sync(mustRepo(t, "o/r")); err == nil || len(f.calls) != 0 {
		t.Errorf("err %v, commands %v", err, f.lines())
	}
}

func TestExecAddsEnvAndWrapsFailures(t *testing.T) {
	out, err := RunCommand(t.TempDir(), []string{"ENGINE_TEST=hello"}, "sh", "-c", `printf %s "$ENGINE_TEST"`)
	if err != nil || out != "hello" {
		t.Errorf("out %q, err %v", out, err)
	}
	if _, err := RunCommand("", nil, "sh", "-c", "echo broken >&2; exit 3"); err == nil || !strings.Contains(err.Error(), "broken") {
		t.Errorf("err = %v, want the command's output in it", err)
	}
}
