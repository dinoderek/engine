package setup

import (
	"regexp"
	"strings"
)

// ghTokenToFile moves gh's token from the macOS login keychain to gh's own
// config file, in plaintext, where a tmux server started over ssh can read
// it. It must run where gh can read the keychain: a Terminal on the host.
// It is the remedy `engine start --check` prints too (hostops.GHRemedy).
const ghTokenToFile = "gh auth token | gh auth login -h github.com --with-token --insecure-storage"

// ghLogin matches an account line of `gh auth status`, in its current form
// ("account octo") and its older one ("as octo"), with where the token is
// kept: keyring, a config file's path, or an environment variable.
var ghLogin = regexp.MustCompile(`(?:Logged in|Failed to log in) to github\.com (?:account|as) (\S+) \(([^)]*)\)`)

// ghAccount is the account line gh uses for github.com: the active account's
// when gh marks one, else the first it lists; nil when the status names none.
// Its submatches are the login and where the token is kept.
func ghAccount(status string) []string {
	var first, current []string
	for _, line := range strings.Split(status, "\n") {
		if m := ghLogin.FindStringSubmatch(line); m != nil {
			current = m
			if first == nil {
				first = m
			}
			continue
		}
		// An "Active account" line belongs to the account above it.
		if current != nil && strings.Contains(line, "Active account: true") {
			return current
		}
	}
	return first
}

// ghWhy is what gh said about a failure: its account line, or else its first
// line that is not the host's name.
func ghWhy(status string, res result) string {
	if m := ghAccount(status); m != nil {
		return strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(m[0]), "X✓ "))
	}
	for _, line := range strings.Split(status, "\n") {
		if line = strings.TrimSpace(line); line != "" && line != "github.com" {
			return line
		}
	}
	return res.why()
}

// ghAuth checks that gh on the target can reach github.com in the context
// setup reaches it in: over ssh for another host, the context a tmux server
// started by a dispatch inherits. On macOS a token in the login keychain is
// open even when it works now: the next tmux server started over ssh will
// not read it.
func (r *runner) ghAuth() (outcome, error) {
	if r.missing["gh"] {
		return r.blocked("gh"), nil
	}
	uname, err := r.t.sh("uname -s", "")
	if err != nil {
		return outcome{}, err
	}
	darwin := len(uname.lines()) > 0 && uname.lines()[0] == "Darwin"
	res, err := r.t.sh("gh auth status -h github.com", "")
	if err != nil {
		return outcome{}, err
	}
	status := res.out + res.errOut
	account := ghAccount(status)
	switch {
	case res.code != 0 && darwin:
		return open("gh cannot sign in to github.com ("+ghWhy(status, res)+"); ssh cannot read a token in the macOS login keychain, "+
			"so in a Terminal on the host (after gh auth login -h github.com, if gh is signed out there too) run", ghTokenToFile), nil
	case res.code != 0:
		return open("gh cannot sign in to github.com ("+ghWhy(status, res)+")", r.t.onHost("gh auth login -h github.com", true)), nil
	case account == nil:
		return passed("signed in"), nil
	case darwin && account[2] == "keyring":
		return open("gh keeps "+account[1]+"'s token in the login keychain, which a tmux server started over ssh cannot read; "+
			"in a Terminal on the host, run", ghTokenToFile), nil
	}
	return passed(account[1] + " (" + account[2] + ")"), nil
}
