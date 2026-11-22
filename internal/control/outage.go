package control

import "time"

// StartPass saves now as the start of a coordinator pass and returns the
// start of the pass before it, zero when there was none. When that start is
// more than gap before now, the server was unavailable between them: the
// same transaction clears the wait and idle starts of every attempt not yet
// ended, so their stalls count from now.
func (s *Store) StartPass(now time.Time, gap time.Duration) (prev time.Time, outage bool, err error) {
	err = s.Transaction(func(st *State) error {
		prev, outage = st.PassStart, !st.PassStart.IsZero() && now.Sub(st.PassStart) > gap
		st.PassStart = now
		if !outage {
			return nil
		}
		for id, at := range st.Attempts {
			if at.State != "ended" {
				at.WaitingSince, at.IdleSince = time.Time{}, time.Time{}
				st.Attempts[id] = at
			}
		}
		return nil
	})
	return prev, outage, err
}
