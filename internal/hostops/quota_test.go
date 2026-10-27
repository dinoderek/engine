package hostops

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/session"
)

func TestStatusQuotaFromEndedAndOlderRuns(t *testing.T) {
	h := newHome(t, "mac")
	at := fixedNow()
	for i, run := range []string{"r-1-old", "r-1-new", "r-2"} {
		dir := writeRun(t, h, session.Record{RunID: run, Name: "r-1", Harness: Claude, Spawned: at.Add(time.Duration(i) * time.Hour)})
		if i == 1 {
			continue
		} // newest run has no reading; keep the older run's.
		q := session.Quota{Harness: Claude, ObservedAt: at.Unix() + int64(i), Windows: map[string]session.QuotaWindow{"five_hour": {Used: 83, ResetsAt: at.Unix() + 100}}}
		data, err := json.Marshal(q)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, session.QuotaFile), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ReadStatus(Deps{Home: h.dir, Tmux: (&tmuxFake{}).tmux(), Exec: agentsFake("[]", nil), Now: func() time.Time { return at }})
	if err != nil || got.Quota[Claude].ObservedAt != at.Unix()+2 {
		t.Fatalf("quota %+v, %v", got.Quota, err)
	}
}

func TestStatusOmitsUnknownQuota(t *testing.T) {
	h := newHome(t, "mac")
	got, err := ReadStatus(Deps{Home: h.dir, Tmux: (&tmuxFake{}).tmux(), Exec: agentsFake("[]", nil)})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	if _, ok := object["quota"]; ok {
		t.Fatalf("unknown quota emitted: %s", data)
	}
}
