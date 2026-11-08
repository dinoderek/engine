package tracker

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// gh's own output for the refusals it reports, as gh api prints them.
const (
	ghBadCredentials = `{"message":"Bad credentials","documentation_url":"https://docs.github.com/rest","status":"401"}` + "\ngh: Bad credentials (HTTP 401)"
	ghSuspended      = "gh: Sorry. Your account was suspended. (HTTP 403)"
	ghSAML           = "gh: Resource protected by organization SAML enforcement. You must grant your Personal Access token access to this organization. (HTTP 403)"
	ghRateLimit      = "gh: API rate limit exceeded for user ID 1234. (HTTP 403)"
	ghSecondary      = "gh: You have exceeded a secondary rate limit. Please wait a few minutes before you try again. (HTTP 403)"
	ghNetwork        = "error connecting to api.github.com\ncheck your internet connection or https://githubstatus.com"
)

func TestIsAuthFailure(t *testing.T) {
	for _, tc := range []struct {
		name, out string
		auth      bool
	}{
		{"401 bad credentials", ghBadCredentials, true},
		{"401 alone", "gh: Requires authentication (HTTP 401)", true},
		{"invalid token", "The token in GH_TOKEN is invalid.", true},
		{"not signed in", "To get started with GitHub CLI, please run:  gh auth login", true},
		{"suspended 403", ghSuspended, true},
		{"SSO 403", ghSAML, true},
		{"SSO by name 403", "gh: Single sign-on is required for this organization (HTTP 403)", true},
		{"rate limit 403", ghRateLimit, false},
		{"secondary rate limit 403", ghSecondary, false},
		{"rate limit header", "HTTP/2.0 403 Forbidden\nX-Ratelimit-Remaining: 0\ngh: Forbidden (HTTP 403)", false},
		{"permission 403", "gh: Resource not accessible by integration (HTTP 403)", false},
		{"not found", "gh: Not Found (HTTP 404)", false},
		{"network", ghNetwork, false},
		{"server error", "gh: Server Error (HTTP 502)", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsAuthFailure(tc.out); got != tc.auth {
				t.Errorf("IsAuthFailure(%q) = %v", tc.out, got)
			}
		})
	}
}

// failingGh fails every gh api call with out, and answers gh auth status
// with status.
func failingGh(out, status string) Runner {
	return func(_ []string, _ string, args ...string) (string, error) {
		if strings.Join(args, " ") == "auth status --active" {
			return status, errors.New("exit status 1")
		}
		return out, errors.New("exit status 1")
	}
}

func TestAPIReturnsAnAuthErrorNamingTheAccount(t *testing.T) {
	for _, tc := range []struct {
		name, status, account string
	}{
		{"current gh", "github.com\n  X Failed to log in to github.com account some-dev (keyring)\n  - The token in keyring is invalid.", "some-dev"},
		{"older gh", "github.com\n  ✓ Logged in to github.com as old-dev (oauth_token)", "old-dev"},
		{"no account", "You are not logged into any GitHub hosts. To log in, run: gh auth login", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := api(failingGh(ghBadCredentials, tc.status), "user")
			ae, ok := AsAuth(fmt.Errorf("wrapped: %w", err))
			if !ok || ae.Account != tc.account || ae.Detail != "gh: Bad credentials (HTTP 401)" {
				t.Fatalf("got %#v from %v", ae, err)
			}
			msg := ae.Error()
			for _, want := range []string{"gh: Bad credentials (HTTP 401)", "gh auth status", "gh auth login"} {
				if !strings.Contains(msg, want) {
					t.Errorf("message %q lacks %q", msg, want)
				}
			}
			if tc.account != "" && !strings.Contains(msg, "GitHub account "+tc.account) {
				t.Errorf("message %q does not name %s", msg, tc.account)
			}
		})
	}
}

func TestAPIKeepsOtherFailuresPlain(t *testing.T) {
	for _, out := range []string{ghRateLimit, ghSecondary, ghNetwork} {
		_, err := api(failingGh(out, ""), "user")
		if _, ok := AsAuth(err); ok || err == nil || !strings.Contains(err.Error(), strings.Split(out, "\n")[0]) {
			t.Errorf("%q: %v", out, err)
		}
	}
}

func TestSyncLabelsReturnsAnAuthError(t *testing.T) {
	err := SyncLabels(failingGh(ghSuspended, ""), "o/r", &strings.Builder{})
	if ae, ok := AsAuth(err); !ok || ae.Detail != ghSuspended {
		t.Fatalf("got %v", err)
	}
}

func TestGhMessageFallsBackToTheLastLine(t *testing.T) {
	if got := ghMessage("first\n  To get started, run gh auth login  \n"); got != "To get started, run gh auth login" {
		t.Errorf("got %q", got)
	}
}
