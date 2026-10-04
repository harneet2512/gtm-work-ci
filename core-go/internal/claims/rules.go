package claims

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// RuleExtractor turns structured source changes into claims without any model: CRM field
// changes become crm_explicit claims, calendar changes drive next_meeting, enrichment becomes a
// third_party title claim. Free text (email, transcripts, Slack, notes) is not handled here.
type RuleExtractor struct {
	Dir Directory
}

// Extract returns the claims a structured activity implies. Activities from other sources
// yield an empty result. A malformed payload or a directory failure is an error.
func (r RuleExtractor) Extract(ctx context.Context, act ActivityInput) (RuleResult, error) {
	switch act.SourceSystem {
	case "crm":
		return r.extractCRM(ctx, act)
	case "calendar":
		return extractCalendar(act)
	case "enrichment":
		return r.extractEnrichment(ctx, act)
	}
	return RuleResult{}, nil
}

// newClaim returns a claim pre-filled from the activity.
func newClaim(act ActivityInput, extractor string, standing Standing, field FieldPath, value json.RawMessage) Claim {
	return Claim{
		AccountID: act.AccountID, OpportunityID: act.OpportunityID, FieldPath: field, Value: value,
		Standing: standing, Confidence: 1, SourceActivityID: act.ID, OccurredAt: act.OccurredAt,
		Extractor: extractor, Status: StatusActive,
	}
}

func extractCalendar(act ActivityInput) (RuleResult, error) {
	var p struct {
		EventID string `json:"event_id"`
		Title   string `json:"title"`
		Start   string `json:"start"`
		Status  string `json:"status"`
	}
	if err := json.Unmarshal(act.Payload, &p); err != nil {
		return RuleResult{}, Permanent(fmt.Errorf("claims: calendar payload of activity %s: %w", act.ID, err))
	}
	switch act.Type {
	case "MeetingCompleted":
		// Nothing is booked because of this meeting; a still-booked other meeting outranks this null.
		return RuleResult{Claims: []Claim{newClaim(act, RuleCalendar, FirstPartyRecord, FieldNextMeeting, json.RawMessage("null"))}}, nil
	case "MeetingScheduled", "MeetingAccepted", "MeetingParticipantAdded":
		if p.Status != "scheduled" {
			return RuleResult{}, nil
		}
		start, err := time.Parse(time.RFC3339, p.Start)
		if err != nil {
			return RuleResult{}, Permanent(fmt.Errorf("claims: calendar start of activity %s: %w", act.ID, err))
		}
		start = start.UTC()
		if !start.After(act.OccurredAt) {
			return RuleResult{}, nil
		}
		value := MustJSON(map[string]string{"event_id": p.EventID, "title": p.Title, "start": start.Format(time.RFC3339)})
		c := newClaim(act, RuleCalendar, FirstPartyRecord, FieldNextMeeting, value)
		c.ExpiresAt = &start // a booked meeting stops being "next" when it starts
		return RuleResult{Claims: []Claim{c}}, nil
	}
	return RuleResult{}, nil
}

func (r RuleExtractor) extractEnrichment(ctx context.Context, act ActivityInput) (RuleResult, error) {
	var p struct {
		Subject struct {
			Email string `json:"email"`
		} `json:"subject"`
		ObservedAt string `json:"observed_at"`
		Facts      struct {
			Title          string `json:"title"`
			EmployerDomain string `json:"employer_domain"`
		} `json:"facts"`
	}
	if err := json.Unmarshal(act.Payload, &p); err != nil {
		return RuleResult{}, Permanent(fmt.Errorf("claims: enrichment payload of activity %s: %w", act.ID, err))
	}
	email, title := strings.ToLower(strings.TrimSpace(p.Subject.Email)), strings.TrimSpace(p.Facts.Title)
	if email == "" || title == "" {
		return RuleResult{Skipped: []Skip{{Reason: "enrichment without a person email and title folds into no field"}}}, nil
	}
	personID, found, err := r.lookup(ctx, email)
	if err != nil {
		return RuleResult{}, err
	}
	if !found {
		return RuleResult{Skipped: []Skip{{Reason: "enrichment subject " + email + " is not a known person"}}}, nil
	}
	member := Member{Title: title, EmployerDomain: strings.TrimSpace(p.Facts.EmployerDomain)}
	c := newClaim(act, RuleEnrichment, ThirdParty, FieldBuyingGroupMember, MustJSON(member))
	c.SubjectPersonID, c.Confidence = personID, 0.5
	if observed, err := time.Parse(time.RFC3339, p.ObservedAt); err == nil {
		c.OccurredAt = observed.UTC()
	}
	return RuleResult{Claims: []Claim{c}}, nil
}

func (r RuleExtractor) lookup(ctx context.Context, email string) (string, bool, error) {
	if r.Dir == nil {
		return "", false, nil
	}
	id, found, err := r.Dir.PersonIDByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
	if err != nil {
		return "", false, fmt.Errorf("claims: look up %s: %w", email, err)
	}
	return id, found, nil
}
