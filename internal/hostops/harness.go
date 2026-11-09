package hostops

import (
	"path/filepath"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

// defaultAdapters builds the adapters of Deps that carry none. The commands
// always pass theirs; tests set this to the registry's.
var defaultAdapters func(Deps) harness.Adapters

// adapters are the harness adapters the ops launch and observe.
func (d Deps) adapters() harness.Adapters {
	if d.Harnesses == nil && defaultAdapters != nil {
		return defaultAdapters(d)
	}
	return d.Harnesses
}

// adapterFor returns the adapter called name, or the default when name is
// empty.
func adapterFor(d Deps, name string) (harness.Adapter, error) {
	a, err := harness.Lookup(d.adapters(), name)
	if err != nil {
		return nil, refuse("%v", err)
	}
	return a, nil
}

// launch is what a window runs: the expanded launch line, the session's
// folder, the run directory, the mode, the kickoff, and whether the session
// joins remote control.
type launch struct {
	line, dir, runDir, mode, kickoff string
	joined                           bool
	control                          *ControlRequest
}

// harnessLaunch is l as the adapter takes it.
func (l launch) harnessLaunch() harness.Launch {
	out := harness.Launch{Line: l.line, Dir: l.dir, RunDir: l.runDir, Kickoff: l.kickoff, Unattended: l.mode == HandsOff, Joined: l.joined}
	if l.control != nil {
		out.ServerURL, out.TokenFile, out.Attempt = l.control.URL, filepath.Join(l.runDir, "server-token"), l.control.Attempt
	}
	return out
}

func launchCommand(name string, h harness.Launcher, l launch) string {
	script := ""
	if l.control != nil {
		script = "export ENGINE_SERVER_URL=" + hosts.ShellWord(l.control.URL) + " ENGINE_SERVER_TOKEN_FILE=" + hosts.ShellWord(filepath.Join(l.runDir, "server-token")) + " ENGINE_ATTEMPT_ID=" + hosts.ShellWord(l.control.Attempt) + " && "
	}
	// The hooks log to the run directory and, unattended, deny or log.
	script += "export " + session.RunDirVar + "=" + hosts.ShellWord(l.runDir) + " && "
	script += "export " + session.HarnessVar + "=" + name + " && "
	if l.mode == HandsOff {
		script += "export " + session.UnattendedVar + "=1 && "
	} else {
		script += "unset " + session.UnattendedVar + " && "
	}
	script += h.Env(l.joined) + "exec " + l.line + h.Args(l.harnessLaunch())
	return "exec /bin/sh -c " + hosts.ShellWord(script)
}
