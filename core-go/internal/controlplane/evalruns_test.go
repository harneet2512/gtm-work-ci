package controlplane_test

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/controlplane"
	"github.com/harneet2512/gtm-work/core-go/internal/evalarea"
	"github.com/harneet2512/gtm-work/core-go/internal/evaldispute/disputetest"
	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
)

func addResult(t *testing.T, runID string, draft int, evalType, verdict string, blocking bool) string {
	t.Helper()
	return disputetest.SeedResult(t, env.DB, disputetest.Result{RunID: runID, DraftIndex: draft, EvalType: evalType, Verdict: verdict, Blocking: blocking})
}

func TestEvalRunCountsRealResultsPerAreaAndTotal(t *testing.T) {
	acct := seedAccount(t, "Eval Run Counts")
	seed := seedEpisode(t, acct)
	addResult(t, seed.RunID, 1, "champion_continuity", "fail", true)
	addResult(t, seed.RunID, 1, "cta_calibration", "warn", false)
	addResult(t, seed.RunID, 2, "champion_continuity", "pass", false)
	addResult(t, seed.RunID, 2, "provenance_coverage", "abstain", false)

	run, err := reader(t).EvalRun(context.Background(), seed.RunID)
	if err != nil {
		t.Fatal(err)
	}
	want := controlplane.Counts{Pass: 1, Warn: 1, Fail: 1, Unknown: 1, BlockingFail: 1, Total: 4}
	if run.ID != seed.RunID || run.AccountID != acct || run.AccountName != "Eval Run Counts" || run.Counts != want || run.ResultCount != 4 {
		t.Fatalf("eval run = %+v", run)
	}
	if run.DecisionEpisodeID == nil || *run.DecisionEpisodeID != seed.EpisodeID || run.PreviousEvalRunID != nil {
		t.Fatalf("episode / previous = %v / %v", run.DecisionEpisodeID, run.PreviousEvalRunID)
	}
	if len(run.Areas) != 4 {
		t.Fatalf("areas = %d", len(run.Areas))
	}
	byArea := map[string]controlplane.AreaCounts{}
	for _, a := range run.Areas {
		byArea[a.Area] = a
		if a.Delta != nil {
			t.Errorf("area %s has a delta without a previous run", a.Area)
		}
	}
	if a := byArea[string(evalarea.DecisionLearning)]; a.Counts.Total != 3 || a.Counts.Fail != 1 || a.Counts.Pass != 1 || a.Counts.Warn != 1 || !a.Measured {
		t.Errorf("decision_learning = %+v", a)
	}
	if a := byArea[string(evalarea.System)]; a.Measured || a.Counts.Total != 0 {
		t.Errorf("system = %+v: nothing in it was evaluated, so it is not measured", a)
	}
	if run.EvaluatedAt.IsZero() {
		t.Error("evaluated_at is the newest result's time")
	}
}

func TestEvalRunDeltaIsAgainstTheAccountsPreviousRun(t *testing.T) {
	acct := seedAccount(t, "Eval Run Deltas")
	first := seedEpisode(t, acct)
	addResult(t, first.RunID, 1, "champion_continuity", "fail", true)
	addResult(t, first.RunID, 1, "cta_calibration", "warn", false)
	second := seedEpisode(t, acct)
	addResult(t, second.RunID, 1, "champion_continuity", "pass", false)
	addResult(t, second.RunID, 1, "cta_calibration", "warn", false)
	other := seedEpisode(t, seedAccount(t, "Eval Run Deltas Other"))
	addResult(t, other.RunID, 1, "champion_continuity", "fail", false)

	run, err := reader(t).EvalRun(context.Background(), second.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.PreviousEvalRunID == nil || *run.PreviousEvalRunID != first.RunID {
		t.Fatalf("previous = %v, want the account's earlier run %s (never another account's)", run.PreviousEvalRunID, first.RunID)
	}
	for _, a := range run.Areas {
		if a.Delta == nil {
			t.Fatalf("area %s has no delta although a previous run exists", a.Area)
		}
		if a.Area == "decision_learning" && (a.Delta.Pass != 1 || a.Delta.Fail != -1 || a.Delta.BlockingFail != -1 || a.Delta.Total != 0) {
			t.Errorf("decision_learning delta = %+v", a.Delta)
		}
	}
	if firstRun, err := reader(t).EvalRun(context.Background(), first.RunID); err != nil || firstRun.PreviousEvalRunID != nil {
		t.Fatalf("the account's first run has no previous: %+v %v", firstRun.PreviousEvalRunID, err)
	}
}

func TestEvalRunNeedsResultsAndAnExistingRun(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Eval Run Empty"))
	for name, id := range map[string]string{"a run with no results": seed.RunID, "an unknown run": missing, "not a uuid": "nope"} {
		if _, err := reader(t).EvalRun(context.Background(), id); !errors.Is(err, readmodel.ErrNotFound) {
			t.Errorf("%s: err = %v, want ErrNotFound", name, err)
		}
	}
	if _, err := reader(t).EvalFamilies(context.Background(), seed.RunID); !errors.Is(err, readmodel.ErrNotFound) {
		t.Errorf("families of a run with no results: %v", err)
	}
}

func TestEvalFamiliesGroupIntoAreasFamiliesAndTypesWithDeltas(t *testing.T) {
	acct := seedAccount(t, "Eval Families")
	first := seedEpisode(t, acct)
	addResult(t, first.RunID, 1, "champion_continuity", "fail", true)
	addResult(t, first.RunID, 1, "trajectory", "fail", true)
	second := seedEpisode(t, acct)
	pass := addResult(t, second.RunID, 1, "champion_continuity", "pass", false)
	addResult(t, second.RunID, 2, "stakeholder_coverage", "warn", false)

	sum, err := reader(t).EvalFamilies(context.Background(), second.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if sum.EvalRunID != second.RunID || sum.PreviousEvalRunID == nil || *sum.PreviousEvalRunID != first.RunID || len(sum.Areas) != 4 {
		t.Fatalf("summary header = %+v", sum)
	}
	dl := sum.Areas[1]
	if dl.Area != "decision_learning" || len(dl.Families) != 1 || dl.Families[0].FamilyID != "E8" {
		t.Fatalf("decision_learning = %+v", dl)
	}
	e8 := dl.Families[0]
	if e8.Counts.Total != 2 || e8.Delta == nil || e8.Delta.Fail != -1 || e8.Delta.Pass != 1 || e8.Delta.Warn != 1 {
		t.Errorf("E8 = %+v delta %+v", e8.Counts, e8.Delta)
	}
	if e8.EvalTypes[0].EvalType != "champion_continuity" || len(e8.EvalTypes[0].ResultIDs) != 1 || e8.EvalTypes[0].ResultIDs[0] != pass {
		t.Errorf("champion_continuity = %+v", e8.EvalTypes[0])
	}
	sys := sum.Areas[3]
	if sys.Measured || len(sys.Families) != 1 || sys.Families[0].FamilyID != "E18" || sys.Families[0].Delta.Fail != -1 {
		t.Errorf("system = %+v: the previous run's failing trajectory must show as a vanished family", sys)
	}
}

func TestListEvalRunsIsNewestFirstFilteredByAccountAndPaged(t *testing.T) {
	acct := seedAccount(t, "Eval List")
	var ids []string
	for i := 0; i < 3; i++ {
		s := seedEpisode(t, acct)
		addResult(t, s.RunID, 1, "champion_continuity", "pass", false)
		ids = append(ids, s.RunID)
	}
	noResults := seedEpisode(t, acct) // a run with no EvalResults is not an EvalRun
	_ = noResults
	ids = ids[:3]

	all, err := reader(t).ListEvalRuns(context.Background(), controlplane.EvalRunFilter{AccountID: acct})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Items) != 3 || all.NextCursor != nil {
		t.Fatalf("list = %d items, cursor %v", len(all.Items), all.NextCursor)
	}
	for i, want := range []string{ids[2], ids[1], ids[0]} {
		if all.Items[i].ID != want {
			t.Fatalf("item %d = %s, want %s (newest first)", i, all.Items[i].ID, want)
		}
	}
	if all.Items[0].PreviousEvalRunID == nil || *all.Items[0].PreviousEvalRunID != ids[1] || all.Items[2].PreviousEvalRunID != nil {
		t.Fatalf("previous links = %v / %v", all.Items[0].PreviousEvalRunID, all.Items[2].PreviousEvalRunID)
	}

	seen := map[string]bool{}
	cursor := ""
	for pages := 0; pages < 10; pages++ {
		page, err := reader(t).ListEvalRuns(context.Background(), controlplane.EvalRunFilter{AccountID: acct, Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range page.Items {
			if seen[it.ID] {
				t.Fatalf("%s listed twice", it.ID)
			}
			seen[it.ID] = true
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 3 {
		t.Fatalf("paging saw %d runs, want 3", len(seen))
	}
}

func TestListEvalRunsRefusesBadArguments(t *testing.T) {
	for name, f := range map[string]controlplane.EvalRunFilter{
		"malformed account": {AccountID: "nope"}, "limit out of range": {Limit: 999}, "garbage cursor": {Cursor: "%%%"}, "foreign cursor": {Cursor: "Zm9yZWlnbg"},
	} {
		if _, err := reader(t).ListEvalRuns(context.Background(), f); !errors.Is(err, readmodel.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestListEvalRunsOfAnUnknownAccountIsEmpty(t *testing.T) {
	page, err := reader(t).ListEvalRuns(context.Background(), controlplane.EvalRunFilter{AccountID: missing})
	if err != nil || len(page.Items) != 0 || page.NextCursor != nil {
		t.Fatalf("page = %+v %v", page, err)
	}
}

// TestEveryStoredEvalTypeHasAnArea pins the SQL enum to the area mapping: a new eval_type added by a migration
// without an eval_areas.json entry would silently fall out of every area.
func TestEveryStoredEvalTypeHasAnArea(t *testing.T) {
	rows, err := env.DB.Query(`SELECT pg_get_constraintdef(c.oid) FROM pg_constraint c JOIN pg_type t ON t.oid = c.contypid WHERE t.typname = 'eval_type'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	quoted := regexp.MustCompile(`'([a-z_]+)'`)
	n := 0
	for rows.Next() {
		var def string
		if err := rows.Scan(&def); err != nil {
			t.Fatal(err)
		}
		for _, m := range quoted.FindAllStringSubmatch(def, -1) {
			n++
			if _, ok := evalarea.AreaOf(m[1]); !ok {
				t.Errorf("eval_type %q has no area in contracts/evals/eval_areas.json", m[1])
			}
		}
	}
	if n < 30 {
		t.Fatalf("read only %d eval types from the domain constraint", n)
	}
}

func TestCompareRunsReadsTheLadderPerEvalType(t *testing.T) {
	acct := seedAccount(t, "Eval Compare")
	a := seedEpisode(t, acct)
	addResult(t, a.RunID, 1, "champion_continuity", "fail", true)
	addResult(t, a.RunID, 2, "champion_continuity", "pass", false) // the worst candidate decides the side
	addResult(t, a.RunID, 1, "cta_calibration", "warn", false)
	addResult(t, a.RunID, 1, "crm_writeback", "pass", false)
	addResult(t, a.RunID, 1, "provenance_coverage", "pass", false)
	b := seedEpisode(t, acct)
	bPass := addResult(t, b.RunID, 1, "champion_continuity", "pass", false)
	addResult(t, b.RunID, 1, "cta_calibration", "warn", false)
	addResult(t, b.RunID, 1, "crm_writeback", "fail", false)
	addResult(t, b.RunID, 1, "stakeholder_coverage", "pass", false)

	cmp, err := reader(t).CompareEvalRuns(context.Background(), a.RunID, b.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if cmp.A.EvalRunID != a.RunID || cmp.B.EvalRunID != b.RunID || cmp.A.Counts.Total != 5 || cmp.B.Counts.Total != 4 {
		t.Fatalf("sides = %+v / %+v", cmp.A, cmp.B)
	}
	got := map[string]controlplane.CompareRow{}
	var order []string
	for _, r := range cmp.Rows {
		got[r.EvalType] = r
		order = append(order, r.EvalType)
	}
	check := func(evalType, wantA, wantB, change string) {
		t.Helper()
		r, ok := got[evalType]
		if !ok {
			t.Fatalf("no row for %s: %v", evalType, order)
		}
		if deref(r.A) != wantA || deref(r.B) != wantB || r.Change != change {
			t.Errorf("%s: %s -> %s %s, want %s -> %s %s", evalType, deref(r.A), deref(r.B), r.Change, wantA, wantB, change)
		}
	}
	check("champion_continuity", "fail", "pass", "improved")
	check("cta_calibration", "warn", "warn", "unchanged")
	check("crm_writeback", "pass", "fail", "regressed")
	check("provenance_coverage", "pass", "", "removed")
	check("stakeholder_coverage", "", "pass", "added")
	if r := got["champion_continuity"]; !r.ABlocking || r.BBlocking || len(r.AResultIDs) != 2 || len(r.BResultIDs) != 1 || r.BResultIDs[0] != bPass {
		t.Errorf("champion_continuity flags/ids = %+v", r)
	}
	if o := cmp.Overall; o.Change != "regressed" || o.Improved != 1 || o.Regressed != 1 || o.Unchanged != 1 || o.Added != 1 || o.Removed != 1 {
		t.Errorf("overall = %+v (one regression makes the episode regressed)", o)
	}
	if want := (controlplane.Counts{Pass: -1, BlockingFail: -1, Total: -1}); cmp.Overall.Delta != want {
		t.Errorf("overall delta = %+v, want B minus A", cmp.Overall.Delta)
	}
	// Area order first, then family order, then eval type: provenance (E1), then E8, E12 and E13, all Decision & Learning.
	wantOrder := []string{"provenance_coverage", "champion_continuity", "stakeholder_coverage", "cta_calibration", "crm_writeback"}
	for i, w := range wantOrder {
		if order[i] != w {
			t.Fatalf("row order = %v, want %v", order, wantOrder)
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func TestCompareRunsWithItself(t *testing.T) {
	a := seedEpisode(t, seedAccount(t, "Eval Compare Self"))
	addResult(t, a.RunID, 1, "champion_continuity", "warn", false)
	same, err := reader(t).CompareEvalRuns(context.Background(), a.RunID, a.RunID)
	if err != nil || same.Overall.Change != "unchanged" || same.Overall.Unchanged != 1 || same.Overall.Delta.Total != 0 {
		t.Fatalf("self comparison = %+v %v", same.Overall, err)
	}
}

// A comparison is the before/after of ONE trigger (a re-run of the same event). Two accounts, or two different events
// of one account, are not that: cross-account comparison is out of scope (HAR-129 demo-loop clarification) and answers
// ErrNotComparable, never a number that would invite reading one account's knowledge into another.
func TestCompareRunsRefusesRunsOfDifferentAccounts(t *testing.T) {
	a := seedEpisode(t, seedAccount(t, "Eval Compare One"))
	addResult(t, a.RunID, 1, "champion_continuity", "warn", false)
	b := seedEpisode(t, seedAccount(t, "Eval Compare Other"))
	addResult(t, b.RunID, 1, "champion_continuity", "pass", false)
	if _, err := reader(t).CompareEvalRuns(context.Background(), a.RunID, b.RunID); !errors.Is(err, controlplane.ErrNotComparable) {
		t.Fatalf("cross-account comparison: %v", err)
	}
}

func TestCompareRunsRefusesDifferentTriggersOfOneAccountAndAcceptsRerunsOfOne(t *testing.T) {
	acct := seedAccount(t, "Eval Compare Triggers")
	a := seedEpisode(t, acct)
	addResult(t, a.RunID, 1, "champion_continuity", "fail", false)
	rerun := seedEpisode(t, acct) // the account's newest activity is the same one: a re-run of the same trigger
	addResult(t, rerun.RunID, 1, "champion_continuity", "pass", false)
	if cmp, err := reader(t).CompareEvalRuns(context.Background(), a.RunID, rerun.RunID); err != nil || cmp.Overall.Change != "improved" {
		t.Fatalf("a re-run of the same trigger compares: %+v %v", cmp.Overall, err)
	}

	exec(t, `WITH se AS (
  INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, payload, occurred_at)
  VALUES ('email', 'later-obj', 'k2', encode(sha256(convert_to('later-obj', 'UTF8')), 'hex'), '{}'::jsonb, '2026-02-02T03:04:05Z') RETURNING id)
INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, account_id, summary, provenance)
SELECT id, 'EmailReceived', 'email', 'later-obj', '2026-02-02T03:04:05Z', $1::uuid, 'a later email', '{"source_system":"email"}'::jsonb FROM se`, acct)
	later := seedEpisode(t, acct) // triggered by the later activity
	addResult(t, later.RunID, 1, "champion_continuity", "pass", false)
	if _, err := reader(t).CompareEvalRuns(context.Background(), a.RunID, later.RunID); !errors.Is(err, controlplane.ErrNotComparable) {
		t.Fatalf("two different triggers of one account: %v", err)
	}
}

func TestCompareRunsRefusesUnknownAndMalformedSides(t *testing.T) {
	a := seedEpisode(t, seedAccount(t, "Eval Compare Refusals"))
	addResult(t, a.RunID, 1, "champion_continuity", "pass", false)
	for name, ids := range map[string][2]string{"unknown b": {a.RunID, missing}, "unknown a": {missing, a.RunID}} {
		if _, err := reader(t).CompareEvalRuns(context.Background(), ids[0], ids[1]); !errors.Is(err, readmodel.ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := reader(t).CompareEvalRuns(context.Background(), a.RunID, "nope"); !errors.Is(err, readmodel.ErrInvalid) {
		t.Errorf("a malformed id is invalid, not missing: %v", err)
	}
}

func TestNewRequiresADatabase(t *testing.T) {
	if _, err := controlplane.New(nil); err == nil {
		t.Fatal("nil database accepted")
	}
}
