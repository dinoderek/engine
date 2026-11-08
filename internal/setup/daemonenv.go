package setup

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/dinoderek/engine/internal/hosts"
)

// daemonInstanceCommand identifies one remote-control daemon by PID and start
// time. A saved environment receipt cannot validate a replacement process.
func daemonInstanceCommand(home string) string {
	return fmt.Sprintf(`ps -ww -eo pid=,lstart=,args= | awk -v p=%s 'index($7, p) == 1 && / app-server( |$)/ { count++; instance = $1 " " $2 " " $3 " " $4 " " $5 " " $6 } END { if (count == 1) print instance }'`, hosts.ShellWord(home+"/packages/"))
}

func (r *runner) daemonInstance() (string, error) {
	res, err := r.t.sh(daemonInstanceCommand(r.codex.engineHome()), "")
	if err != nil {
		return "", err
	}
	if res.code != 0 {
		return "", nil
	}
	return strings.TrimSpace(res.out), nil
}

func (r *runner) daemonEnvironmentReady() (bool, error) {
	instance, err := r.daemonInstance()
	if err != nil || instance == "" {
		return false, err
	}
	res, err := r.t.sh("cat "+shellPath(r.codex.engineHome(), "daemon-environment"), "")
	return err == nil && res.code == 0 && strings.TrimSpace(res.out) == instance, err
}

func (r *runner) recordDaemonEnvironment() (outcome, error) {
	instance, err := r.daemonInstance()
	if err != nil {
		return outcome{}, err
	}
	if instance == "" {
		return open("could not identify engine's daemon", r.rerun()), nil
	}
	path := shellPath(r.codex.engineHome(), "daemon-environment")
	cmd := daemonReceiptCommand(path, instance)
	res, err := r.t.sh(cmd, "")
	if err != nil {
		return outcome{}, err
	}
	if res.code != 0 {
		return open("could not record the daemon environment: "+res.why(), r.rerun()), nil
	}
	conn, err := r.remoteConnection(instance)
	if err != nil {
		return outcome{}, err
	}
	return changed("started; remote control on, " + conn), nil
}

// remoteConnection is how the daemon of instance (its PID and start)
// stands with the apps, from its own log in engine's Codex home: connected,
// or not connected and why. The --remote-control argument only says remote
// control was asked for. A daemon just started may not have connected yet.
func (r *runner) remoteConnection(instance string) (string, error) {
	pid, _, _ := strings.Cut(instance, " ")
	n, err := strconv.Atoi(pid)
	if err != nil {
		return "not connected (its PID cannot be read)", nil
	}
	res, err := r.t.sh("ps -o etime= -p "+pid, "")
	if err != nil {
		return "", err
	}
	elapsed, perr := hosts.ParseElapsed(res.out)
	if res.code != 0 || perr != nil {
		return "not connected (its start cannot be read)", nil
	}
	sql := hosts.CodexRemoteSQL(n, elapsed)
	if res, err = r.t.sh("sqlite3 -readonly -json "+shellPath(r.codex.engineHome(), hosts.CodexLogs)+" "+hosts.ShellWord(sql), ""); err != nil {
		return "", err
	}
	if res.code != 0 {
		return "not connected (its log cannot be read: " + res.why() + ")", nil
	}
	if why := hosts.CodexDisconnected(res.out); why != "" {
		return "not connected (" + why + ")", nil
	}
	return "connected", nil
}

func daemonReceiptCommand(path, instance string) string {
	return fmt.Sprintf(`umask 077 && printf '%%s\n' %s > %s.tmp && mv %s.tmp %s`, hosts.ShellWord(instance), path, path, path)
}
