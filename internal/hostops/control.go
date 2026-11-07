package hostops

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/dinoderek/engine/internal/session"
)

// ControlRequest carries run credentials over stdin, never through argv.
type ControlRequest struct {
	Spawn      SpawnRequest `json:"spawn"`
	URL        string       `json:"url"`
	Credential string       `json:"credential"`
	Attempt    string       `json:"attempt"`
	Remote     string       `json:"remote"`
}

// ControlSpawn syncs with ordinary Git credentials and launches a server-owned run.
// A replay of a completed dispatch returns the recorded window.
func ControlSpawn(d Deps, r ControlRequest) (Spawned, error) {
	if r.URL == "" || r.Credential == "" || r.Attempt == "" || r.Remote == "" {
		return Spawned{}, refuse("control dispatch requires endpoint, credential, execution and remote")
	}
	runDir, err := session.RunDir(d.Home, r.Spawn.RunID)
	if err != nil {
		return Spawned{}, refuse("%v", err)
	}
	if err = os.MkdirAll(filepath.Dir(runDir), 0o700); err != nil {
		return Spawned{}, err
	}
	lock, err := os.OpenFile(runDir+".dispatch-lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return Spawned{}, err
	}
	defer func() { _ = lock.Close() }()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return Spawned{}, err
	}
	if record, err := session.ReadRecord(runDir); err == nil {
		token, err := os.ReadFile(filepath.Join(runDir, "server-token"))
		if err != nil || strings.TrimSpace(string(token)) != r.Credential || record.RunID != r.Spawn.RunID || record.Repo != r.Spawn.Repo || record.Task != fmt.Sprintf("%s#%d", r.Spawn.Repo, r.Spawn.Task) {
			return Spawned{}, refuse("run identity differs")
		}
		if windowID(record.Tmux) == "" {
			return Spawned{}, refuse("dispatch record is incomplete; retain the reserved execution for reconciliation")
		}
		return Spawned{Name: record.Name, Window: windowID(record.Tmux), RunDir: runDir, Worktree: record.Worktree, Branch: record.Branch}, nil
	}
	repo, err := session.ParseRepo(r.Spawn.Repo)
	if err != nil {
		return Spawned{}, refuse("%v", err)
	}
	repos := session.Repos{Run: d.Run, Probe: d.Probe, Home: d.Home, Operator: true, RemoteURL: r.Remote}
	if _, err = repos.Sync(repo); err != nil {
		return Spawned{}, err
	}
	req := r.Spawn
	req.control = &r
	return Spawn(d, req)
}

func windowID(target string) string {
	for i, c := range target {
		if c == ':' {
			return target[i+1:]
		}
	}
	return target
}

func readControl(in io.Reader) (ControlRequest, error) {
	var r ControlRequest
	dec := json.NewDecoder(io.LimitReader(in, 2<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return r, refuse("control dispatch JSON: %v", err)
	}
	return r, nil
}

func writeControl(path string, r ControlRequest) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(path, "server-token"), []byte(r.Credential+"\n"), 0o600); err != nil {
		return fmt.Errorf("write execution credential: %w", err)
	}
	data, err := json.Marshal(struct{ URL, Attempt string }{r.URL, r.Attempt})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(path, "server-session.json"), data, 0o600)
}
