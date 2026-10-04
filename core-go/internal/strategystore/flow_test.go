package strategystore_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

func TestStrategiesAreServedExactlyAsStored(t *testing.T) {
	f := newFixture(t)

	doc, err := f.svc.Strategies(f.ctx, f.seed.RunID)
	if err != nil {
		t.Fatal(err)
	}

	m := decode(t, doc)
	valid(t, "strategy_set", mustMarshal(t, m["strategy_set"]))
	bundles, _ := m["eval_bundles"].([]any)
	if len(bundles) != 3 {
		t.Fatalf("%d eval bundles, want 3", len(bundles))
	}
	for _, b := range bundles {
		valid(t, "eval_bundle", mustMarshal(t, b))
	}
	cands := m["strategy_set"].(map[string]any)["candidates"].([]any)
	if first := cands[0].(map[string]any); first["candidate_id"] != f.seed.Candidates[0] || first["preferred_by_agent"] != true {
		t.Fatalf("the first candidate is not Ghost's preference: %v", first)
	}
	again, _ := f.svc.Strategies(f.ctx, f.seed.RunID)
	if string(again) != string(doc) {
		t.Fatal("reading the strategies twice gave different answers")
	}
}

func TestStrategiesAndDecisionReadsReportNotFoundAndNotReady(t *testing.T) {
	f := newFixture(t)
	world := f.seed.AccountID
	bare := strategytest.NewID()

	if _, err := f.svc.Strategies(f.ctx, "not-a-uuid"); !errors.Is(err, strategystore.ErrNotFound) {
		t.Fatalf("malformed id: %v", err)
	}
	if _, err := f.svc.Strategies(f.ctx, bare); !errors.Is(err, strategystore.ErrNotFound) {
		t.Fatalf("unknown run: %v", err)
	}
	if _, err := f.svc.Decision(f.ctx, f.seed.RunID); !errors.Is(err, strategystore.ErrNotFound) {
		t.Fatalf("decision before choice: %v", err)
	}
	if _, err := f.svc.Inference(f.ctx, f.seed.EpisodeID); !errors.Is(err, strategystore.ErrNotReady) {
		t.Fatalf("inference before send: %v", err)
	}
	if _, err := f.svc.Inference(f.ctx, bare); !errors.Is(err, strategystore.ErrNotFound) {
		t.Fatalf("inference of unknown episode: %v", err)
	}
	if _, err := f.svc.LatestBusinessIntelligence(f.ctx, bare); !errors.Is(err, strategystore.ErrNotFound) {
		t.Fatalf("BI of unknown account: %v", err)
	}
	doc, err := f.svc.LatestBusinessIntelligence(f.ctx, world)
	if err != nil {
		t.Fatal(err)
	}
	valid(t, "business_intelligence_update", doc)
	if decode(t, doc)["id"] != f.seed.BIID {
		t.Fatalf("latest BI is not the newest update: %s", doc)
	}
}

func TestChoosingRecordsTheDecisionOnceAndNeverSends(t *testing.T) {
	f := newFixture(t)

	doc, created, err := f.choose(f.seed.Candidates[1], nil)
	if err != nil || !created {
		t.Fatalf("choose: created=%v err=%v", created, err)
	}

	valid(t, "human_strategy_decision", doc)
	d := decode(t, doc)
	if d["selected_candidate_id"] != f.seed.Candidates[1] || d["original_agent_preference"] != f.seed.Candidates[0] ||
		d["send_decision"] != "pending" || d["surface"] != "slack" {
		t.Fatalf("decision = %v", d)
	}
	if got := scalar(t, `SELECT status FROM decision_episodes WHERE id = $1::uuid`, f.seed.EpisodeID); got != "chosen" {
		t.Fatalf("episode status = %s, want chosen", got)
	}
	if n := scalar(t, `SELECT count(*)::text FROM human_decisions WHERE agent_run_id = $1::uuid`, f.seed.RunID); n != "0" {
		t.Fatalf("choosing recorded %s human decisions; it must not send", n)
	}

	again, created, err := f.choose(f.seed.Candidates[1], nil)
	if err != nil || created || string(again) != string(doc) {
		t.Fatalf("choosing the same candidate again: created=%v err=%v changed=%v", created, err, string(again) != string(doc))
	}
	_, _, err = f.choose(f.seed.Candidates[2], nil)
	asConflict(t, err, strategystore.CodeSelectionLocked)
	read, err := f.svc.Decision(f.ctx, f.seed.RunID)
	if err != nil || string(read) != string(doc) {
		t.Fatalf("Decision read = %v, %v", string(read), err)
	}
}

func TestChoosingRefusesWhatTheRunDoesNotOwn(t *testing.T) {
	f := newFixture(t)
	stranger := strategytest.NewID()

	_, _, err := f.choose(strategytest.NewID(), nil)
	asRefused(t, err, "unknown_candidate")
	_, _, err = f.choose(f.seed.Candidates[1], func(r *strategystore.DecisionRequest) {
		r.FinalTo = &[]strategystore.Recipient{{PersonID: stranger, Role: "to"}}
	})
	asRefused(t, err, "unknown_recipient")
	_, _, err = f.choose(f.seed.Candidates[1], func(r *strategystore.DecisionRequest) { r.ActorPersonID = &stranger })
	asRefused(t, err, "unknown_actor")
	_, _, err = f.svc.RecordDecision(f.ctx, strategytest.NewID(), strategystore.DecisionRequest{SelectedCandidateID: f.seed.Candidates[1], Surface: "slack", ActorLabel: "a"})
	if !errors.Is(err, strategystore.ErrNotFound) {
		t.Fatalf("unknown run: %v", err)
	}
	if _, err := f.svc.Decision(f.ctx, f.seed.RunID); !errors.Is(err, strategystore.ErrNotFound) {
		t.Fatal("a refused choice left a decision behind")
	}
}

func TestEditingPersistsTheFinalArtifactAndItsDiff(t *testing.T) {
	f := newFixture(t)
	if _, _, err := f.choose(f.seed.Candidates[1], nil); err != nil {
		t.Fatal(err)
	}
	subject := "Re: EU rollout, security review"
	artifact := strategystore.Artifact{Channel: "email", Subject: &subject, Body: "Hi Marco,\n\nAttached are the documents. Tell us when suits.\n\nBest,\nDana"}

	doc, created, err := f.choose(f.seed.Candidates[1], func(r *strategystore.DecisionRequest) {
		r.FinalTo = &[]strategystore.Recipient{{PersonID: f.seed.Marco, Role: "to"}}
		r.FinalCC = &[]strategystore.Recipient{}
		r.FinalArtifact = &artifact
	})

	if err != nil || created {
		t.Fatalf("edit: created=%v err=%v", created, err)
	}
	valid(t, "human_strategy_decision", doc)
	d := decode(t, doc)
	if edits, _ := d["edits"].([]any); len(edits) < 2 {
		t.Fatalf("edits = %v, want the subject, body and cc changes", d["edits"])
	}
	fa := d["final_artifact"].(map[string]any)
	if fa["subject"] != subject || !strings.Contains(fa["body"].(string), "Tell us when suits") {
		t.Fatalf("final_artifact = %v", fa)
	}
	if d["send_decision"] != "pending" {
		t.Fatalf("an edit must not send: %v", d["send_decision"])
	}
	// a body-only edit afterwards keeps the saved recipients and subject
	body := strategystore.Artifact{Channel: "email", Subject: &subject, Body: "Hi Marco,\n\nShorter.\n\nBest,\nDana"}
	doc, _, err = f.choose(f.seed.Candidates[1], func(r *strategystore.DecisionRequest) { r.FinalArtifact = &body })
	if err != nil {
		t.Fatal(err)
	}
	if cc, _ := decode(t, doc)["final_cc"].([]any); len(cc) != 0 {
		t.Fatalf("final_cc = %v, want the saved (empty) cc kept", cc)
	}
}

func TestSendRecordsTheEditedDecisionOnceThroughTheRecordingExecutor(t *testing.T) {
	f := newFixture(t)
	if _, _, err := f.choose(f.seed.Candidates[1], nil); err != nil {
		t.Fatal(err)
	}
	subject := "Edited subject"
	edited := strategystore.Artifact{Channel: "email", Subject: &subject, Body: "Edited body"}
	if _, _, err := f.choose(f.seed.Candidates[1], func(r *strategystore.DecisionRequest) { r.FinalArtifact = &edited }); err != nil {
		t.Fatal(err)
	}

	doc, err := f.send("send")

	if err != nil {
		t.Fatal(err)
	}
	valid(t, "human_strategy_decision", doc)
	d := decode(t, doc)
	if d["send_decision"] != "send" || d["human_decision_id"] == nil || d["send_decided_at"] == nil {
		t.Fatalf("decision = %v", d)
	}
	if got := scalar(t, `SELECT decision FROM human_decisions WHERE agent_run_id = $1::uuid`, f.seed.RunID); got != "edit" {
		t.Fatalf("human decision = %s, want edit", got)
	}
	if got := scalar(t, `SELECT status FROM agent_runs WHERE id = $1::uuid`, f.seed.RunID); got != "recorded" {
		t.Fatalf("run status = %s, want recorded (dry run)", got)
	}
	if got := scalar(t, `SELECT status || ':' || coalesce(external_effect_id, '') FROM agent_run_steps WHERE agent_run_id = $1::uuid AND step = 'execute'`, f.seed.RunID); got != "recorded:" {
		t.Fatalf("execute step = %q, want recorded with no external effect", got)
	}
	if got := scalar(t, `SELECT human_action || ':' || status FROM decision_episodes WHERE id = $1::uuid`, f.seed.EpisodeID); got != "APPROVE_WITH_EDIT:decided" {
		t.Fatalf("episode = %s", got)
	}
	if got := scalar(t, `SELECT human_final_artifact ->> 'body' FROM decision_episodes WHERE id = $1::uuid`, f.seed.EpisodeID); got != "Edited body" {
		t.Fatalf("episode final artifact body = %q", got)
	}
	if got := scalar(t, `SELECT detail -> 'recorded_effect' ->> 'idempotency_key' FROM agent_run_steps WHERE agent_run_id = $1::uuid AND step = 'execute'`, f.seed.RunID); got != f.seed.RunID+":execute" {
		t.Fatalf("recorded effect key = %q", got)
	}
}

func TestSecondSendAndLateChoiceAreAlreadyDecidedAndChangeNothing(t *testing.T) {
	f := newFixture(t)
	if _, _, err := f.choose(f.seed.Candidates[0], nil); err != nil {
		t.Fatal(err)
	}
	first, err := f.send("send")
	if err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, `SELECT decision FROM human_decisions WHERE agent_run_id = $1::uuid`, f.seed.RunID); got != "approve" {
		t.Fatalf("an unedited send must record approve, got %s", got)
	}

	_, err = f.send("send")
	c := asConflict(t, err, strategystore.CodeAlreadyDecided)
	if string(c.Decision) != string(first) {
		t.Fatalf("the conflict does not carry the stored record: %s", c.Decision)
	}
	_, err = f.send("discard")
	asConflict(t, err, strategystore.CodeAlreadyDecided)
	_, _, err = f.choose(f.seed.Candidates[0], func(r *strategystore.DecisionRequest) {
		r.FinalArtifact = &strategystore.Artifact{Channel: "email", Body: "late edit"}
	})
	asConflict(t, err, strategystore.CodeAlreadyDecided)
	if n := scalar(t, `SELECT count(*)::text FROM human_decisions WHERE agent_run_id = $1::uuid`, f.seed.RunID); n != "1" {
		t.Fatalf("%s human decisions after retries, want 1", n)
	}
}

func TestConcurrentSendsRecordExactlyOneDecision(t *testing.T) {
	f := newFixture(t)
	if _, _, err := f.choose(f.seed.Candidates[1], nil); err != nil {
		t.Fatal(err)
	}
	const clicks = 8
	var wg sync.WaitGroup
	results := make([]error, clicks)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, results[i] = f.send("send")
		}()
	}
	wg.Wait()

	ok := 0
	for _, err := range results {
		var c *strategystore.ConflictError
		switch {
		case err == nil:
			ok++
		case !errors.As(err, &c) || c.Code != strategystore.CodeAlreadyDecided:
			t.Fatalf("unexpected result of a concurrent send: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d sends succeeded, want exactly 1", ok)
	}
	if n := scalar(t, `SELECT count(*)::text FROM human_decisions WHERE agent_run_id = $1::uuid`, f.seed.RunID); n != "1" {
		t.Fatalf("%s human decisions, want 1", n)
	}
}

func TestSendRefusals(t *testing.T) {
	t.Run("before a choice", func(t *testing.T) {
		f := newFixture(t)
		_, err := f.send("send")
		asConflict(t, err, strategystore.CodeNoChoice)
	})
	t.Run("while a blocking eval failed", func(t *testing.T) {
		f := newFixture(t)
		strategytest.MakeBlocking(t, env.DB, f.seed.Bundles[1])
		if _, _, err := f.choose(f.seed.Candidates[1], nil); err != nil {
			t.Fatal(err)
		}
		_, err := f.send("send")
		asRefused(t, err, "blocking_eval")
		if n := scalar(t, `SELECT count(*)::text FROM human_decisions WHERE agent_run_id = $1::uuid`, f.seed.RunID); n != "0" {
			t.Fatal("a refused send recorded a human decision")
		}
		if _, err := f.send("discard"); err != nil {
			t.Fatalf("a blocked candidate can still be discarded: %v", err)
		}
	})
	t.Run("for a live run", func(t *testing.T) {
		f := newFixture(t)
		if _, _, err := f.choose(f.seed.Candidates[1], nil); err != nil {
			t.Fatal(err)
		}
		liveRun(t, f.seed.RunID)
		_, err := f.send("send")
		asRefused(t, err, "live_send_unavailable")
	})
	t.Run("for a run that is not waiting", func(t *testing.T) {
		f := newFixture(t)
		if _, _, err := f.choose(f.seed.Candidates[1], nil); err != nil {
			t.Fatal(err)
		}
		if _, err := env.DB.Exec(`UPDATE agent_runs SET status = 'cancelled' WHERE id = $1::uuid`, f.seed.RunID); err != nil {
			t.Fatal(err)
		}
		_, err := f.send("send")
		asRefused(t, err, "run_not_awaiting")
	})
}

func TestMalformedIdsAreNotFoundEverywhere(t *testing.T) {
	f := newFixture(t)
	ok := strategystore.DecisionRequest{SelectedCandidateID: f.seed.Candidates[0], Surface: "slack", ActorLabel: "a"}
	checks := map[string]error{}
	_, checks["decision"] = f.svc.Decision(f.ctx, "x")
	_, checks["inference"] = f.svc.Inference(f.ctx, "x")
	_, checks["bi"] = f.svc.LatestBusinessIntelligence(f.ctx, "x")
	_, _, checks["record"] = f.svc.RecordDecision(f.ctx, "x", ok)
	_, checks["send"] = f.svc.Send(f.ctx, "x", strategystore.SendRequest{Decision: "send", Surface: "slack", ActorLabel: "a"})
	_, checks["verdict"] = f.svc.SubmitVerdict(f.ctx, "x", strategystore.VerdictRequest{Verdict: "confirmed", Surface: "slack", ActorLabel: "a"})
	for name, err := range checks {
		if !errors.Is(err, strategystore.ErrNotFound) {
			t.Errorf("%s: error = %v, want not found", name, err)
		}
	}
	if _, _, err := f.svc.RecordDecision(f.ctx, f.seed.RunID, strategystore.DecisionRequest{}); err == nil {
		t.Fatal("an empty request was accepted")
	}
	if _, err := strategystore.New(nil, nil); err == nil {
		t.Fatal("a service without a database was built")
	}
}

func TestDiscardRecordsARejectAndExecutesNothing(t *testing.T) {
	f := newFixture(t)
	if _, _, err := f.choose(f.seed.Candidates[2], nil); err != nil {
		t.Fatal(err)
	}

	doc, err := f.send("discard")

	if err != nil {
		t.Fatal(err)
	}
	valid(t, "human_strategy_decision", doc)
	if got := scalar(t, `SELECT decision FROM human_decisions WHERE agent_run_id = $1::uuid`, f.seed.RunID); got != "reject" {
		t.Fatalf("human decision = %s, want reject", got)
	}
	if got := scalar(t, `SELECT status FROM agent_run_steps WHERE agent_run_id = $1::uuid AND step = 'execute'`, f.seed.RunID); got != "pending" {
		t.Fatalf("a discard must execute nothing; execute step is %s", got)
	}
	if got := scalar(t, `SELECT human_action FROM decision_episodes WHERE id = $1::uuid`, f.seed.EpisodeID); got != "REJECT" {
		t.Fatalf("human_action = %s", got)
	}
}
