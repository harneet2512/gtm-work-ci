package graph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
)

// Extension implements ingest.Extension: entity creation from CRM events (Prepare) and
// identity resolution plus edge writing for every activity (Finalize).
type Extension struct{}

// NewExtension returns the WP5 identity-resolution extension.
func NewExtension() *Extension { return &Extension{} }

var _ ingest.Extension = (*Extension)(nil)

// Finalize implements ingest.Extension. It runs after the activity and its participants are
// stored and is idempotent.
func (e *Extension) Finalize(ctx context.Context, tx *sql.Tx, in ingest.FinalizeInput) (ingest.FinalizeResult, error) {
	f := &finalizer{tx: tx, in: in, at: in.Activity.OccurredAt()}
	var st *prepared
	if in.Prepared != nil {
		var ok bool
		if st, ok = in.Prepared.(*prepared); !ok {
			return ingest.FinalizeResult{}, fmt.Errorf("graph: Finalize got Prepared of type %T, want *prepared", in.Prepared)
		}
	}
	if st != nil && len(st.mappingIDs) > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE entity_source_mappings SET evidence_activity_id = $1::uuid WHERE id = ANY($2::uuid[])`,
			in.ActivityID, st.mappingIDs); err != nil {
			return ingest.FinalizeResult{}, fmt.Errorf("graph: stamp mapping evidence: %w", err)
		}
	}
	steps := []func(context.Context) error{
		f.loadParticipants, f.syncParticipants, f.createMissingContacts, f.resolveCallSpeakers, f.pairSlackUser,
		f.syncParticipants, f.crmEdges, f.deriveEdges,
	}
	for _, step := range steps {
		if err := step(ctx); err != nil {
			return ingest.FinalizeResult{}, err
		}
	}
	return ingest.FinalizeResult{}, nil
}

type slackPayload struct {
	Kind      string  `json:"kind"`
	User      string  `json:"user"`
	UserEmail *string `json:"user_email"`
}

// pairSlackUser maps a Slack user id nobody has mapped yet to the person that owns the email
// the message carries (rule, 0.9). An existing Slack mapping is never overridden by a claim.
func (f *finalizer) pairSlackUser(ctx context.Context) error {
	if f.in.Event.SourceSystem != "slack" {
		return nil
	}
	var s slackPayload
	if err := json.Unmarshal(f.in.Event.Payload, &s); err != nil {
		return fmt.Errorf("graph: decode slack payload of %s: %w", f.in.Event.SourceObjectID, err)
	}
	mail := strings.ToLower(strings.TrimSpace(derefString(s.UserEmail)))
	user := strings.TrimSpace(s.User)
	if mail == "" || user == "" {
		return nil
	}
	k := SourceKey{System: "slack", Key: user}
	if _, ok, err := CurrentMapping(ctx, f.tx, k); err != nil || ok {
		return err
	}
	person, err := f.personForEmail(ctx, mail)
	if err != nil || person == "" {
		return err
	}
	return f.newMapping(ctx, Mapping{EntityType: EntityPerson, EntityID: person, SourceKey: k,
		Method: MethodRule, Confidence: ruleConfidence,
		Evidence: evidenceJSON(map[string]string{"rule": "slack_user_email", "email": mail})})
}
