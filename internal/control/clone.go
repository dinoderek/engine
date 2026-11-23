package control

import (
	"maps"
	"slices"
)

// clone copies the state deeply: nothing a caller does to the copy reaches
// the original. Strings are shared; they never change.
func (s State) clone() State {
	out := State{
		Items:    make(map[string]Item, len(s.Items)),
		Attempts: maps.Clone(s.Attempts),
		Updates:  cloneUpdates(s.Updates),
		Queue:    maps.Clone(s.Queue),
		Auth:     maps.Clone(s.Auth),
		Hosts:    make(map[string]HostRecord, len(s.Hosts)),
		Receipts: make(map[string]receipt, len(s.Receipts)),
		Next:     s.Next,
		PassAt:   s.PassAt,
		Upgrade:  s.Upgrade,
	}
	for k, v := range s.Items {
		out.Items[k] = v.clone()
	}
	for k, v := range s.Hosts {
		v.Tasks = slices.Clone(v.Tasks)
		v.Limits = maps.Clone(v.Limits)
		for harness, ask := range v.Limits {
			ask.Tasks = slices.Clone(ask.Tasks)
			v.Limits[harness] = ask
		}
		out.Hosts[k] = v
	}
	for k, v := range s.Receipts {
		v.Response = v.Response.clone()
		out.Receipts[k] = v
	}
	return out
}

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func (i Item) clone() Item {
	i.Depends = slices.Clone(i.Depends)
	i.Requires = slices.Clone(i.Requires)
	i.Question = i.Question.clone()
	return i
}

func cloneItem(p *Item) *Item {
	if p == nil {
		return nil
	}
	v := p.clone()
	return &v
}

func (r *UpdateRef) clone() *UpdateRef {
	r = clonePtr(r)
	if r != nil {
		r.Choices = slices.Clone(r.Choices)
		r.Recommended = clonePtr(r.Recommended)
	}
	return r
}

func (u Update) clone() Update {
	u.Snapshot = cloneItem(u.Snapshot)
	u.Merge = clonePtr(u.Merge)
	u.Choices = slices.Clone(u.Choices)
	u.Recommended = clonePtr(u.Recommended)
	return u
}

func cloneUpdates(in []Update) []Update {
	if in == nil {
		return nil
	}
	out := make([]Update, len(in))
	for i, u := range in {
		out[i] = u.clone()
	}
	return out
}

func (r Response) clone() Response {
	r.Item = cloneItem(r.Item)
	if r.Items != nil {
		items := make([]Item, len(r.Items))
		for i, v := range r.Items {
			items[i] = v.clone()
		}
		r.Items = items
	}
	r.Updates = cloneUpdates(r.Updates)
	r.Attempt = clonePtr(r.Attempt)
	r.Host = r.Host.clone()
	r.Attempts = slices.Clone(r.Attempts)
	r.Queue = slices.Clone(r.Queue)
	if r.Sessions != nil {
		r.Sessions = slices.Clone(r.Sessions)
		for i := range r.Sessions {
			r.Sessions[i].Feedback = r.Sessions[i].Feedback.clone()
			r.Sessions[i].Latest = r.Sessions[i].Latest.clone()
		}
	}
	r.Fleet = r.Fleet.clone()
	return r
}

func (h *HostStatus) clone() *HostStatus {
	h = clonePtr(h)
	if h == nil || h.Hosts == nil {
		return h
	}
	h.Hosts = slices.Clone(h.Hosts)
	for i, row := range h.Hosts {
		row.SignedOut = slices.Clone(row.SignedOut)
		row.Windows = slices.Clone(row.Windows)
		row.Quota = slices.Clone(row.Quota)
		row.Readiness = slices.Clone(row.Readiness)
		h.Hosts[i] = row
	}
	return h
}

func (h *HostChange) clone() *HostChange {
	h = clonePtr(h)
	if h != nil {
		h.Moved = slices.Clone(h.Moved)
		h.Left = slices.Clone(h.Left)
	}
	return h
}
