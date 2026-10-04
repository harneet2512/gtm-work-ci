package strategystore_test

import (
	"errors"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

// sentFixture is an episode whose human chose candidate 2 and sent it, with the inference seeded as the
// worker would after the send.
func sentFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	if _, _, err := f.choose(f.seed.Candidates[1], nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.send("send"); err != nil {
		t.Fatal(err)
	}
	strategytest.SeedInference(t, env.DB, f.seed)
	return f
}

func (f *fixture) verdict(edit func(*strategystore.VerdictRequest)) ([]byte, error) {
	req := strategystore.VerdictRequest{Surface: "slack", ActorLabel: "alex"}
	edit(&req)
	return f.svc.SubmitVerdict(f.ctx, f.seed.EpisodeID, req)
}

func TestInferenceIsServedPendingUntilTheHumanAnswers(t *testing.T) {
	f := sentFixture(t)

	doc, err := f.svc.Inference(f.ctx, f.seed.EpisodeID)

	if err != nil {
		t.Fatal(err)
	}
	valid(t, "judgment_inference", doc)
	m := decode(t, doc)
	if m["human_verdict"] != "pending" || m["agreement"] != "overrode" ||
		m["agent_preference"] != f.seed.Candidates[0] || m["human_choice"] != f.seed.Candidates[1] {
		t.Fatalf("inference = %v", m)
	}
}

func TestConfirmingClosesTheEpisodeAndKeepsEverythingInOneEpisode(t *testing.T) {
	f := sentFixture(t)

	doc, err := f.verdict(func(r *strategystore.VerdictRequest) { r.Verdict = "confirmed" })

	if err != nil {
		t.Fatal(err)
	}
	valid(t, "judgment_inference", doc)
	m := decode(t, doc)
	if m["human_verdict"] != "confirmed" || m["verdict_surface"] != "slack" || m["verdict_actor_label"] != "alex" || m["verdict_at"] == nil {
		t.Fatalf("inference = %v", m)
	}
	if got := scalar(t, `SELECT status FROM decision_episodes WHERE id = $1::uuid`, f.seed.EpisodeID); got != "judged" {
		t.Fatalf("episode status = %s, want judged", got)
	}
	again, err := f.verdict(func(r *strategystore.VerdictRequest) { r.Verdict = "confirmed" })
	if err != nil || decode(t, again)["verdict_at"] != m["verdict_at"] {
		t.Fatalf("confirming twice must be idempotent: %v", err)
	}
}

func TestCorrectionStoresTheHumansStatementAndNote(t *testing.T) {
	f := sentFixture(t)
	knowledgeBefore := scalar(t, `SELECT count(*)::text FROM knowledge`)

	doc, err := f.verdict(func(r *strategystore.VerdictRequest) {
		r.Verdict, r.CorrectedStatement, r.Note = "corrected", "Not timing: the reviewer is new to us.", "only for security-led reviews"
	})

	if err != nil {
		t.Fatal(err)
	}
	valid(t, "judgment_inference", doc)
	m := decode(t, doc)
	if m["human_verdict"] != "corrected" || m["corrected_statement"] != "Not timing: the reviewer is new to us." ||
		m["human_note"] != "only for security-led reviews" {
		t.Fatalf("inference = %v", m)
	}
	if after := scalar(t, `SELECT count(*)::text FROM knowledge`); after != knowledgeBefore {
		t.Fatalf("a correction changed company knowledge (%s -> %s); it must only feed a candidate criterion", knowledgeBefore, after)
	}
	if _, err := f.verdict(func(r *strategystore.VerdictRequest) { r.Verdict = "confirmed" }); err == nil {
		t.Fatal("a correction was withdrawn by a confirmation")
	} else {
		asRefused(t, err, "verdict_locked")
	}
}

func TestNoteOnlyLeavesTheVerdictPendingAndTheEpisodeOpen(t *testing.T) {
	f := sentFixture(t)

	doc, err := f.verdict(func(r *strategystore.VerdictRequest) { r.Note = "context for later" })

	if err != nil {
		t.Fatal(err)
	}
	m := decode(t, doc)
	if m["human_verdict"] != "pending" || m["human_note"] != "context for later" {
		t.Fatalf("inference = %v", m)
	}
	if got := scalar(t, `SELECT status FROM decision_episodes WHERE id = $1::uuid`, f.seed.EpisodeID); got != "decided" {
		t.Fatalf("a note must not close the episode; status = %s", got)
	}
	// confirmed can later become corrected, and the episode stays judged
	if _, err := f.verdict(func(r *strategystore.VerdictRequest) { r.Verdict = "confirmed" }); err != nil {
		t.Fatal(err)
	}
	doc, err = f.verdict(func(r *strategystore.VerdictRequest) {
		r.Verdict, r.CorrectedStatement = "corrected", "Actually, a different reason."
	})
	if err != nil || decode(t, doc)["human_verdict"] != "corrected" {
		t.Fatalf("confirmed to corrected: %v %s", err, doc)
	}
}

func TestVerdictBeforeTheInferenceExistsIsNotReady(t *testing.T) {
	f := newFixture(t)

	_, err := f.verdict(func(r *strategystore.VerdictRequest) { r.Verdict = "confirmed" })

	if !errors.Is(err, strategystore.ErrNotReady) {
		t.Fatalf("error = %v, want not ready", err)
	}
	_, err = f.svc.SubmitVerdict(f.ctx, strategytest.NewID(), strategystore.VerdictRequest{Verdict: "confirmed", Surface: "slack", ActorLabel: "a"})
	if !errors.Is(err, strategystore.ErrNotFound) {
		t.Fatalf("unknown episode: %v", err)
	}
	_, err = f.verdict(func(r *strategystore.VerdictRequest) { r.Verdict = "corrected" })
	asRefused(t, err, "invalid_request")
}
