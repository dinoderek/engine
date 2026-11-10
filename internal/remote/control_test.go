package remote

import (
	"io"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/hostops"
)

func TestControlDispatchCarriesCredentialsOnlyOnStdin(t *testing.T) {
	r := hostops.ControlRequest{URL: "http://server", Credential: "private-token", Attempt: "a1"}
	ssh := SSH{Name: "devbox", Address: "devbox", Exec: func(in io.Reader, out, errOut io.Writer, name string, args ...string) (int, error) {
		command := strings.Join(args, " ")
		if strings.Contains(command, r.Credential) || !strings.Contains(command, "control-spawn") {
			t.Fatal(command)
		}
		b, err := io.ReadAll(in)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), r.Credential) {
			t.Fatal("missing stdin credential")
		}
		_, err = io.WriteString(out, `{"protocol":6,"engine":"test","host":"devbox","name":"demo-1","window":"@1"}`)
		return 0, err
	}}
	got, err := ControlSpawn(ssh, r)
	if err != nil || got.Window != "@1" {
		t.Fatal(got, err)
	}
}

func TestControlMergeRunsTheHostsMergeOp(t *testing.T) {
	head := strings.Repeat("a", 40)
	var command string
	ssh := SSH{Name: "devbox", Address: "devbox", Exec: func(_ io.Reader, out, _ io.Writer, _ string, args ...string) (int, error) {
		command = strings.Join(args, " ")
		_, err := io.WriteString(out, `{"protocol":6,"engine":"test","host":"devbox","outcome":"merged","reason":"merged at `+head+`"}`)
		return 0, err
	}}
	got, err := ControlMerge(ssh, hostops.MergeRequest{Repo: "o/r", PR: 42, Head: head, Method: "squash"})
	if err != nil || got.Outcome != hostops.MergeDone {
		t.Fatal(got, err)
	}
	if want := ShellJoin([]string{"engine", "host", "merge", "--json", "--repo", "o/r", "--pr", "42", "--head", head, "--method", "squash"}); !strings.HasSuffix(command, want) {
		t.Errorf("command %q, want suffix %q", command, want)
	}
	local, err := ControlMerge(Local{Deps: hostops.Deps{Run: func(string, []string, string, ...string) (string, error) {
		return `{"state":"MERGED","headRefOid":"` + head + `"}`, nil
	}}}, hostops.MergeRequest{Repo: "o/r", PR: 42, Head: head, Method: "squash"})
	if err != nil || local.Outcome != hostops.MergeDone {
		t.Fatal(local, err)
	}
	if _, err := ControlMerge(nil, hostops.MergeRequest{}); err == nil {
		t.Fatal("a client without merges merged")
	}
}
