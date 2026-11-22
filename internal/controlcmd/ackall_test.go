package controlcmd

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
)

func TestCLIAckAllAcknowledgesMessagesInScope(t *testing.T) {
	e, _ := environment(t)
	repo := command(t, e, "repo", "add", "--title", "Demo", "--slug", "owner/demo", "--remote", "ssh://example.test/demo").Item
	plan := command(t, e, "plan", "create", "--repo", repo.ID, "--title", "Feature", "--body", "Requirements").Item
	first := command(t, e, "tell", plan.ID, "first", "--operator").Updates[0]
	command(t, e, "ask", plan.ID, "Which way?")
	later := command(t, e, "tell", plan.ID, "later", "--operator").Updates[0]
	out := command(t, e, "ack", "--all", "--repo", repo.ID, "--before", strconv.Itoa(later.ID), "--request-key", "bulk")
	if len(out.Updates) != 1 || out.Updates[0].Kind != "acknowledged" || out.Updates[0].ReplyTo != first.ID {
		t.Fatalf("ack --all %+v", out.Updates)
	}
	if replay := command(t, e, "ack", "--all", "--repo", repo.ID, "--before", strconv.Itoa(later.ID), "--request-key", "bulk"); replay.Updates[0].ID != out.Updates[0].ID {
		t.Fatal("a replay acknowledged again")
	}
	out = command(t, e, "ack", "--all")
	if len(out.Updates) != 1 || out.Updates[0].ReplyTo != later.ID {
		t.Fatalf("ack --all %+v", out.Updates)
	}
	if inbox := command(t, e, "inbox"); len(inbox.Updates) != 1 || !inbox.Updates[0].RequiresReply {
		t.Fatalf("the question did not stay open: %+v", inbox.Updates)
	}
}

func TestCLIAckAllUsage(t *testing.T) {
	e, _ := environment(t)
	for _, args := range [][]string{
		{"ack", "--all", "3"},
		{"ack", "3", "--before", "4"},
		{"inbox", "--all"},
		{"ack", "--all", "--before", "-1"},
	} {
		e.Err.(*bytes.Buffer).Reset()
		if code := Main(args, e); code != 2 || !strings.Contains(e.Err.(*bytes.Buffer).String(), "engine ack --all [--repo <id>] [--before <sequence>]") {
			t.Fatalf("%v: exit %d: %s", args, code, e.Err)
		}
	}
}
