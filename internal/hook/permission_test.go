package hook

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

var at = time.Date(2026, 10, 24, 13, 7, 0, 0, time.UTC)

const readRequest = `{"hook_event_name":"PermissionRequest","tool_name":"Read","tool_input":{"file_path":"/opt/x"},"permission_mode":"auto"}`

func TestPermissionDeniesAnUnattendedRequest(t *testing.T) {
	var out, errs, log bytes.Buffer
	if code := (Permission{Out: &out, Err: &errs, Unattended: true, Log: &log, Now: at}).Handle(strings.NewReader(readRequest)); code != 0 {
		t.Fatalf("code %d, stderr %q", code, errs.String())
	}
	var got struct {
		Output struct {
			Event    string `json:"hookEventName"`
			Decision struct {
				Behavior string `json:"behavior"`
				Message  string `json:"message"`
			} `json:"decision"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decision %q: %v", out.String(), err)
	}
	if got.Output.Event != "PermissionRequest" || got.Output.Decision.Behavior != "deny" || !strings.Contains(got.Output.Decision.Message, "Permission hard stop") {
		t.Errorf("decision %+v", got)
	}
	var entry Entry
	if err := json.Unmarshal(log.Bytes(), &entry); err != nil {
		t.Fatalf("log %q: %v", log.String(), err)
	}
	if entry.Tool != "Read" || entry.Decision != "deny" || entry.Mode != "auto" || !entry.Time.Equal(at) || string(entry.Input) != `{"file_path":"/opt/x"}` {
		t.Errorf("entry %+v", entry)
	}
}

func TestPermissionLeavesAnAttendedRequestAlone(t *testing.T) {
	var out, errs, log bytes.Buffer
	if code := (Permission{Out: &out, Err: &errs, Unattended: false, Log: &log, Now: at}).Handle(strings.NewReader(readRequest)); code != 0 || out.Len() != 0 || log.Len() != 0 || errs.Len() != 0 {
		t.Errorf("code %d, out %q, log %q, stderr %q", code, out.String(), log.String(), errs.String())
	}
}

func TestPermissionDeniesWithoutALog(t *testing.T) {
	var out, errs bytes.Buffer
	if code := (Permission{Out: &out, Err: &errs, Unattended: true, Now: at}).Handle(strings.NewReader(readRequest)); code != 0 || !strings.Contains(out.String(), `"deny"`) {
		t.Errorf("code %d, out %q", code, out.String())
	}
}

func TestPermissionLogsAnAutoModeDenial(t *testing.T) {
	event := `{"hook_event_name":"PermissionDenied","tool_name":"Bash","tool_input":{"command":"rm -rf /x"},"reason":"[Irreversible Local Destruction]"}`
	for _, unattended := range []bool{true, false} {
		var out, errs, log bytes.Buffer
		if code := (Permission{Out: &out, Err: &errs, Unattended: unattended, Log: &log, Now: at}).Handle(strings.NewReader(event)); code != 0 || out.Len() != 0 {
			t.Fatalf("code %d, out %q", code, out.String())
		}
		var entry Entry
		if err := json.Unmarshal(log.Bytes(), &entry); err != nil || entry.Reason != "[Irreversible Local Destruction]" || entry.Decision != "denied by auto mode" {
			t.Errorf("unattended %v: entry %+v, %v", unattended, entry, err)
		}
	}
}

func TestPermissionRejectsOtherInput(t *testing.T) {
	for name, in := range map[string]string{"not JSON": "{", "other event": `{"hook_event_name":"PreToolUse"}`} {
		var out, errs bytes.Buffer
		if code := (Permission{Out: &out, Err: &errs, Unattended: true, Now: at}).Handle(strings.NewReader(in)); code != 1 || errs.Len() == 0 || out.Len() != 0 {
			t.Errorf("%s: code %d, out %q, stderr %q", name, code, out.String(), errs.String())
		}
	}
}

type failing struct{}

func (failing) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestPermissionWriteFailures(t *testing.T) {
	var errs bytes.Buffer
	if code := (Permission{Out: failing{}, Err: &errs, Unattended: true, Now: at}).Handle(strings.NewReader(readRequest)); code != 1 || !strings.Contains(errs.String(), "disk full") {
		t.Errorf("decision write: code %d, stderr %q", code, errs.String())
	}
	errs.Reset()
	var out bytes.Buffer
	if code := (Permission{Out: &out, Err: &errs, Unattended: true, Log: failing{}, Now: at}).Handle(strings.NewReader(readRequest)); code != 0 || !strings.Contains(out.String(), `"deny"`) || !strings.Contains(errs.String(), "disk full") {
		t.Errorf("log write: code %d, out %q, stderr %q", code, out.String(), errs.String())
	}
}
