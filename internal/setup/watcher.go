package setup

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/hosts"
)

// WatchLabel is the launchd label of the watcher's agent; its plist is
// ~/Library/LaunchAgents/<label>.plist.
const WatchLabel = "com.github.dinoderek.engine.watch"

// watchLog is where the agent writes the watcher's errors, under the home.
const watchLog = ".local/share/engine/watch.log"

// watched is the server's host this host's watcher calls, when there is one
// to watch: this host is a Mac, and another host records the server's
// services. Several that record them is an error.
func (r *runner) watched() (hosts.Host, bool, error) {
	if r.t.remote() || r.goos() != "darwin" || r.cfg == nil {
		return hosts.Host{}, false, nil
	}
	server, err := r.cfg.ServerHost()
	if errors.Is(err, hosts.ErrNoServerHost) {
		return hosts.Host{}, false, nil
	}
	if err != nil || server.Name == r.host.Name {
		return hosts.Host{}, false, err
	}
	return server, true, nil
}

// hostOS is the system this engine runs on, which a local setup checks.
var hostOS = runtime.GOOS

func (r *runner) goos() string {
	if r.d.OS != "" {
		return r.d.OS
	}
	return hostOS
}

// watcher installs `engine watch` on this Mac as a launchd agent, with an
// owner-only copy of the bot token and the chat from the server's host, and
// loads it. It never prints the token.
func (r *runner) watcher() (harness.Outcome, error) {
	server, ok, err := r.watched()
	switch {
	case err != nil:
		return harness.Open(err.Error(), "edit "+filepath.Join(hosts.ConfigDir(r.d.Home), "hosts.toml")+", then "+r.rerun()), nil
	case !ok && r.goos() != "darwin":
		return harness.Passed("not macOS"), nil
	case !ok:
		return harness.Passed("no other host records the server's services"), nil
	}
	chat, token, why, err := r.serverTelegram(server)
	switch {
	case err != nil:
		return harness.Open("cannot reach "+server.Name+" to read its Telegram chat: "+err.Error(), r.rerun()), nil
	case why != "":
		return harness.Passed(why), nil
	}
	tokenPath := filepath.Join(hosts.ConfigDir(r.d.Home), hosts.WatchTokenFile)
	plistPath := filepath.Join(r.d.Home, "Library", "LaunchAgents", WatchLabel+".plist")
	wroteToken, err := writeOwned(tokenPath, token+"\n", 0o600)
	if err != nil {
		return harness.Open("write the bot token copy: "+err.Error(), r.rerun()), nil
	}
	wrotePlist, err := writeOwned(plistPath, r.plist(chat), 0o644)
	if err != nil {
		return harness.Open("write the agent: "+err.Error(), r.rerun()), nil
	}
	return r.loadAgent(server.Name, plistPath, wroteToken || wrotePlist)
}

// serverTelegram reads the chat and bot token from the server's host. A
// host that has not paired Telegram has nothing to copy, which why says; a
// host that does not answer is the error.
func (r *runner) serverTelegram(server hosts.Host) (chat int64, token, why string, err error) {
	sh := NewShell(server.Name, server.Address, r.d.Exec)
	config, err := sh.Sh(`cat "$HOME/.config/engine/config.toml"`, "")
	if err != nil {
		return 0, "", "", err
	}
	tg, perr := hosts.ParseTelegram(config.Out)
	if config.Code != 0 || perr != nil || tg.Chat == 0 {
		return 0, "", "Telegram is not paired on " + server.Name + "; pair it there (telegram.md), then rerun setup", nil
	}
	res, err := sh.Sh(`cat "$HOME/.config/engine/`+hosts.TelegramTokenFile+`"`, "")
	switch {
	case err != nil:
		return 0, "", "", err
	case res.Code != 0:
		return 0, "", "no bot token on " + server.Name + "; pair Telegram there (telegram.md), then rerun setup", nil
	}
	token = strings.TrimSpace(res.Out)
	if len(token) < 32 || strings.ContainsAny(token, " \t\r\n") {
		return 0, "", "the bot token on " + server.Name + " is not a valid credential; pair Telegram there again", nil
	}
	return tg.Chat, token, "", nil
}

// loadAgent loads the agent into this user's launchd domain, first unloading
// one whose files changed.
func (r *runner) loadAgent(server, plistPath string, changed bool) (harness.Outcome, error) {
	domain := `gui/$(id -u)`
	loaded, err := r.t.Sh("launchctl print "+domain+"/"+WatchLabel, "")
	if err != nil {
		return harness.Outcome{}, err
	}
	if loaded.Code == 0 && !changed {
		return harness.Passed("watching " + server), nil
	}
	if loaded.Code == 0 {
		if _, err = r.t.Sh("launchctl bootout "+domain+"/"+WatchLabel, ""); err != nil {
			return harness.Outcome{}, err
		}
	}
	bootstrap := "launchctl bootstrap " + domain + " " + hosts.ShellWord(plistPath)
	res, err := r.t.Sh(bootstrap, "")
	if err != nil {
		return harness.Outcome{}, err
	}
	if res.Code != 0 {
		return harness.Open("launchctl could not load the watcher: "+res.Why(), bootstrap), nil
	}
	return harness.Changed("watching " + server + "; agent " + WatchLabel + " loaded"), nil
}

// plist is the agent: engine watch for chat, started at login and kept
// running, at most one start a minute. It names the build that set it up,
// so a new engine changes it and setup reloads the agent.
func (r *runner) plist(chat int64) string {
	args := []string{filepath.Join(r.d.Home, ".local", "bin", "engine"), "watch", "--chat", strconv.FormatInt(chat, 10)}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + WatchLabel + `</string>
	<key>ProgramArguments</key>
	<array>
`)
	for _, a := range args {
		fmt.Fprintf(&b, "\t\t<string>%s</string>\n", escape(a))
	}
	b.WriteString(`	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ThrottleInterval</key>
	<integer>60</integer>
	<key>EnvironmentVariables</key>
	<dict>
		<key>ENGINE_WATCH_BUILD</key>
		<string>` + escape(r.d.Info.String()) + `</string>
	</dict>
	<key>StandardErrorPath</key>
	<string>` + escape(filepath.Join(r.d.Home, watchLog)) + `</string>
</dict>
</plist>
`)
	return b.String()
}

func escape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// writeOwned makes path hold content with mode, writing through a rename
// only when it differs, and reports whether it changed anything.
func writeOwned(path, content string, mode os.FileMode) (bool, error) {
	have, err := os.ReadFile(path)
	if err == nil && string(have) == content {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() == mode {
			return false, err
		}
		return true, os.Chmod(path, mode)
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-")
	if err != nil {
		return false, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err = tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if err = tmp.Close(); err != nil {
		return false, err
	}
	if err = os.Chmod(tmp.Name(), mode); err != nil {
		return false, err
	}
	return true, os.Rename(tmp.Name(), path)
}
