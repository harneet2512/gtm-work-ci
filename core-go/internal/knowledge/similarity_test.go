package knowledge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func contractSimilarity(t *testing.T) Similarity {
	t.Helper()
	s, err := LoadSimilarity(repoFile(t, "contracts/knowledge/similarity.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestContractSimilarityLoadsAndIsBesideTheLifecycleRules(t *testing.T) {
	s := contractSimilarity(t)
	if s.Version != "knowledge_similarity:v1" || s.Threshold != 0.6 || s.MinComparable != 2 || len(s.Features) != 9 {
		t.Fatalf("similarity = %+v", s)
	}
	r := contractRules(t)
	if r.Similarity == nil || r.Similarity.Threshold != s.Threshold {
		t.Fatal("LoadRules must load similarity.v1.json from the lifecycle file's directory")
	}
	if !r.Similarity.Matches(Knowledge{Provenance: Provenance{CreatedFrom: "manual"}}) ||
		r.Similarity.Matches(Knowledge{Provenance: Provenance{CreatedFrom: "authored"}}) {
		t.Fatal("manual lessons are matched by similarity, authored knowledge keeps the exact matcher")
	}
}

func TestInvalidSimilarityRulesAreRejected(t *testing.T) {
	cases := map[string]func(map[string]any){
		"unknown key":               func(d map[string]any) { d["tune"] = 1 },
		"zero threshold":            func(d map[string]any) { d["threshold"] = 0 },
		"threshold above one":       func(d map[string]any) { d["threshold"] = 1.2 },
		"no minimum":                func(d map[string]any) { d["min_comparable_features"] = 0 },
		"minimum too high":          func(d map[string]any) { d["min_comparable_features"] = 10 },
		"no weight minimum":         func(d map[string]any) { d["min_comparable_weight"] = 0 },
		"weight minimum over total": func(d map[string]any) { d["min_comparable_weight"] = 19 },
		"unknown feature":           func(d map[string]any) { d["features"].([]any)[0].(map[string]any)["id"] = "mood" },
		"zero weight":               func(d map[string]any) { d["features"].([]any)[0].(map[string]any)["weight"] = 0 },
		"no rationale":              func(d map[string]any) { d["features"].([]any)[0].(map[string]any)["rationale"] = "" },
		"duplicate feature":         func(d map[string]any) { d["features"].([]any)[1].(map[string]any)["id"] = "transition" },
		"no sources":                func(d map[string]any) { d["applies_to_created_from"] = []any{} },
		"no features":               func(d map[string]any) { d["features"] = []any{} },
	}
	for name, mutate := range cases {
		var doc map[string]any
		raw, _ := json.Marshal(contractSimilarity(t))
		_ = json.Unmarshal(raw, &doc)
		mutate(doc)
		bad, _ := json.Marshal(doc)
		if _, err := ParseSimilarity(bad); !errors.Is(err, ErrInvalidSimilarity) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func scalarCond(field, value string) Condition {
	return Condition{Field: field, Op: OpEq, Value: value}
}

// lesson builds a manual candidate whose scope is the given conditions.
func lesson(title string, conds ...Condition) Knowledge {
	ep := "00000000-0000-4000-8000-0000000000e1"
	return Knowledge{ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", len(title)), Title: title, Status: StatusCandidate,
		SituationSignature: conds[:1], ApplicabilityConditions: conds[1:], Counts: Counts{Decisions: 1},
		Provenance: Provenance{CreatedFrom: "manual", SourceDecisionEpisodeID: &ep}}
}

func caseSituation(stage, motion, rel string, signals ...string) Situation {
	s := situation(map[string]Value{"stage": known(stage), "motion": known(motion)})
	s.RelationshipState = rel
	for _, typ := range signals {
		s.Signals = append(s.Signals, signal(typ, 24*3600*1e9))
	}
	return s
}

func TestUnknownOnEitherSideContributesNothing(t *testing.T) {
	sim := contractSimilarity(t)
	l := LessonFeatures(lesson("L", scalarCond("stage", "unknown"), scalarCond("relationship_state", "Unknown"),
		scalarCond("motion", "expansion")))
	if len(l) != 1 {
		t.Fatalf("an unknown value is never a lesson feature: %v", l)
	}
	c := CaseFeatures(caseSituation("unknown", "expansion", ""))
	if _, ok := c[FeatStage]; ok || len(c) != 2 { // motion, and signals (none open is known)
		t.Fatalf("an unknown case value is never a case feature: %v", c)
	}
	sc := sim.ScoreFeatures(l, c)
	if sc.Comparable != 1 || sc.Value != 1 || len(sc.Differs) != 0 {
		t.Fatalf("score = %+v", sc)
	}
}

func TestScoreIsTheWeightedShareOfAgreementOverComparableFeatures(t *testing.T) {
	sim := contractSimilarity(t)
	l := LessonFeatures(lesson("L", scalarCond("stage", "negotiation"), scalarCond("motion", "expansion"),
		scalarCond("relationship_state", "STABLE"), scalarCond("transition.from_state", "REORG"),
		scalarCond("transition.to_state", "EXPANSION")))
	c := CaseFeatures(caseSituation("Negotiation", "new business", "STABLE"))
	sc := sim.ScoreFeatures(l, c) // stage 2 + relationship 2 agree, motion 2 differs; transition unknown in the case
	if sc.Comparable != 3 || sc.Value != 0.6667 {
		t.Fatalf("score = %+v, want 3 comparable at 4/6", sc)
	}
	if len(sc.Matched) != 2 || len(sc.Differs) != 1 || sc.Differs[0].ID != FeatMotion {
		t.Fatalf("matched %v differs %v", sc.Matched, sc.Differs)
	}
}

func TestSignalAgreementIsTheShareOfTheLessonsTriggerOpenNow(t *testing.T) {
	sim := contractSimilarity(t)
	l := LessonFeatures(lesson("L", cond("diff.customer_replied", OpExists), cond("diff.pricing_interest", OpExists)))
	c := CaseFeatures(caseSituation("x", "y", "", "customer_replied", "stage_advanced"))
	sc := sim.ScoreFeatures(l, c)
	if sc.Comparable != 1 || sc.Value != 0.5 || sc.Matched[0].Lesson != "customer_replied, pricing_interest" {
		t.Fatalf("score = %+v", sc)
	}
}

func retrieve(t *testing.T, ks []Knowledge, s Situation) ([]Result, *Retrieval) {
	t.Helper()
	sim := contractSimilarity(t)
	rs, rv, err := RetrieveAll(ks, s, &sim)
	if err != nil {
		t.Fatal(err)
	}
	return rs, rv
}

func TestClosestLessonAtOrAboveTheThresholdIsOfferedWithItsScoreAndStatus(t *testing.T) {
	near := lesson("Near", scalarCond("stage", "negotiation"), scalarCond("motion", "expansion"),
		scalarCond("relationship_state", "STABLE"), scalarCond("transition.status", "CANDIDATE"))
	s := caseSituation("Negotiation", "Expansion", "STABLE")
	rs, rv := retrieve(t, []Knowledge{near}, s)
	if rs[0].Label != LabelApplies || !rs[0].Entry.Applies {
		t.Fatalf("result = %+v", rs[0])
	}
	e := rs[0].Entry.Similarity
	if e == nil || e.Status != StatusCandidate || e.Score != 1 || len(e.Matched) != 3 || len(e.Differs) != 0 {
		t.Fatalf("entry similarity = %+v", e)
	}
	if rv.Candidates[0].Decision != DecisionApplicable || !strings.HasPrefix(rv.Message, "Closest past lesson: Near (candidate), similarity 1.00") {
		t.Fatalf("retrieval = %+v", rv)
	}
}

func TestBelowThresholdIsNotOfferedAndTheMessageNamesTheClosestLesson(t *testing.T) {
	far := lesson("Far", scalarCond("stage", "discovery"), scalarCond("motion", "renewal"), scalarCond("relationship_state", "STABLE"))
	s := caseSituation("Negotiation", "Expansion", "STABLE") // one of three agree: 2/6 = 0.33
	rs, rv := retrieve(t, []Knowledge{far}, s)
	if rs[0].Label != LabelDoesNotApply || rs[0].Entry.Applies || rs[0].Entry.Similarity != nil {
		t.Fatalf("a below-threshold lesson must not be offered: %+v", rs[0])
	}
	want := "No similar knowledge in the knowledge base (closest: Far, similarity 0.33 — below 0.60)."
	if rv.Message != want || rv.Candidates[0].Decision != DecisionBelowThreshold {
		t.Fatalf("message %q decision %s", rv.Message, rv.Candidates[0].Decision)
	}
}

func TestTooFewComparableFeaturesCannotScoreHigh(t *testing.T) {
	weak := lesson("Weak", scalarCond("stage", "negotiation"))
	s := caseSituation("Negotiation", "Expansion", "") // perfect agreement, but only one comparable feature
	rs, rv := retrieve(t, []Knowledge{weak}, s)
	if rs[0].Entry.Applies || rv.Candidates[0].Decision != DecisionInsufficientFeatures || rv.Candidates[0].Score != 1 {
		t.Fatalf("result %+v retrieval %+v", rs[0], rv)
	}
	if !strings.Contains(rv.Message, "only 1 comparable feature, 2 needed") {
		t.Fatalf("message = %q", rv.Message)
	}
}

func TestAnExceptionStillBlocksALessonAboveTheThreshold(t *testing.T) {
	near := lesson("Near", scalarCond("stage", "negotiation"), scalarCond("motion", "expansion"), scalarCond("relationship_state", "STABLE"))
	near.Exceptions = []Exception{exc("champion left", cond("motion", OpEq, "expansion"))}
	rs, rv := retrieve(t, []Knowledge{near}, caseSituation("Negotiation", "Expansion", "STABLE"))
	if rs[0].Label != LabelExceptionTriggered || rs[0].Entry.Applies || rv.Candidates[0].Decision != DecisionExceptionBlocked {
		t.Fatalf("result %+v retrieval %+v", rs[0], rv)
	}
}

func TestRankingIsClosestFirstAndTotal(t *testing.T) {
	a := lesson("Aa", scalarCond("stage", "negotiation"), scalarCond("motion", "expansion"), scalarCond("relationship_state", "STABLE"))
	b := lesson("Bbb", scalarCond("stage", "negotiation"), scalarCond("motion", "renewal"), scalarCond("relationship_state", "STABLE"))
	weak := lesson("W", scalarCond("stage", "negotiation"))
	_, rv := retrieve(t, []Knowledge{weak, b, a}, caseSituation("Negotiation", "Expansion", "STABLE"))
	got := []string{rv.Candidates[0].Title, rv.Candidates[1].Title, rv.Candidates[2].Title}
	if strings.Join(got, ",") != "Aa,Bbb,W" {
		t.Fatalf("order = %v", got)
	}
}

func TestNoLessonsSaysSoAndAuthoredKnowledgeKeepsTheExactMatcher(t *testing.T) {
	sim := contractSimilarity(t)
	authored := k([]Condition{cond("stage", OpEq, "negotiation")})
	rs, rv, err := RetrieveAll([]Knowledge{authored}, caseSituation("Negotiation", "x", ""), &sim)
	if err != nil || rv == nil || len(rv.Candidates) != 0 || rs[0].Label != LabelApplies || rs[0].Entry.Similarity != nil {
		t.Fatalf("authored knowledge: %+v %+v %v", rs, rv, err)
	}
	if !strings.HasPrefix(rv.Message, "No similar knowledge in the knowledge base") {
		t.Fatalf("message = %q", rv.Message)
	}
}

func TestSimilarityMatchedLessonsAreNotBoundByTheExactScopeBreadthCheck(t *testing.T) {
	r := contractRules(t)
	l := lesson("Broad", scalarCond("transition.status", "CANDIDATE"))
	if !r.ApplicableKnowledge(l) {
		t.Fatal("a manual narrow candidate is offered; the similarity threshold bounds it at match time")
	}
	r.Similarity = nil
	if r.ApplicableKnowledge(l) {
		t.Fatal("without similarity rules the exact-scope breadth check holds")
	}
}

// The retrieval record and the guidance entry are contract-valid.
func TestRetrievalAndEntriesAreContractValid(t *testing.T) {
	c := jsonschema.NewCompiler()
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(repoFile(t, "contracts/schemas/common.v1.json")), "*.json"))
	for _, f := range files {
		doc := readJSON(t, f)
		if err := c.AddResource(doc["$id"].(string), doc); err != nil {
			t.Fatal(err)
		}
	}
	validate := func(ref string, v any) error {
		schema, err := c.Compile(ref)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(v)
		inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		return schema.Validate(inst)
	}
	if err := validate("https://ghost.local/contracts/knowledge_similarity.v1.json", contractSimilarity(t)); err != nil {
		t.Fatalf("the similarity rules violate their schema: %v", err)
	}
	near := lesson("Near", scalarCond("stage", "negotiation"), scalarCond("motion", "expansion"), scalarCond("relationship_state", "STABLE"))
	near.ID = "00000000-0000-4000-8000-0000000000f1"
	s := caseSituation("Negotiation", "Expansion", "STABLE")
	s.Fields["stage"] = Value{Known: true, Scalar: "negotiation", EvidenceRefs: ref("00000000-0000-4000-8000-0000000000a1")}
	s.Fields["motion"] = Value{Known: true, Scalar: "expansion", EvidenceRefs: ref("00000000-0000-4000-8000-0000000000a2")}
	rs, rv := retrieve(t, []Knowledge{near}, s)
	if err := validate("https://ghost.local/contracts/knowledge_similarity.v1.json#/$defs/retrieval", rv); err != nil {
		t.Fatalf("retrieval record invalid: %v", err)
	}
	doc := map[string]any{
		"id": "00000000-0000-4000-8000-0000000000c1", "account_id": "00000000-0000-4000-8000-0000000000c2", "state_version": 3,
		"recommended_action": "wait", "why_now": "x", "who_to_involve": []any{}, "who_not_to_involve": []any{},
		"supporting_knowledge": []Entry{rs[0].Entry}, "created_at": "2026-10-01T12:00:00Z",
	}
	if err := validate("https://ghost.local/contracts/decision_guidance.v1.json", doc); err != nil {
		t.Fatalf("guidance entry with similarity invalid: %v", err)
	}
}

// H1: three weak features are three comparable features but only 3 of 15 weight units; a lesson cannot be offered on
// them alone (min_comparable_weight), however perfectly they agree.
func TestThreeWeightOneFeaturesCannotCarryALesson(t *testing.T) {
	weak3 := lesson("Weak3", scalarCond("transition.status", "CANDIDATE"), scalarCond("health", "at_risk"), scalarCond("champion_status", "engaged"))
	s := situation(map[string]Value{"health": known("at_risk"), "champion_status": known("engaged")})
	s.Transition = &Transition{ID: "t1", Status: "CANDIDATE"}
	rs, rv := retrieve(t, []Knowledge{weak3}, s)
	c := rv.Candidates[0]
	if c.Comparable != 3 || c.Score != 1 || c.ComparableWeight != 3 {
		t.Fatalf("candidate = %+v", c)
	}
	if rs[0].Entry.Applies || c.Decision != DecisionInsufficientFeatures {
		t.Fatalf("three weight-1 features must not be offered: %+v", rs[0])
	}
	want := "No similar knowledge in the knowledge base (closest: Weak3, 3 comparable features but only weight 3, 5 needed)."
	if rv.Message != want {
		t.Fatalf("message = %q", rv.Message)
	}
	if rv.MinWeight != 5 {
		t.Fatalf("the record carries the rule: %+v", rv)
	}
}

// Owner decision 2026-10-06: two strong facts are enough, one heavy feature alone is not, two medium features (weight 4) are not.
func TestOneHeavyFeatureAloneAndTwoMediumFeaturesAreStillInsufficient(t *testing.T) {
	heavy := lesson("Heavy", cond("diff.customer_replied", OpExists))
	_, rv := retrieve(t, []Knowledge{heavy}, caseSituation("x", "y", "", "customer_replied"))
	if c := rv.Candidates[0]; c.Decision != DecisionInsufficientFeatures || c.Score != 1 || c.Comparable != 1 || c.ComparableWeight != 3 {
		t.Fatalf("one heavy feature: %+v", c)
	}
	medium := lesson("Medium", scalarCond("stage", "negotiation"), scalarCond("motion", "expansion"))
	rs, rv := retrieve(t, []Knowledge{medium}, caseSituation("Negotiation", "Expansion", ""))
	if c := rv.Candidates[0]; rs[0].Entry.Applies || c.Decision != DecisionInsufficientFeatures || c.Comparable != 2 || c.ComparableWeight != 4 {
		t.Fatalf("two medium features: %+v", c)
	}
	strong := lesson("Strong", cond("diff.customer_replied", OpExists), scalarCond("stage", "negotiation"))
	rs, _ = retrieve(t, []Knowledge{strong}, caseSituation("Negotiation", "y", "", "customer_replied"))
	if !rs[0].Entry.Applies {
		t.Fatalf("two strong facts (weight 5) are enough: %+v", rs[0].Entry)
	}
}

func TestOneHeavyAndOneMediumFeatureReachTheMinimumWeight(t *testing.T) {
	l := lesson("Heavy", cond("diff.customer_replied", OpExists), scalarCond("stage", "negotiation"), scalarCond("health", "at_risk"))
	s := caseSituation("Negotiation", "x", "", "customer_replied")
	s.Fields["health"] = known("at_risk")
	rs, rv := retrieve(t, []Knowledge{l}, s) // signals 3 + stage 2 + health 1 = 6 >= 5
	if !rs[0].Entry.Applies || rv.Candidates[0].ComparableWeight != 6 {
		t.Fatalf("%+v %+v", rs[0], rv.Candidates[0])
	}
}

// H2: "no open signals" is a known fact: the lesson's trigger does not hold, so signals is comparable and scores 0.
func TestALessonsTriggerSignalsAreComparedEvenWhenTheCaseHasNoOpenSignals(t *testing.T) {
	l := lesson("Triggered", cond("diff.customer_replied", OpExists), scalarCond("stage", "negotiation"),
		scalarCond("motion", "expansion"), scalarCond("relationship_state", "STABLE"))
	_, rv := retrieve(t, []Knowledge{l}, caseSituation("Negotiation", "Expansion", "STABLE"))
	c := rv.Candidates[0]
	if c.Comparable != 4 || c.Score != 0.6667 || len(c.Differs) != 1 || c.Differs[0].ID != FeatSignals || c.Differs[0].Agreement != 0 {
		t.Fatalf("signals must be comparable and score 0 (6/9): %+v", c)
	}
	if got := c.Differs[0].Render(); got != "signals: lesson customer_replied, case " {
		t.Fatalf("render = %q", got)
	}
	// a lesson without trigger signals does not compare them
	plain := lesson("Plain", scalarCond("stage", "negotiation"), scalarCond("motion", "expansion"), scalarCond("relationship_state", "STABLE"))
	_, rv = retrieve(t, []Knowledge{plain}, caseSituation("Negotiation", "Expansion", "STABLE"))
	if rv.Candidates[0].Comparable != 3 {
		t.Fatalf("%+v", rv.Candidates[0])
	}
}

// M1: an offered lesson names the evidence behind the conditions that held, like the exact matcher does.
func TestAnOfferedLessonCarriesTheCurrentEvidenceBehindItsMatchedFeatures(t *testing.T) {
	l := lesson("Near", scalarCond("stage", "negotiation"), cond("diff.customer_replied", OpExists), scalarCond("motion", "expansion"))
	s := caseSituation("Negotiation", "Expansion", "", "customer_replied")
	rs, _ := retrieve(t, []Knowledge{l}, s)
	var ids []string
	for _, r := range rs[0].Entry.CurrentEvidenceRefs {
		ids = append(ids, r.ActivityID)
	}
	if !rs[0].Entry.Applies || strings.Join(ids, ",") != `act-"Negotiation",act-customer_replied,act-"Expansion"` {
		t.Fatalf("applies %v refs %v", rs[0].Entry.Applies, ids)
	}
	far := lesson("Far", scalarCond("stage", "discovery"), scalarCond("motion", "renewal"), scalarCond("relationship_state", "STABLE"))
	rs, _ = retrieve(t, []Knowledge{far}, caseSituation("Negotiation", "Expansion", "STABLE"))
	if len(rs[0].Entry.CurrentEvidenceRefs) != 0 {
		t.Fatalf("a lesson that is not offered cites no current evidence: %+v", rs[0].Entry)
	}
}

// M4: scope conditions outside the eight features are not scored; they are reported, never silently dropped.
func TestScopeConditionsOutsideTheFeaturesAreReported(t *testing.T) {
	l := lesson("Odd", scalarCond("stage", "negotiation"), scalarCond("motion", "expansion"), scalarCond("relationship_state", "STABLE"),
		cond("blockers", OpNotExists), cond("stage", OpNeq, "closed"), cond("diff.customer_replied", OpExists))
	got := IgnoredConditions(l)
	want := []string{`blockers not_exists`, `stage neq "closed"`}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("ignored = %v", got)
	}
	_, rv := retrieve(t, []Knowledge{l}, caseSituation("Negotiation", "Expansion", "STABLE", "customer_replied"))
	if strings.Join(rv.Candidates[0].Ignored, "|") != strings.Join(want, "|") {
		t.Fatalf("candidate = %+v", rv.Candidates[0])
	}
	if got := IgnoredConditions(lesson("Fine", scalarCond("stage", "negotiation"), scalarCond("stage", "unknown"))); len(got) != 0 {
		t.Fatalf("supported fields are not ignored, even with an unknown value: %v", got)
	}
}

// Topic (ADR-0013 amendment 4): what the interpretation is ABOUT, the change dimensions of the diff that triggered the
// case (common.v1.json#changeDimension), a heavy feature compared like signals (coverage of the lesson's topics).
func topicLesson(title string, dims []string, conds ...Condition) Knowledge {
	var cs []Condition
	for _, d := range dims {
		cs = append(cs, cond("topic."+d, OpExists))
	}
	return lesson(title, append(cs, conds...)...)
}

func TestTopicIsAHeavyFeatureComparedAsCoverageOfTheLessonsTopics(t *testing.T) {
	l := topicLesson("Topical", []string{"blockers_risk", "buyer_intent", "next_step_commitment"}, scalarCond("stage", "negotiation"))
	s := caseSituation("Negotiation", "x", "")
	s.Topics = []string{"blockers_risk", "buyer_intent"}
	_, rv := retrieve(t, []Knowledge{l}, s)
	c := rv.Candidates[0]
	// topic 3 x 2/3 + stage 2 over 5: two comparable features of weight 5, enough (owner decision 2026-10-06)
	if c.Comparable != 2 || c.ComparableWeight != 5 || c.Score != 0.8 || c.Decision != DecisionApplicable {
		t.Fatalf("candidate = %+v", c)
	}
	if got := c.Matched[0].Render(); c.Matched[0].ID != FeatTopic || got != "topic: lesson blockers_risk, buyer_intent, next_step_commitment, case blockers_risk, buyer_intent" {
		t.Fatalf("matched = %v", c.Matched)
	}
	l2 := topicLesson("Topical3", []string{"buyer_intent"}, scalarCond("stage", "negotiation"), scalarCond("health", "at_risk"))
	s.Fields["health"] = known("at_risk")
	rs, rv := retrieve(t, []Knowledge{l2}, s) // topic 3 + stage 2 + health 1: weight 6, all agree
	if !rs[0].Entry.Applies || rv.Candidates[0].Score != 1 {
		t.Fatalf("%+v", rv.Candidates[0])
	}
}

func TestTopicUnknownOnEitherSideContributesNothing(t *testing.T) {
	l := topicLesson("Topical", []string{"buyer_intent"}, scalarCond("stage", "negotiation"), scalarCond("motion", "expansion"), scalarCond("relationship_state", "STABLE"))
	_, rv := retrieve(t, []Knowledge{l}, caseSituation("Negotiation", "Expansion", "STABLE")) // the case knows no topic
	if rv.Candidates[0].Comparable != 3 {
		t.Fatalf("a case without a diff has no topic: %+v", rv.Candidates[0])
	}
	plain := lesson("Plain", scalarCond("stage", "negotiation"), scalarCond("motion", "expansion"), scalarCond("relationship_state", "STABLE"))
	s := caseSituation("Negotiation", "Expansion", "STABLE")
	s.Topics = []string{"buyer_intent"}
	_, rv = retrieve(t, []Knowledge{plain}, s) // the lesson knew no topic
	if rv.Candidates[0].Comparable != 3 {
		t.Fatalf("a lesson without a topic does not compare it: %+v", rv.Candidates[0])
	}
}

func TestTopicConditionsAreValidatedAndEvaluated(t *testing.T) {
	s := situation(map[string]Value{})
	s.Topics = []string{"buyer_intent"}
	for field, want := range map[string]bool{"topic.buyer_intent": true, "topic.blockers_risk": false} {
		ok, _, err := newEnv(s).holds(cond(field, OpExists))
		if err != nil || ok != want {
			t.Errorf("%s: ok=%v err=%v", field, ok, err)
		}
	}
	for name, c := range map[string]Condition{"unknown dimension": cond("topic.mood", OpExists), "eq is not supported": cond("topic.buyer_intent", OpEq, "x")} {
		if _, _, err := newEnv(s).holds(c); !errors.Is(err, ErrInvalidCondition) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// A lesson that is not offered keeps exactly the entry text it had before topic existed, so the guidance (and the prompt
// keyed on it) of a case whose lesson is not offered does not change; an offered lesson names the topic.
func TestTopicIsLeftOutOfTheEntryOfALessonThatIsNotOffered(t *testing.T) {
	l := topicLesson("Topical", []string{"buyer_intent", "blockers_risk", "next_step_commitment"}, scalarCond("stage", "discovery"))
	s := caseSituation("Negotiation", "x", "")
	s.Topics = []string{"buyer_intent"} // topic 1/3 x 3, stage differs: 1/5, below the threshold
	rs, rv := retrieve(t, []Knowledge{l}, s)
	if rv.Candidates[0].Decision != DecisionBelowThreshold || rs[0].Entry.Applies {
		t.Fatalf("%+v", rv.Candidates[0])
	}
	if got := strings.Join(rs[0].Entry.MatchedConditions, "|"); got != "" || strings.Join(rs[0].Entry.UnmatchedConditions, "|") != "stage: lesson discovery, case negotiation" {
		t.Fatalf("matched = %q unmatched = %v", got, rs[0].Entry.UnmatchedConditions)
	}
	if len(rv.Candidates[0].Differs) != 2 || rv.Candidates[0].Differs[0].ID != FeatTopic {
		t.Fatalf("the persisted record still shows the topic: %+v", rv.Candidates[0].Matched)
	}
	l2 := topicLesson("Topical2", []string{"buyer_intent"}, scalarCond("stage", "negotiation"), scalarCond("health", "at_risk"))
	s.Fields["health"] = known("at_risk")
	rs, _ = retrieve(t, []Knowledge{l2}, s)
	if !rs[0].Entry.Applies || !strings.Contains(strings.Join(rs[0].Entry.Similarity.Matched, "|"), "topic: buyer_intent") {
		t.Fatalf("an offered lesson names the topic: %+v", rs[0].Entry)
	}
}

// A scope condition the matcher cannot evaluate against the case is an error, not a quietly missing evidence ref.
func TestAnOfferedLessonWhoseScopeConditionCannotBeEvaluatedIsAnError(t *testing.T) {
	sim := contractSimilarity(t)
	kn := lesson("L", scalarCond("stage", "negotiation"), scalarCond("motion", "expansion"),
		scalarCond("relationship_state", "STABLE"), scalarCond("transition.status", "CANDIDATE"), scalarCond("health", "good"))
	s := caseSituation("Negotiation", "Expansion", "STABLE")
	s.Fields["health"] = items(Item{Text: "a list where the lesson names a scalar"})
	if _, err := newEnv(s).matchedEvidence(kn); !errors.Is(err, ErrInvalidSituation) {
		t.Fatalf("matchedEvidence err = %v, want ErrInvalidSituation", err)
	}
	if _, _, err := RetrieveAll([]Knowledge{kn}, s, &sim); !errors.Is(err, ErrInvalidSituation) {
		t.Fatalf("RetrieveAll must surface the matcher error, got %v", err)
	}
	s.Fields["health"] = known("good")
	rs, _, err := RetrieveAll([]Knowledge{kn}, s, &sim)
	if err != nil || len(rs) != 1 || len(rs[0].Entry.CurrentEvidenceRefs) == 0 {
		t.Fatalf("an evaluable lesson still carries its evidence: %+v %v", rs, err)
	}
}
