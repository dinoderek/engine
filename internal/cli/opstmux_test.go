package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dinoderek/engine/internal/session"
)

// paneWindow is one window of a host's fake tmux server.
type paneWindow struct {
	id, pane, name, dir, command, log string
	dead                              bool
	code                              int
	// screen is what capture-pane shows; input is text typed and not yet
	// sent, and sent is every line Enter sent, in order.
	screen, input string
	sent          []string
}

// paneServer is a host's tmux server as engine drives it, through the tmux
// command lines session.Tmux runs. A window whose name is a readiness
// probe's runs its command for real, with sh, when its pane is respawned,
// and is dead with the command's exit code once it returns; every other
// window keeps its command as text and lives until the test ends it.
type paneServer struct {
	mu   sync.Mutex
	home string
	// up is a server running, with pid; cwd is its working directory.
	up  bool
	pid int
	cwd string
	// session is the session engine existing; next numbers windows and panes.
	session bool
	next    int
	windows []*paneWindow
	// respawnFails is how many respawn-pane calls still fail.
	respawnFails int
	calls        []string
}

func newPaneServer(home string, pid int) *paneServer {
	return &paneServer{home: home, pid: pid, cwd: home}
}

// tmux is the session.Tmux that drives this server.
func (p *paneServer) tmux() *session.Tmux {
	return &session.Tmux{Run: p.run, Bin: "tmux", Home: p.home, Sleep: func(time.Duration) {}}
}

func noServer(command string) error {
	return &session.ExitError{Command: "tmux " + command, Code: 1, Stderr: "no server running on /tmp/tmux-1/default"}
}

// run answers one tmux command line.
func (p *paneServer) run(_ string, args ...string) (string, error) {
	if args[0] == "-L" {
		args = args[2:]
	}
	p.mu.Lock()
	p.calls = append(p.calls, strings.Join(args, " "))
	if args[0] == "respawn-pane" {
		return p.respawn(args)
	}
	defer p.mu.Unlock()
	switch args[0] {
	case "has-session", "list-windows", "list-panes", "display-message", "capture-pane":
		return p.read(args)
	case "new-session", "new-window":
		p.up, p.session = true, true
		w := p.add(args)
		return w.id + " " + w.pane + "\n", nil
	case "split-window":
		w := p.byTarget(args[slicesIndex(args, "-t")+1])
		p.next++
		w.pane = fmt.Sprintf("%%%d", p.next)
		return w.pane + "\n", nil
	case "pipe-pane":
		w := p.byTarget(args[2])
		w.log = strings.Trim(strings.TrimPrefix(strings.ReplaceAll(args[3], "##", "#"), "cat >> "), "'")
	case "send-keys":
		p.keys(args)
	case "kill-window":
		p.kill(args[2])
	}
	return "", nil
}

// read answers the commands that only read.
func (p *paneServer) read(args []string) (string, error) {
	switch {
	case !p.up:
		return "", noServer(args[0])
	case args[0] == "has-session" && !p.session:
		return "", &session.ExitError{Command: "tmux has-session", Code: 1, Stderr: "can't find session: engine"}
	case args[0] == "display-message" && args[len(args)-1] == "#{pid}":
		return fmt.Sprintf("%d\n", p.pid), nil
	case args[0] == "display-message":
		return "0\n", nil
	case args[0] == "capture-pane":
		return p.byTarget(args[slicesIndex(args, "-t")+1]).screen, nil
	case args[0] == "list-windows" || args[0] == "list-panes":
		return p.list(args[len(args)-1]), nil
	}
	return "", nil
}

func slicesIndex(args []string, s string) int {
	for i, a := range args {
		if a == s {
			return i
		}
	}
	return -1
}

func (p *paneServer) list(format string) string {
	var b strings.Builder
	for _, w := range p.windows {
		dead := map[bool]string{true: "1", false: "0"}[w.dead]
		switch {
		case strings.Contains(format, "pane_dead_status"):
			code := ""
			if w.dead {
				code = fmt.Sprint(w.code)
			}
			fmt.Fprintf(&b, "%s %s %s\n", w.id, dead, code)
		case strings.HasPrefix(format, "server="):
			fmt.Fprintf(&b, "server=%d engine:%s %s pid=1 tty=/dev/ttys1 cwd=%s\n", p.pid, w.id, w.pane, w.dir)
		default:
			fmt.Fprintf(&b, "%s %s %s %s\n", w.id, w.pane, dead, w.name)
		}
	}
	return b.String()
}

func (p *paneServer) add(args []string) *paneWindow {
	p.next++
	w := &paneWindow{id: fmt.Sprintf("@%d", p.next), name: args[slicesIndex(args, "-n")+1], dir: args[slicesIndex(args, "-c")+1]}
	p.next++
	w.pane = fmt.Sprintf("%%%d", p.next)
	p.windows = append(p.windows, w)
	return w
}

// byTarget is the window a target names: =engine:@3, @3 or a pane %5.
func (p *paneServer) byTarget(target string) *paneWindow {
	target = strings.TrimPrefix(target, "="+session.Name+":")
	for _, w := range p.windows {
		if w.id == target || w.pane == target {
			return w
		}
	}
	return &paneWindow{}
}

func (p *paneServer) kill(target string) {
	w := p.byTarget(target)
	for i, x := range p.windows {
		if x == w {
			p.windows = append(p.windows[:i], p.windows[i+1:]...)
			// The session ends with its last window, and the server with it.
			if len(p.windows) == 0 {
				p.session, p.up = false, false
			}
			return
		}
	}
}

func (p *paneServer) keys(args []string) {
	if args[1] == "-X" {
		return
	}
	w := p.byTarget(args[2])
	if args[len(args)-1] == "Enter" && args[len(args)-2] == args[2] {
		w.sent = append(w.sent, w.input)
		w.input = ""
		return
	}
	w.input += args[len(args)-1]
}

// respawn puts the window's command in place, and runs a probe's to its
// end. It is called with the lock held and releases it.
func (p *paneServer) respawn(args []string) (string, error) {
	defer p.mu.Unlock()
	if p.respawnFails > 0 {
		p.respawnFails--
		return "", &session.ExitError{Command: "tmux respawn-pane", Code: 1, Stderr: "create pane failed: fork failed: No such file or directory"}
	}
	w := p.byTarget(args[3])
	w.command = args[len(args)-1]
	if !strings.HasPrefix(w.name, "engine-ready-") {
		return "", nil
	}
	cmd := exec.Command("/bin/sh", "-c", w.command)
	cmd.Dir, cmd.Env = w.dir, []string{"HOME=" + p.home, "PATH=" + os.Getenv("PATH")}
	out, err := cmd.CombinedOutput()
	w.dead, w.screen = true, string(out)
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		w.code = exit.ExitCode()
	}
	return "", nil
}

// window is the live or dead window called name, or nil.
func (p *paneServer) window(name string) *paneWindow {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, w := range p.windows {
		if w.name == name {
			return w
		}
	}
	return nil
}

// end has the window's command exit with code, after printing tail to its
// pane and its pane log.
func (p *paneServer) end(name string, code int, tail string) {
	w := p.window(name)
	p.mu.Lock()
	defer p.mu.Unlock()
	w.dead, w.code, w.screen = true, code, tail
	if w.log != "" {
		_ = os.MkdirAll(filepath.Dir(w.log), 0o755)
		f, err := os.OpenFile(w.log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = f.WriteString(tail)
			_ = f.Close()
		}
	}
}

// live is the names of the windows whose command still runs.
func (p *paneServer) live() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, w := range p.windows {
		if !w.dead {
			out = append(out, w.name)
		}
	}
	return out
}
