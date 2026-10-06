package strategystore_test

import (
	"context"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// meteredLabeler is a delta labeler that, like the real worker client, reports what its call cost to the context's sink
// while the send transaction is open.
type meteredLabeler struct{ stubLabeler }

func (m *meteredLabeler) LabelDelta(ctx context.Context, req workerclient.LabelDeltaRequest) (workerclient.LabelDeltaResponse, error) {
	workerclient.ReportUsage(ctx, workerclient.UsageRecord{Stage: "human_delta", WallMs: 12, Usage: workerclient.Usage{
		ModelCalls: 1, InputTokens: 800, OutputTokens: 60, ModelMs: 9, Models: []string{"stub-labeler-v1"}, UsageSource: "live"}})
	return m.stubLabeler.LabelDelta(ctx, req)
}

func TestTheDeltaLabelersSpendIsStoredAfterTheSendTransactionUnderTheAwaitHumanStep(t *testing.T) {
	criterion := workerclient.DeltaCriterion{Statement: "The human drops the CTA when the buyer asks for time.", SuggestedEvalType: "cta_calibration"}
	labeler := &meteredLabeler{stubLabeler{resp: workerclient.LabelDeltaResponse{
		SemanticLabels: []string{"reduced_pressure"}, CandidateCriterion: &criterion, Model: "stub-labeler-v1"}}}
	f := newFixtureWith(t, strategystore.WithLabeler(labeler))

	editAndSend(t, f) // would hang here if the usage insert waited on the send transaction's row locks

	got := scalar(t, `SELECT stage || '|' || run_step || '|' || model_calls || '|' || input_tokens || '|' || wall_ms
 FROM run_model_usage WHERE agent_run_id = $1::uuid`, f.seed.RunID)
	if got != "human_delta|await_human|1|800|12" {
		t.Fatalf("stored usage = %s", got)
	}
}

func TestALabelerThatFailsAfterReportingStillHasItsSpendStored(t *testing.T) {
	labeler := &meteredLabeler{stubLabeler{err: context.DeadlineExceeded}}
	f := newFixtureWith(t, strategystore.WithLabeler(labeler))
	editAndSend(t, f) // the labeler fails, core falls back to the unlabeled delta, and the send still succeeds
	if n := scalar(t, `SELECT count(*)::text FROM run_model_usage WHERE agent_run_id = $1::uuid AND stage = 'human_delta'`, f.seed.RunID); n != "1" {
		t.Fatalf("a call that reported usage before failing is still recorded, got %s rows", n)
	}
}

func TestASendWithoutAReportingWorkerStoresNoUsage(t *testing.T) {
	f := newFixture(t)
	if _, _, err := f.choose(f.seed.Candidates[0], nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.send("send"); err != nil {
		t.Fatal(err)
	}
	if n := scalar(t, `SELECT count(*)::text FROM run_model_usage WHERE agent_run_id = $1::uuid`, f.seed.RunID); n != "0" {
		t.Fatalf("stored %s usage rows without a reporting worker", n)
	}
}
