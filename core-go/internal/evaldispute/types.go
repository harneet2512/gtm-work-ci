// Package evaldispute records a human's "this eval is wrong" on one EvalResult (HAR-97 E19 grader validity,
// contracts/schemas/eval_dispute.v1.json, POST /eval-results/{eval_result_id}/disputes). A dispute snapshots the
// disputed verdict and blocking flag so eval-of-evals (E19-E22) can count false passes and false blocks. It is
// append-only supervision: it never changes the result, its bundle, the send gate or company knowledge.
package evaldispute

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ErrNotFound: the eval result does not exist (404 not_found).
var ErrNotFound = errors.New("evaldispute: eval result not found")

// Codes of a RefusedError (422).
const (
	CodeInvalidRequest        = "invalid_request"
	CodeExpectedEqualsVerdict = "expected_equals_verdict"
	CodeUnknownPerson         = "unknown_person"
)

// Limits from the contract (eval_dispute.v1.json), counted in characters like JSON Schema and Postgres.
const (
	maxReason     = 2000
	maxActorLabel = 200
)

// RefusedError is a 422: the request is well formed JSON but the contract or the data refuses it.
type RefusedError struct{ Code, Message string }

func (e *RefusedError) Error() string { return "evaldispute: refused: " + e.Message }

func refuse(code, format string, a ...any) error {
	return &RefusedError{Code: code, Message: fmt.Sprintf(format, a...)}
}

// Request is the EvalDisputeRequest body.
type Request struct {
	Reason          string  `json:"reason"`
	ExpectedVerdict *string `json:"expected_verdict,omitempty"`
	Surface         string  `json:"surface"`
	ActorPersonID   *string `json:"actor_person_id,omitempty"`
	ActorLabel      string  `json:"actor_label"`
}

var (
	surfaces         = map[string]bool{"web": true, "slack": true, "mcp": true, "api": true}
	expectedVerdicts = map[string]bool{"pass": true, "warn": true, "fail": true, "abstain": true, "not_relevant": true}
	uuidPattern      = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// IsUUID reports whether s is a canonical UUID.
func IsUUID(s string) bool { return uuidPattern.MatchString(s) }

// normalized returns the request with the reason trimmed, or a RefusedError naming what is wrong. The
// receiver is a value: the caller's request is never modified.
func (r Request) normalized() (Request, error) {
	r.Reason = strings.TrimSpace(r.Reason)
	switch {
	case r.Reason == "":
		return r, refuse(CodeInvalidRequest, "reason must say what the eval got wrong")
	case utf8.RuneCountInString(r.Reason) > maxReason:
		return r, refuse(CodeInvalidRequest, "reason is limited to %d characters", maxReason)
	case !surfaces[r.Surface]:
		return r, refuse(CodeInvalidRequest, "surface must be web, slack, mcp or api")
	case r.ActorLabel == "" || utf8.RuneCountInString(r.ActorLabel) > maxActorLabel:
		return r, refuse(CodeInvalidRequest, "actor_label must be 1 to %d characters", maxActorLabel)
	case r.ActorPersonID != nil && !IsUUID(*r.ActorPersonID):
		return r, refuse(CodeInvalidRequest, "actor_person_id must be a uuid")
	case r.ExpectedVerdict != nil && !expectedVerdicts[*r.ExpectedVerdict]:
		return r, refuse(CodeInvalidRequest, "expected_verdict must be pass, warn, fail, abstain or not_relevant")
	}
	return r, nil
}
