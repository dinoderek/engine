package hosts

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// withServices is base with a services table appended to host mac.
func withServices(body string) string {
	return base + "[host.mac.services]\n" + body
}

func TestServicesLoad(t *testing.T) {
	c, err := Parse(withServices("manager = \"systemd\"\nserver = \"engine-server.service\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	got := c.Hosts["mac"].Services
	if got != (Services{Manager: "systemd", Server: "engine-server.service"}) || !got.Recorded() {
		t.Errorf("services: %+v", got)
	}
}

func TestNoServicesTableLoadsAsNone(t *testing.T) {
	c, err := Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	if s := c.Hosts["mac"].Services; s != (Services{}) || s.Recorded() {
		t.Errorf("services: %+v, want none", s)
	}
}

func TestEmptyServicesTableLoadsAsNone(t *testing.T) {
	c, err := Parse(withServices(""))
	if err != nil {
		t.Fatal(err)
	}
	if c.Hosts["mac"].Services.Recorded() {
		t.Errorf("an empty table records nothing: %+v", c.Hosts["mac"].Services)
	}
}

func TestServicesRefusals(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"other manager", "manager = \"launchd\"\nserver = \"engine-server.service\"\n", `host.mac.services: manager "launchd" is not supported`},
		{"manager without server", "manager = \"systemd\"\n", "host.mac.services: manager needs server"},
		{"server without manager", "server = \"engine-server.service\"\n", "host.mac.services: server needs manager"},
		{"unit without .service", "manager = \"systemd\"\nserver = \"engine-server\"\n", `host.mac.services: server "engine-server" is not a systemd user unit name`},
		{"unit of another type", "manager = \"systemd\"\nserver = \"engine.timer\"\n", "is not a systemd user unit name"},
		{"unit with a shell metacharacter", "manager = \"systemd\"\nserver = \"a;reboot.service\"\n", "is not a systemd user unit name"},
		{"unit with a substitution", "manager = \"systemd\"\nserver = \"$(id).service\"\n", "is not a systemd user unit name"},
		{"unit with whitespace", "manager = \"systemd\"\nserver = \"engine server.service\"\n", "is not a systemd user unit name"},
		{"unit with a quote", "manager = \"systemd\"\nserver = \"it's.service\"\n", "is not a systemd user unit name"},
		{"unit that reads as an option", "manager = \"systemd\"\nserver = \"--now.service\"\n", "is not a systemd user unit name"},
		{"unit with no name", "manager = \"systemd\"\nserver = \".service\"\n", "is not a systemd user unit name"},
		{"unknown key", "manager = \"systemd\"\nserver = \"engine-server.service\"\nclient = \"x.service\"\n", "host.mac.services: unknown key client"},
		{"manager of the wrong type", "manager = 1\nserver = \"engine-server.service\"\n", "host.mac.services: manager must be a string"},
		{"server of the wrong type", "manager = \"systemd\"\nserver = [\"engine-server.service\"]\n", "host.mac.services: server must be a string"},
		{"empty manager", "manager = \"\"\nserver = \"engine-server.service\"\n", "host.mac.services: manager must not be empty"},
		{"empty server", "manager = \"systemd\"\nserver = \"\"\n", "host.mac.services: server must not be empty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(withServices(c.body))
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("got %v, want an *Error", err)
			}
			if !strings.Contains(e.Msg, c.want) {
				t.Errorf("got %q, want %q", e.Msg, c.want)
			}
		})
	}
}

func TestServicesAcceptsSystemdUnitNames(t *testing.T) {
	for _, unit := range []string{"engine-server.service", "engine_server.service", "a@b.service", `a\x2db.service`, "a:b.c.service", "9.service"} {
		src := withServices("manager = \"systemd\"\nserver = \"" + strings.ReplaceAll(unit, `\`, `\\`) + "\"\n")
		c, err := Parse(src)
		if err != nil {
			t.Errorf("%s: %v", unit, err)
			continue
		}
		if got := c.Hosts["mac"].Services.Server; got != unit {
			t.Errorf("server %q, want %q", got, unit)
		}
	}
}

func TestServerCommand(t *testing.T) {
	h := Host{Name: "devbox", Services: Services{Manager: ManagerSystemd, Server: "engine-server.service"}}
	for op, want := range map[ServerOp]string{
		ServerStart:    "systemctl --user start engine-server.service",
		ServerStop:     "systemctl --user stop engine-server.service",
		ServerIsActive: "systemctl --user is-active engine-server.service",
	} {
		argv, err := h.ServerCommand(op)
		if err != nil || !slices.Equal(argv, strings.Fields(want)) {
			t.Errorf("%s argv: %v, %v; want %q", op, argv, err, want)
		}
		line, err := h.ServerShellLine(op)
		if err != nil || line != want {
			t.Errorf("%s line: %q, %v; want %q", op, line, err, want)
		}
	}
}

func TestServerShellLineQuotesTheUnit(t *testing.T) {
	// Load refuses such a unit; a Host built by hand still gets a safe line.
	h := Host{Name: "devbox", Services: Services{Manager: ManagerSystemd, Server: "a b;c.service"}}
	line, err := h.ServerShellLine(ServerStop)
	if err != nil || line != "systemctl --user stop 'a b;c.service'" {
		t.Errorf("line %q, %v", line, err)
	}
}

func TestServerCommandRefusals(t *testing.T) {
	recorded := Host{Name: "devbox", Services: Services{Manager: ManagerSystemd, Server: "engine-server.service"}}
	if _, err := recorded.ServerCommand("restart"); err == nil || !strings.Contains(err.Error(), `unknown server operation "restart"`) {
		t.Errorf("unknown op: %v", err)
	}
	bare := Host{Name: "devbox"}
	if _, err := bare.ServerCommand(ServerStart); err == nil || !strings.Contains(err.Error(), "host devbox records no [services] table") {
		t.Errorf("no services: %v", err)
	}
	if _, err := bare.ServerShellLine(ServerStart); err == nil {
		t.Error("a host with no services has no shell line")
	}
}
