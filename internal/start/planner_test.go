package start

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/session"
)

func TestCheckPlanner(t *testing.T) {
	h := newHome(t, "mac")
	tests := []struct {
		name   string
		issue  issue
		refuse string
	}{
		{"approved feature", issue{number: 3, state: "open", labels: []string{"engine:feature", "engine:approved"}}, ""},
		{"closed", issue{number: 3, state: "closed", labels: []string{"engine:feature", "engine:approved"}}, "is closed"},
		{"not approved", issue{number: 3, state: "open", labels: []string{"engine:feature", "engine:prepare"}}, "not an approved feature"},
		{"a task", issue{number: 3, state: "open", labels: []string{"engine:approved"}}, "not an approved feature"},
		{"planned", issue{number: 3, state: "open", labels: []string{"engine:feature", "engine:approved", "engine:planned"}}, "already engine:planned"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gh := &ghFake{issue: tt.issue}
			tm := &tmuxFake{}
			d := Deps{GH: gh.run, Run: gitFake(t), Tmux: &session.Tmux{Run: tm.run, Bin: "tmux"}, Home: h.dir, Getenv: func(string) string { return "" }}
			p, err := checkPlanner(ref(3), d)
			if tt.refuse != "" {
				if err == nil || !strings.Contains(err.Error(), tt.refuse) {
					t.Errorf("error %v, want %q", err, tt.refuse)
				}
				gh.readOnly(t)
				tm.readOnly(t)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !p.planner || p.tier != "deep" || p.model.Model != "opus-x" || p.name != "r-3" || p.opts.Mode != HandsOff {
				t.Errorf("plan %+v", p)
			}
			r := p.spawnRequest("r-3-20260102T030405Z")
			if r.Kickoff != "Plan breakdown "+ref(3).String()+" with the engine planner. run=r-3-20260102T030405Z tmux=engine:r-3" ||
				r.Model != "opus-x" || r.Feature != ref(3).String() || r.Task != 3 || r.Tier != "deep" {
				t.Errorf("spawn request %+v", r)
			}
		})
	}
}

func TestRunPlannerRefuses(t *testing.T) {
	h := newHome(t, "mac")
	gh := &ghFake{issue: issue{number: 3, state: "closed"}}
	d := Deps{GH: gh.run, Tmux: &session.Tmux{Run: (&tmuxFake{}).run, Bin: "tmux"}, Home: h.dir, Getenv: func(string) string { return "" }}
	if err := RunPlanner(ref(3), d, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "nothing was changed") {
		t.Errorf("got %v", err)
	}
}

// The feature's Harness / tier section picks the planner's harness; its tier
// word does not change the planner's deep tier, and a harness hosts.toml does
// not define is refused.
func TestCheckPlannerHarness(t *testing.T) {
	h := newHome(t, "mac")
	write(t, filepath.Join(h.dir, "bin", "codex"), "#!/bin/sh\nexit 0\n", 0o755)
	labels := []string{"engine:feature", "engine:approved"}
	tests := []struct {
		name, body, harness, model, refuse string
	}{
		{"default", "## Outcome\nWorks\n", "claude", "opus-x", ""},
		{"codex", "## Harness / tier\ncodex light\n", "codex", "sol", ""},
		{"undefined", "## Harness / tier\ngemini\n", "", "", `names harness "gemini"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gh := &ghFake{issue: issue{number: 3, state: "open", labels: labels, body: tt.body}}
			d := Deps{GH: gh.run, Run: gitFake(t), Tmux: &session.Tmux{Run: (&tmuxFake{}).run, Bin: "tmux"}, Home: h.dir, Getenv: func(string) string { return "" }}
			p, err := checkPlanner(ref(3), d)
			if tt.refuse != "" {
				if err == nil || !strings.Contains(err.Error(), tt.refuse) {
					t.Fatalf("error %v, want %q", err, tt.refuse)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if p.harness != tt.harness || p.tier != "deep" || p.model.Model != tt.model {
				t.Errorf("harness %s tier %s model %s", p.harness, p.tier, p.model.Model)
			}
		})
	}
}
