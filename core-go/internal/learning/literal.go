package learning

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/evals/deterministic"
)

// Spec is evaluator_versions.shadow_spec: the executable form of a delta-derived candidate criterion.
// The axis check replays the literal changes the human made as predicates over a new draft — "the draft
// still omits what the human added" is the shadowed criterion's failure. A spec with no executable
// change kinds backtests but has nothing to run alongside the evals.
type Spec struct {
	AccountID      string          `json:"account_id"`
	DeltaID        string          `json:"human_delta_id,omitempty"`
	LiteralChanges []LiteralChange `json:"literal_changes"`
}

// check is one executable predicate a literal change kind produces. holds runs on the draft under
// evaluation (deterministic.Output — the same shape candidates and final artifacts share).
type check struct {
	name   string
	holds  func(d deterministic.Output) bool
	detail func(d deterministic.Output, ok bool) string
}

// personRole decodes the {person_id, role} object a recipient edit carries.
func personRole(v any) (id, role string, ok bool) {
	m, is := v.(map[string]any)
	if !is {
		return "", "", false
	}
	id, _ = m["person_id"].(string)
	role, _ = m["role"].(string)
	return id, role, id != ""
}

// recipientHas reports whether the draft addresses person in the given role.
func recipientHas(d deterministic.Output, personID, role string) bool {
	for _, r := range d.Recipients {
		if r.PersonID == personID {
			return role == "" || r.Role == role
		}
	}
	return false
}

// textMatch is the contract's case-insensitive, whitespace-collapsed comparison on attachment names.
func textMatch(hay, needle string) bool {
	norm := func(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }
	return strings.Contains(norm(hay), norm(needle))
}

func attachmentHas(d deterministic.Output, name string) bool {
	for _, a := range d.FinishedArtifact.Attachments {
		if textMatch(a, name) {
			return true
		}
	}
	return false
}

func strOf(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// checks derives the executable predicates of the spec's literal changes. Recipient, role, channel and
// attachment edits become exact predicates; paragraph, subject, cta, timing, action-type and crm edits
// are semantic and stay unexecutable — their criterion still backtests, it simply has no check to
// shadow.
func (s Spec) checks() []check {
	out := []check{}
	for _, ch := range s.LiteralChanges {
		switch ch.Kind {
		case "recipient_added":
			id, role, ok := personRole(ch.After)
			if !ok {
				continue
			}
			out = append(out, check{
				name:  "recipient_added",
				holds: func(d deterministic.Output) bool { return recipientHas(d, id, role) },
				detail: func(_ deterministic.Output, ok bool) string {
					if ok {
						return fmt.Sprintf("the draft includes recipient %s as %s", id, role)
					}
					return fmt.Sprintf("the draft omits recipient %s the human added as %s", id, role)
				},
			})
		case "recipient_removed":
			id, _, ok := personRole(ch.Before)
			if !ok {
				continue
			}
			out = append(out, check{
				name:  "recipient_removed",
				holds: func(d deterministic.Output) bool { return !recipientHas(d, id, "") },
				detail: func(_ deterministic.Output, ok bool) string {
					if ok {
						return fmt.Sprintf("the draft no longer addresses %s", id)
					}
					return fmt.Sprintf("the draft still addresses recipient %s the human removed", id)
				},
			})
		case "recipient_role_changed":
			id, role, ok := personRole(ch.After)
			if !ok {
				continue
			}
			out = append(out, check{
				name:  "recipient_role_changed",
				holds: func(d deterministic.Output) bool { return recipientHas(d, id, role) },
				detail: func(d deterministic.Output, ok bool) string {
					if ok {
						return fmt.Sprintf("the draft addresses %s as %s", id, role)
					}
					return fmt.Sprintf("the draft does not address %s in the role %s the human set", id, role)
				},
			})
		case "channel_changed":
			want := strOf(ch.After)
			if want == "" {
				continue
			}
			out = append(out, check{
				name:  "channel_changed",
				holds: func(d deterministic.Output) bool { return d.FinishedArtifact.Channel == want },
				detail: func(d deterministic.Output, ok bool) string {
					if ok {
						return fmt.Sprintf("the draft uses channel %q", want)
					}
					return fmt.Sprintf("the draft still uses channel %q, not the %q the human chose",
						d.FinishedArtifact.Channel, want)
				},
			})
		case "attachment_added":
			name := strOf(ch.After)
			if name == "" {
				continue
			}
			out = append(out, check{
				name:  "attachment_added",
				holds: func(d deterministic.Output) bool { return attachmentHas(d, name) },
				detail: func(_ deterministic.Output, ok bool) string {
					if ok {
						return fmt.Sprintf("the draft carries %q", name)
					}
					return fmt.Sprintf("the draft lacks the %q the human attached", name)
				},
			})
		case "attachment_removed":
			name := strOf(ch.Before)
			if name == "" {
				continue
			}
			out = append(out, check{
				name:  "attachment_removed",
				holds: func(d deterministic.Output) bool { return !attachmentHas(d, name) },
				detail: func(_ deterministic.Output, ok bool) string {
					if ok {
						return fmt.Sprintf("the draft no longer carries %q", name)
					}
					return fmt.Sprintf("the draft still carries the %q the human removed", name)
				},
			})
		}
	}
	return out
}

// agnosticKinds are change kinds whose predicates are meaningful on any world's draft — the channel and
// attachment names a change carries are self-contained, while a person id only names someone in the
// delta's own account. The gold leg of a backtest scores only agnostic specs (a person-bound spec is
// vacuously false on a fixture world and would measure nothing).
var agnosticKinds = map[string]bool{
	"channel_changed": true, "attachment_added": true, "attachment_removed": true,
}

// Executable reports whether the spec produces at least one axis check.
func (s Spec) Executable() bool { return len(s.checks()) > 0 }

// Agnostic reports whether every executable check is world-agnostic (see agnosticKinds).
func (s Spec) Agnostic() bool {
	cs := s.checks()
	if len(cs) == 0 {
		return false
	}
	for _, c := range cs {
		if !agnosticKinds[c.name] {
			return false
		}
	}
	return true
}

// RunSpec applies every executable check to the draft: the axis fails when any predicate is violated.
// The detail lists the violated predicates, or the satisfied ones on a pass.
func (s Spec) RunSpec(d deterministic.Output) (violated bool, detail string) {
	cs := s.checks()
	var fails, passes []string
	for _, c := range cs {
		if c.holds(d) {
			passes = append(passes, c.detail(d, true))
		} else {
			fails = append(fails, c.detail(d, false))
		}
	}
	if len(fails) > 0 {
		return true, strings.Join(fails, "; ")
	}
	return false, strings.Join(passes, "; ")
}

// parseSpec decodes an evaluator_versions.shadow_spec payload.
func parseSpec(raw []byte) (Spec, error) {
	var s Spec
	if len(raw) == 0 || string(raw) == "null" {
		return s, nil
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, fmt.Errorf("%w: %v", ErrInvalidSpec, err)
	}
	return s, nil
}
