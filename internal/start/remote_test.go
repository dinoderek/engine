package start

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/remote"
)

// hostFake is another machine, as the check sees it: it records the check it
// was asked for.
type hostFake struct {
	remote.Client
	checked bool
	// checkedHarness is the harness the last check named.
	checkedHarness string
	checkOK        bool
	checkErr       error
}

func (f *hostFake) Check(harness string, out io.Writer) (bool, error) {
	f.checked, f.checkedHarness = true, harness
	_, _ = io.WriteString(out, "the report\n")
	return f.checkOK, f.checkErr
}

// remoteDeps are Deps whose named machine is f.
func remoteDeps(h home, f remote.Client) Deps {
	return Deps{Home: h.dir, Getenv: func(string) string { return "" }, Now: fixedNow, Client: f}
}

func TestCheckOnAHost(t *testing.T) {
	h := newHome(t, "mac")
	f := &hostFake{checkOK: true}
	d := remoteDeps(h, f)
	var out bytes.Buffer
	if ok, err := Check(d, "dev", "", &out); !ok || err != nil || !f.checked || f.checkedHarness != "" || out.String() != "the report\n" {
		t.Errorf("ok=%v err=%v checked=%v out=%q", ok, err, f.checked, out.String())
	}
	f = &hostFake{checkOK: false}
	if ok, err := Check(remoteDeps(h, f), "dev", "codex", &out); ok || err != nil || !f.checked || f.checkedHarness != "codex" {
		t.Errorf("a failed check of codex: ok=%v err=%v harness=%q", ok, err, f.checkedHarness)
	}
	f = &hostFake{checkErr: errors.New("unreachable")}
	if ok, err := Check(remoteDeps(h, f), "", "", &out); ok || err == nil || !f.checked {
		t.Errorf("no host: ok=%v err=%v checked=%v", ok, err, f.checked)
	}
}

func TestCheckRefusesWhatItCannotReach(t *testing.T) {
	h := newHome(t, "mac")
	d := remoteDeps(h, &hostFake{})
	_, err := Check(d, "nowhere", "", &bytes.Buffer{})
	var refused *RefusedError
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), `host "nowhere" is not in hosts.toml`) {
		t.Errorf("unknown host: %v", err)
	}
	d.Home = t.TempDir()
	if _, err := Check(d, "dev", "", &bytes.Buffer{}); !errors.As(err, &refused) || !strings.Contains(err.Error(), "read host config") {
		t.Errorf("no host config: %v", err)
	}
}
