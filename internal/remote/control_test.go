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
		_, err = io.WriteString(out, `{"protocol":4,"engine":"test","host":"devbox","name":"demo-1","window":"@1"}`)
		return 0, err
	}}
	got, err := ControlSpawn(ssh, r)
	if err != nil || got.Window != "@1" {
		t.Fatal(got, err)
	}
}
