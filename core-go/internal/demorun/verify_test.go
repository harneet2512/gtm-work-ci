package demorun

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

func fullFacts() Facts {
	return Facts{
		ManifestID: "m-1", InvisibilityAtSeed: "withheld", InvisibilityNow: "released", Played: true, SlackOn: true,
		BIUpdateID: "bi-1", M1TS: "1.1", RunID: "run-1", StrategySetID: "ss-1", EpisodeID: "ep-1", M2TS: "2.2", M3TS: "3.3",
		Decision:    &DecisionFact{ID: "d-1", SelectedCandidateID: "c-2", OriginalPreference: "c-1", SendDecision: "send", HumanDecision: "edit", HumanAction: "APPROVE_WITH_EDIT", Edits: 2},
		Delta:       &DeltaFact{ID: "hd-1", Labels: []string{"smaller_ask"}, Unexplained: false, LiteralChanges: 2},
		Inference:   &InferenceFact{ID: "ji-1", Verdict: "corrected", HasCorrection: true},
		VerdictRows: 1, NoteRows: 1, SlackRowsPosted: 3, AuditPosts: 3, AuditKnown: true,
	}
}

func stepByID(steps []Step, id string) Step {
	for _, s := range steps {
		if s.ID == id {
			return s
		}
	}
	return Step{ID: id, Status: "MISSING"}
}

func TestEvaluateAllPassOnACompleteWalkthrough(t *testing.T) {
	steps := Evaluate(fullFacts())
	for _, s := range steps {
		if s.Status != StatusPass {
			t.Errorf("%s %s = %s (%s), want PASS", s.ID, s.Name, s.Status, s.Detail)
		}
	}
	if len(steps) < 12 {
		t.Fatalf("only %d steps", len(steps))
	}
	var buf bytes.Buffer
	failed := WriteVerify(&buf, steps)
	if failed != 0 || !strings.Contains(buf.String(), "PASS") || !strings.Contains(buf.String(), "ep-1") || !strings.Contains(buf.String(), "hd-1") {
		t.Fatalf("report should carry ids and no failures (%d):\n%s", failed, buf.String())
	}
}

func TestEvaluateUnsentChoiceFailsWithAClickHint(t *testing.T) {
	f := fullFacts()
	f.Decision.SendDecision = "pending"
	f.Decision.HumanDecision, f.Decision.HumanAction, f.Delta = "", "", nil
	f.Inference, f.M3TS, f.VerdictRows, f.NoteRows = nil, "", 0, 0
	steps := Evaluate(f)
	d := stepByID(steps, "V07")
	if d.Status != StatusFail || !strings.Contains(d.Detail, "Send") {
		t.Fatalf("a chosen-but-unsent decision must FAIL and say Send: %+v", d)
	}
	if got := stepByID(steps, "V08").Status; got != StatusSkip {
		t.Fatalf("the delta step cannot be judged before a send, got %s", got)
	}
}

func TestEvaluateSendWithoutEditHasNoDeltaAndFails(t *testing.T) {
	f := fullFacts()
	f.Decision.HumanDecision, f.Decision.HumanAction, f.Decision.Edits, f.Delta = "approve", "APPROVE_UNCHANGED", 0, nil
	s := stepByID(Evaluate(f), "V08")
	if s.Status != StatusFail || !strings.Contains(s.Detail, "Edit") {
		t.Fatalf("an unedited send writes no HumanDelta; the demo requires an edit: %+v", s)
	}
}

func TestEvaluateEditedSendWithoutADeltaRowIsAFailure(t *testing.T) {
	f := fullFacts()
	f.Delta = nil
	s := stepByID(Evaluate(f), "V08")
	if s.Status != StatusFail || !strings.Contains(s.Detail, "no HumanDelta row") {
		t.Fatalf("an edited send with no delta is a product defect and must FAIL: %+v", s)
	}
}

func TestEvaluateJudgmentStepsEachSayWhatToClick(t *testing.T) {
	f := fullFacts()
	f.Inference = &InferenceFact{ID: "ji-1", Verdict: "pending"}
	f.VerdictRows, f.NoteRows = 0, 0
	steps := Evaluate(f)
	if s := stepByID(steps, "V10"); s.Status != StatusFail || !strings.Contains(s.Detail, "Needs correction") {
		t.Fatalf("pending verdict: %+v", s)
	}
	if s := stepByID(steps, "V11"); s.Status != StatusFail || !strings.Contains(s.Detail, "Add note") {
		t.Fatalf("no note: %+v", s)
	}
	f.Inference = nil
	if s := stepByID(Evaluate(f), "V10"); s.Status != StatusFail || !strings.Contains(s.Detail, "not generated") {
		t.Fatalf("no inference yet: %+v", s)
	}
}

func TestEvaluateVerdictWithoutHistoryRowsIsInconsistent(t *testing.T) {
	f := fullFacts()
	f.VerdictRows = 0
	if s := stepByID(Evaluate(f), "V12"); s.Status != StatusFail {
		t.Fatalf("an answered inference must have an append-only history row: %+v", s)
	}
}

func TestEvaluateSlackOffSkipsSlackStepsInsteadOfPassingThem(t *testing.T) {
	f := fullFacts()
	f.SlackOn, f.M1TS, f.M2TS, f.M3TS, f.SlackRowsPosted, f.AuditKnown = false, "", "", "", 0, false
	steps := Evaluate(f)
	for _, id := range []string{"V04", "V06", "V09", "V13"} {
		if s := stepByID(steps, id); s.Status != StatusSkip || !strings.Contains(s.Detail, "Slack") {
			t.Errorf("%s must be SKIP when Slack was off, got %s (%s)", id, s.Status, s.Detail)
		}
	}
}

func TestEvaluateMoreThanThreeSlackMessagesFails(t *testing.T) {
	f := fullFacts()
	f.AuditPosts = 4
	s := stepByID(Evaluate(f), "V13")
	if s.Status != StatusFail || !strings.Contains(s.Detail, "4") {
		t.Fatalf("a fourth Slack post breaks the three-message contract: %+v", s)
	}
	f = fullFacts()
	f.SlackRowsPosted = 4
	if s := stepByID(Evaluate(f), "V13"); s.Status != StatusFail {
		t.Fatalf("four posted surface rows must FAIL: %+v", s)
	}
}

func TestEvaluateBeforePlayEverythingAfterSeedIsSkippedNotFailed(t *testing.T) {
	f := Facts{ManifestID: "m-1", InvisibilityAtSeed: "withheld", InvisibilityNow: "withheld", SlackOn: true}
	steps := Evaluate(f)
	if s := stepByID(steps, "V01"); s.Status != StatusPass {
		t.Fatalf("seed step: %+v", s)
	}
	for _, s := range steps[2:] {
		if s.Status != StatusSkip {
			t.Errorf("%s %s = %s before Play, want SKIP", s.ID, s.Name, s.Status)
		}
	}
}

func TestCountAuditPostsCountsOnlyOKPostMessage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "audit.jsonl")
	body := `{"method":"chat.postMessage","ok":true,"ts":"1.1"}` + "\n" +
		`{"method":"chat.update","ok":true,"ts":"1.1"}` + "\n" +
		`{"method":"chat.postMessage","ok":false,"error":"x"}` + "\n" +
		"not json\n" +
		`{"method":"chat.postMessage","ok":true,"ts":"2.2"}` + "\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	posts, updates, known := CountAudit(p)
	if !known || posts != 2 || updates != 1 {
		t.Fatalf("posts=%d updates=%d known=%v", posts, updates, known)
	}
	if _, _, known := CountAudit(filepath.Join(t.TempDir(), "none")); known {
		t.Fatal("a missing audit file is unknown, not zero")
	}
}

// TestFetchFactsSQLRunsAgainstTheRealSchema executes every verify query on a migrated database with no rows: a
// misspelled column or table fails here instead of during the user's live walkthrough.
func TestFetchFactsSQLRunsAgainstTheRealSchema(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a Postgres")
	}
	env, err := storetest.Start(context.Background())
	if err != nil {
		t.Fatalf("start db: %v", err)
	}
	defer env.Close()
	st := DemoState{ManifestID: "00000000-0000-0000-0000-000000000001", AccountID: "00000000-0000-0000-0000-000000000002",
		BIUpdateID: "00000000-0000-0000-0000-000000000003", RunID: "00000000-0000-0000-0000-000000000004",
		EpisodeID: "00000000-0000-0000-0000-000000000005", StrategySetID: "00000000-0000-0000-0000-000000000006", InvisibilityAtSeed: "withheld"}
	f, err := FetchFacts(context.Background(), env.DB, st, FetchOptions{SlackOn: true})
	if err != nil {
		t.Fatalf("FetchFacts on an empty database: %v", err)
	}
	if f.Decision != nil || f.Delta != nil || f.Inference != nil || f.VerdictRows != 0 || f.M1TS != "" {
		t.Fatalf("an empty database holds nothing: %+v", f)
	}
	// The real Stores and the real PlayCore over the same empty database.
	stores := PGStores{DSN: env.URL}
	if err := stores.RequireEmpty(context.Background()); err != nil {
		t.Fatalf("a freshly migrated database is empty: %v", err)
	}
	if pf, err := stores.Facts(context.Background(), st, FetchOptions{SlackOn: true}); err != nil || pf.Decision != nil {
		t.Fatalf("PGStores.Facts: %+v %v", pf, err)
	}
	live := &LiveCore{CoreClient: CoreClient{Base: "http://127.0.0.1:1"}, DSN: env.URL}
	defer live.Close()
	if runs, err := live.PlayRuns(context.Background(), st.ManifestID); err != nil || len(runs) != 0 {
		t.Fatalf("LiveCore.PlayRuns on an empty database: %v %v", runs, err)
	}
	if err := (PGStores{DSN: "postgres://nobody:nothing@127.0.0.1:1/never?sslmode=disable"}).RequireEmpty(context.Background()); err == nil {
		t.Fatal("an unreachable database must be an error")
	}
}
