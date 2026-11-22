package reconcile

// nearFull reports whether the harness's account has used at least its
// threshold of any usage window. The reading is the newest from any host,
// since every host signs in to the same account, and a window past its
// reset counts as empty until a new reading arrives. An absent window, or a
// harness never read, is not near full.
func (f Fleet) nearFull(harness string) bool {
	var observed int64
	near := false
	for _, h := range f.Hosts {
		q, ok := h.Status.Quota[harness]
		if !ok || q.ObservedAt <= observed {
			continue
		}
		observed, near = q.ObservedAt, false
		for window, w := range q.Windows {
			limit, ok := f.QuotaThresholds[window]
			if ok && f.Now.Unix() < w.ResetsAt && w.Used >= float64(limit) {
				near = true
			}
		}
	}
	return near
}

// preferRoom moves near-full harnesses after the rest, keeping each group's
// order. When every harness is near full, the order stands.
func (f Fleet) preferRoom(names []string) []string {
	var room, full []string
	for _, name := range names {
		if f.nearFull(name) {
			full = append(full, name)
		} else {
			room = append(room, name)
		}
	}
	if len(room) == 0 {
		return names
	}
	return append(room, full...)
}
