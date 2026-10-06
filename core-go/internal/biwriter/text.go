package biwriter

import (
	"fmt"
	"sort"
	"strings"
)

// activityPhrases say what kind of activity caused the change; any other type is named as it is.
var activityPhrases = map[string]string{
	"EmailReceived": "an inbound email", "EmailSent": "an outbound email", "EmailReply": "an email reply",
	"CustomerReplied": "a customer reply", "MeetingCompleted": "a completed meeting", "MeetingScheduled": "a scheduled meeting",
	"MeetingAccepted": "an accepted meeting", "MeetingDeclined": "a declined meeting", "CallEnded": "a finished call",
	"TranscriptReady": "a call transcript", "CRMFieldChanged": "a CRM field change", "OpportunityStageChanged": "a stage change in the CRM",
	"ContactAdded": "a new contact", "DocumentShared": "a shared document", "SlackMessage": "a Slack message",
}

const (
	maxSummaryFields = 5
	maxMissingListed = 6
	maxSubject       = 80
)

// summary is the one-line headline: "<account>: N material changes after <activity> (<date>): <fields>."
func summary(f Facts, claims []Claim) string {
	name := strings.TrimSpace(f.AccountName)
	if name == "" {
		name = "The account"
	}
	var labels []string
	seen := map[string]bool{}
	for _, c := range claims {
		l := fields[*c.StateDiffField].label
		if !seen[l] {
			seen[l] = true
			labels = append(labels, l)
		}
	}
	listed := labels
	more := ""
	if len(labels) > maxSummaryFields {
		listed, more = labels[:maxSummaryFields], fmt.Sprintf(", +%d more", len(labels)-maxSummaryFields)
	}
	s := fmt.Sprintf("%s: %s after %s%s: %s%s.", name, countChanges(len(claims)), activityPhrase(f.Activity),
		activityDate(f.Activity), strings.Join(listed, ", "), more)
	return clip(s, maxSummary)
}

func countChanges(n int) string {
	if n == 1 {
		return "1 material change"
	}
	return fmt.Sprintf("%d material changes", n)
}

func activityPhrase(a Activity) string {
	phrase, ok := activityPhrases[a.Type]
	if !ok {
		phrase = "an activity of type " + a.Type
	}
	if s := strings.TrimSpace(a.Summary); s != "" {
		phrase += ` "` + clip(s, maxSubject) + `"`
	}
	return phrase
}

func activityDate(a Activity) string {
	if a.OccurredAt.IsZero() {
		return ""
	}
	return " (" + a.OccurredAt.UTC().Format("2006-01-02") + ")"
}

// dimensionReading is the business reading of one change dimension: what moved, and why that matters
// commercially. It is fixed text; the facts it cites are the diff's own (factPhrase).
type dimensionReading struct{ moved, matters string }

var dimensionReadings = map[string]dimensionReading{
	dimStakeholders: {"The buying group moved",
		"Who sits at the table decides whose objections must be answered before signature, so the next message has to reach the people who just entered and not only the usual contact."},
	dimOwnership: {"Ownership of the relationship moved",
		"Trust and promises are held by people, so when the owner or the champion changes, momentum depends on the new one being brought up to speed quickly."},
	dimIntent: {"The buyer's position moved",
		"Stage, criteria and process show how close the buyer is to a decision, so the way we engage should match where they now are."},
	dimBlockers: {"The risk picture moved",
		"An open blocker or objection stands between this deal and a signature until it is answered, and every day it stays open it is cheaper for the buyer to wait."},
	dimNextStep: {"The next step moved",
		"A deal without an agreed, dated next step drifts, so the follow-up has to re-establish what happens next and who owns it."},
}

// maxFactsPerDimension bounds the facts one dimension sentence cites; the claims above it list them all.
const maxFactsPerDimension = 3

// whyItMatters is the business reading of the change (HAR-129 section 5): one template per dimension that moved,
// saying what changed (citing the diff's facts) and why that matters commercially, then, when the event touched a
// relationship-state transition, where that stands. It carries no pipeline diagnostics (no signal types, no trigger
// reason codes) and adds no judgment beyond the fixed reading of each dimension.
func whyItMatters(f Facts, claims []Claim) string {
	var order []string
	facts := map[string][]string{}
	entries := map[string]DiffEntry{}
	for _, e := range f.Diff.Entries {
		entries[e.Field] = e
	}
	for _, c := range claims {
		if _, ok := facts[c.Dimension]; !ok {
			order = append(order, c.Dimension)
		}
		facts[c.Dimension] = append(facts[c.Dimension], factPhrase(entries[*c.StateDiffField], c))
	}
	parts := make([]string, 0, len(order)+1)
	for _, dim := range order {
		parts = append(parts, dimensionSentence(dim, facts[dim]))
	}
	if s := transitionSentence(f.Transition); s != "" {
		parts = append(parts, s)
	}
	return clip(strings.Join(parts, " "), maxWhy)
}

func dimensionSentence(dim string, cited []string) string {
	r, ok := dimensionReadings[dim]
	if !ok {
		r = dimensionReading{"The account changed (" + dimensionLabels[dim] + ")", ""}
	}
	listed, more := cited, ""
	if len(cited) > maxFactsPerDimension {
		listed, more = cited[:maxFactsPerDimension], fmt.Sprintf("; +%d more", len(cited)-maxFactsPerDimension)
	}
	s := fmt.Sprintf("%s (%s%s).", r.moved, strings.Join(listed, "; "), more)
	if r.matters != "" {
		s += " " + r.matters
	}
	return s
}

// factPhrase is the short citation of one diff entry: its label and its new value. Entries with a structure of
// their own (buying group, open transition) cite the claim's statement.
func factPhrase(e DiffEntry, c Claim) string {
	if e.Field == fieldBuyingGroup || e.Field == fieldOpenTransition {
		return strings.TrimSuffix(c.Statement, ".")
	}
	label := fields[e.Field].label
	switch e.Op {
	case opSet, opAdded:
		return fmt.Sprintf("%s now %s", label, render(e.After))
	case opRemoved:
		return fmt.Sprintf("%s cleared, was %s", label, render(e.Before))
	case opBecameUnknown:
		return fmt.Sprintf("%s no longer known, was %s", label, render(e.Before))
	}
	return fmt.Sprintf("%s moved from %s to %s", label, render(e.Before), render(e.After))
}

// transitionSentence says where the relationship-state transition the event touched stands, in plain words. An
// untouched transition (Transition.Touched false) is not part of the reading: the structured transition of the
// update shows it, labelled unchanged.
func transitionSentence(t *Transition) string {
	if t == nil || !t.Touched {
		return ""
	}
	to := "an unclear target"
	if t.ToState != nil {
		to = *t.ToState
	}
	switch t.Status {
	case "CONFIRMED":
		return fmt.Sprintf("The relationship moved from %s to %s.", t.FromState, to)
	case "REJECTED":
		return fmt.Sprintf("A possible move of the relationship from %s to %s was ruled out.", t.FromState, to)
	}
	s := fmt.Sprintf("The relationship may be moving from %s to %s, but that is not confirmed.", t.FromState, to)
	required, optional := missingKeys(t.Missing)
	if len(required)+len(optional) == 0 {
		return s
	}
	var needs []string
	if len(required) > 0 {
		needs = append(needs, "needed: "+humanKeys(required))
	}
	if len(optional) > 0 {
		needs = append(needs, "helpful: "+humanKeys(optional))
	}
	return s + " Still " + strings.Join(needs, "; ") + "."
}

func humanKeys(keys []string) string {
	human := make([]string, len(keys))
	for i, k := range keys {
		human[i] = strings.ReplaceAll(k, "_", " ")
	}
	return listKeys(human)
}

func missingKeys(facts []Fact) (required, optional []string) {
	for _, m := range facts {
		if m.Required {
			required = append(required, m.Key)
		} else {
			optional = append(optional, m.Key)
		}
	}
	sort.Strings(required)
	sort.Strings(optional)
	return required, optional
}

func listKeys(keys []string) string {
	if len(keys) > maxMissingListed {
		return strings.Join(keys[:maxMissingListed], ", ") + fmt.Sprintf(", +%d more", len(keys)-maxMissingListed)
	}
	return strings.Join(keys, ", ")
}
