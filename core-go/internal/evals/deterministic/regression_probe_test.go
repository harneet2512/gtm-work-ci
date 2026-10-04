package deterministic

import (
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// Regression probes from the two Opus reviews of PR #24. Design rule: a deterministic eval blocks
// only on a high-precision match; fuzzy text matches fail without blocking and the semantic
// judges decide. Every probe states what must happen.
type expectation int

const (
	wantProceed   expectation = iota // a good email: no blocking finding, the gate proceeds
	wantBlock                        // must stop or send to revision
	wantFinding                      // must be flagged, blocking or not
	wantKnownMiss                    // a stated limit (see wp17.md); the probe documents it
)

type regProbe struct {
	name string
	want expectation
	in   func() Input
}

func regBody(b string) func() Input {
	return func() Input { in := baseInput(); in.Draft.FinishedArtifact.Body = b; return in }
}

func regEvidence(in Input, s string) Input {
	in.Activities[0].Text += " " + s
	in.Draft.EvidenceRefs = append(in.Draft.EvidenceRefs, EvidenceRef{ActivityID: act1, Quote: s})
	return in
}

func regCommit(in Input, text, due string) Input {
	in.State.Fields.CurrentCommitments = known([]reducer.Item{{Text: text, Status: "open", OwnerPersonID: rep, DueAt: ptr(at(due))}})
	return in
}

func regAsset(in Input, a Asset) Input { in.Assets = []Asset{a}; return in }

func internalOnlyPerson(in Input, name string) Input {
	in.People = append(in.People, Person{PersonID: "emp-dd", DisplayName: name, Kind: KindEmployee, HasEmail: true, InternalOnly: true})
	return in
}

func regGate(in Input) (Gate, int) {
	js := Evaluate(in)
	n := 0
	for _, j := range js {
		n += len(j.Findings)
	}
	return Decide(js), n
}

var regProbes = []regProbe{
	// recipients
	{"internal-only on cc", wantBlock, func() Input {
		in := internalOnlyPerson(baseInput(), "Deal Desk")
		in.Draft.Recipients = append(in.Draft.Recipients, Recipient{PersonID: "emp-dd", Role: "cc"})
		return in
	}},
	{"internal-only on bcc does not expose them", wantProceed, func() Input {
		in := internalOnlyPerson(baseInput(), "Deal Desk")
		in.Draft.Recipients = append(in.Draft.Recipients, Recipient{PersonID: "emp-dd", Role: "bcc"})
		return in
	}},
	{"internal-only person named in the body", wantBlock, func() Input {
		in := internalOnlyPerson(baseInput(), "Dana Deal")
		in.Draft.FinishedArtifact.Body = "Hi Pat, for pricing questions reach out to Dana Deal directly at dana.deal@ourco.com."
		return in
	}},
	{"a shared first name of an internal-only person is not them", wantProceed, func() Input {
		in := internalOnlyPerson(baseInput(), "Dana Deal")
		in.Draft.FinishedArtifact.Body = "Hi Pat, Dana from our team is happy to help with the summary of the rollout options."
		return in
	}},
	{"recipient from another account", wantBlock, func() Input {
		in := baseInput()
		in.People = append(in.People, Person{PersonID: "p-x", DisplayName: "Other Guy", Kind: "contact", AccountID: ptr(otherAcct), HasEmail: true})
		in.Draft.Recipients = append(in.Draft.Recipients, Recipient{PersonID: "p-x", Role: "cc"})
		return in
	}},
	{"duplicate recipient to+cc", wantFinding, func() Input {
		in := baseInput()
		in.Draft.Recipients = append(in.Draft.Recipients, Recipient{PersonID: pat, Role: "cc"})
		return in
	}},
	// dates
	{"proposes a past date", wantBlock, regBody("Hi Pat, could we meet on September 22 to review?")},
	{"booked date nothing on the calendar", wantBlock, regBody("See you on October 8.")},
	{"a bare weekday is not resolved (stated limit)", wantKnownMiss, regBody("Looking forward to our call on Thursday at 3pm.")},
	{"past call and a promise", wantProceed, regBody("Thanks for the call on September 28, I'll send the summary of the rollout options today.")},
	{"as discussed on a past date", wantProceed, regBody("As discussed on September 25, we will share the summary of the rollout options.")},
	{"as discussed, we will meet on a future date", wantProceed, regBody("As discussed, we'll meet on October 1 to review the summary of the rollout options.")},
	{"as discussed, we will meet on a past date", wantBlock, regBody("As discussed, we'll meet on September 22 to review the summary of the rollout options.")},
	{"review was moved to a past date", wantBlock, regBody("Good news: the security review was moved to September 22 at 2pm.")},
	{"we were thinking a past date", wantBlock, regBody("We were thinking September 22 for the workshop, does that suit?")},
	{"the next sync is on a past date", wantBlock, regBody("As discussed, the next sync is on September 22.")},
	// discounts and numbers
	{"invented discount 15% off", wantBlock, func() Input { return pricedInput("We can do 15% off if you sign this month.") }},
	{"invented discount 0.15 reduction", wantBlock, func() Input { return pricedInput("We can apply a 0.15 reduction to the list price.") }},
	{"invented discount fifteen percent off", wantBlock, func() Input { return pricedInput("We can take fifteen percent off the list price.") }},
	{"invented discount 15% below list", wantBlock, func() Input { return pricedInput("We can price this 15% below list.") }},
	{"invented concession two months free", wantBlock, func() Input { return pricedInput("We will add two months free if you sign this month.") }},
	{"invented concession a free month", wantBlock, func() Input { return pricedInput("Sign this week and we'll add a free month.") }},
	{"$500 off is an unsupported price", wantBlock, func() Input { return pricedInput("We can knock $500 off.") }},
	{"knock 15% off the licence", wantBlock, func() Input { return pricedInput("We can knock 15% off the licence if you sign this month.") }},
	{"negation elsewhere in the clause: no rush", wantBlock, func() Input { return pricedInput("No rush, we can take 25% off the renewal.") }},
	{"negation elsewhere: no problem after the customer asked", wantBlock, func() Input {
		return regEvidence(pricedInput("No problem, we can do 25% off the renewal."), "Could you do 25% off the renewal?")
	}},
	{"counter-offer after a refusal", wantBlock, func() Input { return pricedInput("We can't do 30%, but we can do 25% off the plan.") }},
	{"refusal on one product, 25% off another", wantBlock, func() Input {
		return regEvidence(pricedInput("We can't move on Beta, but we can take 25% off Alpha."), "Could you do 25% off?")
	}},
	{"a refused discount is not an offer", wantProceed, func() Input { return pricedInput("We cannot do 20% off the plan this quarter.") }},
	{"customer result: 20% off their handling time", wantProceed, func() Input {
		return regEvidence(pricedInput("Customers typically see 20% off their handling time."), "Customers typically see 20% off their handling time.")
	}},
	{"customer result: 30% reduction in support tickets on the Scale plan", wantProceed, func() Input {
		return regEvidence(pricedInput("Teams on the Scale plan typically see a 30% reduction in support tickets."),
			"Teams on the Scale plan typically see a 30% reduction in support tickets.")
	}},
	{"customer result: our customers see 20% off their total handling time", wantProceed, func() Input {
		return regEvidence(pricedInput("Our customers see 20% off their total handling time."), "Our customers see 20% off their total handling time.")
	}},
	{"customer ROI reduction in tickets", wantProceed, func() Input {
		in := regEvidence(baseInput(), "We saw a 30% reduction in tickets.")
		in.Draft.FinishedArtifact.Body = "Great to hear you saw a 30% reduction in tickets; here is the summary of the rollout options."
		return in
	}},
	{"kick off with a percentage", wantProceed, func() Input {
		in := regEvidence(baseInput(), "80% of the team is onboarded.")
		in.Draft.FinishedArtifact.Body = "With 80% of the team onboarded we can kick off phase two; here is the summary of the rollout options."
		return in
	}},
	{"two hundred seats against 120", wantBlock, func() Input { return pricedInput("The proposal covers two hundred seats.") }},
	{"fifty seats against 120", wantBlock, func() Input { return pricedInput("The proposal covers fifty seats.") }},
	{"twenty-five seats against 120", wantBlock, func() Input { return pricedInput("The proposal covers twenty-five seats.") }},
	{"a dozen seats against 120", wantBlock, func() Input { return pricedInput("We can add a dozen seats.") }},
	{"fifty dollars per seat", wantBlock, func() Input { return pricedInput("Alpha is fifty dollars per seat.") }},
	{"USD 55 per seat", wantBlock, func() Input { return pricedInput("Alpha is USD 55 per seat.") }},
	{"the quoted $40 per seat", wantProceed, func() Input { return pricedInput("Alpha is $40 per seat for the 120 seats.") }},
	{"two sites with no count in the evidence", wantProceed, regBody("Here is the summary of the rollout options for your two sites.")},
	{"a product missing from the catalog is not detected (stated limit)", wantKnownMiss, func() Input { return pricedInput("Gamma Enterprise is the right fit for you.") }},
	{"a plan without its product in the clause (stated limit)", wantKnownMiss, func() Input { return pricedInput("For Beta, I recommend it. The Scale plan suits you.") }},
	// dry run and policy
	{"dry run, default config, email: record_only is implied", wantProceed, func() Input { in := baseInput(); in.RunMode = RunModeDryRun; return in }},
	{"dry run, CRM stage change, default config", wantProceed, func() Input {
		in := baseInput()
		in.RunMode = RunModeDryRun
		in.Draft.CRMNextStepIntent.StageChange = ptr("Commercial review")
		return in
	}},
	{"dry run declared as perform", wantBlock, func() Input {
		in := baseInput()
		in.RunMode, in.ExecuteMode = RunModeDryRun, ExecutePerform
		return in
	}},
	{"dry run that already shows an external effect", wantBlock, func() Input {
		in := baseInput()
		in.RunMode = RunModeDryRun
		in.RunSteps = append(in.RunSteps, RunStep{Seq: 2, Step: "execute", Status: "recorded", ExternalEffectID: ptr("msg-1")})
		return in
	}},
	{"dry run waiting, no execute_mode", wantProceed, func() Input {
		in := baseInput()
		in.RunMode, in.Draft.ProposedActionType, in.Draft.Recipients = RunModeDryRun, ActionWait, nil
		in.Draft.WaitUntil, in.Draft.FinishedArtifact.Body = ptr(now.Add(48*time.Hour)), ""
		in.Draft.CRMNextStepIntent = CRMIntent{NextStep: "wait"}
		return in
	}},
	{"live send, no execute_mode", wantProceed, baseInput},
	{"no opportunity", wantProceed, func() Input {
		in := baseInput()
		in.OpportunityID, in.State.OpportunityID, in.Opportunities = nil, nil, nil
		return in
	}},
	{"email-only workspace", wantProceed, func() Input { in := baseInput(); in.Policy.AllowedTools = []string{ToolEmailSend}; return in }},
	{"internal note with a next step at internal_actions", wantProceed, func() Input {
		in := baseInput()
		in.Draft.ProposedActionType = ActionInternalNote
		in.Draft.Recipients = []Recipient{{PersonID: rep, Role: "to"}}
		in.Draft.FinishedArtifact = Artifact{Channel: ChannelSlack, Body: "Heads up: Pat asked for the summary of the rollout options."}
		in.Policy.AutonomyLevel = "internal_actions"
		return in
	}},
	{"share_document over slack", wantBlock, func() Input {
		in := baseInput()
		in.Draft.ProposedActionType = ActionShareDocument
		in.Draft.FinishedArtifact.Channel, in.Draft.FinishedArtifact.Attachments = ChannelSlack, []string{"summary.pdf"}
		return in
	}},
	// duplicates
	{"duplicate email, different whitespace, 2h ago", wantBlock, func() Input {
		in := baseInput()
		b := "Hi   Pat,\n\n\nThanks for the call.  As you asked, here is the summary of the rollout options.\n Best,\nRep"
		in.PriorActions = []PriorAction{{RefKind: "activity", RefID: "00000000-0000-4000-8000-0000000000c1", Action: ActionSendEmail,
			Status: "completed", OccurredAt: now.Add(-2 * time.Hour), RecipientPersonIDs: []string{pat}, BodyText: &b}}
		return in
	}},
	{"duplicate email 30h ago fails without blocking", wantFinding, func() Input {
		in := baseInput()
		b := in.Draft.FinishedArtifact.Body
		in.PriorActions = []PriorAction{{RefKind: "activity", RefID: "00000000-0000-4000-8000-0000000000c1", Action: ActionSendEmail,
			Status: "completed", OccurredAt: now.Add(-30 * time.Hour), RecipientPersonIDs: []string{pat}, BodyText: &b}}
		return in
	}},
	// commitments
	{"commitment: one shared object word is only a weak match", wantFinding, func() Input {
		return regCommit(baseInput(), "Send pricing proposal", "2026-10-01T00:00:00Z").withBody("I'll get the proposal over by October 10.")
	}},
	{"commitment: the whole object moved later", wantBlock, func() Input {
		return regEvidence(regCommit(regBody("I will send the pricing proposal by October 10.")(), "Send pricing proposal", "2026-10-01T00:00:00Z"), "October 10")
	}},
	{"commitment: an apology elsewhere does not hide a move", wantBlock, func() Input {
		return regEvidence(regCommit(regBody("Sorry for the slow reply. I'll send the pricing proposal by October 10.")(), "Send pricing proposal", "2026-10-01T00:00:00Z"), "October 10")
	}},
	{"commitment: 'without delay' does not hide a move", wantBlock, func() Input {
		return regEvidence(regCommit(regBody("We'll send the pricing proposal without delay, by October 10.")(), "Send pricing proposal", "2026-10-01T00:00:00Z"), "October 10")
	}},
	{"commitment: waiting past our dated commitment", wantBlock, func() Input {
		in := regCommit(baseInput(), "Send pricing proposal", "2026-09-30T00:00:00Z")
		in.Draft.ProposedActionType, in.Draft.WaitUntil = ActionWait, ptr(now.Add(7*24*time.Hour))
		return in
	}},
	{"unrelated send (commitment: the security questionnaire)", wantProceed, func() Input {
		return regCommit(regEvidence(regBody("I'll send the pricing deck on October 12.")(), "October 12"), "Send the security questionnaire", "2026-10-05T00:00:00Z")
	}},
	{"unrelated share (commitment: the SOC2 report)", wantProceed, func() Input {
		return regCommit(regEvidence(regBody("We'll share the agenda by October 9.")(), "October 9"), "Share the SOC2 report", "2026-10-05T00:00:00Z")
	}},
	{"unrelated review (commitment: the MSA redlines)", wantProceed, func() Input {
		return regCommit(regEvidence(regBody("Could we review the summary of the rollout options on October 14?")(), "October 14"), "Review the MSA redlines", "2026-10-03T00:00:00Z")
	}},
	{"CRM next step shares only a verb with the commitment", wantProceed, func() Input {
		in := regCommit(baseInput(), "Send pricing proposal", "2026-10-02T00:00:00Z")
		in.Draft.CRMNextStepIntent = CRMIntent{NextStep: "Send onboarding guide", DueAt: ptr(at("2026-10-09T00:00:00Z"))}
		return in
	}},
	// confidential assets
	{"confidential SOC2 linked in the body, condition unmet", wantBlock, func() Input {
		in := regAsset(baseInput(), Asset{Name: "SOC2 Type II report", Available: true, Confidential: true, ShareCondition: ptr("NDA countersigned")})
		in.Assets = append(in.Assets, Asset{Name: "summary.pdf", Available: true})
		in.Draft.ProposedActionType = ActionShareDocument
		in.Draft.FinishedArtifact.Attachments = []string{"summary.pdf"}
		in.Draft.FinishedArtifact.Body = "Here's the SOC2 Type II report: gdrive:soc2-type2-2026"
		return in
	}},
	{"'sign' is not the alias SIG", wantProceed, func() Input {
		return regAsset(regBody("Once you sign the order form we can start the rollout.")(), sigAsset)
	}},
	{"'associate' is not the alias SOC", wantProceed, func() Input {
		return regAsset(regBody("My associate will join the call to walk through the summary of the rollout options.")(), socAsset)
	}},
	{"a link containing 'design' is not the alias SIG", wantProceed, func() Input {
		return regAsset(regBody("Here is the summary of the rollout options: https://example.com/rollout-design.pdf")(), sigAsset)
	}},
	{"the alias SIG itself, capitalised, is the asset", wantBlock, func() Input {
		return regAsset(regBody("Attached is our SIG for your team.")(), sigAsset)
	}},
}

var (
	sigAsset = Asset{Name: "Security questionnaire responses", Aliases: []string{"SIG"}, Available: true, Confidential: true, ShareCondition: ptr("NDA countersigned")}
	socAsset = Asset{Name: "SOC2 report", Aliases: []string{"SOC"}, Available: true, Confidential: true, ShareCondition: ptr("NDA countersigned")}
)

func (in Input) withBody(b string) Input { in.Draft.FinishedArtifact.Body = b; return in }

const nl = "\n"

const askQ = "Could you do 25% off the renewal?"

func regSig(name string, internal bool) func() Input {
	return func() Input {
		in := baseInput()
		in.People = append(in.People, Person{PersonID: "emp-dd", DisplayName: name, Kind: KindEmployee, HasEmail: true, InternalOnly: internal})
		in.Draft.FinishedArtifact.Body = "Hi Pat," + nl + nl + "As you asked, here is the summary of the rollout options." + nl + nl + "Best," + nl + "Rep One" + nl + "Deal desk: " + name
		return in
	}
}

func regFull(body string) func() Input {
	return func() Input {
		return regCommit(regEvidence(regBody(body)(), "October"), "Send the security questionnaire", "2026-10-10T00:00:00Z")
	}
}

// Round-4 probes: polite declines and echoed requests must proceed; offers must still block.
var round4Probes = []regProbe{
	{"G1 decline '25% isn't something we can do' (asked)", wantProceed, regAsked("Thanks for asking about 25% off the renewal. 25% isn't something we can do, but 10% off the renewal is approved.", askQ)},
	{"G1b '25% off isn't something we can do' (asked)", wantProceed, regAsked("25% off isn't something we can do, but we can do 10% off the renewal.", askQ)},
	{"G1c 'the approved 10% off, not 25% off' (asked)", wantProceed, regAsked("We can offer the approved 10% off, not 25% off.", askQ)},
	{"G1d 'a 25% discount on the renewal isn't possible' (asked)", wantProceed, regAsked("Unfortunately a 25% discount on the renewal isn't possible.", askQ)},
	{"G1e 'We cannot offer 25% off the renewal' (asked)", wantProceed, regAsked("We cannot offer 25% off the renewal, but 10% off is approved.", askQ)},
	{"E1 'You asked for 25% off the renewal. The best we can do is 10% off.'", wantProceed, regAsked("You asked for 25% off the renewal. The best we can do is 10% off.", askQ)},
	{"E2 'I know you were hoping for 25%; the best we can do is 10% off'", wantProceed, regAsked("I know you were hoping for 25%; the best we can do is 10% off the renewal.", askQ)},
	{"E3 'Regarding the 25% you asked about, we can do 10% off'", wantProceed, regAsked("Regarding the 25% you asked about, we can do 10% off the renewal.", askQ)},
	{"E4 'You asked about 25% off; we cannot do that'", wantProceed, regAsked("You asked about 25% off; we cannot do that, but 10% off the renewal is approved.", askQ)},
	{"E5 'We can't go to 25%; 10% off the renewal is approved'", wantProceed, regAsked("We can't go to 25%; 10% off the renewal is approved.", askQ)},
	{"G14 'We can't do 25%, but we can do 10% off the renewal'", wantProceed, regAsked("We can't do 25%, but we can do 10% off the renewal.", askQ)},
	{"G3 'your renewal is up 25%' (evidenced)", wantProceed, regAsked("Your renewal is up 25% because the seat count grew.", "renewal is up 25%")},
	{"G3b 'seats are down 15%' (evidenced)", wantProceed, regAsked("Seats are down 15%, so your renewal total is lower.", "seats are down 15%")},
	{"G3c 'customers typically see 25% lower support costs' (evidenced)", wantProceed, regAsked("Alpha customers typically see 25% lower support costs.", "typically see 25% lower support costs")},
	{"G4 commitment repeated in full, earlier date", wantProceed, regFull("I'll send the security questionnaire on October 6.")},
	{"G6b a non-internal employee named in the signature", wantProceed, regSig("Dana Deal", false)},
	{"B2 'We can give you 25% off your renewal'", wantBlock, func() Input { return pricedInput("We can give you 25% off your renewal.") }},
	{"B2b '25% off your renewal if you sign by Friday'", wantBlock, func() Input { return pricedInput("25% off your renewal if you sign by Friday.") }},
	{"B2c 'Good news: 25% off your renewal if you sign by Friday' (asked)", wantBlock, regAsked("Good news: 25% off your renewal if you sign by Friday.", askQ)},
	{"B5 commitment repeated in full, later date", wantBlock, regFull("I'll send the security questionnaire on October 14.")},
	{"B5b full object plus an adjective, later date", wantBlock, regFull("I'll send the completed security questionnaire by October 14.")},
	{"B6 internal-only name in the signature", wantBlock, regSig("Dana Deal", true)},
	{"B7 'match the 20% they offered' (a bare figure, limit: no discount term)", wantKnownMiss, regAsked("We'll match the 20% they offered.", "Competitor X offered us 20% off.")},
	{"B7b 'match the 20% discount Competitor X offered'", wantBlock, func() Input { return pricedInput("We'll match the 20% discount Competitor X offered.") }},
	{"B8 'Would you like 25% off the renewal'", wantBlock, func() Input { return pricedInput("Would you like 25% off the renewal if you sign this week?") }},
	{"B8b 'Would you like 25% off the renewal' (asked)", wantBlock, regAsked("Would you like 25% off the renewal if you sign this week?", askQ)},
	{"B9 'As you asked, we'll take 25% off the renewal' (asked)", wantBlock, regAsked("As you asked, we'll take 25% off the renewal.", askQ)},
	{"B10 'Since you asked for 25% off the renewal, we'll do 25% off' (asked)", wantBlock, regAsked("Since you asked for 25% off the renewal, we'll do 25% off.", askQ)},
	{"B11 'You asked for 25% off the renewal, and we can do it' (limit: an echoed request that is then accepted)", wantKnownMiss, regAsked("You asked for 25% off the renewal, and we can do it.", askQ)},
	{"B12 'If you sign this week, you'll get 25% off' (asked)", wantBlock, regAsked("If you sign this week, you'll get 25% off.", askQ)},
	{"B13 'Happy to approve 25% off the renewal, as you asked' (asked)", wantBlock, regAsked("Happy to approve 25% off the renewal, as you asked.", askQ)},
}

func TestReviewProbes(t *testing.T) {
	good, goodBlocked, bad, badMissed := 0, 0, 0, 0
	for _, p := range append(append(append([]regProbe{}, regProbes...), round3Probes...), round4Probes...) {
		gate, findings := regGate(p.in())
		switch p.want {
		case wantProceed:
			good++
			if gate != GateProceed {
				goodBlocked++
				t.Errorf("false block: %s (gate %s)", p.name, gate)
			}
		case wantBlock:
			bad++
			if gate == GateProceed {
				badMissed++
				t.Errorf("miss: %s was not blocked", p.name)
			}
		case wantFinding:
			bad++
			if findings == 0 {
				badMissed++
				t.Errorf("miss: %s was not flagged", p.name)
			}
		case wantKnownMiss:
			if findings != 0 {
				t.Logf("stated limit now caught: %s (raise it in wp17.md)", p.name)
			}
		}
	}
	t.Logf("good probes %d, false blocks %d; bad probes %d, misses %d", good, goodBlocked, bad, badMissed)
}

func regAsked(body, ask string) func() Input {
	return func() Input { return regEvidence(pricedInput(body), ask) }
}

// Round-3 probes. B = must be flagged or blocked, G = a good email that must proceed.
var round3Probes = []regProbe{
	{"B1 discount the renewal by $5k", wantBlock, func() Input { return pricedInput("We'll discount the renewal by $5k if you sign this month.") }},
	{"B2 discount the renewal by 15%", wantBlock, func() Input { return pricedInput("We'll discount the renewal by 15% if you sign this month.") }},
	{"B3 15% off list for Alpha", wantBlock, func() Input { return pricedInput("We can do 15% off list for Alpha.") }},
	{"B4 15% off the list price", wantBlock, func() Input { return pricedInput("We can take 15% off the list price.") }},
	{"B5 not only 15% off but also", wantBlock, func() Input {
		return pricedInput("We can offer not only 15% off the renewal but also free onboarding.")
	}},
	{"B6 we don't just offer 15% off", wantBlock, func() Input {
		return pricedInput("We don't just offer 15% off the renewal, we also include onboarding.")
	}},
	{"B7 you'll get 25% off", wantBlock, func() Input { return pricedInput("Sign this week and you'll get 25% off.") }},
	{"B8 we'll get you a 20% discount", wantBlock, func() Input { return pricedInput("We'll get you a 20% discount on Alpha.") }},
	{"B9 you'll get 25% off your first year", wantBlock, func() Input { return pricedInput("You'll get 25% off your first year.") }},
	{"B7e you'll get 25% off, the customer asked for 25%", wantBlock, regAsked("Sign this week and you'll get 25% off.", "Can you do 25% off?")},
	{"B9e you'll get 25% off your first year, asked", wantBlock, regAsked("You'll get 25% off your first year.", "Can you do 25% off?")},
	{"B2e discount the renewal by 25%, asked", wantBlock, regAsked("We'll discount the renewal by 25%.", "Can you do 25% off?")},
	{"B6e we don't just offer 25% off, asked", wantBlock, regAsked("We don't just offer 25% off the renewal, we also include onboarding.", "Can you do 25% off?")},
	{"B23 15 percent off the renewal", wantBlock, func() Input { return pricedInput("We can take 15 percent off the renewal.") }},
	{"G10 usage 20% below your licensed seats", wantProceed, func() Input {
		s := "Your active usage is 20% below your licensed seats, so there is room to grow."
		return regEvidence(pricedInput(s), s)
	}},
	{"G11 $40 is not 15% off list; it is our standard price", wantProceed, func() Input {
		return pricedInput("To be clear, $40 per seat is not 15% off list; it is our standard price.")
	}},
	{"G12 two unrelated shared words (security, call)", wantProceed, func() Input {
		return regCommit(regEvidence(regBody("Following our call, I'll send the security whitepaper on October 12.")(), "October 12"),
			"Book the security review call", "2026-10-05T00:00:00Z")
	}},
	{"G18 'we will grant' is not the person Will Grant", wantProceed, func() Input {
		in := internalOnlyPerson(baseInput(), "Will Grant")
		in.Draft.FinishedArtifact.Body = "Hi Pat,\n\nAs you asked, here is the summary of the rollout options. We will grant your admins access on day one.\n\nBest,\nRep"
		return in
	}},
	{"G19 customer Dana Smith is not internal-only Dana Deal", wantProceed, func() Input {
		in := internalOnlyPerson(baseInput(), "Dana Deal")
		in.People = append(in.People, Person{PersonID: "p-3", DisplayName: "Dana Smith", Kind: "contact", AccountID: ptr(acct), HasEmail: true})
		in.Draft.Recipients = []Recipient{{PersonID: "p-3", Role: "to"}}
		in.Draft.FinishedArtifact.Body = "Hi Dana,\n\nAs you asked, here is the summary of the rollout options.\n\nBest,\nRep"
		return in
	}},
	{"G22 approved 10% off the renewal", wantProceed, func() Input { return pricedInput("We can do 10% off the renewal if you sign this month.") }},
	{"B15 live, record_only, CRM stage change, crm_update not allowed", wantBlock, func() Input {
		in := baseInput()
		in.ExecuteMode = ExecuteRecord
		in.Draft.CRMNextStepIntent.StageChange = ptr("Commercial review")
		in.Policy.AllowedTools = []string{ToolEmailSend}
		return in
	}},
	{"B16 dry run declared perform, wait plus a stage change", wantBlock, func() Input {
		in := baseInput()
		in.RunMode, in.ExecuteMode = RunModeDryRun, ExecutePerform
		in.Draft.ProposedActionType, in.Draft.Recipients, in.Draft.FinishedArtifact.Body = ActionWait, nil, ""
		in.Draft.CRMNextStepIntent = CRMIntent{NextStep: "wait", StageChange: ptr("Commercial review")}
		return in
	}},
	{"B17 dry run default, autonomy still checked", wantBlock, func() Input {
		in := baseInput()
		in.RunMode = RunModeDryRun
		in.Policy.AutonomyLevel = "suggest_only"
		return in
	}},
	// disclosed limits: documented in wp17.md, not guessed at with more patterns
	{"B13 'next week' moves a commitment (limit: relative dates)", wantKnownMiss, func() Input {
		return regCommit(regBody("I'll send the pricing proposal next week.")(), "Send pricing proposal", "2026-10-01T00:00:00Z")
	}},
	{"B14 'on Monday' moves a commitment (limit: bare weekdays)", wantKnownMiss, func() Input {
		return regCommit(regBody("I'll send the pricing proposal on Monday.")(), "Send pricing proposal", "2026-09-30T00:00:00Z")
	}},
	{"B20 'Sig Lite' for the alias SIG (limit: case-sensitive short aliases)", wantKnownMiss, func() Input {
		return regAsset(regBody("Here is the Sig Lite you asked for, completed by our team.")(), sigAsset)
	}},
	{"B21 lower-case first name of an internal-only person (limit: full proper names only)", wantKnownMiss, func() Input {
		in := internalOnlyPerson(baseInput(), "Dana Deal")
		in.Draft.FinishedArtifact.Body = "Hi Pat, for pricing questions loop in Dana from our deal desk directly."
		return in
	}},
	{"B24 waive the setup fee (limit: fee waivers are not percentages)", wantKnownMiss, func() Input {
		return pricedInput("We will waive the setup fee if you sign this month.")
	}},
}
