package control

import "time"

// HostStatus is the coordinator's latest read of the fleet: what the
// host-status op returns. At is zero until a pass has read the hosts.
type HostStatus struct {
	At    time.Time `json:"at,omitzero"`
	Hosts []HostRow `json:"hosts"`
}

// HostRow is one host of the latest pass, in hosts.toml. An unreachable host
// carries only its name and why; the rest is unknown.
type HostRow struct {
	Name        string `json:"name"`
	Unreachable string `json:"unreachable,omitempty"`
	// Slots are how many sessions the host runs at once and how many more
	// it can start, with the heavy ones counted apart.
	Slots          int `json:"slots"`
	FreeSlots      int `json:"free_slots"`
	HeavySlots     int `json:"heavy_slots"`
	FreeHeavySlots int `json:"free_heavy_slots"`
	// Unready is why the host takes no session, when its tmux server's
	// working directory is gone.
	Unready string `json:"unready,omitempty"`
	// SignedOut names the harnesses the host says are signed out.
	SignedOut []string     `json:"signed_out,omitempty"`
	Windows   []HostWindow `json:"windows"`
	Quota     []HostQuota  `json:"quota,omitempty"`
	// Readiness is the probe's answer for each repo asked about.
	Readiness []HostReadiness `json:"readiness,omitempty"`
}

// HostWindow is a window of the host's tmux session, and the attempt it
// runs, when one matches it.
type HostWindow struct {
	Name    string `json:"name"`
	Alive   bool   `json:"alive"`
	Harness string `json:"harness,omitempty"`
	// State is working, waiting or limited, from the harness; empty is unknown.
	State   string `json:"state,omitempty"`
	Item    string `json:"item,omitempty"`
	Attempt string `json:"attempt,omitempty"`
}

// HostQuota is one usage window of one harness: the share used and when it
// resets, in epoch seconds.
type HostQuota struct {
	Harness  string  `json:"harness"`
	Window   string  `json:"window"`
	Used     float64 `json:"used"`
	ResetsAt int64   `json:"resets_at"`
}

// HostReadiness is a host's readiness for one repo. State is ready, unready
// or unknown; Detail names the failed checks, or why the probe could not run.
type HostReadiness struct {
	Repo   string `json:"repo"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// hostStatus is the host-status op: an operator-only read of the cached
// pass, so a request never contacts a host.
func (s Service) hostStatus(a Actor) (Response, error) {
	if err := operator(a); err != nil {
		return Response{}, err
	}
	out := HostStatus{Hosts: []HostRow{}}
	if s.Hosts != nil {
		out = s.Hosts()
	}
	return Response{Fleet: &out}, nil
}
