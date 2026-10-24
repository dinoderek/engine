package start

import (
	"bytes"
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
			got, err := p.command(d, session.Clone{Path: "/clones/o/r"}, "r-3-20260102T030405Z")
			if err != nil || !strings.Contains(got, "Plan breakdown "+ref(3).String()+" with the engine planner. run=r-3-20260102T030405Z tmux=engine:r-3") || !strings.Contains(got, "--model opus-x") {
				t.Errorf("command %s, %v", got, err)
			}
			rec := p.record(Started{RunID: "r-3-x"}, "/log", fixedNow())
			if rec.Feature != ref(3).String() || rec.Task != ref(3).String() || rec.Tier != "deep" {
				t.Errorf("record %+v", rec)
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
