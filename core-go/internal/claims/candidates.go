package claims

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxQuoteRunes is the longest evidence quote a claim may carry (claims.evidence_quote CHECK).
const MaxQuoteRunes = 2000

// Candidate mirrors claim_candidate.v1.json: what the worker's extractor proposes for one
// activity. It carries no standing and no ids; core resolves identities and decides both.
type Candidate struct {
	FieldPath       FieldPath       `json:"field_path"`
	Value           json.RawMessage `json:"value"`
	Confidence      float64         `json:"confidence"`
	EvidenceQuote   string          `json:"evidence_quote"`
	SpeakerIdentity string          `json:"speaker_identity,omitempty"`
	SubjectIdentity string          `json:"subject_identity,omitempty"`
	Role            string          `json:"role,omitempty"`
	DueAt           *time.Time      `json:"due_at,omitempty"`
}

// Drop explains why a candidate did not become a claim.
type Drop struct {
	Index  int
	Reason string
}

// Conversion is the outcome of turning candidates into claims.
type Conversion struct {
	Claims  []Claim
	Dropped []Drop
}

var roles = map[string]bool{
	"champion": true, "economic_buyer": true, "technical_evaluator": true, "security": true, "legal": true,
	"executive_sponsor": true, "user": true, "influencer": true, "blocker": true, "procurement": true,
}

// FromCandidates converts worker candidates into claims for one activity. Standing is
// first_party_ai (third_party for enrichment activities); the speaker and subject identities are
// resolved through resolve. A candidate whose quote is not a verbatim substring of the activity
// body, whose confidence is out of range, or whose value cannot be made sense of is dropped with
// a reason rather than stored.
func FromCandidates(act ActivityInput, cands []Candidate, model, version string, resolve Resolver) Conversion {
	var out Conversion
	standing := FirstPartyAI
	if act.SourceSystem == "enrichment" {
		standing = ThirdParty
	}
	for i, cand := range cands {
		claim, err := convertOne(act, cand, resolve)
		if err != nil {
			out.Dropped = append(out.Dropped, Drop{Index: i, Reason: err.Error()})
			continue
		}
		claim.Standing = standing
		claim.Extractor = fmt.Sprintf("llm:%s@%s", model, version)
		out.Claims = append(out.Claims, claim)
	}
	return out
}

func convertOne(act ActivityInput, cand Candidate, resolve Resolver) (Claim, error) {
	if !cand.FieldPath.Valid() || cand.FieldPath == FieldAmount { // amount is the CRM rule's alone (ADR-0016)
		return Claim{}, fmt.Errorf("unknown field path %q", cand.FieldPath)
	}
	if math.IsNaN(cand.Confidence) || cand.Confidence < 0 || cand.Confidence > 1 {
		return Claim{}, fmt.Errorf("confidence %v is outside [0,1]", cand.Confidence)
	}
	if err := checkQuote(act.Body, cand.EvidenceQuote); err != nil {
		return Claim{}, err
	}
	if len(strings.TrimSpace(string(cand.Value))) == 0 || !json.Valid(cand.Value) {
		return Claim{}, errors.New("value is missing or not valid JSON")
	}
	role := strings.ToLower(strings.TrimSpace(cand.Role))
	if role != "" && role != Unknown && !roles[role] {
		return Claim{}, fmt.Errorf("role %q is not a known stakeholder role", cand.Role)
	}
	cand.Role = role
	claim := Claim{
		AccountID: act.AccountID, OpportunityID: act.OpportunityID, FieldPath: cand.FieldPath,
		Confidence: cand.Confidence, SourceActivityID: act.ID, EvidenceQuote: cand.EvidenceQuote,
		OccurredAt: act.OccurredAt, Status: StatusActive,
	}
	if id, ok := resolve(cand.SpeakerIdentity); ok {
		claim.SpeakerPersonID = id
	}
	if id, ok := resolve(cand.SubjectIdentity); ok {
		claim.SubjectPersonID = id
	}
	if err := shapeValue(&claim, cand, resolve); err != nil {
		return Claim{}, err
	}
	return claim, nil
}

func checkQuote(body, quote string) error {
	switch {
	case strings.TrimSpace(quote) == "":
		return errors.New("evidence quote is empty")
	case utf8.RuneCountInString(quote) > MaxQuoteRunes:
		return fmt.Errorf("evidence quote exceeds %d characters", MaxQuoteRunes)
	case !strings.Contains(body, quote):
		return errors.New("evidence quote is not a verbatim substring of the activity text")
	}
	return nil
}

// shapeValue validates and canonicalizes the value for the claim's field.
func shapeValue(c *Claim, cand Candidate, resolve Resolver) error {
	switch c.FieldPath {
	case FieldChampion, FieldEconomicBuyer:
		return shapePersonField(c, cand, resolve)
	case FieldStakeholderRole:
		return shapeRole(c, cand)
	case FieldBuyingGroupMember:
		return shapeMember(c, cand, resolve)
	case FieldDelegation:
		return shapeDelegation(c, cand, resolve)
	case FieldBlockers, FieldObjections, FieldDecisionCriteria, FieldCommitment:
		return shapeListItem(c, cand)
	}
	if s, ok := StringValue(cand.Value); ok {
		s = strings.TrimSpace(s)
		if s == "" {
			return errors.New("value is an empty string")
		}
		c.Value = MustJSON(s)
		return nil
	}
	c.Value = cand.Value
	return nil
}

// valueIdentity is the string a candidate's value holds when it names a person.
func valueIdentity(cand Candidate) string {
	s, _ := StringValue(cand.Value)
	return s
}

func shapePersonField(c *Claim, cand Candidate, resolve Resolver) error {
	if IsUnknown(cand.Value) {
		c.Value, c.SubjectPersonID = MustJSON(Unknown), ""
		return nil
	}
	person := c.SubjectPersonID
	if person == "" {
		person, _ = resolve(valueIdentity(cand))
	}
	if person == "" {
		return fmt.Errorf("cannot resolve the %s to a known person", c.FieldPath)
	}
	c.SubjectPersonID, c.Value = person, MustJSON(person)
	return nil
}

func shapeRole(c *Claim, cand Candidate) error {
	role := cand.Role
	if role == "" || role == Unknown {
		role = strings.ToLower(strings.TrimSpace(valueIdentity(cand)))
	}
	if !roles[role] {
		return fmt.Errorf("stakeholder role %q is not a known role", role)
	}
	if c.SubjectPersonID == "" {
		return errors.New("stakeholder role claim needs a resolvable subject person")
	}
	c.Value = MustJSON(role)
	return nil
}

func shapeMember(c *Claim, cand Candidate, resolve Resolver) error {
	member := ParseMember(cand.Value)
	if member.Role == "" && cand.Role != Unknown {
		member.Role = cand.Role
	}
	if c.SubjectPersonID == "" {
		c.SubjectPersonID, _ = resolve(valueIdentity(cand))
	}
	if c.SubjectPersonID == "" {
		return errors.New("buying-group member claim needs a resolvable subject person")
	}
	c.Value = MustJSON(member)
	return nil
}

func shapeDelegation(c *Claim, cand Candidate, resolve Resolver) error {
	var raw struct {
		From  string `json:"from"`
		To    string `json:"to"`
		Scope string `json:"scope"`
	}
	if s, ok := StringValue(cand.Value); ok {
		raw.To = s
	} else if err := json.Unmarshal(cand.Value, &raw); err != nil {
		return fmt.Errorf("delegation value must be an object or the delegate's identity: %w", err)
	}
	from, to := c.SubjectPersonID, ""
	if raw.From != "" {
		from, _ = resolve(raw.From)
	}
	to, _ = resolve(raw.To)
	if from == "" || to == "" {
		return errors.New("cannot resolve both ends of the delegation to known people")
	}
	if from == to {
		return errors.New("delegation from and to are the same person")
	}
	c.SubjectPersonID = from
	c.Value = MustJSON(Delegation{FromPersonID: from, ToPersonID: to, Scope: strings.TrimSpace(raw.Scope)})
	return nil
}

// shapeListItem canonicalizes the "open: x" string form and the object form into one object, so
// the same item asserted either way hashes to the same value (claims_dedupe_uniq).
func shapeListItem(c *Claim, cand Candidate) error {
	item, err := ParseListItem(cand.Value)
	if err != nil {
		return err
	}
	if item.DueAt == nil && cand.DueAt != nil {
		due := cand.DueAt.UTC()
		item.DueAt = &due
	}
	obj := map[string]any{"text": item.Text, "status": item.Status}
	if item.DueAt != nil {
		obj["due_at"] = item.DueAt.Format(time.RFC3339)
	}
	if item.OwnerPersonID != "" {
		obj["owner_person_id"] = item.OwnerPersonID
	}
	c.Value = MustJSON(obj)
	return nil
}
