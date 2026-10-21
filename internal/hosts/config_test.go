package hosts

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
)

// docExample is the hosts.toml in docs/hosts-and-sessions.md.
func docExample(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../../docs/hosts-and-sessions.md")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile("(?s)```toml\n(.*?)```").FindSubmatch(data)
	if m == nil {
		t.Fatal("no toml block in docs/hosts-and-sessions.md")
	}
	return string(m[1])
}

func TestDocExample(t *testing.T) {
	c, err := Parse(docExample(t))
	if err != nil {
		t.Fatal(err)
	}
	if c.DefaultHarness != "claude" || c.GBPerSession != 8 || c.ReserveGB != 8 {
		t.Errorf("top level: %+v", c)
	}
	claude := c.Harnesses["claude"]
	if claude.MaxSessions != 8 || !strings.HasPrefix(claude.Launch, "{bin} --permission-mode auto") {
		t.Errorf("claude: %+v", claude)
	}
	if got := claude.Tiers[Light]; got != (TierModel{Model: "claude-haiku-5-5", Effort: "medium"}) {
		t.Errorf("claude light tier: %+v", got)
	}
	if codex := c.Harnesses["codex"]; codex.MaxSessions != 2 || codex.Tiers[Deep].Model != "gpt-6.1-sol" || codex.Tiers[Standard].Effort != "medium" {
		t.Errorf("codex: %+v", codex)
	}
	if got := c.HostNames(); len(got) != 2 || got[0] != "devbox" || got[1] != "mac" {
		t.Errorf("hosts: %v", got)
	}
	mac := c.Hosts["mac"]
	if mac.RAMGB != 48 || mac.ReserveGB != 16 || mac.GBPerSession != 8 || !mac.Has("ios") || mac.Has("linux") || !mac.Runs("codex") {
		t.Errorf("mac: %+v", mac)
	}
	if mac.Bin["claude"] != "~/.local/bin/claude" || mac.Bin["codex"] != "~/.local/bin/codex" || mac.Address != "<user>@<tailscale name>" {
		t.Errorf("mac bin or address: %+v", mac)
	}
	devbox := c.Hosts["devbox"]
	if devbox.ReserveGB != 8 || devbox.RAMGB != 32 || len(devbox.Capabilities) != 3 {
		t.Errorf("devbox: %+v", devbox)
	}
	if mac.Slots() != 4 || devbox.Slots() != 3 {
		t.Errorf("slots: mac %d, devbox %d; want 4 and 3", mac.Slots(), devbox.Slots())
	}
}

const base = `default_harness = "claude"
gb_per_session = 8
reserve_gb = 8

[harness.claude]
max_sessions = 8
launch = "{bin} --model {model} --effort {effort} -n {name}"
tiers.deep     = { model = "opus", effort = "high" }
tiers.standard = { model = "sonnet", effort = "high" }
tiers.light    = { model = "haiku", effort = "medium" }

[host.mac]
address = "me@mac"
harnesses = ["claude"]
ram_gb = 48
reserve_gb = 16
capabilities = ["macos"]
bin = { claude = "~/bin/claude" }
`

func TestBaseIsValid(t *testing.T) {
	c, err := Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	if c.Hosts["mac"].Slots() != 4 {
		t.Errorf("slots: %d", c.Hosts["mac"].Slots())
	}
}

// lineOf is the 1-based line of src that holds needle.
func lineOf(t *testing.T, src, needle string) int {
	t.Helper()
	for i, line := range strings.Split(src, "\n") {
		if strings.Contains(line, needle) {
			return i + 1
		}
	}
	t.Fatalf("%q is not in:\n%s", needle, src)
	return 0
}

func TestValidationRules(t *testing.T) {
	// Each case replaces old with new (once) in base, and expects the
	// violation on the line holding at.
	cases := []struct {
		name, old, new, at, want string
	}{
		{"missing default_harness", "default_harness = \"claude\"\n", "", "gb_per_session", "missing default_harness"},
		{"empty default_harness", `"claude"
gb`, `""
gb`, "default_harness", "must not be empty"},
		{"undefined default_harness", `default_harness = "claude"`, `default_harness = "codex"`, "default_harness", `"codex" is not defined`},
		{"missing gb_per_session", "gb_per_session = 8\n", "", "default_harness", "missing gb_per_session"},
		{"zero gb_per_session", "gb_per_session = 8", "gb_per_session = 0", "gb_per_session", "at least 1"},
		{"missing reserve_gb", "reserve_gb = 8\n", "", "default_harness", "missing reserve_gb"},
		{"negative reserve_gb", "reserve_gb = 8", "reserve_gb = -1", "reserve_gb = -1", "at least 0"},
		{"unknown top-level key", "gb_per_session = 8", "gb_per_session = 8\nbogus = 1", "bogus", "unknown key bogus"},
		{"wrong type", "gb_per_session = 8", `gb_per_session = "8"`, "gb_per_session", "must be an integer"},
		{"no harness", "[harness.claude]", "[harnes.claude]", "default_harness", "missing harness"},
		{"harness not a table", "[harness.claude]", "[harness]\nx = 1\n[harness.claude]", "x = 1", "harness.x must be a table"},
		{"harness missing max_sessions", "max_sessions = 8\n", "", "[harness.claude]", "missing max_sessions"},
		{"harness zero max_sessions", "max_sessions = 8", "max_sessions = 0", "max_sessions", "at least 1"},
		{"harness missing launch", `launch = "{bin} --model {model} --effort {effort} -n {name}"`, "", "[harness.claude]", "missing launch"},
		{"launch without bin", `"{bin} --model`, `"claude --model`, "launch", "must start with {bin}"},
		{"launch bin elsewhere", `"{bin} --model {model}`, `"x {bin} --model {model}`, "launch", "must start with {bin}"},
		{"launch bin glued", `"{bin} --model`, `"{bin}x --model`, "launch", "must start with {bin}"},
		{"launch without model", "--model {model} ", "", "launch", "must contain {model}"},
		{"launch unknown placeholder", "-n {name}", "-n {bogus}", "launch", "unknown placeholder {bogus}"},
		{"harness unknown key", "max_sessions = 8", "max_sessions = 8\nextra = 1", "extra", "unknown key extra"},
		{"harness missing tiers", "tiers.deep     = { model = \"opus\", effort = \"high\" }\ntiers.standard = { model = \"sonnet\", effort = \"high\" }\ntiers.light    = { model = \"haiku\", effort = \"medium\" }\n", "", "[harness.claude]", "missing tiers"},
		{"harness missing tier", "tiers.light    = { model = \"haiku\", effort = \"medium\" }\n", "", "tiers.deep", "harness.claude.tiers: missing light"},
		{"unknown tier", "tiers.light ", "tiers.huge = { model = \"m\", effort = \"e\" }\ntiers.light ", "tiers.huge", "unknown key huge"},
		{"tier missing effort", `{ model = "haiku", effort = "medium" }`, `{ model = "haiku" }`, "tiers.light", "missing effort"},
		{"tier missing model", `{ model = "sonnet", effort = "high" }`, `{ effort = "high" }`, "tiers.standard", "missing model"},
		{"tier empty model", `{ model = "opus",`, `{ model = "",`, "tiers.deep", "must not be empty"},
		{"tier not a table", `tiers.deep     = { model = "opus", effort = "high" }`, `tiers.deep = "opus"`, "tiers.deep", "must be a table"},
		{"tier unknown key", `{ model = "haiku", effort = "medium" }`, `{ model = "haiku", effort = "medium", cost = "low" }`, "tiers.light", "unknown key cost"},
		{"no host", "[host.mac]", "[hosting.mac]", "default_harness", "missing host"},
		{"host not a table", "[host.mac]", "[host]\nx = 1\n[host.mac]", "x = 1", "host.x must be a table"},
		{"missing address", "address = \"me@mac\"\n", "", "[host.mac]", "missing address"},
		{"address without @", `"me@mac"`, `"mac"`, "address", "must be <user>@<tailscale name>"},
		{"address without user", `"me@mac"`, `"@mac"`, "address", "must be <user>@<tailscale name>"},
		{"address without machine", `"me@mac"`, `"me@"`, "address", "must be <user>@<tailscale name>"},
		{"address that looks like an ssh option", `"me@mac"`, `"-oProxyCommand=x@mac"`, "address", "must be <user>@<tailscale name>"},
		{"bin skipped when harnesses is malformed", `harnesses = ["claude"]`, `harnesses = 5`, "harnesses =", "must be an array of strings"},
		{"address with two @", `"me@mac"`, `"me@mac@x"`, "address", "must be <user>@<tailscale name>"},
		{"missing harnesses", "harnesses = [\"claude\"]\n", "", "[host.mac]", "missing harnesses"},
		{"empty harnesses", `["claude"]`, `[]`, "harnesses = []", "at least one harness"},
		{"unknown host harness", `["claude"]`, `["claude", "codex"]`, "harnesses =", `"codex", which is not defined`},
		{"duplicate host harness", `["claude"]`, `["claude", "claude"]`, "harnesses =", `"claude" twice`},
		{"missing ram_gb", "ram_gb = 48\n", "", "[host.mac]", "missing ram_gb"},
		{"zero ram_gb", "ram_gb = 48", "ram_gb = 0", "ram_gb", "at least 1"},
		{"negative host reserve_gb", "reserve_gb = 16", "reserve_gb = -2", "reserve_gb = -2", "at least 0"},
		{"zero host gb_per_session", "reserve_gb = 16", "reserve_gb = 16\ngb_per_session = 0", "gb_per_session = 0", "at least 1"},
		{"zero host max_sessions", "reserve_gb = 16", "reserve_gb = 16\nmax_sessions = 0", "max_sessions = 0", "at least 1"},
		{"unknown capability", `["macos"]`, `["macos", "gpu"]`, "capabilities", `unknown capability "gpu"`},
		{"duplicate capability", `["macos"]`, `["macos", "macos"]`, "capabilities", `"macos" twice`},
		{"capabilities not an array", `["macos"]`, `"macos"`, "capabilities", "must be an array of strings"},
		{"missing bin", "bin = { claude = \"~/bin/claude\" }\n", "", "[host.mac]", "missing bin"},
		{"bin lacks a harness", `{ claude = "~/bin/claude" }`, `{}`, "bin =", "host.mac.bin: missing claude"},
		{"bin names another harness", `{ claude = "~/bin/claude" }`, `{ claude = "~/bin/claude", codex = "x" }`, "bin =", "unknown key codex"},
		{"empty bin path", `"~/bin/claude"`, `""`, "bin =", "must not be empty"},
		{"unknown host key", "ram_gb = 48", "ram_gb = 48\ncolor = \"red\"", "color", "unknown key color"},
		{"cloud host", "[host.mac]", "[host.cloud]\nkind = \"cloud\"\nharnesses = [\"claude\"]\nmax_sessions = 4\n[host.mac]", "[host.cloud]", "host.cloud: missing address"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(base, c.old) {
				t.Fatalf("base lacks %q", c.old)
			}
			src := strings.Replace(base, c.old, c.new, 1)
			_, err := Parse(src)
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("got %v, want an *Error\n%s", err, src)
			}
			line := lineOf(t, src, c.at)
			if e.Line != line || !strings.Contains(e.Msg, c.want) {
				t.Errorf("got line %d: %s\nwant line %d containing %q\n%s", e.Line, e.Msg, line, c.want, src)
			}
		})
	}
}

func TestFirstViolationWins(t *testing.T) {
	// The host's bad ram_gb is found before the unknown top-level key, but the
	// key is earlier in the file.
	src := strings.Replace(base, "gb_per_session = 8", "gb_per_session = 8\nbogus = 1", 1)
	src = strings.Replace(src, "ram_gb = 48", "ram_gb = 0", 1)
	_, err := Parse(src)
	var e *Error
	if !errors.As(err, &e) || e.Line != 3 || !strings.Contains(e.Msg, "unknown key bogus") {
		t.Errorf("got %v, want the unknown key on line 3", err)
	}
}

func TestEmptyFileAndSections(t *testing.T) {
	cases := map[string]string{
		"": "missing default_harness",
		`default_harness = "claude"
gb_per_session = 8
reserve_gb = 8
harness = {}
host = {}
`: "no [harness.<name>] is defined",
	}
	for src, want := range cases {
		_, err := Parse(src)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) = %v, want %q", src, err, want)
		}
	}
	_, err := Parse(strings.Replace(base, "[host.mac]", "host = {}\n[x]", 1))
	if err == nil {
		t.Error("an empty host table and a header both define host")
	}
	noHosts := base[:strings.Index(base, "[host.mac]")] + "[host]\n"
	if _, err := Parse(noHosts); err == nil || !strings.Contains(err.Error(), "no [host.<name>] is defined") {
		t.Errorf("no hosts: %v", err)
	}
}

func TestSyntaxErrorsCarryTheirLine(t *testing.T) {
	_, err := Parse(base + "\n[host.devbox]\naddress = \"\"\"x\"\"\"\n")
	var e *Error
	if !errors.As(err, &e) || e.Line != strings.Count(base, "\n")+3 {
		t.Errorf("got %v", err)
	}
	_, err = Parse(base + "[host.mac]\n")
	if !errors.As(err, &e) || e.Line != strings.Count(base, "\n")+1 || !strings.Contains(e.Msg, "table host.mac is defined twice") {
		t.Errorf("duplicate host: %v", err)
	}
}
