package transitions

import "slices"

// MarkFirstParty returns the signals with FirstParty set from the standing of the claims each one cites
// (ADR-0009: enrichment never earns a state). standingOf maps a claim id to its standing. A signal that cites
// no claim rests on activities alone and counts as first-party; one that cites claims counts when at least one
// of them has a standing in the rule set's claim_standings. A cited claim missing from standingOf cannot be
// proven first-party, so it does not count. The input slice is not modified.
func MarkFirstParty(rs RuleSet, signals []Signal, standingOf map[string]string) []Signal {
	out := make([]Signal, len(signals))
	for i, s := range signals {
		first := true
		cited := false
		for _, r := range s.EvidenceRefs {
			if r.ClaimID == "" {
				continue
			}
			if !cited {
				cited, first = true, false
			}
			if st, ok := standingOf[r.ClaimID]; ok && slices.Contains(rs.ClaimStandings, st) {
				first = true
			}
		}
		s.FirstParty = &first
		out[i] = s
	}
	return out
}

// CitedClaimIDs lists the distinct claim ids the signals cite, so a caller can look their standings up.
func CitedClaimIDs(signals []Signal) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range signals {
		for _, r := range s.EvidenceRefs {
			if r.ClaimID != "" && !seen[r.ClaimID] {
				seen[r.ClaimID] = true
				out = append(out, r.ClaimID)
			}
		}
	}
	return out
}
