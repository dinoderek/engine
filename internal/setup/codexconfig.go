package setup

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// reposDir is where engine keeps its clones and worktrees, under the home.
const reposDir = ".local/share/engine/repos"

// configHeader opens the block of engine's Codex config.toml that setup owns.
const configHeader = "# engine setup writes this block; Codex's own settings follow it.\n"

// engineBlock is the settings engine's Codex sessions need: the launch line's
// workspace-write sandbox with auto-review (the sandbox exceptions are network
// access, and engine's clones, whose git metadata sits outside a worktree).
// Hook trust is left to the launch.
func engineBlock(home string) string {
	return configHeader +
		"approval_policy = \"on-request\"\n" +
		"approvals_reviewer = \"auto_review\"\n" +
		"sandbox_mode = \"workspace-write\"\n" +
		"\n" +
		"[sandbox_workspace_write]\n" +
		"network_access = true\n" +
		fmt.Sprintf("writable_roots = [%q]\n", home+"/"+reposDir)
}

// withEngineBlock is config with the block at its top and the rest left as
// Codex wrote it: the marketplace and plugin tables it adds, and any folder
// trust. An earlier block is replaced: it runs from the header to the first
// table other than its own, so a top-level key Codex adds later, or one in a
// file with no header, falls inside the block and is dropped by the next run.
func withEngineBlock(config, block string) string {
	rest := config
	if strings.HasPrefix(config, configHeader) {
		lines := strings.SplitAfter(config, "\n")
		i := 1
		for ; i < len(lines); i++ {
			if t := strings.TrimSpace(lines[i]); strings.HasPrefix(t, "[") && t != "[sandbox_workspace_write]" {
				break
			}
		}
		rest = strings.Join(lines[i:], "")
	}
	rest = strings.TrimLeft(rest, "\n")
	if rest == "" {
		return block
	}
	return block + "\n" + rest
}

// codexPlugin is one entry of `codex plugin list --json`.
type codexPlugin struct {
	PluginID string `json:"pluginId"`
	Version  string `json:"version"`
	Enabled  bool   `json:"enabled"`
}

// installedCodexPlugin is the version of engine's plugin in the list, and
// whether it is installed and enabled.
func installedCodexPlugin(listJSON string) (string, bool) {
	var list struct {
		Installed []codexPlugin `json:"installed"`
	}
	if json.Unmarshal([]byte(listJSON), &list) != nil {
		return "", false
	}
	for _, p := range list.Installed {
		if p.PluginID == pluginID && p.Enabled {
			return p.Version, true
		}
	}
	return "", false
}

var (
	ansiColor  = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	ignoredKey = regexp.MustCompile("\\((.+?)\\): `([^`]+)` is ignored")
	rcErrored  = regexp.MustCompile(`Remote control is enabled on .+ but the connection is errored`)
)

// remoteErrored is the state of a connection that is not up.
const remoteErrored = "errored"

// remoteState reads the output of `codex remote-control --json`: the status it
// prints, errored for the exit that says the connection is, else unreadable.
func remoteState(res result) string {
	var reply struct {
		Status string `json:"status"`
	}
	if json.Unmarshal([]byte(res.out), &reply) == nil && reply.Status != "" {
		return reply.Status
	}
	if rcErrored.MatchString(ansiColor.ReplaceAllString(res.errOut, "")) {
		return remoteErrored
	}
	return "unreadable"
}

// operatorNotes are the things about the operator's own Codex that setup
// reports and does not fix: a remote-control connection that is not up, and
// config keys Codex ignores. They come from the output of `codex
// remote-control --json` on the operator's home.
func operatorNotes(res result) []string {
	var notes []string
	if state := remoteState(res); state != "connected" && state != "unreadable" {
		notes = append(notes, "the operator's remote control is "+state)
	}
	for _, m := range ignoredKey.FindAllStringSubmatch(ansiColor.ReplaceAllString(res.errOut, ""), -1) {
		notes = append(notes, fmt.Sprintf("%s is ignored in %s", m[2], m[1]))
	}
	return notes
}

// atLeast reports whether version v is at least want, comparing dotted numbers.
// An empty want asks for nothing. Versions that are not dotted numbers must be
// equal.
func atLeast(v, want string) bool {
	if want == "" || v == want {
		return true
	}
	have, need := strings.Split(v, "."), strings.Split(want, ".")
	for i := 0; i < len(have) || i < len(need); i++ {
		a, b := part(have, i), part(need, i)
		if a < 0 || b < 0 {
			return false
		}
		if a != b {
			return a > b
		}
	}
	return true
}

// part is the i-th number of a dotted version: 0 past its end, -1 when it is
// not a number.
func part(fields []string, i int) int {
	if i >= len(fields) {
		return 0
	}
	n, err := strconv.Atoi(fields[i])
	if err != nil || n < 0 {
		return -1
	}
	return n
}
