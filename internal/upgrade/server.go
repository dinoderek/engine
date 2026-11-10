package upgrade

import (
	"fmt"
	"io"

	"github.com/dinoderek/engine/internal/hosts"
)

// backupDir is where the server's backups go, as the host's shell writes it;
// the report shows it under ~.
const backupDir = "$HOME/.local/share/engine/server/backups"

// backupCommand writes a backup with the engine the unit runs, before
// setup replaces it: a newer engine migrates the database when it opens it.
func (f *fleet) backupCommand() string {
	return fmt.Sprintf(`mkdir -p "%[1]s" && "$HOME/%[2]s" serve --backup "%[1]s/%[3]s.db"`, backupDir, newBinary, f.stamp)
}

// runOn runs line on h, and records an open item, closed by running it
// again, when it fails.
func (f *fleet) runOn(h hosts.Host, line, what string, out io.Writer) bool {
	sh := f.shell(h)
	res, err := sh.Sh(line, "")
	switch {
	case err != nil:
		f.openf(out, sh.OnHost(line, false), "%s failed: %v", what, err)
		return false
	case res.Code != 0:
		f.openf(out, sh.OnHost(line, false), "%s failed: %s", what, res.Why())
		return false
	}
	return true
}

// stop stops the server's unit. From here on the start runs on every exit.
func (f *fleet) stop(out io.Writer) bool {
	f.stopped = true
	line, _ := f.server.ServerShellLine(hosts.ServerStop)
	return f.runOn(f.server, line, "the server's stop", out)
}

// backup writes the database's backup; without one the upgrade goes no
// further.
func (f *fleet) backup(out io.Writer) bool {
	if !f.runOn(f.server, f.backupCommand(), "the backup", out) {
		return false
	}
	f.backedUp = true
	return true
}

// start starts the server's unit, once and only after a stop was tried, and
// waits until the server answers. A start that fails, or a server that does
// not answer, is open with the command that reads its log.
func (f *fleet) start(out io.Writer) bool {
	if !f.stopped || f.started {
		return true
	}
	f.started = true
	sh := f.shell(f.server)
	journal := sh.OnHost("journalctl --user -u "+hosts.ShellWord(f.server.Services.Server), false)
	line, _ := f.server.ServerShellLine(hosts.ServerStart)
	res, err := sh.Sh(line, "")
	switch {
	case err != nil:
		f.openf(out, journal, "the server did not start: %v; read its log", err)
	case res.Code != 0:
		f.openf(out, journal, "the server did not start: %s; read its log", res.Why())
	case !f.answers():
		f.openf(out, journal, "the server started but did not answer within %s; read its log", answerTries*answerPause)
	default:
		f.answered = true
	}
	return true
}

// answers waits, a pause between tries, until the server lists its tasks.
func (f *fleet) answers() bool {
	for i := range answerTries {
		if i > 0 {
			f.d.Sleep(answerPause)
		}
		if _, err := f.d.Tasks(); err == nil {
			return true
		}
	}
	return false
}
