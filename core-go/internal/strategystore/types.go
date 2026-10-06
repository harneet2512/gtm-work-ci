// Package strategystore persists and serves the HAR-129 decision objects that sit between the strategy
// generator and the learning loop: the strategy set with its eval bundles, the human's strategy decision
// (choose, edit, send), the judgment inference and the human's verdict on it, and the latest
// business-intelligence update (contracts/openapi/core.yaml, ADR-0017, migrations 0019 and 0020).
//
// It generates nothing. The strategies, the business intelligence and the inference are written by the
// orchestrator and the worker; this package stores the human's side of the episode and serves every object
// exactly as stored, as the JSON the contract schemas describe.
package strategystore

import (
	"errors"
	"fmt"
	"regexp"
)

// Errors a caller maps to the HTTP status codes of the contract.
var (
	// ErrNotFound: the run, episode, account or object does not exist (404 not_found, no_decision, no_update).
	ErrNotFound = errors.New("strategystore: not found")
	// ErrNotReady: the object is not produced yet (404 strategies_not_ready, inference_not_ready).
	ErrNotReady = errors.New("strategystore: not ready")
)

// Codes of a ConflictError (409) and of the 404 variants.
const (
	CodeSelectionLocked = "selection_locked"
	CodeAlreadyDecided  = "already_decided"
	CodeNoChoice        = "no_choice"
	CodeNoDecision      = "no_decision"
	CodeNoUpdate        = "no_update"
	CodeNotReady        = "strategies_not_ready"
	CodeInferenceWait   = "inference_not_ready"
)

// NotFoundError carries the contract code of a 404.
type NotFoundError struct{ Code string }

func (e *NotFoundError) Error() string        { return "strategystore: not found: " + e.Code }
func (e *NotFoundError) Is(target error) bool { return target == ErrNotFound }

// NotReadyError is a 404 whose object will exist later.
type NotReadyError struct{ Code string }

func (e *NotReadyError) Error() string        { return "strategystore: not ready: " + e.Code }
func (e *NotReadyError) Is(target error) bool { return target == ErrNotReady }

// ConflictError is a 409. Decision is the stored HumanStrategyDecision JSON, set for already_decided.
type ConflictError struct {
	Code     string
	Decision []byte
}

func (e *ConflictError) Error() string { return "strategystore: conflict: " + e.Code }

// RefusedError is a 422: the request is well formed but policy or the data refuses it.
type RefusedError struct{ Code, Message string }

func (e *RefusedError) Error() string { return "strategystore: refused: " + e.Message }

func refuse(code, format string, a ...any) error {
	return &RefusedError{Code: code, Message: fmt.Sprintf(format, a...)}
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// IsUUID reports whether s is a canonical UUID.
func IsUUID(s string) bool { return uuidPattern.MatchString(s) }

// Recipient is agent_run_output.recipients[]: a person, never an address.
type Recipient struct {
	PersonID string `json:"person_id"`
	Role     string `json:"role"`
	Why      string `json:"why,omitempty"`
}

// Artifact is agent_run_output.finished_artifact.
type Artifact struct {
	Channel     string   `json:"channel"`
	Subject     *string  `json:"subject,omitempty"`
	Body        string   `json:"body"`
	Attachments []string `json:"attachments,omitempty"`
}

// Surfaces a decision may come from (human_decision.surface).
var surfaces = map[string]bool{"web": true, "slack": true, "mcp": true, "api": true}

// Limits of the request fields (core.yaml).
const (
	maxActorLabel   = 200
	maxArtifactBody = 20000
	maxSubject      = 300
	maxStatement    = 1500
	maxNote         = 2000
)

// DecisionRequest is the body of POST /runs/{run_id}/strategy-decision. An omitted final_* field keeps its
// current value (the saved edit, else the candidate's).
type DecisionRequest struct {
	SelectedCandidateID string       `json:"selected_candidate_id"`
	Surface             string       `json:"surface"`
	ActorPersonID       *string      `json:"actor_person_id,omitempty"`
	ActorLabel          string       `json:"actor_label"`
	FinalTo             *[]Recipient `json:"final_to,omitempty"`
	FinalCC             *[]Recipient `json:"final_cc,omitempty"`
	FinalArtifact       *Artifact    `json:"final_artifact,omitempty"`
}

// HasEdits reports whether the request carries any final_* field.
func (r DecisionRequest) HasEdits() bool {
	return r.FinalTo != nil || r.FinalCC != nil || r.FinalArtifact != nil
}

// Validate checks the request the way the schema does, so the service is safe without the HTTP layer.
func (r DecisionRequest) Validate() error {
	if !IsUUID(r.SelectedCandidateID) {
		return refuse("invalid_request", "selected_candidate_id must be a UUID")
	}
	if err := validateActor(r.Surface, r.ActorPersonID, r.ActorLabel); err != nil {
		return err
	}
	if r.FinalTo != nil {
		if len(*r.FinalTo) == 0 {
			return refuse("invalid_request", "final_to needs at least one recipient")
		}
		if err := validateRecipients(*r.FinalTo, "to"); err != nil {
			return err
		}
	}
	if r.FinalCC != nil {
		if err := validateRecipients(*r.FinalCC, "cc"); err != nil {
			return err
		}
	}
	if a := r.FinalArtifact; a != nil {
		if a.Channel == "" || len(a.Body) > maxArtifactBody || (a.Subject != nil && len(*a.Subject) > maxSubject) {
			return refuse("invalid_request", "final_artifact needs a channel, a body of at most %d characters and a subject of at most %d", maxArtifactBody, maxSubject)
		}
	}
	return nil
}

// SendRequest is the body of POST /runs/{run_id}/send.
type SendRequest struct {
	Decision      string  `json:"decision"`
	Surface       string  `json:"surface"`
	ActorPersonID *string `json:"actor_person_id,omitempty"`
	ActorLabel    string  `json:"actor_label"`
}

// Validate checks the request like the schema.
func (r SendRequest) Validate() error {
	if r.Decision != "send" && r.Decision != "discard" {
		return refuse("invalid_request", "decision must be send or discard")
	}
	return validateActor(r.Surface, r.ActorPersonID, r.ActorLabel)
}

// VerdictRequest is the body of POST /episodes/{episode_id}/judgment-verdict.
type VerdictRequest struct {
	Verdict            string  `json:"verdict,omitempty"`
	CorrectedStatement string  `json:"corrected_statement,omitempty"`
	Note               string  `json:"note,omitempty"`
	Surface            string  `json:"surface"`
	ActorPersonID      *string `json:"actor_person_id,omitempty"`
	ActorLabel         string  `json:"actor_label"`
}

// Validate checks the request like the schema.
func (r VerdictRequest) Validate() error {
	if err := validateActor(r.Surface, r.ActorPersonID, r.ActorLabel); err != nil {
		return err
	}
	switch r.Verdict {
	case "", "confirmed", "corrected", "no_learning":
	default:
		return refuse("invalid_request", "verdict must be confirmed, corrected or no_learning")
	}
	if r.Verdict == "" && r.Note == "" {
		return refuse("invalid_request", "a verdict or a note is required")
	}
	if r.Verdict == "corrected" && r.CorrectedStatement == "" {
		return refuse("invalid_request", "a correction needs corrected_statement")
	}
	if r.Verdict != "corrected" && r.CorrectedStatement != "" {
		return refuse("invalid_request", "corrected_statement only goes with verdict corrected")
	}
	if len(r.CorrectedStatement) > maxStatement || len(r.Note) > maxNote {
		return refuse("invalid_request", "corrected_statement is limited to %d and note to %d characters", maxStatement, maxNote)
	}
	return nil
}

func validateActor(surface string, person *string, label string) error {
	switch {
	case !surfaces[surface]:
		return refuse("invalid_request", "surface must be web, slack, mcp or api")
	case label == "" || len(label) > maxActorLabel:
		return refuse("invalid_request", "actor_label must be 1 to %d characters", maxActorLabel)
	case person != nil && !IsUUID(*person):
		return refuse("invalid_request", "actor_person_id must be a UUID")
	}
	return nil
}

func validateRecipients(rs []Recipient, role string) error {
	for _, r := range rs {
		if !IsUUID(r.PersonID) || r.Role != role || len(r.Why) > 500 {
			return refuse("invalid_request", "every %s recipient needs a person_id UUID and role %q", role, role)
		}
	}
	return nil
}
