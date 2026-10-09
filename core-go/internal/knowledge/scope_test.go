package knowledge

import "testing"

func TestScopeFieldsAreDistinctAcrossSignatureAndApplicability(t *testing.T) {
	k := Knowledge{
		SituationSignature:      []Condition{cond("transition.status", OpEq, "candidate")},
		ApplicabilityConditions: []Condition{cond("transition.to_state", OpEq, "EXPANSION"), cond("transition.status", OpNeq, "x")},
	}
	got := ScopeFields(k)
	if len(got) != 2 || got[0] != "transition.status" || got[1] != "transition.to_state" {
		t.Fatalf("scope fields = %v", got)
	}
	if len(got) != 2 {
		t.Fatal("two distinct fields")
	}
	if !ScopeTooBroad(k) {
		t.Fatal("a transition-only scope reads no account state and is too broad")
	}
	k.ApplicabilityConditions = append(k.ApplicabilityConditions, cond("relationship_state", OpEq, "STABLE"))
	if ScopeTooBroad(k) {
		t.Fatal("a transition plus the relationship state is not too broad")
	}
}

func TestAnUnknownConditionConstrainsNoState(t *testing.T) {
	k := Knowledge{SituationSignature: []Condition{cond("relationship_state", OpIsUnknown), cond("transition.status", OpEq, "candidate")}}
	if got := StateScopeFields(k); len(got) != 0 || !ScopeTooBroad(k) {
		t.Fatalf("state fields %v, too broad %v", got, ScopeTooBroad(k))
	}
}

// A learned candidate whose scope is too broad stays a candidate however much evidence arrives; a scoped one
// earns its rung from a customer reply.
func TestRecordHoldsBroadLearnedKnowledgeAsACandidate(t *testing.T) {
	rules := contractRules(t)
	rules.Similarity = nil // exact matching: the scope itself must be narrow (similarity-matched knowledge is bounded by its threshold)
	reply := Evidence{Kind: EvidenceCustomerReaction, RefID: "r1", Polarity: "positive", At: now}
	episode := Evidence{Kind: EvidenceDecisionEpisode, RefID: "e1", At: now}
	broad := candidate()
	broad.Provenance.CreatedFrom = "human_delta"
	broad.SituationSignature = []Condition{cond("transition.status", OpEq, "candidate")}
	next, change, err := Record(broad, episode, rules)
	if err != nil {
		t.Fatal(err)
	}
	next, change, err = Record(next, reply, rules)
	if err != nil || change != nil || next.Status != StatusCandidate || next.Counts.PositiveReactions != 1 {
		t.Fatalf("broad: %s %+v %v counts %+v", next.Status, change, err, next.Counts)
	}
	scoped := candidate()
	scoped.Provenance.CreatedFrom = "human_delta"
	scoped.SituationSignature = []Condition{cond("transition.status", OpEq, "candidate")}
	scoped.ApplicabilityConditions = []Condition{cond("relationship_state", OpEq, "STABLE")}
	next, _, _ = Record(scoped, episode, rules)
	next, change, err = Record(next, reply, rules)
	if err != nil || change == nil || next.Status != StatusProvisional {
		t.Fatalf("scoped: %s %+v %v", next.Status, change, err)
	}
}

func TestAStatusOnlyScopeIsTooBroad(t *testing.T) {
	k := Knowledge{SituationSignature: []Condition{cond("transition.status", OpEq, "candidate")}}
	if !ScopeTooBroad(k) {
		t.Fatal("transition.status alone must be too broad")
	}
	if !ScopeTooBroad(Knowledge{}) {
		t.Fatal("an empty scope is too broad")
	}
}

// A human's choice is evidence that the human chose it, not that it worked: only customer or world evidence
// refreshes last_validated_at (HAR-97 B9: human choice is not truth).
func TestAHumanDecisionEpisodeDoesNotRevalidateKnowledge(t *testing.T) {
	kn := candidate()
	old := now.Add(-100 * day)
	kn.LastValidatedAt = &old
	next, err := ApplyEvidence(kn, Evidence{Kind: EvidenceDecisionEpisode, RefID: "e1", At: now})
	if err != nil {
		t.Fatal(err)
	}
	if next.Counts.Decisions != 1 {
		t.Fatalf("the decision is still counted: %+v", next.Counts)
	}
	if !next.LastValidatedAt.Equal(old) {
		t.Fatalf("a human choice moved last_validated_at to %v", next.LastValidatedAt)
	}
}

func TestACustomerReplyRevalidatesKnowledge(t *testing.T) {
	next, err := ApplyEvidence(candidate(), Evidence{Kind: EvidenceCustomerReaction, RefID: "r1", Polarity: "positive", At: now})
	if err != nil {
		t.Fatal(err)
	}
	if next.LastValidatedAt == nil || !next.LastValidatedAt.Equal(now) {
		t.Fatalf("customer evidence must revalidate: %v", next.LastValidatedAt)
	}
}

func TestOneEditWithoutAReplyStaysACandidate(t *testing.T) {
	rules := contractRules(t)
	next, _, err := Record(candidate(), Evidence{Kind: EvidenceDecisionEpisode, RefID: "e1", At: now}, rules)
	if err != nil {
		t.Fatal(err)
	}
	if rules.Applicable(next.Status) {
		t.Fatalf("a human edit alone became %s, which is applicable", next.Status)
	}
}

func TestSimilarityMatchedLearnedKnowledgeIsNotHeldBackByTheExactScopeBreadthCheck(t *testing.T) {
	rules := contractRules(t)
	broad := candidate()
	broad.Provenance.CreatedFrom = "human_delta"
	broad.SituationSignature = []Condition{cond("transition.status", OpEq, "candidate")}
	next, _, _ := Record(broad, Evidence{Kind: EvidenceDecisionEpisode, RefID: "e1", At: now}, rules)
	next, change, err := Record(next, Evidence{Kind: EvidenceCustomerReaction, RefID: "r1", Polarity: "positive", At: now}, rules)
	if err != nil || change == nil || next.Status != StatusProvisional {
		t.Fatalf("%s %+v %v", next.Status, change, err)
	}
}
