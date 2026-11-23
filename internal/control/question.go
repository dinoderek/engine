package control

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// HardStopClasses are the classes a question's class names: the build
// skill's hard-stop reference's, in its order, each in lower case.
var HardStopClasses = []string{
	"product", "data", "security", "contract", "gates", "outward or costly",
	"drift", "objectives", "review", "environment", "repo rule", "permission",
}

// MaxChoices and ChoiceRunes bound a question's choices: how many, and how
// long each may be.
const (
	MaxChoices  = 6
	ChoiceRunes = 80
)

// ReasonAlreadyResolved is resolve's refusal of a question an update has
// already resolved, by the operator or by a pane answer.
const ReasonAlreadyResolved = "already-resolved"

// questionFields checks a request's choices, recommendation and class: only
// an ask carries them, each choice is one non-blank line of at most
// ChoiceRunes with no repeat, the recommendation indexes the choices from 0,
// and the class is one of HardStopClasses.
func questionFields(r Request) error {
	if r.Op != "ask" {
		if len(r.Choices) > 0 || r.Recommended != nil || r.Class != "" {
			return problem(400, "only ask takes choices, a recommendation or a class")
		}
		return nil
	}
	if len(r.Choices) > MaxChoices {
		return problem(400, "a question takes at most %d choices, not %d", MaxChoices, len(r.Choices))
	}
	for i, c := range r.Choices {
		if err := validChoice(c); err != nil {
			return problem(400, "choice %d %s", i, err)
		}
		if slices.Contains(r.Choices[:i], c) {
			return problem(400, "choice %d repeats an earlier choice", i)
		}
	}
	if r.Recommended != nil {
		if len(r.Choices) == 0 {
			return problem(400, "a recommendation needs choices to recommend from")
		}
		if n := *r.Recommended; n < 0 || n >= len(r.Choices) {
			return problem(400, "recommended %d is not a choice: give an index from 0 to %d", n, len(r.Choices)-1)
		}
	}
	if r.Class != "" && !slices.Contains(HardStopClasses, r.Class) {
		return problem(400, "class %q is not a hard-stop class; use one of: %s", r.Class, strings.Join(HardStopClasses, ", "))
	}
	return nil
}

func validChoice(c string) error {
	if strings.TrimSpace(c) == "" {
		return fmt.Errorf("is blank")
	}
	if n := utf8.RuneCountInString(c); n > ChoiceRunes {
		return fmt.Errorf("has %d characters; the limit is %d", n, ChoiceRunes)
	}
	if strings.ContainsFunc(c, unicode.IsControl) {
		return fmt.Errorf("must be one line, with no control characters")
	}
	return nil
}

// alreadyResolved refuses a resolution of question id once an update has
// resolved it, naming that update.
func alreadyResolved(tx *Tx, id int) error {
	by := tx.Resolution(id)
	if by == 0 {
		return nil
	}
	return &Problem{Status: 409, Reason: ReasonAlreadyResolved,
		Message: fmt.Sprintf("question %d is already resolved, by update %d", id, by)}
}
