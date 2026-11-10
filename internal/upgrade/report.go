package upgrade

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/dinoderek/engine/internal/hosts"
)

// report prints each host's engine before and after, its plugin and its
// setup, then every open item with its command, and reports whether there
// are none.
func (f *fleet) report(out io.Writer) bool {
	fmt.Fprintf(out, "\nengine upgrade: %s, the head of main\n", f.shortTarget())
	opens := append([]openItem(nil), f.opens...)
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "host\tbefore\tafter\tplugin\tsetup")
	for _, h := range f.hosts {
		r := f.results[h.Name]
		state, item := f.hostState(h, r)
		if item != nil {
			opens = append(opens, *item)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", h.Name, r.before, dash(r.after), dash(r.plugin), state)
	}
	_ = tw.Flush()
	fmt.Fprintf(out, "server: %s on %s %s\n", f.server.Services.Server, f.server.Name, f.serverState())
	if len(opens) == 0 {
		fmt.Fprintln(out, "every host is upgraded")
	} else {
		fmt.Fprintf(out, "%d open item(s); run each command, then rerun engine upgrade:\n", len(opens))
		for _, o := range opens {
			fmt.Fprintf(out, "  %s: %s\n", o.detail, o.command)
		}
	}
	fmt.Fprintln(out, "engine ui is not restarted: restart it by hand where it runs")
	return len(opens) == 0
}

// hostState is a host's setup column, and its open item.
func (f *fleet) hostState(h hosts.Host, r *hostResult) (string, *openItem) {
	name := h.Name
	switch {
	case r.unreachable != "":
		return "OPEN: unreachable", &openItem{name + " could not be reached (" + r.unreachable + ")", f.setupLine(h)}
	case !r.ran:
		return "not run", &openItem{name + " was not set up: the upgrade stopped first", "engine upgrade"}
	case r.open != "":
		return "OPEN", &openItem{name + ": " + r.open, f.setupLine(h)}
	}
	return "set up", nil
}

// serverState is how the run left the server.
func (f *fleet) serverState() string {
	switch {
	case !f.stopped:
		return "was not stopped"
	case f.answered && f.backedUp:
		return fmt.Sprintf("was backed up to ~/.local/share/engine/server/backups/%s.db, and started and answers", f.stamp)
	case f.answered:
		return "was not backed up; it started and answers"
	}
	return "is open: see below"
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
