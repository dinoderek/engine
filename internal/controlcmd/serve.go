package controlcmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/controlrun"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/run"
	"github.com/dinoderek/engine/internal/session"
)

// ServerDBPath is the control server's default database, under home.
const ServerDBPath = ".local/share/engine/server/control.db"

// ServerTokenFile is the operator credential that the server and the CLI read
// by default, in engine's config directory.
const ServerTokenFile = "server-token"

func serveMain(args []string, e Env) int {
	fs := flag.NewFlagSet("engine serve", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	db := fs.String("db", filepath.Join(e.Home, ServerDBPath), "database path")
	tokenPath := fs.String("token-file", filepath.Join(hosts.ConfigDir(e.Home), ServerTokenFile), "private operator credential")
	listen := fs.String("listen", "127.0.0.1:7331", "private listen address")
	advertise := fs.String("advertise", "", "origin reachable from execution hosts")
	backup := fs.String("backup", "", "write a consistent backup and exit")
	foreground := fs.Bool("foreground", false, "run the server in this process instead of starting it on its host")
	onHost := fs.String("on-host", "", "start the server here, on the named host (engine serve's own hop)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return 2
	}
	if !*foreground && *backup == "" {
		var set []string
		fs.Visit(func(f *flag.Flag) {
			if f.Name != "on-host" {
				set = append(set, "--"+f.Name)
			}
		})
		if len(set) > 0 {
			fmt.Fprintf(e.Err, "engine serve: %s needs --foreground; without it, engine serve starts the server on the host config.toml names\n", strings.Join(set, ", "))
			return 2
		}
		return launchServer(e, *onHost)
	}
	token, err := control.ReadCredential(*tokenPath)
	if err != nil {
		fmt.Fprintf(e.Err, "engine serve: operator credential %s: %v\n", *tokenPath, err)
		return 1
	}
	store, err := control.Open(*db, nil)
	if err != nil {
		fmt.Fprintln(e.Err, "engine serve:", err)
		return 1
	}
	defer func() {
		if err := store.Close(); err != nil {
			fmt.Fprintln(e.Err, "engine serve close:", err)
		}
	}()
	if *backup != "" {
		if err = store.Backup(*backup); err != nil {
			fmt.Fprintln(e.Err, "engine serve backup:", err)
			return 1
		}
		return 0
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintln(e.Err, "engine serve:", err)
		return 1
	}
	if *advertise == "" {
		*advertise = "http://" + ln.Addr().String()
	}
	if _, err = control.Endpoint(*advertise); err != nil {
		_ = ln.Close()
		fmt.Fprintln(e.Err, "engine serve:", err)
		return 1
	}
	ctx := e.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	e.Context = ctx
	logf := func(format string, args ...any) { fmt.Fprintf(e.Err, format+"\n", args...) }
	service, c, obs := wire(store, *advertise, e)
	fmt.Fprintf(e.Out, "engine server listening on %s\n", ln.Addr())
	if err = Serve(ctx, ln, control.Handler(service, token, logf), background(c.Run, obs.Run)); err != nil {
		fmt.Fprintln(e.Err, "engine serve:", err)
		return 1
	}
	return 0
}

// wire connects the command service, the coordinator and the session view's observer.
func wire(store *control.Store, url string, e Env) (control.Service, *controlrun.Coordinator, *controlrun.Observer) {
	service := control.Service{Store: store}
	c := newCoordinator(service, url, e)
	obs := newObserver(store, e)
	service.Reconcile = c.ConfirmEnded
	service.Observe = obs.Observation
	c.Service = service
	c.Observer = obs
	return service, c, obs
}

func newCoordinator(service control.Service, url string, e Env) *controlrun.Coordinator {
	ctx := e.Context
	if ctx == nil {
		ctx = context.Background()
	}
	hostExec := session.ExecContext(ctx, 30*time.Second)
	c := &controlrun.Coordinator{Service: service, URL: url, Home: e.Home, Logf: func(format string, args ...any) { fmt.Fprintf(e.Err, format+"\n", args...) }}
	c.Client = hostClients(ctx, e, 7*time.Minute)
	c.Wake = func(s run.Session, text string) error {
		cfg, local, err := hosts.Open(hosts.ConfigDir(e.Home))
		if err != nil {
			return err
		}
		h := cfg.Hosts[s.Host]
		tm := session.New()
		tm.Run = hostExec
		if h.Name != local.Name {
			tm = &session.Tmux{Bin: "tmux", Run: remote.TmuxRunner(hostExec, h.Address), Sleep: time.Sleep}
		}
		wins, err := tm.Windows()
		if err != nil {
			return err
		}
		for _, w := range wins {
			if w.ID == s.WindowID && w.Name == s.Name && !w.Dead {
				return tm.Type(w.ID, text)
			}
		}
		return fmt.Errorf("execution window changed")
	}
	c.Resume = func(s run.Session) error { return c.Wake(s, "continue") }
	return c
}

// hostClients reaches each configured host, bounding every host command by limit.
func hostClients(ctx context.Context, e Env, limit time.Duration) func(hosts.Host) remote.Client {
	hostExec := session.ExecContext(ctx, 30*time.Second)
	return func(h hosts.Host) remote.Client {
		name, _ := hosts.ReadHostName(hosts.ConfigDir(e.Home) + "/host")
		if h.Name == name {
			tm := session.New()
			tm.Run = hostExec
			return remote.Local{Deps: hostops.Deps{Home: e.Home, Run: session.RunCommandContext(ctx, limit), Probe: session.RunCommandContext(ctx, min(limit, 3*time.Minute)), Exec: hostExec, Tmux: tm, Getenv: e.Getenv, Now: time.Now, JoinRemote: true}}
		}
		return remote.SSH{Name: h.Name, Address: h.Address, Exec: remote.ExecContext(ctx, limit)}
	}
}

// newObserver samples active attempts for the session view, with a short host-read limit.
func newObserver(store *control.Store, e Env) *controlrun.Observer {
	ctx := e.Context
	if ctx == nil {
		ctx = context.Background()
	}
	return &controlrun.Observer{Store: store, Client: hostClients(ctx, e, time.Minute),
		Config: func() (*hosts.Config, error) { cfg, _, err := hosts.Open(hosts.ConfigDir(e.Home)); return cfg, err },
		Logf:   func(format string, args ...any) { fmt.Fprintf(e.Err, format+"\n", args...) }}
}

// background runs every loop until shutdown and returns when all have stopped.
func background(loops ...func(context.Context)) func(context.Context) {
	return func(ctx context.Context) {
		var wg sync.WaitGroup
		for _, loop := range loops {
			wg.Go(func() { loop(ctx) })
		}
		wg.Wait()
	}
}

// Serve shuts down HTTP and the coordinator before its caller closes storage.
func Serve(ctx context.Context, ln net.Listener, handler http.Handler, coordinate func(context.Context)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16 << 10}
	done := make(chan struct{})
	go func() { defer close(done); coordinate(ctx) }()
	errs := make(chan error, 1)
	go func() { errs <- server.Serve(ln) }()
	var err error
	select {
	case <-ctx.Done():
	case err = <-errs:
	}
	cancel()
	shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if e := server.Shutdown(shutdown); e != nil {
		_ = server.Close()
		if err == nil {
			err = e
		}
	}
	<-done
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// CheckMerge is the extra guard for server sessions, invoked immediately before gh.
func CheckMerge(e Env) error {
	client, err := makeClient(e)
	if err != nil {
		return err
	}
	path := e.Getenv("ENGINE_RUN_DIR") + "/merge-lock.json"
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("take the repository merge lock first: %w", err)
	}
	var lock control.Lock
	if err = json.Unmarshal(b, &lock); err != nil {
		return err
	}
	ctx := e.Context
	if ctx == nil {
		ctx = context.Background()
	}
	_, err = client.Call(ctx, control.Request{Op: "lock-check", Attempt: e.Getenv("ENGINE_ATTEMPT_ID"), Token: lock.Token})
	return err
}
