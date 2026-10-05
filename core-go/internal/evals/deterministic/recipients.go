package deterministic

import (
	"fmt"
	"strings"
)

// HAR-97 §4 "Recipient correctness" sub-checks.
const (
	CheckRecipientsExist        Check = "recipient.exists"
	CheckRecipientBelongs       Check = "recipient.belongs_to_account"
	CheckRecipientsNotDuplicate Check = "recipient.no_duplicates"
	CheckInternalNotExternal    Check = "recipient.internal_only_not_externalized"
)

var recipientChecks = []Check{CheckRecipientsExist, CheckRecipientBelongs, CheckRecipientsNotDuplicate, CheckInternalNotExternal}

// RecipientCorrectness is the HAR-97 §4 "Recipient correctness" eval.
func RecipientCorrectness(in Input) Judgment {
	var f []Finding
	f = append(f, RecipientsExist(in)...)
	f = append(f, RecipientsBelongToAccount(in)...)
	f = append(f, RecipientsNotDuplicated(in)...)
	f = append(f, InternalOnlyNotExternalized(in)...)
	return judge(in, TypeRecipient, recipientChecks, []string{"buying_group"}, f)
}

// RecipientsExist: every recipient is a live (unmerged) person, reachable over email when the
// action is an email.
func RecipientsExist(in Input) []Finding {
	people := personIndex(in)
	email := in.Draft.FinishedArtifact.Channel == ChannelEmail
	var out []Finding
	for _, r := range in.Draft.Recipients {
		p, ok := people[r.PersonID]
		switch {
		case !ok:
			out = append(out, failure(CheckRecipientsExist, fmt.Sprintf("recipient %s is not a known person", r.PersonID),
				"Address only people in the account's directory.").blocking())
		case p.MergedInto != nil:
			out = append(out, failure(CheckRecipientsExist,
				fmt.Sprintf("recipient %s was merged into %s", r.PersonID, *p.MergedInto),
				fmt.Sprintf("Address %s instead of the merged record %s.", *p.MergedInto, r.PersonID)).blocking())
		case email && !p.HasEmail:
			out = append(out, failure(CheckRecipientsExist, fmt.Sprintf("%s has no email address", p.DisplayName),
				fmt.Sprintf("Use another channel for %s or drop them.", p.DisplayName)).blocking())
		}
	}
	return out
}

// RecipientsBelongToAccount: every recipient is our employee or a current contact of the run's
// account (a departed buying-group member no longer belongs to it).
func RecipientsBelongToAccount(in Input) []Finding {
	people := personIndex(in)
	departed := departedMembers(in)
	var out []Finding
	for _, r := range in.Draft.Recipients {
		p, ok := people[r.PersonID]
		if !ok || p.Kind == KindEmployee {
			continue // existence is RecipientsExist's finding; employees are our org
		}
		switch {
		case p.AccountID == nil:
			out = append(out, failure(CheckRecipientBelongs,
				fmt.Sprintf("%s is not resolved to any account", p.DisplayName),
				fmt.Sprintf("Confirm %s belongs to this account before addressing them.", p.DisplayName)).blocking())
		case *p.AccountID != in.AccountID:
			out = append(out, failure(CheckRecipientBelongs,
				fmt.Sprintf("%s belongs to account %s, not the run's account %s", p.DisplayName, *p.AccountID, in.AccountID),
				fmt.Sprintf("Remove %s: they are another account's contact.", p.DisplayName)).blocking())
		case departed[r.PersonID]:
			out = append(out, failure(CheckRecipientBelongs,
				fmt.Sprintf("%s has departed the account (buying group status departed)", p.DisplayName),
				fmt.Sprintf("Remove %s; address their successor or delegate.", p.DisplayName)).blocking().withState("buying_group"))
		}
	}
	return out
}

func departedMembers(in Input) map[string]bool {
	out := map[string]bool{}
	for _, m := range latestState(in).BuyingGroup {
		if m.Status == statusDeparted {
			out[m.PersonID] = true
		}
	}
	return out
}

// RecipientsNotDuplicated: no person appears twice, directly or through a merged record.
func RecipientsNotDuplicated(in Input) []Finding {
	people := personIndex(in)
	seen := map[string]string{}
	var out []Finding
	for _, r := range in.Draft.Recipients {
		canonical := r.PersonID
		if p, ok := people[r.PersonID]; ok && p.MergedInto != nil {
			canonical = *p.MergedInto
		}
		if first, dup := seen[canonical]; dup {
			out = append(out, failure(CheckRecipientsNotDuplicate,
				fmt.Sprintf("person %s is addressed twice (%s and %s)", canonical, first, r.Role),
				"Address each person once, in one role."))
			continue
		}
		seen[canonical] = r.Role
	}
	return out
}

// InternalOnlyNotExternalized: an internal-only employee is never on to/cc of a message that
// reaches a customer (bcc does not expose them; ADR-0014).
func InternalOnlyNotExternalized(in Input) []Finding {
	if !reachesCustomer(in) {
		return nil
	}
	people := personIndex(in)
	var out []Finding
	for _, r := range visibleRecipients(in.Draft) {
		if p, ok := people[r.PersonID]; ok && p.InternalOnly {
			out = append(out, failure(CheckInternalNotExternal,
				fmt.Sprintf("internal-only %s is on %s of a customer-facing message", p.DisplayName, r.Role),
				fmt.Sprintf("Remove %s from the customer thread (bcc or brief them internally).", p.DisplayName)).blocking())
		}
	}
	return append(out, internalOnlyNamedInText(in)...)
}

// internalOnlyNamedInText: a customer-facing message must not point the customer at an
// internal-only person by full name ("reach out to Dana Deal"), even when they are not a recipient.
// Only full names of two or more words are matched, case-sensitively as a proper-noun sequence,
// so a shared first name or ordinary words ("we will grant" vs Will Grant) never trigger it.
func internalOnlyNamedInText(in Input) []Finding {
	text := draftText(in.Draft)
	var out []Finding
	for _, p := range in.People {
		if p.InternalOnly && len(strings.Fields(p.DisplayName)) >= 2 && wordRE(p.DisplayName, false).MatchString(text) {
			out = append(out, failure(CheckInternalNotExternal,
				fmt.Sprintf("internal-only %s is named in a customer-facing message", p.DisplayName),
				fmt.Sprintf("Do not point the customer at %s; route through the account owner.", p.DisplayName)).blocking())
		}
	}
	return out
}
