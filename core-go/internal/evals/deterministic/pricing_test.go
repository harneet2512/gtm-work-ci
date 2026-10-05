package deterministic

import (
	"strings"
	"testing"
)

func pricedInput(body string) Input {
	in := baseInput()
	in.Draft.FinishedArtifact.Body = body
	in.Commercial = Commercial{
		Currency: ptr("USD"),
		Catalog: []CatalogItem{
			{Product: "Alpha", Plans: []string{"Team", "Scale"}, Regions: []string{"US", "EU"}},
			{Product: "Beta", Plans: []string{"Basic"}, Regions: []string{"US"}},
		},
		Quoted: []QuotedLine{{Product: "Alpha", Plan: ptr("Team"), Region: ptr("US"), Quantity: ptr(120.0),
			Unit: ptr("seats"), UnitPrice: ptr(40.0), Total: ptr(4800.0)}},
		ApprovedDiscountPercents: []float64{10},
	}
	return in
}

func TestPriceSupported(t *testing.T) {
	t.Run("quoted unit price and total pass", func(t *testing.T) {
		expectNone(t, PriceSupported(pricedInput("Alpha is $40 per seat, $4,800 in total.")))
	})
	t.Run("amount in k notation equals quoted total", func(t *testing.T) {
		expectNone(t, PriceSupported(pricedInput("Total is $4.8k.")))
	})
	t.Run("price the customer stated in cited evidence passes", func(t *testing.T) {
		in := pricedInput("You mentioned a $50,000 budget.")
		in.Activities[0].Text = "Our budget is $50,000 for the year."
		expectNone(t, PriceSupported(in))
	})
	t.Run("unsupported price blocks", func(t *testing.T) {
		expectFinding(t, PriceSupported(pricedInput("Alpha is $55 per seat.")), CheckPriceSupported, true, "$55")
	})
	t.Run("no figures pass", func(t *testing.T) {
		expectNone(t, PriceSupported(baseInput()))
	})
}

func TestDiscountNotInvented(t *testing.T) {
	t.Run("approved discount passes", func(t *testing.T) {
		expectNone(t, DiscountNotInvented(pricedInput("We can offer a 10% discount.")))
	})
	t.Run("invented discount blocks", func(t *testing.T) {
		expectFinding(t, DiscountNotInvented(pricedInput("We can offer 25% off.")), CheckDiscountApproved, true, "25%")
	})
	t.Run("percent outside a discount clause is ignored", func(t *testing.T) {
		expectNone(t, DiscountNotInvented(pricedInput("Adoption reached 80% of the team.")))
	})
}

func TestProductPlanRegionCorrect(t *testing.T) {
	t.Run("offered plan and region pass", func(t *testing.T) {
		expectNone(t, ProductPlanRegionCorrect(pricedInput("Alpha Scale is available in the EU.")))
	})
	t.Run("plan of another product blocks", func(t *testing.T) {
		expectFinding(t, ProductPlanRegionCorrect(pricedInput("Beta Scale suits you.")), CheckProductPlan, true, "plan")
	})
	t.Run("region not offered blocks", func(t *testing.T) {
		expectFinding(t, ProductPlanRegionCorrect(pricedInput("Beta is hosted in the EU.")), CheckProductPlan, true, "region")
	})
	t.Run("empty catalog passes", func(t *testing.T) {
		expectNone(t, ProductPlanRegionCorrect(baseInput()))
	})
}

func TestQuantitiesMatchEvidence(t *testing.T) {
	t.Run("quoted quantity passes", func(t *testing.T) {
		expectNone(t, QuantitiesMatchEvidence(pricedInput("That covers 120 seats.")))
	})
	t.Run("quantity in evidence passes", func(t *testing.T) {
		in := baseInput()
		in.Draft.FinishedArtifact.Body = "For your 120 seats."
		expectNone(t, QuantitiesMatchEvidence(in))
	})
	t.Run("different quantity blocks and names the evidence", func(t *testing.T) {
		expectFinding(t, QuantitiesMatchEvidence(pricedInput("That covers 150 seats.")), CheckQuantitiesMatch, true, "evidence has 120")
	})
	t.Run("spelled number is read", func(t *testing.T) {
		expectFinding(t, QuantitiesMatchEvidence(pricedInput("Ten seats are included.")), CheckQuantitiesMatch, true, "Ten seats")
	})
	t.Run("a unit the evidence never counts is left to provenance", func(t *testing.T) {
		expectNone(t, QuantitiesMatchEvidence(pricedInput("Ten sites are included.")))
	})
}

func TestPricingIntegrityResult(t *testing.T) {
	pass := PricingIntegrity(pricedInput("Alpha is $40 per seat for 120 seats."))
	if pass.Result.Verdict != "pass" || len(pass.Checks) != 4 {
		t.Fatalf("pass = %+v", pass.Result)
	}
	expectContractValid(t, pass.Result)
	fail := PricingIntegrity(pricedInput("Alpha is $55 per seat with 25% off."))
	if fail.Result.Verdict != "fail" || !fail.Result.Blocking || len(fail.Findings) != 2 {
		t.Fatalf("fail = %+v", fail.Result)
	}
	expectContractValid(t, fail.Result)
}

func TestDiscountRequiresAnOfferConstruction(t *testing.T) {
	notOffers := []string{
		"You saw a 30% reduction in tickets.",
		"Great to hear the team saw a 30% reduction in tickets after rollout.",
		"With 80% of the team onboarded we can kick off phase two.",
		"You asked for 20% off, which we cannot do.",
	}
	for _, b := range notOffers {
		expectNone(t, DiscountNotInvented(pricedInput(b)))
	}
	offers := map[string]string{
		"We can do 15% off if you sign this month.":              "15%",
		"We can apply a 0.15 reduction to the list price.":       "15%",
		"We can take fifteen percent off the list price.":        "15%",
		"We can price this 15% below list.":                      "15%",
		"We will add two months free if you sign this month.":    "two months free",
		"Sign now for a 25% discount on the annual plan.":        "25%",
		"We'll include a free 3 months on the renewal contract.": "3 months",
	}
	for b, want := range offers {
		got := DiscountNotInvented(pricedInput(b))
		if len(got) != 1 || !strings.Contains(got[0].Detail, want) || !got[0].Blocking {
			t.Errorf("%q: findings = %+v, want one blocking finding mentioning %q", b, got, want)
		}
	}
	expectNone(t, DiscountNotInvented(pricedInput("We can do 10% off the list price.")))
}

func TestSpelledNumbersAndCurrencyForms(t *testing.T) {
	qty := map[string]float64{"twenty-five seats": 25, "fifty seats": 50, "a dozen seats": 12, "two hundred seats": 200,
		"one hundred twenty seats": 120, "twenty five seats": 25, "3 sites": 3}
	for text, want := range qty {
		got := quantities("We cover " + text + ".")
		if len(got) != 1 || got[0].Value != want {
			t.Errorf("%q = %+v, want %v", text, got, want)
		}
	}
	money := map[string]float64{"USD 55 per seat": 55, "55 USD": 55, "fifty dollars": 50, "ninety-eight thousand dollars": 98000,
		"€1,200": 1200, "$98k": 98000, "GBP 1,000.50": 1000.5}
	for text, want := range money {
		got := moneyAmounts("It is " + text + ".")
		if len(got) != 1 || !sameValue(got[0].Value, want) {
			t.Errorf("%q = %+v, want %v", text, got, want)
		}
	}
	if got := quantities("It is €40 per seat and £5 seats"); len(got) != 0 {
		t.Errorf("a price is not a quantity: %+v", got)
	}
	if v, ok := spelledNumber("banana"); ok || v != 0 {
		t.Errorf("banana is not a number")
	}
	pf := PriceSupported(pricedInput("Alpha is fifty dollars per seat."))
	expectFinding(t, pf, CheckPriceSupported, true, "fifty dollars")
	expectFinding(t, QuantitiesMatchEvidence(pricedInput("That covers twenty-five seats.")), CheckQuantitiesMatch, true, "twenty-five seats")
	expectNone(t, QuantitiesMatchEvidence(pricedInput("That covers one hundred twenty seats.")))
}

// Wording that cannot be classified with high precision flags without blocking; the semantic judges decide.
func TestUnclearDiscountWordingDoesNotBlock(t *testing.T) {
	for _, body := range []string{"You'll get 25% off your first year.", "Your usage is 25% below your licensed seats."} {
		got := DiscountNotInvented(pricedInput(body))
		if len(got) > 1 || (len(got) == 1 && got[0].Blocking) {
			t.Errorf("%q: findings = %+v; an unclear wording must not block", body, got)
		}
	}
	got := DiscountNotInvented(pricedInput("You'll get 25% off your first year."))
	if len(got) != 1 || !strings.Contains(got[0].Detail, "unclear") {
		t.Errorf("an unapproved percentage in unclear wording must be flagged: %+v", got)
	}
	if got := DiscountNotInvented(pricedInput("You'll get 25% off your renewal.")); len(got) != 1 || !got[0].Blocking {
		t.Errorf("your + a price noun is an offer and blocks: %+v", got)
	}
	if got := DiscountNotInvented(pricedInput("We'll discount the renewal by 25%.")); len(got) != 1 || !got[0].Blocking {
		t.Errorf("discount ... by N%% is an offer and blocks: %+v", got)
	}
}
