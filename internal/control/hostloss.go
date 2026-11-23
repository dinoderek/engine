package control

import (
	"fmt"
	"time"
)

// HostRecord is what the coordinator saved of one host.
type HostRecord struct {
	// Paused is the operator's pause: placement starts nothing new on the
	// host while it is set (hostpause.go). PausedAt is when it was set.
	Paused   bool      `json:"paused,omitempty"`
	PausedAt time.Time `json:"paused_at,omitzero"`
	// TmuxStart is when the host's session tmux server started, in epoch
	// seconds, as the last successful read that found a server saw it.
	TmuxStart int64 `json:"tmux_start,omitempty"`
	// UnreachableSince is the first failed read of the host's current
	// unreachable stretch; zero while it answers (unreachable.go).
	UnreachableSince time.Time `json:"unreachable_since,omitzero"`
	// Question is the fleet question asked about that stretch, at most one,
	// and Tasks the tasks it named. Acted is set once the coordinator has run
	// the operator's answer to it.
	Question int      `json:"question,omitempty"`
	Tasks    []string `json:"tasks,omitempty"`
	Acted    bool     `json:"acted,omitempty"`
	// Limits are the fleet questions about the host's task sessions at a
	// usage limit, by harness (limitask.go).
	Limits map[string]LimitAsk `json:"limits,omitempty"`
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
func (s Service) SawTmux(host string, read TmuxRead, alive func(*Tx, Attempt) bool) (int, error) {
	marked := 0
	err := s.write(func(tx *Tx) error {
		saved := tx.Host(host).TmuxStart
		for _, at := range hostLoss(tx, host, read, saved, alive) {
			at.HostLost = true
			tx.PutAttempt(at)
			marked++
		}
		if read.Known && read.Start != saved {
			rec := tx.Host(host)
			rec.TmuxStart = read.Start
			tx.PutHost(host, rec)
		}
		if marked > 0 {
			tx.AppendUpdate(Actor{Name: "coordinator", Role: "coordinator"}, Request{Recipient: "operator"}, "say",
				fmt.Sprintf("%s's tmux restarted; %d tasks will resume", host, marked), s.now())
		}
		return nil
	})
	return marked, err
}

// hostLoss lists the attempts of host a read shows lost with its tmux
// server, none when it shows no host-wide loss. An attempt already marked
// belongs to a loss settled before, and a dispatch still in flight has no
// window to lose.
func hostLoss(tx *Tx, host string, read TmuxRead, saved int64, alive func(*Tx, Attempt) bool) []Attempt {
	if read.Known && (saved == 0 || read.Start == saved) {
		return nil
	}
	var gone []Attempt
	seen := false
	for _, at := range tx.AttemptsOnHost(host) {
		if at.State == "ended" || at.State == "dispatching" || at.Window == "" || at.HostLost || tx.item(at.Item).Attempt != at.ID {
			continue
		}
		up := alive(tx, at)
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
