package ps

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

func TestLimitedRowsAndResetRendering(t *testing.T) {
	for _, tc := range []struct {
		label, state, want string
		reset              int64
	}{
		{"engine:running", session.StateLimited, Limited, 2000},
		{"engine:running", session.StateLimited, Limited, 0},
		{"engine:in-review", session.StateLimited, InReview, 0},
		{"engine:needs-human", session.StateLimited, NeedsHuman, 0},
	} {
		t.Run(tc.label+tc.state+time.Unix(tc.reset, 0).String(), func(t *testing.T) {
			w := win("w-1", tc.state)
			w.ResetsAt = tc.reset
			f := (&fake{tasks: []tracker.ClaimedTask{task(1, "w-1", tc.label)}}).on([]hostops.Window{w})
			l := list(t, f)
			r := l.Rows[0]
			if r.State != tc.want || r.ResetsAt != tc.reset {
				t.Fatalf("row %+v", r)
			}
			var out bytes.Buffer
			if err := WriteTable(&out, l); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), tc.want) {
				t.Errorf("table %s", out.String())
			}
			if tc.reset > 0 && !strings.Contains(out.String(), time.Unix(tc.reset, 0).Local().Format("Mon 15:04")) {
				t.Errorf("reset missing: %s", out.String())
			}
			out.Reset()
			if err := WriteJSON(&out, l); err != nil {
				t.Fatal(err)
			}
			var got Listing
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Rows[0].State != tc.want || got.Rows[0].ResetsAt != tc.reset {
				t.Errorf("json %s", out.String())
			}
		})
	}
}
