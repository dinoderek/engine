package ps

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

func TestListQuotaAcrossHostsWithoutClaims(t *testing.T) {
	at := time.Unix(150, 0)
	src := Source{Hosts: []string{"mac", "dev", "down"}, Claimed: func() ([]tracker.ClaimedTask, error) { return nil, nil }, Now: func() time.Time { return at }, Status: func(host string) (hostops.Status, error) {
		q := session.Quota{Harness: "claude", ObservedAt: 100, Windows: map[string]session.QuotaWindow{"five_hour": {Used: 83, ResetsAt: 150}, "seven_day": {Used: 41, ResetsAt: 300}}}
		if host == "dev" {
			q.ObservedAt = 110
		}
		st := hostops.Status{Quota: map[string]session.Quota{"claude": q}}
		if host == "down" {
			q.ObservedAt = 120
			st.Quota["claude"] = q
			return st, errors.New("offline")
		}
		return st, nil
	}}
	l, err := List(src)
	if err != nil || l.Quota["claude"].ObservedAt != 110 || l.Quota["claude"].Windows["five_hour"].Used != 0 || len(l.Warnings) != 1 {
		t.Fatalf("%+v, %v", l, err)
	}
	var out bytes.Buffer
	if err := WriteTable(&out, l); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "claude  5h 0% (resets ") || !strings.Contains(out.String(), "  7d 41% (resets ") || !strings.HasSuffix(out.String(), "no claimed tasks\n") {
		t.Fatalf("%s", out.String())
	}
	if err := WriteJSON(&out, l); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"quota": {`) || !strings.Contains(out.String(), `"sessions": []`) {
		t.Fatalf("%s", out.String())
	}
}
