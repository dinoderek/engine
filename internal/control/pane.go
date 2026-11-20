package control

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"strings"
)

// The update kinds of the operator's pane input, which a session's prompt
// hook posts with the pane-input op: an answer resolves the open question
// that holds the session's item; a message, sent while none did, resolves
// nothing.
const (
	KindPaneAnswer  = "pane-answer"
	KindPaneMessage = "pane-message"
)

// PaneInput is how the prompt hook vouches for a pane-input post: the
// signature, with its run's pane key, of PaneMessage, and whether the text
// was found among the prompts in the session's transcript.
type PaneInput struct {
	Signature  string `json:"signature,omitempty"`
	Transcript bool   `json:"transcript,omitempty"`
}

// PaneMessage is what the prompt hook signs for the operator's text in the
// session of attempt.
func PaneMessage(attempt, text string) []byte {
	return []byte("engine pane input\n" + attempt + "\n" + text)
}

// Started records that the attempt's window opened as window, and the
// public key, in hex, of its run's pane key, "" for a run that has none.
func (s Service) Started(id, window, paneKey string) error {
	return s.setAttempt(id, "running", window, paneKey)
}

// paneInput records the text the operator typed into the pane of the
// calling session, verbatim, for the session's own item. It resolves the
// item's open question, if one holds it, restoring the held tasks as a
// resolution does. A post the hook did not vouch for (an absent or invalid
// signature, or text missing from the transcript) is still recorded, and
// resolves alike, but raises one coordinator note to the operator.
func (s Service) paneInput(st *State, a Actor, r Request) (Response, error) {
	if a.Role != "worker" {
		return Response{}, problem(403, "pane input comes only from a session's prompt hook")
	}
	if strings.TrimSpace(r.Body) == "" {
		return Response{}, problem(400, "pane input text is required")
	}
	at := st.Attempts[a.Attempt]
	item := at.Item
	if r.ID != "" && r.ID != item {
		return Response{}, problem(403, "pane input is for the session's own item, %s", item)
	}
	vouch := PaneInput{}
	if r.Pane != nil {
		vouch = *r.Pane
	}
	r.ID, r.Recipient, r.ReplyTo, r.RequiresReply, r.Pane = item, "", 0, false, nil
	kind, question := KindPaneMessage, OwnQuestion(st, item)
	if question > 0 {
		kind, r.ID, r.ReplyTo = KindPaneAnswer, st.Updates[question-1].Item, question
	}
	u := st.update(a, r, kind, r.Body, s.now())
	out := Response{Updates: []Update{u}}
	if question > 0 {
		out.Updates = append(out.Updates, s.release(st, a, st.Updates[question-1])...)
	}
	if doubts := paneDoubts(at, r.Body, vouch); len(doubts) > 0 {
		outcome := "resolved nothing"
		if question > 0 {
			outcome = fmt.Sprintf("resolved question %d", question)
		}
		note := fmt.Sprintf("Pane input %d on %s may not be the operator's: %s. It is recorded and %s; check that you typed it, and reopen the question if you did not.",
			u.ID, item, strings.Join(doubts, ", and "), outcome)
		out.Updates = append(out.Updates, st.update(Actor{Name: "coordinator", Role: "coordinator"}, Request{ID: item, Recipient: "operator"}, "say", note, s.now()))
	}
	return out, nil
}

// paneDoubts are the reasons a pane-input post may not have come through
// its session's prompt hook.
func paneDoubts(at Attempt, text string, vouch PaneInput) []string {
	var doubts []string
	switch {
	case vouch.Signature == "":
		doubts = append(doubts, "it is unsigned")
	case !validPaneSignature(at, text, vouch.Signature):
		doubts = append(doubts, "its signature does not match the run's pane key")
	}
	if !vouch.Transcript {
		doubts = append(doubts, "it is missing from the prompts in the session's transcript")
	}
	return doubts
}

func validPaneSignature(at Attempt, text, signature string) bool {
	key, err := hex.DecodeString(at.PaneKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return false
	}
	sig, err := hex.DecodeString(signature)
	return err == nil && ed25519.Verify(key, PaneMessage(at.ID, text), sig)
}
