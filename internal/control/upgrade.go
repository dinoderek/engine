package control

import "time"

// UpgradeMax bounds an upgrade whose end was never recorded, as when the
// upgrade itself was killed: it is taken to have ended this long after its
// start.
const UpgradeMax = time.Hour

// UpgradeRecord is the newest `engine upgrade` run, as it marked its start
// and end on the server, by the server's clock. End is zero while it runs.
type UpgradeRecord struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end,omitzero"`
}

// The upgrade op's stages, in Request.State.
const (
	UpgradeStart = "start"
	UpgradeEnd   = "end"
)

// upgrade is the upgrade op, the operator's only: UpgradeStart begins a run,
// replacing the record of the last one, and UpgradeEnd ends the run that
// started, once.
func (s Service) upgrade(st *State, a Actor, r Request) (Response, error) {
	if err := operator(a); err != nil {
		return Response{}, err
	}
	switch r.State {
	case UpgradeStart:
		st.Upgrade = UpgradeRecord{Start: s.now()}
	case UpgradeEnd:
		if !st.Upgrade.Start.IsZero() && st.Upgrade.End.IsZero() {
			st.Upgrade.End = s.now()
		}
	default:
		return Response{}, problem(400, "upgrade takes state %q or %q", UpgradeStart, UpgradeEnd)
	}
	return Response{}, nil
}

// duringUpgrade reports whether at, whose window a read at now confirmed
// gone, may have died while an upgrade ran: it was last seen live before the
// upgrade's end, and confirmed gone after its start. A death on a host that
// could not be read during the upgrade, as a host on the old protocol is
// not, is confirmed only later; it still counts as during.
func duringUpgrade(st *State, at Attempt, now time.Time) bool {
	u := st.Upgrade
	if u.Start.IsZero() || now.Before(u.Start) {
		return false
	}
	end := u.End
	if end.IsZero() {
		end = u.Start.Add(UpgradeMax)
	}
	last := at.Started
	if at.LiveAt.After(last) {
		last = at.LiveAt
	}
	return !last.After(end)
}
