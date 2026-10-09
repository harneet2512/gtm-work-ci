package slacksurface

import (
	"embed"
	"encoding/json"
	"fmt"
)

// The fixture episode is built from copies of the contract examples (contracts/examples/*.example.json,
// kept identical by a test) so previews, golden files and the fake core all use contract-valid
// objects. The people and the account name are synthetic placeholders (see NOTICE).
//
//go:embed testdata/contracts/*.json
var contractExamples embed.FS

// Fixed identifiers of the fixture episode.
const (
	FixtureRunID     = "0f0a0000-0000-4000-8000-000000000601"
	FixtureEpisodeID = "0e9e0000-0000-4000-8000-000000000a01"
	FixtureAccountID = "0a0c0000-0000-4000-8000-000000000001"

	fixtureCandA = "0ca00000-0000-4000-8000-0000000000a1" // ranked 1, Ghost's preference
	fixtureCandB = "0ca00000-0000-4000-8000-0000000000a2" // ranked 2, the human's choice
	fixtureCandC = "0ca00000-0000-4000-8000-0000000000a3"

	fixtureMarco = "0b0e0000-0000-4000-8000-000000000018"
	fixturePriya = "0b0e0000-0000-4000-8000-000000000017"
)

// Fixture is a complete synthetic demo episode used by --dry-run, golden tests and the fake core.
type Fixture struct {
	AccountName string
	BI          BusinessIntelligenceUpdate
	Strategies  RunStrategies
	Directory   Directory
	// Chosen, Edited and Sent are the HumanStrategyDecision at the three stages of Message 2.
	Chosen, Edited, Sent HumanStrategyDecision
	// Inference is Message 3 before the human answered; Confirmed, Corrected and NoLearning after.
	Inference, Confirmed, Corrected, NoLearning JudgmentInference
}

// FixtureDocs returns the contract documents of the fixture keyed by schema name, for validation.
// Bundles are keyed "eval_bundle:<candidate id>".
func FixtureDocs() (map[string][]byte, error) {
	docs := map[string][]byte{}
	for _, name := range []string{"business_intelligence_update", "strategy_set", "human_strategy_decision", "judgment_inference"} {
		b, err := contractExamples.ReadFile("testdata/contracts/" + name + ".example.json")
		if err != nil {
			return nil, err
		}
		docs[name] = b
	}
	bundles, err := fixtureBundles()
	if err != nil {
		return nil, err
	}
	for id, m := range bundles {
		b, err := json.Marshal(m)
		if err != nil {
			return nil, err
		}
		docs["eval_bundle:"+id] = b
	}
	for name, edit := range map[string]func(map[string]any){
		"human_strategy_decision:chosen": chosenEdit,
		"judgment_inference:pending":     pendingEdit,
		"judgment_inference:confirmed":   confirmedEdit,
		"judgment_inference:no_learning": noLearningEdit,
	} {
		base := "human_strategy_decision"
		if name[:3] == "jud" {
			base = "judgment_inference"
		}
		m, err := loadExample(base)
		if err != nil {
			return nil, err
		}
		edit(m)
		b, _ := json.Marshal(m)
		docs[name] = b
	}
	return docs, nil
}

func loadExample(name string) (map[string]any, error) {
	b, err := contractExamples.ReadFile("testdata/contracts/" + name + ".example.json")
	if err != nil {
		return nil, err
	}
	var m map[string]any
	return m, json.Unmarshal(b, &m)
}

func decode[T any](m any) (T, error) {
	var v T
	b, err := json.Marshal(m)
	if err != nil {
		return v, err
	}
	return v, json.Unmarshal(b, &v)
}

// fixtureBundles returns an eval bundle per candidate: the contract example for candidate B and
// variations of it for A (a CTA calibration failure and a champion warning) and C (all pass).
func fixtureBundles() (map[string]map[string]any, error) {
	out := map[string]map[string]any{}
	for cand, spec := range map[string]struct {
		id    string
		draft float64
		a, b  string // verdicts of items 0 and 1
	}{
		fixtureCandA: {"0eb00000-0000-4000-8000-0000000000b1", 1, "warn", "fail"},
		fixtureCandB: {"0eb00000-0000-4000-8000-0000000000b2", 2, "pass", "warn"},
		fixtureCandC: {"0eb00000-0000-4000-8000-0000000000b3", 3, "pass", "pass"},
	} {
		m, err := loadExample("eval_bundle")
		if err != nil {
			return nil, err
		}
		m["id"], m["draft_index"], m["strategy_candidate_id"] = spec.id, spec.draft, cand
		items := m["items"].([]any)
		for i, v := range []string{spec.a, spec.b} {
			it := items[i].(map[string]any)
			it["verdict"] = v
			r := it["result"].(map[string]any)
			r["verdict"], r["draft_index"] = v, spec.draft
		}
		out[cand] = m
	}
	return out, nil
}

func chosenEdit(m map[string]any) {
	m["surface"] = SurfaceSlack
	m["actor_label"] = "alex"
	m["edits"] = []any{}
	m["send_decision"] = SendPending
	m["send_decided_at"], m["human_decision_id"] = nil, nil
	m["final_to"], m["final_cc"], m["final_artifact"] = nil, nil, nil
}

func pendingEdit(m map[string]any) {
	m["human_verdict"] = VerdictPending
	for _, k := range []string{"corrected_statement", "human_note", "verdict_surface", "verdict_actor_label", "verdict_at"} {
		m[k] = nil
	}
}

func confirmedEdit(m map[string]any) {
	m["human_verdict"] = VerdictConfirmed
	m["corrected_statement"] = nil
	m["verdict_surface"] = SurfaceSlack
}

func noLearningEdit(m map[string]any) {
	m["human_verdict"] = VerdictNoLearning
	m["corrected_statement"] = nil
	m["verdict_surface"] = SurfaceSlack
}

// NewFixture builds the synthetic episode, or panics: the embedded examples are tested to be valid.
func NewFixture() Fixture {
	f, err := buildFixture()
	if err != nil {
		panic(fmt.Sprintf("slacksurface: fixture: %v", err))
	}
	return f
}

func buildFixture() (Fixture, error) {
	var f Fixture
	biM, err := loadExample("business_intelligence_update")
	if err != nil {
		return f, err
	}
	if f.BI, err = decode[BusinessIntelligenceUpdate](biM); err != nil {
		return f, err
	}
	setM, err := loadExample("strategy_set")
	if err != nil {
		return f, err
	}
	set, err := decode[StrategySet](setM)
	if err != nil {
		return f, err
	}
	bundles, err := fixtureBundles()
	if err != nil {
		return f, err
	}
	rs := RunStrategies{StrategySet: set}
	for _, c := range set.Candidates {
		b, err := decode[EvalBundle](bundles[c.CandidateID])
		if err != nil {
			return f, err
		}
		rs.EvalBundles = append(rs.EvalBundles, b)
	}
	f.Strategies = rs
	f.AccountName = "Acme (synthetic fixture)"
	f.Directory = Directory{
		fixtureMarco: {ID: fixtureMarco, Name: "Marco", Email: "marco@acme.example.test"},
		fixturePriya: {ID: fixturePriya, Name: "Priya", Email: "priya@acme.example.test"},
	}
	decM, err := loadExample("human_strategy_decision")
	if err != nil {
		return f, err
	}
	if f.Sent, err = decode[HumanStrategyDecision](decM); err != nil {
		return f, err
	}
	f.Sent.Surface, f.Sent.ActorLabel = SurfaceSlack, "alex"
	chosenM, _ := loadExample("human_strategy_decision")
	chosenEdit(chosenM)
	if f.Chosen, err = decode[HumanStrategyDecision](chosenM); err != nil {
		return f, err
	}
	// Edited: the example's edits and final artifact, still pending.
	f.Edited = f.Sent
	f.Edited.SendDecision, f.Edited.SendDecidedAt, f.Edited.HumanDecisionID = SendPending, nil, nil
	return f, f.buildInferences()
}

func (f *Fixture) buildInferences() error {
	for _, v := range []struct {
		dst  *JudgmentInference
		edit func(map[string]any)
	}{{&f.Inference, pendingEdit}, {&f.Confirmed, confirmedEdit}, {&f.Corrected, func(map[string]any) {}}, {&f.NoLearning, noLearningEdit}} {
		m, err := loadExample("judgment_inference")
		if err != nil {
			return err
		}
		v.edit(m)
		if *v.dst, err = decode[JudgmentInference](m); err != nil {
			return err
		}
	}
	return nil
}

// FixtureAccountState is the contract account state example (GET /accounts/{id}/state) addressed to the fixture's account:
// the Slack surface reads its buying group to name the people a graph node does not carry.
func FixtureAccountState() (map[string]any, error) {
	m, err := loadExample("account_state")
	if err != nil {
		return nil, err
	}
	m["account_id"] = FixtureAccountID
	return m, nil
}
