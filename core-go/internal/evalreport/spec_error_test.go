package evalreport_test

// S3: a malformed shadow_spec must not kill the report — the version records the spec error and its
// spec-replay / repeat-correction metrics report n/a with the reason, while every other version (and
// every spec-independent metric of this one) still reports.

import (
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/evalreport"
)

func TestMalformedShadowSpecDegradesToPerVersionNA(t *testing.T) {
	promoted := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	// literal_changes is an object-typed column value jsonb accepts but Spec cannot decode.
	insertVersion(t, "timing_cadence", 7, "active", "human_delta", "manual", `{"literal_changes":"nope"}`, &promoted, "")

	set := generate(t, evalreport.Options{}) // --all must survive the bad row
	bad := reportOf(t, set, "timing_cadence:v7")
	if bad.SpecError == "" {
		t.Fatalf("spec_error not recorded: %+v", bad)
	}
	rc := bad.Metrics.RepeatCorrection
	if rc.Status != "n/a" || !strings.Contains(rc.Reason, "shadow_spec") {
		t.Fatalf("repeat_correction = %+v, want n/a naming the malformed shadow_spec", rc)
	}
	con := bad.Metrics.Consistency
	if con.Status != "n/a" || !strings.Contains(con.Reason, "shadow_spec") {
		t.Fatalf("consistency = %+v, want n/a naming the malformed shadow_spec", con)
	}
	// Neighbours are unaffected.
	if good := reportOf(t, set, "evidence_sufficiency:v1"); good.SpecError != "" ||
		good.Metrics.Consistency.Value == nil || good.Metrics.Consistency.Value.Replay == nil {
		t.Fatalf("a healthy version lost its replay: %+v", good.Metrics.Consistency)
	}
	// Corpus-level metrics of the bad version still report.
	if bad.Metrics.Explained.Status != "ok" {
		t.Fatalf("explained = %+v", bad.Metrics.Explained)
	}
}
