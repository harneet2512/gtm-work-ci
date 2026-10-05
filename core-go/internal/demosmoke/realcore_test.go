package demosmoke_test

import (
	"context"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/demosmoke"
)

// TestTheRealCoreFlowPassesEveryCheck drives the whole Slack flow against the real core API on embedded
// Postgres (HAR-139): send-time re-evaluation, the HumanDelta write (asserted inside RunRealCore), the
// append-only verdict history and the request log all come from the real store. It is the slow path —
// embedded Postgres plus the sample ingest — and shares the verifier with the fixture run.
func TestTheRealCoreFlowPassesEveryCheck(t *testing.T) {
	dir := t.TempDir()
	if err := demosmoke.RunRealCore(context.Background(), dir); err != nil {
		t.Fatalf("real-core run: %v", err)
	}
	checks, err := demosmoke.Verify(demosmoke.VerifyOptions{Dir: dir, Getenv: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	if bad := failedNames(checks); len(bad) != 0 {
		t.Fatalf("failed checks: %v", bad)
	}
}
