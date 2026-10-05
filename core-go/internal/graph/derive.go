package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

// isMeeting reports whether an activity type is a meeting or call, where attendance means
// working the opportunity.
func isMeeting(activityType string) bool {
	switch activityType {
	case "MeetingScheduled", "MeetingAccepted", "MeetingDeclined", "MeetingCompleted",
		"MeetingParticipantAdded", "CallEnded", "TranscriptReady":
		return true
	}
	return false
}

// takesPart reports whether a participant role is someone acting in the activity (a "mentioned"
// party is not). It is the one role table: these roles count as participation, may cause a
// contact to be created from an email address, and make an employee a supporter.
func takesPart(role string) bool {
	switch role {
	case normalize.RoleActor, normalize.RoleFrom, normalize.RoleTo, normalize.RoleCC,
		normalize.RoleAttendee, normalize.RoleOrganizer, normalize.RoleSpeaker:
		return true
	}
	return false
}

// actInfo is what the edge rules need to know about an activity.
type actInfo struct {
	id        string
	typ       string
	at        time.Time
	accountID string
	oppID     string
	ownerID   string // owner of the opportunity, "" if unknown or no opportunity
}

// ruleSpec builds a rule-derived edge: a deterministic first-party record (standing
// first_party_record) with the confidence of the identity it rests on.
func ruleSpec(a actInfo, src EntityRef, rel string, dst EntityRef, conf float64, rule string) EdgeSpec {
	return EdgeSpec{Src: src, Rel: rel, Dst: dst, Standing: StandingFirstPartyRecord, Confidence: conf,
		SourceActivityID: a.id, Evidence: evidenceJSON(map[string]string{"rule": rule}), ValidFrom: a.at}
}

// participantEdges writes the edges one resolved participant implies for an activity:
// involves (Activity -involves-> Person, HAR-96 section 5) for every resolved participant,
// mentioned parties included; participated_in (Person -> Activity) for those acting in it; and
// supports for an employee who is not the owner and takes part in a meeting about an
// opportunity (without a known owner nothing distinguishes a supporter from the rep).
func participantEdges(ctx context.Context, q Txn, a actInfo, p participant) error {
	if p.personID == "" {
		return nil
	}
	me, act := EntityRef{EntityPerson, p.personID}, EntityRef{EntityActivity, a.id}
	if _, err := UpsertEdge(ctx, q, ruleSpec(a, act, RelInvolves, me, p.conf, "activity_participant")); err != nil {
		return err
	}
	if !takesPart(p.role) {
		return nil
	}
	if _, err := UpsertEdge(ctx, q, ruleSpec(a, me, RelParticipatedIn, act, p.conf, "participation")); err != nil {
		return err
	}
	if a.oppID != "" && p.kind == "employee" && isMeeting(a.typ) && a.ownerID != "" && p.personID != a.ownerID {
		_, err := UpsertEdge(ctx, q, ruleSpec(a, me, RelSupports, EntityRef{EntityOpportunity, a.oppID}, ruleConfidence, "non_owner_meeting_attendance"))
		return err
	}
	return nil
}

// deriveEdges writes the edges that follow from the activity itself.
func (f *finalizer) deriveEdges(ctx context.Context) error {
	a := actInfo{id: f.in.ActivityID, typ: f.in.Activity.Type(), at: f.at,
		accountID: f.in.Resolution.AccountID, oppID: f.in.Resolution.OpportunityID}
	if a.oppID != "" {
		if err := f.tx.QueryRowContext(ctx, `SELECT coalesce(owner_person_id::text, '') FROM opportunities WHERE id = $1::uuid`, a.oppID).Scan(&a.ownerID); err != nil {
			return fmt.Errorf("graph: opportunity owner: %w", err)
		}
	}
	for _, p := range f.parts {
		if err := participantEdges(ctx, f.tx, a, p); err != nil {
			return err
		}
	}
	if err := f.aboutEdge(ctx, a); err != nil {
		return err
	}
	return f.sharedWith(ctx, a)
}

// aboutEdge links the activity to its opportunity, else its account.
func (f *finalizer) aboutEdge(ctx context.Context, a actInfo) error {
	var dst EntityRef
	switch {
	case a.oppID != "":
		dst = EntityRef{EntityOpportunity, a.oppID}
	case a.accountID != "":
		dst = EntityRef{EntityAccount, a.accountID}
	default:
		return nil
	}
	_, err := UpsertEdge(ctx, f.tx, ruleSpec(a, EntityRef{EntityActivity, a.id}, RelAbout, dst, exactConfidence, "activity_resolution"))
	return err
}

type documentPayload struct {
	Kind       string `json:"kind"`
	DocumentID string `json:"document_id"`
}

// sharedWith links a shared document to each recipient.
func (f *finalizer) sharedWith(ctx context.Context, a actInfo) error {
	if f.in.Event.SourceSystem != "docs" {
		return nil
	}
	var d documentPayload
	if err := json.Unmarshal(f.in.Event.Payload, &d); err != nil {
		return fmt.Errorf("graph: decode document payload of %s: %w", f.in.Event.SourceObjectID, err)
	}
	if d.Kind != "document" || d.DocumentID == "" {
		return fmt.Errorf("graph: docs event %s is not a document payload", f.in.Event.SourceObjectID)
	}
	doc := DocumentRef(d.DocumentID)
	for _, p := range f.parts {
		if p.personID == "" || p.role != normalize.RoleTo {
			continue
		}
		if _, err := UpsertEdge(ctx, f.tx, ruleSpec(a, doc, RelSharedWith, EntityRef{EntityPerson, p.personID}, p.conf, "document_recipient")); err != nil {
			return err
		}
	}
	return nil
}
