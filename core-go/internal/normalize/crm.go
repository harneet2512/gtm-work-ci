package normalize

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
)

const keyFieldPrefix = "field:"

type crmFieldChange struct {
	Old json.RawMessage `json:"old"`
	New json.RawMessage `json:"new"`
}

type crmPayload struct {
	Kind            string                    `json:"kind"`
	ObjectType      string                    `json:"object_type"`
	RecordID        string                    `json:"record_id"`
	AccountRecordID *string                   `json:"account_record_id"`
	ChangedAt       time.Time                 `json:"changed_at"`
	ChangedBy       string                    `json:"changed_by"`
	Created         bool                      `json:"created"`
	Fields          map[string]crmFieldChange `json:"fields"`
	NoteBody        *string                   `json:"note_body"`
	// OpportunityRecordID links a Task, Quote, Contract or Order record to its opportunity.
	OpportunityRecordID *string `json:"opportunity_record_id"`
	// Description is the free text of a Task, Quote, Contract or Order record (its body text).
	Description *string `json:"description"`
}

// crmChange is the single change an event key selects out of a crm_change payload.
type crmChange struct {
	activityType string
	field        string // empty for "created"
	value        string // rendered new value
}

// isCreation: a "created" event selects no field.
func (c crmChange) isCreation() bool { return c.field == "" }

// creationTypes maps the record types whose creation is an activity of its own (HAR-130 rows).
var creationTypes = map[string]string{
	"Contact": "ContactAdded", "Account": "CRMFieldChanged", "Opportunity": "CRMFieldChanged",
	"Task": "CRMTaskLogged", "Quote": "QuoteCreated", "Contract": "ContractSigned", "Order": "OrderPlaced",
}

// describedTypes carry their body text in description.
var describedTypes = map[string]bool{"Task": true, "Quote": true, "Contract": true, "Order": true}

// normalizeCRM implements the CRM rows of the mapping table.
func normalizeCRM(ev SourceEvent) (Activity, error) {
	key := ev.SourceEventKey
	if key != "created" && !strings.HasPrefix(key, keyFieldPrefix) {
		return Activity{}, unsupported("crm event key %q has no mapping (want created or field:<name>:<new>)", key)
	}
	p, err := decodePayload[crmPayload](ev, "crm_change")
	if err != nil {
		return Activity{}, err
	}
	if err := validateCRM(ev, p); err != nil {
		return Activity{}, err
	}
	change, err := selectChange(key, p)
	if err != nil {
		return Activity{}, err
	}

	act := newActivity(ev, change.activityType, p.ChangedAt)
	act.participants = crmParticipants(change.activityType, p)
	act.accountHints, act.opportunityHints = crmHints(p)
	if change.isCreation() && p.ObjectType == "Account" {
		act.accountHints = append(act.accountHints, accountDomainHints(p)...)
	}
	switch {
	case change.activityType == "CRMNoteAdded":
		act.bodyText = *p.NoteBody
	case describedTypes[p.ObjectType]:
		act.bodyText = deref(p.Description)
	}
	act.summary = summarize(crmSummary(change, p))
	return act, nil
}

func validateCRM(ev SourceEvent, p crmPayload) error {
	if _, ok := creationTypes[p.ObjectType]; !ok && p.ObjectType != "Note" {
		return invalid("crm object_type %q is not Account, Contact, Opportunity, Note, Task, Quote, Contract or Order", p.ObjectType)
	}
	if p.ObjectType == "Opportunity" && deref(p.OpportunityRecordID) != "" {
		return invalid("an Opportunity record is its own opportunity; opportunity_record_id must be empty")
	}
	if err := requireObjectID(ev, p.RecordID); err != nil {
		return err
	}
	if p.ChangedAt.IsZero() || strings.TrimSpace(p.ChangedBy) == "" {
		return invalid("crm change requires changed_at and changed_by")
	}
	return nil
}

// selectChange resolves the event key against the payload and picks the activity type.
func selectChange(key string, p crmPayload) (crmChange, error) {
	if key == "created" {
		if !p.Created {
			return crmChange{}, invalid("event key \"created\" requires created=true in the payload")
		}
		return creationChange(p)
	}

	name, value, ok := strings.Cut(strings.TrimPrefix(key, keyFieldPrefix), ":")
	if !ok || name == "" {
		return crmChange{}, invalid("event key %q must look like field:<name>:<new>", key)
	}
	fc, present := p.Fields[name]
	if !present || fc.New == nil {
		return crmChange{}, invalid("event key names field %q, which has no new value in the payload", name)
	}
	if rendered := renderValue(fc.New); rendered != value {
		return crmChange{}, invalid("event key value %q does not match the payload value %q for %s", value, rendered, name)
	}
	return crmChange{activityType: fieldActivityType(p.ObjectType, name), field: name, value: value}, nil
}

// creationChange picks the activity of a "created" event (no field selected).
func creationChange(p crmPayload) (crmChange, error) {
	switch p.ObjectType {
	case "Note":
		if strings.TrimSpace(deref(p.NoteBody)) == "" {
			return crmChange{}, invalid("a created Note requires a non-empty note_body")
		}
		return crmChange{activityType: "CRMNoteAdded"}, nil
	case "Opportunity":
		if fc, ok := p.Fields["StageName"]; ok && fc.New != nil {
			return crmChange{}, invalid("an Opportunity created with a StageName is keyed field:StageName:<stage>")
		}
	}
	return crmChange{activityType: creationTypes[p.ObjectType]}, nil
}

func fieldActivityType(objectType, field string) string {
	switch {
	case objectType == "Opportunity" && field == "StageName":
		return "OpportunityStageChanged"
	case objectType == "Contact" && (field == "Role__c" || field == "Title"):
		return "StakeholderRoleChanged"
	default:
		return "CRMFieldChanged"
	}
}

// renderValue is how a field value appears inside an event key: strings verbatim, anything
// else as compact JSON.
func renderValue(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var buf bytes.Buffer
	if json.Compact(&buf, raw) != nil {
		return string(raw)
	}
	return buf.String()
}

// crmParticipants: contact-centred activities mention the contact AND record who made the change —
// supervision gates "customer" reactions on authorship (HAR-120) — the rest record who made it.
func crmParticipants(activityType string, p crmPayload) []Participant {
	actor := strings.TrimSpace(p.ChangedBy)
	if norm, ok := normalizeEmailAddress(actor); ok {
		actor = norm
	}
	if activityType == "ContactAdded" || activityType == "StakeholderRoleChanged" {
		return []Participant{{RawIdentity: "crm:" + p.RecordID, Role: RoleMentioned},
			{RawIdentity: actor, Role: RoleActor}}
	}
	return []Participant{{RawIdentity: actor, Role: RoleActor}}
}

// crmHints: account records identify themselves when they carry no account link; an
// Opportunity record is its own opportunity hint. All are CRM ids.
func crmHints(p crmPayload) (account, opportunity []Hint) {
	account = hintOf(HintCRM, deref(p.AccountRecordID))
	if account == nil && p.ObjectType == "Account" {
		account = hintOf(HintCRM, p.RecordID)
	}
	if p.ObjectType == "Opportunity" {
		opportunity = hintOf(HintCRM, p.RecordID)
	} else {
		opportunity = hintOf(HintCRM, deref(p.OpportunityRecordID))
	}
	return account, opportunity
}

func crmSummary(c crmChange, p crmPayload) string {
	switch c.activityType {
	case "OpportunityStageChanged":
		return p.RecordID + " stage changed to " + c.value
	case "ContactAdded":
		return "Contact " + p.RecordID + " added"
	case "CRMNoteAdded":
		return "CRM note added on " + p.RecordID
	case "CRMTaskLogged":
		return withDetail("Task "+p.RecordID+" logged", fieldText(p, "Subject"))
	case "QuoteCreated":
		return "Quote " + p.RecordID + " created"
	case "ContractSigned":
		return "Contract " + p.RecordID + " signed"
	case "OrderPlaced":
		return "Order " + p.RecordID + " placed"
	default:
		if c.isCreation() {
			return p.ObjectType + " " + p.RecordID + " created"
		}
		return p.RecordID + " " + c.field + " changed to " + c.value
	}
}

// fieldText is the rendered new value of a field, "" when absent.
func fieldText(p crmPayload, name string) string {
	fc, ok := p.Fields[name]
	if !ok || fc.New == nil {
		return ""
	}
	return renderValue(fc.New)
}

// accountDomainFields are the CRM fields that may carry an account's web domain.
var accountDomainFields = []string{"Website", "Domain", "Domain__c"}

// accountDomainHints returns the customer domain announced by a newly created Account so
// resolution can match accounts.domain even before a CRM mapping exists.
func accountDomainHints(p crmPayload) []Hint {
	for _, name := range accountDomainFields {
		fc, ok := p.Fields[name]
		if !ok || fc.New == nil {
			continue
		}
		if d := DomainFromWebsite(renderValue(fc.New)); d != "" {
			return domainHint(d)
		}
	}
	return nil
}

// DomainFromWebsite extracts a lower-case host from "acme.com", "https://www.acme.com/x" and
// similar. Our own domain and anything that is not a dotted hostname yield "".
func DomainFromWebsite(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if _, rest, ok := strings.Cut(s, "://"); ok {
		s = rest
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, ":"); i >= 0 {
		s = s[:i] // port
	}
	s = strings.TrimPrefix(s, "www.")
	d, ok := asciiDomain(s)
	if !ok || isInternalDomain(d) || IsPersonalDomain(d) {
		return ""
	}
	return d
}
