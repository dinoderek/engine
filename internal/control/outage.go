package control

import (
	"context"
	"time"
)

// StartPass saves now as the start of a coordinator pass and returns the
// time the coordinator was last known to run, zero when there was none. When
// that is more than gap before now, the server was unavailable between them: the
// same transaction clears the wait and idle starts of every attempt not yet
// ended, so their stalls count from now, and the start of each host's
// unreachable stretch not yet asked about.
func (s *Store) StartPass(now time.Time, gap time.Duration) (prev time.Time, outage bool, err error) {
	err = s.Write(context.Background(), func(tx *Tx) error {
		prev = tx.PassAt()
		outage = !prev.IsZero() && now.Sub(prev) > gap
		tx.SetPassAt(now)
		if !outage {
			return nil
		}
		for _, at := range tx.UnendedAttempts() {
			at.WaitingSince, at.IdleSince = time.Time{}, time.Time{}
			tx.PutAttempt(at)
		}
		// Nobody read the hosts meanwhile: an unreachable stretch not yet
		// asked about starts again at its next failed read.
		for name, rec := range tx.HostRecords() {
			if !rec.UnreachableSince.IsZero() && rec.Question == 0 {
				rec.UnreachableSince = time.Time{}
				tx.PutHost(name, rec)
			}
		}
		return nil
	})
	return prev, outage, err
}

// EndPass saves now as the end of a coordinator pass, so a pass that ran
// long, its host calls slow, does not leave a gap behind it.
func (s *Store) EndPass(now time.Time) error {
	return s.Write(context.Background(), func(tx *Tx) error {
		if now.After(tx.PassAt()) {
			tx.SetPassAt(now)
		}
		return nil
	})
}
