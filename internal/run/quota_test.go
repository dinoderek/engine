package run

import (
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/session"
)

func TestFleetQuota(t *testing.T) {
	host := func(at int64, down string) HostLoad {
		return HostLoad{Unreachable: down, Status: hostops.Status{Quota: map[string]session.Quota{"claude": {Harness: "claude", ObservedAt: at, Windows: map[string]session.QuotaWindow{"five_hour": {Used: 83, ResetsAt: 150}, "seven_day": {Used: 41, ResetsAt: 300}}}}}}
	}
	f := Fleet{Hosts: []HostLoad{host(110, ""), host(100, ""), host(120, "offline")}}
	got := f.Quota(time.Unix(150, 0))
	if len(got) != 1 || got["claude"].ObservedAt != 110 || got["claude"].Windows["five_hour"].Used != 0 || got["claude"].Windows["seven_day"].Used != 41 {
		t.Fatalf("%+v", got)
	}
}
