package controlcmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
)

func TestHostPauseResumeAndMoveParse(t *testing.T) {
	e := Env{In: strings.NewReader(""), Getenv: func(string) string { return "" }}
	r, err := Parse(strings.Fields("host pause devbox --json --request-key k"), e)
	if err != nil || r.Op != "host-pause" || r.ID != "devbox" || r.Key != "k" || r.Move != nil {
		t.Fatalf("pause: %+v, %v", r, err)
	}
	if r, err = Parse(strings.Fields("host resume devbox"), e); err != nil || r.Op != "host-resume" || r.ID != "devbox" || r.Key == "" {
		t.Fatalf("resume: %+v, %v", r, err)
	}
	r, err = Parse(strings.Fields("host move devbox --harness codex --yes"), e)
	if err != nil || r.Op != "host-move" || r.ID != "devbox" || r.Item.Harness != "" || r.Move == nil || *r.Move != (control.MoveRequest{Harness: "codex", Confirm: true}) {
		t.Fatalf("move: %+v (move %+v), %v", r, r.Move, err)
	}
	if r, err = Parse(strings.Fields("host move devbox"), e); err != nil || r.Move == nil || *r.Move != (control.MoveRequest{}) {
		t.Fatalf("a bare move: %+v, %v", r, err)
	}
	for _, args := range []string{"host pause", "host resume", "host move", "host pause devbox --yes", "host resume devbox --harness codex",
		"host pause devbox --host mac", "host move devbox --host mac", "host move devbox mac", "host pause devbox extra"} {
		if _, err := Parse(strings.Fields(args), e); err == nil {
			t.Errorf("%s parsed", args)
		}
	}
	for _, line := range []string{"engine host pause|resume <host> [--json]\n", "engine host move <host> [--harness <harness>] [--yes] [--json]\n"} {
		if !strings.Contains(Usage, line) {
			t.Errorf("the usage does not show %q", line)
		}
	}
}

func TestHostPauseAndResumePrintTheHostAndAreSafeToRepeat(t *testing.T) {
	e, _ := moving(t, false)
	out := command(t, e, "host", "pause", "devbox", "--json", "--request-key", "pause-1")
	if out.Host == nil || out.Host.Name != "devbox" || !out.Host.Paused || out.Host.PausedAt.IsZero() {
		t.Fatalf("pause printed %+v", out.Host)
	}
	if again := command(t, e, "host", "pause", "devbox", "--request-key", "pause-1"); !again.Host.PausedAt.Equal(out.Host.PausedAt) {
		t.Fatalf("a repeat: %+v", again.Host)
	}
	if again := command(t, e, "host", "pause", "devbox"); !again.Host.Paused || !again.Host.PausedAt.Equal(out.Host.PausedAt) {
		t.Fatalf("a repeat under a new key: %+v", again.Host)
	}
	if got := command(t, e, "host", "resume", "devbox").Host; got.Paused {
		t.Fatalf("resume printed %+v", got)
	}
	if got := command(t, e, "host", "resume", "devbox").Host; got.Paused {
		t.Fatalf("a second resume printed %+v", got)
	}
}

func TestHostCommandsRefuseAnUnknownHost(t *testing.T) {
	e, _ := moving(t, false)
	errOut := e.Err.(*bytes.Buffer)
	for _, verb := range []string{"pause", "resume", "move"} {
		errOut.Reset()
		if code := Main([]string{"host", verb, "nowhere"}, e); code != 1 || !strings.Contains(errOut.String(), "host nowhere is not in hosts.toml") {
			t.Errorf("host %s nowhere: %d: %s", verb, code, errOut)
		}
	}
}

func TestHostMoveMovesItsTasksAndOnlyTheHarnessWhenGiven(t *testing.T) {
	e, task := moving(t, false)
	none := command(t, e, "host", "move", "devbox", "--harness", "codex", "--json")
	if none.Host == nil || !none.Host.Paused || len(none.Host.Moved) != 0 {
		t.Fatalf("a harness with no task there: %+v", none.Host)
	}
	if got := command(t, e, "task", "show", task).Item; got.State != "running" {
		t.Fatalf("the task moved: %+v", got)
	}
	out := command(t, e, "host", "move", "devbox", "--harness", "claude", "--request-key", "evacuate")
	if len(out.Host.Moved) != 1 || out.Host.Moved[0] != task || !out.Host.Paused {
		t.Fatalf("host move printed %+v", out.Host)
	}
	if got := command(t, e, "attempt", "list", task).Attempts; len(got) != 1 || got[0].Reason != control.EndMoved || got[0].MovedBlind {
		t.Fatalf("attempts %+v", got)
	}
	if again := command(t, e, "host", "move", "devbox", "--harness", "claude", "--request-key", "evacuate"); len(again.Host.Moved) != 1 {
		t.Fatalf("a repeat under its key: %+v", again.Host)
	}
}

// Off a host the server could not read, the host move warns once, and
// pauses and moves only with --yes or a yes at a terminal.
func TestHostMoveOffAnUnreachableHostConfirmsOnce(t *testing.T) {
	e, task := moving(t, true)
	errOut := e.Err.(*bytes.Buffer)
	if code := Main([]string{"host", "move", "devbox"}, e); code != 1 || !strings.Contains(errOut.String(), "warning: the coordinator's latest pass did not read host devbox") ||
		!strings.Contains(errOut.String(), "rerun with --yes") || !strings.Contains(errOut.String(), "not paused") {
		t.Fatalf("an unconfirmed host move exited %d: %s", code, errOut)
	}
	terminal := e
	terminal.Terminal = func() bool { return true }
	terminal.In = strings.NewReader("n\n")
	errOut.Reset()
	if code := Main([]string{"host", "move", "devbox"}, terminal); code != 1 || !strings.Contains(errOut.String(), "Move it anyway? [y/N]") {
		t.Fatalf("a declined host move exited %d: %s", code, errOut)
	}
	if got := command(t, e, "task", "show", task).Item; got.State != "running" {
		t.Fatalf("a refused host move changed the task: %+v", got)
	}
	terminal.In = strings.NewReader("y\n")
	terminal.Out.(*bytes.Buffer).Reset()
	if code := Main([]string{"host", "move", "devbox", "--request-key", "asked"}, terminal); code != 0 || !strings.Contains(terminal.Out.(*bytes.Buffer).String(), `"paused": true`) {
		t.Fatalf("a confirmed host move exited %d: %s %s", code, errOut, terminal.Out)
	}
	if got := command(t, e, "attempt", "list", task).Attempts; len(got) != 1 || !got[0].MovedBlind {
		t.Fatalf("attempts %+v", got)
	}
}

func TestHostStatusMarksAPausedHost(t *testing.T) {
	fleet := twoHosts()
	fleet.Hosts[0].Paused = true
	var out bytes.Buffer
	if err := renderHostStatus(&out, fleet, fleet.At); err != nil || !strings.Contains(out.String(), fleet.Hosts[0].Name+" (paused)") {
		t.Fatalf("%v\n%s", err, out.String())
	}
}
