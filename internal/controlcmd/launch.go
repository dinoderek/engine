package controlcmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/session"
)

// ServerSocket is the private tmux server the control server runs in, apart
// from the session `engine`, whose windows are task sessions.
const ServerSocket = "engine-server"

// ServerWindow is the control server's window in ServerSocket.
const ServerWindow = "server"

// ServerLog is the control server's output, under home.
const ServerLog = ".local/share/engine/server/serve.log"

// serverSettle is how long a started server has to fail before engine serve
// reports it running.
const serverSettle = 2 * time.Second

// ServerDeps are what engine serve starts the server with.
type ServerDeps struct {
	// Tmux drives ServerSocket on this host.
	Tmux *session.Tmux
	// Exec runs ssh to reach the server's host, and systemctl on this host
	// when it records a service manager.
	Exec remote.Exec
	// Executable is this engine binary, which the window runs.
	Executable func() (string, error)
	Sleep      func(time.Duration)
}

// RealServerDeps are the real tmux, ssh and systemctl, binary and clock.
func RealServerDeps() ServerDeps {
	tm := session.New()
	tm.Socket = ServerSocket
	return ServerDeps{Tmux: tm, Exec: remote.ExecCommand, Executable: os.Executable, Sleep: time.Sleep}
}

func (e Env) serverDeps() ServerDeps {
	if e.Server != nil {
		return *e.Server
	}
	return RealServerDeps()
}

// launchServer starts the control server on the host config.toml names, or
// on onHost when engine serve hopped here over ssh, and returns. A server
// already running there is left as it is.
func launchServer(e Env, onHost string) int {
	err := startServer(e, onHost)
	var code exitCode
	switch {
	case errors.As(err, &code):
		return int(code)
	case err != nil:
		fmt.Fprintln(e.Err, "engine serve:", err)
		return 1
	}
	return 0
}

// exitCode is the remote engine serve's own exit code, already reported.
type exitCode int

func (c exitCode) Error() string { return fmt.Sprintf("exit status %d", int(c)) }

func startServer(e Env, onHost string) error {
	dir := hosts.ConfigDir(e.Home)
	cfg, local, err := hosts.Open(dir)
	if err != nil {
		return err
	}
	name := onHost
	if name == "" {
		s, err := hosts.ReadServer(dir)
		if err != nil {
			return err
		}
		if s.Host == "" {
			return errors.New("set [server] host in " + filepath.Join(dir, "config.toml") + " to the host that runs the server")
		}
		name = s.Host
	}
	h, ok := cfg.Hosts[name]
	if !ok {
		return fmt.Errorf("server host %q is not in hosts.toml", name)
	}
	d := e.serverDeps()
	if h.Name != local.Name {
		if onHost != "" {
			return fmt.Errorf("this host is %s, not %s; check %s", local.Name, onHost, filepath.Join(dir, "host"))
		}
		return forward(e, d, h)
	}
	return startHere(e, d, h)
}

// forward runs engine serve on h, over ssh, for it to start the server there.
func forward(e Env, d ServerDeps, h hosts.Host) error {
	ssh := remote.SSH{Name: h.Name, Address: h.Address, Exec: d.Exec}
	code, err := ssh.Run(nil, e.Out, e.Err, remote.ShellJoin([]string{"engine", "serve", "--on-host", h.Name}))
	switch {
	case err != nil:
		return err
	case code == 255:
		return fmt.Errorf("host %s (%s) did not answer over ssh", h.Name, h.Address)
	case code != 0:
		return exitCode(code)
	}
	return nil
}

func startHere(e Env, d ServerDeps, h hosts.Host) error {
	if h.Services.Recorded() {
		return startSystemd(e, d, h)
	}
	listen, origin := hosts.ServerAddress(h)
	attach := "tmux -L " + ServerSocket + " attach"
	windows, err := d.Tmux.Windows()
	if err != nil {
		return err
	}
	for _, w := range windows {
		if w.Name != ServerWindow {
			continue
		}
		if !w.Dead {
			fmt.Fprintf(e.Out, "engine server already running on %s at %s (%s)\n", h.Name, origin, attach)
			return nil
		}
		if err := d.Tmux.Kill(w.ID); err != nil {
			return err
		}
	}
	bin, err := d.Executable()
	if err != nil {
		return err
	}
	opened, err := d.Tmux.Open(session.Window{
		Name: ServerWindow,
		Dir:  e.Home,
		// Without TMUX, the server's own tmux calls reach the host's default
		// tmux server, where task windows live, not engine-server.
		Command: remote.ShellJoin([]string{"env", "-u", "TMUX", "-u", "TMUX_PANE", bin, "serve", "--foreground", "--listen", listen, "--advertise", origin}),
		Log:     filepath.Join(e.Home, ServerLog),
	})
	if err != nil {
		return err
	}
	d.Sleep(serverSettle)
	if err := stillRunning(d.Tmux, opened.ID); err != nil {
		return err
	}
	fmt.Fprintf(e.Out, "engine server started on %s at %s (%s; log %s)\n", h.Name, origin, attach, filepath.Join(e.Home, ServerLog))
	return nil
}

// stillRunning fails with the window's last screen when the server exited.
func stillRunning(tm *session.Tmux, id string) error {
	windows, err := tm.Windows()
	if err != nil {
		return err
	}
	for _, w := range windows {
		if w.ID == id && !w.Dead {
			return nil
		}
	}
	screen, _ := tm.Capture(id, 20)
	return fmt.Errorf("the server exited at start:\n%s", strings.TrimSpace(screen))
}
