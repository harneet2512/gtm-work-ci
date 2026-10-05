package deterministic

import (
	"fmt"
	"strings"
)

// HAR-97 §4 "Number / product / pricing integrity" sub-checks.
const (
	CheckPriceSupported   Check = "pricing.price_supported"
	CheckDiscountApproved Check = "pricing.discount_not_invented"
	CheckProductPlan      Check = "pricing.product_plan_region"
	CheckQuantitiesMatch  Check = "pricing.quantities_match_evidence"
)

var pricingChecks = []Check{CheckPriceSupported, CheckDiscountApproved, CheckProductPlan, CheckQuantitiesMatch}

// PricingIntegrity is the HAR-97 §4 "Number / product / pricing integrity" eval.
func PricingIntegrity(in Input) Judgment {
	var f []Finding
	f = append(f, PriceSupported(in)...)
	f = append(f, DiscountNotInvented(in)...)
	f = append(f, ProductPlanRegionCorrect(in)...)
	f = append(f, QuantitiesMatchEvidence(in)...)
	return judge(in, TypePricing, pricingChecks, []string{"commercial_issue", "product_use_case"}, f)
}

// supportedAmounts are quoted unit prices, totals, quantity x unit price, and the amounts the evidence states.
func supportedAmounts(in Input) []float64 {
	var out []float64
	for _, q := range in.Commercial.Quoted {
		if q.UnitPrice != nil {
			out = append(out, *q.UnitPrice)
		}
		if q.Total != nil {
			out = append(out, *q.Total)
		}
		if q.UnitPrice != nil && q.Quantity != nil {
			out = append(out, *q.UnitPrice**q.Quantity)
		}
	}
	for _, a := range moneyAmounts(supportText(in)) {
		out = append(out, a.Value)
	}
	return out
}

func hasValue(xs []float64, v float64) bool {
	for _, x := range xs {
		if sameValue(x, v) {
			return true
		}
	}
	return false
}

// PriceSupported: every price in the draft is a quoted price (or a quote's quantity x unit price)
// or an amount the cited evidence or state states.
func PriceSupported(in Input) []Finding {
	supported := supportedAmounts(in)
	var out []Finding
	for _, a := range moneyAmounts(draftText(in.Draft)) {
		if !hasValue(supported, a.Value) {
			out = append(out, failure(CheckPriceSupported,
				fmt.Sprintf("price %q is not a quoted price and no cited evidence states it", a.Text),
				fmt.Sprintf("Remove %q or replace it with a quoted price; do not state prices without a source.", a.Text)).
				blocking().withState("commercial_issue"))
		}
	}
	return out
}

// DiscountNotInvented: every discount percentage the draft offers is on the workspace's approved
// list, and no free-time concession ("two months free") is offered: the contract carries no
// approved concessions.
func DiscountNotInvented(in Input) []Finding {
	var out []Finding
	for _, d := range discounts(draftText(in.Draft), catalogProducts(in)) {
		switch {
		case d.Free != "":
			out = append(out, failure(CheckDiscountApproved,
				fmt.Sprintf("offers %q, which is not an approved concession", d.Free),
				fmt.Sprintf("Remove %q or get it approved first.", d.Free)).blocking())
		case !hasValue(in.Commercial.ApprovedDiscountPercents, d.Percent) && d.Sure && !d.Unclear:
			out = append(out, failure(CheckDiscountApproved,
				fmt.Sprintf("offers a %g%% discount that is not approved", d.Percent),
				fmt.Sprintf("Remove the %g%% discount or get it approved first.", d.Percent)).blocking())
		case !hasValue(in.Commercial.ApprovedDiscountPercents, d.Percent):
			out = append(out, failure(CheckDiscountApproved,
				fmt.Sprintf("may offer a %g%% discount (the wording is unclear) that is not approved", d.Percent),
				fmt.Sprintf("Check whether %g%% is a discount offer; if it is, remove it or get it approved.", d.Percent)))
		}
	}
	return out
}

func hasFold(xs []string, s string) bool {
	for _, x := range xs {
		if strings.EqualFold(strings.TrimSpace(x), strings.TrimSpace(s)) {
			return true
		}
	}
	return false
}

// ProductPlanRegionCorrect: where a clause names a catalog product together with a plan or region,
// that product offers that plan and region.
func ProductPlanRegionCorrect(in Input) []Finding {
	var plans, regions []string
	for _, it := range in.Commercial.Catalog {
		plans = append(plans, it.Plans...)
		regions = append(regions, it.Regions...)
	}
	var out []Finding
	for _, c := range clauses(draftText(in.Draft)) {
		for _, it := range in.Commercial.Catalog {
			if !containsFold(c, it.Product) {
				continue
			}
			out = append(out, wrongOffer(it.Product, "plan", c, plans, it.Plans)...)
			out = append(out, wrongOffer(it.Product, "region", c, regions, it.Regions)...)
		}
	}
	return out
}

// wrongOffer reports names of kind (plan/region) in clause that exist in the catalog but not for product.
func wrongOffer(product, kind, clause string, all, offered []string) []Finding {
	var out []Finding
	for _, n := range unique(all) {
		if containsFold(clause, n) && !hasFold(offered, n) {
			out = append(out, failure(CheckProductPlan,
				fmt.Sprintf("%s is described with %s %q, which it does not offer", product, kind, n),
				fmt.Sprintf("Use only the %ss %s offers: %s.", kind, product, strings.Join(offered, ", "))).blocking())
		}
	}
	return out
}

// QuantitiesMatchEvidence: a "<n> <unit>" in the draft that the evidence states differently for
// that unit is a mismatch. A unit the evidence never counts cannot be contradicted here; the
// provenance eval reports it as unsupported instead.
func QuantitiesMatchEvidence(in Input) []Finding {
	supported := quantities(supportText(in))
	for _, q := range in.Commercial.Quoted {
		if q.Quantity != nil {
			unit := ""
			if q.Unit != nil {
				unit = singular(*q.Unit)
			}
			supported = append(supported, Quantity{Value: *q.Quantity, Unit: unit})
		}
	}
	var out []Finding
	for _, d := range quantities(draftText(in.Draft)) {
		if !quantitySupported(supported, d) && sameUnitCounted(supported, d) {
			out = append(out, failure(CheckQuantitiesMatch,
				fmt.Sprintf("states %q, which no quote or cited evidence supports%s", d.Text, otherQuantities(supported, d)),
				fmt.Sprintf("Correct the %s count to what the evidence says, or remove it.", d.Unit)).blocking())
		}
	}
	return out
}

func sameUnitCounted(supported []Quantity, d Quantity) bool {
	for _, s := range supported {
		if s.Unit == d.Unit {
			return true
		}
	}
	return false
}

func quantitySupported(supported []Quantity, d Quantity) bool {
	for _, s := range supported {
		if sameValue(s.Value, d.Value) && (s.Unit == "" || s.Unit == d.Unit) {
			return true
		}
	}
	return false
}

func otherQuantities(supported []Quantity, d Quantity) string {
	var vals []string
	for _, s := range supported {
		if s.Unit == d.Unit {
			vals = append(vals, fmt.Sprintf("%g", s.Value))
		}
	}
	if len(vals) == 0 {
		return ""
	}
	return fmt.Sprintf(" (evidence has %s)", strings.Join(unique(vals), ", "))
}

// catalogProducts are the names of the products the workspace sells or has quoted.
func catalogProducts(in Input) []string {
	var out []string
	for _, it := range in.Commercial.Catalog {
		out = append(out, it.Product)
	}
	for _, q := range in.Commercial.Quoted {
		out = append(out, q.Product)
	}
	return unique(out)
}
