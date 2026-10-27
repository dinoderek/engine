package run

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/start"
)

func TestSystemResumesOnTheWindowsHost(t *testing.T) {
	for _, host := range []string{"mac", "devbox"} {
		t.Run(host, func(t *testing.T) {
			var calls []string
			runner := func(name string, args ...string) (string, error) {
				call := name + " " + strings.Join(args, " ")
				calls = append(calls, call)
				if strings.Contains(call, "list-windows") {
					return "@4 %1 0 r-2\n", nil
				}
				return "0", nil
			}
			cfg, err := hosts.Parse(fleetConfig)
			if err != nil {
				t.Fatal(err)
			}
			s := System{Config: cfg, Host: cfg.Hosts["mac"], Starts: start.Deps{
				Exec: runner, Tmux: &session.Tmux{Bin: "tmux", Run: runner, Sleep: func(time.Duration) {}}, Sleep: func(time.Duration) {},
			}}
			if err := s.Resume(Session{Host: host, Name: "r-2", WindowID: "@4"}); err != nil {
				t.Fatal(err)
			}
			checkResumeCalls(t, host, calls)
		})
	}
}

func checkResumeCalls(t *testing.T, host string, calls []string) {
	t.Helper()
	if len(calls) != 5 {
		t.Fatalf("calls %q", calls)
	}
	if host == "mac" {
		if calls[3] != "tmux send-keys -t =engine:@4 -l continue" || calls[4] != "tmux send-keys -t =engine:@4 Enter" {
			t.Fatalf("calls %q", calls)
		}
	} else {
		for _, c := range calls {
			if !strings.HasPrefix(c, "ssh -o BatchMode=yes -o ConnectTimeout=10 me@devbox ") {
				t.Errorf("remote call %q", c)
			}
		}
		if !strings.HasSuffix(calls[3], "'send-keys' '-t' '=engine:@4' '-l' 'continue'") || !strings.HasSuffix(calls[4], "'send-keys' '-t' '=engine:@4' 'Enter'") {
			t.Fatalf("calls %q", calls)
		}
	}
}

func TestSystemResumeRefusesChangedOrUnreadableWindows(t *testing.T) {
	for _, which := range []string{"unknown host", "unreachable", "dead", "name reused", "id reused", "typing failed"} {
		t.Run(which, func(t *testing.T) {
			cfg, err := hosts.Parse(fleetConfig)
			if err != nil {
				t.Fatal(err)
			}
			var typed bool
			runner := func(_ string, args ...string) (string, error) {
				call := strings.Join(args, " ")
				switch {
				case which == "unreachable":
					return "", errors.New("ssh down")
				case strings.Contains(call, "list-windows"):
					switch which {
					case "dead":
						return "@4 %1 1 r-2", nil
					case "name reused":
						return "@4 %1 0 other", nil
					case "id reused":
						return "@5 %1 0 r-2", nil
					}
					return "@4 %1 0 r-2", nil
				case strings.Contains(call, "send-keys"):
					typed = true
					return "", errors.New("typing failed")
				}
				return "0", nil
			}
			s := System{Config: cfg, Host: cfg.Hosts["mac"], Starts: start.Deps{Exec: runner}}
			host := "devbox"
			if which == "unknown host" {
				host = "gone"
			}
			if err := s.Resume(Session{Host: host, Name: "r-2", WindowID: "@4"}); err == nil {
				t.Fatal("no error")
			}
			if typed && which != "typing failed" {
				t.Fatal("typed into a changed window")
			}
		})
	}
}
