package hostops

import "github.com/dinoderek/engine/internal/session"

// probeCodex supplements hook state with a newer failed usage-limit turn.
// Without that failure, only waiting plus exhausted quota derives a limit.
func (s *Status) probeCodex(w *Window, r session.Run) {
	st, ok := session.ReadHarnessState(r.Dir)
	if !ok || st.State == session.StateEnded {
		return
	}
	w.State = st.State
	if st.State == session.StateLimited {
		w.ResetsAt = st.ResetsAt
		return
	}
	failed := codexStateLimited(s.codexTurns[st.SessionID], st, r.Record.Spawned)
	if failed {
		w.State = session.StateLimited
	}
	if st.State != session.StateWaiting && !failed {
		return
	}
	for _, q := range s.Quota[Codex].Windows {
		if q.Used >= 100 {
			w.State = session.StateLimited
			w.ResetsAt = max(w.ResetsAt, q.ResetsAt)
		}
	}
}
