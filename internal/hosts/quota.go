package hosts

// QuotaWindows are the usage windows a quota threshold may name: a
// harness account's five-hour and weekly limits.
var QuotaWindows = []string{"five_hour", "seven_day"}

// DefaultQuotaThresholds is the share of each window, in percent, at which
// placement prefers another harness.
func DefaultQuotaThresholds() map[string]int {
	return map[string]int{"five_hour": 90, "seven_day": 95}
}

// thresholds reads quota_threshold: each window it names replaces that
// window's default.
func (v *validator) thresholds(r *reader) map[string]int {
	out := DefaultQuotaThresholds()
	n := r.table("quota_threshold", false)
	if n == nil {
		return out
	}
	rr := v.reader(n, "quota_threshold")
	for _, window := range QuotaWindows {
		pct, ok := rr.number(window, false, 1)
		if !ok {
			continue
		}
		if pct > 100 {
			v.fail(n.entries[window].pos, "quota_threshold: %s must be at most 100", window)
		}
		out[window] = pct
	}
	rr.finish()
	return out
}
