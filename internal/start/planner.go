package start

import (
	"fmt"
	"io"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

// Feature labels the planner start reads.
const (
	featureLabel = "engine:feature"
	planned      = "engine:planned"
)

// RunPlanner starts the planner session of an approved feature that has no
// tasks yet, hands-off at the deep tier, as engine start does for a task.
func RunPlanner(feature tracker.Ref, d Deps, out io.Writer) error {
	p, err := checkPlanner(feature, d)
	if err != nil {
		return err
	}
	s, err := p.launch(d)
	if err != nil {
		return err
	}
	report(out, s)
	return nil
}

// checkPlanner runs the refusals for a planner start.
func checkPlanner(feature tracker.Ref, d Deps) (plan, error) {
	p := plan{opts: Options{Ref: feature, Mode: HandsOff}, planner: true}
	if err := p.checkOptions(); err != nil {
		return plan{}, err
	}
	if err := p.loadHost(d); err != nil {
		return plan{}, err
	}
	issue, err := tracker.ReadTask(d.GH, p.token, feature)
	if err != nil {
		return plan{}, err
	}
	switch {
	case !issue.Open:
		return plan{}, refuse("%s is closed", feature)
	case !issue.Has(featureLabel) || !issue.Has(approved):
		return plan{}, refuse("%s is not an approved feature (%s and %s)", feature, featureLabel, approved)
	case issue.Has(planned):
		return plan{}, refuse("%s is already %s", feature, planned)
	}
	p.task = tracker.Task{Ref: feature, Open: true, Tier: string(hosts.Deep)}
	for _, step := range []func(Deps) error{p.pickModel, p.checkBin, p.checkTmux} {
		if err := step(d); err != nil {
			return plan{}, err
		}
	}
	return p, nil
}

// PlannerKickoff is the planner session's first message.
func PlannerKickoff(feature tracker.Ref, runID, name string) string {
	return fmt.Sprintf("Plan breakdown %s with the engine planner. run=%s tmux=%s:%s", feature, runID, session.Name, name)
}
