package tell

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/session"
)

// The task references are split so this file holds no tracker reference.
const (
	task7 = "o/r#" + "7"
	task8 = "o/r#" + "8"
	task9 = "o/r#" + "9"
)

// statusHost is a host whose status op lists runs, or fails.
type statusHost struct {
	runs    []hostops.Run
	runsErr string
	err     error
}

func (f statusHost) Status() (hostops.Status, error) {
	return hostops.Status{Runs: f.runs, RunsError: f.runsErr}, f.err
}
func (statusHost) SyncRepo(string) (hostops.Synced, error) {
	return hostops.Synced{}, errors.New("unused")
}
func (statusHost) Spawn(hostops.SpawnRequest) (hostops.Spawned, error) {
	return hostops.Spawned{}, errors.New("unused")
}
func (statusHost) Collect(string) (hostops.Collected, error) {
	return hostops.Collected{}, errors.New("unused")
}
func (statusHost) Release(string, bool) (hostops.Released, error) {
	return hostops.Released{}, errors.New("unused")
}
func (statusHost) Check(string, io.Writer) (bool, error) { return false, errors.New("unused") }

// preClaimGh answers a claim search with no claims, the read of task 7 of
// o/r with state and labels, and records the comment it is asked to post.
type preClaimGh struct {
	t      *testing.T
	state  string
	labels []string
	posted string
}

func (g *preClaimGh) run(_ []string, _ string, args ...string) (string, error) {
	labels := make([]map[string]string, len(g.labels))
	for i, l := range g.labels {
		labels[i] = map[string]string{"name": l}
	}
	switch {
	case args[1] == "user":
		return "developer", nil
	case args[1] == "search/issues":
		return jsonOf(g.t, map[string]any{"incomplete_results": false, "items": []any{}}), nil
	case args[1] == "repos/o/r/"+"issues/7":
		return jsonOf(g.t, map[string]any{"number": 7, "title": "task", "state": g.state, "labels": labels, "body": ""}), nil
	case strings.HasSuffix(args[1], "/dependencies/blocked_by"):
		return "[]", nil
	case args[1] == comments && len(args) == 4 && args[2] == "-f":
		g.posted = strings.TrimPrefix(args[3], "body=")
		return jsonOf(g.t, map[string]string{"html_url": commentURL}), nil
	}
	g.t.Errorf("unexpected gh api %v", args)
	return "", errors.New("unexpected gh api")
}

func run(name, task, host, window string, at int) hostops.Run {
	return hostops.Run{Name: name, Record: session.Record{RunID: fmt.Sprintf("%s-%d", name, at), Name: name, Task: task, Host: host,
		Tmux: "engine:" + window, Spawned: time.Date(2026, 10, 26, 22, at, 0, 0, time.UTC)}}
}

// preClaimRig is this host, mac, with runs on mac and devbox and the
// windows of mac's tmux (devbox's are reached over ssh, through the same fake).
func preClaimRig(t *testing.T, g *preClaimGh, tm *tmux, mac, devbox statusHost) Deps {
	return Deps{
		Gh: g.run, Run: (&ssh{tm: tm}).run, Tmux: &session.Tmux{Run: tm.run, Bin: "tmux", Sleep: func(time.Duration) {}},
		Getenv: func(string) string { return "" }, Home: home(t, "mac"),
		Client: func(h hosts.Host, remote bool) remote.Client {
			if remote != (h.Name != "mac") {
				t.Errorf("host %s reached with remote=%v", h.Name, remote)
			}
			return map[string]statusHost{"mac": mac, "devbox": devbox}[h.Name]
		},
	}
}

// A session engine run started that has not claimed its task is found by its
// newest run record: the comment goes on the record's task and the pointer
// into the record's window, on the record's host.
func TestTellReachesASessionBeforeItsClaim(t *testing.T) {
	for _, host := range []string{"mac", "devbox"} {
		t.Run(host, func(t *testing.T) {
			g := &preClaimGh{t: t, state: "open", labels: []string{"engine:blocked"}}
			tm := &tmux{windows: "@1 %1 0 other\n@4 %9 0 r-7\n"}
			hs := map[string]statusHost{"mac": {}, "devbox": {}}
			// An older run of the same name, in another window, loses to the newest.
			hs[host] = statusHost{runs: []hostops.Run{run("r-7", task7, host, "@2", 1), run("r-7", task7, host, "@4", 5), run("other", task9, host, "@1", 9)}}
			told, err := Tell("r-7", "Carry on with the kickoff.", preClaimRig(t, g, tm, hs["mac"], hs["devbox"]))
			if err != nil {
				t.Fatal(err)
			}
			if g.posted != "Carry on with the kickoff." || told.URL != commentURL || told.Host != host || told.Window != "engine:@4" || told.Task.Number != 7 {
				t.Errorf("told %+v, posted %q", told, g.posted)
			}
			want := [][]string{{"send-keys", "-t", "=engine:@4", "-l", Pointer(commentURL)}, {"send-keys", "-t", "=engine:@4", "Enter"}}
			if fmt.Sprint(tm.keys) != fmt.Sprint(want) {
				t.Errorf("keys %q", tm.keys)
			}
		})
	}
}

// Every trust check refuses before the comment is posted.
func TestTellRefusesAnUntrustedPreClaimSession(t *testing.T) {
	live := "@4 %9 0 r-7\n"
	tests := []struct {
		name, state, windows, want string
		labels                     []string
		mac, devbox                statusHost
	}{
		{"no run anywhere", "open", live, "no claimed task or engine run named r-7", nil, statusHost{}, statusHost{}},
		{"unread host is named", "open", live, "hosts not read: devbox: no route", nil, statusHost{}, statusHost{err: errors.New("no route")}},
		{"unread run records are named", "open", live, "hosts not read: mac: run records: bad json", nil, statusHost{runsErr: "bad json"}, statusHost{}},
		{"window outside the engine session", "open", live, `names window "other:@4"`, nil, statusHost{runs: []hostops.Run{{Name: "r-7", Record: session.Record{RunID: "r-7-1", Name: "r-7", Task: task7, Host: "mac", Tmux: "other:@4"}}}}, statusHost{}},
		{"record names another host", "open", live, "no claimed task or engine run named r-7", nil, statusHost{runs: []hostops.Run{run("r-7", task7, "laptop", "@4", 1)}}, statusHost{}},
		{"record of another task", "open", live, "whose session is r-8", nil, statusHost{runs: []hostops.Run{run("r-7", task8, "mac", "@4", 1)}}, statusHost{}},
		{"closed task", "closed", live, task7 + " is closed", nil, statusHost{runs: []hostops.Run{run("r-7", task7, "mac", "@4", 1)}}, statusHost{}},
		{"waiting on the operator", "open", live, "engine:needs-human", []string{"engine:needs-human"}, statusHost{runs: []hostops.Run{run("r-7", task7, "mac", "@4", 1)}}, statusHost{}},
		{"dead window", "open", "@4 %9 1 r-7\n", "not a live window named r-7", nil, statusHost{runs: []hostops.Run{run("r-7", task7, "mac", "@4", 1)}}, statusHost{}},
		{"window id reused", "open", "@4 %9 0 other\n@5 %10 0 r-7\n", "names window @4 on mac", nil, statusHost{runs: []hostops.Run{run("r-7", task7, "mac", "@4", 1)}}, statusHost{}},
		{"no window on the other host", "open", "@1 %1 0 other\n", "not a live window named r-7", nil, statusHost{}, statusHost{runs: []hostops.Run{run("r-7", task7, "devbox", "@4", 1)}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &preClaimGh{t: t, state: tt.state, labels: tt.labels}
			tm := &tmux{windows: tt.windows}
			_, err := Tell("r-7", "hi", preClaimRig(t, g, tm, tt.mac, tt.devbox))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %v, want %q", err, tt.want)
			}
			if g.posted != "" || len(tm.keys) != 0 {
				t.Errorf("posted %q, keys %q", g.posted, tm.keys)
			}
		})
	}
}

// Status lists each host's newest run of a name, so the newest of all is
// chosen across hosts.
func TestTellPicksTheNewestRunAcrossHosts(t *testing.T) {
	g := &preClaimGh{t: t, state: "open"}
	tm := &tmux{windows: "@2 %2 0 r-7\n@4 %9 0 r-7\n"}
	mac := statusHost{runs: []hostops.Run{run("r-7", task7, "mac", "@2", 1)}}
	devbox := statusHost{runs: []hostops.Run{run("r-7", task7, "devbox", "@4", 5)}}
	told, err := Tell("r-7", "hi", preClaimRig(t, g, tm, mac, devbox))
	if err != nil || told.Host != "devbox" || told.Window != "engine:@4" {
		t.Fatalf("told %+v, err %v", told, err)
	}
}
