package reactions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

// episode is the supervision window of one sent decision: the recipient channel, the conversation
// threads, and the interval (send_at, windowEnd) in which this episode owns follow-on activity.
// Everything inside is candidate evidence; correlation decides what lands.
type episode struct {
	ID            string
	RunID         string
	AccountID     string
	OpportunityID string          // strategy set opportunity, else the run's; "" = unknown
	SendAt        time.Time       // the approved send — the start of the window
	WindowEnd     *time.Time      // the NEXT send on the account, if any; follow-on then feeds that episode
	DecisionID    string          // human_decisions.id the send recorded
	CorrelationID string          // run correlation: activities share the causal batch
	TriggerIDs    []string        // activities that prompted the run — their threads are the channel
	RecipientIDs  []string        // people ids the decision was sent to (final_to + final_cc)
	recipients    map[string]bool // recipient person ids and lower-cased emails
	threads       map[string]bool // thread ids of triggers + linked activities
	knowledgeIDs  []string        // existing knowledge the decision used (candidate + run refs)
	activities    []activity      // the window's activities, occurred order
}

// activity is one scanned row plus the evidence the classifier needs.
type activity struct {
	ID            string
	Type          string
	Occurred      time.Time
	OpportunityID string
	CorrelationID string
	CausedBy      string
	ThreadID      string // email thread id from the source event payload
	EventKey      string // source_events.source_event_key — 'field:<name>:<new>' selects the CRM field
	Body          string
	Summary       string
	Payload       json.RawMessage // source_events.payload — outcome field changes live here
	participants  []participant
}

// participant is activity_participants: the person/address on each side, with its role.
type participant struct {
	Raw      string // raw_identity: an email, a 'slack:U…' / 'crm:003…' handle, …
	Role     string // actor | from | to | cc | bcc | attendee | organizer | speaker | mentioned | owner
	PersonID string
}

// authorRoles mark the side that produced the activity — authorship, not receipt.
var authorRoles = map[string]bool{"from": true, "actor": true, "speaker": true, "organizer": true}

// ScanAccount scans every supervision-open episode on the account for follow-on activity, inserts
// new reactions and outcomes, and records each new row as evidence on the knowledge the decision
// used. Idempotent: stored (activity, type) pairs are skipped before insert.
func (s *Service) ScanAccount(ctx context.Context, accountID string) (ScanResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ScanResult{}, fmt.Errorf("reactions: scan begin: %w", err)
	}
	res, err := s.ScanAccountTx(ctx, tx, accountID)
	if err != nil {
		_ = tx.Rollback()
		return ScanResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ScanResult{}, fmt.Errorf("reactions: scan commit: %w", err)
	}
	return res, nil
}

// ScanAccountTx is ScanAccount inside an existing transaction (a caller that recomputes account
// state after ingest runs it inside the same tx, so supervision is atomic with what it scanned).
func (s *Service) ScanAccountTx(ctx context.Context, tx *sql.Tx, accountID string) (ScanResult, error) {
	eps, err := s.openEpisodes(ctx, tx, accountID)
	if err != nil {
		return ScanResult{}, err
	}
	var res ScanResult
	for i := range eps {
		ep := &eps[i]
		res.Episodes++
		if i+1 < len(eps) { // openEpisodes never sets WindowEnd; the next send bounds this one
			end := eps[i+1].SendAt
			ep.WindowEnd = &end
		}
		if err := s.scanEpisode(ctx, tx, ep, &res); err != nil {
			return res, err
		}
	}
	return res, nil
}

// scanEpisode loads the episode's window of activities, classifies the correlated ones, inserts the
// new reactions/outcomes, and applies all of it as knowledge evidence.
func (s *Service) scanEpisode(ctx context.Context, tx *sql.Tx, ep *episode, res *ScanResult) error {
	if err := ep.loadWindow(ctx, tx); err != nil {
		return err
	}
	if err := s.applyDecisionEvidence(ctx, tx, ep, res); err != nil {
		return err
	}
	for i := range ep.activities {
		a := &ep.activities[i]
		if !ep.links(a) {
			continue
		}
		if a.ThreadID != "" {
			ep.threads[a.ThreadID] = true // a linked reply extends the thread the episode watches
		}
		for _, r := range classify(ep, a, s.silenceWindow) {
			id, err := insertReaction(ctx, tx, ep, a, r, s.clk.Now())
			if errors.Is(err, errDupReaction) {
				continue
			}
			if err != nil {
				return err
			}
			res.Reactions++
			err = s.applyRowEvidence(ctx, tx, ep, res, knowledge.Evidence{
				Kind: knowledge.EvidenceCustomerReaction, RefID: id, Polarity: polarityOf[r], At: a.Occurred,
			})
			if err != nil {
				return err
			}
		}
		for _, o := range classifyOutcomes(ep, a) {
			id, err := insertOutcome(ctx, tx, ep, a, o)
			if errors.Is(err, errDupOutcome) {
				continue
			}
			if err != nil {
				return err
			}
			res.Outcomes++
			err = s.applyRowEvidence(ctx, tx, ep, res, knowledge.Evidence{
				Kind: knowledge.EvidenceBusinessOutcome, RefID: id, OutcomeType: o.typ, At: a.Occurred,
			})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// links reports whether an in-window activity belongs to the episode's channel. The order is the
// confidence order; any one suffices, none claims to "cause" anything — windowed correlation.
func (ep *episode) links(a *activity) bool {
	switch {
	case ep.CorrelationID != "" && a.CorrelationID == ep.CorrelationID:
		return true // pipeline-carried correlation: the strongest deterministic link
	case isTrigger(a.CausedBy, ep.TriggerIDs):
		return true // activity caused by a trigger of the run
	case a.ThreadID != "" && ep.threads[a.ThreadID]:
		return true // same conversation thread (never sufficient to pin an account — window+account bound it)
	}
	switch a.Type {
	case "EmailReply", "EmailReceived", "CustomerReplied", "SlackMessage":
		return ep.customerAuthored(a) // a reply links through WHO wrote it, on the watched channel
	case "ContactAdded", "StakeholderRoleChanged", "CustomerWentSilent":
		return true // account-scope events: the account is the channel
	}
	if a.OpportunityID != "" && a.OpportunityID == ep.OpportunityID {
		return true // meetings/crm events on the episode's opportunity
	}
	return ep.involvesRecipient(a)
}

// isTrigger reports whether causedBy is one of the run's trigger activities.
func isTrigger(causedBy string, triggers []string) bool {
	if causedBy == "" {
		return false
	}
	for _, t := range triggers {
		if causedBy == t {
			return true
		}
	}
	return false
}

// involvesRecipient: any participant (any role) is a sent recipient — a meeting invite reaches the
// channel when a recipient sits on it, regardless of who organized it.
func (ep *episode) involvesRecipient(a *activity) bool {
	for _, p := range a.participants {
		if p.PersonID != "" && ep.recipients[p.PersonID] {
			return true
		}
		if isEmail(p.Raw) && ep.recipients[strings.ToLower(p.Raw)] {
			return true
		}
	}
	return false
}

// customerAuthored: an author-side participant is a sent recipient or an external address — never
// our own side, so our own sends/forwards never read as customer reactions.
func (ep *episode) customerAuthored(a *activity) bool {
	for _, p := range a.participants {
		if !authorRoles[p.Role] {
			continue
		}
		if p.PersonID != "" && ep.recipients[p.PersonID] {
			return true
		}
		if !isEmail(p.Raw) {
			continue // 'slack:U…'/'crm:003…' raws carry no authorship we can compare
		}
		if ep.recipients[strings.ToLower(p.Raw)] {
			return true
		}
		if !normalize.IsOurEmail(p.Raw) {
			return true // external author on the account's channel
		}
	}
	return false
}

// customerSubject: the person this activity is ABOUT (an added attendee, a new stakeholder) is on the
// customer's side — a sent recipient or a non-internal address. Our own people joining a meeting or
// being added to our own records never read as the customer gaining a stakeholder.
func (ep *episode) customerSubject(a *activity) bool {
	for _, p := range a.participants {
		if p.PersonID != "" && ep.recipients[p.PersonID] {
			return true
		}
		if isEmail(p.Raw) && !normalize.IsOurEmail(p.Raw) {
			return true
		}
	}
	return false
}
