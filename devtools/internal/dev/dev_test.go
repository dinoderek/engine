package dev

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinoderek/engine/devtools/internal/lanes"
)

// fakeTool returns a Tool rooted in a temp dir holding go.mod, whose commands
// go to run and whose output is captured.
func fakeTool(t *testing.T, run func(name string, args []string) (string, error)) (Tool, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module m\n\ngo 1.27.0\n\ntoolchain go1.27.2\n")
	var stdout, stderr bytes.Buffer
	tool := Tool{
		Root: root, Self: "go", GoVersion: "go1.27.2",
		Run: func(_, name string, args ...string) (string, error) {
			if run == nil {
				return "", errors.New("unexpected " + name)
			}
			return run(name, args)
		},
		Attach:   func(string, string, ...string) int { return 0 },
		LookPath: func(name string) (string, error) { return "/bin/" + name, nil },
		Fetch:    func(string) ([]byte, error) { return nil, errors.New("offline") },
		Stdout:   &stdout, Stderr: &stderr,
	}
	return tool, &stdout, &stderr
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func want(t *testing.T, code, wantCode int, out *bytes.Buffer, contains ...string) {
	t.Helper()
	if code != wantCode {
		t.Errorf("exit code %d, want %d\n%s", code, wantCode, out)
	}
	for _, c := range contains {
		if !strings.Contains(out.String(), c) {
			t.Errorf("output lacks %q:\n%s", c, out)
		}
	}
}

func TestMainDispatch(t *testing.T) {
	tool, stdout, stderr := fakeTool(t, nil)
	want(t, tool.Main(nil), 2, stderr, "usage: ./dev")
	want(t, tool.Main([]string{"deploy"}), 2, stderr, `unknown verb "deploy"`)
	want(t, tool.Main([]string{"help"}), 0, stdout, "no deploy verb")
	want(t, tool.Main([]string{"check"}), 2, stderr, "usage: ./dev check")
	stdout.Reset()
	want(t, tool.Main([]string{"check", "nope"}), 2, stdout, `unknown check "nope"`)

	var attached string
	tool.Attach = func(_, name string, args ...string) int {
		attached = name + " " + strings.Join(args, " ")
		return 3
	}
	if code := tool.Main([]string{"run", "version"}); code != 3 || attached != "go run ./cmd/engine version" {
		t.Errorf("run: code %d, attached %q", code, attached)
	}
}

func TestMainFromTheModule(t *testing.T) {
	if code := Main([]string{"help"}); code != 0 {
		t.Errorf("Main(help) = %d", code)
	}
}

func TestTest(t *testing.T) {
	tool, stdout, stderr := fakeTool(t, nil)
	want(t, tool.Main([]string{"test"}), 1, stderr, "lanes.tsv")

	writeFile(t, filepath.Join(tool.Root, lanes.Registry),
		"env\trequired\t*.go\tgo env GOOS\nslow\textra\t*\tdev env GOARCH\n")
	stderr.Reset()
	want(t, tool.Main([]string{"test", "--list"}), 0, stdout, "slow         extra    dev env GOARCH")
	want(t, tool.Main([]string{"test", "for"}), 2, stderr, "usage: ./dev test for")
	stdout.Reset()
	want(t, tool.Main([]string{"test", "for", "README.md"}), 0, stdout, "no lane is triggered")
	stdout.Reset()
	want(t, tool.Main([]string{"test", "for", "a.go"}), 0, stdout, "PASS env", "Overall: PASS")
	stdout.Reset()
	want(t, tool.Main([]string{"test", "slow"}), 0, stdout, "PASS slow")
	want(t, tool.Main([]string{"test", "nope"}), 2, stderr, `unknown lane "nope"`)
}

func TestDoctor(t *testing.T) {
	installed := []string{}
	tool, stdout, _ := fakeTool(t, func(name string, args []string) (string, error) {
		switch strings.Join(append([]string{name}, args...), " ") {
		case "go env CGO_ENABLED CC":
			return "1\nclang\n", nil
		case "brew install gh":
			installed = append(installed, "gh")
			return "", errors.New("brew failed")
		}
		return "", nil
	})
	tool.LookPath = func(name string) (string, error) {
		if name == "gh" {
			return "", errors.New("not found")
		}
		return "/bin/" + name, nil
	}
	want(t, tool.Doctor(), 1, stdout, "ok   git", "FAIL gh", "installing with brew install gh", "ok   C toolchain", "1 gap(s)")
	if len(installed) != 1 {
		t.Errorf("brew install attempts = %v", installed)
	}

	tool.LookPath = func(name string) (string, error) { return "/bin/" + name, nil }
	stdout.Reset()
	want(t, tool.Doctor(), 0, stdout, "doctor: ready")

	tool.GoVersion = "go1.26.0"
	stdout.Reset()
	want(t, tool.Doctor(), 1, stdout, "running go1.26.0, go.mod pins go1.27.2")
}

func TestDoctorGaps(t *testing.T) {
	tool, stdout, _ := fakeTool(t, func(name string, args []string) (string, error) {
		if args[0] == "env" {
			return "0\ncc\n", nil
		}
		return "no network", errors.New("exit 1")
	})
	writeFile(t, filepath.Join(tool.Root, "go.mod"), "module m\n")
	want(t, tool.Doctor(), 1, stdout, "go.mod has no toolchain line", "cgo is disabled", "no network", "5 gap(s)")

	tool.Run = func(string, string, ...string) (string, error) { return "1 nocc", nil }
	tool.LookPath = func(name string) (string, error) {
		if name == "nocc" {
			return "", errors.New("missing")
		}
		return name, nil
	}
	stdout.Reset()
	want(t, tool.Doctor(), 1, stdout, `no C compiler "nocc"`)
}

func TestDeps(t *testing.T) {
	gomod := `{"Require":[{"Path":"github.com/golangci/golangci-lint/v2"},{"Path":"example.com/lib"},{"Path":"example.com/other"}],` +
		`"Tool":[{"Path":"github.com/golangci/golangci-lint/v2/cmd/golangci-lint"}]}`
	modules := `{"Path":"m","Main":true}
{"Path":"github.com/golangci/golangci-lint/v2","Version":"v2.14.0","Indirect":true,"Update":{"Version":"v2.15.0"}}
{"Path":"example.com/lib","Version":"v1.0.0","Update":{"Version":"v1.1.0"}}
{"Path":"example.com/deep","Version":"v0.1.0","Indirect":true,"Update":{"Version":"v0.2.0"}}
{"Path":"example.com/other","Version":"v1.0.0"}`
	vuln := error(nil)
	tool, stdout, stderr := fakeTool(t, func(name string, args []string) (string, error) {
		switch strings.Join(args, " ") {
		case "mod edit -json":
			return gomod, nil
		case "list -m -u -json all":
			return modules, nil
		default:
			return "govulncheck output\n", vuln
		}
	})
	tool.Fetch = func(string) ([]byte, error) {
		return []byte(`[{"version":"go1.28rc1","stable":false},{"version":"go1.27.3","stable":true}]`), nil
	}
	want(t, tool.Main([]string{"deps"}), 0, stdout,
		"outdated tool   github.com/golangci/golangci-lint/v2 v2.14.0 -> v2.15.0",
		"outdated module example.com/lib v1.0.0 -> v1.1.0",
		"1 indirect module(s)",
		"outdated go     go1.27.2 -> go1.27.3",
		"govulncheck output")

	vuln = errors.New("exit 3")
	tool.Fetch = func(string) ([]byte, error) { return []byte(`[{"version":"go1.27.2","stable":true}]`), nil }
	stdout.Reset()
	want(t, tool.Deps(), 1, stdout, "go1.27.2 is the newest release", "govulncheck reports vulnerabilities")

	tool.Run = func(string, string, ...string) (string, error) { return "", errors.New("no go") }
	want(t, tool.Deps(), 1, stderr, "go mod edit -json")
}

func TestReportGoEdgeCases(t *testing.T) {
	tool, stdout, _ := fakeTool(t, nil)
	tool.reportGo()
	want(t, 0, 0, stdout, "could not list Go releases: offline")

	tool.Fetch = func(string) ([]byte, error) { return []byte(`[{"version":"go1.28rc1","stable":false}]`), nil }
	stdout.Reset()
	tool.reportGo()
	want(t, 0, 0, stdout, "no stable Go release listed")

	writeFile(t, filepath.Join(tool.Root, "go.mod"), "module m\n")
	stdout.Reset()
	tool.reportGo()
	want(t, 0, 0, stdout, "go.mod has no toolchain line")
}

func TestAttach(t *testing.T) {
	if code := attach(t.TempDir(), "go", "env", "GOOS"); code != 0 {
		t.Errorf("attach(go env) = %d", code)
	}
	if code := attach(t.TempDir(), "go", "no-such-subcommand"); code == 0 {
		t.Error("attach(bad subcommand) = 0")
	}
	if code := attach(t.TempDir(), "no-such-binary-anywhere"); code != 1 {
		t.Errorf("attach(missing binary) = %d", code)
	}
}

func TestFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("body"))
	}))
	defer srv.Close()
	if body, err := fetch(srv.URL); err != nil || string(body) != "body" {
		t.Errorf("fetch = %q, %v", body, err)
	}
	if _, err := fetch(srv.URL + "/missing"); err == nil {
		t.Error("fetch(404): want an error")
	}
	if _, err := fetch("http://127.0.0.1:1"); err == nil {
		t.Error("fetch(closed port): want an error")
	}
}
