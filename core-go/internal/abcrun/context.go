package abcrun

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// context describes the situation for a blind reviewer: the account at the trigger, the customer's email, and the
// people of the account and of the seller's team. It is read from the database the arms ran on, so a reviewer sees
// what the agent could see.
func (r *Runner) context(ctx context.Context, s Situation, ref Ref) (ContextOut, error) {
	var state []byte
	if err := r.o.DB.QueryRowContext(ctx, `SELECT state FROM account_state WHERE account_id = $1::uuid`, ref.AccountID).Scan(&state); err != nil {
		return ContextOut{}, fmt.Errorf("read account state: %w", err)
	}
	out := ContextOut{StateHeader: stateHeader(state), TriggerSummary: triggerSummary(s)}
	titles := map[string]Person{}
	for _, p := range s.People {
		titles[strings.ToLower(p.Email)] = p
	}
	rows, err := r.o.DB.QueryContext(ctx, `SELECT id::text, kind, display_name, coalesce(primary_email, '') FROM people
 WHERE kind = 'employee' OR account_id = $1::uuid ORDER BY kind DESC, display_name, id`, ref.AccountID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, kind, name, email string
		if err := rows.Scan(&id, &kind, &name, &email); err != nil {
			return out, err
		}
		p := PersonOut{PersonID: id, Email: email, Name: name, Side: "buyer"}
		if kind == "employee" {
			p.Side = "seller"
		}
		if known, ok := titles[strings.ToLower(email)]; ok {
			p.Title = known.Title
		}
		out.People = append(out.People, p)
	}
	return out, rows.Err()
}

type listField struct {
	Value []struct {
		Text   string `json:"text"`
		Status string `json:"status"`
	} `json:"value"`
}

// stateHeader is a short human summary of the account state: stage and what is open.
func stateHeader(raw []byte) string {
	var st struct {
		Name   string `json:"account_name"`
		Fields struct {
			Stage struct {
				Value any `json:"value"`
			} `json:"stage"`
			Objections listField `json:"objections"`
			Blockers   listField `json:"blockers"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return "account state unavailable"
	}
	parts := []string{fmt.Sprintf("%s: stage %v", orAccount(st.Name), st.Fields.Stage.Value)}
	for _, o := range st.Fields.Objections.Value {
		parts = append(parts, "open objection: "+o.Text)
	}
	for _, b := range st.Fields.Blockers.Value {
		parts = append(parts, "open blocker: "+b.Text)
	}
	return strings.Join(parts, "; ")
}

func orAccount(s string) string {
	if s == "" {
		return "Account"
	}
	return s
}

// triggerSummary renders the trigger event for review: the customer's email, or the CRM change.
func triggerSummary(s Situation) string {
	for _, e := range s.Events {
		if e.SourceObjectID != s.Trigger.SourceObjectID || e.SourceEventKey != s.Trigger.SourceEventKey {
			continue
		}
		var p struct {
			Subject string `json:"subject"`
			Body    string `json:"body_text"`
			From    struct {
				Name  string `json:"name"`
				Email string `json:"email"`
			} `json:"from"`
		}
		if json.Unmarshal(e.Payload, &p) == nil && p.Body != "" {
			return fmt.Sprintf("Inbound email from %s <%s>, subject %q:\n\n%s", p.From.Name, p.From.Email, p.Subject, p.Body)
		}
		return fmt.Sprintf("%s %s %s", e.SourceSystem, e.SourceObjectID, e.SourceEventKey)
	}
	return "trigger event"
}
