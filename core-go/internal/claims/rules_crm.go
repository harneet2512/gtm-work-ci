package claims

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type crmField struct {
	New json.RawMessage `json:"new"`
}

type crmChange struct {
	ObjectType string              `json:"object_type"`
	Created    bool                `json:"created"`
	Fields     map[string]crmField `json:"fields"`
}

// field returns the trimmed string held by the new value of a field ("" when absent or not a string).
func (c crmChange) field(name string) string {
	s, _ := StringValue(c.Fields[name].New)
	return strings.TrimSpace(s)
}

// motions maps Salesforce Opportunity.Type onto opportunities.motion.
var motions = map[string]string{
	"expansion": "expansion", "renewal": "renewal", "new business": "new_business", "new": "new_business", "new_business": "new_business",
}

// crmRoles maps Contact.Role__c onto a stakeholder role, or onto a field of its own.
var crmRoles = map[string]string{
	"security approver": "security", "security": "security",
	"technical approver": "technical_evaluator", "technical evaluator": "technical_evaluator", "evaluation owner": "technical_evaluator",
	"legal": "legal", "executive sponsor": "executive_sponsor", "procurement": "procurement",
	"user": "user", "influencer": "influencer", "blocker": "blocker",
}

func (r RuleExtractor) extractCRM(ctx context.Context, act ActivityInput) (RuleResult, error) {
	var ch crmChange
	if err := json.Unmarshal(act.Payload, &ch); err != nil {
		return RuleResult{}, Permanent(fmt.Errorf("claims: crm payload of activity %s: %w", act.ID, err))
	}
	switch ch.ObjectType {
	case "Opportunity":
		return r.crmOpportunity(ctx, act, ch)
	case "Contact":
		return crmContact(act, ch), nil
	}
	return RuleResult{}, nil
}

func (r RuleExtractor) crmOpportunity(ctx context.Context, act ActivityInput, ch crmChange) (RuleResult, error) {
	var res RuleResult
	add := func(f FieldPath, v string) {
		res.Claims = append(res.Claims, newClaim(act, RuleCRM, CRMExplicit, f, MustJSON(v)))
	}
	if v := ch.field("StageName"); v != "" {
		add(FieldStage, v)
	}
	if v := ch.field("NextStep"); v != "" {
		add(FieldNextMilestone, v)
	}
	if v := ch.field("Type"); v != "" {
		if motion, ok := motions[strings.ToLower(v)]; ok {
			add(FieldMotion, motion)
		} else {
			res.Skipped = append(res.Skipped, Skip{Reason: fmt.Sprintf("opportunity Type %q is not a known motion", v)})
		}
	}
	if raw, ok := ch.Fields["Amount"]; ok {
		var amount float64
		if err := json.Unmarshal(raw.New, &amount); err == nil && raw.New != nil && string(raw.New) != "null" {
			res.Claims = append(res.Claims, newClaim(act, RuleCRM, CRMExplicit, FieldAmount, MustJSON(amount)))
		} else if string(raw.New) != "null" {
			res.Skipped = append(res.Skipped, Skip{Reason: "opportunity Amount is not a number"})
		}
	}
	if email := ch.field("OwnerEmail"); email != "" {
		id, found, err := r.lookup(ctx, email)
		if err != nil {
			return RuleResult{}, err
		}
		if found {
			add(FieldOwner, id)
		} else {
			res.Skipped = append(res.Skipped, Skip{Reason: "opportunity owner " + email + " is not a known person"})
		}
	}
	if _, ok := ch.Fields["CloseDate"]; ok {
		res.Skipped = append(res.Skipped, Skip{Reason: "CloseDate is recognised but folds into no account-state field"})
	}
	return res, nil
}

func crmContact(act ActivityInput, ch crmChange) RuleResult {
	person := mentionedPerson(act)
	title, role := ch.field("Title"), ch.field("Role__c")
	if person == "" {
		if title == "" && role == "" {
			return RuleResult{}
		}
		return RuleResult{Skipped: []Skip{{Reason: "contact change is not linked to a person yet"}}}
	}
	var res RuleResult
	subject := func(f FieldPath, v json.RawMessage) {
		c := newClaim(act, RuleCRM, CRMExplicit, f, v)
		c.SubjectPersonID = person
		res.Claims = append(res.Claims, c)
	}
	if title != "" || ch.Created {
		subject(FieldBuyingGroupMember, MustJSON(Member{Title: title}))
	}
	if role == "" {
		return res
	}
	switch mapped, key := crmRoles[strings.ToLower(role)], strings.ToLower(role); {
	case key == "economic buyer":
		subject(FieldEconomicBuyer, MustJSON(person))
	case key == "champion":
		subject(FieldChampion, MustJSON(person))
	case mapped != "":
		subject(FieldStakeholderRole, MustJSON(mapped))
	default:
		res.Skipped = append(res.Skipped, Skip{Reason: fmt.Sprintf("Role__c %q is not mapped to a stakeholder role", role)})
	}
	return res
}

// mentionedPerson is the contact a CRM contact change is about.
func mentionedPerson(act ActivityInput) string {
	for _, p := range act.Participants {
		if p.Role == "mentioned" && p.PersonID != "" {
			return p.PersonID
		}
	}
	return ""
}
