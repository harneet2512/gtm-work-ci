package bucket2

import (
	"fmt"
	"sort"
	"strings"
)

// KnowledgeUseInput is what the EcoLite continuation needs. Retrieved and Applicable come from the run's
// knowledge_attribution. Offered is the knowledge that was actually in the D1 to D3 inputs (the judges and the
// ranking step saw it). Cited is the knowledge the ranking reasons or the intent rationale name.
type KnowledgeUseInput struct {
	EpisodeID             string
	Retrieved, Applicable []string
	Offered, Cited        []string
}

// KnowledgeUseReport reports the three values separately. None of them is "influence": used means only that the
// knowledge appears in the D1 to D3 inputs AND is cited in the ranking or the intent rationale.
type KnowledgeUseReport struct {
	Retrieved, Applicable, Used []string
}

func intersect(a, b []string) []string {
	in := map[string]bool{}
	for _, x := range b {
		in[x] = true
	}
	var out []string
	for _, x := range dedupe(a) {
		if in[x] {
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

// MeasureKnowledgeUse computes retrieved, applicable and used. Used is only ever knowledge that was applicable,
// offered to D1 to D3 and cited in the ranking or intent rationale.
func MeasureKnowledgeUse(in KnowledgeUseInput) (KnowledgeUseReport, Result) {
	rep := KnowledgeUseReport{Retrieved: dedupe(in.Retrieved), Applicable: dedupe(in.Applicable),
		Used: intersect(intersect(in.Applicable, in.Offered), in.Cited)}
	r := Result{Gate: "D1", SubGate: "knowledge_use", JudgedType: "DecisionEpisode", JudgedID: in.EpisodeID, SpanID: "knowledge_used:" + in.EpisodeID, Grader: Deterministic}
	r.Observed = fmt.Sprintf("retrieved %d, applicable %d, used %d (used = in the D1 to D3 inputs and cited in the ranking or intent rationale)",
		len(rep.Retrieved), len(rep.Applicable), len(rep.Used))
	switch {
	case len(rep.Applicable) == 0:
		r.Verdict, r.Why = Unknown, "no knowledge was applicable here, so its use was not measured"
		r.EvidenceRefs = prefixed("knowledge:", rep.Retrieved)
		if len(r.EvidenceRefs) == 0 {
			r.EvidenceRefs = []string{"decision_episode:" + in.EpisodeID}
		}
	case len(rep.Used) == 0:
		r.Verdict, r.EvidenceRefs = Warn, prefixed("knowledge:", rep.Applicable)
		r.Why = "applicable knowledge was not cited in the ranking or the intent rationale: " + strings.Join(rep.Applicable, ", ")
	default:
		r.Verdict, r.EvidenceRefs = Pass, prefixed("knowledge:", rep.Used)
		r.Why = "applicable knowledge appears in the D1 to D3 inputs and is cited in the ranking or the intent rationale"
	}
	return rep, r.Finalize()
}
