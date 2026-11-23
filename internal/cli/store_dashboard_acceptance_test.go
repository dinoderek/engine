package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/ui"
)

// commandLog sits between `engine ui` and the control server, forwarding
// every command and recording it. Once armed for n calls, it holds each call
// until n are in flight together, so calls made one after another never get
// there and are reported as serial.
type commandLog struct {
	upstream string

	mu       sync.Mutex
	calls    []control.Request
	inFlight int
	peak     int
	want     int
	together chan struct{}
}

func (l *commandLog) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req control.Request
	_ = json.Unmarshal(body, &req)
	l.hold(req)
	defer l.release()
	out, err := http.NewRequestWithContext(r.Context(), r.Method, l.upstream+r.URL.Path, bytes.NewReader(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	out.Header = r.Header.Clone()
	res, err := http.DefaultClient.Do(out)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = res.Body.Close() }()
	w.Header().Set("Content-Type", res.Header.Get("Content-Type"))
	w.WriteHeader(res.StatusCode)
	_, _ = io.Copy(w, res.Body)
}

// hold records req and, while armed, waits until the armed number of calls
// are in flight, or gives up after five seconds.
func (l *commandLog) hold(req control.Request) {
	l.mu.Lock()
	l.calls = append(l.calls, req)
	l.inFlight++
	l.peak = max(l.peak, l.inFlight)
	together := l.together
	if together != nil && l.inFlight == l.want {
		close(together)
		l.together = nil
	}
	l.mu.Unlock()
	if together == nil {
		return
	}
	select {
	case <-together:
	case <-time.After(5 * time.Second):
	}
}

func (l *commandLog) release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.inFlight--
}

// arm forgets what was recorded and holds the next n calls for each other.
func (l *commandLog) arm(n int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls, l.peak, l.want, l.together = nil, 0, n, make(chan struct{})
}

// recorded is every call since arm, and the most that were in flight at once.
func (l *commandLog) recorded() ([]control.Request, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.calls), l.peak
}

// TestADashboardRefreshOfTheMigratedServerMakesThreeCalls runs `engine ui`
// against a real `engine serve` on the migrated version 6 fixture. One
// refresh of the Plans page with a task's Communications open, its three
// routes requested together as the page does, reaches the control server as
// at most three calls, all in flight at once, and the thread's call reads
// only the updates after its cursor.
func TestADashboardRefreshOfTheMigratedServerMakesThreeCalls(t *testing.T) {
	db := fixtureDatabase(t, filepath.Join(t.TempDir(), "control.db"))
	p := startServer(t, serverHome(t), db)
	log := &commandLog{upstream: p.url()}
	proxy := httptest.NewServer(log)
	t.Cleanup(proxy.Close)
	d := startDashboardOn(t, proxy.URL, p.token)

	var opened ui.Thread
	if status, env := d.get(t, "/api/items/t3/thread?after=0", &opened); status != http.StatusOK || opened.Next == 0 {
		t.Fatalf("opening t3's thread: %d %+v %+v", status, env, opened)
	}
	cursor := opened.Next
	said := p.call(control.Request{Op: "say", ID: "t3", Body: "After the cursor"}).Updates[0]

	log.arm(3)
	routes := []string{"/api/overview", "/api/plans/p2", "/api/items/t3/thread?after=" + itoa(cursor)}
	var overview ui.Bundle
	var thread ui.Thread
	var plan, threadEnv envelope
	var wg sync.WaitGroup
	wg.Go(func() { overview = d.bundle(t, routes[0]) })
	wg.Go(func() { _, plan = d.get(t, routes[1], nil) })
	wg.Go(func() { _, threadEnv = d.get(t, routes[2], &thread) })
	wg.Wait()

	calls, peak := log.recorded()
	check(t, len(calls) <= 3, "one refresh made %d control calls: %+v", len(calls), calls)
	check(t, peak == len(calls), "the refresh's %d calls were not in flight together (at most %d were)", len(calls), peak)
	ops := map[string]control.Request{}
	for _, c := range calls {
		ops[c.Op] = c
	}
	check(t, len(ops) == 3 && ops["overview"].Op != "" && ops["plan-detail"].ID == "p2", "the refresh's calls: %+v", calls)
	check(t, ops["updates"].ID == "t3" && ops["updates"].After == cursor, "the thread's call %+v does not read after its cursor %d", ops["updates"], cursor)
	for route, env := range map[string]envelope{routes[1]: plan, routes[2]: threadEnv} {
		check(t, env.Error == "" && !env.Stale && len(env.Data) > 0, "%s answered %+v", route, env)
	}
	for name, view := range map[string]ui.Envelope{"inbox": overview.Inbox, "sessions": overview.Sessions, "plans": overview.Plans} {
		check(t, view.Error == "" && !view.Stale && view.Data != nil, "the overview's %s view: %+v", name, view)
	}
	check(t, len(thread.Messages) > 0 && thread.Messages[len(thread.Messages)-1].ID == said.ID, "the refreshed thread lacks the new message %d: %+v", said.ID, thread.Messages)
	for _, m := range thread.Messages {
		check(t, m.ID > cursor, "the refreshed thread repeats message %d, at or before its cursor %d", m.ID, cursor)
	}
}

// bundle reads the dashboard's overview route, which answers an envelope
// for each of its three views.
func (d *dashboard) bundle(t *testing.T, path string) ui.Bundle {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, d.base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v; stderr %s", path, err, d.stderr.String())
	}
	defer func() { _ = res.Body.Close() }()
	var b ui.Bundle
	if err = json.NewDecoder(res.Body).Decode(&b); err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %d %v", path, res.StatusCode, err)
	}
	return b
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }
