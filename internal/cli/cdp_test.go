package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// browserCandidates are the Chromium-family browsers the browser smoke test
// drives, by command name and then by macOS application path. The CI
// workflow probes the same names, in the same order.
var browserCandidates = []string{
	"chromium", "chromium-browser", "google-chrome", "google-chrome-stable", "microsoft-edge",
	"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	"/Applications/Chromium.app/Contents/MacOS/Chromium",
	"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
	"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
}

// findBrowser returns the first Chromium-family browser on this machine.
func findBrowser() (string, bool) {
	for _, name := range browserCandidates {
		if filepath.IsAbs(name) {
			if info, err := os.Stat(name); err == nil && !info.IsDir() {
				return name, true
			}
			continue
		}
		if path, err := exec.LookPath(name); err == nil {
			return path, true
		}
	}
	return "", false
}

// browser is a headless Chromium driven over the DevTools protocol on a
// pipe (--remote-debugging-pipe): NUL-terminated JSON on file descriptors 3
// and 4, so no network port or WebSocket client is needed.
type browser struct {
	t       *testing.T
	cmd     *exec.Cmd
	send    io.WriteCloser
	session string
	stderr  *syncBuffer

	mu      sync.Mutex
	next    int
	pending map[int]chan cdpMessage
	closed  error
	done    chan struct{}
}

type cdpMessage struct {
	ID        int             `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    any             `json:"params,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// launchBrowser starts the browser with one blank page and attaches to it.
func launchBrowser(t *testing.T, path string) *browser {
	t.Helper()
	toBrowser, fromTest, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	fromBrowser, toTest, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	// The mock keychain and basic password store keep the browser out of
	// the user's keychain: it writes only under its temporary profile.
	args := []string{"--headless=new", "--remote-debugging-pipe", "--user-data-dir=" + t.TempDir(),
		"--no-first-run", "--no-default-browser-check", "--disable-gpu", "--disable-extensions",
		"--disable-background-networking", "--disable-sync", "--disable-component-update", "--mute-audio",
		"--use-mock-keychain", "--password-store=basic", "--window-size=1280,900"}
	if !sandboxAvailable() {
		args = append(args, "--no-sandbox")
	}
	b := &browser{t: t, send: fromTest, stderr: &syncBuffer{}, pending: map[int]chan cdpMessage{}, done: make(chan struct{})}
	b.cmd = exec.Command(path, append(args, "about:blank")...)
	b.cmd.Env = browserEnv(t.TempDir())
	b.cmd.ExtraFiles = []*os.File{toBrowser, toTest}
	b.cmd.Stdout, b.cmd.Stderr = b.stderr, b.stderr
	if err = b.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = toBrowser.Close()
	_ = toTest.Close()
	go b.read(fromBrowser)
	t.Cleanup(b.close)
	b.attach()
	return b
}

// userNamespaceRestriction is where Ubuntu 23.10 and later say whether
// AppArmor forbids the unprivileged user namespaces Chromium sandboxes with.
const userNamespaceRestriction = "/proc/sys/kernel/apparmor_restrict_unprivileged_userns"

// sandboxAvailable reports whether Chromium can sandbox itself here. On
// Linux it cannot as root, nor where AppArmor forbids unprivileged user
// namespaces to a browser it has no profile for, as on CI runners. The test
// browser loads only the test's own loopback page, so it then runs without.
func sandboxAvailable() bool {
	if runtime.GOOS != "linux" {
		return true
	}
	restricted, err := os.ReadFile(userNamespaceRestriction)
	return os.Getuid() != 0 && (err != nil || strings.TrimSpace(string(restricted)) != "1")
}

// browserEnv gives the browser its own home: Linux keeps its NSS database
// and caches under HOME and the XDG directories, and macOS resolves
// Application Support, where Crashpad keeps its files whatever the profile,
// from CFFIXED_USER_HOME.
func browserEnv(home string) []string {
	env := []string{"HOME=" + home, "CFFIXED_USER_HOME=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "XDG_CACHE_HOME=" + filepath.Join(home, ".cache")}
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !slices.Contains([]string{"HOME", "CFFIXED_USER_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME"}, name) {
			env = append(env, kv)
		}
	}
	return env
}

// read delivers each reply to its caller. Events are not used.
func (b *browser) read(r io.ReadCloser) {
	defer close(b.done)
	defer func() { _ = r.Close() }()
	in := bufio.NewReader(r)
	for {
		raw, err := in.ReadBytes(0)
		if err != nil {
			b.fail(fmt.Errorf("browser pipe closed: %w", err))
			return
		}
		var m cdpMessage
		if json.Unmarshal(bytes.TrimSuffix(raw, []byte{0}), &m) != nil || m.ID == 0 {
			continue
		}
		b.mu.Lock()
		reply := b.pending[m.ID]
		delete(b.pending, m.ID)
		b.mu.Unlock()
		if reply != nil {
			reply <- m
		}
	}
}

func (b *browser) fail(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = err
	for id, reply := range b.pending {
		close(reply)
		delete(b.pending, id)
	}
}

// call sends one command, to the attached page unless browserWide, and decodes its result into out.
func (b *browser) call(browserWide bool, method string, params, out any) error {
	b.mu.Lock()
	if b.closed != nil {
		b.mu.Unlock()
		return b.closed
	}
	b.next++
	m := cdpMessage{ID: b.next, Method: method, Params: params}
	if !browserWide {
		m.SessionID = b.session
	}
	reply := make(chan cdpMessage, 1)
	b.pending[m.ID] = reply
	b.mu.Unlock()
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if _, err = b.send.Write(append(raw, 0)); err != nil {
		return err
	}
	select {
	case got, ok := <-reply:
		if !ok {
			return fmt.Errorf("%s: browser exited: %s", method, b.stderr.String())
		}
		if got.Error != nil {
			return fmt.Errorf("%s: %s", method, got.Error.Message)
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(got.Result, out)
	case <-time.After(30 * time.Second):
		return fmt.Errorf("%s: no reply from the browser", method)
	}
}

// do is call on the page, failing the test on an error.
func (b *browser) do(method string, params map[string]any) {
	b.t.Helper()
	if err := b.call(false, method, params, nil); err != nil {
		b.t.Fatal(err)
	}
}

func (b *browser) attach() {
	b.t.Helper()
	var created struct {
		TargetID string `json:"targetId"`
	}
	if err := b.call(true, "Target.createTarget", map[string]any{"url": "about:blank"}, &created); err != nil {
		b.t.Fatalf("%v\n%s", err, b.stderr.String())
	}
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err := b.call(true, "Target.attachToTarget", map[string]any{"targetId": created.TargetID, "flatten": true}, &attached); err != nil {
		b.t.Fatal(err)
	}
	b.session = attached.SessionID
	b.do("Page.enable", nil)
	b.do("Runtime.enable", nil)
}

// eval runs a JavaScript expression in the page, awaiting a promise, and
// returns its JSON value.
func (b *browser) eval(expression string) (json.RawMessage, error) {
	var out struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	err := b.call(false, "Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true, "awaitPromise": true}, &out)
	if err != nil {
		return nil, err
	}
	if e := out.ExceptionDetails; e != nil {
		return nil, fmt.Errorf("%s: %s %s", expression, e.Text, e.Exception.Description)
	}
	return out.Result.Value, nil
}

// value evaluates expression into out, failing the test on an error.
func (b *browser) value(expression string, out any) {
	b.t.Helper()
	raw, err := b.eval(expression)
	if err == nil {
		err = json.Unmarshal(raw, out)
	}
	if err != nil {
		b.t.Fatalf("%s: %v (got %s)", expression, err, raw)
	}
}

// waitFor polls a JavaScript condition until it is true, against a deadline,
// because the page loads its data asynchronously.
func (b *browser) waitFor(what, condition string) {
	b.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		raw, err := b.eval("Boolean(" + condition + ")")
		if err == nil && string(raw) == "true" {
			return
		}
		select {
		case <-ctx.Done():
			var page string
			b.value("document.body ? document.body.innerText.slice(0, 2000) : ''", &page)
			b.t.Fatalf("waiting for %s: %s never held (last error %v)\npage text:\n%s", what, condition, err, page)
		case <-tick.C:
		}
	}
}

// navigate loads url and waits for the page's scripts to run.
func (b *browser) navigate(url string) {
	b.t.Helper()
	b.do("Page.navigate", map[string]any{"url": url})
	b.waitFor("the page to load", "document.readyState === 'complete' && window.engine && window.engine.panel")
}

// keyCodes are the Windows virtual key codes Chromium needs for each key.
var keyCodes = map[string]int{"Tab": 9, "Enter": 13, "Escape": 27, "ArrowLeft": 37, "ArrowRight": 39, "Home": 36, "End": 35}

// press sends one key as the keyboard would.
func (b *browser) press(key string) {
	b.t.Helper()
	code, ok := keyCodes[key]
	if !ok {
		b.t.Fatalf("no key code for %s", key)
	}
	down := map[string]any{"type": "keyDown", "key": key, "code": key, "windowsVirtualKeyCode": code}
	if key == "Enter" {
		down["text"] = "\r"
	}
	b.do("Input.dispatchKeyEvent", down)
	b.do("Input.dispatchKeyEvent", map[string]any{"type": "keyUp", "key": key, "code": key, "windowsVirtualKeyCode": code})
}

// tabTo presses Tab until the focused element matches selector, proving it
// is reachable from the keyboard in document order.
func (b *browser) tabTo(selector string, limit int) {
	b.t.Helper()
	matched := fmt.Sprintf("document.activeElement && document.activeElement.matches(%q)", selector)
	for range limit {
		var ok bool
		b.value(matched, &ok)
		if ok {
			return
		}
		b.press("Tab")
	}
	var at string
	b.value("document.activeElement ? document.activeElement.outerHTML.slice(0, 200) : ''", &at)
	b.t.Fatalf("%d Tab presses did not reach %s; focus is on %s", limit, selector, at)
}

// viewport sets the page's size, as a phone (mobile) or a desktop window.
func (b *browser) viewport(width, height int, mobile bool) {
	b.t.Helper()
	b.do("Emulation.setDeviceMetricsOverride", map[string]any{"width": width, "height": height, "deviceScaleFactor": 1, "mobile": mobile})
}

// screenshot saves the page as a PNG in dir.
func (b *browser) screenshot(dir, name string) {
	b.t.Helper()
	var out struct {
		Data string `json:"data"`
	}
	if err := b.call(false, "Page.captureScreenshot", map[string]any{"format": "png"}, &out); err != nil {
		b.t.Fatal(err)
	}
	png, err := base64.StdEncoding.DecodeString(out.Data)
	if err != nil {
		b.t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, name+".png"), png, 0o600); err != nil {
		b.t.Fatal(err)
	}
}

// version is the browser's product string, for the test log.
func (b *browser) version() string {
	var v struct {
		Product   string `json:"product"`
		UserAgent string `json:"userAgent"`
	}
	if err := b.call(true, "Browser.getVersion", nil, &v); err != nil {
		return "unknown: " + err.Error()
	}
	return v.Product
}

func (b *browser) close() {
	_ = b.call(true, "Browser.close", nil, nil)
	select {
	case <-b.done:
	case <-time.After(10 * time.Second):
		_ = b.cmd.Process.Kill()
	}
	_ = b.send.Close()
	if err := b.cmd.Wait(); err != nil && !errors.As(err, new(*exec.ExitError)) {
		b.t.Errorf("browser: %v", err)
	}
}
