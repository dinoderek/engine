package ps

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
)

// Age renders a duration as one number and a unit: 5m, 3h, 2d. Under a minute
// is <1m, and under two days is in hours.
func Age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// WriteTable prints the rows as aligned columns under a header, or says there
// are none. A host or harness the claim left out shows as -.
func WriteTable(w io.Writer, rows []Row) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "no claimed tasks")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TASK\tHOST\tHARNESS\tSTATE\tAGE")
	for _, r := range rows {
		age := Age(time.Duration(r.AgeSeconds) * time.Second)
		fmt.Fprintln(tw, strings.Join([]string{r.Task, cmp.Or(r.Host, "-"), cmp.Or(r.Harness, "-"), r.State, age}, "\t"))
	}
	return tw.Flush()
}

// WriteJSON prints the rows as one indented JSON array: [] when there are
// none, never null.
func WriteJSON(w io.Writer, rows []Row) error {
	if rows == nil {
		rows = []Row{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rows)
}
