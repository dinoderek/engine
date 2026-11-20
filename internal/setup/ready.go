package setup

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/session"
)

// shimsCommand prints the home directory, then each version manager's shim
// directory the host has: mise's, asdf's, and every nvm node's bin.
const shimsCommand = `echo "home $HOME"; for d in "$HOME/.local/share/mise/shims" "$HOME/.asdf/shims" "$HOME"/.nvm/versions/node/*/bin; do [ -d "$d" ] && echo "$d"; done; :`

// path suggests the host's hosts.toml path when it has version manager shims
// that path leaves out: tools installed through them are on no session's
// PATH otherwise. It never opens: the readiness probe shows whether it
// matters.
func (r *runner) path() (harness.Outcome, error) {
	res, err := r.t.Sh(shimsCommand, "")
	if err != nil {
		return harness.Outcome{}, err
	}
	lines := res.Lines()
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "home ") {
		return harness.Passed("could not list shim directories: " + res.Why()), nil
	}
	home := strings.TrimPrefix(lines[0], "home ")
	have, err := r.host.PathPrefix(home)
	if err != nil {
		return harness.Passed(err.Error()), nil
	}
	var missing []string
	for _, dir := range shimDirs(lines[1:]) {
		if !slices.Contains(have, dir) {
			missing = append(missing, `"`+tilde(dir, home)+`"`)
		}
	}
	if len(missing) == 0 {
		return harness.Passed(""), nil
	}
	return harness.Passed(fmt.Sprintf("suggested: tools installed through version manager shims reach sessions only through "+
		"the host's path; add path = [%s] to [host.%s] in hosts.toml, then rerun %s", strings.Join(missing, ", "), r.host.Name, r.rerun())), nil
}

// shimDirs keeps mise's and asdf's shims, and the newest nvm node's bin.
func shimDirs(found []string) []string {
	var out, nvm []string
	for _, dir := range found {
		if strings.Contains(dir, "/.nvm/versions/node/") {
			nvm = append(nvm, dir)
		} else {
			out = append(out, dir)
		}
	}
	if len(nvm) > 0 {
		out = append(out, slices.MaxFunc(nvm, compareNode))
	}
	return out
}

// compareNode orders nvm's node directories by their version, v<major>.<minor>.<patch>.
func compareNode(a, b string) int {
	va, vb := nodeVersion(a), nodeVersion(b)
	for i := range va {
		if va[i] != vb[i] {
			return va[i] - vb[i]
		}
	}
	return strings.Compare(a, b)
}

func nodeVersion(dir string) [3]int {
	var v [3]int
	_, rest, _ := strings.Cut(dir, "/.nvm/versions/node/v")
	name, _, _ := strings.Cut(rest, "/")
	for i, part := range strings.SplitN(name, ".", 3) {
		v[i], _ = strconv.Atoi(part)
	}
	return v
}

// tilde writes dir under home as ~/..., as hosts.toml takes it.
func tilde(dir, home string) string {
	if rest, ok := strings.CutPrefix(dir, home+"/"); ok {
		return "~/" + rest
	}
	return dir
}

// clonesCommand lists the repos engine has cloned on the host for the
// server, as owner/name.
var clonesCommand = `cd "$HOME/` + session.ControlReposDir + `" 2>/dev/null && for d in */*/.git; do [ -d "$d" ] && echo "${d%/.git}"; done; :`

// readiness runs the coordinator's readiness probe, afresh, for every repo
// engine has cloned on the host, once nothing above is open: a probe cannot
// pass past a missing tool. A fresh result replaces the host's cached one,
// so a fixed host takes work again at the coordinator's next ask.
func (r *runner) readiness(out io.Writer) (harness.Outcome, error) {
	if r.opened > 0 {
		return harness.Open("not run while items above are open", r.rerun()), nil
	}
	res, err := r.t.Sh(clonesCommand, "")
	if err != nil {
		return harness.Outcome{}, err
	}
	repos := res.Lines()
	if len(repos) == 0 {
		return harness.Passed("no repo is cloned here yet; the coordinator probes each before it places work"), nil
	}
	var unready []string
	for _, repo := range repos {
		ok, err := r.d.Ready(repo, out)
		var unreachable *remote.UnreachableError
		switch {
		case errors.As(err, &unreachable):
			return harness.Outcome{}, err
		case err != nil:
			return harness.Open(fmt.Sprintf("the probe of %s could not run: %v", repo, err), r.rerun()), nil
		case !ok:
			unready = append(unready, repo)
		}
	}
	if len(unready) > 0 {
		return harness.Open("unready for "+strings.Join(unready, ", ")+"; fix what the report above shows, or remove the clone in ~/"+
			session.ControlReposDir+" of a repo engine no longer runs", r.rerun()), nil
	}
	return harness.Passed("ready for " + strings.Join(repos, ", ")), nil
}
