package upgrade

import (
	"fmt"
	"io"
	"strings"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/setup"
)

// action is one numbered step of the run, as --dry-run prints it.
type action struct {
	// where is the host it runs on, "here" for the caller; show is what it
	// runs.
	where, show string
	// always is set on the server's start, which runs after a failure or an
	// interrupt too.
	always bool
	// do takes the step; false stops every later step but the start.
	do func(out io.Writer) bool
}

// plan lists the run's steps, in order.
func (f *fleet) plan() []action {
	f.stamp = f.d.Now().UTC().Format("20060102T150405Z")
	stop, _ := f.server.ServerShellLine(hosts.ServerStop)
	start, _ := f.server.ServerShellLine(hosts.ServerStart)
	on := f.where(f.server)
	acts := []action{
		{where: on, show: stop, do: f.stop},
		{where: on, show: f.backupCommand(), do: f.backup},
		{where: "here", show: setup.InstallCommand(f.toolchain, f.commit), do: f.install},
	}
	acts = append(acts, f.pluginActions()...)
	acts = append(acts, f.setupAction(f.server),
		action{where: on, show: start + ", then wait until the server answers", always: true, do: f.start})
	for _, h := range f.rest() {
		acts = append(acts, f.setupAction(h))
	}
	return acts
}

// execute takes the steps in order. After a failed step, or an interrupt,
// only the server's start still runs; and it runs on any exit once the stop
// was tried.
func (f *fleet) execute(out io.Writer) {
	defer func() {
		if f.stopped && !f.started {
			f.start(out)
		}
	}()
	fmt.Fprintf(out, "engine upgrade: to %s, the head of main\n", f.shortTarget())
	halted := false
	for i, a := range f.actions {
		if !halted && f.interrupted() {
			halted = true
			f.openf(out, "engine upgrade", "the upgrade was interrupted before step %d", i+1)
		}
		if halted && !a.always {
			continue
		}
		fmt.Fprintf(out, "%d. %s: %s\n", i+1, a.where, a.show)
		if !a.do(out) {
			halted = true
		}
	}
}

// printPlan is --dry-run's output: what the preflight found, and the steps.
func (f *fleet) printPlan(out io.Writer) {
	fmt.Fprintf(out, "engine upgrade --dry-run: nothing is changed\n")
	fmt.Fprintf(out, "target: %s, the head of main\n", f.shortTarget())
	fmt.Fprintf(out, "server: %s on %s, %s\n", f.server.Services.Server, f.server.Name, f.active)
	var now []string
	for _, h := range f.hosts {
		r := f.results[h.Name]
		if r.unreachable != "" {
			now = append(now, h.Name+" unreachable ("+r.unreachable+")")
			continue
		}
		now = append(now, h.Name+" "+r.before)
	}
	fmt.Fprintf(out, "engine now: %s\n", strings.Join(now, ", "))
	fmt.Fprintln(out, "the upgrade would take these steps, in order:")
	for i, a := range f.actions {
		fmt.Fprintf(out, "%d. %s: %s\n", i+1, a.where, a.show)
	}
}

// openf records an item for the operator, and prints it under its step.
func (f *fleet) openf(out io.Writer, command, format string, args ...any) {
	item := openItem{detail: fmt.Sprintf(format, args...), command: command}
	f.opens = append(f.opens, item)
	fmt.Fprintf(out, "   OPEN: %s\n", item.detail)
}
