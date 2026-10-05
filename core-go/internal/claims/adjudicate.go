package claims

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// ruleExtractorPrefix marks claims produced by deterministic Go rules.
const ruleExtractorPrefix = "rule:"

// maxConflictReason bounds Conflict.Reason (account_state.v1.json allows 500).
const maxConflictReason = 500

// StatusUpdate is the status a claim row should carry after adjudication.
type StatusUpdate struct {
	Status       Status
	SupersededBy string
}

// Slot is one thing a field asserts: a scalar field, one list item, one (person, role) pair,
// one person's title, or one person's delegation. Claims compete only inside their slot.
type Slot struct {
	// Scope is the opportunity the slot belongs to ("" = the account). Claims compete only inside their
	// slot, so a deal's claims never compete with another deal's or with the account's (ADR-0016).
	Scope   string
	Field   FieldPath
	Subject string // person id for per-subject slots
	Key     string // item key, role, or "title"/"member"
	// Winner is nil when no eligible claim exists (only suggestions, expirations, or nothing).
	Winner *Claim
	// Competing holds every eligible claim that did not win (outranked or superseded).
	Competing []Claim
	// Suggested holds first-party AI claims below MinAIConfidence: shown, never winning.
	Suggested []Claim
	// FirstSeen is the earliest occurred_at of any claim in the slot.
	FirstSeen time.Time
	// Conflicts lists, newest first, the lower-standing claims that are newer than the winner of a
	// scalar field and contradict it (ADR-0008). Each also appears in Competing.
	Conflicts []Conflict
}

// Adjudication is the outcome of one pure pass over an account's claims.
type Adjudication struct {
	// Scope is the scope Find and Items look in ("" = the account). Adjudicate returns the full
	// adjudication of every scope, read at account scope; View narrows it to one deal.
	Scope string
	// Slots is ordered by field, subject, key, then scope.
	Slots []Slot
	// Updates lists the claims whose stored status or superseded_by must change.
	Updates map[string]StatusUpdate
	// Conflicts lists every conflict, ordered by field (also available on the slots).
	Conflicts []Conflict
}

// Find returns the slot for (field, subject, key) in the adjudication's scope, or nil. Account-wide person
// facts (Slot.PersonFact) are found whatever the scope.
func (a Adjudication) Find(field FieldPath, subject, key string) *Slot {
	for i := range a.Slots {
		s := &a.Slots[i]
		if s.Field == field && s.Subject == subject && s.Key == key && a.inScope(*s) {
			return s
		}
	}
	return nil
}

func (a Adjudication) inScope(s Slot) bool { return s.Scope == a.Scope || s.PersonFact() }

// Scopes lists, sorted, the opportunities that have at least one slot.
func (a Adjudication) Scopes() []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range a.Slots {
		if s.Scope != "" && !seen[s.Scope] {
			seen[s.Scope] = true
			out = append(out, s.Scope)
		}
	}
	sort.Strings(out)
	return out
}

// View returns a read-only adjudication of one scope: its own slots plus the account-wide person facts that
// keep accepts (nil keeps none). Updates are not carried over; they belong to the full adjudication.
func (a Adjudication) View(scope string, keep func(Slot) bool) Adjudication {
	out := Adjudication{Scope: scope}
	for _, s := range a.Slots {
		if s.Scope == scope || (s.PersonFact() && keep != nil && keep(s)) {
			out.Slots = append(out.Slots, s)
			out.Conflicts = append(out.Conflicts, s.Conflicts...)
		}
	}
	return out
}

// Items returns the slots of a list field ordered by when each item first appeared.
func (a Adjudication) Items(field FieldPath) []Slot {
	var out []Slot
	for _, s := range a.Slots {
		if s.Field == field && a.inScope(s) {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].FirstSeen.Equal(out[j].FirstSeen) {
			return out[i].FirstSeen.Before(out[j].FirstSeen)
		}
		return out[i].Key < out[j].Key
	})
	return out
}

type slotID struct {
	field               FieldPath
	scope, subject, key string
}

// Adjudicate folds claims into one winner per slot. It is pure and independent of input order.
//
// Winner: highest standing rank, then latest occurred_at, then highest confidence, then lowest
// id. A deterministic rule's null (for example "meeting completed, nothing booked") ranks below
// any live non-null claim of the same standing. First-party AI claims under MinAIConfidence never
// win; claims whose expires_at has passed are excluded; rejected claims are ignored. Losers are
// retained: outranked when their standing is lower, superseded otherwise. Third-party claims
// never outrank first-party ones because they rank below them.
func Adjudicate(cs []Claim, now time.Time) Adjudication {
	groups := map[slotID][]Claim{}
	for _, c := range cs {
		if c.Status == StatusRejected {
			continue
		}
		id := slotOf(c)
		groups[id] = append(groups[id], c)
	}
	ids := make([]slotID, 0, len(groups))
	for id := range groups {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if ids[i].field != ids[j].field {
			return ids[i].field < ids[j].field
		}
		if ids[i].subject != ids[j].subject {
			return ids[i].subject < ids[j].subject
		}
		if ids[i].key != ids[j].key {
			return ids[i].key < ids[j].key
		}
		return ids[i].scope < ids[j].scope
	})

	out := Adjudication{Updates: map[string]StatusUpdate{}}
	for _, id := range ids {
		slot := adjudicateSlot(id, groups[id], now, out.Updates)
		out.Slots = append(out.Slots, slot)
		out.Conflicts = append(out.Conflicts, slot.Conflicts...)
	}
	return out
}

func adjudicateSlot(id slotID, claims []Claim, now time.Time, updates map[string]StatusUpdate) Slot {
	slot := Slot{Scope: id.scope, Field: id.field, Subject: id.subject, Key: id.key}
	var eligible []Claim
	for _, c := range claims {
		if slot.FirstSeen.IsZero() || c.OccurredAt.Before(slot.FirstSeen) {
			slot.FirstSeen = c.OccurredAt
		}
		switch {
		case c.ExpiresAt != nil && !c.ExpiresAt.After(now):
			record(updates, c, StatusExpired, "")
		case c.Standing == FirstPartyAI && c.Confidence < MinAIConfidence:
			slot.Suggested = append(slot.Suggested, c)
			record(updates, c, StatusActive, "")
		default:
			eligible = append(eligible, c)
		}
	}
	sort.Slice(slot.Suggested, func(i, j int) bool { return better(slot.Suggested[i], slot.Suggested[j]) })
	if len(eligible) == 0 {
		return slot
	}
	sort.Slice(eligible, func(i, j int) bool { return better(eligible[i], eligible[j]) })
	winner := eligible[0]
	slot.Winner = &winner
	record(updates, winner, StatusActive, "")
	for _, loser := range eligible[1:] {
		slot.Competing = append(slot.Competing, loser)
		if loser.Standing.Rank() < winner.Standing.Rank() {
			record(updates, loser, StatusOutranked, "")
		} else {
			record(updates, loser, StatusSuperseded, winner.ID)
		}
	}
	if conflictsApply(id.field) {
		slot.Conflicts = findConflicts(winner, slot.Competing)
	}
	return slot
}

// record stores an update only when the claim's current row differs from the desired state.
func record(updates map[string]StatusUpdate, c Claim, status Status, supersededBy string) {
	if c.Status != status || c.SupersededBy != supersededBy {
		updates[c.ID] = StatusUpdate{Status: status, SupersededBy: supersededBy}
	}
}

// better is the strict total order that picks winners (true when a ranks above b).
func better(a, b Claim) bool {
	if ra, rb := a.Standing.Rank(), b.Standing.Rank(); ra != rb {
		return ra > rb
	}
	if an, bn := isRuleNull(a), isRuleNull(b); an != bn {
		return bn
	}
	if !a.OccurredAt.Equal(b.OccurredAt) {
		return a.OccurredAt.After(b.OccurredAt)
	}
	if a.Confidence != b.Confidence {
		return a.Confidence > b.Confidence
	}
	return a.ID < b.ID
}

// isRuleNull: a deterministic rule's "known absent" (e.g. a completed meeting) says nothing about
// other bookings, unlike an explicit null stated in an email.
func isRuleNull(c Claim) bool {
	return IsNull(c.Value) && strings.HasPrefix(c.Extractor, ruleExtractorPrefix)
}

// conflictsApply: scalar fields and list items surface conflicts. Per-person slots (role, title,
// delegation) do not: a newer disagreeing claim there is an ordinary update, not a field a rep owns.
func conflictsApply(f FieldPath) bool {
	switch f {
	case FieldStakeholderRole, FieldBuyingGroupMember, FieldDelegation:
		return false
	}
	return true
}

// contradicts: a claim contradicts the winner when it asserts a different concrete value; for a list
// item (same item text by construction) that means a different status. Null and "unknown" assert nothing.
func contradicts(winner, c Claim) bool {
	if IsNull(c.Value) || IsUnknown(c.Value) {
		return false
	}
	if winner.FieldPath.IsList() {
		w, errW := ParseListItem(winner.Value)
		o, errO := ParseListItem(c.Value)
		return errW == nil && errO == nil && w.Status != o.Status
	}
	return !SameValue(c.Value, winner.Value)
}

// findConflicts returns, newest first, every eligible lower-standing claim that is newer than the
// winner and asserts a different concrete value (ADR-0008, any lower standing). Competing is
// already eligible, so suggestions (low-confidence AI) and expired claims never count; null and
// "unknown" assert nothing concrete and never contradict.
func findConflicts(winner Claim, competing []Claim) []Conflict {
	if IsNull(winner.Value) || IsUnknown(winner.Value) {
		return nil
	}
	var found []Claim
	for _, c := range competing {
		if c.Standing.Rank() >= winner.Standing.Rank() || !c.OccurredAt.After(winner.OccurredAt) {
			continue
		}
		if contradicts(winner, c) {
			found = append(found, c)
		}
	}
	sort.Slice(found, func(i, j int) bool { return newerFirst(found[i], found[j]) })
	conflicts := make([]Conflict, len(found))
	for i, c := range found {
		reason := fmt.Sprintf("newer %s claim (%s) says %s; keeping the %s value %s (%s) until a rep confirms",
			c.Standing, c.OccurredAt.UTC().Format("2006-01-02"), brief(c.Value),
			winner.Standing, brief(winner.Value), winner.OccurredAt.UTC().Format("2006-01-02"))
		conflicts[i] = Conflict{Field: winner.FieldPath, Winner: winner, Contradicting: c, Reason: truncateRunes(reason, maxConflictReason)}
	}
	return conflicts
}

func newerFirst(a, b Claim) bool {
	if !a.OccurredAt.Equal(b.OccurredAt) {
		return a.OccurredAt.After(b.OccurredAt)
	}
	return better(a, b)
}

func brief(raw []byte) string {
	if s, ok := StringValue(raw); ok {
		return fmt.Sprintf("%q", truncateRunes(s, 80))
	}
	return truncateRunes(CanonicalValue(raw), 80)
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

func slotOf(c Claim) slotID {
	id := slotID{field: c.FieldPath}
	if !c.PersonFact() {
		id.scope = c.OpportunityID
	}
	switch c.FieldPath {
	case FieldBlockers, FieldObjections, FieldDecisionCriteria, FieldCommitment:
		if item, err := ParseListItem(c.Value); err == nil {
			id.key = ItemKey(item.Text)
		} else {
			id.key = "invalid:" + c.ID
		}
	case FieldStakeholderRole:
		id.subject = c.SubjectPersonID
		if s, ok := StringValue(c.Value); ok {
			id.key = strings.ToLower(strings.TrimSpace(s))
		} else {
			id.key = CanonicalValue(c.Value)
		}
	case FieldBuyingGroupMember:
		id.subject = c.SubjectPersonID
		// Titles compete with titles; role assertions compete per role; bare presence claims share one slot.
		switch m := ParseMember(c.Value); {
		case m.Title != "":
			id.key = "title"
		case m.Role != "":
			id.key = "member:" + strings.ToLower(m.Role)
		default:
			id.key = "member"
		}
	case FieldDelegation:
		id.subject = c.SubjectPersonID
		if id.subject == "" {
			if d, err := ParseDelegation(c.Value); err == nil {
				id.subject = d.FromPersonID
			}
		}
	}
	return id
}

func sortedKeys(m map[string]StatusUpdate) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
