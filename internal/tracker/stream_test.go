package tracker

import (
	"bytes"
	"strings"
	"testing"
)

func TestStreamExecConnectsStreamsAndEnv(t *testing.T) {
	var out, errOut bytes.Buffer
	code, err := StreamExec([]string{"ENGINE_TEST_VALUE=from-env"}, Streams{strings.NewReader("from-stdin"), &out, &errOut},
		"sh", "-c", `cat; printf ' %s' "$ENGINE_TEST_VALUE"; printf oops >&2`)
	if err != nil || code != 0 {
		t.Fatalf("got code %d, err %v", code, err)
	}
	if out.String() != "from-stdin from-env" {
		t.Errorf("stdout %q", out.String())
	}
	if errOut.String() != "oops" {
		t.Errorf("stderr %q", errOut.String())
	}
}

func TestStreamExecReturnsTheExitCode(t *testing.T) {
	code, err := StreamExec(nil, Streams{strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}}, "sh", "-c", "exit 3")
	if err != nil || code != 3 {
		t.Errorf("got code %d, err %v; want 3, nil", code, err)
	}
}

func TestStreamExecReportsAProgramThatCannotRun(t *testing.T) {
	code, err := StreamExec(nil, Streams{strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}}, "engine-test-no-such-program")
	if err == nil || code != 1 {
		t.Errorf("got code %d, err %v; want 1 and an error", code, err)
	}
}
