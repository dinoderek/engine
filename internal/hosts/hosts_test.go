package hosts

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSlots(t *testing.T) {
	cases := []struct {
		name string
		host Host
		want int
	}{
		{"mac from the doc", Host{RAMGB: 48, ReserveGB: 16, GBPerSession: 8}, 4},
		{"devbox from the doc", Host{RAMGB: 32, ReserveGB: 8, GBPerSession: 8}, 3},
		{"rounds down", Host{RAMGB: 30, ReserveGB: 8, GBPerSession: 8}, 2},
		{"max_sessions replaces the derived count", Host{RAMGB: 48, ReserveGB: 16, GBPerSession: 8, MaxSessions: 9}, 9},
		{"max_sessions may be lower", Host{RAMGB: 48, ReserveGB: 16, GBPerSession: 8, MaxSessions: 1}, 1},
		{"reserve above RAM floors at zero", Host{RAMGB: 8, ReserveGB: 16, GBPerSession: 8}, 0},
		{"no gb_per_session", Host{RAMGB: 8}, 0},
	}
	for _, c := range cases {
		if got := c.host.Slots(); got != c.want {
			t.Errorf("%s: %d slots, want %d", c.name, got, c.want)
		}
	}
}

func TestPerHostOverrides(t *testing.T) {
	src := strings.Replace(base, "reserve_gb = 16", "reserve_gb = 0\ngb_per_session = 16", 1)
	c, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if h := c.Hosts["mac"]; h.ReserveGB != 0 || h.GBPerSession != 16 || h.Slots() != 3 {
		t.Errorf("mac: %+v with %d slots, want reserve 0, 16 GB each, 3 slots", h, h.Slots())
	}
	src = strings.Replace(base, "reserve_gb = 16", "max_sessions = 6", 1)
	c, err = Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if h := c.Hosts["mac"]; h.ReserveGB != 8 || h.Slots() != 6 {
		t.Errorf("mac inherits the top reserve and takes max_sessions: %+v", h)
	}
}

func TestHeavy(t *testing.T) {
	src := strings.Replace(base, "reserve_gb = 8", "reserve_gb = 8\nheavy_capabilities = [\"ios\", \"vm\"]", 1)
	src = strings.Replace(src, "ram_gb = 48", "ram_gb = 48\nmax_sessions = 6\nmax_heavy_sessions = 4", 1)
	c, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if h := c.Hosts["mac"]; h.Slots() != 6 || h.HeavySlots() != 4 {
		t.Errorf("mac: %d slots, %d heavy; want 6 and 4", h.Slots(), h.HeavySlots())
	}
	for _, tc := range []struct {
		requires []string
		want     bool
	}{{[]string{"ios"}, true}, {[]string{"macos", "vm"}, true}, {[]string{"docker"}, false}, {nil, false}} {
		if got := c.Heavy(tc.requires); got != tc.want {
			t.Errorf("Heavy(%v) = %v, want %v", tc.requires, got, tc.want)
		}
	}
	// With no heavy cap of its own, every slot may be heavy; with no heavy
	// capabilities named, no task is heavy.
	c, err = Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	if h := c.Hosts["mac"]; h.HeavySlots() != h.Slots() || h.HeavySlots() != 4 {
		t.Errorf("default heavy slots %d, want the host's 4", h.HeavySlots())
	}
	if c.Heavy([]string{"ios", "vm"}) {
		t.Error("a config naming no heavy capability made a task heavy")
	}
}

func TestModel(t *testing.T) {
	c, err := Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	for tier, want := range map[Tier]TierModel{
		Deep:     {Model: "opus", Effort: "high"},
		Standard: {Model: "sonnet", Effort: "high"},
		Light:    {Model: "haiku", Effort: "medium"},
	} {
		got, err := c.Model("claude", tier)
		if err != nil || got != want {
			t.Errorf("Model(claude, %s) = %+v, %v; want %+v", tier, got, err, want)
		}
	}
	if _, err := c.Model("codex", Deep); err == nil || !strings.Contains(err.Error(), `harness "codex"`) {
		t.Errorf("unknown harness: %v", err)
	}
	if _, err := c.Model("claude", "huge"); err == nil || !strings.Contains(err.Error(), `no tier "huge"`) {
		t.Errorf("unknown tier: %v", err)
	}
}

func TestParseTier(t *testing.T) {
	for _, tier := range Tiers {
		if got, err := ParseTier(string(tier)); err != nil || got != tier {
			t.Errorf("ParseTier(%s) = %q, %v", tier, got, err)
		}
	}
	if _, err := ParseTier("gpt-6"); err == nil || !strings.Contains(err.Error(), "want deep, standard or light") {
		t.Errorf("ParseTier(gpt-6): %v", err)
	}
}

func TestLocal(t *testing.T) {
	c, err := Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	if h, err := c.Local("mac"); err != nil || h.Name != "mac" {
		t.Errorf("Local(mac) = %+v, %v", h, err)
	}
	if _, err := c.Local("laptop"); err == nil || !strings.Contains(err.Error(), `host "laptop" is not in hosts.toml (hosts: mac)`) {
		t.Errorf("Local(laptop): %v", err)
	}
	if h := c.Hosts["mac"]; !h.Has("macos") || h.Has("ios") || !h.Runs("claude") || h.Runs("codex") {
		t.Errorf("capabilities or harnesses: %+v", h)
	}
}

func writeConfig(t *testing.T, hostsToml, host string) string {
	t.Helper()
	dir := t.TempDir()
	if hostsToml != "" {
		if err := os.WriteFile(filepath.Join(dir, "hosts.toml"), []byte(hostsToml), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if host != "" {
		if err := os.WriteFile(filepath.Join(dir, "host"), []byte(host), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestOpen(t *testing.T) {
	c, h, err := Open(writeConfig(t, base, "mac\n"))
	if err != nil {
		t.Fatal(err)
	}
	if h.Name != "mac" || h.Slots() != 4 || !slices.Equal(c.HostNames(), []string{"mac"}) {
		t.Errorf("host %+v, config hosts %v", h, c.HostNames())
	}
}

func TestOpenFailures(t *testing.T) {
	cases := []struct {
		name string
		dir  string
		want string
	}{
		{"no hosts.toml", writeConfig(t, "", "mac\n"), "read host config"},
		{"no host file", writeConfig(t, base, ""), "read host file"},
		{"empty host file", writeConfig(t, base, " \n"), "is empty"},
		{"two words in the host file", writeConfig(t, base, "mac mini\n"), "one host name on one line"},
		{"two lines in the host file", writeConfig(t, base, "mac\nlinux\n"), "one host name on one line"},
		{"host not configured", writeConfig(t, base, "laptop\n"), `host "laptop" is not in hosts.toml`},
		{"invalid config", writeConfig(t, "a = 1\n\na = 2\n", "mac\n"), "hosts.toml: line 3: key a is defined twice"},
	}
	for _, c := range cases {
		_, _, err := Open(c.dir)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
		}
	}
}

func TestLoadKeepsTheLine(t *testing.T) {
	_, err := Load(filepath.Join(writeConfig(t, strings.Replace(base, "ram_gb = 48", "ram_gb = 0", 1), ""), "hosts.toml"))
	var e *Error
	if !errors.As(err, &e) || e.Line != 15 || !strings.Contains(err.Error(), "hosts.toml: line 15: host.mac: ram_gb must be at least 1") {
		t.Errorf("got %v", err)
	}
}

func TestConfigDir(t *testing.T) {
	if got := ConfigDir("/home/me"); got != "/home/me/.config/engine" {
		t.Errorf("got %q", got)
	}
}
