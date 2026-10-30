// Package session runs engine's sessions on this machine. It reaches git and
// gh only through a RunFunc, so tests replace them.
package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// ReposDir is where this host keeps engine's clones, relative to the home
// directory: one clone per repo, at <owner>/<repo> below it.
const ReposDir = ".local/share/engine/repos"

// GitHubURL is where engine clones from, over HTTPS.
const GitHubURL = "https://github.com"

// credentialHelper makes git ask gh, which answers from GH_TOKEN, so the
// clone acts as the machine user whatever else the host has configured.
const credentialHelper = "!gh auth git-credential"

var namePart = regexp.MustCompile(`^[\w.-]+$`)

// Repo is a GitHub repository.
type Repo struct {
	Owner, Name string
}

// ParseRepo parses "owner/repo".
func ParseRepo(s string) (Repo, error) {
	owner, name, ok := strings.Cut(s, "/")
	if !ok || !validPart(owner) || !validPart(name) {
		return Repo{}, fmt.Errorf("want owner/repo, got %q", s)
	}
	return Repo{Owner: owner, Name: name}, nil
}

// validPart accepts a GitHub owner or repo name, and refuses "." and "..",
// which would walk out of the clones directory.
func validPart(s string) bool {
	return namePart.MatchString(s) && s != "." && s != ".."
}

func (r Repo) String() string { return r.Owner + "/" + r.Name }

// Clone is engine's clone of a repo on this host.
type Clone struct {
	Repo    Repo
	Path    string // the clone's working tree: the main checkout for the repo's worktrees
	Default string // the remote's default branch, such as "main"
}

// Repos syncs engine's clones and creates task worktrees in them.
type Repos struct {
	Run RunFunc
	// Home holds the clones: <Home>/.local/share/engine/repos/<owner>/<repo>.
	Home string
	// Token is the machine user's token. It is passed to git and gh as
	// GH_TOKEN and never written to disk.
	Token string
	// BaseURL is the host that serves "<owner>/<repo>.git"; GitHubURL when empty.
	BaseURL string
	// Probe runs a repo's own `dev` with no verb, to read its usage. It should
	// carry a deadline (RunCommandWithin), since that `dev` is arbitrary code.
	// Nil uses Run.
	Probe RunFunc
}

// Path is where the clone of repo lives.
func (r Repos) Path(repo Repo) (string, error) {
	if r.Home == "" {
		return "", errors.New("no home directory to keep engine's clones in")
	}
	return filepath.Join(r.Home, filepath.FromSlash(ReposDir), repo.Owner, repo.Name), nil
}

func (r Repos) url(repo Repo) string {
	base := r.BaseURL
	if base == "" {
		base = GitHubURL
	}
	return strings.TrimRight(base, "/") + "/" + repo.String() + ".git"
}

// checkToken refuses an empty token: gh would then fall back to the host's own
// login, and the clone would act as the operator instead of the machine user.
func (r Repos) checkToken() error {
	if strings.TrimSpace(r.Token) == "" {
		return errors.New("no machine-user token: engine would act as whoever gh is signed in as")
	}
	return nil
}

// env is added to every git and gh call: the token, and no prompting for
// credentials, so a missing one fails instead of hanging.
func (r Repos) env() []string {
	return []string{"GH_TOKEN=" + r.Token, "GIT_TERMINAL_PROMPT=0"}
}

func (r Repos) git(dir string, args ...string) (string, error) {
	out, err := r.Run(dir, r.env(), "git", args...)
	return strings.TrimSpace(out), err
}

// Sync makes sure engine's clone of repo exists, fetches it, and leaves it
// checked out at the remote's default branch. A first sync clones over HTTPS.
// Every sync sets the clone's local config: gh as the only credential helper,
// and the machine user's name and noreply email. Syncs and worktree creation
// in one repo take turns, through a lock file beside the clone.
func (r Repos) Sync(repo Repo) (Clone, error) {
	if err := r.checkToken(); err != nil {
		return Clone{}, err
	}
	path, err := r.Path(repo)
	if err != nil {
		return Clone{}, err
	}
	unlock, err := lock(path + ".lock")
	if err != nil {
		return Clone{}, err
	}
	defer unlock()
	if err := r.ensureClone(repo, path); err != nil {
		return Clone{}, err
	}
	if err := r.configure(path); err != nil {
		return Clone{}, err
	}
	if _, err := r.git(path, "fetch", "--quiet", "--prune", "origin"); err != nil {
		return Clone{}, fmt.Errorf("fetch %s: %w", repo, err)
	}
	def, err := r.defaultBranch(path)
	if err != nil {
		return Clone{}, err
	}
	if _, err := r.git(path, "checkout", "--quiet", "--detach", "origin/"+def); err != nil {
		return Clone{}, fmt.Errorf("check out origin/%s in %s: %w", def, path, err)
	}
	return Clone{Repo: repo, Path: path, Default: def}, nil
}

// Open returns engine's clone of repo as the last Sync left it, without
// fetching: its default branch is the one that Sync recorded. It fails when
// there is no clone yet.
func (r Repos) Open(repo Repo) (Clone, error) {
	path, err := r.Path(repo)
	if err != nil {
		return Clone{}, err
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		return Clone{}, fmt.Errorf("no clone of %s at %s: sync it first", repo, path)
	}
	ref, err := r.git(path, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	def, ok := strings.CutPrefix(ref, "origin/")
	if err != nil || !ok || def == "" {
		return Clone{}, fmt.Errorf("the clone of %s at %s has no default branch: sync it first", repo, path)
	}
	return Clone{Repo: repo, Path: path, Default: def}, nil
}

// ensureClone clones repo to path unless a clone of it is already there, and
// checks that the clone's remote is the HTTPS URL and stays it.
func (r Repos) ensureClone(repo Repo, path string) error {
	want := r.url(repo)
	fresh, err := r.cloneIfMissing(repo, path, want)
	if err != nil {
		return err
	}
	if err := r.checkRemote(path, want); err != nil {
		if fresh {
			_ = os.RemoveAll(path)
		}
		return err
	}
	return nil
}

// cloneIfMissing clones want to path unless a clone is there, and reports
// whether it cloned.
func (r Repos) cloneIfMissing(repo Repo, path, want string) (bool, error) {
	_, err := os.Stat(filepath.Join(path, ".git"))
	switch {
	case err == nil:
		return false, nil
	case !errors.Is(err, os.ErrNotExist):
		return false, err
	}
	if _, err := os.Stat(path); err == nil {
		return false, fmt.Errorf("%s exists and is not a git clone", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	// A url.<base>.insteadOf rule would send the clone elsewhere, such as over
	// SSH with the operator's key. ls-remote --get-url applies it, so refuse first.
	if got, err := r.git("", "ls-remote", "--get-url", want); err != nil || got != want {
		return false, rewriteError(want, got, err)
	}
	// The helper is passed here too, because the clone itself needs the token.
	_, err = r.git("", "-c", "credential.helper=", "-c", "credential.helper="+credentialHelper,
		"clone", "--quiet", want, path)
	if err != nil {
		_ = os.RemoveAll(path) // a failed clone can leave a partial directory
		return false, fmt.Errorf("clone %s: %w", repo, err)
	}
	return true, nil
}

// checkRemote refuses a clone whose origin is not want, or that git would
// fetch from or push to anywhere else. A host's url.<base>.insteadOf or
// pushInsteadOf rule can do that quietly, and then engine's pushes would go
// out under the operator's own credentials instead of the machine user's.
func (r Repos) checkRemote(path, want string) error {
	raw, err := r.git(path, "config", "--get", "remote.origin.url")
	if err != nil || raw != want {
		return fmt.Errorf("%s is not a clone of %s (origin is %q)", path, want, raw)
	}
	for _, args := range [][]string{{"remote", "get-url", "origin"}, {"remote", "get-url", "--push", "origin"}} {
		if got, err := r.git(path, args...); err != nil || got != want {
			return rewriteError(want, got, err)
		}
	}
	return nil
}

func rewriteError(want, got string, err error) error {
	if err != nil {
		return fmt.Errorf("cannot resolve %s: %w", want, err)
	}
	return fmt.Errorf("git rewrites %s to %s (a url.<base>.insteadOf or pushInsteadOf rule); "+
		"remove the rule on this host, so engine's clones fetch and push over HTTPS as the machine user", want, got)
}

// configure sets the clone's local credential helper and commit identity.
// The empty helper first drops any helper the host configures globally, such
// as the operator's keychain, so pushes can only be the machine user's.
func (r Repos) configure(path string) error {
	login, email, err := r.machineUser()
	if err != nil {
		return err
	}
	steps := [][]string{
		{"config", "--local", "--replace-all", "credential.helper", ""},
		{"config", "--local", "--add", "credential.helper", credentialHelper},
		{"config", "--local", "user.name", login},
		{"config", "--local", "user.email", email},
	}
	for _, args := range steps {
		if _, err := r.git(path, args...); err != nil {
			return fmt.Errorf("configure %s: %w", path, err)
		}
	}
	return nil
}

// machineUser asks GitHub who the token belongs to, and returns the login
// and the user's noreply email, which GitHub links to the account.
func (r Repos) machineUser() (login, email string, err error) {
	out, err := r.Run("", r.env(), "gh", "api", "user", "--jq", `.login + " " + (.id | tostring)`)
	if err != nil {
		return "", "", fmt.Errorf("could not read the machine user from GitHub: %w", err)
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return "", "", fmt.Errorf("could not read the machine user from GitHub: unexpected %q", strings.TrimSpace(out))
	}
	return fields[0], fmt.Sprintf("%s+%s@users.noreply.github.com", fields[1], fields[0]), nil
}

// defaultBranch asks the remote, so a default branch renamed since the clone
// does not matter.
func (r Repos) defaultBranch(path string) (string, error) {
	if _, err := r.git(path, "remote", "set-head", "origin", "--auto"); err != nil {
		return "", fmt.Errorf("find the default branch: %w", err)
	}
	ref, err := r.git(path, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", fmt.Errorf("find the default branch: %w", err)
	}
	def, ok := strings.CutPrefix(ref, "origin/")
	if !ok || def == "" {
		return "", fmt.Errorf("find the default branch: unexpected %q", ref)
	}
	return def, nil
}

// lock takes an exclusive lock on the file at path, waiting for its holder,
// and returns the function that releases it. The kernel drops a lock with
// its process, so a crash leaves nothing stale.
func lock(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return func() { _ = f.Close() }, nil // closing the file releases the lock
}
