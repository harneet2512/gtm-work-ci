package codespace

import (
	"context"
	"strings"
	"testing"
)

func TestStatusReportsAFailedStartUpInsteadOfStartingForever(t *testing.T) {
	r := newRig(t)
	r.seeded()
	_ = WriteMarker(r.ops.Paths.ActiveFile(), SlotCase1)
	src := newSource(r, "worker")
	src.BootError = func() string { return "worker failed to health: did not answer" }
	st := src.Compute(context.Background())
	if st.Ready || st.Phase != PhaseAttention || !strings.Contains(st.Message, "worker failed to health") {
		t.Fatalf("status = %+v", st)
	}
	src.BootError = func() string { return "" }
	if st := src.Compute(context.Background()); st.Phase != PhaseStarting {
		t.Fatalf("no recorded failure = still starting: %+v", st)
	}
	// A stale recorded failure never hides a healthy system.
	healthy := newSource(r)
	healthy.BootError = func() string { return "old failure" }
	if st := healthy.Compute(context.Background()); !st.Ready {
		t.Fatalf("status = %+v", st)
	}
}
