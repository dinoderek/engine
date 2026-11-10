package hosts

import (
	"fmt"
	"regexp"
	"strings"
)

// ManagerSystemd is the one service manager a host may record: the server
// runs as a `systemctl --user` unit.
const ManagerSystemd = "systemd"

// Services is a host's optional [host.<name>.services] table: how the host
// runs the control server. The zero value records nothing.
type Services struct {
	Manager string
	// Server is the server's user unit, such as engine-server.service.
	Server string
}

// Recorded reports whether the host records how it runs the server.
func (s Services) Recorded() bool { return s.Manager != "" }

// unitName is a systemd service unit name: the characters systemd allows
// (letters, digits and :-_.\@), starting with a letter, digit or underscore so
// it can never read as a systemctl option, and ending in .service.
var unitName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9:_.\\@-]*\.service$`)

// ServerOp is what a systemctl command line does to the server's unit.
type ServerOp string

// The operations ServerCommand builds.
const (
	ServerStart    ServerOp = "start"
	ServerStop     ServerOp = "stop"
	ServerIsActive ServerOp = "is-active"
)

// ServerCommand returns the argv that runs op on the server's unit on host h:
// `systemctl --user <op> <unit>`. It runs nothing; the caller runs it, locally
// or over ssh (ServerShellLine).
func (h Host) ServerCommand(op ServerOp) ([]string, error) {
	switch op {
	case ServerStart, ServerStop, ServerIsActive:
	default:
		return nil, fmt.Errorf("unknown server operation %q", op)
	}
	if !h.Services.Recorded() {
		return nil, fmt.Errorf("host %s records no [services] table", h.Name)
	}
	return []string{"systemctl", "--user", string(op), h.Services.Server}, nil
}

// ServerShellLine is ServerCommand as one shell command line, each word quoted
// unless it is plain, for running over ssh.
func (h Host) ServerShellLine(op ServerOp) (string, error) {
	argv, err := h.ServerCommand(op)
	if err != nil {
		return "", err
	}
	words := make([]string, len(argv))
	for i, w := range argv {
		words[i] = ShellWord(w)
	}
	return strings.Join(words, " "), nil
}

// hostServices reads the host's optional services table.
func (v *validator) hostServices(r *reader, h *Host) {
	n := r.table("services", false)
	if n == nil {
		return
	}
	sr := v.reader(n, r.where+".services")
	before := len(v.errs)
	manager, managerAt := sr.text("manager", false)
	server, serverAt := sr.text("server", false)
	sr.finish()
	switch {
	case len(v.errs) > before: // a key that does not read is already reported
	case managerAt != nil && manager != ManagerSystemd:
		v.fail(managerAt.pos, "%s: manager %q is not supported; want %q", sr.where, manager, ManagerSystemd)
	case managerAt != nil && serverAt == nil:
		v.fail(managerAt.pos, "%s: manager needs server, the unit that runs the control server", sr.where)
	case managerAt == nil && serverAt != nil:
		v.fail(serverAt.pos, "%s: server needs manager = %q", sr.where, ManagerSystemd)
	case serverAt != nil && !unitName.MatchString(server):
		v.fail(serverAt.pos, "%s: server %q is not a systemd user unit name: letters, digits and :-_.\\@, ending in .service", sr.where, server)
	case serverAt != nil:
		h.Services = Services{Manager: manager, Server: server}
	}
}
