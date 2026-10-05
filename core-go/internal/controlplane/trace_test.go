package controlplane_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/harneet2512/gtm-work/core-go/internal/controlplane"
	"github.com/harneet2512/gtm-work/core-go/internal/evaldispute/disputetest"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
	"github.com/harneet2512/gtm-work/core-go/internal/schemacheck"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

var causalOrder = []string{
	"source_event", "evidence", "resolution", "graph_mutation", "state", "precedents",
	"knowledge_retrieved", "knowledge_applicable", "knowledge_used", "candidates", "ranking",
	"cliff_message", "cliff_message", "cliff_message", "human_interaction", "recomputed_action", "knowledge_mutation",
}

func traceOf(t *testing.T, episodeID string) controlplane.EpisodeTrace {
	t.Helper()
	tr, err := reader(t).Trace(context.Background(), episodeID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(tr)
	if err != nil {
		t.Fatal(err)
	}
	v, err := schemacheck.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Validate("episode_trace", raw); err != nil {
		t.Fatalf("the trace violates episode_trace.v1.json: %v\n%s", err, raw)
	}
	return tr
}

func spanOf(t *testing.T, tr controlplane.EpisodeTrace, id string) controlplane.TraceSpan {
	t.Helper()
	for _, s := range tr.Spans {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no span %s in %v", id, spanIDs(tr))
	return controlplane.TraceSpan{}
}

func spanIDs(tr controlplane.EpisodeTrace) []string {
	var ids []string
	for _, s := range tr.Spans {
		ids = append(ids, s.ID)
	}
	return ids
}

func hasRef(s controlplane.TraceSpan, kind, id string) bool {
	for _, r := range s.Refs {
		if r.Kind == kind && r.ID == id {
			return true
		}
	}
	return false
}

func TestTraceOfAFreshEpisodeListsEveryStageInCausalOrder(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Trace Fresh"))
	tr := traceOf(t, seed.EpisodeID)

	if tr.EpisodeID != seed.EpisodeID || tr.AgentRunID != seed.RunID || tr.AccountID != seed.AccountID {
		t.Fatalf("header = %+v", tr)
	}
	var kinds []string
	for i, s := range tr.Spans {
		kinds = append(kinds, s.Kind)
		if s.Seq != i+1 {
			t.Errorf("span %d has seq %d", i, s.Seq)
		}
	}
	if !reflect.DeepEqual(kinds, causalOrder) {
		t.Fatalf("kinds = %v\nwant    %v", kinds, causalOrder)
	}
	want := map[string]string{
		"source_event:" + seed.ActivityID: "recorded", "resolution:" + seed.ActivityID: "recorded", "precedents:0": "not_recorded",
		"graph_mutation:0": "not_recorded", "knowledge_retrieved:0": "not_recorded", "knowledge_applicable:0": "not_recorded",
		"knowledge_used:0": "not_recorded", "candidates:" + seed.SetID: "recorded", "ranking:" + seed.SetID: "recorded",
		"cliff_message:bi": "not_recorded", "cliff_message:chooser": "not_recorded", "cliff_message:judgment": "pending",
		"human_interaction:0": "pending", "recomputed_action:0": "pending", "knowledge_mutation:0": "pending",
	}
	for id, status := range want {
		if got := spanOf(t, tr, id).Status; got != status {
			t.Errorf("%s status = %s, want %s", id, got, status)
		}
	}
}

func TestTraceSourceEvidenceStateAndCandidatesCarryTheirRows(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Trace Rows"))
	tr := traceOf(t, seed.EpisodeID)

	src := spanOf(t, tr, "source_event:"+seed.ActivityID)
	if !hasRef(src, "activity", seed.ActivityID) || !strings.Contains(src.Summary, "Marco asks for the security documents") || src.OccurredAt == nil {
		t.Fatalf("source = %+v", src)
	}
	var evidence controlplane.TraceSpan
	for _, s := range tr.Spans {
		if s.Kind == "evidence" {
			evidence = s
		}
	}
	if evidence.Status != "recorded" || len(evidence.EvidenceRefs) == 0 || !strings.Contains(string(evidence.EvidenceRefs), seed.ActivityID) || len(evidence.Refs) != 1 || evidence.Refs[0].Kind != "account_change" {
		t.Fatalf("evidence = %+v", evidence)
	}
	var state controlplane.TraceSpan
	for _, s := range tr.Spans {
		if s.Kind == "state" {
			state = s
		}
	}
	if state.Status != "recorded" || len(state.Refs) != 1 || state.Refs[0].Kind != "state_diff" || state.Attributes["is_material"] != true {
		t.Fatalf("state = %+v", state)
	}
	cand := spanOf(t, tr, "candidates:"+seed.SetID)
	if !hasRef(cand, "strategy_set", seed.SetID) || len(cand.Refs) != 7 {
		t.Fatalf("candidates carry the set, three candidates and three bundles: %+v", cand.Refs)
	}
	for i := range seed.Candidates {
		if !hasRef(cand, "strategy_candidate", seed.Candidates[i]) || !hasRef(cand, "eval_bundle", seed.Bundles[i]) {
			t.Errorf("candidate %d is not referenced", i)
		}
	}
	rank := spanOf(t, tr, "ranking:"+seed.SetID)
	order, _ := rank.Attributes["order"].([]string)
	if len(order) != 3 || order[0] != seed.Candidates[0] || rank.Attributes["preferred_candidate_id"] != seed.Candidates[0] {
		t.Fatalf("ranking = %+v", rank.Attributes)
	}
}

func TestTraceKeepsRetrievedApplicableAndUsedKnowledgeSeparate(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Trace Knowledge"))
	k1, k2, k3 := strategytest.NewID(), strategytest.NewID(), strategytest.NewID()
	exec(t, `UPDATE agent_run_steps SET detail = $2::jsonb WHERE agent_run_id = $1::uuid AND step = 'build_context'`, seed.RunID,
		`{"knowledge_attribution":{"as_of":"2026-01-02T03:04:05Z","retrieved":["`+k1+`","`+k2+`","`+k3+`"],"applicable":["`+k1+`","`+k2+`"],`+
			`"exception_blocked":["`+k3+`"],"used":["`+k1+`"]}}`)
	tr := traceOf(t, seed.EpisodeID)

	ids := func(id string) []string {
		got, _ := spanOf(t, tr, id).Attributes["knowledge_ids"].([]string)
		return got
	}
	if got := ids("knowledge_retrieved:0"); len(got) != 3 {
		t.Errorf("retrieved = %v", got)
	}
	if got := ids("knowledge_applicable:0"); !reflect.DeepEqual(got, []string{k1, k2}) {
		t.Errorf("applicable = %v", got)
	}
	if got := ids("knowledge_used:0"); !reflect.DeepEqual(got, []string{k1}) {
		t.Errorf("used = %v", got)
	}
	app := spanOf(t, tr, "knowledge_applicable:0")
	if blocked, _ := app.Attributes["exception_blocked_ids"].([]string); !reflect.DeepEqual(blocked, []string{k3}) {
		t.Errorf("blocked = %v", app.Attributes["exception_blocked_ids"])
	}
	used := spanOf(t, tr, "knowledge_used:0")
	if used.Status != "recorded" || used.OccurredAt == nil {
		t.Errorf("used = %+v", used)
	}
	// Citation is not influence: the span is titled for what the data proves and says influence was not measured.
	if used.Title != "Knowledge cited" || used.Attributes["influence_measured"] != false || !strings.Contains(used.Summary, "cited") {
		t.Errorf("the knowledge_used span overclaims: title %q influence_measured %v summary %q", used.Title, used.Attributes["influence_measured"], used.Summary)
	}
	if _, claimed := spanOf(t, tr, "knowledge_applicable:0").Attributes["influence_measured"]; claimed {
		t.Error("only the cited-knowledge span carries influence_measured")
	}
	if !hasRef(spanOf(t, tr, "knowledge_retrieved:0"), "knowledge", k3) || hasRef(used, "knowledge", k3) {
		t.Error("retrieval is not influence: k3 is retrieved but never used")
	}
}

func TestTraceAttachesEveryEvalResultToItsFamilysSpanExactlyOnce(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Trace Evals"))
	byType := map[string]string{}
	for evalType, verdict := range map[string]string{
		"provenance_coverage": "pass", "champion_continuity": "fail", "cta_calibration": "warn", "knowledge_applicability": "pass",
		"evidence_sufficiency": "abstain", "human_delta": "pass", "trajectory": "pass", "crm_writeback": "pass",
	} {
		byType[evalType] = addResult(t, seed.RunID, 1, evalType, verdict, false)
	}
	tr := traceOf(t, seed.EpisodeID)

	onSpan := func(kind string) []string {
		for _, s := range tr.Spans {
			if s.Kind == kind {
				return s.EvalResultIDs
			}
		}
		return nil
	}
	cases := map[string]string{
		"evidence": "provenance_coverage", "knowledge_applicable": "knowledge_applicability", "ranking": "evidence_sufficiency", "human_interaction": "human_delta",
	}
	for kind, evalType := range cases {
		if got := onSpan(kind); len(got) != 1 || got[0] != byType[evalType] {
			t.Errorf("%s carries %v, want %s", kind, got, byType[evalType])
		}
	}
	if got := onSpan("candidates"); len(got) != 2 {
		t.Errorf("candidates carry the E8 and E12 results: %v", got)
	}
	if got := onSpan("tool_call"); len(got) != 1 || got[0] != byType["crm_writeback"] {
		t.Errorf("tool use, permissions and writes (E13) attach to the tool_call span: %v", got)
	}
	if len(tr.UnassignedEvalResultIDs) != 1 || tr.UnassignedEvalResultIDs[0] != byType["trajectory"] {
		t.Errorf("trace-integrity results have no span of their own and are listed as unassigned: %v", tr.UnassignedEvalResultIDs)
	}
	seen := map[string]int{}
	for _, s := range tr.Spans {
		for _, id := range s.EvalResultIDs {
			seen[id]++
		}
	}
	for _, id := range tr.UnassignedEvalResultIDs {
		seen[id]++
	}
	if len(seen) != len(byType) {
		t.Fatalf("%d results reachable, %d persisted", len(seen), len(byType))
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("result %s is on %d spans", id, n)
		}
	}
}

func TestTraceCliffMessagesShowPostedReservedAndPending(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Trace Cliff"))
	exec(t, `INSERT INTO surface_messages (subject_id, surface, kind, channel, ts) VALUES ($1::uuid, 'slack', 'bi', 'C0DEMO', '1759587744.000100')`, seed.BIID)
	exec(t, `INSERT INTO surface_messages (subject_id, surface, kind, channel) VALUES ($1::uuid, 'slack', 'chooser', 'C0DEMO')`, seed.EpisodeID)
	tr := traceOf(t, seed.EpisodeID)

	bi := spanOf(t, tr, "cliff_message:bi")
	if bi.Status != "recorded" || bi.Attributes["posted"] != true || !hasRef(bi, "surface_message", seed.BIID+"/slack/bi") || !strings.Contains(bi.Summary, "posted") {
		t.Errorf("bi = %+v", bi)
	}
	chooser := spanOf(t, tr, "cliff_message:chooser")
	if chooser.Status != "recorded" || chooser.Attributes["posted"] != false || !strings.Contains(chooser.Summary, "reserved") {
		t.Errorf("a reservation without a ts is not a confirmed post: %+v", chooser)
	}
	if j := spanOf(t, tr, "cliff_message:judgment"); j.Status != "pending" {
		t.Errorf("judgment before the human decides = %s", j.Status)
	}

	choose(t, seed, seed.Candidates[1])
	send(t, seed, "send")
	if j := spanOf(t, traceOf(t, seed.EpisodeID), "cliff_message:judgment"); j.Status != "not_recorded" {
		t.Errorf("judgment after the send with no message recorded = %s", j.Status)
	}
}

func TestPlainLanguageInUISummaries(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Trace Plain Words"))
	tr := traceOf(t, seed.EpisodeID)
	for _, s := range tr.Spans {
		for _, jargon := range []string{"E5", "E7", "projection diff", "state diff", "graph diff"} {
			if strings.Contains(s.Summary, jargon) {
				t.Errorf("span %s summary %q leaks internal wording %q", s.ID, s.Summary, jargon)
			}
		}
	}
}

func TestSendTimeResultsAttachToTheRecomputedActionNotTheCandidates(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Trace Send Evals"))
	gen := disputetest.SeedResult(t, env.DB, disputetest.Result{RunID: seed.RunID, DraftIndex: 1, EvalType: "champion_continuity", Verdict: "pass"})
	choose(t, seed, seed.Candidates[1])
	sendTime := disputetest.SeedResult(t, env.DB, disputetest.Result{RunID: seed.RunID, DraftIndex: 2, EvalType: "champion_continuity", Verdict: "fail", SendTime: true})
	send(t, seed, "send")
	tr := traceOf(t, seed.EpisodeID)

	cands := spanOf(t, tr, "candidates:"+seed.SetID)
	if !reflect.DeepEqual(cands.EvalResultIDs, []string{gen}) {
		t.Errorf("candidates carry only the generation-time results: %v", cands.EvalResultIDs)
	}
	rec := spanOf(t, tr, "recomputed_action:"+decisionID(t, seed))
	// The real send also writes its own send-time results (all tagged send), so check membership, not equality.
	if !slices.Contains(rec.EvalResultIDs, sendTime) || slices.Contains(rec.EvalResultIDs, gen) {
		t.Errorf("the send-time result is on the recomputed action and the generation one is not: %v", rec.EvalResultIDs)
	}
	if !strings.Contains(rec.Summary, "re-evaluated") {
		t.Errorf("with send-time results the span may say it was re-evaluated: %q", rec.Summary)
	}
}

func TestRecomputedActionDoesNotClaimAReEvaluationItHasNoResultsFor(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Trace No Send Evals"))
	choose(t, seed, seed.Candidates[1])
	send(t, seed, "send")
	// An episode sent before send-time results were tagged (or whose rows were lost) has nothing to link.
	exec(t, `DELETE FROM eval_runs WHERE agent_run_id = $1::uuid AND phase = 'send'`, seed.RunID)
	rec := spanOf(t, traceOf(t, seed.EpisodeID), "recomputed_action:"+decisionID(t, seed))
	if len(rec.EvalResultIDs) != 0 || strings.Contains(rec.Summary, "re-evaluated") {
		t.Errorf("no send-time results, so no claim of re-evaluation: %+v", rec)
	}
}

func TestTraceHumanInteractionRecomputedActionAndKnowledgeMutation(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Trace Human"))
	choose(t, seed, seed.Candidates[1])
	if s := spanOf(t, traceOf(t, seed.EpisodeID), "recomputed_action:"+decisionID(t, seed)); s.Status != "pending" {
		t.Errorf("recomputed before the send = %s", s.Status)
	}
	send(t, seed, "send")
	strategytest.SeedInference(t, env.DB, seed)
	record(t, seed.KnowledgeID, knowledge.Evidence{Kind: knowledge.EvidenceDecisionEpisode, RefID: seed.EpisodeID, At: time.Now()})
	tr := traceOf(t, seed.EpisodeID)

	hid := decisionID(t, seed)
	human := spanOf(t, tr, "human_interaction:"+hid)
	if human.Status != "recorded" || !hasRef(human, "human_strategy_decision", hid) || !strings.Contains(human.Summary, "Dana Kim chose") ||
		!strings.Contains(human.Summary, "over Ghost's pick") || !strings.Contains(human.Summary, "sent it") || human.Attributes["agreement"] != "overrode" {
		t.Fatalf("human = %+v", human)
	}
	inference := scalar(t, `SELECT id::text FROM judgment_inferences WHERE decision_episode_id = $1::uuid`, seed.EpisodeID)
	if !hasRef(human, "judgment_inference", inference) || human.Attributes["judgment_verdict"] != "pending" {
		t.Errorf("the judgment inference is a ref of the human interaction: %+v", human)
	}
	rec := spanOf(t, tr, "recomputed_action:"+hid)
	if rec.Status != "recorded" || rec.Attributes["send_decision"] != "send" || rec.Attributes["final_action"] != "send_email" {
		t.Errorf("recomputed = %+v", rec)
	}
	mut := spanOf(t, tr, "knowledge_mutation:"+seed.KnowledgeID)
	if mut.Status != "recorded" || !hasRef(mut, "knowledge", seed.KnowledgeID) || !strings.Contains(mut.Summary, "STRENGTHEN") {
		t.Errorf("mutation = %+v", mut)
	}
}

func decisionID(t *testing.T, seed strategytest.Seeded) string {
	t.Helper()
	return scalar(t, `SELECT id::text FROM human_strategy_decisions WHERE decision_episode_id = $1::uuid`, seed.EpisodeID)
}

func TestTraceOfADiscardedEpisodeSaysNoActionWasTaken(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Trace Discard"))
	choose(t, seed, seed.Candidates[0])
	send(t, seed, "discard")
	tr := traceOf(t, seed.EpisodeID)
	rec := spanOf(t, tr, "recomputed_action:"+decisionID(t, seed))
	if rec.Status != "recorded" || rec.Attributes["final_action"] != "none" || !strings.Contains(rec.Summary, "discarded") {
		t.Errorf("recomputed = %+v", rec)
	}
	if m := spanOf(t, tr, "knowledge_mutation:0"); m.Status != "not_recorded" {
		t.Errorf("a decided episode that taught nothing = %s", m.Status)
	}
}

func TestTraceGraphMutationNamesTheProjectionDiffOfTheTriggerEvent(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Trace Graph"))
	diff := scalar(t, `WITH j AS (INSERT INTO graph_projection_jobs (account_id, claimed_at, claimed_by, lease_expires_at, completed_at)
   VALUES ($1::uuid, now(), 'test', now(), now()) RETURNING id),
 g AS (INSERT INTO graph_projection_diffs (job_id, account_id, source_event_ids, summary, changes)
   SELECT j.id, $1::uuid, ARRAY[a.source_event_id], '{}'::jsonb, '[{"op":"add"},{"op":"add"},{"op":"change"}]'::jsonb FROM j, activities a WHERE a.id = $2::uuid RETURNING id)
 SELECT id::text FROM g`, seed.AccountID, seed.ActivityID)
	tr := traceOf(t, seed.EpisodeID)
	g := spanOf(t, tr, "graph_mutation:"+diff)
	if g.Status != "recorded" || !hasRef(g, "graph_diff", diff) || g.Attributes["change_count"] != 3 {
		t.Fatalf("graph = %+v", g)
	}
}

func TestTraceIsStableAcrossReadsAndClipsLongSummaries(t *testing.T) {
	acct := seedAccount(t, "Trace Stable")
	exec(t, `UPDATE activities SET summary = $2 WHERE account_id = $1::uuid`, acct, strings.Repeat("é", 900))
	seed := seedEpisode(t, acct)
	a, b := traceOf(t, seed.EpisodeID), traceOf(t, seed.EpisodeID)
	if !reflect.DeepEqual(spanIDs(a), spanIDs(b)) {
		t.Fatalf("span ids changed between reads:\n%v\n%v", spanIDs(a), spanIDs(b))
	}
	src := spanOf(t, a, "source_event:"+seed.ActivityID)
	if n := utf8.RuneCountInString(src.Summary); n > 600 || n < 590 {
		t.Fatalf("summary has %d characters, want it clipped to the 600 bound", n)
	}
	seen := map[string]bool{}
	for _, id := range spanIDs(a) {
		if seen[id] {
			t.Errorf("span id %s is not unique", id)
		}
		seen[id] = true
	}
}

func TestTraceOfAnUnknownEpisodeIsNotFound(t *testing.T) {
	for _, id := range []string{missing, "nope"} {
		if _, err := reader(t).Trace(context.Background(), id); !errors.Is(err, readmodel.ErrNotFound) {
			t.Errorf("%q: %v", id, err)
		}
	}
}
