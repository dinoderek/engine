package controlcmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

const hostUsage = "usage: engine host status --all [--json]\n"

// hostMain is `engine host status --all`: the fleet as the coordinator's
// latest pass read it, a table or, with --json, the op's reply. It reads the
// server and reaches no host.
func hostMain(args []string, e Env) int {
	asJSON := slices.Contains(args, "--json")
	rest := slices.DeleteFunc(slices.Clone(args), func(a string) bool { return a == "--json" })
	if len(rest) != 2 || rest[0] != "status" || rest[1] != "--all" {
		fmt.Fprint(e.Err, hostUsage)
		return 2
	}
	client, err := makeClient(e)
	if err != nil {
		fmt.Fprintln(e.Err, "engine host status:", err)
		return 1
	}
	ctx := e.Context
	if ctx == nil {
		ctx = context.Background()
	}
	out, err := client.Call(ctx, control.Request{Op: "host-status"})
	if err == nil && out.Fleet == nil {
		err = fmt.Errorf("the server sent no fleet")
	}
	if err != nil {
		fmt.Fprintln(e.Err, "engine host status:", err)
		return 1
	}
	if asJSON {
		enc := json.NewEncoder(e.Out)
		enc.SetIndent("", "  ")
		err = enc.Encode(out.Fleet)
	} else {
		err = renderHostStatus(e.Out, *out.Fleet, time.Now())
	}
	if err != nil {
		fmt.Fprintln(e.Err, "engine host status:", err)
		return 1
	}
	return 0
}

// renderHostStatus prints one row per host, then one per window with its
// attempt. now dates the pass.
func renderHostStatus(w io.Writer, s control.HostStatus, now time.Time) error {
	if s.At.IsZero() {
		_, err := fmt.Fprintln(w, "No coordinator pass has read the hosts yet: passes read them only while a plan is coordinating.")
		return err
	}
	fmt.Fprintf(w, "As of the coordinator's pass at %s (%s ago)\n\n", s.At.UTC().Format(time.RFC3339), now.Sub(s.At).Round(time.Second))
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "HOST\tREACHABLE\tFREE SLOTS\tQUOTA\tREADINESS")
	for _, h := range s.Hosts {
		if h.Unreachable != "" {
			fmt.Fprintf(tw, "%s\tno: %s\t-\t-\t-\n", h.Name, oneLine(h.Unreachable))
			continue
		}
		fmt.Fprintf(tw, "%s\tyes\t%d of %d (heavy %d of %d)\t%s\t%s\n", h.Name, h.FreeSlots, h.Slots, h.FreeHeavySlots, h.HeavySlots, quotaText(h), readinessText(h))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	tw = tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "\nHOST\tWINDOW\tSTATE\tHARNESS\tTASK (ATTEMPT)")
	for _, h := range s.Hosts {
		for _, win := range h.Windows {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", h.Name, win.Name, windowState(win), dash(win.Harness), attemptText(win))
		}
	}
	return tw.Flush()
}

func windowState(w control.HostWindow) string {
	switch {
	case !w.Alive:
		return "exited"
	case w.State == "":
		return "live"
	}
	return w.State
}

func attemptText(w control.HostWindow) string {
	if w.Item == "" {
		return "-"
	}
	return w.Item + " (" + w.Attempt + ")"
}

func quotaText(h control.HostRow) string {
	var parts []string
	for _, q := range h.Quota {
		parts = append(parts, fmt.Sprintf("%s %s %.0f%% to %s", q.Harness, q.Window, q.Used, time.Unix(q.ResetsAt, 0).UTC().Format("01-02 15:04Z")))
	}
	return dash(strings.Join(parts, ", "))
}

// readinessText is "ready" when every repo's probe passed, else what is
// wrong: the tmux server's gone directory, a signed-out harness, each
// unready repo and each repo not yet probed.
func readinessText(h control.HostRow) string {
	var bad []string
	if h.Unready != "" {
		bad = append(bad, oneLine(h.Unready))
	}
	for _, name := range h.SignedOut {
		bad = append(bad, name+" signed out")
	}
	unknown := 0
	for _, r := range h.Readiness {
		switch r.State {
		case "unready":
			bad = append(bad, r.Repo+": "+oneLine(r.Detail))
		case "unknown":
			unknown++
		}
	}
	if unknown > 0 {
		bad = append(bad, fmt.Sprintf("%d repo(s) not probed yet", unknown))
	}
	if len(bad) == 0 {
		return "ready"
	}
	return "unready: " + strings.Join(bad, "; ")
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// oneLine keeps a table cell to the first line of a message.
func oneLine(s string) string { return strings.TrimSpace(strings.SplitN(s, "\n", 2)[0]) }
