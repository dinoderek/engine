package setup

import (
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/hostops"
)

const (
	ghKeyring = "github.com\n  ✓ Logged in to github.com account octo (keyring)\n  - Active account: true\n  - Token: gho_****\n"
	ghOldForm = "github.com\n  ✓ Logged in to github.com as octo (keyring)\n  ✓ Git operations for github.com configured to use https protocol.\n"
	ghEnv     = "github.com\n  ✓ Logged in to github.com account bot (GH_TOKEN)\n  - Active account: true\n"
	// ghTwo has an inactive keyring account above the active file one.
	ghTwo = "github.com\n  ✓ Logged in to github.com account old (keyring)\n  - Active account: false\n" +
		"  ✓ Logged in to github.com account octo (/Users/o/.config/gh/hosts.yml)\n  - Active account: true\n"
	// gh401 is gh over ssh on macOS with its token in the keychain.
	gh401 = "github.com\n  X Failed to log in to github.com account octo (keyring)\n  - Active account: true\n" +
		"  - The token in keyring is invalid.\n  - To re-authenticate, run: gh auth login -h github.com\n"
	ghSignedOut = "You are not logged into any GitHub hosts. To log in, run: gh auth login\n"
)

func TestGhAccount(t *testing.T) {
	cases := []struct {
		name, status, login, storage string
	}{
		{"keyring", ghKeyring, "octo", "keyring"},
		{"older gh", ghOldForm, "octo", "keyring"},
		{"environment token", ghEnv, "bot", "GH_TOKEN"},
		{"file token", ghFileToken, "octo", "/home/me/.config/gh/hosts.yml"},
		{"the active of two", ghTwo, "octo", "/Users/o/.config/gh/hosts.yml"},
		{"a failed login", gh401, "octo", "keyring"},
		{"signed out", ghSignedOut, "", ""},
		{"empty", "", "", ""},
	}
	for _, c := range cases {
		m := ghAccount(c.status)
		login, storage := "", ""
		if m != nil {
			login, storage = m[1], m[2]
		}
		if login != c.login || storage != c.storage {
			t.Errorf("%s: %q %q, want %q %q", c.name, login, storage, c.login, c.storage)
		}
	}
}

func TestGhAuthStep(t *testing.T) {
	ssh := "ssh -t dev@box 'gh auth login -h github.com'"
	cases := []struct {
		name    string
		os      string
		gh      string
		code    int
		want    string // the step's line
		command string // the open item's command; "" is a closed step
	}{
		{"a file token on macOS", "Darwin", "", 0, "7. gh auth: ok (octo (/home/me/.config/gh/hosts.yml))", ""},
		{"an environment token on macOS", "Darwin", ghEnv, 0, "7. gh auth: ok (bot (GH_TOKEN))", ""},
		{"a keyring token on Linux", "Linux", ghKeyring, 0, "7. gh auth: ok (octo (keyring))", ""},
		{"gh names no account", "Linux", "ok\n", 0, "7. gh auth: ok (signed in)", ""},
		{"a keyring token on macOS that works today", "Darwin", ghKeyring, 0,
			"7. gh auth: OPEN (gh keeps octo's token in the login keychain, which a tmux server started over ssh cannot read", ghTokenToFile},
		{"the 401 over ssh on macOS", "Darwin", gh401, 1,
			"7. gh auth: OPEN (gh cannot sign in to github.com (Failed to log in to github.com account octo (keyring)); ssh cannot read", ghTokenToFile},
		{"signed out on Linux", "Linux", ghSignedOut, 1, "7. gh auth: OPEN (gh cannot sign in to github.com (You are not logged into any GitHub hosts", ssh},
		{"a silent failure on Linux", "Linux", "\n", 4, "7. gh auth: OPEN (gh cannot sign in to github.com (exit status 4))", ssh},
	}
	for _, c := range cases {
		r := newRig(t)
		r.box = settledBox(r.home)
		r.box.os, r.box.gh, r.box.ghCode = c.os, c.gh, c.code
		if c.os == "Darwin" {
			r.box.masked = 0 // not Linux: no suspend passes without masked targets
		}
		closed := mustRun(t, r, "box")
		out := r.out.String()
		if closed != (c.command == "") || !strings.Contains(out, c.want) {
			t.Errorf("%s: closed %v, want %q in:\n%s", c.name, closed, c.want, out)
		}
		if c.command != "" && (!strings.Contains(out, "  7. gh auth: "+c.command+"\n") || len(r.checked) != 0) {
			t.Errorf("%s: want the command %q and no check:\n%s", c.name, c.command, out)
		}
	}
}

func TestGhAuthNeedsGh(t *testing.T) {
	r := newRig(t)
	r.box = settledBox(r.home)
	r.box.missing = []string{"gh"}
	mustRun(t, r, "box")
	if !strings.Contains(r.out.String(), "7. gh auth: OPEN (gh is missing (step 1))") {
		t.Errorf("gh auth ran without gh:\n%s", r.out.String())
	}
	for _, c := range r.box.commands {
		if strings.HasPrefix(c, "gh ") {
			t.Errorf("ran %q", c)
		}
	}
}

func TestGhAuthStepMatchesTheCheck(t *testing.T) {
	if ghTokenToFile != hostops.GHRemedy {
		t.Errorf("setup gives %q, the check %q", ghTokenToFile, hostops.GHRemedy)
	}
}
