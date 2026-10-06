package reactions

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// polarityOf pins every reaction_type to its polarity (customer_reaction.v1.json enum); knowledge
// counts positive and negative reactions, a neutral one is linked but moves neither.
var polarityOf = map[string]string{
	"replied":               "neutral", // a reply is not automatically positive: the phrases it carries decide (HAR-97)
	"ignored":               "negative",
	"meeting_accepted":      "positive",
	"stakeholder_added":     "positive",
	"stakeholder_removed":   "negative",
	"objection_raised":      "negative",
	"new_commitment":        "positive",
	"requested_document":    "neutral",
	"conversation_advanced": "positive",
	"conversation_cooled":   "negative",
}

// phraseRules read customer-authored text into the softer reaction types. Patterns are deliberately
// narrow — a wrong phrase match writes a row a human can dispute, while a missed one writes nothing.
// Order is the order a row yields its types.
var phraseRules = []struct {
	typ string
	re  *regexp.Regexp
}{
	{"requested_document", regexp.MustCompile(`(?i)\b(?:send|share|forward|provide|attach|include)\b[^\n.!?]{0,80}\b(?:document|deck|doc\b|file|proposal|contract|agreement|paperwork|questionnaire|nda|quote|pricing|case stud|whitepaper|datasheet|one[- ]?pager|rfp|security (?:review|docs|questionnaire))\b`)},
	{"objection_raised", regexp.MustCompile(`(?i)\b(?:concern(?:ed|s)?|worried|hesitant|not sure|not convinced|push ?back|too expensive|over ?budget|budget (?:concern|constraint|freeze)|security risk|competitor|alternative vendor|cheaper|blocker)\b`)},
	{"conversation_cooled", regexp.MustCompile(`(?i)\b(?:not interested|no longer interested|stop (?:emailing|contacting|reaching out)|unsubscribe|went with|chose (?:a |an |the )?(?:competitor|alternative|vendor)|on hold|postpone|delay (?:this|the)|next quarter|not a priority|deprioritized)\b`)},
	{"new_commitment", regexp.MustCompile(`(?i)\b(?:we'?ll (?:sign|move forward|proceed|go ahead)|let'?s (?:proceed|move forward|get (?:this )?going|sign)|signed|green ?light|approved by|count us in|ready to (?:sign|move|proceed))\b`)},
	{"conversation_advanced", regexp.MustCompile(`(?i)\b(?:let'?s meet|book (?:a )?(?:call|meeting|demo)|schedule (?:a )?(?:call|meeting|demo)|next steps?|looking forward|sounds good|works for (?:me|us)|makes sense|we'?re interested|talk next week)\b`)},
}

// replyTypes are customer-authored text; they always yield `replied` plus whatever the phrases find.
var replyTypes = map[string]bool{
	"EmailReply": true, "EmailReceived": true, "CustomerReplied": true, "SlackMessage": true,
}

// classify maps one linked activity to its reaction types, in a stable order: the type-derived
// reaction first, then phrase rules in declaration order. Caller checked ep.links(a) already.
func classify(ep *episode, a *activity, silenceWindow time.Duration) []string {
	var out []string
	authored := replyTypes[a.Type] && ep.customerAuthored(a)
	switch {
	case authored:
		out = append(out, "replied")
	case a.Type == "MeetingAccepted" && ep.customerAuthored(a):
		out = append(out, "meeting_accepted") // the responder is the actor; our own accept is not the customer's
	case a.Type == "MeetingDeclined" && ep.customerAuthored(a):
		out = append(out, "conversation_cooled")
	case a.Type == "MeetingParticipantAdded" && ep.customerSubject(a):
		out = append(out, "stakeholder_added") // the added attendee is on the customer's side
	case a.Type == "MeetingScheduled" && ep.customerAuthored(a):
		out = append(out, "conversation_advanced") // the customer put time on the calendar
	case a.Type == "ContactAdded" && ep.customerAuthored(a):
		out = append(out, "stakeholder_added") // changed_by is the actor; our own CRM hygiene is not a reaction
	case a.Type == "StakeholderRoleChanged" && ep.customerAuthored(a):
		if t := stakeholderRoleReaction(a); t != "" {
			out = append(out, t)
		}
	case a.Type == "CustomerWentSilent":
		// The clock tick is evidence only when it arrives at least SilenceWindow after the send —
		// silence is never inferred from absent evidence.
		if !a.Occurred.Before(ep.SendAt.Add(silenceWindow)) {
			out = append(out, "ignored")
		}
	}
	if authored {
		text := a.Body + "\n" + a.Summary
		for _, rule := range phraseRules {
			if rule.re.MatchString(text) && !has(out, rule.typ) {
				out = append(out, rule.typ)
			}
		}
	}
	return out
}

// stakeholderRoleReaction reads the selected CRM field's old→new: an empty old value gaining a role
// joins the buying group (stakeholder_added), a role emptied is stakeholder_removed; swapping one
// non-empty role for another is a change, not an add/remove — nothing.
func stakeholderRoleReaction(a *activity) string {
	fc, ok := selectedField(a)
	if !ok {
		return ""
	}
	old, new_ := renderFieldValue(fc.Old), renderFieldValue(fc.New)
	switch {
	case old == "" && new_ != "":
		return "stakeholder_added"
	case new_ == "" && old != "":
		return "stakeholder_removed"
	}
	return ""
}

// outcome is one detected business outcome: its type (business_outcome.v1.json enum) and the
// observed value object.
type outcome struct {
	typ   string
	value map[string]any
}

// classifyOutcomes reads the selected field of a CRM change on the episode's opportunity into a
// business outcome: StageName ranks via reducer.StageRank (unknown ranks are no outcome — never a
// direction guess), closed stages are won/lost on reducer.StageWon, and a numeric Amount change is
// an acv_change. Everything else is not an outcome.
func classifyOutcomes(ep *episode, a *activity) []outcome {
	if a.OpportunityID == "" || a.OpportunityID != ep.OpportunityID {
		return nil
	}
	fc, ok := selectedField(a)
	if !ok {
		return nil
	}
	name := fc.name
	switch {
	case a.Type == "OpportunityStageChanged" && name == "StageName":
		old, new_ := renderFieldValue(fc.Old), renderFieldValue(fc.New)
		if new_ == "" {
			return nil
		}
		typ := stageOutcome(old, new_)
		if typ == "" {
			return nil
		}
		return []outcome{{typ: typ, value: map[string]any{"field": "StageName", "from": old, "to": new_}}}
	case a.Type == "CRMFieldChanged" && name == "Amount" && fc.objectType == "Opportunity":
		oldN, newN, ok := numericField(fc)
		if !ok {
			return nil
		}
		return []outcome{{typ: "acv_change", value: map[string]any{
			"field": "Amount", "from": oldN, "to": newN, "delta": newN - oldN}}}
	}
	return nil
}

// stageOutcome names a stage move. A closed new stage is won/lost before rank is consulted; two
// ranked stages compare directly; anything involving an unraked stage yields no outcome — the
// pipeline vocabulary is not ours to guess.
func stageOutcome(old, new_ string) string {
	if reducer.StageClosed(new_) {
		if reducer.StageWon(new_) {
			return "closed_won"
		}
		return "closed_lost"
	}
	a, aok := reducer.StageRank(old)
	b, bok := reducer.StageRank(new_)
	if !aok || !bok || a == b {
		return ""
	}
	if b > a {
		return "stage_advanced"
	}
	return "stage_regressed"
}

// fieldChange is payload.fields[name] = {"old": …, "new": …}; the selected field is the one
// source_event_key `field:<name>:<rendered>` picked. objectType is the CRM record the change belongs to.
type fieldChange struct {
	name       string
	objectType string
	Old        json.RawMessage
	New        json.RawMessage
}

// selectedField decodes the crm_change payload and returns the field the event key selected.
func selectedField(a *activity) (fieldChange, bool) {
	name, ok := strings.CutPrefix(a.EventKey, "field:")
	if !ok {
		return fieldChange{}, false
	}
	name, _, _ = strings.Cut(name, ":")
	if name == "" {
		return fieldChange{}, false
	}
	var p struct {
		ObjectType string                                `json:"object_type"`
		Fields     map[string]map[string]json.RawMessage `json:"fields"`
	}
	if err := json.Unmarshal(a.Payload, &p); err != nil {
		return fieldChange{}, false
	}
	f, ok := p.Fields[name]
	if !ok {
		return fieldChange{}, false
	}
	return fieldChange{name: name, objectType: p.ObjectType, Old: f["old"], New: f["new"]}, true
}

// renderFieldValue mirrors normalize.renderValue: a JSON string verbatim, anything else compact.
func renderFieldValue(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

// numericField parses old/new as numbers; a missing side is 0 (an empty Amount means none yet).
func numericField(fc fieldChange) (float64, float64, bool) {
	parse := func(raw json.RawMessage) (float64, bool) {
		if len(raw) == 0 || string(raw) == "null" {
			return 0, true
		}
		var n float64
		if err := json.Unmarshal(raw, &n); err != nil {
			var s string
			if err := json.Unmarshal(raw, &s); err == nil {
				if v, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
					return v, true
				}
			}
			return 0, false
		}
		return n, true
	}
	oldN, ok1 := parse(fc.Old)
	newN, ok2 := parse(fc.New)
	if !ok1 || !ok2 || oldN == newN {
		return 0, 0, false
	}
	return oldN, newN, true
}

func has(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
