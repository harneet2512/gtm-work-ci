package ingest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

// Reasons recorded in unresolved_activities by the default resolver.
const (
	ReasonNoAccountHint  = "no_account_hint"
	ReasonAccountMissing = "account_not_found"
	// ReasonCalendarMismatch: the call's calendar event belongs to an account that nothing on
	// the call or the event corroborates.
	ReasonCalendarMismatch = "calendar_account_mismatch"
)

// Querier is the read access a Resolver gets; it runs inside the ingest transaction so a
// resolver sees (and can rely on) the same snapshot the activity is written in.
type Querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Resolution is where an activity belongs. An empty AccountID means unresolved; Reason then
// explains why (a default is used when it is blank). OpportunityID is only honoured together
// with an AccountID.
type Resolution struct {
	AccountID     string
	OpportunityID string
	Reason        string
}

// Resolver decides the account and opportunity of a freshly normalized activity. It must not
// create accounts, people or claims: that is the identity-resolution work package (WP5),
// which plugs in here later.
type Resolver interface {
	Resolve(ctx context.Context, q Querier, act normalize.Activity) (Resolution, error)
}

// MappingResolver is the minimal deterministic default. Every hint is looked up only in the
// namespace its kind names:
//
//	domain  -> accounts.domain, entity_source_mappings source_system 'domain'
//	crm     -> entity_source_mappings source_system 'crm'
//	slack   -> entity_source_mappings source_system 'slack'
//	calendar-> the already-ingested calendar activity (corroborated, see resolveViaCalendar)
//
// Opportunity hints: kind crm -> 'crm' mappings, kind email_thread -> 'email_thread' mappings.
// An opportunity is attached only if it belongs to the account the account hints resolved.
// Only an opportunity ref on a CRM-sourced event may pin the account by itself: email and
// calendar refs are attacker-influenced and never do.
type MappingResolver struct{}

// Resolve implements Resolver.
func (MappingResolver) Resolve(ctx context.Context, q Querier, act normalize.Activity) (Resolution, error) {
	res, stop, err := resolveAccount(ctx, q, act)
	if err != nil {
		return Resolution{}, err
	}
	if stop {
		return res, nil
	}
	if res.AccountID == "" && act.SourceSystem() == "crm" {
		res, err = pinAccountByCRMOpportunity(ctx, q, act)
		if err != nil {
			return Resolution{}, err
		}
	}
	if res.AccountID == "" {
		res.OpportunityID = ""
		res.Reason = ReasonAccountMissing
		if len(act.AccountHints()) == 0 {
			res.Reason = ReasonNoAccountHint
		}
		return res, nil
	}
	if res.OpportunityID == "" {
		res.OpportunityID, err = resolveOpportunity(ctx, q, act, res.AccountID)
		if err != nil {
			return Resolution{}, err
		}
	}
	return res, nil
}

// resolveAccount walks the account hints in priority order; the first hit wins. stop is true
// when resolution must end with the returned (unresolved) result.
func resolveAccount(ctx context.Context, q Querier, act normalize.Activity) (res Resolution, stop bool, err error) {
	for _, h := range act.AccountHints() {
		switch h.Kind {
		case normalize.HintCalendar:
			link, found, err := calendarLink(ctx, q, h.Value)
			if err != nil {
				return Resolution{}, false, err
			}
			if !found {
				continue
			}
			ok, err := corroborated(ctx, q, act, link)
			if err != nil {
				return Resolution{}, false, err
			}
			if !ok {
				return Resolution{Reason: ReasonCalendarMismatch}, true, nil
			}
			return Resolution{AccountID: link.accountID, OpportunityID: link.opportunityID}, false, nil
		default:
			id, err := accountByHint(ctx, q, h)
			if err != nil {
				return Resolution{}, false, err
			}
			if id != "" {
				return Resolution{AccountID: id}, false, nil
			}
		}
	}
	return Resolution{}, false, nil
}

// accountByHint looks the hint up in its own namespace only.
func accountByHint(ctx context.Context, q Querier, h normalize.Hint) (string, error) {
	var query string
	switch h.Kind {
	case normalize.HintDomain:
		if normalize.IsPersonalDomain(h.Value) {
			return "", nil // a mailbox provider identifies no account
		}
		query = `
SELECT id FROM (
    SELECT a.id::text AS id, 0 AS rank FROM accounts a WHERE a.domain = $1
    UNION ALL
    SELECT a.id::text, 1
      FROM entity_source_mappings m JOIN accounts a ON a.id = m.entity_id
     WHERE m.entity_type = 'account' AND m.valid_to IS NULL AND m.source_system = 'domain' AND m.source_key = $1
) candidates
ORDER BY rank, id LIMIT 1`
	case normalize.HintCRM, normalize.HintSlack:
		query = `
SELECT a.id::text
  FROM entity_source_mappings m JOIN accounts a ON a.id = m.entity_id
 WHERE m.entity_type = 'account' AND m.valid_to IS NULL AND m.source_system = $2 AND m.source_key = $1
 ORDER BY a.id LIMIT 1`
	default:
		return "", nil // other kinds never name an account directly
	}
	args := []any{h.Value}
	if h.Kind != normalize.HintDomain {
		args = append(args, string(h.Kind))
	}
	return scanOptionalString(q.QueryRowContext(ctx, query, args...), "account by hint")
}

// resolveOpportunity attaches the first opportunity hint that maps to an opportunity of the
// given account.
func resolveOpportunity(ctx context.Context, q Querier, act normalize.Activity, accountID string) (string, error) {
	for _, h := range act.OpportunityHints() {
		system, ok := opportunityNamespace(h.Kind)
		if !ok {
			continue
		}
		id, _, err := opportunityByKey(ctx, q, system, h.Value, accountID)
		if err != nil || id != "" {
			return id, err
		}
	}
	return "", nil
}

// pinAccountByCRMOpportunity: a CRM-sourced event naming an opportunity but no resolvable
// account takes the account of that (crm-mapped) opportunity.
func pinAccountByCRMOpportunity(ctx context.Context, q Querier, act normalize.Activity) (Resolution, error) {
	for _, h := range act.OpportunityHints() {
		if h.Kind != normalize.HintCRM {
			continue
		}
		oppID, accountID, err := opportunityByKey(ctx, q, "crm", h.Value, "")
		if err != nil {
			return Resolution{}, err
		}
		if oppID != "" {
			return Resolution{AccountID: accountID, OpportunityID: oppID}, nil
		}
	}
	return Resolution{}, nil
}

// opportunityNamespace maps an opportunity hint kind to its entity_source_mappings system.
func opportunityNamespace(k normalize.HintKind) (string, bool) {
	switch k {
	case normalize.HintCRM:
		return "crm", true
	case normalize.HintEmailThread:
		return "email_thread", true
	default:
		return "", false
	}
}

// opportunityByKey maps (system, key) through a current opportunity mapping; when accountID
// is set the opportunity must belong to it (the activities table enforces the same pairing).
func opportunityByKey(ctx context.Context, q Querier, system, key, accountID string) (oppID, oppAccountID string, err error) {
	const query = `
SELECT o.id::text, o.account_id::text
  FROM entity_source_mappings m JOIN opportunities o ON o.id = m.entity_id
 WHERE m.entity_type = 'opportunity' AND m.valid_to IS NULL AND m.source_system = $1 AND m.source_key = $2
   AND ($3::uuid IS NULL OR o.account_id = $3::uuid)
 ORDER BY m.created_at, m.id LIMIT 1`
	err = q.QueryRowContext(ctx, query, system, key, nullIfEmpty(accountID)).Scan(&oppID, &oppAccountID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("ingest: opportunity by hint: %w", err)
	}
	return oppID, oppAccountID, nil
}

// calendarActivity is the first resolved activity of a calendar event.
type calendarActivity struct {
	id            string
	accountID     string
	opportunityID string
}

func calendarLink(ctx context.Context, q Querier, eventID string) (calendarActivity, bool, error) {
	const query = `
SELECT id::text, account_id::text, opportunity_id::text FROM activities
 WHERE source_system = 'calendar' AND source_object_id = $1 AND account_id IS NOT NULL
 ORDER BY occurred_at, id LIMIT 1`
	var link calendarActivity
	var opp sql.NullString
	err := q.QueryRowContext(ctx, query, eventID).Scan(&link.id, &link.accountID, &opp)
	if errors.Is(err, sql.ErrNoRows) {
		return calendarActivity{}, false, nil
	}
	if err != nil {
		return calendarActivity{}, false, fmt.Errorf("ingest: resolve via calendar: %w", err)
	}
	link.opportunityID = opp.String
	return link, true, nil
}

// corroborated reports whether the account of a calendar event may be taken over by a call:
// the domain of an external speaker on the call, or of an attendee/organizer of the calendar
// activity, must belong to that account. Calendar event ids are connector-supplied, so the
// link alone proves nothing.
func corroborated(ctx context.Context, q Querier, act normalize.Activity, link calendarActivity) (bool, error) {
	domains := map[string]struct{}{}
	for _, p := range act.Participants() {
		if p.Role == normalize.RoleSpeaker {
			addExternalDomain(domains, p.RawIdentity)
		}
	}
	attendees, err := attendeeIdentities(ctx, q, link.id)
	if err != nil {
		return false, err
	}
	for _, raw := range attendees {
		addExternalDomain(domains, raw)
	}
	if len(domains) == 0 {
		return false, nil
	}
	list := make([]string, 0, len(domains))
	for d := range domains {
		list = append(list, d)
	}
	const query = `
SELECT EXISTS (
    SELECT 1 FROM accounts WHERE id = $1::uuid AND domain = ANY($2::text[])
    UNION ALL
    SELECT 1 FROM entity_source_mappings
     WHERE entity_type = 'account' AND entity_id = $1::uuid AND valid_to IS NULL
       AND source_system = 'domain' AND source_key = ANY($2::text[]))`
	var ok bool
	if err := q.QueryRowContext(ctx, query, link.accountID, list).Scan(&ok); err != nil {
		return false, fmt.Errorf("ingest: corroborate calendar link: %w", err)
	}
	return ok, nil
}

// attendeeIdentities returns the raw identities of the calendar activity's organizer and
// attendees, joined into one string per row-set to stay within Querier's single-row access.
func attendeeIdentities(ctx context.Context, q Querier, activityID string) ([]string, error) {
	const query = `
SELECT coalesce(string_agg(raw_identity, E'\n'), '') FROM activity_participants
 WHERE activity_id = $1::uuid AND role IN ('attendee', 'organizer')`
	var joined string
	if err := q.QueryRowContext(ctx, query, activityID).Scan(&joined); err != nil {
		return nil, fmt.Errorf("ingest: calendar attendees: %w", err)
	}
	return splitLines(joined), nil
}

func addExternalDomain(set map[string]struct{}, rawIdentity string) {
	if d := normalize.ExternalDomain(rawIdentity); d != "" {
		set[d] = struct{}{}
	}
}

func scanOptionalString(row *sql.Row, what string) (string, error) {
	var s string
	err := row.Scan(&s)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("ingest: %s: %w", what, err)
	}
	return s, nil
}

// nullIfEmpty maps "" to SQL NULL.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
