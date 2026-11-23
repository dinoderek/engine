package control

import "time"

// UpgradeMax bounds an upgrade whose end was never recorded, as when the
// upgrade itself was killed: it is taken to have ended this long after its
// start.
const UpgradeMax = time.Hour

// UpgradeLead is how long before an upgrade's start a window last seen live
// may still have died during it, the last pass before the server's stop
// having read its host that long before; and how long after its end a read
// may still first find the death, a host set up last being read once its
// setup is done. A death outside, as on a host offline across the upgrade,
// is no upgrade's.
const UpgradeLead = 10 * time.Minute

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
func (s Service) upgrade(tx *Tx, a Actor, r Request) (Response, error) {
	if err := operator(a); err != nil {
		return Response{}, err
	}
	switch r.State {
	case UpgradeStart:
		tx.SetUpgrade(UpgradeRecord{Start: s.now()})
	case UpgradeEnd:
		if u := tx.Upgrade(); !u.Start.IsZero() && u.End.IsZero() {
			u.End = s.now()
			tx.SetUpgrade(u)
		}
	default:
		return Response{}, problem(400, "upgrade takes state %q or %q", UpgradeStart, UpgradeEnd)
	}
	return Response{}, nil
}

// duringUpgrade reports whether at, whose window a read at now confirmed
// gone, may have died while an upgrade ran: it was last seen live, or
// started, between UpgradeLead before the upgrade's start and its end, and
// confirmed gone between its start and UpgradeLead after its end. A death on
// a host that could not be read during the upgrade, as a host on the old
// protocol is not, is confirmed only later; it still counts as during.
func duringUpgrade(tx *Tx, at Attempt, now time.Time) bool {
	u := tx.Upgrade()
	end := u.End
	if end.IsZero() {
		end = u.Start.Add(UpgradeMax)
	}
	if u.Start.IsZero() || now.Before(u.Start) || now.After(end.Add(UpgradeLead)) {
		return false
	}
	last := at.Started
	if at.LiveAt.After(last) {
		last = at.LiveAt
	}
	return !last.After(end) && !last.Before(u.Start.Add(-UpgradeLead))
}
