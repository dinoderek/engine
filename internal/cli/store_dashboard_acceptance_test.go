package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	client   *http.Client

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
	res, err := l.client.Do(out)
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
	log := &commandLog{upstream: p.url(), client: p.client}
	proxy := httptest.NewServer(log)
	t.Cleanup(proxy.Close)
	d := startDashboardOn(t, proxy.URL, p.token)

	var opened ui.Thread
	if status, env := d.get(t, "/api/items/t3/thread?after=0", &opened); status != http.StatusOK || opened.Next == 0 {
		t.Fatalf("opening t3's thread: %d %+v %+v", status, env, opened)
	}
	cursor := opened.Next
	said := p.call(t, control.Request{Op: "say", ID: "t3", Body: "After the cursor"}).Updates[0]

	log.arm(3)
	routes := []string{"/api/overview", "/api/plans/p2", "/api/items/t3/thread?after=" + itoa(cursor)}
	bodies := make([][]byte, len(routes))
	errs := make([]error, len(routes))
	var wg sync.WaitGroup
	for i, route := range routes {
		wg.Go(func() { bodies[i], errs[i] = d.fetch(t.Context(), route) })
	}
	wg.Wait()
	var overview ui.Bundle
	var thread ui.Thread
	var plan, threadEnv envelope
	for i, out := range []any{&overview, &plan, &threadEnv} {
		if errs[i] == nil {
			errs[i] = json.Unmarshal(bodies[i], out)
		}
		if errs[i] != nil {
			t.Fatalf("GET %s: %v; stderr %s", routes[i], errs[i], d.stderr.String())
		}
	}
	if err := json.Unmarshal(threadEnv.Data, &thread); err != nil {
		t.Fatal(err)
	}

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

// fetch is the body of the dashboard's answer to a GET of path, an error
// unless it is a 200.
func (d *dashboard) fetch(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.base+path, nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err == nil && res.StatusCode != http.StatusOK {
		err = fmt.Errorf("status %d: %s", res.StatusCode, body)
	}
	return body, err
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }
