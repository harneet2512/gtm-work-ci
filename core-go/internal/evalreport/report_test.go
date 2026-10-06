package evalreport_test

// TestReportWorld builds a small but complete supervision corpus through the real write paths —
// seeded episodes, chooses, sends (one refused then repaired), a discard, human_deltas, explanations,
// seeded candidate versions and a promoted learned axis — then asserts every metric of the HAR-121
// report against hand-computed expectations. The timeline: episodes E1..E6 decide at t0 (before
// activation tMid), E5c at t2 (after).

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/evalreport"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

var (
	t0   = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC) // decisions before activation
	tMid = time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC) // evidence_sufficiency:v1 promoted_at
	t2   = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC) // the one post-activation episode
)

func TestReportWorld(t *testing.T) {
	world := ctxfixture.Get(t, env.DB)

	// evidence_sufficiency:v1 — a learned axis already promoted for this account: "the human attaches
	// the pen-test summary when security comes up". Executable shadow_spec (attachment_added is one of
	// the three executable literal-change kinds): fails while the draft lacks the attachment, passes
	// once it carries it — emits a human_delta-kind result on every send, never blocking.
	spec := fmt.Sprintf(`{"account_id":%q,"literal_changes":[{"kind":"attachment_added","after":"pentest-summary-2026Q3.pdf"}]}`,
		world.AccountA)
	insertVersion(t, "evidence_sufficiency", 1, "active", "semantic", "seed", spec, &tMid, world.AccountA)

	// E1: approve unchanged (draft 1, carries both attachments). Generation-time rows:
	// recipient_correctness:v1 pass (conf .9), grounding:v1 pass (conf .8). The send adds the
	// deterministic passes and the es:v1 axis pass.
	e1 := newEpisode(t)
	insertEval(t, e1.seed.RunID, 1, "recipient_correctness", "recipient_correctness:v1", "deterministic", "pass", ptr(0.9), t0)
	insertEval(t, e1.seed.RunID, 1, "grounding", "grounding:v1", "semantic", "pass", ptr(0.8), t0)
	e1.choose(e1.seed.Candidates[0], nil)
	e1.send("send")

	// E2: a body rewrite the evals never flagged — an unexplained delta that seeds
	// next_action_quality:v2 (the first learned candidate of an axis is always v2: v1 is the
	// shipped baseline). Generation rows pass on draft 2: grounding .6, naq .9. Same paragraph
	// count as the candidate -> a clean paragraph_edited change.
	e2 := newEpisode(t)
	insertEval(t, e2.seed.RunID, 2, "grounding", "grounding:v1", "semantic", "pass", ptr(0.6), t0)
	insertEval(t, e2.seed.RunID, 2, "next_action_quality", "next_action_quality:v1", "semantic", "pass", ptr(0.9), t0)
	e2.choose(e2.seed.Candidates[1], nil)
	art2 := e2.artifact(e2.seed.Candidates[1])
	art2.Body = "Hi Marco,\n\nLet us know what timing suits.\n\nBest,\nDana"
	e2.choose(e2.seed.Candidates[1], func(r *strategystore.DecisionRequest) { r.FinalArtifact = &art2 })
	e2.send("send")
	e2.setLabels("reduced_pressure", "style_only")

	// E3: refuse first (a recipient with no email blocks on recipient_correctness; the dropped
	// attachments also fail the es:v1 axis and provenance_coverage), then repair: Marco restored as
	// the recipient, Priya added to cc (a recipient edit — the delta is attributed to rc:v1, whose
	// deterministic refused-fail row is cited: a true detection), the pen-test attachment restored
	// while soc2 stays dropped (an attachment_removed — the delta is also attributed to es:v1, whose
	// axis fail is NOT citable: axis result ids hash (run, draft, axis, clock) but not the judged
	// artifact, so the repaired send's pass reuses the refused fail's id and is dropped on conflict —
	// a fail_unresolved_edit, an honest "the flag was not what the explanation cites").
	e3 := newEpisode(t)
	noEmail := strategytest.NewID()
	if _, err := env.DB.Exec(`INSERT INTO people (id, kind, display_name, account_id)
 VALUES ($1::uuid, 'contact', 'No Email (seed)', $2::uuid)`, noEmail, e3.seed.AccountID); err != nil {
		t.Fatal(err)
	}
	cand3 := e3.artifact(e3.seed.Candidates[0])
	e3.choose(e3.seed.Candidates[0], nil)
	stripped := cand3
	stripped.Attachments = nil
	e3.choose(e3.seed.Candidates[0], func(r *strategystore.DecisionRequest) {
		r.FinalTo = &[]strategystore.Recipient{{PersonID: noEmail, Role: "to"}}
		r.FinalArtifact = &stripped
	})
	var refused *strategystore.RefusedError
	if _, err := e3.svc.Send(e3.ctx, e3.seed.RunID,
		strategystore.SendRequest{Decision: "send", Surface: "api", ActorLabel: "alex"}); !errors.As(err, &refused) {
		t.Fatalf("the no-email send must refuse, got %v", err)
	}
	repaired := cand3
	repaired.Attachments = []string{"pentest-summary-2026Q3.pdf"}
	subj3 := "Re: EU rollout — security follow-up"
	repaired.Subject = &subj3
	e3.choose(e3.seed.Candidates[0], func(r *strategystore.DecisionRequest) {
		r.FinalTo = &[]strategystore.Recipient{{PersonID: e3.seed.Marco, Role: "to"}}
		r.FinalCC = &[]strategystore.Recipient{{PersonID: e3.seed.Priya, Role: "cc"}}
		r.FinalArtifact = &repaired
	})
	e3.send("send")
	e3.setLabels("corrected_fact", "added_missing_stakeholder")

	// E4: approve unchanged on draft 3 (no attachments — the es:v1 axis fails it and so does the
	// generation-time row; an rc:v1 fail the human overrode is a false block and a disagreeing
	// same-input repeat against the send-time pass).
	e4 := newEpisode(t)
	insertEval(t, e4.seed.RunID, 3, "recipient_correctness", "recipient_correctness:v1", "deterministic", "fail", ptr(0.7), t0)
	insertEval(t, e4.seed.RunID, 3, "evidence_sufficiency", "evidence_sufficiency:v1", "human_delta", "fail", nil, t0)
	e4.choose(e4.seed.Candidates[2], nil)
	e4.send("send")

	// E5a: attach the pen-test summary to draft 3, still before activation — the es:v1 axis now
	// passes (the correction it proxies happened anyway), so the delta is unexplained and seeds
	// evidence_sufficiency:v2.
	e5a := newEpisode(t)
	art5 := e5a.artifact(e5a.seed.Candidates[2])
	art5.Attachments = []string{"pentest-summary-2026Q3.pdf"}
	e5a.choose(e5a.seed.Candidates[2], nil)
	e5a.choose(e5a.seed.Candidates[2], func(r *strategystore.DecisionRequest) { r.FinalArtifact = &art5 })
	e5a.send("send")
	e5a.setLabels("corrected_fact")

	// E6: discard — a reject. grounding:v1 failed the draft (true block); next_step_quality:v1
	// passed what the human threw away (pass_rejected).
	e6 := newEpisode(t)
	insertEval(t, e6.seed.RunID, 3, "grounding", "grounding:v1", "semantic", "fail", ptr(0.4), t0)
	insertEval(t, e6.seed.RunID, 3, "next_step_quality", "next_step_quality:v1", "semantic", "pass", ptr(0.5), t0)
	e6.choose(e6.seed.Candidates[2], nil)
	e6.send("discard")

	// E5c: the same attachment correction after activation — still unexplained (a pass cannot repair
	// its own flag), seeding evidence_sufficiency:v3.
	e5c := newEpisode(t)
	art5c := e5c.artifact(e5c.seed.Candidates[2])
	art5c.Attachments = []string{"pentest-summary-2026Q3.pdf"}
	e5c.choose(e5c.seed.Candidates[2], nil)
	e5c.choose(e5c.seed.Candidates[2], func(r *strategystore.DecisionRequest) { r.FinalArtifact = &art5c })
	e5c.send("send")
	e5c.setLabels("corrected_fact")

	// The before/after split reads human_decisions.created_at (the decision wall-clock — eval_runs
	// created_at is the run's replay clock and cannot order the windows).
	for _, e := range []*episode{e1, e2, e3, e4, e5a, e6} {
		e.setDecidedAt(t0)
	}
	e5c.setDecidedAt(t2)
	// E6's rejection states why: the grounding was wrong. The calibration of grounding:v1 scores that
	// rejection; next_step_quality:v1 (which passed the draft) is not what the human pointed at.
	if _, err := env.DB.Exec(`UPDATE human_decisions SET reason = $2 WHERE agent_run_id = $1::uuid`,
		e6.seed.RunID, "grounding: the draft cites a number we cannot source"); err != nil {
		t.Fatalf("set rejection reason: %v", err)
	}

	set := generate(t, evalreport.Options{})
	assertRecipientV1(t, set)
	assertGroundingV1(t, set)
	assertEvidenceV1(t, set)
	assertNaqV1(t, set)
	assertSeededAndBareVersions(t, set)
	assertAttribution(t, set)
	assertSelection(t)
}
