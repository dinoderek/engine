package control

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// The first choice of a limit question, and what the coordinator resolves
// one as when no session it covers is limited any more, or when a question
// covering more sessions replaces it.
const (
	ChoiceWaitReset      = "Wait for the reset"
	ResolutionNoLimit    = "no longer limited"
	ResolutionSuperseded = "superseded"
)

// LimitRead is what one coordinator pass read of the live task sessions at
// a usage limit on one host × harness (reconcile.LimitChoices).
type LimitRead struct {
	Host, Harness string
	// Tasks are the limited tasks there, by number.
	Tasks []string
	// ResetsAt is the latest of their resets in epoch seconds, zero when
	// any is unknown.
	ResetsAt int64
	// Room is set when a move has somewhere to go: for one task, the
	// MoveHost × MoveHarness it would resume on.
	Room                  bool
	MoveHost, MoveHarness string
	// Wait is set when waiting for the reset is the recommended answer.
	Wait bool
}

// LimitAsk is the fleet question about the task sessions at a usage limit
// on one host × harness, kept on the host's record by harness.
type LimitAsk struct {
	Question int `json:"question"`
	// Tasks are the limited tasks it covers, as the last read saw them.
	Tasks []string `json:"tasks,omitempty"`
	// Group is set for a question covering several, whose move is the
	// host's move of that harness's tasks; else the move is MoveHost ×
	// MoveHarness for its one task.
	Group       bool   `json:"group,omitempty"`
	MoveHost    string `json:"move_host,omitempty"`
	MoveHarness string `json:"move_harness,omitempty"`
	// Acted is set once the coordinator has run the answer.
	Acted bool `json:"acted,omitempty"`
}

// SawLimits records one successful read of host's task sessions at a usage
// limit, reads holding one per harness with any, in one transaction:
//
//   - A limited session no question covers, with room elsewhere, gets one,
//     holding no task: wait for the reset, or move. Two or more on one
//     harness get one question, whose move is `engine host move <host>
//     --harness <h>`. No answer is a wait: the session resumes on its reset.
//   - A session limited while a question covering several is open joins it.
//     One limited while a question about one session is open replaces it
//     with a question covering both, resolving the first as superseded.
//   - A question none of whose sessions is limited any more resolves as
//     ResolutionNoLimit.
//   - An answer is run once: a move as `engine task move` or `engine host
//     move` does. A resolution that is not the move, like a wait, runs
//     nothing.
func (s Service) SawLimits(host string, reads []LimitRead) error {
	return s.Store.Transaction(func(st *State) error {
		rec := st.Hosts[host]
		harnesses := slices.Sorted(maps.Keys(rec.Limits))
		for _, harness := range harnesses {
			if err := s.actOnLimit(st, host, harness); err != nil {
				return err
			}
		}
		for _, r := range reads {
			if !slices.Contains(harnesses, r.Harness) {
				harnesses = append(harnesses, r.Harness)
			}
		}
		for _, harness := range harnesses {
			i := slices.IndexFunc(reads, func(r LimitRead) bool { return r.Harness == harness })
			read := LimitRead{Host: host, Harness: harness}
			if i >= 0 {
				read = reads[i]
			}
			s.limitStretch(st, host, read)
		}
		return nil
	})
}

// limitStretch keeps host × harness's limit question to what read shows.
func (s Service) limitStretch(st *State, host string, read LimitRead) {
	rec := st.Hosts[host]
	ask := rec.Limits[read.Harness]
	open := ask.Question > 0 && !resolved(st, ask.Question)
	ask.Tasks = slices.DeleteFunc(ask.Tasks, func(id string) bool { return !slices.Contains(read.Tasks, id) })
	if open && len(ask.Tasks) == 0 {
		st.update(coordinatorActor, Request{ReplyTo: ask.Question}, "resolved", ResolutionNoLimit, s.now())
		open = false
	}
	covered := !slices.ContainsFunc(read.Tasks, func(id string) bool { return !slices.Contains(ask.Tasks, id) })
	switch {
	case len(read.Tasks) == 0:
		delete(rec.Limits, read.Harness)
	case covered || !read.Room:
		if ask.Question > 0 {
			rec.Limits = setLimit(rec.Limits, read.Harness, ask)
		}
	case open && ask.Group:
		ask.Tasks = slices.Clone(read.Tasks)
		rec.Limits = setLimit(rec.Limits, read.Harness, ask)
	default:
		next := s.askLimit(st, read)
		if open {
			st.update(coordinatorActor, Request{ReplyTo: ask.Question}, "resolved",
				fmt.Sprintf("%s\nby question %d, which covers %s", ResolutionSuperseded, next.Question, strings.Join(read.Tasks, ", ")), s.now())
		}
		rec.Limits = setLimit(rec.Limits, read.Harness, next)
	}
	if len(rec.Limits) == 0 {
		rec.Limits = nil
	}
	st.Hosts[host] = rec
}

// setLimit is limits with harness's question set to ask.
func setLimit(limits map[string]LimitAsk, harness string, ask LimitAsk) map[string]LimitAsk {
	if limits == nil {
		limits = map[string]LimitAsk{}
	}
	limits[harness] = ask
	return limits
}

// askLimit asks the operator about read's limited sessions, which have room
// elsewhere.
func (s Service) askLimit(st *State, read LimitRead) LimitAsk {
	ask := LimitAsk{Tasks: slices.Clone(read.Tasks), Group: len(read.Tasks) > 1}
	until := "an unknown reset"
	if read.ResetsAt > 0 {
		until = time.Unix(read.ResetsAt, 0).UTC().Format("2006-01-02 15:04 MST")
	}
	var body, move string
	if ask.Group {
		move = "Move them to other hosts"
		body = fmt.Sprintf("%s are at %s's limit on %s until %s.\n\n"+
			"Wait: each resumes on its own after the reset, as with no answer. "+
			"Move: `engine host move %s --harness %s` pauses %s and resumes each of its %s tasks on another host now, from what its branch has on origin.",
			strings.Join(read.Tasks, ", "), read.Harness, read.Host, until, read.Host, read.Harness, read.Host, read.Harness)
	} else {
		ask.MoveHost, ask.MoveHarness = read.MoveHost, read.MoveHarness
		move = fmt.Sprintf("Move to %s on %s", read.MoveHarness, read.MoveHost)
		from := "keeping its worktree"
		if read.MoveHost != read.Host {
			from = "from what its branch has on origin, once its window here pushes it"
		}
		body = fmt.Sprintf("%s is at %s's limit on %s until %s.\n\n"+
			"Wait: it resumes on its own after the reset, as with no answer. "+
			"Move: `engine task move %s --harness %s --host %s` resumes it there now, %s.",
			read.Tasks[0], read.Harness, read.Host, until, read.Tasks[0], read.MoveHarness, read.MoveHost, from)
	}
	recommended := 1
	if read.Wait {
		recommended = 0
	}
	u := st.update(coordinatorActor, Request{Recipient: "operator", RequiresReply: true,
		Choices: []string{ChoiceWaitReset, move}, Recommended: &recommended, Class: "environment"}, "ask", body, s.now())
	u.Host = read.Host
	st.Updates[u.ID-1] = u
	ask.Question = u.ID
	return ask
}

// actOnLimit runs the operator's answer to host × harness's limit question
// once: the question's move is its second choice, and the answer the first
// line of its resolution.
func (s Service) actOnLimit(st *State, host, harness string) error {
	rec := st.Hosts[host]
	ask := rec.Limits[harness]
	by := resolution(st, ask.Question)
	if ask.Acted || by == 0 || len(ask.Tasks) == 0 {
		return nil
	}
	ask.Acted = true
	rec.Limits[harness] = ask
	st.Hosts[host] = rec
	q := st.Updates[ask.Question-1]
	answer, _, _ := strings.Cut(st.Updates[by-1].Body, "\n")
	if len(q.Choices) < 2 || answer != q.Choices[1] {
		return nil
	}
	// The operator's answer is their go-ahead, confirming a move off a host
	// the coordinator could not read since.
	as := Actor{Name: coordinatorActor.Name, Role: "operator"}
	var note string
	if ask.Group {
		out, err := s.moveHost(st, as, Request{Op: "host-move", ID: host, Move: &MoveRequest{Harness: harness, Confirm: true}})
		if note, err = refusedNote(err, by, "move the "+harness+" tasks of "+host); err != nil {
			return err
		}
		if note == "" {
			note = answerNote(host, ChoiceMove, by, out.Host.Moved, out.Host.Left)
		}
	} else {
		id := ask.Tasks[0]
		_, err := s.moveTask(st, as, Request{Op: "task-move", ID: id, Move: &MoveRequest{Host: ask.MoveHost, Harness: ask.MoveHarness, Confirm: true}})
		if note, err = refusedNote(err, by, "move "+id); err != nil {
			return err
		}
		if note == "" {
			note = fmt.Sprintf("As answered in update %d, the coordinator moved %s to %s on %s.", by, id, ask.MoveHarness, ask.MoveHost)
		}
	}
	s.hostNote(st, host, note)
	return nil
}

// refusedNote tells the operator that the answer in update by could not
// do what, when the server refused it; another error is returned.
func refusedNote(err error, by int, what string) (string, error) {
	var p *Problem
	switch {
	case err == nil:
		return "", nil
	case errors.As(err, &p):
		return fmt.Sprintf("As answered in update %d, the coordinator tried to %s, but the server refused: %s.", by, what, p.Message), nil
	}
	return "", err
}
