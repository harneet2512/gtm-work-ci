package ctxgraph

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/graph"
	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
)

// conversationTypes are the activity types that are conversations (a call, a meeting, an email or a
// Slack exchange with people on it); every other activity is a plain Activity.
var conversationTypes = map[string]bool{
	"EmailSent": true, "EmailReceived": true, "EmailReply": true, "CustomerReplied": true,
	"CallStarted": true, "CallEnded": true, "TranscriptReady": true,
	"MeetingScheduled": true, "MeetingAccepted": true, "MeetingDeclined": true, "MeetingCompleted": true, "MeetingParticipantAdded": true,
	"SlackMessage": true, "SlackDecision": true,
}

// visibilityOrg is the only visibility the agent may read without an identity (corectx withholds the rest).
const visibilityOrg = "org"

func (b *builder) loadActivities(ctx context.Context) error {
	rows, err := b.db.QueryContext(ctx, `
SELECT a.id::text, a.source_event_id::text, a.activity_type, a.source_system, a.occurred_at, a.ingested_at,
       a.opportunity_id::text, COALESCE(NULLIF(a.permissions ->> 'visibility', ''), 'none'),
       a.caused_by_activity_id::text, a.correlation_id::text,
       CASE WHEN a.source_system = 'docs' THEN se.payload ->> 'document_id' END
  FROM activities a JOIN source_events se ON se.id = a.source_event_id
 WHERE a.account_id = $1::uuid AND ($2::timestamptz IS NULL OR a.occurred_at < $2)`, b.accountID, b.cutArg())
	if err != nil {
		return fmt.Errorf("ctxgraph: read activities: %w", err)
	}
	defer rows.Close()
	docRaw := map[string]bool{}
	for rows.Next() {
		var id, event, typ, system, visibility string
		var occurred, ingested time.Time
		var opp, caused, corr, doc sql.NullString
		if err := rows.Scan(&id, &event, &typ, &system, &occurred, &ingested, &opp, &visibility, &caused, &corr, &doc); err != nil {
			return err
		}
		label := LabelActivity
		if conversationTypes[typ] {
			label = LabelConversation
		}
		info := actInfo{eventID: event, label: label, visibility: visibility, occurred: occurred}
		if doc.Valid && doc.String != "" {
			info.sourceDocRaw, info.documentID = doc.String, graph.DocumentID(doc.String)
			docRaw[doc.String] = true
		}
		b.acts[id] = info
		extra := map[string]any{"activity_type": typ, "source_system": system, "occurred_at": ts(occurred), "visibility": visibility}
		for k, v := range map[string]sql.NullString{"opportunity_id": opp, "caused_by_activity_id": caused, "correlation_id": corr} {
			if v.Valid {
				extra[k] = v.String
			}
		}
		labels := []string{label}
		if label == LabelConversation {
			labels = []string{LabelConversation, LabelActivity}
		}
		b.addNode(newNode(labels, id, b.accountID, nodeProps("activities", []string{id}, []string{event}, ingested, ingested, extra)))
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return b.loadDocuments(ctx, docRaw)
}

// loadDocuments creates one global Document node per distinct source document. Its properties depend
// on the document alone, so every account that references it projects the identical node.
func (b *builder) loadDocuments(ctx context.Context, raw map[string]bool) error {
	if len(raw) == 0 {
		return nil
	}
	ids := make([]string, 0, len(raw))
	for d := range raw {
		ids = append(ids, d)
	}
	rows, err := b.db.QueryContext(ctx, `
SELECT payload ->> 'document_id', min(received_at) FROM source_events
 WHERE source_system = 'docs' AND payload ->> 'document_id' = ANY($1::text[]) GROUP BY 1`, signalstore.UUIDArray(ids))
	if err != nil {
		return fmt.Errorf("ctxgraph: read documents: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var doc string
		var first time.Time
		if err := rows.Scan(&doc, &first); err != nil {
			return err
		}
		b.addNode(newNode([]string{LabelDocument}, graph.DocumentID(doc), "",
			nodeProps("source_events", []string{}, []string{}, first, first, map[string]any{"source_document_id": doc})))
	}
	return rows.Err()
}
