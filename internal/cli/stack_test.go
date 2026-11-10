package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/controlrun"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/remote"
)

// engineProcessVar makes this test binary run as the engine command, so a
// test can start `engine ui` as its own process exactly as cmd/engine does.
const engineProcessVar = "ENGINE_CLI_TEST_AS_ENGINE"

func TestMain(m *testing.M) {
	if os.Getenv(engineProcessVar) == "1" {
		os.Exit(Run(os.Args[1:], System()))
	}
	os.Exit(m.Run())
}

// syncBuffer is a writer that a child process's copier and the test share.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// stackClock is the control server's and the observer's clock, so a test
// ages observations without waiting.
type stackClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *stackClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.at }
func (c *stackClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

// fakeHost is a host boundary: it answers the observer's status reads with
// the windows a test sets, or fails as an unreachable host does.
type fakeHost struct {
	mu     sync.Mutex
	status hostops.Status
	err    error
}

func (h *fakeHost) SyncRepo(string) (hostops.Synced, error) {
	return hostops.Synced{}, errors.New("unused")
}
func (h *fakeHost) Spawn(hostops.SpawnRequest) (hostops.Spawned, error) {
	return hostops.Spawned{}, errors.New("unused")
}
func (h *fakeHost) Collect(string) (hostops.Collected, error) {
	return hostops.Collected{}, errors.New("unused")
}
func (h *fakeHost) Release(string, hostops.Outcome) (hostops.Released, error) {
	return hostops.Released{}, errors.New("unused")
}
func (h *fakeHost) Check(string, io.Writer) (bool, error) { return false, errors.New("unused") }
func (h *fakeHost) Status() (hostops.Status, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.status, h.err
}

func (h *fakeHost) set(windows []hostops.Window, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.status, h.err = hostops.Status{Windows: windows}, err
}

// controlStack is a control server on an isolated store, with fake hosts
// behind its session observer. It can be stopped and started again on the
// same address and database, as a server restart does.
type controlStack struct {
	t     *testing.T
	db    string
	token string
	addr  string
	clock *stackClock
	hosts map[string]*fakeHost

	service control.Service
	obs     *controlrun.Observer
	server  *http.Server
	served  chan struct{}
}

func newControlStack(t *testing.T, hostNames ...string) *controlStack {
	t.Helper()
	return newControlStackAt(t, filepath.Join(t.TempDir(), "control.db"), hostNames...)
}

// newControlStackAt is a control stack on the database at db.
func newControlStackAt(t *testing.T, db string, hostNames ...string) *controlStack {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 is absent")
	}
	s := &controlStack{t: t, db: db, token: control.Secret(),
		addr: "127.0.0.1:0", clock: &stackClock{at: time.Now().UTC().Truncate(time.Second)}, hosts: map[string]*fakeHost{}}
	for _, name := range hostNames {
		s.hosts[name] = &fakeHost{}
	}
	s.start()
	t.Cleanup(func() {
		if s.server != nil {
			s.stop()
		}
	})
	return s
}

// start opens the store and serves the control API, wired as engine serve
// wires it, except that host reads reach the fake hosts.
func (s *controlStack) start() {
	s.t.Helper()
	store, err := control.Open(s.db, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	cfg := &hosts.Config{Hosts: map[string]hosts.Host{}}
	for name := range s.hosts {
		cfg.Hosts[name] = hosts.Host{Name: name}
	}
	s.obs = &controlrun.Observer{Store: store, Now: s.clock.now, Logf: s.t.Logf,
		Config: func() (*hosts.Config, error) { return cfg, nil },
		Client: func(h hosts.Host) remote.Client { return s.hosts[h.Name] }}
	s.service = control.Service{Store: store, Now: s.clock.now, Observe: s.obs.Observation}
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		s.t.Fatal(err)
	}
	s.addr = ln.Addr().String()
	s.server = &http.Server{Handler: control.Handler(s.service, s.token, s.t.Logf), ReadHeaderTimeout: 5 * time.Second}
	s.served = make(chan struct{})
	go func() {
		defer close(s.served)
		if err := s.server.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			s.t.Errorf("control server: %v", err)
		}
	}()
}

// stop takes the control server down: its port refuses connections and the
// observer's cache is gone with the process.
func (s *controlStack) stop() {
	s.t.Helper()
	if err := s.server.Close(); err != nil {
		s.t.Fatal(err)
	}
	<-s.served
	s.obs.Settle()
	if err := s.service.Store.Close(); err != nil {
		s.t.Fatal(err)
	}
	s.server = nil
}

func (s *controlStack) url() string { return "http://" + s.addr }

// operator is the operator's client, as the CLI builds it.
func (s *controlStack) operator() control.Client { return control.Client{URL: s.url(), Token: s.token} }

// as is a client holding an execution credential.
func (s *controlStack) as(credential string) control.Client {
	return control.Client{URL: s.url(), Token: credential}
}

// observe has the observer read every host again, as its periodic pass
// does once observations are older than they may be reused.
func (s *controlStack) observe() {
	s.t.Helper()
	s.clock.add(controlrun.ObservationFresh)
	if err := s.obs.Refresh(); err != nil {
		s.t.Fatal(err)
	}
	s.obs.Settle()
}

// dashboard is a running `engine ui` process.
type dashboard struct {
	t      *testing.T
	base   string
	cmd    *exec.Cmd
	stderr *syncBuffer
}

// startDashboard runs `engine ui` as its own process under a fresh home
// whose engine config names the control server and holds the operator
// credential, at the paths every engine command reads.
func startDashboard(t *testing.T, s *controlStack) *dashboard {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	dir := hosts.ConfigDir(home)
	if err = os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "config.toml"), "[server]\nurl = \""+s.url()+"\"\n")
	writeFile(t, filepath.Join(dir, "server-token"), s.token+"\n")
	d := &dashboard{t: t, stderr: &syncBuffer{}}
	d.cmd = exec.Command(self, "ui", "--listen", "127.0.0.1:0")
	d.cmd.Env = append(processEnv(), "HOME="+home, engineProcessVar+"=1")
	d.cmd.Stderr = d.stderr
	stdout, err := d.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = d.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.stop)
	line, err := bufio.NewReader(stdout).ReadString('\n')
	m := regexp.MustCompile(`^engine ui: serving (http://127\.0\.0\.1:\d+)/\n$`).FindStringSubmatch(line)
	if err != nil || m == nil {
		t.Fatalf("engine ui printed %q (%v); stderr %s", line, err, d.stderr.String())
	}
	d.base = m[1]
	return d
}

// processEnv is this process's environment without engine's own settings,
// so the dashboard reads only the config under its home.
func processEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "ENGINE_") && !strings.HasPrefix(kv, "HOME=") {
			out = append(out, kv)
		}
	}
	return out
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// stop signals the dashboard as a terminal would and expects a clean exit.
func (d *dashboard) stop() {
	if d.cmd.ProcessState != nil {
		return
	}
	_ = d.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- d.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			d.t.Errorf("engine ui exited with %v; stderr %s", err, d.stderr.String())
		}
	case <-time.After(15 * time.Second):
		_ = d.cmd.Process.Kill()
		d.t.Errorf("engine ui did not stop on SIGTERM; stderr %s", d.stderr.String())
	}
}

// envelope is the dashboard's response to every data route.
type envelope struct {
	Data    json.RawMessage `json:"data"`
	Fetched time.Time       `json:"fetched"`
	Stale   bool            `json:"stale"`
	Error   string          `json:"error"`
}

// get reads one dashboard route as the page does, decoding its data into out.
func (d *dashboard) get(t *testing.T, path string, out any) (int, envelope) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v; stderr %s", path, err, d.stderr.String())
	}
	defer func() { _ = res.Body.Close() }()
	if got := res.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("GET %s: Cache-Control %q", path, got)
	}
	var env envelope
	if err = json.NewDecoder(res.Body).Decode(&env); err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	if out != nil && len(env.Data) > 0 && string(env.Data) != "null" {
		if err = json.Unmarshal(env.Data, out); err != nil {
			t.Fatalf("GET %s data: %v", path, err)
		}
	}
	return res.StatusCode, env
}
