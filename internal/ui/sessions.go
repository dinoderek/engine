package ui

import (
	"context"
	"slices"

	"github.com/dinoderek/engine/internal/control"
)

// SessionList is the Sessions tab: active planner and executor attempts with
// the ones awaiting the operator first.
type SessionList struct {
	Sessions []control.SessionRow `json:"sessions"`
	// Feedback counts the sessions with an unresolved question.
	Feedback int `json:"feedback"`
}

// sessionList orders rows for the operator: sessions with an unresolved
// question first, the oldest request leading, then the rest by item number.
func sessionList(rows []control.SessionRow) SessionList {
	out := SessionList{Sessions: slices.Clone(rows)}
	if out.Sessions == nil {
		out.Sessions = []control.SessionRow{}
	}
	for _, r := range out.Sessions {
		if r.Feedback != nil {
			out.Feedback++
		}
	}
	slices.SortStableFunc(out.Sessions, func(a, b control.SessionRow) int {
		switch {
		case a.Feedback != nil && b.Feedback == nil:
			return -1
		case a.Feedback == nil && b.Feedback != nil:
			return 1
		case a.Feedback != nil && a.Feedback.Update != b.Feedback.Update:
			return a.Feedback.Update - b.Feedback.Update
		}
		return control.Number(a.Item) - control.Number(b.Item)
	})
	return out
}

// feedbackByTask maps each session's item to the unresolved question that
// applies to it, its own or its plan's.
func feedbackByTask(rows []control.SessionRow) map[string]*control.UpdateRef {
	out := map[string]*control.UpdateRef{}
	for _, r := range rows {
		if r.Feedback != nil {
			out[r.Item] = r.Feedback
		}
	}
	return out
}

// sessions loads the Sessions tab. Like every view it falls back to its last
// success when the control server fails.
func (u *upstream) sessions(ctx context.Context) (Envelope, int) {
	return u.view(ctx, "sessions", func(ctx context.Context) (any, error) {
		got, err := u.call(ctx, control.Request{Op: "session-view"})
		if err != nil {
			return nil, err
		}
		return sessionList(got.Sessions), nil
	})
}
