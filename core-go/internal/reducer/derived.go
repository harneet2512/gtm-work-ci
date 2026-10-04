package reducer

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

// customerRoles lists, per activity type, the participant roles whose presence makes the
// activity an interaction with the customer.
var customerRoles = map[string]string{
	"EmailReceived": "from", "EmailReply": "from", "CustomerReplied": "from",
	"TranscriptReady": "speaker", "CallEnded": "speaker",
	"MeetingCompleted": "attendee",
}

// derivedField builds a known derived field: computed from activities, evidence is the activity,
// no winning claim (account_state.v1.json derivedField).
func derivedField(value string, at time.Time, refs []EvidenceRef) Field {
	f := unknownField()
	f.Derived = true
	if len(refs) == 0 {
		return f
	}
	f.Value, f.Known = value, true
	f.Confidence, f.AsOf, f.EvidenceRefs = ptr(1.0), ptr(at.UTC()), refs
	return f
}

// isCustomer: a resolved person is a customer when its kind is contact; an unresolved identity is
// a customer when it is an email address outside our own domain. Unlinked call speakers are not.
func (r *run) isCustomer(p Participant) bool {
	if p.PersonID != "" {
		if person, ok := r.in.People[p.PersonID]; ok {
			return person.Kind == "contact"
		}
	}
	at := strings.LastIndex(p.RawIdentity, "@")
	if at < 0 {
		return false
	}
	domain := strings.ToLower(p.RawIdentity[at+1:])
	own := strings.ToLower(r.in.OwnDomain)
	return domain != "" && domain != own && !strings.HasSuffix(domain, "."+own)
}

// lastCustomerInteraction is the latest inbound email, call or completed meeting that involves a
// customer. Outbound mail, internal Slack and bare invite responses are not interactions.
func (r *run) lastCustomerInteraction() Field {
	var best *Activity
	for i := range r.in.Activities {
		a := &r.in.Activities[i]
		role, ok := customerRoles[a.Type]
		if !ok || !r.hasCustomer(a, role) {
			continue
		}
		if best == nil || a.OccurredAt.After(best.OccurredAt) || (a.OccurredAt.Equal(best.OccurredAt) && a.ID < best.ID) {
			best = a
		}
	}
	if best == nil {
		return derivedField("", time.Time{}, nil)
	}
	return derivedField(best.OccurredAt.UTC().Format(time.RFC3339), best.OccurredAt,
		[]EvidenceRef{{ActivityID: best.ID, OccurredAt: best.OccurredAt.UTC()}})
}

func (r *run) hasCustomer(a *Activity, role string) bool {
	for _, p := range a.Participants {
		if p.Role == role && r.isCustomer(p) {
			return true
		}
	}
	return false
}

// championSince is when the current champion's unbroken run of claims began (ADR-0012, owner stability):
// the earliest claim, of the winner's standing, in the run of claims agreeing with the winner. It is
// absent without a known champion.
func (r *run) championSince() *Field {
	s := r.in.Adjudication.Find(claims.FieldChampion, "", "")
	if s == nil || s.Winner == nil || claims.IsNull(s.Winner.Value) || claims.IsUnknown(s.Winner.Value) {
		return nil
	}
	first := championRunStart(*s)
	f := derivedField(first.OccurredAt.UTC().Format(time.RFC3339), first.OccurredAt, []EvidenceRef{evidence(first)})
	return &f
}

// championRunStart is the earliest claim of the unbroken run of claims naming the current champion, across
// standings: a later human or CRM re-confirmation of the same person does not restart the run.
func championRunStart(s claims.Slot) claims.Claim {
	all := append([]claims.Claim{*s.Winner}, s.Competing...)
	sort.Slice(all, func(i, j int) bool {
		if !all[i].OccurredAt.Equal(all[j].OccurredAt) {
			return all[i].OccurredAt.Before(all[j].OccurredAt)
		}
		return all[i].ID < all[j].ID
	})
	start := -1
	for i, c := range all {
		switch {
		case claims.SameValue(c.Value, s.Winner.Value):
			if start < 0 {
				start = i
			}
		case !claims.IsNull(c.Value) && !claims.IsUnknown(c.Value):
			start = -1 // another person was named: the run is broken
		}
	}
	if start < 0 {
		return *s.Winner
	}
	return all[start]
}

// change is the moment the current value of one slot was established.
type change struct {
	field string
	at    time.Time
	claim claims.Claim
}

// lastMeaningfulChange is the latest moment any field took its current concrete value. A claim that
// merely reaffirms a value does not move it; absences (null, unknown) are not changes.
func (r *run) lastMeaningfulChange() Field {
	var changes []change
	for _, s := range r.in.Adjudication.Slots {
		if s.Winner == nil || claims.IsNull(s.Winner.Value) || claims.IsUnknown(s.Winner.Value) || !r.ownsSlot(s) {
			continue
		}
		est := establishedBy(s)
		changes = append(changes, change{field: stateFieldName(s.Field), at: est.OccurredAt, claim: est})
	}
	if len(changes) == 0 {
		return derivedField("", time.Time{}, nil)
	}
	latest := changes[0].at
	for _, c := range changes {
		if c.at.After(latest) {
			latest = c.at
		}
	}
	names := map[string]bool{}
	seen := map[string]bool{}
	var refs []EvidenceRef
	var types []string
	for _, c := range changes {
		if !c.at.Equal(latest) {
			continue
		}
		names[c.field] = true
		if !seen[c.claim.SourceActivityID] {
			seen[c.claim.SourceActivityID] = true
			refs = append(refs, EvidenceRef{ActivityID: c.claim.SourceActivityID, OccurredAt: latest.UTC()})
			types = append(types, r.activityType(c.claim.SourceActivityID))
		}
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].ActivityID < refs[j].ActivityID })
	sort.Strings(types)
	value := fmt.Sprintf("%s updated (%s, %s)", joinSorted(names), strings.Join(dedupe(types), "/"), latest.UTC().Format("2006-01-02"))
	return derivedField(value, latest, refs)
}

func (r *run) activityType(id string) string {
	for _, a := range r.in.Activities {
		if a.ID == id {
			return a.Type
		}
	}
	return "activity"
}

func dedupe(sorted []string) []string {
	var out []string
	for i, s := range sorted {
		if i == 0 || s != sorted[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// stateFieldName names the state field a slot feeds.
func stateFieldName(f claims.FieldPath) string {
	switch f {
	case claims.FieldCommitment:
		return "current_commitments"
	case claims.FieldBuyingGroupMember, claims.FieldStakeholderRole, claims.FieldDelegation:
		return "buying_group"
	}
	return string(f)
}

// establishedBy returns the claim at which the winner's current value started: the earliest claim of
// the winner's standing in the unbroken run of agreeing claims that ends at the winner.
func establishedBy(s claims.Slot) claims.Claim {
	w := *s.Winner
	same := []claims.Claim{w}
	for _, c := range s.Competing {
		if c.Standing == w.Standing {
			same = append(same, c)
		}
	}
	sort.Slice(same, func(i, j int) bool {
		if !same[i].OccurredAt.Equal(same[j].OccurredAt) {
			return same[i].OccurredAt.Before(same[j].OccurredAt)
		}
		return same[i].ID < same[j].ID
	})
	i := 0
	for j, c := range same {
		if c.ID == w.ID {
			i = j
		}
	}
	for i > 0 && agrees(same[i-1], w) {
		i--
	}
	return same[i]
}

func agrees(c, w claims.Claim) bool {
	if w.FieldPath.IsList() {
		a, errA := claims.ParseListItem(c.Value)
		b, errB := claims.ParseListItem(w.Value)
		return errA == nil && errB == nil && a.Status == b.Status
	}
	return claims.SameValue(c.Value, w.Value)
}

// ownsSlot: a deal's own changes are those of its scope; person facts about its people, which come from other
// deals' evidence as well, are not changes of this deal. The account counts every slot.
func (r *run) ownsSlot(s claims.Slot) bool {
	scope := r.in.Adjudication.Scope
	return scope == "" || s.Scope == scope
}
