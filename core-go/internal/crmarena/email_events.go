package crmarena

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// emailPayload is source_payloads.v1.json#/$defs/email.
type emailPayload struct {
	Kind              string    `json:"kind"`
	MessageID         string    `json:"message_id"`
	ThreadID          string    `json:"thread_id"`
	InReplyTo         *string   `json:"in_reply_to"`
	Direction         string    `json:"direction"`
	From              address   `json:"from"`
	To                []address `json:"to"`
	CC                []address `json:"cc,omitempty"`
	Date              time.Time `json:"date"`
	Subject           string    `json:"subject"`
	BodyText          string    `json:"body_text"`
	CRMOpportunityRef *string   `json:"crm_opportunity_ref"`
}

type address struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

// replyPrefix matches one or more leading "Re:" markers.
var replyPrefix = regexp.MustCompile(`(?i)^\s*(re\s*:\s*)+`)

// baseSubject is the subject without reply markers, case- and space-folded.
func baseSubject(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(replyPrefix.ReplaceAllString(s, "")), " "))
}

// datedEmail is an email with its parsed date, for per-deal ordering.
type datedEmail struct {
	msg EmailMessage
	at  time.Time
}

// emails: direction comes from the sender (a rep sends outbound mail; anyone else's mail is inbound),
// because the source's Incoming flag is false on every message. A "Re:" email answers the latest
// earlier email of the same deal with the same base subject (the source has no reply headers).
func (b *builder) emails() error {
	byDeal := map[string][]datedEmail{}
	for _, e := range b.snap.Emails {
		at, err := parseDateTime("EmailMessage.MessageDate", e.MessageDate)
		if err != nil {
			return err
		}
		byDeal[e.RelatedToID] = append(byDeal[e.RelatedToID], datedEmail{msg: e, at: at})
	}
	deals := make([]string, 0, len(byDeal))
	for d := range byDeal {
		deals = append(deals, d)
	}
	sort.Strings(deals)
	for _, deal := range deals {
		list := byDeal[deal]
		sort.Slice(list, func(i, j int) bool {
			if !list[i].at.Equal(list[j].at) {
				return list[i].at.Before(list[j].at)
			}
			return list[i].msg.ID < list[j].msg.ID
		})
		for i, de := range list {
			if err := b.email(de, b.replyTarget(list[:i], de.msg.Subject)); err != nil {
				return err
			}
		}
	}
	return nil
}

// replyTarget returns the message id a "Re:" subject answers, or nil.
func (b *builder) replyTarget(earlier []datedEmail, subject string) *string {
	if !replyPrefix.MatchString(subject) {
		return nil
	}
	want := baseSubject(subject)
	for i := len(earlier) - 1; i >= 0; i-- {
		if baseSubject(earlier[i].msg.Subject) == want {
			b.stats.RepliesLinked++
			return strPtr(earlier[i].msg.ID)
		}
	}
	b.stats.RepliesUnlinked++
	return nil
}

func (b *builder) email(de datedEmail, inReplyTo *string) error {
	e := de.msg
	from, fromRep := b.reps.Address(e.FromAddress)
	to := b.addresses(e.ToAddress)
	if from == "" || len(to) == 0 {
		return fmt.Errorf("crmarena: email %s needs a sender and a recipient", e.ID)
	}
	name := strings.TrimSpace(e.FromName)
	if name == "" {
		name = b.idx.names[strings.ToLower(strings.TrimSpace(e.FromAddress))]
	}
	direction, key := "inbound", "received"
	if fromRep {
		direction, key = "outbound", "sent"
	}
	p := emailPayload{Kind: "email", MessageID: e.ID, ThreadID: oppRef(e.RelatedToID), InReplyTo: inReplyTo, Direction: direction,
		From: address{Email: from, Name: name}, To: to, CC: b.addresses(e.CcAddress), Date: de.at, Subject: e.Subject,
		BodyText: e.TextBody, CRMOpportunityRef: strPtr(oppRef(e.RelatedToID))}
	return b.add("email", e.ID, key, de.at, p, b.idx.accountOf(e.RelatedToID), e.RelatedToID, phaseActivity)
}

// addresses rebinds reps and names everyone the CRM knows.
func (b *builder) addresses(list string) []address {
	var out []address
	for _, raw := range splitAddresses(list) {
		addr, _ := b.reps.Address(raw)
		out = append(out, address{Email: addr, Name: b.idx.names[raw]})
	}
	return out
}
