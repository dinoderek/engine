package run

import (
	"errors"
	"fmt"
	"time"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/reconcile"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

// PlaceNoted is the note that the coordinator has commented that no host
// can take a task.
const PlaceNoted = "place-noted"

// Fleet is every host in hosts.toml as placement sees it this pass: the
// reconcile core's.
type Fleet = reconcile.Fleet

// HostLoad is one host: what it runs, and how full it is now.
type HostLoad = reconcile.HostLoad

// load is a host's load from its status.
func load(h hosts.Host, st hostops.Status, err error) HostLoad {
	return reconcile.Load(h.Name, h.Harnesses, h.Capabilities, st, err)
}

// place is step 3's placement, by the reconcile core's rules: each ready
// task, in start order, gets a host or waits with the reason. One no host
// could take even when free is commented on once.
func place(v View, cands []tracker.Card) []Action {
	wants := make([]reconcile.Want, len(cands))
	for i, c := range cands {
		pinned, _, err := tracker.ParseHarnessTier(c.Body)
		wants[i] = reconcile.Want{ID: c.Ref.String(), Requires: c.Requires, Harness: pinned, Invalid: err}
	}
	var acts []Action
	for i, p := range v.Fleet.Place(wants) {
		c := cands[i]
		if p.Wait != "" {
			acts = append(acts, Action{Kind: Wait, Ref: c.Ref, Text: p.Wait, Notify: p.Notify && !v.PlaceNoted[c.Ref]})
			continue
		}
		acts = append(acts, Action{Kind: StartTask, Ref: c.Ref, Host: p.Host, Harness: p.Harness, Text: "ready, on " + p.Host})
	}
	return acts
}

// status is a host's reply, or why there is none.
func status(f Fleet, name string) (hostops.Status, error) {
	h, ok := f.Host(name)
	switch {
	case !ok:
		return hostops.Status{}, fmt.Errorf("%q is not in hosts.toml", name)
	case h.Unreachable != "":
		return hostops.Status{}, errors.New(h.Unreachable)
	}
	return h.Status, nil
}

// Quota is the newest reading per harness from hosts that answered.
func Quota(f Fleet, now time.Time) map[string]session.Quota {
	readings := []map[string]session.Quota{}
	for _, host := range f.Hosts {
		if host.Unreachable == "" {
			readings = append(readings, host.Status.Quota)
		}
	}
	return session.MergeQuota(now, readings...)
}
