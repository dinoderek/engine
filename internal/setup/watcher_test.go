package setup

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// watcherAnswer answers what the watcher step runs: on the server's host,
// the reads of its config and bot token; on the Mac, launchctl.
func (b *box) watcherAnswer(cmd string, out, errOut io.Writer) (int, bool) {
	if name, found := strings.CutPrefix(cmd, `cat "$HOME/.config/engine/`); found {
		content, ok := b.files[strings.TrimSuffix(name, `"`)]
		if !ok {
			fmt.Fprint(errOut, "cat: No such file or directory")
			return 1, true
		}
		fmt.Fprint(out, content)
		return 0, true
	}
	switch {
	case cmd == "launchctl print gui/$(id -u)/"+WatchLabel:
		if b.agentLoaded {
			return 0, true
		}
		fmt.Fprint(errOut, "Could not find service")
		return 113, true
	case cmd == "launchctl bootout gui/$(id -u)/"+WatchLabel:
		b.mutate(cmd)
		b.agentLoaded = false
		return 0, true
	case strings.HasPrefix(cmd, "launchctl bootstrap gui/$(id -u) "):
		b.mutate(cmd)
		if b.bootstrapFail {
			fmt.Fprint(errOut, "Bootstrap failed: 5: Input/output error")
			return 5, true
		}
		b.agentLoaded = true
		return 0, true
	}
	return 0, false
}

const watchToken = "123456:AAbbccddeeffgghhiijjkkllmmnnooppqq"

// watcherRig is a Mac whose hosts.toml records box as the server's host,
// with Telegram paired on box.
func watcherRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	r.home = callerHomeWith(t, hostsTOML+"[host.box.services]\nmanager = \"systemd\"\nserver = \"engine-server.service\"\n")
	r.os = "darwin"
	r.box.files["config.toml"] = "[server]\nhost = \"box\"\n\n[telegram]\nchat = 4242\n"
	r.box.files["telegram-token"] = watchToken + "\n"
	return r
}

func (r *rig) agentFiles(t *testing.T) (token, plist string) {
	t.Helper()
	for _, f := range []struct {
		path string
		mode os.FileMode
		into *string
	}{
		{filepath.Join(r.home, ".config", "engine", "watch-telegram-token"), 0o600, &token},
		{filepath.Join(r.home, "Library", "LaunchAgents", WatchLabel+".plist"), 0o644, &plist},
	} {
		info, err := os.Stat(f.path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != f.mode {
			t.Errorf("%s has mode %o, want %o", f.path, info.Mode().Perm(), f.mode)
		}
		data, _ := os.ReadFile(f.path)
		*f.into = string(data)
	}
	return token, plist
}

func TestSetupInstallsTheWatcherOnTheMac(t *testing.T) {
	r := watcherRig(t)
	if !mustRun(t, r, "") {
		t.Fatalf("open:\n%s", r.out.String())
	}
	out := r.out.String()
	for _, want := range []string{"mac (this host; the watcher steps act)", "10. watcher: changed (watching box; agent " + WatchLabel + " loaded)"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, watchToken) {
		t.Errorf("setup printed the token:\n%s", out)
	}
	token, plist := r.agentFiles(t)
	if _, err := os.Stat(filepath.Join(r.home, ".local", "share", "engine")); err != nil {
		t.Errorf("the log's directory: %v", err)
	}
	if token != watchToken+"\n" {
		t.Errorf("token copy %q", token)
	}
	for _, want := range []string{
		"<string>" + WatchLabel + "</string>",
		"<string>" + filepath.Join(r.home, ".local", "bin", "engine") + "</string>\n\t\t<string>watch</string>\n\t\t<string>--chat</string>\n\t\t<string>4242</string>",
		"<key>KeepAlive</key>\n\t<true/>", "<string>" + filepath.Join(r.home, ".local/share/engine/watch.log") + "</string>",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist lacks %q:\n%s", want, plist)
		}
	}
	want := `launchctl bootstrap gui/$(id -u) ` + filepath.Join(r.home, "Library", "LaunchAgents", WatchLabel+".plist")
	if len(r.local.mutations) != 1 || r.local.mutations[0] != want {
		t.Errorf("mutations %q, want %q", r.local.mutations, want)
	}
	for _, c := range r.box.commands {
		if c != `cat "$HOME/.config/engine/config.toml"` && c != `cat "$HOME/.config/engine/telegram-token"` {
			t.Errorf("the server's host ran %q", c)
		}
	}
}

func TestTheWatcherStepIsIdempotent(t *testing.T) {
	r := watcherRig(t)
	mustRun(t, r, "")
	r.local.mutations = nil
	if !mustRun(t, r, "") || !strings.Contains(r.out.String(), "10. watcher: ok (watching box)") || len(r.local.mutations) != 0 {
		t.Fatalf("a second run changed %q:\n%s", r.local.mutations, r.out.String())
	}
	// A new token on the server's host, or a loose mode, reloads the agent.
	r.box.files["telegram-token"] = strings.Replace(watchToken, "AA", "ZZ", 1)
	mustRun(t, r, "")
	if token, _ := r.agentFiles(t); !strings.HasPrefix(token, "123456:ZZ") || len(r.local.mutations) != 2 || !strings.HasPrefix(r.local.mutations[0], "launchctl bootout") {
		t.Errorf("token %q, mutations %q", token, r.local.mutations)
	}
	// A new engine changes the agent's build, which reloads it.
	r.local.mutations = nil
	r.info.Revision = strings.Repeat("c", 40)
	mustRun(t, r, "")
	if _, plist := r.agentFiles(t); !strings.Contains(plist, "<key>ENGINE_WATCH_BUILD</key>\n\t\t<string>"+r.info.String()+"</string>") || len(r.local.mutations) != 2 {
		t.Errorf("a new build did not reload: %q\n%s", r.local.mutations, plist)
	}
	r.local.mutations = nil
	path := filepath.Join(r.home, ".config", "engine", "watch-telegram-token")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, r, "")
	r.agentFiles(t)
	if len(r.local.mutations) != 2 {
		t.Errorf("a loose mode did not reload: %q", r.local.mutations)
	}
}

func TestTheWatcherStepSkips(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		prep       func(*rig)
	}{
		{"on Linux", "10. watcher: ok (not macOS)", func(r *rig) { r.os = "linux" }},
		{"with no server host", "10. watcher: ok (no other host records the server's services)", func(r *rig) {
			r.home = callerHome(r.t)
		}},
		{"on the server's host", "11. watcher: ok (no other host records the server's services)", func(r *rig) {
			r.home = callerHomeWith(r.t, hostsTOML+"[host.mac.services]\nmanager = \"systemd\"\nserver = \"engine-server.service\"\n")
		}},
		{"with no Telegram chat", "10. watcher: ok (Telegram is not paired on box", func(r *rig) { delete(r.box.files, "config.toml") }},
		{"with no token", "10. watcher: ok (no bot token on box", func(r *rig) { delete(r.box.files, "telegram-token") }},
		{"with a bad token", "10. watcher: ok (the bot token on box is not a valid credential", func(r *rig) { r.box.files["telegram-token"] = "short" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := watcherRig(t)
			r.t = t
			tc.prep(r)
			if !mustRun(t, r, "") || !strings.Contains(r.out.String(), tc.want) || len(r.local.mutations) != 0 {
				t.Errorf("mutations %q, want %q:\n%s", r.local.mutations, tc.want, r.out.String())
			}
			if _, err := os.Stat(filepath.Join(r.home, ".config", "engine", "watch-telegram-token")); err == nil {
				t.Error("a skipped step wrote the token copy")
			}
		})
	}
}

func TestTheWatcherStepOpens(t *testing.T) {
	r := watcherRig(t)
	r.local.bootstrapFail = true
	if mustRun(t, r, "") || !strings.Contains(r.out.String(), "10. watcher: OPEN (launchctl could not load the watcher: Bootstrap failed: 5: Input/output error)") {
		t.Errorf("a failed bootstrap:\n%s", r.out.String())
	}
	r = watcherRig(t)
	r.box.down = true
	if mustRun(t, r, "") || !strings.Contains(r.out.String(), "10. watcher: OPEN (cannot read the Telegram chat on box: ") {
		t.Errorf("the server's host down:\n%s", r.out.String())
	}
	if _, err := os.Stat(filepath.Join(r.home, ".config", "engine", "watch-telegram-token")); err == nil {
		t.Error("an unreachable server's host wrote the token copy")
	}
	r = watcherRig(t)
	r.box.files["config.toml"] = "[telegram]\nchat = 'bad'\n"
	if mustRun(t, r, "") || !strings.Contains(r.out.String(), "10. watcher: OPEN (cannot read the Telegram chat on box: its config.toml: ") {
		t.Errorf("a config.toml that does not parse:\n%s", r.out.String())
	}
	r = watcherRig(t)
	r.home = callerHomeWith(t, hostsTOML+"[host.box.services]\nmanager = \"systemd\"\nserver = \"a.service\"\n[host.mac.services]\nmanager = \"systemd\"\nserver = \"b.service\"\n")
	if mustRun(t, r, "") || !strings.Contains(r.out.String(), "11. watcher: OPEN (hosts box and mac each record a [services] table") {
		t.Errorf("two server hosts:\n%s", r.out.String())
	}
	r = watcherRig(t)
	if err := os.MkdirAll(filepath.Join(r.home, "Library"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.home, "Library", "LaunchAgents"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if mustRun(t, r, "") || !strings.Contains(r.out.String(), "10. watcher: OPEN (write the agent: ") {
		t.Errorf("an agent that cannot be written:\n%s", r.out.String())
	}
}

func TestPlistEscapesPaths(t *testing.T) {
	r := &runner{d: Deps{Home: "/Users/a&b"}}
	if p := r.plist(1); !strings.Contains(p, "<string>/Users/a&amp;b/.local/bin/engine</string>") {
		t.Errorf("plist:\n%s", p)
	}
}
