package claims

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"testing"
	"time"
)

var base = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func at(days int) time.Time { return base.AddDate(0, 0, days) }

// mk builds a claim; id doubles as the claim id and (padded) the source activity id.
func mk(id string, field FieldPath, value string, st Standing, conf float64, when time.Time) Claim {
	c := Claim{ID: id, AccountID: "acct", FieldPath: field, Value: json.RawMessage(value), Standing: st,
		Confidence: conf, SourceActivityID: "act-" + id, OccurredAt: when, Extractor: "test@1", Status: StatusActive}
	if st == FirstPartyAI {
		c.EvidenceQuote = "quote"
	}
	return c
}

func scalar(t *testing.T, a Adjudication, f FieldPath) *Slot {
	t.Helper()
	s := a.Find(f, "", "")
	if s == nil {
		t.Fatalf("no slot for %s", f)
	}
	return s
}

func TestHigherStandingWinsEvenWhenOlder(t *testing.T) {
	crm := mk("crm", FieldStage, `"Commercial review"`, CRMExplicit, 1, at(0))
	ai := mk("ai", FieldStage, `"Negotiation"`, FirstPartyAI, 0.9, at(-1)) // the Acme puzzle: AI said it first
	a := Adjudicate([]Claim{ai, crm}, at(10))

	s := scalar(t, a, FieldStage)
	if s.Winner == nil || s.Winner.ID != "crm" {
		t.Fatalf("winner = %+v, want crm", s.Winner)
	}
	if len(s.Competing) != 1 || s.Competing[0].ID != "ai" {
		t.Fatalf("competing = %+v", s.Competing)
	}
	u, ok := a.Updates["ai"]
	if !ok || u.Status != StatusOutranked {
		t.Fatalf("losing claim update = %+v ok=%v, want outranked (retained, not deleted)", u, ok)
	}
	if len(a.Conflicts) != 0 {
		t.Fatalf("older AI claim must not raise a conflict, got %+v", a.Conflicts)
	}
}

func TestSameStandingLatestOccurredAtWinsAndOlderIsSuperseded(t *testing.T) {
	older := mk("old", FieldHealth, `"on_track"`, FirstPartyAI, 0.95, at(0))
	newer := mk("new", FieldHealth, `"at_risk"`, FirstPartyAI, 0.7, at(3))
	a := Adjudicate([]Claim{older, newer}, at(10))

	if w := scalar(t, a, FieldHealth).Winner; w == nil || w.ID != "new" {
		t.Fatalf("winner = %+v, want the newer claim despite lower confidence", w)
	}
	if u := a.Updates["old"]; u.Status != StatusSuperseded || u.SupersededBy != "new" {
		t.Fatalf("update = %+v, want superseded by new", u)
	}
	if _, changed := a.Updates["new"]; changed {
		t.Fatal("the winner is already active; no update expected")
	}
}

func TestTieBreakersConfidenceThenID(t *testing.T) {
	lowConf := mk("a", FieldHealth, `"x"`, FirstPartyAI, 0.7, at(0))
	highConf := mk("b", FieldHealth, `"y"`, FirstPartyAI, 0.9, at(0))
	if w := scalar(t, Adjudicate([]Claim{lowConf, highConf}, at(1)), FieldHealth).Winner; w.ID != "b" {
		t.Fatalf("confidence tiebreak winner = %s, want b", w.ID)
	}
	c1 := mk("c1", FieldHealth, `"x"`, FirstPartyAI, 0.8, at(0))
	c2 := mk("c2", FieldHealth, `"y"`, FirstPartyAI, 0.8, at(0))
	if w := scalar(t, Adjudicate([]Claim{c2, c1}, at(1)), FieldHealth).Winner; w.ID != "c1" {
		t.Fatalf("id tiebreak winner = %s, want c1 (lowest id)", w.ID)
	}
}

func TestLowConfidenceAIClaimNeverWinsAndIsSuggested(t *testing.T) {
	weak := mk("weak", FieldRelationshipRisk, `"high"`, FirstPartyAI, 0.59, at(5))
	a := Adjudicate([]Claim{weak}, at(10))
	s := scalar(t, a, FieldRelationshipRisk)
	if s.Winner != nil {
		t.Fatalf("a 0.59 AI claim won: %+v", s.Winner)
	}
	if len(s.Suggested) != 1 || s.Suggested[0].ID != "weak" {
		t.Fatalf("suggested = %+v", s.Suggested)
	}
	if _, changed := a.Updates["weak"]; changed {
		t.Fatal("a suggestion stays active; it is not outranked")
	}

	solid := mk("solid", FieldRelationshipRisk, `"low"`, FirstPartyAI, 0.6, at(0))
	s = scalar(t, Adjudicate([]Claim{weak, solid}, at(10)), FieldRelationshipRisk)
	if s.Winner == nil || s.Winner.ID != "solid" {
		t.Fatalf("the 0.6 claim is the floor and must win over the newer 0.59 one, got %+v", s.Winner)
	}
	if len(s.Suggested) != 1 {
		t.Fatalf("suggested = %+v", s.Suggested)
	}
}

func TestLowConfidenceFloorAppliesOnlyToFirstPartyAI(t *testing.T) {
	crm := mk("crm", FieldStage, `"Discovery"`, CRMExplicit, 0.3, at(0))
	if w := scalar(t, Adjudicate([]Claim{crm}, at(1)), FieldStage).Winner; w == nil {
		t.Fatal("a structured CRM claim is not subject to the AI confidence floor")
	}
}

func TestExpiredClaimsAreExcludedAndMarkedExpired(t *testing.T) {
	exp := at(2)
	meeting := mk("m", FieldNextMeeting, `{"event_id":"e1"}`, CRMExplicit, 1, at(0))
	meeting.ExpiresAt = &exp
	a := Adjudicate([]Claim{meeting}, at(3))
	if s := scalar(t, a, FieldNextMeeting); s.Winner != nil {
		t.Fatalf("expired claim won: %+v", s.Winner)
	}
	if a.Updates["m"].Status != StatusExpired {
		t.Fatalf("update = %+v, want expired", a.Updates["m"])
	}

	// Not yet expired at an earlier "now": live again, and a stale 'expired' status heals.
	meeting.Status = StatusExpired
	a = Adjudicate([]Claim{meeting}, at(1))
	if s := scalar(t, a, FieldNextMeeting); s.Winner == nil || s.Winner.ID != "m" {
		t.Fatalf("winner = %+v, want m", s.Winner)
	}
	if a.Updates["m"].Status != StatusActive {
		t.Fatalf("update = %+v, want active again", a.Updates["m"])
	}
}

func TestRejectedClaimsAreIgnoredAndUntouched(t *testing.T) {
	rej := mk("rej", FieldStage, `"Closed won"`, HumanApproved, 1, at(5))
	rej.Status = StatusRejected
	ok := mk("ok", FieldStage, `"Discovery"`, CRMExplicit, 1, at(0))
	a := Adjudicate([]Claim{rej, ok}, at(10))
	if w := scalar(t, a, FieldStage).Winner; w.ID != "ok" {
		t.Fatalf("winner = %s", w.ID)
	}
	if _, touched := a.Updates["rej"]; touched {
		t.Fatal("a human-rejected claim must keep its status")
	}
}

func TestThirdPartyNeverOutranksFirstParty(t *testing.T) {
	enr := mk("enr", FieldBuyingGroupMember, `{"title":"Senior Engineering Manager"}`, ThirdParty, 1, at(10))
	enr.SubjectPersonID = "ravi"
	crm := mk("crm", FieldBuyingGroupMember, `{"title":"Head of Platform Engineering"}`, CRMExplicit, 1, at(0))
	crm.SubjectPersonID = "ravi"
	a := Adjudicate([]Claim{enr, crm}, at(20))
	s := a.Find(FieldBuyingGroupMember, "ravi", "title")
	if s == nil || s.Winner == nil || s.Winner.ID != "crm" {
		t.Fatalf("title winner = %+v", s)
	}
	if a.Updates["enr"].Status != StatusOutranked {
		t.Fatalf("enrichment claim update = %+v, want outranked and retained", a.Updates["enr"])
	}
	if len(a.Conflicts) != 0 {
		t.Fatalf("third-party evidence is never conflict evidence, got %+v", a.Conflicts)
	}
}

func TestRuleNullDoesNotOverrideLiveBookedMeeting(t *testing.T) {
	start := at(7)
	booked := mk("b", FieldNextMeeting, `{"event_id":"B","start":"x"}`, CRMExplicit, 1, at(0))
	booked.ExpiresAt = &start
	booked.Extractor = "rule:calendar@1"
	completedOther := mk("done", FieldNextMeeting, `null`, CRMExplicit, 1, at(3)) // meeting A ended later than B was booked
	completedOther.Extractor = "rule:calendar@1"

	s := scalar(t, Adjudicate([]Claim{booked, completedOther}, at(4)), FieldNextMeeting)
	if s.Winner == nil || s.Winner.ID != "b" {
		t.Fatalf("winner = %+v, want the still-booked meeting", s.Winner)
	}

	// Once the booked meeting has started its claim expires and the null stands: nothing booked.
	s = scalar(t, Adjudicate([]Claim{booked, completedOther}, at(8)), FieldNextMeeting)
	if s.Winner == nil || s.Winner.ID != "done" || !IsNull(s.Winner.Value) {
		t.Fatalf("winner = %+v, want the null claim after the booked meeting expired", s.Winner)
	}
}

func TestAINullIsAnExplicitStatementAndMayOverrideOlderAIValue(t *testing.T) {
	proposed := mk("p", FieldNextMeeting, `"2026-10-01 security review"`, FirstPartyAI, 0.8, at(0))
	cantCommit := mk("n", FieldNextMeeting, `null`, FirstPartyAI, 0.8, at(3))
	if w := scalar(t, Adjudicate([]Claim{proposed, cantCommit}, at(4)), FieldNextMeeting).Winner; w.ID != "n" {
		t.Fatalf("winner = %s, want the newer explicit null", w.ID)
	}
}

func TestRuleNullBelowNonNullOnlyWithinTheSameStanding(t *testing.T) {
	ai := mk("ai", FieldNextMeeting, `"call on Friday"`, FirstPartyAI, 0.9, at(5))
	null := mk("null", FieldNextMeeting, `null`, CRMExplicit, 1, at(0))
	null.Extractor = "rule:calendar@1"
	if w := scalar(t, Adjudicate([]Claim{ai, null}, at(6)), FieldNextMeeting).Winner; w.ID != "null" {
		t.Fatalf("winner = %s, standing outranks the null rule", w.ID)
	}
}

func TestListItemsFoldItemWiseWithLatestStatusWinning(t *testing.T) {
	open := mk("o", FieldBlockers, `"open: EU budget line"`, FirstPartyAI, 0.9, at(0))
	resolved := mk("r", FieldBlockers, `"resolved: EU budget line"`, FirstPartyAI, 0.9, at(7))
	other := mk("s", FieldBlockers, `"Security sign-off"`, FirstPartyAI, 0.9, at(5))
	a := Adjudicate([]Claim{open, other, resolved}, at(10))

	items := a.Items(FieldBlockers)
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2 independent slots", len(items))
	}
	var budget *Slot
	for i := range items {
		if ItemKey("EU budget line") == items[i].Key {
			budget = &items[i]
		}
	}
	if budget == nil || budget.Winner.ID != "r" {
		t.Fatalf("budget slot = %+v, want the later 'resolved' claim", budget)
	}
	if a.Updates["o"].Status != StatusSuperseded {
		t.Fatalf("older status claim update = %+v", a.Updates["o"])
	}
	if _, touched := a.Updates["s"]; touched {
		t.Fatal("an unrelated item must not be touched")
	}
}

func TestOutOfOrderIngestResolvesByOccurredAtNotInputOrder(t *testing.T) {
	// Beta: the correction email (09-16) is ingested before the call it corrects (09-15).
	correction := mk("email", FieldBlockers, `"resolved: SSO is the main open item"`, FirstPartyAI, 0.9, at(15))
	call := mk("call", FieldBlockers, `"SSO is the main open item"`, FirstPartyAI, 0.9, at(14))
	for _, in := range [][]Claim{{correction, call}, {call, correction}} {
		items := Adjudicate(in, at(30)).Items(FieldBlockers)
		if len(items) != 1 || items[0].Winner.ID != "email" {
			t.Fatalf("items = %+v, want the later-occurring correction to win", items)
		}
	}
}

func TestPerSubjectSlotsSeparateRolesTitlesAndDelegations(t *testing.T) {
	mkSub := func(id string, f FieldPath, v, subj string) Claim {
		c := mk(id, f, v, CRMExplicit, 1, at(0))
		c.SubjectPersonID = subj
		return c
	}
	cs := []Claim{
		mkSub("r1", FieldStakeholderRole, `"security"`, "marco"),
		mkSub("r2", FieldStakeholderRole, `"technical_evaluator"`, "marco"),
		mkSub("r3", FieldStakeholderRole, `"security"`, "sam"),
		mkSub("m1", FieldBuyingGroupMember, `{"title":"Head of Security"}`, "marco"),
		mkSub("m2", FieldBuyingGroupMember, `{}`, "marco"),
		mkSub("d1", FieldDelegation, `{"from_person_id":"elena","to_person_id":"sam"}`, "elena"),
	}
	a := Adjudicate(cs, at(5))
	for _, want := range []struct {
		f    FieldPath
		subj string
		key  string
		id   string
	}{
		{FieldStakeholderRole, "marco", "security", "r1"},
		{FieldStakeholderRole, "marco", "technical_evaluator", "r2"},
		{FieldStakeholderRole, "sam", "security", "r3"},
		{FieldBuyingGroupMember, "marco", "title", "m1"},
		{FieldBuyingGroupMember, "marco", "member", "m2"},
		{FieldDelegation, "elena", "", "d1"},
	} {
		s := a.Find(want.f, want.subj, want.key)
		if s == nil || s.Winner == nil || s.Winner.ID != want.id {
			t.Errorf("slot %s/%s/%s = %+v, want winner %s", want.f, want.subj, want.key, s, want.id)
		}
	}
	if len(a.Updates) != 0 {
		t.Fatalf("independent slots must not outrank each other: %+v", a.Updates)
	}
}

func TestUnparseableListItemGetsItsOwnSlot(t *testing.T) {
	bad := mk("bad", FieldBlockers, `42`, FirstPartyAI, 0.9, at(0))
	good := mk("good", FieldBlockers, `"Real blocker"`, FirstPartyAI, 0.9, at(1))
	if got := len(Adjudicate([]Claim{bad, good}, at(2)).Items(FieldBlockers)); got != 2 {
		t.Fatalf("slots = %d, want 2", got)
	}
}

func TestAdjudicationIsOrderIndependent(t *testing.T) {
	var cs []Claim
	for i := 0; i < 40; i++ {
		f := []FieldPath{FieldStage, FieldHealth, FieldBlockers}[i%3]
		st := []Standing{CRMExplicit, FirstPartyAI, FirstPartyAI, ThirdParty}[i%4]
		cs = append(cs, mk(fmt.Sprintf("c%02d", i), f, fmt.Sprintf(`"v%d"`, i%5), st, float64(5+i%5)/10, at(i%7)))
	}
	want := summarize(Adjudicate(cs, at(30)))
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 25; i++ {
		shuffled := append([]Claim(nil), cs...)
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		if got := summarize(Adjudicate(shuffled, at(30))); got != want {
			t.Fatalf("order changed the outcome:\n%s\nvs\n%s", got, want)
		}
	}
}

func summarize(a Adjudication) string {
	out := ""
	for _, s := range a.Slots {
		w := "-"
		if s.Winner != nil {
			w = s.Winner.ID
		}
		out += fmt.Sprintf("%s/%s/%s=%s;", s.Field, s.Subject, s.Key, w)
	}
	for _, id := range sortedKeys(a.Updates) {
		out += fmt.Sprintf("%s:%s>%s;", id, a.Updates[id].Status, a.Updates[id].SupersededBy)
	}
	return out
}

func TestEmptyInputYieldsEmptyAdjudication(t *testing.T) {
	a := Adjudicate(nil, at(0))
	if len(a.Slots) != 0 || len(a.Updates) != 0 || len(a.Conflicts) != 0 || a.Find(FieldStage, "", "") != nil {
		t.Fatalf("got %+v", a)
	}
}

func TestStandingRankMatchesGeneratedColumn(t *testing.T) {
	want := map[Standing]int{HumanApproved: 5, CRMExplicit: 4, FirstPartyRecord: 3, FirstPartyAI: 2, ThirdParty: 1}
	for s, r := range want {
		if s.Rank() != r || !s.Valid() {
			t.Errorf("%s rank %d valid %v", s, s.Rank(), s.Valid())
		}
	}
	if Standing("bogus").Valid() || FieldPath("bogus").Valid() || !FieldStage.Valid() {
		t.Error("validity checks")
	}
	if !FieldBlockers.IsList() || FieldStage.IsList() {
		t.Error("IsList")
	}
}
