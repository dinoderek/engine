package control

import "time"

// StartPass saves now as the start of a coordinator pass and returns the
// time the coordinator was last known to run, zero when there was none. When
// that is more than gap before now, the server was unavailable between them: the
// same transaction clears the wait and idle starts of every attempt not yet
// ended, so their stalls count from now, and the start of each host's
// unreachable stretch not yet asked about.
func (s *Store) StartPass(now time.Time, gap time.Duration) (prev time.Time, outage bool, err error) {
	err = s.Transaction(func(st *State) error {
		prev, outage = st.PassAt, !st.PassAt.IsZero() && now.Sub(st.PassAt) > gap
		st.PassAt = now
		if !outage {
			return nil
		}
		for id, at := range st.Attempts {
			if at.State != "ended" {
				at.WaitingSince, at.IdleSince = time.Time{}, time.Time{}
				st.Attempts[id] = at
			}
		}
		// Nobody read the hosts meanwhile: an unreachable stretch not yet
		// asked about starts again at its next failed read.
		for name, rec := range st.Hosts {
			if !rec.UnreachableSince.IsZero() && rec.Question == 0 {
				rec.UnreachableSince = time.Time{}
				st.Hosts[name] = rec
			}
		}
		return nil
	})
	return prev, outage, err
}

// EndPass saves now as the end of a coordinator pass, so a pass that ran
// long, its host calls slow, does not leave a gap behind it.
func (s *Store) EndPass(now time.Time) error {
	return s.Transaction(func(st *State) error {
		if now.After(st.PassAt) {
			st.PassAt = now
		}
		return nil
	})
}
