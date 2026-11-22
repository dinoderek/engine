package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/harness/claude"
	"github.com/dinoderek/engine/internal/session"
)

// opsHost is one machine of the operations fleet: its home, holding
// engine's config and data, its tmux server, its Claude Code CLI and its
// PATH prefix, ~/fakebin, which holds the gh a readiness probe runs.
type opsHost struct {
	f    *opsFleet
	name string
	home string
	tmux *paneServer
	// down is ssh failing to connect.
	down bool
	mu   sync.Mutex
	// agents is each Claude session's status by window name, busy when
	// unset.
	agents map[string]string
}

func newOpsHost(f *opsFleet, name string, pid int) *opsHost {
	h := &opsHost{f: f, name: name, home: f.t.TempDir(), agents: map[string]string{}}
	h.tmux = newPaneServer(h.home, pid)
	writeExec(f.t, filepath.Join(h.home, "bin", "claude"), "#!/bin/sh\nexit 0\n")
	writeExec(f.t, filepath.Join(h.home, "fakebin", "gh"), "#!/bin/sh\necho 'github.com: Logged in to github.com account fleet'\nexit 0\n")
	writeFile(f.t, filepath.Join(h.home, ".gitconfig"), "[user]\n\tname = Fleet\n\temail = fleet@example.test\n[init]\n\tdefaultBranch = main\n")
	return h
}

func writeExec(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

// env is the Env of an engine command run on this host.
func (h *opsHost) env(in io.Reader, out, errOut io.Writer) Env {
	return Env{In: in, Out: out, Err: errOut, Home: h.home, Getenv: func(string) string { return "" },
		Now: h.f.clock.now, Local: h.exec, Run: h.run, Tmux: h.tmux.tmux(), Sleep: func(time.Duration) {}}
}

// ssh is Env.SSH and the coordinator's ssh: `engine host <op>` on the host
// at the address, through Run with that host's Env, as its engine runs it.
func (f *opsFleet) ssh(in io.Reader, out, errOut io.Writer, name string, args ...string) (int, error) {
	if name != "ssh" {
		f.t.Errorf("ran %s %q outside ssh", name, args)
		return -1, errors.New("not in this fleet")
	}
	h := f.hosts[strings.TrimPrefix(args[len(args)-2], "dev@")]
	if h == nil || h.down {
		fmt.Fprintf(errOut, "ssh: connect to host %s port 22: No route to host\n", args[len(args)-2])
		return 255, nil
	}
	words := shellWords(args[len(args)-1])
	if len(words) < 2 || words[0] != "engine" {
		f.t.Errorf("%s: unexpected command %q", h.name, args[len(args)-1])
		return 127, nil
	}
	if in == nil {
		in = strings.NewReader("")
	}
	return Run(words[1:], h.env(in, out, errOut)), nil
}

// shellWords splits a command line that remote.ShellJoin wrote.
func shellWords(line string) []string {
	var words []string
	var word strings.Builder
	quoted, inWord := false, false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '\'':
			quoted, inWord = !quoted, true
		case c == '\\' && !quoted && i+1 < len(line):
			i++
			word.WriteByte(line[i])
		case c == ' ' && !quoted:
			if inWord {
				words = append(words, word.String())
				word.Reset()
			}
			inWord = false
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		words = append(words, word.String())
	}
	return words
}

// exec is the host's own CLIs: Claude Code's, and what reads the tmux
// server's directory and cgroup.
func (h *opsHost) exec(name string, args ...string) (string, error) {
	line := strings.Join(args, " ")
	switch {
	case name == filepath.Join(h.home, "bin", "claude") && line == "auth status":
		return `{"loggedIn":true}`, nil
	case name == filepath.Join(h.home, "bin", "claude") && line == "agents --json":
		return h.agentList(), nil
	case name == "lsof":
		return fmt.Sprintf("p%d\nfcwd\nn%s\n", h.tmux.pid, h.tmux.cwd), nil
	case name == "readlink":
		return h.tmux.cwd + "\n", nil
	case name == "cat" && line == fmt.Sprintf("/proc/%d/cgroup", h.tmux.pid):
		return "0::/user.slice/user-1000.slice/user@1000.service/app.slice/run-p1-i2.scope\n", nil
	}
	h.f.t.Errorf("%s: ran %s %s", h.name, name, line)
	return "", errors.New("not in this fleet")
}

// agentList is `claude agents --json`: a session in every live session
// window, in its directory.
func (h *opsHost) agentList() string {
	h.tmux.mu.Lock()
	defer h.tmux.mu.Unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	agents := []claude.Agent{}
	for _, w := range h.tmux.windows {
		if w.dead || strings.HasPrefix(w.name, "engine-ready-") {
			continue
		}
		status := h.agents[w.name]
		if status == "" {
			status = claude.AgentBusy
		}
		agents = append(agents, claude.Agent{PID: 100, CWD: w.dir, Name: w.name, SessionID: "s-" + w.name, Status: status})
	}
	data, _ := json.Marshal(agents)
	return string(data)
}

// setAgent sets what Claude Code says the window's session is doing.
func (h *opsHost) setAgent(window, status string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.agents[window] = status
}

// run is the host's git, for real, under its own global config, and gh,
// which the fleet's GitHub answers.
func (h *opsHost) run(dir string, env []string, name string, args ...string) (string, error) {
	if name == "gh" {
		return h.f.github.run(args...)
	}
	return session.RunCommand(dir, append(append([]string{}, env...), h.gitEnv()...), name, args...)
}

func (h *opsHost) gitEnv() []string {
	return []string{"HOME=" + h.home, "GIT_CONFIG_GLOBAL=" + filepath.Join(h.home, ".gitconfig"), "GIT_CONFIG_NOSYSTEM=1"}
}

// clone is engine's clone of the fleet's repo on this host.
func (h *opsHost) clone() string {
	return filepath.Join(h.home, ".local/share/engine/control-repos", filepath.FromSlash(opsSlug))
}

// branchAt is the commit the branch is at in this host's clone, or "" when
// the clone has no such branch.
func (h *opsHost) branchAt(branch string) string {
	out, err := h.run(h.clone(), nil, "git", "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// git runs git on this host, failing the test on an error.
func (h *opsHost) git(dir string, args ...string) string {
	h.f.t.Helper()
	out, err := h.run(dir, nil, "git", args...)
	if err != nil {
		h.f.t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

// engine runs an engine command on this host with the variables in vars,
// and returns its exit code, output and errors.
func (h *opsHost) engine(vars map[string]string, in string, args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	env := h.env(strings.NewReader(in), &out, &errOut)
	env.Getenv = func(k string) string { return vars[k] }
	code := Run(args, env)
	return code, out.String(), errOut.String()
}
