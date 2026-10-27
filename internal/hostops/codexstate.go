package hostops

import "github.com/dinoderek/engine/internal/session"

// probeCodex prefers an explicit hook limit, otherwise deriving one only
// from a waiting session and exhausted live quota. Working always wins;
// an unreadable or ended session remains unknown while its window lives.
func (s *Status) probeCodex(w *Window, dir string) {
	st, ok := session.ReadHarnessState(dir)
	if !ok || st.State == session.StateEnded {
		return
	}
	w.State = st.State
	if st.State == session.StateLimited {
		w.ResetsAt = st.ResetsAt
		return
	}
	if st.State != session.StateWaiting {
		return
	}
	for _, q := range s.Quota[Codex].Windows {
		if q.Used >= 100 {
			w.State = session.StateLimited
			w.ResetsAt = max(w.ResetsAt, q.ResetsAt)
		}
	}
}
