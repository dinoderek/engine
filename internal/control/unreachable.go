package control

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// UnreachableAsk is how long a host with tasks running there may stay
// unreachable before the coordinator asks the operator about it.
const UnreachableAsk = 15 * time.Minute

// The choices of an unreachable host's fleet question, the recommended one
// first, and what the question resolves as when its host answers first.
const (
	ChoiceWait         = "Wait, and resume when it returns"
	ChoiceMove         = "Move its tasks to other hosts"
	ChoiceStop         = "Stop its tasks"
	ResolutionReturned = "returned"
)

// coordinatorActor is the author of what the coordinator writes. Acting on
// the operator's answer, it carries the operator's role (actOnAnswer).
var coordinatorActor = Actor{Name: "coordinator", Role: "coordinator"}

// SawReach records whether a coordinator pass could read host, in one
// transaction with the host's fleet question:
//
//   - The first failed read of a stretch is saved. Once the host has been
//     unreachable for UnreachableAsk with tasks running there, the operator
//     gets one question about it, holding no task: wait, move its tasks or
//     stop them. No answer is a wait.
//   - An answer is run once: a move as `engine host move` does, a stop as
//     `engine task stop` does for each task. A resolution that is none of the
//     choices, like a wait, runs nothing.
//   - A read that succeeds ends the stretch. A question still open then
//     resolves as ResolutionReturned, and one note says what became of each
//     task it named, alive saying whether a task's window still runs.
func (s Service) SawReach(host string, reached bool, alive func(*Tx, Attempt) bool) error {
	return s.write(func(tx *Tx) error {
		if err := s.actOnAnswer(tx, host); err != nil {
			return err
		}
		if reached {
			s.reachedAgain(tx, host, alive)
		} else {
			s.unreachable(tx, host)
		}
		return nil
	})
}

// unreachable saves the start of host's stretch, and asks about it once it
// has lasted UnreachableAsk with tasks running there.
func (s Service) unreachable(tx *Tx, host string) {
	rec, now := tx.Host(host), s.now()
	if rec.UnreachableSince.IsZero() {
		rec.UnreachableSince = now
		tx.PutHost(host, rec)
		return
	}
	tasks := hostTasks(tx, host, "")
	if rec.Question > 0 || now.Sub(rec.UnreachableSince) < UnreachableAsk || len(tasks) == 0 {
		return
	}
	run := fmt.Sprintf("%d tasks run", len(tasks))
	if len(tasks) == 1 {
		run = "1 task runs"
	}
	body := fmt.Sprintf("%s has been unreachable since %s. %s there: %s.\n\n"+
		"Wait: each resumes, or ends by the vanish rules, when the host returns. "+
		"Move: `engine host move %s` pauses the host and resumes each task elsewhere from origin; its old session may still push. "+
		"Stop: `engine task stop` for each; each is held until `engine task resume`.",
		host, rec.UnreachableSince.Format("2006-01-02 15:04 MST"), run, strings.Join(tasks, ", "), host)
	recommended := 0
	u := tx.AppendUpdate(coordinatorActor, Request{Recipient: "operator", RequiresReply: true,
		Choices: []string{ChoiceWait, ChoiceMove, ChoiceStop}, Recommended: &recommended, Class: "environment"}, "ask", body, now)
	u.Host = host
	tx.PutUpdate(u)
	rec.Question, rec.Tasks, rec.Acted = u.ID, tasks, false
	tx.PutHost(host, rec)
}

// reachedAgain ends host's unreachable stretch, resolving a question still
// open about it as ResolutionReturned, with one note on its tasks.
func (s Service) reachedAgain(tx *Tx, host string, alive func(*Tx, Attempt) bool) {
	rec := tx.Host(host)
	if rec.UnreachableSince.IsZero() && rec.Question == 0 {
		return
	}
	if rec.Question > 0 && tx.Resolution(rec.Question) == 0 {
		tx.AppendUpdate(coordinatorActor, Request{ReplyTo: rec.Question}, "resolved", ResolutionReturned, s.now())
		s.hostNote(tx, host, returnedNote(tx, host, rec, alive))
	}
	rec.UnreachableSince, rec.Question, rec.Tasks, rec.Acted = time.Time{}, 0, nil, false
	tx.PutHost(host, rec)
}

// returnedNote says what became of each task host's question named, now
// that the host answers.
func returnedNote(tx *Tx, host string, rec HostRecord, alive func(*Tx, Attempt) bool) string {
	lines := []string{fmt.Sprintf("%s answers again, before question %d was answered.", host, rec.Question)}
	for _, id := range rec.Tasks {
		item := tx.item(id)
		at, ok := tx.Attempt(item.Attempt)
		what := "no longer runs there; it is " + item.State
		switch {
		case !ok || at.Host != host || at.State == "ended":
		case at.State == "dispatching":
			what = "its dispatch there is settled by the next pass"
		case alive(tx, at):
			what = "still runs there"
		default:
			what = "its session there is gone; it resumes, or ends, by the vanish rules"
		}
		lines = append(lines, id+": "+what+".")
	}
	return strings.Join(lines, "\n")
}

// actOnAnswer runs the operator's answer to host's fleet question once:
// the question's choice is the first line of its resolution.
func (s Service) actOnAnswer(tx *Tx, host string) error {
	rec := tx.Host(host)
	if rec.Question == 0 || rec.Acted {
		return nil
	}
	by := tx.Resolution(rec.Question)
	if by == 0 {
		return nil
	}
	rec.Acted = true
	tx.PutHost(host, rec)
	answer, _, _ := strings.Cut(tx.update(by).Body, "\n")
	// The operator's answer is their go-ahead, confirming a move off a host
	// the coordinator could not read.
	as := Actor{Name: coordinatorActor.Name, Role: "operator"}
	var moved, left []string
	switch answer {
	case ChoiceMove:
		out, err := s.moveHost(tx, as, Request{Op: "host-move", ID: host, Move: &MoveRequest{Confirm: true}})
		if err != nil {
			return err
		}
		moved, left = out.Host.Moved, out.Host.Left
	case ChoiceStop:
		var err error
		if moved, left, err = s.stopHost(tx, as, host); err != nil {
			return err
		}
	default:
		return nil
	}
	s.hostNote(tx, host, answerNote(host, answer, by, moved, left))
	return nil
}

// hostNote tells the operator body about host, with no item.
func (s Service) hostNote(tx *Tx, host, body string) {
	u := tx.AppendUpdate(coordinatorActor, Request{Recipient: "operator"}, "say", body, s.now())
	u.Host = host
	tx.PutUpdate(u)
}

// stopHost stops every task running on host, as the operator's task stop
// does, returning those stopped and those whose dispatch was still in flight.
func (s Service) stopHost(tx *Tx, a Actor, host string) (stopped, left []string, err error) {
	for _, id := range hostTasks(tx, host, "") {
		_, err := s.stopTask(tx, a, Request{Op: "task-stop", ID: id})
		var p *Problem
		switch {
		case err == nil:
			stopped = append(stopped, id)
		case errors.As(err, &p) && p.Status == 409:
			left = append(left, id)
		default:
			return nil, nil, err
		}
	}
	return stopped, left, nil
}

// answerNote tells the operator what their answer to host's question did.
func answerNote(host, answer string, by int, done, left []string) string {
	verb, after := "moved", fmt.Sprintf(" %s is paused until `engine host resume %s`.", host, host)
	if answer == ChoiceStop {
		verb, after = "stopped", " Each is held until `engine task resume`."
	}
	text := fmt.Sprintf("As answered in update %d, the coordinator %s no task on %s.", by, verb, host)
	if len(done) > 0 {
		text = fmt.Sprintf("As answered in update %d, the coordinator %s %s on %s.", by, verb, strings.Join(done, ", "), host)
	}
	text += after
	if len(left) > 0 {
		text += fmt.Sprintf(" Left, their dispatch still in flight: %s; repeat once they start.", strings.Join(left, ", "))
	}
	return text
}
