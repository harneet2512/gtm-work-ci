package deterministic

import "testing"

// Gold-style unit fixtures: realistic drafts with the verdict a reviewer would give. They are not
// part of fixtures/evals (the gold is never edited); they exercise the pricing and commitment
// patterns, which the gold covers with one pricing label.

type fixtureCase struct {
	name     string
	build    func() Input
	verdict  string
	blocking bool
}

func runFixtureCases(t *testing.T, eval func(Input) Judgment, cases []fixtureCase) {
	t.Helper()
	for _, c := range cases {
		r := eval(c.build()).Result
		if r.Verdict != c.verdict || r.Blocking != c.blocking {
			t.Errorf("%s: got %s blocking=%v, want %s blocking=%v (%s)", c.name, r.Verdict, r.Blocking, c.verdict, c.blocking, r.Reason)
		}
	}
}

func pricingFixture(body string) func() Input {
	return func() Input { return pricedInput(body) }
}

func TestPricingGoldStyleFixtures(t *testing.T) {
	runFixtureCases(t, PricingIntegrity, []fixtureCase{
		{"quoted price, quantity and an approved renewal discount",
			pricingFixture("Alpha Team is $40 per seat, so 120 seats comes to $4,800. We can take 10% off the renewal."), "pass", false},
		{"quote restated in k notation",
			pricingFixture("Alpha Team for your 120 seats is $4.8k in total."), "pass", false},
		{"an unapproved discount on the plan",
			pricingFixture("Alpha Team for 120 seats is $4,800, and we can offer 20% off the plan if you sign by Friday."), "fail", true},
		{"a price nobody quoted",
			pricingFixture("Alpha Scale is $55 per seat for your 120 seats."), "fail", true},
		{"a plan that belongs to another product",
			pricingFixture("Beta Scale would suit the team and is quoted at $40 per seat."), "fail", true},
		{"a seat count that contradicts the quote",
			pricingFixture("That is 150 seats at $40 per seat."), "fail", true},
		{"a refused discount is not an offer",
			pricingFixture("We cannot offer 30% off at this volume, but the quoted $40 per seat stands."), "pass", false},
		{"a result the customer can expect is not a discount",
			pricingFixture("Teams like yours typically see a 25% reduction in onboarding time on Alpha Team."), "pass", false},
		{"free months are a concession nobody approved",
			pricingFixture("Sign this month and we will add two months free to the Alpha Team plan."), "fail", true},
		{"a region the product is not sold in",
			pricingFixture("Beta is hosted in the EU for all customers."), "fail", true},
	})
}

func commitmentFixture(commitment, due, body string) func() Input {
	return func() Input { return regCommit(baseInput(), commitment, due).withBody(body) }
}

func TestCommitmentGoldStyleFixtures(t *testing.T) {
	const due = "2026-10-05T00:00:00Z"
	waiting := func() Input {
		in := regCommit(baseInput(), "Send the security questionnaire", due)
		in.Draft.ProposedActionType, in.Draft.WaitUntil = ActionWait, ptr(at("2026-10-12T00:00:00Z"))
		return in
	}
	runFixtureCases(t, DateCommitmentConsistency, []fixtureCase{
		{"the commitment is kept without a date change",
			commitmentFixture("Send the security questionnaire", due, "The security questionnaire is on its way to you today, as promised."), "pass", false},
		{"waiting past a dated commitment of ours", waiting, "fail", true},
		{"the whole commitment moved later without saying so",
			commitmentFixture("Send the security questionnaire", due, "We'll have the security questionnaire back to you by October 14."), "fail", true},
		{"a move that says it is a move",
			commitmentFixture("Send the security questionnaire", due, "I need more time on the security questionnaire; it will reach you by October 14."), "pass", false},
		{"a different deliverable on a later date",
			commitmentFixture("Send the security questionnaire", due, "We'll send the onboarding agenda by October 14."), "pass", false},
		{"an earlier date than the commitment",
			commitmentFixture("Send the security questionnaire", due, "We'll send the security questionnaire by October 3."), "pass", false},
		{"one shared word is only a weak match",
			commitmentFixture("Send the SOC2 security questionnaire", due, "We'll pick up the security topic by October 14."), "fail", false},
	})
}
