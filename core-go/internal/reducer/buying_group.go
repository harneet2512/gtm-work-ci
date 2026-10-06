package reducer

import (
	"regexp"
	"sort"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

// Engagement thresholds for buying-group status, tuned on the gold checkpoints (HAR-99). Gold fits any
// new-member window between 5.1 days (Acme Marco, first seen 09-21 10:30, "new" at 09-26 11:00) and 6.7
// days (Northstar Grace, first seen 08-27 17:46, "unknown" at 09-03 11:20); the design brief said 7 days,
// which misclassifies Grace, so the window is 6 days.
const (
	// NewWindow: a member first seen this recently who has not written an email yet is "new".
	NewWindow = 6 * 24 * time.Hour
	// WeakeningAfter: no direct engagement for this long marks a member "weakening".
	WeakeningAfter = 45 * 24 * time.Hour
	// InactiveAfter: no direct engagement for this long marks a member "inactive".
	InactiveAfter = 90 * 24 * time.Hour
)

// roleOrder is the schema vocabulary order of buying-group roles.
var roleOrder = []string{
	"champion", "economic_buyer", "technical_evaluator", "security", "legal",
	"executive_sponsor", "user", "influencer", "blocker", "procurement", "unknown",
}

// gapOrder is the schema vocabulary order of coverage gaps.
var gapOrder = []string{"economic_buyer", "technical_evaluator", "security", "legal", "executive_sponsor", "procurement"}

type memberAcc struct {
	roles     map[string]bool
	byRole    map[string][]claims.Claim // winning claims that gave the person each role
	evidence  map[string]claims.Claim   // winning claims that put the person in the group, by claim id
	firstSeen time.Time
	delegated string
}

// grant records that a winning claim gives the person a role.
func (a *memberAcc) grant(role string, w claims.Claim) {
	a.roles[role] = true
	a.byRole[role] = append(a.byRole[role], w)
}

// buyingGroup assembles members from the winning claims about people and computes coverage gaps.
// Third-party claims never make someone a member.
func (r *run) buyingGroup() ([]Member, []string) {
	acc := r.collectMembers()
	r.recordFirstSeen(acc)
	members := make([]Member, 0, len(acc))
	for person, a := range acc {
		members = append(members, r.member(person, a))
	}
	sort.Slice(members, func(i, j int) bool {
		fi, fj := acc[members[i].PersonID].firstSeen, acc[members[j].PersonID].firstSeen
		if !fi.Equal(fj) {
			return fi.Before(fj)
		}
		return members[i].PersonID < members[j].PersonID
	})
	return members, r.coverageGaps(members)
}

// collectMembers reads the winning, non-third-party claims about people into per-person accumulators.
func (r *run) collectMembers() map[string]*memberAcc {
	acc := map[string]*memberAcc{}
	touch := func(person string) *memberAcc {
		a, ok := acc[person]
		if !ok {
			a = &memberAcc{roles: map[string]bool{}, byRole: map[string][]claims.Claim{}, evidence: map[string]claims.Claim{}}
			acc[person] = a
		}
		return a
	}
	for _, s := range r.in.Adjudication.Slots {
		w := s.Winner
		if w == nil || w.Standing == claims.ThirdParty {
			continue
		}
		switch s.Field {
		case claims.FieldBuyingGroupMember:
			a := touch(s.Subject)
			a.evidence[w.ID] = *w
			if role := claims.ParseMember(w.Value).Role; role != "" {
				a.grant(role, *w)
			}
		case claims.FieldStakeholderRole:
			if s.Subject == "" {
				continue
			}
			a := touch(s.Subject)
			a.grant(s.Key, *w)
			a.evidence[w.ID] = *w
		case claims.FieldDelegation:
			r.addDelegation(touch, *w)
		case claims.FieldChampion, claims.FieldEconomicBuyer:
			if person, ok := claims.StringValue(w.Value); ok && !claims.IsUnknown(w.Value) {
				a := touch(person)
				a.grant(string(s.Field), *w)
				a.evidence[w.ID] = *w
			}
		}
	}
	return acc
}

func (r *run) addDelegation(touch func(string) *memberAcc, w claims.Claim) {
	d, err := claims.ParseDelegation(w.Value)
	if err != nil {
		r.warn("claim %s is not a delegation: %v", w.ID, err)
		return
	}
	from := touch(d.FromPersonID)
	from.delegated = d.ToPersonID
	from.evidence[w.ID] = w
	to := touch(d.ToPersonID)
	to.evidence[w.ID] = w
}

// recordFirstSeen sets, for each member, the earliest activity or claim that mentions the person.
func (r *run) recordFirstSeen(acc map[string]*memberAcc) {
	see := func(person string, at time.Time) {
		if a, ok := acc[person]; ok && (a.firstSeen.IsZero() || at.Before(a.firstSeen)) {
			a.firstSeen = at
		}
	}
	for _, act := range r.in.Activities {
		for _, p := range act.Participants {
			see(p.PersonID, act.OccurredAt)
		}
	}
	for _, s := range r.in.Adjudication.Slots {
		var all []claims.Claim
		if s.Winner != nil {
			all = append(all, *s.Winner)
		}
		all = append(all, s.Competing...)
		for _, c := range all {
			see(c.SubjectPersonID, c.OccurredAt)
		}
	}
	for _, a := range acc { // a member known only through undated claims still sorts deterministically
		if a.firstSeen.IsZero() {
			a.firstSeen = r.asOf
		}
	}
}

func (r *run) member(person string, a *memberAcc) Member {
	p, known := r.in.People[person]
	if !known {
		r.warn("buying-group member %s is not a known person", person)
	}
	m := Member{PersonID: person, DisplayName: p.DisplayName, Roles: orderedRoles(a.roles), EvidenceRefs: []EvidenceRef{}}
	m.RoleSource, m.RoleBasis, m.RoleProvenance = roleProvenance(a.byRole)
	if m.DisplayName == "" {
		m.DisplayName = claims.Unknown
	}
	title := p.Title
	if s := r.in.Adjudication.Find(claims.FieldBuyingGroupMember, person, "title"); s != nil && s.Winner != nil {
		title = claims.ParseMember(s.Winner.Value).Title
	}
	if title != "" {
		m.Title = ptr(title)
	}
	m.Tenure = r.tenure(person, title)
	if a.delegated != "" {
		m.DelegatedToPerson = ptr(a.delegated)
	}
	var refs []claims.Claim
	for _, c := range a.evidence {
		refs = append(refs, c)
	}
	sort.Slice(refs, func(i, j int) bool {
		if !refs[i].OccurredAt.Equal(refs[j].OccurredAt) {
			return refs[i].OccurredAt.Before(refs[j].OccurredAt)
		}
		return refs[i].ID < refs[j].ID
	})
	for _, c := range refs {
		m.EvidenceRefs = append(m.EvidenceRefs, evidence(c))
	}
	last, authored := r.engagement(person)
	if !last.IsZero() {
		m.LastEngagedAt = ptr(last.UTC())
	}
	m.Status = r.status(a.firstSeen, last, authored)
	return m
}

// interimWords mark an acting or interim holder, in a title or in the sentence that gave it.
var interimWords = regexp.MustCompile(`(?i)(^|[^a-z])(acting|interim|temporary|until a permanent)([^a-z]|$)`)

// tenure is "interim" when the member's title, or the quote of a first-party claim about them, says the
// role is held on an acting or interim basis, and "permanent" for any other titled member.
func (r *run) tenure(person, title string) string {
	if interimWords.MatchString(title) {
		return "interim"
	}
	for _, s := range r.in.Adjudication.Slots {
		if s.Subject != person || s.Winner == nil || s.Winner.Standing == claims.ThirdParty {
			continue
		}
		if s.Field == claims.FieldBuyingGroupMember || s.Field == claims.FieldStakeholderRole {
			if interimWords.MatchString(s.Winner.EvidenceQuote) {
				return "interim"
			}
		}
	}
	if title == "" {
		return "" // no title and nothing said about the role: unknown
	}
	return "permanent"
}

// status is the combined engagement rule: "new" when first seen within NewWindow and no email authored
// yet; "unknown" when never directly engaged; otherwise by time since the last direct engagement.
func (r *run) status(firstSeen, lastEngaged time.Time, authoredEmail bool) string {
	if !authoredEmail && r.asOf.Sub(firstSeen) <= NewWindow {
		return "new"
	}
	if lastEngaged.IsZero() {
		return "unknown"
	}
	switch gap := r.asOf.Sub(lastEngaged); {
	case gap >= InactiveAfter:
		return "inactive"
	case gap >= WeakeningAfter:
		return "weakening"
	}
	return "active"
}

// engagementRoles lists, per activity type, the participant role that means the person acted.
var engagementRoles = map[string]string{
	"EmailReceived": "from", "EmailReply": "from", "EmailSent": "from",
	"TranscriptReady": "speaker", "CallEnded": "speaker",
	"MeetingAccepted": "actor", "MeetingDeclined": "actor",
	"SlackMessage": "actor", "SlackDecision": "actor",
}

var emailTypes = map[string]bool{"EmailReceived": true, "EmailReply": true, "EmailSent": true}

// engagement returns the latest time the person acted directly and whether they ever wrote an email.
func (r *run) engagement(person string) (last time.Time, authoredEmail bool) {
	for _, a := range r.in.Activities {
		role, ok := engagementRoles[a.Type]
		if !ok {
			continue
		}
		for _, p := range a.Participants {
			if p.PersonID != person || p.Role != role {
				continue
			}
			if a.OccurredAt.After(last) {
				last = a.OccurredAt
			}
			authoredEmail = authoredEmail || emailTypes[a.Type]
		}
	}
	return last, authoredEmail
}

func orderedRoles(set map[string]bool) []string {
	var out []string
	for _, role := range roleOrder {
		if set[role] {
			out = append(out, role)
		}
	}
	if len(out) == 0 {
		return []string{claims.Unknown}
	}
	return out
}

// coverageGaps lists the roles an opportunity needs that nobody holds: the economic buyer, and any role
// a claim asked for without naming a person.
func (r *run) coverageGaps(members []Member) []string {
	held := map[string]bool{}
	for _, m := range members {
		for _, role := range m.Roles {
			held[role] = true
		}
	}
	needed := map[string]bool{"economic_buyer": true}
	for _, s := range r.in.Adjudication.Slots {
		if s.Field == claims.FieldStakeholderRole && s.Subject == "" && s.Winner != nil {
			needed[s.Key] = true
		}
	}
	gaps := []string{}
	for _, role := range gapOrder {
		if needed[role] && !held[role] {
			gaps = append(gaps, role)
		}
	}
	return gaps
}
