package demorun

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

func postSurfaceMessage(t *testing.T, db *sql.DB, subject, kind, ts string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO surface_messages (subject_id, surface, kind, channel, ts) VALUES ($1::uuid, 'slack', $2, 'C0TEST', $3)`, subject, kind, ts); err != nil {
		t.Fatalf("post surface message %s: %v", kind, err)
	}
}

// TestFetchFactsAndEvaluateOnARealWalkthrough drives the real strategy service through the walkthrough the user
// performs in Slack (choose, edit, send, a corrected verdict, a note) on a real migrated database, and checks that
// FetchFacts reads the authoritative rows and Evaluate passes every step. It is also the proof that the verify
// queries match the schema the product writes.
func TestFetchFactsAndEvaluateOnARealWalkthrough(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a Postgres and loads the sample world")
	}
	ctx := context.Background()
	env, err := storetest.Start(ctx)
	if err != nil {
		t.Fatalf("start db: %v", err)
	}
	defer env.Close()
	world := ctxfixture.Get(t, env.DB)
	seed := strategytest.Seed(t, env.DB, world.AccountA)
	svc, err := strategystore.New(env.DB, nil)
	if err != nil {
		t.Fatal(err)
	}
	st := DemoState{ManifestID: "m-1", AccountID: seed.AccountID, PlayedAt: time.Now(), InvisibilityAtSeed: "withheld"}
	opts := FetchOptions{SlackOn: true}

	// Published, nothing decided yet: the run, set and episode are found, the human steps are not.
	f, err := FetchFacts(ctx, env.DB, st, opts)
	if err != nil {
		t.Fatal(err)
	}
	if f.StrategySetID != seed.SetID || f.RunID != seed.RunID || f.EpisodeID != seed.EpisodeID || f.BIUpdateID != seed.BIID {
		t.Fatalf("ids not read from the database: %+v", f)
	}
	if f.Decision != nil || f.Delta != nil || f.Inference != nil {
		t.Fatalf("nothing is decided yet: %+v", f)
	}
	postSurfaceMessage(t, env.DB, seed.BIID, "bi", "1700000000.000100")
	postSurfaceMessage(t, env.DB, seed.EpisodeID, "chooser", "1700000001.000200")

	// Choose candidate 2, edit it, send it.
	sendReq := strategystore.DecisionRequest{SelectedCandidateID: seed.Candidates[1], Surface: "slack", ActorLabel: "alex"}
	if _, _, err := svc.RecordDecision(ctx, seed.RunID, sendReq); err != nil {
		t.Fatal(err)
	}
	subject := "Changed at send time"
	edited := strategystore.Artifact{Channel: "email", Subject: &subject, Body: "Hi Marco,\n\nLet us know what timing suits.\n\nBest,\nDana"}
	sendReq.FinalArtifact = &edited
	if _, _, err := svc.RecordDecision(ctx, seed.RunID, sendReq); err != nil {
		t.Fatal(err)
	}
	f, err = FetchFacts(ctx, env.DB, st, opts)
	if err != nil {
		t.Fatal(err)
	}
	if f.Decision == nil || f.Decision.SendDecision != "pending" || f.Decision.SelectedCandidateID != seed.Candidates[1] || f.Decision.Edits == 0 {
		t.Fatalf("a chosen, edited, unsent decision: %+v", f.Decision)
	}
	if step := stepByID(Evaluate(withNow(f)), "V07"); step.Status != StatusFail {
		t.Fatalf("an unsent choice must FAIL V07: %+v", step)
	}
	if _, err := svc.Send(ctx, seed.RunID, strategystore.SendRequest{Decision: "send", Surface: "slack", ActorLabel: "alex"}); err != nil {
		t.Fatal(err)
	}

	// The judgment inference exists after the send; the human corrects it and adds a note.
	strategytest.SeedInference(t, env.DB, seed)
	postSurfaceMessage(t, env.DB, seed.EpisodeID, "judgment", "1700000002.000300")
	if _, err := svc.SubmitVerdict(ctx, seed.EpisodeID, strategystore.VerdictRequest{Verdict: "corrected", CorrectedStatement: "The edit kept Priya on cc because she owns the budget.", Surface: "slack", ActorLabel: "alex"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SubmitVerdict(ctx, seed.EpisodeID, strategystore.VerdictRequest{Note: "Ghost missed the budget owner.", Surface: "slack", ActorLabel: "alex"}); err != nil {
		t.Fatal(err)
	}

	f, err = FetchFacts(ctx, env.DB, st, opts)
	if err != nil {
		t.Fatal(err)
	}
	f.InvisibilityNow = "released"
	if f.Decision == nil || f.Decision.SendDecision != "send" || f.Decision.HumanAction != "APPROVE_WITH_EDIT" || f.Decision.HumanDecision != "edit" {
		t.Fatalf("decision after send: %+v", f.Decision)
	}
	if f.Delta == nil || f.Delta.LiteralChanges == 0 {
		t.Fatalf("an edited send must write a HumanDelta with literal changes: %+v", f.Delta)
	}
	if f.Inference == nil || f.Inference.Verdict != "corrected" || !f.Inference.HasCorrection || f.VerdictRows != 1 || f.NoteRows != 1 {
		t.Fatalf("judgment: %+v verdict rows %d note rows %d", f.Inference, f.VerdictRows, f.NoteRows)
	}
	if f.M1TS == "" || f.M2TS == "" || f.M3TS == "" || f.SlackRowsPosted != 3 {
		t.Fatalf("slack refs: M1=%q M2=%q M3=%q posted=%d", f.M1TS, f.M2TS, f.M3TS, f.SlackRowsPosted)
	}
	for _, s := range Evaluate(f) {
		if s.Status != StatusPass {
			t.Errorf("%s %s = %s (%s)", s.ID, s.Name, s.Status, s.Detail)
		}
	}
	// A database that holds a world is not empty: the seed's guard must refuse it.
	if err := (PGStores{DSN: env.URL}).RequireEmpty(ctx); err == nil || !strings.Contains(err.Error(), "demo reset") {
		t.Fatalf("a populated database must be refused with the recovery, got %v", err)
	}
}

// withNow marks core's invisibility as released so the early steps do not drown the one under test.
func withNow(f Facts) Facts {
	f.InvisibilityNow = "released"
	return f
}
