package codex

import (
	"strconv"
	"strings"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

// Codex keeps engine's plugin in one directory per version, under its home's
// plugins/cache, and `codex plugin add` removes every version but the one it
// adds. A session started before an update still reads its skills from the
// version it started with, so setup keeps the old versions in engine's Codex
// home, which the sessions that join engine's daemon use: it copies them
// aside before the add and puts them back after, and records when it did.
// Every later setup removes them once no pane of session `engine` on the
// host has run its process since before then.

// pluginCache is where Codex keeps engine's plugin in the Codex home whose
// shell word is home: plugins/cache/<marketplace>/<plugin>.
func pluginCache(home string) string {
	plugin, market, _ := strings.Cut(harness.PluginID, "@")
	return home + "/plugins/cache/" + market + "/" + plugin
}

// keptDir holds the versions copied aside while Codex adds the plugin.
func keptDir(home string) string { return home + "/plugins/.engine-kept" }

// installedMark records, in Unix seconds, when setup last added the plugin
// and put the older versions back.
func installedMark(home string) string { return home + "/plugins/.engine-installed" }

// putBack is the shell that puts each version copied aside ($k) that the
// cache ($d) lacks back into it, then removes the copy.
const putBack = `if [ -d "$k" ]; then mkdir -p "$d" && for v in "$k"/*; do [ -e "$v" ] || continue; [ -e "$d/${v##*/}" ] || cp -Rp "$v" "$d/" || exit 1; done; rm -rf "$k"; fi`

// keepCommand copies every version of the plugin in home aside, before an
// add removes them. A copy an earlier setup left aside, cut off before its
// restore, goes back first: it may hold the only copy of a version.
func keepCommand(home string) string {
	return `k=` + keptDir(home) + `; d=` + pluginCache(home) + `; ` + putBack +
		` && if [ -d "$d" ]; then mkdir -p "$k" && cp -Rp "$d"/. "$k"/; fi`
}

// restoreCommand puts back each version copied aside that the add removed,
// and records when.
func restoreCommand(home string) string {
	return `k=` + keptDir(home) + `; d=` + pluginCache(home) + `; ` + putBack +
		` && mkdir -p ` + home + `/plugins && date +%s > ` + installedMark(home)
}

// windowAgesCommand prints the host's clock in Unix seconds, then, one a
// line, how long each pane of session engine has run its process, as ps's
// [[dd-]hh:]mm:ss elapsed time (tmux keeps no window start time), or
// "unknown" for a pane whose process ps cannot read. It prints only the
// clock when no tmux server runs, and exits 3 when tmux is missing, and
// nothing can be told.
var windowAgesCommand = `command -v tmux >/dev/null 2>&1 || exit 3; date +%s; for p in $(tmux list-panes -s -t =` + session.Name +
	` -F '#{pane_pid}' 2>/dev/null); do ps -o etime= -p "$p" 2>/dev/null || echo unknown; done; exit 0`

// pruneCommand removes every version of the plugin in home but current, and
// the record of the install that kept them.
func pruneCommand(home, current string) string {
	return `d=` + pluginCache(home) + `; for v in "$d"/*; do [ -e "$v" ] || continue; [ "${v##*/}" = ` + hosts.ShellWord(current) +
		` ] || rm -rf "$v" || exit 1; done; rm -f ` + installedMark(home)
}

// keptSince reports whether a window that started at or before since may
// still read a version kept then, from windowAgesCommand's reply: tmux is
// missing, a pane's age cannot be read, or a pane started that early.
// Anything it cannot read keeps the versions.
func keptSince(res harness.Result, since int64) bool {
	lines := res.Lines()
	if res.Code != 0 || len(lines) == 0 {
		return true
	}
	now, err := strconv.ParseInt(strings.TrimSpace(lines[0]), 10, 64)
	if err != nil {
		return true
	}
	for _, line := range lines[1:] {
		age, err := parseElapsed(line)
		if err != nil || now-int64(age) <= since {
			return true
		}
	}
	return false
}

// prune removes the versions setup kept in h, other than current, once no
// window on the host started before they were kept. A home that keeps none,
// one with nothing kept, or no current version, has nothing to remove.
func (r *setupRun) prune(h pluginHome, current string) error {
	if h.home == "" || current == "" {
		return nil
	}
	mark, err := r.t.Sh("cat "+installedMark(h.home), "")
	if err != nil || mark.Code != 0 {
		return err
	}
	since, perr := strconv.ParseInt(strings.TrimSpace(mark.Out), 10, 64)
	if perr != nil {
		since = 1<<63 - 1 // an unreadable record keeps everything
	}
	windows, err := r.t.Sh(windowAgesCommand, "")
	if err != nil || keptSince(windows, since) {
		return err
	}
	_, err = r.t.Sh(pruneCommand(h.home, current), "")
	return err
}
