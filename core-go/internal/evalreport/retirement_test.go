package evalreport_test

// P1: the version's in-force period ends at its audited retirement (eval_promotions), read from the
// database by the loader.

import (
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/evalreport"
)

func TestRepeatCorrectionInForcePeriodEndsAtAuditedRetirement(t *testing.T) {
	promoted := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	retired := time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC)
	spec := `{"literal_changes":[{"kind":"attachment_added","after":"x.pdf"}]}`
	insertVersion(t, "crm_writeback", 6, "retired", "human_delta", "manual", spec, &promoted, "")
	if _, err := env.DB.Exec(`INSERT INTO eval_promotions
 (evaluator, version, from_status, to_status, decided_by, reason, created_at)
 VALUES ('crm_writeback', 6, 'active', 'retired', 'test', 'superseded in test', $1)`, retired); err != nil {
		t.Fatalf("insert retirement: %v", err)
	}
	r := reportOf(t, generate(t, evalreport.Options{Evaluator: "crm_writeback"}), "crm_writeback:v6")
	rc := r.Metrics.RepeatCorrection
	if rc.Status != "ok" || rc.Value == nil || rc.Value.InForceUntil == nil || !rc.Value.InForceUntil.Equal(retired) ||
		rc.Value.Scope != "global" || rc.Value.Method != "spec_replay" {
		t.Fatalf("repeat_correction = %+v / %+v", rc, rc.Value)
	}
}
