package control

import "time"

const (
	// receiptRetention is how long a request key's stored response replays.
	receiptRetention = 7 * 24 * time.Hour
	// receiptSweepLimit caps the receipts one write deletes, so a backlog
	// drains across writes and none runs long.
	receiptSweepLimit = 100
)

// deleteExpiredReceipts deletes the oldest receipts written before its first
// argument, up to its second, keeping a dispatch receipt of an unended attempt.
const deleteExpiredReceipts = `DELETE FROM receipts WHERE key IN (
 SELECT r.key FROM receipts r WHERE r.created<? AND (r.attempt='' OR NOT EXISTS (
  SELECT 1 FROM attempts a WHERE a.id=r.attempt AND a.state<>'ended'))
 ORDER BY r.created LIMIT ?)`

// DeleteExpiredReceipts deletes up to limit receipts written before cutoff,
// oldest first, and returns how many it deleted. A dispatch receipt whose
// attempt has not ended stays, since an interrupted dispatch recovers from
// its token. It reads the created index from its oldest end, never every
// receipt.
func (t *Tx) DeleteExpiredReceipts(cutoff time.Time, limit int) int {
	t.mustWrite()
	if t.err != nil {
		return 0
	}
	res, err := t.q.ExecContext(t.ctx, deleteExpiredReceipts, cutoff.UTC().Format(createdLayout), limit)
	t.fail(err)
	if err != nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return int(n)
}

// sweepReceipts is the retention every write runs before it looks up its
// request key, so a key whose receipt has expired is a new request.
func (s Service) sweepReceipts(tx *Tx) {
	tx.DeleteExpiredReceipts(s.now().Add(-receiptRetention), receiptSweepLimit)
}
