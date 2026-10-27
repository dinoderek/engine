package ps

import (
	"cmp"
	"encoding/json"
	"fmt"
	"github.com/dinoderek/engine/internal/session"
	"io"
	"slices"
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
func WriteTable(w io.Writer, listing Listing) error {
	if err := writeQuota(w, listing.Quota); err != nil {
		return err
	}
	rows := listing.Rows
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

// WriteJSON prints quota and sessions together, with empty objects and arrays.
func WriteJSON(w io.Writer, listing Listing) error {
	if listing.Rows == nil {
		listing.Rows = []Row{}
	}
	if listing.Quota == nil {
		listing.Quota = map[string]session.Quota{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(listing)
}

func writeQuota(w io.Writer, quota map[string]session.Quota) error {
	names := make([]string, 0, len(quota))
	for name := range quota {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		line := name
		for _, key := range []string{"five_hour", "seven_day"} {
			window, ok := quota[name].Windows[key]
			if !ok {
				continue
			}
			label, format := "5h", "15:04"
			if key == "seven_day" {
				label, format = "7d", "Mon 15:04"
			}
			line += fmt.Sprintf("  %s %g%% (resets %s)", label, window.Used, time.Unix(window.ResetsAt, 0).Local().Format(format))
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}
