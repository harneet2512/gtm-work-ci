package bucket1run

import "github.com/harneet2512/gtm-work/core-go/internal/bucket1"

const maxPayloadClaims = 60

// judgePayload is what a gate's one model call reads, the evidence ids it may cite and the name of the
// assertion it settles. ok is false when the episode has nothing for the model to read.
func judgePayload(ep bucket1.Episode, gate string) (payload map[string]any, ids []string, assertion string, ok bool) {
	acts := make([]map[string]any, 0, len(ep.Activities))
	for _, a := range ep.Activities {
		acts = append(acts, map[string]any{"id": a.ID, "occurred_at": a.OccurredAt, "speaker": a.SpeakerID, "body": a.Text})
		ids = append(ids, a.ID)
	}
	claim := func(c bucket1.Claim) map[string]any {
		return map[string]any{"id": c.ID, "kind": c.Kind, "field": c.Field, "value": c.Value, "quote": c.Quote, "status": c.Status}
	}
	newClaims := make([]map[string]any, 0, len(ep.Claims))
	for _, c := range ep.Claims {
		newClaims = append(newClaims, claim(c))
		ids = append(ids, c.ID)
	}
	switch gate {
	case "B1":
		return map[string]any{"activities": acts, "claims": newClaims}, ids, "inference_boundary", len(ep.Claims) > 0
	case "B3":
		prior := make([]map[string]any, 0)
		for i, c := range ep.PriorClaims {
			if i >= maxPayloadClaims {
				break
			}
			prior = append(prior, claim(c))
			ids = append(ids, c.ID)
		}
		return map[string]any{"activities": acts, "new_claims": newClaims, "prior_claims": prior}, ids, "supporting_and_conflicting_links",
			len(ep.Claims) > 0 && len(ep.PriorClaims) > 0
	case "B5":
		prec := make([]map[string]any, 0, len(ep.Precedents))
		for _, p := range ep.Precedents {
			prec = append(prec, map[string]any{"id": p.ID, "shared_features": p.SharedFeatures, "lesson": p.Lesson})
			ids = append(ids, p.ID)
		}
		return map[string]any{"activities": acts, "precedents": prec}, ids, "relevance_and_misses", len(ep.Precedents) > 0
	case "B8":
		beliefs := make([]map[string]any, 0, len(ep.Beliefs))
		for _, b := range ep.Beliefs {
			beliefs = append(beliefs, map[string]any{"kind": b.Kind, "statement": b.Statement})
		}
		return map[string]any{"activities": acts, "claims": newClaims, "beliefs": beliefs}, ids, "confidence_and_omissions", len(ep.Beliefs) > 0
	}
	return nil, nil, "", false
}
