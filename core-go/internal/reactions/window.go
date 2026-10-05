package reactions

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// openEpisodes loads the account's sent episodes in send order, with the channel and the knowledge
// each one used. `send_decision='send'` pins the window to an approved action; constraint
// human_strategy_decisions_send_is_complete makes send_decided_at, final_to and human_decision_id
// non-null in that case.
func (s *Service) openEpisodes(ctx context.Context, tx *sql.Tx, accountID string) ([]episode, error) {
	rows, err := tx.QueryContext(ctx, `SELECT e.id::text, e.agent_run_id::text, h.send_decided_at,
       h.final_to::text, COALESCE(h.final_cc::text, '[]'), e.human_decision_id::text,
       COALESCE(ar.correlation_id::text, ''), COALESCE(to_jsonb(ar.trigger_activity_ids), '[]'::jsonb)::text,
       COALESCE(ar.knowledge_refs_used, '[]'::jsonb)::text,
       COALESCE(to_jsonb(sc.knowledge_refs), '[]'::jsonb)::text,
       COALESCE(ss.opportunity_id, ar.opportunity_id)::text
	FROM decision_episodes e
	JOIN human_strategy_decisions h ON h.decision_episode_id = e.id AND h.send_decision = 'send'
	JOIN agent_runs ar ON ar.id = e.agent_run_id
	LEFT JOIN strategy_candidates sc ON sc.id = h.selected_candidate_id
	LEFT JOIN strategy_sets ss ON ss.id = h.strategy_set_id
	WHERE e.account_id = $1::uuid AND e.status IN ('decided', 'judged')
	ORDER BY h.send_decided_at, e.id`, accountID)
	if err != nil {
		return nil, fmt.Errorf("reactions: episodes: %w", err)
	}
	defer rows.Close()
	var eps []episode
	for rows.Next() {
		var ep episode
		var to, cc, triggers, used, candidateRefs string
		var opp sql.NullString
		if err := rows.Scan(&ep.ID, &ep.RunID, &ep.SendAt, &to, &cc, &ep.DecisionID, &ep.CorrelationID,
			&triggers, &used, &candidateRefs, &opp); err != nil {
			return nil, fmt.Errorf("reactions: episode scan: %w", err)
		}
		ep.AccountID = accountID
		if opp.Valid {
			ep.OpportunityID = opp.String
		}
		ep.RecipientIDs = append(recipientIDs(json.RawMessage(to)), recipientIDs(json.RawMessage(cc))...)
		ep.TriggerIDs = stringArray(json.RawMessage(triggers))
		ep.knowledgeIDs = dedupeStrings(append(stringArray(json.RawMessage(used)), stringArray(json.RawMessage(candidateRefs))...))
		eps = append(eps, ep)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reactions: episode rows: %w", err)
	}
	for i := range eps {
		if err := eps[i].loadChannel(ctx, tx); err != nil {
			return nil, err
		}
	}
	return eps, nil
}

// recipientIDs extracts the person_id of every Recipient in a final_to/final_cc jsonb array.
func recipientIDs(raw json.RawMessage) []string {
	var rs []struct {
		PersonID string `json:"person_id"`
	}
	if err := json.Unmarshal(raw, &rs); err != nil {
		return nil
	}
	ids := make([]string, 0, len(rs))
	for _, r := range rs {
		if r.PersonID != "" {
			ids = append(ids, r.PersonID)
		}
	}
	return ids
}

// loadChannel fills recipients (people ids → their emails), trigger threads, and the set of
// knowledge refs that resolve to real rows — refs to missing knowledge are ignored, not errored.
func (ep *episode) loadChannel(ctx context.Context, tx *sql.Tx) error {
	ep.recipients = map[string]bool{}
	ep.threads = map[string]bool{}
	for _, id := range ep.RecipientIDs {
		ep.recipients[id] = true
	}
	if len(ep.RecipientIDs) > 0 {
		rows, err := tx.QueryContext(ctx,
			`SELECT id::text, primary_email FROM people WHERE id = ANY($1::uuid[])`, uuidList(ep.RecipientIDs))
		if err != nil {
			return fmt.Errorf("reactions: recipients: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var email sql.NullString
			if err := rows.Scan(&id, &email); err != nil {
				return fmt.Errorf("reactions: recipient scan: %w", err)
			}
			if email.Valid {
				ep.recipients[email.String] = true // primary_email is stored lower-cased
			}
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("reactions: recipient rows: %w", err)
		}
	}
	if len(ep.TriggerIDs) > 0 {
		threads, err := queryStrings(ctx, tx,
			`SELECT se.payload->>'thread_id' FROM activities a
			 JOIN source_events se ON se.id = a.source_event_id
			 WHERE a.id = ANY($1::uuid[]) AND se.payload->>'thread_id' IS NOT NULL`, uuidList(ep.TriggerIDs))
		if err != nil {
			return fmt.Errorf("reactions: trigger threads: %w", err)
		}
		for _, t := range threads {
			if t != "" {
				ep.threads[t] = true
			}
		}
	}
	if len(ep.knowledgeIDs) > 0 {
		existing, err := queryStrings(ctx, tx,
			`SELECT id::text FROM knowledge WHERE id = ANY($1::uuid[])`, uuidList(ep.knowledgeIDs))
		if err != nil {
			return fmt.Errorf("reactions: knowledge: %w", err)
		}
		ep.knowledgeIDs = existing
	}
	return nil
}

// queryStrings returns one string column of a single-argument query.
func queryStrings(ctx context.Context, tx *sql.Tx, query, arg string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, query, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// loadWindow reads the account's activities inside (send_at, windowEnd) in occurred order.
func (ep *episode) loadWindow(ctx context.Context, tx *sql.Tx) error {
	query := `SELECT a.id::text, a.activity_type, a.occurred_at, COALESCE(a.opportunity_id::text, ''),
       COALESCE(a.correlation_id::text, ''), COALESCE(a.caused_by_activity_id::text, ''),
       COALESCE(se.payload->>'thread_id', ''), COALESCE(se.source_event_key, ''),
       COALESCE(a.body_text, ''), COALESCE(a.summary, ''),
       COALESCE(se.payload, 'null'::jsonb)::text
	FROM activities a
	LEFT JOIN source_events se ON se.id = a.source_event_id
	WHERE a.account_id = $1::uuid AND a.occurred_at > $2`
	args := []any{ep.AccountID, ep.SendAt}
	if ep.WindowEnd != nil {
		// <= the next send: an activity at the exact boundary instant belongs to the window that was
		// open when it arrived, not to the episode the next send opens.
		query += ` AND a.occurred_at <= $3`
		args = append(args, *ep.WindowEnd)
	}
	query += ` ORDER BY a.occurred_at, a.id`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("reactions: activities: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var a activity
		var payload string
		if err := rows.Scan(&a.ID, &a.Type, &a.Occurred, &a.OpportunityID, &a.CorrelationID,
			&a.CausedBy, &a.ThreadID, &a.EventKey, &a.Body, &a.Summary, &payload); err != nil {
			return fmt.Errorf("reactions: activity scan: %w", err)
		}
		a.Payload = json.RawMessage(payload)
		ep.activities = append(ep.activities, a)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reactions: activity rows: %w", err)
	}
	return ep.loadParticipants(ctx, tx)
}

// loadParticipants fills every scanned activity's participants in one grouped query.
func (ep *episode) loadParticipants(ctx context.Context, tx *sql.Tx) error {
	if len(ep.activities) == 0 {
		return nil
	}
	ids := make([]string, len(ep.activities))
	pos := map[string]int{}
	for i, a := range ep.activities {
		ids[i] = a.ID
		pos[a.ID] = i
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT activity_id::text, raw_identity, role, COALESCE(person_id::text, '')
		 FROM activity_participants WHERE activity_id = ANY($1::uuid[])`, uuidList(ids))
	if err != nil {
		return fmt.Errorf("reactions: participants: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var aid string
		var p participant
		if err := rows.Scan(&aid, &p.Raw, &p.Role, &p.PersonID); err != nil {
			return fmt.Errorf("reactions: participant scan: %w", err)
		}
		if i, ok := pos[aid]; ok {
			ep.activities[i].participants = append(ep.activities[i].participants, p)
		}
	}
	return rows.Err()
}

// stringArray decodes a jsonb array of uuid strings (to_jsonb(uuid[]) / knowledge_refs_used).
func stringArray(raw json.RawMessage) []string {
	var parsed []any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil
	}
	ids := make([]string, 0, len(parsed))
	for _, v := range parsed {
		if s, ok := v.(string); ok && s != "" {
			ids = append(ids, s)
		}
	}
	return ids
}

// dedupeStrings drops repeated ids — a knowledge row cited by both the run and the chosen candidate
// is evidenced once per scan.
func dedupeStrings(ids []string) []string {
	seen := map[string]bool{}
	out := ids[:0]
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// uuidList renders uuid strings as a Postgres array literal for `= ANY($1::uuid[])` — uuids are
// quote-free, so the literal is safe without escaping.
func uuidList(ids []string) string {
	return "{" + strings.Join(ids, ",") + "}"
}

func isEmail(raw string) bool { return strings.Contains(raw, "@") }
