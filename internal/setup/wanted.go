package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

// EngineRepo is where the caller's commit must be, for another host to
// install it.
const EngineRepo = "dinoderek/engine"

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
	out, err := r.d.GH("", nil, "gh", "api", "repos/"+EngineRepo+"/commits/"+commit, "--jq", ".sha")
	switch {
	case err != nil:
		return wanted{}, refuse("could not confirm commit %s is on GitHub (%s): %v", commit, EngineRepo, err)
	case !strings.HasPrefix(strings.TrimSpace(out), commit):
		return wanted{}, refuse("commit %s is not on GitHub (%s); push it, or build this engine from a pushed commit", commit, EngineRepo)
	}
	w := wanted{commit: commit}
	w.toolchain, w.toolchainErr = ReadToolchain(r.d.GH, strings.TrimSpace(out))
	if w.files, err = r.configFiles(); err != nil {
		return wanted{}, err
	}
	version, err := r.callerPlugin()
	if err != nil {
		return wanted{}, err
	}
	if version == "" {
		return wanted{}, refuse("the %s plugin is not installed for the user on this host, so there is no version to bring %s to", harness.PluginID, r.t.name)
	}
	w.plugin = version
	return w, nil
}

// callerPlugin is the version of engine's plugin installed for the user on
// this host: the first harness, in the registry's order, that has one.
func (r *runner) callerPlugin() (string, error) {
	if r.d.Harnesses == nil {
		return "", nil
	}
	local := target{name: "this host", exec: r.d.Exec}
	for _, name := range r.d.Harnesses.Names() {
		a, _ := r.d.Harnesses.Get(name)
		version, found, err := a.Setup().Plugin(local)
		if err != nil || found {
			return version, err
		}
	}
	return "", nil
}

// configFiles are the files a host gets: hosts.toml, copied,
// and its own name for the host file.
func (r *runner) configFiles() ([]configFile, error) {
	dir := hosts.ConfigDir(r.d.Home)
	files := []configFile{{name: "host", content: r.t.name + "\n"}}
	for _, name := range []string{"hosts.toml"} {
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
func (r *runner) copyConfig() (harness.Outcome, error) {
	var wrote []string
	for _, f := range r.want.files {
		did, res, err := r.syncFile(f)
		if err != nil {
			return harness.Outcome{}, err
		}
		if res.Code != 0 {
			path := filepath.Join(hosts.ConfigDir(r.d.Home), f.name)
			return harness.Open("write "+f.name+" failed: "+res.Why(), "ssh "+r.t.address+" 'mkdir -p ~/.config/engine' && scp -p "+path+" "+r.t.address+":.config/engine/"+f.name), nil
		}
		if did {
			wrote = append(wrote, f.name)
		}
	}
	if len(wrote) == 0 {
		return harness.Passed(""), nil
	}
	return harness.Changed("wrote " + strings.Join(wrote, ", ")), nil
}

// configDir is the host's config directory, as its shell writes it.
const configDir = `"$HOME/.config/engine"`

// syncFile makes the file on the host equal to f with mode 600, and reports
// whether it had to write. The result is the failed write, when it failed.
func (r *runner) syncFile(f configFile) (bool, harness.Result, error) {
	path := configDir + "/" + f.name
	same, err := r.t.Sh("cmp -s - "+path, f.content)
	if err != nil {
		return false, harness.Result{}, err
	}
	if same.Code == 0 {
		perm, err := r.t.Sh("find "+path+" -maxdepth 0 -perm 600", "")
		if err != nil || strings.TrimSpace(perm.Out) != "" {
			return false, harness.Result{}, err
		}
		res, err := r.t.Sh("chmod 600 "+path, "")
		return err == nil && res.Code == 0, res, err
	}
	tmp := path + ".tmp"
	res, err := r.t.Sh("umask 077 && mkdir -p "+configDir+" && cat > "+tmp+" && chmod 600 "+tmp+" && mv "+tmp+" "+path, f.content)
	return err == nil && res.Code == 0, res, err
}

var (
	toolchainLine = regexp.MustCompile(`(?m)^toolchain[ \t]+(\S+)[ \t]*$`)
	// goName is a Go release name such as go1.27.3 or go1.28rc1: the only text
	// that goes into the install command.
	goName = regexp.MustCompile(`^go[0-9][0-9a-z.]*$`)
)

// ReadToolchain is the Go that go.mod's toolchain line names at the commit,
// read from GitHub, which has already confirmed the commit.
func ReadToolchain(gh session.RunFunc, commit string) (string, error) {
	out, err := gh("", nil, "gh", "api", "-H", "Accept: application/vnd.github.raw",
		"repos/"+EngineRepo+"/contents/go.mod?ref="+commit)
	if err != nil {
		return "", fmt.Errorf("could not read go.mod at %s (%s): %w", commit[:shortRev], EngineRepo, err)
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
