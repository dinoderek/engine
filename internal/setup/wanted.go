package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/tracker"
)

// engineRepo is where the caller's commit must be, for another host to
// install it.
const engineRepo = "dinoderek/engine"

// configFile is a file of ~/.config/engine that setup copies or writes.
type configFile struct{ name, content string }

// wanted is what the caller has, which the host is brought to.
type wanted struct {
	commit string
	plugin string
	files  []configFile
	// toolchain is the Go go.mod names at commit; toolchainErr says why it
	// could not be read, which leaves the engine step open rather than refusing.
	toolchain    string
	toolchainErr error
}

// resolveWanted reads everything a remote setup copies from the caller, and
// refuses before any change when one is missing: a build that is not a clean
// pushed commit, a plugin not installed here, or a config file not written.
func (r *runner) resolveWanted() (wanted, error) {
	info := r.d.Info
	commit := info.Commit()
	switch {
	case len(commit) < shortRev:
		return wanted{}, refuse("this engine's build has no commit; install it from a pushed commit")
	case info.Dirty():
		return wanted{}, refuse("this engine was built from a modified working tree; build it from a pushed commit")
	}
	token, err := tracker.Token(r.d.Getenv, r.d.Home)
	if err != nil {
		return wanted{}, refuse("%v", err)
	}
	out, err := r.d.GH([]string{"GH_TOKEN=" + token}, "gh", "api", "repos/"+engineRepo+"/commits/"+commit, "--jq", ".sha")
	switch {
	case err != nil:
		return wanted{}, refuse("could not confirm commit %s is on GitHub (%s): %v", commit, engineRepo, err)
	case !strings.HasPrefix(strings.TrimSpace(out), commit):
		return wanted{}, refuse("commit %s is not on GitHub (%s); push it, or build this engine from a pushed commit", commit, engineRepo)
	}
	w := wanted{commit: commit}
	w.toolchain, w.toolchainErr = readToolchain(r.d.GH, token, strings.TrimSpace(out))
	if w.files, err = r.configFiles(); err != nil {
		return wanted{}, err
	}
	local := target{name: "this host", exec: r.d.Exec}
	res, err := local.sh("claude plugin list --json", "")
	if err != nil {
		return wanted{}, err
	}
	version, found := userPlugin(res.out)
	if !found {
		return wanted{}, refuse("the %s plugin is not installed for the user on this host, so there is no version to bring %s to", pluginID, r.t.name)
	}
	w.plugin = version
	return w, nil
}

// configFiles are the files a host gets: hosts.toml and the token, copied,
// and its own name for the host file.
func (r *runner) configFiles() ([]configFile, error) {
	dir := hosts.ConfigDir(r.d.Home)
	files := []configFile{{name: "host", content: r.t.name + "\n"}}
	for _, name := range []string{"hosts.toml", "github-token"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			return nil, refuse("%s does not exist; write it first (operator-setup.md)", filepath.Join(dir, name))
		}
		if err != nil {
			return nil, refuse("read %s: %v", name, err)
		}
		files = append(files, configFile{name: name, content: string(data)})
	}
	return files, nil
}

// copyConfig writes the files the host lacks or has different, each with mode
// 600, and leaves the rest alone.
func (r *runner) copyConfig() (outcome, error) {
	var wrote []string
	for _, f := range r.want.files {
		did, res, err := r.syncFile(f)
		if err != nil {
			return outcome{}, err
		}
		if res.code != 0 {
			path := filepath.Join(hosts.ConfigDir(r.d.Home), f.name)
			return open("write "+f.name+" failed: "+res.why(), "ssh "+r.t.address+" 'mkdir -p ~/.config/engine' && scp -p "+path+" "+r.t.address+":.config/engine/"+f.name), nil
		}
		if did {
			wrote = append(wrote, f.name)
		}
	}
	if len(wrote) == 0 {
		return passed(""), nil
	}
	return changed("wrote " + strings.Join(wrote, ", ")), nil
}

// configDir is the host's config directory, as its shell writes it.
const configDir = `"$HOME/.config/engine"`

// syncFile makes the file on the host equal to f with mode 600, and reports
// whether it had to write. The result is the failed write, when it failed.
func (r *runner) syncFile(f configFile) (bool, result, error) {
	path := configDir + "/" + f.name
	same, err := r.t.sh("cmp -s - "+path, f.content)
	if err != nil {
		return false, result{}, err
	}
	if same.code == 0 {
		perm, err := r.t.sh("find "+path+" -maxdepth 0 -perm 600", "")
		if err != nil || strings.TrimSpace(perm.out) != "" {
			return false, result{}, err
		}
		res, err := r.t.sh("chmod 600 "+path, "")
		return err == nil && res.code == 0, res, err
	}
	tmp := path + ".tmp"
	res, err := r.t.sh("umask 077 && mkdir -p "+configDir+" && cat > "+tmp+" && chmod 600 "+tmp+" && mv "+tmp+" "+path, f.content)
	return err == nil && res.code == 0, res, err
}

var (
	toolchainLine = regexp.MustCompile(`(?m)^toolchain[ \t]+(\S+)[ \t]*$`)
	// goName is a Go release name such as go1.27.3 or go1.28rc1: the only text
	// that goes into the install command.
	goName = regexp.MustCompile(`^go[0-9][0-9a-z.]*$`)
)

// readToolchain is the Go that go.mod's toolchain line names at the commit,
// read from GitHub, which has already confirmed the commit.
func readToolchain(gh tracker.Runner, token, commit string) (string, error) {
	out, err := gh([]string{"GH_TOKEN=" + token}, "gh", "api", "-H", "Accept: application/vnd.github.raw",
		"repos/"+engineRepo+"/contents/go.mod?ref="+commit)
	if err != nil {
		return "", fmt.Errorf("could not read go.mod at %s (%s): %w", commit[:shortRev], engineRepo, err)
	}
	m := toolchainLine.FindStringSubmatch(out)
	switch {
	case m == nil:
		return "", fmt.Errorf("go.mod at %s has no toolchain line", commit[:shortRev])
	case !goName.MatchString(m[1]):
		return "", fmt.Errorf("go.mod at %s names the toolchain %q, which is not a Go release", commit[:shortRev], m[1])
	}
	return m[1], nil
}
