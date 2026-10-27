package run

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/session"
)

func (w *world) Resume(s Session) error {
	w.resumed = append(w.resumed, s)
	return w.fails("resume")
}

func limitedWorld(t *testing.T, waiting bool) *world {
	t.Helper()
	w := onDevbox(t, behaviour{})
	w.card(ref(2)).Labels = []string{Running}
	e := w.execs[ref(2)]
	e.status, e.limited, e.resetsAt = session.AgentWaiting, !waiting, t0.Add(3*time.Minute).Unix()
	return w
}

func TestResumeOncePerReset(t *testing.T) {
	for _, waiting := range []bool{false, true} {
		t.Run(map[bool]string{false: "limited", true: "waiting with limit"}[waiting], func(t *testing.T) {
			w := limitedWorld(t, waiting)
			checks(t, w, 4)
			if len(w.resumed) != 0 {
				t.Fatal("typed before reset plus delay")
			}
			v := gather(t, w)
			w.sleep(CheckEvery)
			if !passDue(w, v, outages{}) {
				t.Fatal("due reset did not wake the session check")
			}
			passOf(t, w)
			checks(t, w, 3)
			if len(w.resumed) != 1 || w.resumed[0].Host != "devbox" {
				t.Fatalf("resumed %+v", w.resumed)
			}
			s := gather(t, w).Sessions[ref(2)]
			if !s.ResumeAt.Equal(t0.Add(5*time.Minute)) || !noteOf(t, w, 2, resetNote("resumed-for", s)) {
				t.Fatalf("resume note: %+v", s)
			}
			// A restart reconstructs state from host status and the same notes.
			passOf(t, w)
			if len(w.resumed) != 1 {
				t.Fatal("restart repeated continue")
			}
			w.execs[ref(2)].resetsAt = w.now.Add(time.Minute).Unix()
			checks(t, w, 2)
			if len(w.resumed) != 1 {
				t.Fatal("new reset resumed early")
			}
			checks(t, w, 1)
			if len(w.resumed) != 2 {
				t.Fatalf("new reset: resumed %+v", w.resumed)
			}
		})
	}
}

func TestResumeStuckAlertOnce(t *testing.T) {
	w := limitedWorld(t, false)
	checks(t, w, 5)
	checks(t, w, 9)
	if len(w.comments[ref(2)]) != 0 {
		t.Fatal("alert before ten minutes")
	}
	v := gather(t, w)
	w.sleep(CheckEvery)
	if !passDue(w, v, outages{}) {
		t.Fatal("stuck limit did not wake session check")
	}
	passOf(t, w)
	checks(t, w, 6)
	c := w.comments[ref(2)]
	if len(c) != 1 || !strings.Contains(c[0], "@op ") || !strings.Contains(c[0], "still limited 10 minutes") || !strings.Contains(c[0], "Do you want to proceed?") {
		t.Fatalf("comments %q", c)
	}
	if len(w.resumed) != 1 {
		t.Fatalf("resumed %v", w.resumed)
	}
}

func TestFarResetAlertOnceThenResumeWhenDue(t *testing.T) {
	w := limitedWorld(t, false)
	w.execs[ref(2)].resetsAt = t0.Add(25 * time.Hour).Unix()
	passOf(t, w)
	checks(t, w, 10)
	c := w.comments[ref(2)]
	if len(w.resumed) != 0 || len(c) != 1 || !strings.Contains(c[0], "@op ") || !strings.Contains(c[0], "more than 24 hours") {
		t.Fatalf("resumed %v, comments %q", w.resumed, c)
	}
	// Sleep and a restart do not rely on a timer having fired.
	w.now = t0.Add(25*time.Hour + resumeDelay)
	passOf(t, w)
	if len(w.resumed) != 1 {
		t.Fatalf("due after sleep: %v", w.resumed)
	}
}

func TestResumeLeavesOtherSessionsAlone(t *testing.T) {
	for _, state := range []string{"working", "unknown state", "host down", "dead", "closed", "no record", "unknown reset"} {
		t.Run(state, func(t *testing.T) {
			w := limitedWorld(t, false)
			e := w.execs[ref(2)]
			switch state {
			case "working":
				e.limited = false
				e.status = session.AgentBusy
			case "unknown state":
				e.limited = false
				e.status = ""
			case "host down":
				w.hosts[1].down = "cannot read host"
			case "dead":
				e.window.Dead = true
			case "closed":
				w.card(ref(2)).Open = false
			case "no record":
				if err := os.Remove(filepath.Join(e.dir, session.RecordFile)); err != nil {
					t.Fatal(err)
				}
			case "unknown reset":
				e.resetsAt = 0
			}
			w.now = t0.Add(6 * time.Minute)
			passOf(t, w)
			if len(w.resumed) != 0 {
				t.Fatalf("resumed %v", w.resumed)
			}
		})
	}
}

func TestResumePreclaimAndPlanner(t *testing.T) {
	w := limitedWorld(t, false)
	w.card(ref(2)).Labels = nil
	checks(t, w, 5)
	if len(w.resumed) != 1 || len(w.comments[ref(2)]) != 0 {
		t.Fatalf("preclaim: %v %v", w.resumed, w.comments)
	}
	w = newWorld(t, []string{Approved})
	passOf(t, w)
	e := w.execs[ref(1)]
	e.limited, e.resetsAt = true, t0.Unix()
	// Do not run the fake planner's completion script.
	w.now = t0.Add(resumeDelay)
	passOf(t, w)
	if len(w.resumed) != 1 || w.resumed[0].Name != "r-1" {
		t.Fatalf("planner: %+v", w.resumed)
	}
}

func TestResumeFailureDoesNotPasteTwice(t *testing.T) {
	w := limitedWorld(t, false)
	w.failNext = map[string]error{"resume": errors.New("Enter failed")}
	checks(t, w, 16)
	if len(w.resumed) != 1 || len(w.comments[ref(2)]) != 1 || !strings.Contains(strings.Join(w.log, "\n"), "Enter failed") {
		t.Fatalf("resumed %v, comments %v, log %v", w.resumed, w.comments, w.log)
	}
}

func TestResumeNotesBelongToTheSession(t *testing.T) {
	w := limitedWorld(t, false)
	checks(t, w, 5)
	s := gather(t, w).Sessions[ref(2)]
	dir, _ := w.NoteDir(ref(2))
	s.Spawned = w.now.Add(time.Second)
	if err := readResumeNotes(dir, &s); err != nil {
		t.Fatal(err)
	}
	// Reading a new session starts with empty state, as newestRun does.
	s.ResumeAt = time.Time{}
	if err := readResumeNotes(dir, &s); err != nil || !s.ResumeAt.IsZero() {
		t.Fatalf("old session note: %+v %v", s, err)
	}
	s.Spawned = w.now.Add(100 * time.Millisecond)
	if err := readResumeNotes(dir, &s); err != nil || s.ResumeAt.IsZero() {
		t.Fatalf("whole-second note: %+v %v", s, err)
	}
}

func TestUnreadableResumeNoteStopsThePass(t *testing.T) {
	w := limitedWorld(t, false)
	dir, _ := w.NoteDir(ref(2))
	s := gather(t, w).Sessions[ref(2)]
	if err := os.WriteFile(filepath.Join(dir, resetNote("resumed-for", s)), []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Gather(w, ref(1), false); err == nil {
		t.Fatal("bad note was ignored")
	}
	if len(w.resumed) != 0 {
		t.Fatal("typed with unreadable notes")
	}
}

func TestObservedLimitSurvivesTheQuotaReset(t *testing.T) {
	w := limitedWorld(t, false)
	checks(t, w, 2)
	// The host drops its derived limit once live quota has expired. The
	// coordinator's observation is still a limit record for this wait.
	e := w.execs[ref(2)]
	e.limited, e.resetsAt = false, 0
	checks(t, w, 2)
	if len(w.resumed) != 0 {
		t.Fatal("resumed before grace period")
	}
	checks(t, w, 1)
	if len(w.resumed) != 1 {
		t.Fatalf("lost reset observation: %v", w.resumed)
	}
	checks(t, w, 11)
	if len(w.resumed) != 1 || len(w.comments[ref(2)]) != 0 {
		t.Fatalf("waiting limit: resumed %v, comments %v", w.resumed, w.comments)
	}
	// A successful automatic or coordinator resume clears the retained limit.
	e.status = session.AgentBusy
	checks(t, w, 1)
	e.status = session.AgentWaiting
	checks(t, w, 1)
	if s := gather(t, w).Sessions[ref(2)]; s.ResetsAt != 0 || s.WaitingSince.IsZero() {
		t.Fatalf("ordinary wait inherited limit: %+v", s)
	}
	checks(t, w, 10)
	if c := w.comments[ref(2)]; len(c) != 1 || !strings.Contains(c[0], "waited for input") {
		t.Fatalf("ordinary wait: %v", c)
	}
}

func TestANewSessionDoesNotInheritAnObservedLimit(t *testing.T) {
	w := limitedWorld(t, false)
	passOf(t, w)
	w.now = t0.Add(4 * time.Minute)
	if err := w.launch(ref(2), "devbox", "claude", behaviour{}); err != nil {
		t.Fatal(err)
	}
	w.execs[ref(2)].status = session.AgentWaiting
	checks(t, w, 2)
	if s := gather(t, w).Sessions[ref(2)]; s.ResetsAt != 0 || len(w.resumed) != 0 {
		t.Fatalf("new session: %+v, resumed %v", s, w.resumed)
	}
}
