package knowledge

import (
	"errors"
	"fmt"
	"time"
)

// Evidence kinds (knowledge_evidence.kind).
const (
	EvidenceDecisionEpisode  = "decision_episode"
	EvidenceHumanDecision    = "human_decision"
	EvidenceCustomerReaction = "customer_reaction"
	EvidenceBusinessOutcome  = "business_outcome"
	EvidenceCounterexample   = "counterexample"
)

// ErrInvalidEvidence is wrapped by every evidence error.
var ErrInvalidEvidence = errors.New("invalid knowledge evidence")

// Evidence is one new piece of support or contradiction for a knowledge object.
type Evidence struct {
	Kind        string
	RefID       string    // the decision episode, decision, reaction or outcome id
	Polarity    string    // customer_reaction: positive | neutral | negative
	OutcomeType string    // business_outcome: business_outcomes.outcome_type
	Note        string    // counterexample: why following the knowledge was wrong
	At          time.Time // when the evidence arrived (re-validation time)
}

// Change is one lifecycle status change with its reason.
type Change struct {
	From, To, Reason string
}

var polarities = map[string]bool{"positive": true, "neutral": true, "negative": true}

// advancedOutcomes count as outcomes_advanced; the others are linked but move no count (ADR-0013).
var advancedOutcomes = map[string]bool{"stage_advanced": true, "expansion": true, "renewal": true, "closed_won": true}

var outcomeTypes = map[string]bool{
	"stage_advanced": true, "stage_regressed": true, "renewal": true, "expansion": true, "closed_won": true,
	"closed_lost": true, "acv_change": true, "cycle_change": true,
}

func (ev Evidence) check() error {
	if ev.RefID == "" || ev.At.IsZero() {
		return fmt.Errorf("%w: ref id and time are required", ErrInvalidEvidence)
	}
	switch ev.Kind {
	case EvidenceDecisionEpisode, EvidenceHumanDecision:
		return nil
	case EvidenceCustomerReaction:
		if !polarities[ev.Polarity] {
			return fmt.Errorf("%w: reaction polarity %q", ErrInvalidEvidence, ev.Polarity)
		}
		return nil
	case EvidenceBusinessOutcome:
		if !outcomeTypes[ev.OutcomeType] {
			return fmt.Errorf("%w: outcome type %q", ErrInvalidEvidence, ev.OutcomeType)
		}
		return nil
	case EvidenceCounterexample:
		if ev.Note == "" {
			return fmt.Errorf("%w: a counterexample needs a note", ErrInvalidEvidence)
		}
		return nil
	}
	return fmt.Errorf("%w: unknown kind %q", ErrInvalidEvidence, ev.Kind)
}

// ApplyEvidence returns a copy of k with the evidence counted (ADR-0013 table). It does not change status.
func ApplyEvidence(k Knowledge, ev Evidence) (Knowledge, error) {
	if err := ev.check(); err != nil {
		return Knowledge{}, err
	}
	out := k.clone()
	validated := false
	switch ev.Kind {
	case EvidenceDecisionEpisode:
		if hasString(out.SupportingDecisionEpisodeIDs, ev.RefID) {
			return Knowledge{}, fmt.Errorf("%w: episode %s already supports %s", ErrInvalidEvidence, ev.RefID, k.ID)
		}
		out.SupportingDecisionEpisodeIDs = append(out.SupportingDecisionEpisodeIDs, ev.RefID)
		out.Counts.Decisions++
		// A human's choice is counted, but it does not validate the knowledge: only customer or world
		// evidence refreshes last_validated_at (HAR-97 B9, human choice is not truth).
	case EvidenceCustomerReaction:
		validated = ev.Polarity == "positive"
		out.Counts.PositiveReactions += boolInt(ev.Polarity == "positive")
		out.Counts.NegativeReactions += boolInt(ev.Polarity == "negative")
	case EvidenceBusinessOutcome:
		validated = advancedOutcomes[ev.OutcomeType]
		out.Counts.OutcomesAdvanced += boolInt(validated)
	case EvidenceCounterexample:
		out.Counterexamples = append(out.Counterexamples, Counterexample{DecisionEpisodeID: ev.RefID, Note: ev.Note})
		out.Counts.Counterexamples++
	}
	// Late evidence never moves last_validated_at backwards.
	if validated && (out.LastValidatedAt == nil || ev.At.After(*out.LastValidatedAt)) {
		at := ev.At.UTC()
		out.LastValidatedAt = &at
	}
	return out, nil
}

// Record applies the evidence and then the lifecycle rules.
func Record(k Knowledge, ev Evidence, rules Rules) (Knowledge, *Change, error) {
	next, err := ApplyEvidence(k, ev)
	if err != nil {
		return Knowledge{}, nil, err
	}
	applied := next
	next, change := EvaluateLifecycle(applied, ev.At, rules)
	// Learned knowledge whose scope is too broad stays a candidate however much evidence arrives: one human edit
	// and one reply must not make it applicable everywhere (HAR-97 B9). The evidence is still recorded. Knowledge
	// matched by similarity is bounded by the similarity threshold at match time instead (ADR-0013 amendment 2).
	if change != nil && learnedFromAnEpisode(applied) && rules.Applicable(change.To) && !rules.Similarity.Matches(applied) && ScopeTooBroad(applied) {
		return applied, nil, nil
	}
	return next, change, nil
}

// learnedFromAnEpisode is knowledge the loop minted from a human delta or a corrected verdict, as opposed to
// authored knowledge that carries its own scope.
func learnedFromAnEpisode(k Knowledge) bool {
	return k.Provenance.CreatedFrom == "human_delta" || k.Provenance.CreatedFrom == "manual"
}

// EvaluateLifecycle returns k with the status the rules give at now, and the change (nil if none).
// Precedence: disputed, stale, then the highest earned rung; the ladder never moves down except into
// disputed or stale, and leaving those returns to the rung the evidence earns now.
func EvaluateLifecycle(k Knowledge, now time.Time, rules Rules) (Knowledge, *Change) {
	to, reason := nextStatus(k, now, rules)
	if to == k.Status {
		return k, nil
	}
	out := k.clone()
	out.Status = to
	return out, &Change{From: k.Status, To: to, Reason: reason}
}

func nextStatus(k Knowledge, now time.Time, rules Rules) (string, string) {
	if why, ok := disputed(k.Counts, rules.Dispute); ok {
		return StatusDisputed, why
	}
	if why, ok := stale(k, now, rules.Stale); ok {
		return StatusStale, why
	}
	earned, why := earnedRung(k.Counts, rules)
	if k.Status == StatusDisputed || k.Status == StatusStale {
		return earned, "recovered from " + k.Status + ": " + why
	}
	if rank(earned) > rank(k.Status) {
		return earned, why
	}
	return k.Status, ""
}

func disputed(c Counts, d DisputeRule) (string, bool) {
	share := counterexampleShare(c)
	if c.Counterexamples >= d.MinCounterexamples && share >= d.MinCounterexampleShare {
		return fmt.Sprintf("disputed: %d counterexamples (>= %d), share %.2f (>= %.2f)",
			c.Counterexamples, d.MinCounterexamples, share, d.MinCounterexampleShare), true
	}
	if c.NegativeReactions >= d.MinNegativeReactions && c.NegativeReactions > c.PositiveReactions {
		return fmt.Sprintf("disputed: %d negative reactions (>= %d) outnumber %d positive",
			c.NegativeReactions, d.MinNegativeReactions, c.PositiveReactions), true
	}
	return "", false
}

func stale(k Knowledge, now time.Time, s StaleRule) (string, bool) {
	anchor := k.CreatedAt
	if k.LastValidatedAt != nil {
		anchor = *k.LastValidatedAt
	}
	if anchor.IsZero() {
		return "", false
	}
	days := int(now.Sub(anchor).Hours() / 24)
	if days >= s.AfterDays {
		return fmt.Sprintf("stale: not validated for %d days (>= %d)", days, s.AfterDays), true
	}
	return "", false
}

func earnedRung(c Counts, rules Rules) (string, string) {
	status, why := StatusCandidate, "no rung earned yet"
	share := counterexampleShare(c)
	for _, g := range rules.Rungs {
		if c.Decisions < g.MinDecisions || c.PositiveReactions < g.MinPositiveReactions ||
			c.OutcomesAdvanced < g.MinOutcomesAdvanced || share > g.MaxCounterexampleShare {
			break
		}
		status = g.Status
		why = fmt.Sprintf("earned %s: %d decisions (>= %d), %d positive reactions (>= %d), %d advanced outcomes (>= %d), counterexample share %.2f (<= %.2f)",
			g.Status, c.Decisions, g.MinDecisions, c.PositiveReactions, g.MinPositiveReactions,
			c.OutcomesAdvanced, g.MinOutcomesAdvanced, share, g.MaxCounterexampleShare)
	}
	return status, why
}

// counterexampleShare = counterexamples / (decisions + counterexamples), 0 when both are 0.
func counterexampleShare(c Counts) float64 {
	total := c.Decisions + c.Counterexamples
	if total == 0 {
		return 0
	}
	return float64(c.Counterexamples) / float64(total)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// clone deep-copies the knowledge so the caller's value is never written through, including the
// condition values (lists and item-pattern maps) inside signatures and exceptions.
func (k Knowledge) clone() Knowledge {
	out := k
	out.SituationSignature = cloneConditions(k.SituationSignature)
	out.ApplicabilityConditions = cloneConditions(k.ApplicabilityConditions)
	out.SupportingDecisionEpisodeIDs = append([]string(nil), k.SupportingDecisionEpisodeIDs...)
	out.Counterexamples = append([]Counterexample(nil), k.Counterexamples...)
	out.Exceptions = make([]Exception, len(k.Exceptions))
	for i, x := range k.Exceptions {
		out.Exceptions[i] = Exception{Description: x.Description, Conditions: cloneConditions(x.Conditions)}
	}
	if k.Exceptions == nil {
		out.Exceptions = nil
	}
	out.StatusHistory = append([]HistoryEntry(nil), k.StatusHistory...)
	out.Guidance = Guidance{Summary: k.Guidance.Summary, Do: append([]string(nil), k.Guidance.Do...),
		Dont: append([]string(nil), k.Guidance.Dont...)}
	if k.LastValidatedAt != nil {
		t := *k.LastValidatedAt
		out.LastValidatedAt = &t
	}
	return out
}

func cloneConditions(cs []Condition) []Condition {
	if cs == nil {
		return nil
	}
	out := make([]Condition, len(cs))
	for i, c := range cs {
		out[i] = Condition{Field: c.Field, Op: c.Op, Value: cloneValue(c.Value)}
	}
	return out
}

// cloneValue deep-copies a decoded JSON value.
func cloneValue(v any) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = cloneValue(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for key, e := range x {
			out[key] = cloneValue(e)
		}
		return out
	}
	return v
}
