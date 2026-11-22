package control

import "fmt"

// HostRecord is what the coordinator saved of one host.
type HostRecord struct {
	// TmuxStart is when the host's session tmux server started, in epoch
	// seconds, as the last successful read that found a server saw it.
	TmuxStart int64 `json:"tmux_start,omitempty"`
}

// TmuxRead is what a successful read of a host showed of its tmux server.
type TmuxRead struct {
	// Known is false when no tmux server runs, and Start then zero.
	Known bool
	Start int64
}

// SawTmux records a successful read of host's tmux server, and settles a
// host-wide loss it shows, in one transaction. The start time is saved
// whenever a server runs.
//
// A read is a host-wide loss when every attempt of the host last seen live
// has lost its window (alive says whether it still has one), and either the
// server's start time differs from the saved one or no server runs. Each
// attempt of the host whose window is gone is then marked HostLost, so its
// death ends outside the cap, and the operator gets one notice for the host.
// A server with no saved time to compare, and a host with no attempt last
// seen live, are no loss: tmux exits after its last window. It returns how
// many attempts the loss marked.
func (s Service) SawTmux(host string, read TmuxRead, alive func(*State, Attempt) bool) (int, error) {
	marked := 0
	err := s.Store.Transaction(func(st *State) error {
		saved := st.Hosts[host].TmuxStart
		for _, at := range hostLoss(st, host, read, saved, alive) {
			at.HostLost = true
			st.Attempts[at.ID] = at
			marked++
		}
		if read.Known && read.Start != saved {
			st.Hosts[host] = HostRecord{TmuxStart: read.Start}
		}
		if marked > 0 {
			st.update(Actor{Name: "coordinator", Role: "coordinator"}, Request{Recipient: "operator"}, "say",
				fmt.Sprintf("%s's tmux restarted; %d tasks will resume", host, marked), s.now())
		}
		return nil
	})
	return marked, err
}

// hostLoss lists the attempts of host a read shows lost with its tmux
// server, none when it shows no host-wide loss. An attempt already marked
// belongs to a loss settled before.
func hostLoss(st *State, host string, read TmuxRead, saved int64, alive func(*State, Attempt) bool) []Attempt {
	if read.Known && (saved == 0 || read.Start == saved) {
		return nil
	}
	var gone []Attempt
	seen := false
	for _, at := range st.Attempts {
		if at.Host != host || at.State == "ended" || at.Window == "" || at.HostLost || st.Items[at.Item].Attempt != at.ID {
			continue
		}
		up := alive(st, at)
		switch {
		case up && !at.LiveAt.IsZero():
			return nil
		case !up:
			gone = append(gone, at)
		}
		seen = seen || !at.LiveAt.IsZero()
	}
	if !seen {
		return nil
	}
	return gone
}
