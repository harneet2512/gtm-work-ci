package transitions

// scopes returns one evaluator per scope the rule reads (ADR-0016). With per-deal states, the deal-scoped
// facts are read from every open deal ("any open deal satisfies"), never from the account headline, so a
// change of primary deal cannot confirm or reverse a transition. A "deal" rule sees, for each deal, that
// deal's claims and signals plus those that name no deal; an "account" rule sees all of them. With no open
// deal (or no deal states) the account headline in in.State is the only scope.
func scopes(rs RuleSet, rule Rule, in Input) []*evaluator {
	var open []Deal
	for _, d := range in.Deals {
		if d.IsOpen {
			open = append(open, d)
		}
	}
	if len(in.Deals) == 0 || len(open) == 0 {
		return []*evaluator{newEvaluator(rs, in)}
	}
	out := make([]*evaluator, 0, len(open))
	for _, d := range open {
		scoped := in
		scoped.State.Fields, scoped.State.BuyingGroup = d.Fields, d.BuyingGroup
		if rule.ClaimScope != "account" {
			scoped.Claims, scoped.Signals = nil, nil
			for _, c := range in.Claims {
				if c.OpportunityID == "" || c.OpportunityID == d.OpportunityID {
					scoped.Claims = append(scoped.Claims, c)
				}
			}
			for _, s := range in.Signals {
				if s.OpportunityID == "" || s.OpportunityID == d.OpportunityID {
					scoped.Signals = append(scoped.Signals, s)
				}
			}
		}
		out = append(out, newEvaluator(rs, scoped))
	}
	return out
}
