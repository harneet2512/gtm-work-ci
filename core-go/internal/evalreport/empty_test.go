package evalreport_test

// The empty-world contract of HAR-121: a database with no supervision yet (and then one bare
// registered version) produces a valid report document where every metric is "n/a" with a reason —
// never an error, never a zero table pretending to be measured. This test must run before the world
// test seeds episodes (Go runs *_test.go files in lexical order: empty < harness < report).

import (
	"context"
	"errors"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/evalreport"
)

func TestEmptyWorld(t *testing.T) {
	// No versions, no eval runs, no episodes: --all is a valid empty document.
	set := generate(t, evalreport.Options{})
	if len(set.Reports) != 0 {
		t.Fatalf("empty world produced reports: %v", keysOf(set))
	}

	// One registered-but-never-run version: the report exists and every metric is n/a with a reason.
	insertVersion(t, "cta_calibration", 9, "candidate", "semantic", "manual", "", nil, "")

	set = generate(t, evalreport.Options{})
	rep := reportOf(t, set, "cta_calibration:v9")
	if !rep.Registered || rep.Status != "candidate" || rep.Tag != "cta_calibration:v9" {
		t.Fatalf("report header = %+v", rep)
	}
	type named struct {
		name, status, reason string
		hasValue             bool
	}
	m := rep.Metrics
	for _, mt := range []named{
		{"human_agreement", m.HumanAgreement.Status, m.HumanAgreement.Reason, m.HumanAgreement.Value != nil},
		{"false_pass_rate", m.FalsePass.Status, m.FalsePass.Reason, m.FalsePass.Value != nil},
		{"false_block_rate", m.FalseBlock.Status, m.FalseBlock.Reason, m.FalseBlock.Value != nil},
		{"confidence_calibration", m.Calibration.Status, m.Calibration.Reason, m.Calibration.Value != nil},
		{"repeat_consistency", m.Consistency.Status, m.Consistency.Reason, m.Consistency.Value != nil},
		{"correction_category_coverage", m.Coverage.Status, m.Coverage.Reason, m.Coverage.Value != nil},
		{"edits_explained_by_evals", m.Explained.Status, m.Explained.Reason, m.Explained.Value != nil},
		{"new_criterion_discovery", m.Discovery.Status, m.Discovery.Reason, m.Discovery.Value != nil},
		{"repeat_semantic_correction", m.RepeatCorrection.Status, m.RepeatCorrection.Reason, m.RepeatCorrection.Value != nil},
		{"human_vs_inference_agreement", m.InferenceAgree.Status, m.InferenceAgree.Reason, m.InferenceAgree.Value != nil},
	} {
		if mt.status != "n/a" || mt.reason == "" || mt.hasValue {
			t.Fatalf("metric %s = status %q reason %q value %v — want n/a with a reason and no value",
				mt.name, mt.status, mt.reason, mt.hasValue)
		}
	}

	// Selection: --evaluator alone reports every version of the axis; a version filter that misses
	// yields an empty document.
	set = generate(t, evalreport.Options{Evaluator: "cta_calibration"})
	if len(set.Reports) != 1 || set.Reports["cta_calibration:v9"] == nil {
		t.Fatalf("--evaluator selection = %v", keysOf(set))
	}
	// S4: a version filter that misses is an error, never an empty document a CI gate would read as a pass.
	if _, err := evalreport.Generate(context.Background(), env.DB,
		evalreport.Options{Evaluator: "cta_calibration", Version: 1}); !errors.Is(err, evalreport.ErrUnknownSelection) {
		t.Fatalf("an unobserved version: err = %v, want ErrUnknownSelection", err)
	}
	if _, err := evalreport.Generate(context.Background(), env.DB,
		evalreport.Options{Evaluator: "grounding"}); !errors.Is(err, evalreport.ErrUnknownSelection) {
		t.Fatalf("an axis with no version: err = %v, want ErrUnknownSelection", err)
	}
}
