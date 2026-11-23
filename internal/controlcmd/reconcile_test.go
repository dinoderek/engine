package controlcmd

import (
	"bytes"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
)

func TestAttemptReconcileTakesItsEvidenceEitherWay(t *testing.T) {
	e := Env{In: strings.NewReader(""), Getenv: func(string) string { return "" }}
	for _, args := range [][]string{
		{"attempt", "reconcile", "a4", "host", "rebooted"},
		{"attempt", "reconcile", "a4", "--body", "host rebooted"},
		{"attempt", "reconcile", "--body", "host rebooted", "a4"},
	} {
		r, err := Parse(args, e)
		if err != nil || r.Op != "attempt-reconcile" || r.ID != "a4" || r.Body != "host rebooted" {
			t.Errorf("%v: %+v, %v", args, r, err)
		}
	}
	if _, err := Parse([]string{"attempt", "reconcile", "a4", "rebooted", "--body", "crashed"}, e); err == nil || !strings.Contains(err.Error(), "give the evidence once") {
		t.Errorf("evidence twice: %v", err)
	}
	if !strings.Contains(Usage, "engine attempt reconcile <attempt-id> <evidence> | --body <evidence>\n") {
		t.Error("the usage does not show attempt reconcile")
	}
}

// A host refusal prints the 409's reason and exits non-zero.
func TestAttemptReconcilePrintsTheHostsRefusal(t *testing.T) {
	st, err := control.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := control.Service{Store: st, Reconcile: func(string) error {
		return &control.Problem{Status: 409, Reason: control.ReasonReconcileRefused, Message: "execution is still alive in window demo-3 on devbox"}
	}}
	token := control.Secret()
	server := httptest.NewServer(control.Handler(svc, token, t.Logf))
	t.Cleanup(server.Close)
	var out, errs bytes.Buffer
	e := Env{In: strings.NewReader(""), Out: &out, Err: &errs, Home: t.TempDir(), Getenv: func(string) string { return "" },
		Client: &control.Client{URL: server.URL, Token: token}}
	if code := Main([]string{"attempt", "reconcile", "a4", "host", "rebooted"}, e); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if want := "engine: reconcile-refused: execution is still alive in window demo-3 on devbox\n"; errs.String() != want {
		t.Errorf("stderr %q, want %q", errs.String(), want)
	}
}
