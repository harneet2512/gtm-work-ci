package evalreport_test

// The human-vs-inference agreement reads judgment_inferences and the append-only judgment_verdicts
// history. This file sorts after report_test.go on purpose: its episodes must not enter the world the
// hand-computed TestReportWorld expectations were derived on.

import (
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/evalreport"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

func TestHumanVsInferenceAgreementReadsVerdictHistory(t *testing.T) {
	// X1: the human picks Ghost's preferred candidate and confirms its inference through the real
	// verdict path (judgment_verdicts row + judgment_inferences update).
	x1 := newEpisode(t)
	x1.choose(x1.seed.Candidates[0], nil)
	x1.send("send")
	strategytest.SeedInference(t, env.DB, x1.seed)
	if _, err := x1.svc.SubmitVerdict(x1.ctx, x1.seed.EpisodeID,
		strategystore.VerdictRequest{Verdict: "confirmed", Surface: "slack", ActorLabel: "alex"}); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	insertEval(t, x1.seed.RunID, 1, "grounding", "grounding:v1", "semantic", "pass", nil, t0)

	// X2: the human overrides Ghost, first confirms, then corrects the inference. A correction seeds a
	// learning-loop criterion through the service, so the history rows are written directly here
	// (the append-only table accepts inserts) to keep this test about the read side.
	x2 := newEpisode(t)
	x2.choose(x2.seed.Candidates[1], nil)
	x2.send("send")
	infID := strategytest.SeedInference(t, env.DB, x2.seed)
	for _, step := range []struct{ verdict, statement string }{{"confirmed", ""}, {"corrected", "It was the new reviewer."}} {
		var stmt any
		if step.statement != "" {
			stmt = step.statement
		}
		if _, err := env.DB.Exec(`INSERT INTO judgment_verdicts (judgment_inference_id, verdict, corrected_statement, surface, actor_label)
 VALUES ($1::uuid, $2, $3, 'slack', 'alex')`, infID, step.verdict, stmt); err != nil {
			t.Fatalf("history %s: %v", step.verdict, err)
		}
	}
	if _, err := env.DB.Exec(`UPDATE judgment_inferences SET human_verdict = 'corrected', corrected_statement = 'It was the new reviewer.',
 verdict_surface = 'slack', verdict_actor_label = 'alex', verdict_at = now() WHERE id = $1::uuid`, infID); err != nil {
		t.Fatalf("latest answer: %v", err)
	}
	insertEval(t, x2.seed.RunID, 2, "grounding", "grounding:v1", "semantic", "pass", nil, t0)

	// X3: an inference nobody has answered yet.
	x3 := newEpisode(t)
	x3.choose(x3.seed.Candidates[1], nil)
	x3.send("send")
	strategytest.SeedInference(t, env.DB, x3.seed)

	m := reportOf(t, generate(t, evalreport.Options{}), "grounding:v1").Metrics.InferenceAgree
	a := m.Value
	if m.Status != "ok" || a == nil {
		t.Fatalf("human_vs_inference_agreement = %+v", m)
	}
	if a.Inferences != 3 || a.Answered != 2 || a.Confirmed != 1 || a.Corrected != 1 || a.Pending != 1 ||
		a.ConfirmRate == nil || !closeTo(*a.ConfirmRate, 0.5) {
		t.Fatalf("counts = %+v", a)
	}
	if a.VerdictRows != 3 || a.Revised != 1 {
		t.Fatalf("history: rows %d, revised %d (want 3, 1)", a.VerdictRows, a.Revised)
	}
	// grounding:v1 judged X1 and X2 (final drafts) but not X3.
	if a.ThisVersion.N != 2 || a.ThisVersion.Answered != 2 || a.ThisVersion.Confirmed != 1 {
		t.Fatalf("this version = %+v", a.ThisVersion)
	}
	if ag := a.ByAgentAgreement["agreed"]; ag.N < 1 {
		t.Fatalf("by agreement = %+v", a.ByAgentAgreement)
	}
}
