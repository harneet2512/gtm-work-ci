package bucket1run

import (
	"sort"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/bucket1"
)

// Row order must not depend on a row's random id: a tie in the database's ORDER BY would otherwise reorder the claims
// of one activity (they share a time) on every run, change the prompt and miss the recorded cassette. Every list is
// put in a content order here; ids stay in the prompt only so the model can cite them (the cache masks uuids by order of
// appearance, which is stable once the order is).

func claimLess(a, b bucket1.Claim) bool {
	if !a.OccurredAt.Equal(b.OccurredAt) {
		return a.OccurredAt.Before(b.OccurredAt)
	}
	return strings.Join([]string{a.Field, a.Value, a.Quote, a.Kind, a.Status}, "\x00") <
		strings.Join([]string{b.Field, b.Value, b.Quote, b.Kind, b.Status}, "\x00")
}

func sortedClaims(in []bucket1.Claim) []bucket1.Claim {
	out := append([]bucket1.Claim(nil), in...)
	sort.SliceStable(out, func(i, j int) bool { return claimLess(out[i], out[j]) })
	return out
}

func sortedActivities(in []bucket1.Activity) []bucket1.Activity {
	out := append([]bucket1.Activity(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].OccurredAt.Equal(out[j].OccurredAt) {
			return out[i].OccurredAt.Before(out[j].OccurredAt)
		}
		return out[i].Text < out[j].Text
	})
	return out
}

func sortedPrecedents(in []bucket1.Precedent) []bucket1.Precedent {
	out := append([]bucket1.Precedent(nil), in...)
	key := func(p bucket1.Precedent) string { return strings.Join(p.SharedFeatures, "\x01") + "\x00" + p.Lesson }
	sort.SliceStable(out, func(i, j int) bool { return key(out[i]) < key(out[j]) })
	return out
}

const maxPayloadClaims = 60

// judgePayload is what a gate's one model call reads, the evidence ids it may cite and the name of the
// assertion it settles. ok is false when the episode has nothing for the model to read.
func judgePayload(ep bucket1.Episode, gate string) (payload map[string]any, ids []string, assertion string, ok bool) {
	acts := make([]map[string]any, 0, len(ep.Activities))
	for _, a := range sortedActivities(ep.Activities) {
		acts = append(acts, map[string]any{"id": a.ID, "occurred_at": a.OccurredAt, "speaker": a.SpeakerID, "body": a.Text})
		ids = append(ids, a.ID)
	}
	claim := func(c bucket1.Claim) map[string]any {
		return map[string]any{"id": c.ID, "kind": c.Kind, "field": c.Field, "value": c.Value, "quote": c.Quote, "status": c.Status}
	}
	newClaims := make([]map[string]any, 0, len(ep.Claims))
	for _, c := range sortedClaims(ep.Claims) {
		newClaims = append(newClaims, claim(c))
		ids = append(ids, c.ID)
	}
	switch gate {
	case "B1":
		return map[string]any{"activities": acts, "claims": newClaims}, ids, "inference_boundary", len(ep.Claims) > 0
	case "B3":
		prior := make([]map[string]any, 0)
		for i, c := range sortedClaims(ep.PriorClaims) {
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
		for _, p := range sortedPrecedents(ep.Precedents) {
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
