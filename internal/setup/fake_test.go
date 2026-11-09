package setup

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/harness/harnesstest"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/version"
)

const (
	callerRev    = "2a08a0c1f2a2b3c4d5e6f708192a3b4c5d6e7f80"
	callerPlugin = "0.7.1"
	callerGo     = "go1.27.3"
	boxAddress   = "dev@box"
	secret       = "ghp_secret_token"
)

const hostsTOML = `default_harness = "claude"
gb_per_session = 8
reserve_gb = 8

[harness.claude]
max_sessions = 8
launch = "{bin} --model {model} --effort {effort} -n {name} --remote-control {name}"
tiers.deep     = { model = "opus-x",   effort = "high" }
tiers.standard = { model = "sonnet-x", effort = "high" }
tiers.light    = { model = "haiku-x",  effort = "medium" }

[host.mac]
address = "me@mac"
harnesses = ["claude"]
ram_gb = 24
capabilities = ["macos"]
bin = { claude = "~/bin/claude" }

[host.box]
address = "dev@box"
harnesses = ["claude"]
ram_gb = 24
capabilities = ["linux"]
bin = { claude = "~/bin/claude" }
`

// codexHostsTOML lists codex on both hosts, so setup runs the Codex steps.
var codexHostsTOML = strings.NewReplacer(
	`harnesses = ["claude"]`, `harnesses = ["claude", "codex"]`,
	`bin = { claude = "~/bin/claude" }`, `bin = { claude = "~/bin/claude", codex = "~/.local/bin/codex" }`,
	`[host.mac]`, "[harness.codex]\nmax_sessions = 2\nlaunch = \"{bin} -m {model}\"\n"+
		"tiers.deep     = { model = \"sol\", effort = \"high\" }\n"+
		"tiers.standard = { model = \"sol\", effort = \"medium\" }\n"+
		"tiers.light    = { model = \"luna\", effort = \"medium\" }\n\n[host.mac]",
).Replace(hostsTOML)

// callerHome is the Mac's home: host config, host file and token.
func callerHome(t *testing.T) string { return callerHomeWith(t, hostsTOML) }

// callerHomeWith is callerHome with the host config toml.
func callerHomeWith(t *testing.T, toml string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{"hosts.toml": toml, "host": "mac\n"} {
		path := filepath.Join(dir, ".config", "engine", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// box is a host that answers the commands setup runs, and remembers what
// changed. mutations lists the commands that changed something.
type box struct {
	missing     []string // tools not on the PATH
	rev         string   // short commit of the engine on the PATH; "" is none
	goVer       string   // Go that built it; "" is go1.27.2
	installTo   string   // what go install leaves on the PATH; "" is the commit asked for
	installFail bool
	files       map[string]string
	badMode     map[string]bool
	writeFail   bool
	gh          string // gh auth status output; "" is signed in with a token in gh's config file
	ghCode      int    // gh auth status exit code
	os          string
	noSystemd   bool
	masked      int // sleep targets masked
	down        bool

	mutations []string
	commands  []string
	stdin     []string
}

// staleBox is the devbox as it was: an old engine, no config, Linux with
// every sleep target masked.
func staleBox() *box {
	return &box{
		rev: "d8c1744", files: map[string]string{}, badMode: map[string]bool{}, os: "Linux", masked: 4,
	}
}

// settled is staleBox after a setup: nothing left to do.
func settledBox(home string) *box {
	b := staleBox()
	b.rev, b.goVer = callerRev[:7], callerGo
	for _, name := range []string{"hosts.toml"} {
		data, _ := os.ReadFile(filepath.Join(home, ".config", "engine", name))
		b.files[name] = string(data)
	}
	b.files["host"] = "box\n"
	return b
}

func (b *box) mutate(cmd string) { b.mutations = append(b.mutations, cmd) }

// answer is the host's reply to one command line.
func (b *box) answer(cmd, stdin string, out io.Writer, errOut io.Writer) int {
	b.commands = append(b.commands, cmd)
	say := func(format string, args ...any) int { fmt.Fprintf(out, format, args...); return 0 }
	switch {
	case strings.HasPrefix(cmd, "for t in"):
		return say("%s", strings.Join(b.missing, "\n"))
	case cmd == "command -v engine":
		if b.rev == "" {
			return 1
		}
		return say("/home/me/.local/bin/engine\n")
	case cmd == "engine version":
		if b.rev == "" {
			fmt.Fprint(errOut, "engine: command not found")
			return 127
		}
		goVer := b.goVer
		if goVer == "" {
			goVer = "go1.27.2"
		}
		if len(b.rev) == 12 { // built by go install module@commit: no VCS settings
			return say("engine v0.0.0-20261025174939-%s (%s)\n", b.rev, goVer)
		}
		return say("engine v0.0.0 (%s, %s)\n", b.rev, goVer)
	case strings.Contains(cmd, "go install"):
		return b.goInstall(cmd, errOut)
	case strings.HasPrefix(cmd, "cmp -s - "):
		return b.cmp(cmd, stdin)
	case strings.HasPrefix(cmd, "find "):
		return b.find(cmd, out)
	case strings.HasPrefix(cmd, "chmod 600 "):
		b.mutate(cmd)
		b.badMode[fileOf(cmd)] = false
		return 0
	case strings.HasPrefix(cmd, "umask 077"):
		return b.write(cmd, stdin, errOut)
	case cmd == "gh auth status -h github.com":
		return b.ghStatus(out, errOut)
	case strings.HasPrefix(cmd, "uname -s") && b.noSystemd:
		return say("%s\n", b.os)
	case strings.HasPrefix(cmd, "uname -s"):
		return say("%s\n%s", b.os, strings.Repeat("masked\n", b.masked)+strings.Repeat("enabled\n", 4-b.masked))
	}
	fmt.Fprintf(errOut, "box: unexpected command %q", cmd)
	return 99
}

// ghFileToken is gh auth status for a token in gh's config file.
const ghFileToken = "github.com\n  ✓ Logged in to github.com account octo (/home/me/.config/gh/hosts.yml)\n  - Active account: true\n"

// ghStatus answers gh auth status: on stdout when signed in, on stderr when
// not, as gh does.
func (b *box) ghStatus(out, errOut io.Writer) int {
	status := b.gh
	if status == "" {
		status = ghFileToken
	}
	if b.ghCode != 0 {
		fmt.Fprint(errOut, status)
	} else {
		fmt.Fprint(out, status)
	}
	return b.ghCode
}

func (b *box) goInstall(cmd string, errOut io.Writer) int {
	b.mutate(cmd)
	if b.installFail {
		fmt.Fprint(errOut, "go: network is unreachable")
		return 1
	}
	_, commit, _ := strings.Cut(cmd, "@")
	b.rev, b.goVer = commit[:12], goToolchain(cmd)
	if b.installTo != "" {
		b.rev = b.installTo
	}
	return 0
}

// goToolchain is the Go a go install line pins with GOTOOLCHAIN, or the host's
// own go1.27.2 when it pins none.
func goToolchain(cmd string) string {
	for _, word := range strings.Fields(cmd) {
		if v, found := strings.CutPrefix(word, "GOTOOLCHAIN="); found {
			return v
		}
	}
	return "go1.27.2"
}

// fileOf is the name of the config file a command names.
func fileOf(cmd string) string {
	for _, name := range []string{"hosts.toml", "host"} {
		if strings.Contains(cmd, `/`+name) {
			return name
		}
	}
	return ""
}

// cmp is cmp -s - <file>: 0 when the file holds exactly stdin.
func (b *box) cmp(cmd, stdin string) int {
	if content, found := b.files[fileOf(cmd)]; found && content == stdin {
		return 0
	}
	return 1
}

func (b *box) find(cmd string, out io.Writer) int {
	if name := fileOf(cmd); !b.badMode[name] {
		fmt.Fprintln(out, name)
	}
	return 0
}

func (b *box) write(cmd, stdin string, errOut io.Writer) int {
	b.mutate(cmd)
	if b.writeFail {
		fmt.Fprint(errOut, "cat: No space left on device")
		return 1
	}
	b.stdin = append(b.stdin, stdin)
	name := fileOf(cmd)
	b.files[name], b.badMode[name] = stdin, false
	return 0
}

// rig is a setup run against a box, with the Mac's own answers.
type rig struct {
	home  string
	box   *box
	local *box // the Mac, which answers sh
	ssh   []string
	sh    []string
	out   strings.Builder
	// checked are the harnesses checked, in order; check answers the
	// default's check and checkCodex the codex harness's.
	checked    []string
	check      func() (bool, error)
	checkCodex func() (bool, error)
	gh         session.RunFunc
	info       version.Info
	goMod      string // go.mod at the caller's commit, as GitHub serves it
	// claude and codex are the fake adapters, in the registry's order.
	claude, codex *harnesstest.Fake
}

// fakeAdapter is an adapter called name whose setup steps all pass, and
// whose failed check is open with its own fix.
func fakeAdapter(name string, steps ...string) *harnesstest.Fake {
	f := &harnesstest.Fake{Called: name, Failed: [2]string{"a " + name + " session would stop", "fix " + name}}
	for _, s := range steps {
		f.Steps = append(f.Steps, harnesstest.FakeStep{Name: s, Outcome: harness.Passed("")})
	}
	return f
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{home: callerHome(t), box: staleBox(), info: version.Info{Revision: callerRev},
		goMod: "module github.com/dinoderek/engine\n\ngo 1.27.0\n\ntoolchain " + callerGo + "\n"}
	r.local = &box{files: map[string]string{}, badMode: map[string]bool{}, os: "Darwin", rev: "2a08a0c"}
	r.claude = fakeAdapter("claude", "plugin", "sign-in")
	r.claude.SetupTools, r.claude.PluginVersion = []string{"claude"}, callerPlugin
	r.codex = fakeAdapter("codex", "codex", "codex sign-in", "codex home", "codex plugin", "codex daemon")
	r.check = func() (bool, error) { return true, nil }
	r.checkCodex = r.check
	r.gh = func(_ string, env []string, name string, args ...string) (string, error) {
		if len(env) != 0 {
			return "", fmt.Errorf("gh got env %v", env)
		}
		if strings.Contains(strings.Join(args, " "), "contents/go.mod?ref="+callerRev) {
			return r.goMod, nil
		}
		return callerRev + "\n", nil
	}
	return r
}

func (r *rig) exec(in io.Reader, out, errOut io.Writer, name string, args ...string) (int, error) {
	stdin := ""
	if in != nil {
		data, _ := io.ReadAll(in)
		stdin = string(data)
	}
	command := args[len(args)-1]
	switch name {
	case "ssh":
		r.ssh = append(r.ssh, strings.Join(args, " "))
		if r.box.down {
			fmt.Fprint(errOut, "ssh: connect to host box port 22: No route to host")
			return 255, nil
		}
		return r.box.answer(command, stdin, out, errOut), nil
	case "sh":
		r.sh = append(r.sh, command)
		return r.local.answer(command, stdin, out, errOut), nil
	}
	return -1, fmt.Errorf("unexpected %s", name)
}

func (r *rig) deps() Deps {
	return Deps{
		Home: r.home, Getenv: func(k string) string {
			if k == "SHELL" {
				return "/bin/zsh"
			}
			return ""
		}, Exec: r.exec, GH: r.gh, Info: r.info, Harnesses: harness.NewAdapters(r.claude, r.codex),
		Check: func(name string, out io.Writer) (bool, error) {
			fmt.Fprintln(out, "the check report")
			r.checked = append(r.checked, name)
			if name == "codex" {
				return r.checkCodex()
			}
			return r.check()
		},
	}
}

func (r *rig) run(host string) (bool, error) {
	r.out.Reset()
	return Run(host, r.deps(), &r.out)
}
