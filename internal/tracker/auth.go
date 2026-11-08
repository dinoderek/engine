package tracker

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// AuthError is a gh call GitHub refused because of the host's sign-in: a
// rejected or missing token (HTTP 401), or a 403 that denies the account
// itself, such as a suspension or SSO enforcement. Waiting does not cure
// it, so a loop stops on it. A rate limit is not one: it passes.
type AuthError struct {
	// Account is the login gh auth status names, or empty when it names none.
	Account string
	// Detail is gh's own message.
	Detail string
}

func (e *AuthError) Error() string {
	who := "the host's gh sign-in"
	if e.Account != "" {
		who = "the gh sign-in of GitHub account " + e.Account
	}
	return fmt.Sprintf("GitHub refuses %s: %s. Check it with `gh auth status`, and sign in again with `gh auth login`", who, e.Detail)
}

// AsAuth returns the AuthError in err's chain, if there is one.
func AsAuth(err error) (*AuthError, bool) {
	var ae *AuthError
	ok := errors.As(err, &ae)
	return ae, ok
}

var (
	rateLimited = regexp.MustCompile(`(?i)rate limit|x-ratelimit-remaining: 0`)
	unsignedIn  = regexp.MustCompile(`(?i)http 401|bad credentials|requires authentication|the token in \S+ is invalid|gh auth login`)
	forbidden   = regexp.MustCompile(`(?i)http 403`)
	deniedUser  = regexp.MustCompile(`(?i)suspended|saml|\bsso\b|single sign-on`)
	authAccount = regexp.MustCompile(`github\.com (?:account|as) ([A-Za-z0-9][A-Za-z0-9-]*)`)
)

// ghLines are the lines of a failed gh call's output that gh wrote itself.
// Response bodies, which gh prints first, are JSON, and what an issue or
// comment says is never read as gh's verdict.
func ghLines(out string) []string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.ContainsAny(l[:1], `{}[]"`) {
			lines = append(lines, l)
		}
	}
	return lines
}

// IsAuthFailure says whether out, a failed gh call's output, is GitHub
// refusing the sign-in, rather than a rate limit or anything else. Each
// verdict comes from one of gh's own lines.
func IsAuthFailure(out string) bool {
	lines := ghLines(out)
	for _, l := range lines {
		if rateLimited.MatchString(l) {
			return false
		}
	}
	for _, l := range lines {
		if unsignedIn.MatchString(l) || forbidden.MatchString(l) && deniedUser.MatchString(l) {
			return true
		}
	}
	return false
}

// authError returns the AuthError for out, a failed gh call's output, or
// nil when it is not one. It asks gh auth status which account is signed
// in, and leaves Account empty when that names none.
func authError(run Runner, out string) *AuthError {
	if !IsAuthFailure(out) {
		return nil
	}
	status, _ := run(nil, "gh", "auth", "status", "--active")
	account := ""
	if m := authAccount.FindStringSubmatch(status); m != nil {
		account = m[1]
	}
	return &AuthError{Account: account, Detail: ghMessage(out)}
}

// ghMessage is gh's own line that says why it failed: the last one naming
// an HTTP status, else the first. IsAuthFailure has found at least one.
func ghMessage(out string) string {
	lines := ghLines(out)
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(strings.ToUpper(lines[i]), "HTTP 4") {
			return lines[i]
		}
	}
	return lines[0]
}
