package setup

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/remote"
)

// Shims the host's path leaves out are suggested, under ~/, with the newest
// nvm node only; the step never opens.
func TestSetupSuggestsAPathForShims(t *testing.T) {
	r := newRig(t)
	r.box.shims = []string{"/home/me/.local/share/mise/shims", "/home/me/.nvm/versions/node/v9.11.2/bin", "/home/me/.nvm/versions/node/v18.2.0/bin"}
	if !mustRun(t, r, "box") {
		t.Fatalf("open:\n%s", r.out.String())
	}
	want := `9. path: ok (suggested: tools installed through version manager shims reach sessions only through the host's path; ` +
		`add path = ["~/.local/share/mise/shims", "~/.nvm/versions/node/v18.2.0/bin"] to [host.box] in hosts.toml, then rerun engine setup --host box)`
	if !strings.Contains(r.out.String(), want) {
		t.Errorf("lacks %q:\n%s", want, r.out.String())
	}
}

func TestSetupSuggestsNothingWhenThePathHasTheShims(t *testing.T) {
	r := newRig(t)
	r.home = callerHomeWith(t, strings.Replace(hostsTOML, "address = \"dev@box\"", "address = \"dev@box\"\npath = [\"~/.asdf/shims\"]", 1))
	r.box.shims = []string{"/home/me/.asdf/shims"}
	if !mustRun(t, r, "box") || !strings.Contains(r.out.String(), "9. path: ok\n") {
		t.Errorf("output:\n%s", r.out.String())
	}
	r.box.shims = nil
	if !mustRun(t, r, "box") || !strings.Contains(r.out.String(), "9. path: ok\n") {
		t.Errorf("no shims:\n%s", r.out.String())
	}
}

// The readiness step probes every repo engine cloned there, afresh, and is
// open while any is unready.
func TestSetupProbesEveryClonedRepo(t *testing.T) {
	r := newRig(t)
	if !mustRun(t, r, "box") || !strings.Contains(r.out.String(), "10. readiness: ok (no repo is cloned here yet") || len(r.probed) != 0 {
		t.Fatalf("no clones:\n%s", r.out.String())
	}
	r.box.clones = []string{"o/r", "o/x"}
	r.ready = func(repo string) (bool, error) { return repo == "o/r", nil }
	if mustRun(t, r, "box") {
		t.Fatalf("closed with o/x unready:\n%s", r.out.String())
	}
	out := r.out.String()
	for _, want := range []string{"the readiness report for o/r", "the readiness report for o/x",
		"10. readiness: OPEN (unready for o/x; fix what the report above shows, or remove the clone in ~/.local/share/engine/control-repos of a repo engine no longer runs)", "10. readiness: engine setup --host box"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	if !slices.Equal(r.probed, []string{"o/r", "o/x"}) {
		t.Errorf("probed %v", r.probed)
	}
	r.ready = func(string) (bool, error) { return true, nil }
	if !mustRun(t, r, "box") || !strings.Contains(r.out.String(), "10. readiness: ok (ready for o/r, o/x)") {
		t.Errorf("all ready:\n%s", r.out.String())
	}
	r.ready = func(string) (bool, error) { return false, errors.New(`unknown op "ready"`) }
	if mustRun(t, r, "box") || !strings.Contains(r.out.String(), `10. readiness: OPEN (the probe of o/r could not run: unknown op "ready")`) {
		t.Errorf("a probe that cannot run:\n%s", r.out.String())
	}
	r.ready = func(string) (bool, error) { return false, &remote.UnreachableError{Host: "box", Detail: "gone"} }
	if _, err := r.run("box"); err == nil {
		t.Error("an unreachable host set up")
	}
}

func TestShimDirsKeepsTheNewestNode(t *testing.T) {
	got := shimDirs([]string{"/h/.nvm/versions/node/v20.1.0/bin", "/h/.asdf/shims", "/h/.nvm/versions/node/v20.10.0/bin", "/h/.nvm/versions/node/v8.0.0/bin"})
	if !slices.Equal(got, []string{"/h/.asdf/shims", "/h/.nvm/versions/node/v20.10.0/bin"}) {
		t.Errorf("got %v", got)
	}
	if tilde("/elsewhere/bin", "/h") != "/elsewhere/bin" {
		t.Error("tilde")
	}
}
