package strategystore

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/evals/deterministic"
)

func TestRefusalDetailNamesEachBlockingEvalWithItsReason(t *testing.T) {
	blocked := []deterministic.EvalResult{
		{EvalType: "quote_provenance", Reason: "quote not in the activity"},
		{EvalType: "recipient_correctness"},
	}
	got := refusalDetail(blocked)
	if !strings.Contains(got, "quote_provenance: quote not in the activity") || !strings.Contains(got, "recipient_correctness") {
		t.Fatalf("detail = %q", got)
	}
}

func TestLogRefusedSendWritesTheBlockingEvalsAtWarn(t *testing.T) {
	var buf bytes.Buffer
	s := &Service{log: slog.New(slog.NewTextHandler(&buf, nil))}
	s.logRefusedSend(context.Background(), "run-1", []deterministic.EvalResult{{EvalType: "quote_provenance", Reason: "quote not in the activity"}})
	out := buf.String()
	for _, want := range []string{"level=WARN", "send refused", "run_id=run-1", "quote_provenance"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log %q lacks %q", out, want)
		}
	}
}
