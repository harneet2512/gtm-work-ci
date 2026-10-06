package recompute

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/schemacheck"
)

const (
	cand      = "0ca00000-0000-4000-8000-0000000000a2"
	account   = "0a0c0000-0000-4000-8000-000000000001"
	knowledge = "0c17c000-0000-4000-8000-000000000017"
	decision  = "05d00000-0000-4000-8000-000000000c02"
)

func v(n int) *int { return &n }

var hashA, hashB = strings.Repeat("a", 64), strings.Repeat("b", 64)

// baseFacts is a sent decision with a bundle of one eval per dependency shape: recipients, text, both and
// neither.
func baseFacts(edits ...literalEdit) facts {
	return facts{
		RunID: "0f0a0000-0000-4000-8000-000000000601", EpisodeID: "0e9e0000-0000-4000-8000-000000000a01", AccountID: account,
		DecisionID: decision, Decided: true, SendDecision: "send", Edits: edits, CandidateID: cand, Ranking: 2,
		KnowledgeIDs: []string{knowledge}, Linked: true, StateBefore: v(7), StateAfter: v(7), HashBefore: hashA, HashAfter: hashA,
		Old: []judged{
			{"0e1a0000-0000-4000-8000-000000000a01", "recipient_correctness", "deterministic", "pass"},
			{"0e1a0000-0000-4000-8000-000000000a02", "champion_continuity", "semantic", "pass"},
			{"0e1a0000-0000-4000-8000-000000000a03", "cta_calibration", "semantic", "warn"},
			{"0e1a0000-0000-4000-8000-000000000a04", "buyer_readiness", "semantic", "pass"},
		},
		New: []judged{
			{"0e1a0000-0000-4000-8000-000000000b01", "recipient_correctness", "deterministic", "pass"},
			{"0e1a0000-0000-4000-8000-000000000b02", "permission_policy", "deterministic", "pass"},
			{"0e1a0000-0000-4000-8000-000000000b03", "provenance_coverage", "deterministic", "pass"},
		},
		Now: time.Date(2026, 10, 4, 15, 0, 0, 0, time.UTC),
	}
}

var (
	removeCC     = literalEdit{Kind: "recipient_removed", Before: map[string]any{"person_id": "p1", "role": "cc"}}
	changeSubj   = literalEdit{Kind: "subject_changed", Before: "Old", After: "New"}
	editParagrph = literalEdit{Kind: "paragraph_edited", Before: "Hi", After: "Hello"}
)

func refsOf(rs []SpanRef, kind RefKind) []string {
	var out []string
	for _, r := range rs {
		if r.Kind == kind {
			if r.RefID != nil {
				out = append(out, *r.RefID)
			} else {
				out = append(out, "")
			}
		}
	}
	slices.Sort(out)
	return out
}

func labels(rs []SpanRef, kind RefKind) []string {
	var out []string
	for _, r := range rs {
		if r.Kind == kind {
			out = append(out, r.Label)
		}
	}
	slices.Sort(out)
	return out
}

func valid(t *testing.T, doc Invalidation) {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	c, err := schemacheck.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Validate("dependency_invalidation", raw); err != nil {
		t.Fatalf("violates its schema: %v\n%s", err, raw)
	}
}

func TestAnEditToTheRecipientInvalidatesTheRecipientDependentEvalsAndPreservesAccountState(t *testing.T) {
	doc, err := derive(baseFacts(removeCC))
	if err != nil {
		t.Fatal(err)
	}
	valid(t, doc)
	if doc.Status != Reevaluated || !doc.Edited || len(doc.Entries) != 1 {
		t.Fatalf("status %s edited %v entries %d", doc.Status, doc.Edited, len(doc.Entries))
	}
	e := doc.Entries[0]
	if e.Edit.Field != Recipients || e.Edit.SemanticClass != "stakeholder_change" {
		t.Fatalf("edit = %+v", e.Edit)
	}
	if got, want := labels(e.Invalidated, EvalResult), []string{"champion_continuity", "recipient_correctness"}; !slices.Equal(got, want) {
		t.Errorf("invalidated evals = %v, want exactly the ones that read the recipients: %v", got, want)
	}
	if len(refsOf(e.Invalidated, ArtifactField)) != 1 || len(refsOf(e.Invalidated, RankingRat)) != 1 || !slices.Equal(refsOf(e.Invalidated, KnowledgeUse), []string{knowledge}) {
		t.Errorf("invalidated = %+v: the edited field, the ranking rationale and the knowledge-use claim go stale with it", e.Invalidated)
	}
	for _, r := range e.Invalidated {
		if r.Reason == "" {
			t.Errorf("an invalidated reference has no reason: %+v", r)
		}
	}
	// the account state is preserved, both on the entry and overall
	if doc.AccountState.Preserved == nil || !*doc.AccountState.Preserved || *doc.AccountState.VersionBefore != 7 || *doc.AccountState.VersionAfter != 7 {
		t.Errorf("account state = %+v", doc.AccountState)
	}
	if len(refsOf(e.Preserved, AccountState)) != 1 || len(refsOf(doc.PreservedOverall, AccountState)) != 1 {
		t.Errorf("the account state must be preserved: %+v / %+v", e.Preserved, doc.PreservedOverall)
	}
	// evals that do not read the recipients are preserved
	if got, want := labels(e.Preserved, EvalResult), []string{"buyer_readiness", "cta_calibration"}; !slices.Equal(got, want) {
		t.Errorf("preserved evals = %v, want %v", got, want)
	}
}

func TestAnUnrelatedEditLeavesTheEvalsThatDoNotDependOnItPreserved(t *testing.T) {
	doc, err := derive(baseFacts(changeSubj))
	if err != nil {
		t.Fatal(err)
	}
	valid(t, doc)
	e := doc.Entries[0]
	if got, want := labels(e.Invalidated, EvalResult), []string{"cta_calibration"}; !slices.Equal(got, want) {
		t.Errorf("invalidated evals = %v, want only the one that reads the subject", got)
	}
	if got, want := labels(e.Preserved, EvalResult), []string{"buyer_readiness", "champion_continuity", "recipient_correctness"}; !slices.Equal(got, want) {
		t.Errorf("preserved evals = %v, want %v: none of them reads the subject", got, want)
	}
	if got := labels(doc.PreservedOverall, EvalResult); !slices.Equal(got, []string{"buyer_readiness", "champion_continuity", "recipient_correctness"}) {
		t.Errorf("preserved overall = %v", got)
	}
	// a preserved eval says its re-run verdict was unchanged
	for _, r := range e.Preserved {
		if r.Kind == EvalResult && r.Label == "recipient_correctness" && r.Reason != "Declared independent of the subject; its re-run verdict is unchanged (pass)." {
			t.Errorf("reason = %q", r.Reason)
		}
	}
}

func TestNoEditMeansAnEmptyInvalidationSet(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*facts)
		status Status
		edited bool
	}{
		{"sent unedited", func(f *facts) {}, Unedited, false},
		// the human saved edits and then discarded: the edits exist, the edited artifact was never used
		{"discarded with saved edits", func(f *facts) { f.SendDecision, f.Edits = "discard", []literalEdit{removeCC} }, Discarded, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := baseFacts()
			tc.mutate(&f)
			doc, err := derive(f)
			if err != nil {
				t.Fatal(err)
			}
			valid(t, doc)
			if doc.Status != tc.status || doc.Edited != tc.edited || len(doc.Entries) != 0 {
				t.Fatalf("%+v", doc)
			}
			for _, k := range []RefKind{AccountState, EvalResult, RankingRat, KnowledgeUse} {
				if k == AccountState && tc.status == Discarded {
					// a discard has no send-time evaluation: the state is unproven, so it is not claimed
					if len(refsOf(doc.PreservedOverall, k)) != 0 {
						t.Errorf("a discarded decision claimed the state preserved with nothing to compare: %+v", doc.PreservedOverall)
					}
					continue
				}
				if len(refsOf(doc.PreservedOverall, k)) == 0 {
					t.Errorf("nothing was invalidated, so a %s must be preserved: %+v", k, doc.PreservedOverall)
				}
			}
		})
	}
}

func TestNoDecisionIsNotDecidedAndClaimsNothingPreserved(t *testing.T) {
	f := baseFacts()
	f.Decided, f.Old, f.New, f.StateAfter = false, nil, nil, nil
	doc, err := derive(f)
	if err != nil {
		t.Fatal(err)
	}
	valid(t, doc)
	if doc.Status != NotDecided || len(doc.Entries) != 0 || len(doc.PreservedOverall) != 0 || doc.AccountState.Preserved != nil {
		t.Fatalf("%+v", doc)
	}
}

func TestEditsSavedButNotSentInvalidateAndRecomputeNothing(t *testing.T) {
	f := baseFacts(removeCC)
	f.SendDecision, f.New, f.Linked, f.StateAfter = "pending", nil, false, nil
	doc, err := derive(f)
	if err != nil {
		t.Fatal(err)
	}
	valid(t, doc)
	e := doc.Entries[0]
	if doc.Status != EditsPend || len(e.Invalidated) == 0 || len(e.Recomputed) != 0 {
		t.Fatalf("status %s invalidated %d recomputed %d", doc.Status, len(e.Invalidated), len(e.Recomputed))
	}
	if len(e.NotRecomputed) != len(e.Invalidated) {
		t.Errorf("every invalidated object is not recomputed yet: %d of %d", len(e.NotRecomputed), len(e.Invalidated))
	}
	if doc.AccountState.VersionAfter != nil {
		t.Errorf("version_after = %v before the send", *doc.AccountState.VersionAfter)
	}
}

func TestWhatNothingReDerivedIsSaidToBeNotRecomputed(t *testing.T) {
	doc, _ := derive(baseFacts(removeCC))
	e := doc.Entries[0]
	// the semantic eval has no send-time result: stale, with no replacement
	var champion *SpanRef
	for i, r := range e.NotRecomputed {
		if r.Label == "champion_continuity" {
			champion = &e.NotRecomputed[i]
		}
	}
	if champion == nil || champion.Reason != reasonSemanticStale {
		t.Fatalf("not_recomputed = %+v: champion_continuity must be listed as stale with no new verdict", e.NotRecomputed)
	}
	if len(refsOf(e.NotRecomputed, RankingRat)) != 1 || len(refsOf(e.NotRecomputed, KnowledgeUse)) != 1 {
		t.Errorf("the ranking and the knowledge-use claim are not re-derived at send time: %+v", e.NotRecomputed)
	}
	// the deterministic eval has a new row that replaces it
	var replaced bool
	for _, r := range e.Recomputed {
		if r.Kind == EvalResult && r.Label == "recipient_correctness" && r.Replaces != nil && *r.Replaces == "0e1a0000-0000-4000-8000-000000000a01" {
			replaced = true
		}
	}
	if !replaced {
		t.Errorf("recomputed = %+v: the new recipient_correctness must name the result it replaces", e.Recomputed)
	}
	if len(refsOf(e.Recomputed, FinalArtifact)) != 1 {
		t.Errorf("the re-evaluated final artifact is recomputed: %+v", e.Recomputed)
	}
	// the send-time permission_policy reads the recipients too and had no old counterpart: a new row, replacing nothing
	var newRow bool
	for _, r := range e.Recomputed {
		if r.Kind == EvalResult && r.Label == "permission_policy" && r.Replaces == nil {
			newRow = true
		}
		if r.Kind == EvalResult && r.Label == "provenance_coverage" {
			t.Errorf("provenance_coverage does not read the recipients: %+v", r)
		}
	}
	if !newRow {
		t.Errorf("recomputed = %+v: permission_policy reads the recipients and was evaluated at send time", e.Recomputed)
	}
}

func TestAnUndeclaredDependencyIsInvalidatedNeverPreserved(t *testing.T) {
	f := baseFacts(removeCC)
	f.Old = append(f.Old, judged{"0e1a0000-0000-4000-8000-000000000a05", "provenance_coverage", "deterministic", "fail"})
	doc, err := derive(f)
	if err != nil {
		t.Fatal(err)
	}
	valid(t, doc)
	e := doc.Entries[0]
	// provenance_coverage is declared independent of the recipients, but its re-run verdict changed fail -> pass
	if !slices.Contains(labels(e.Invalidated, EvalResult), "provenance_coverage") {
		t.Fatalf("invalidated = %v: a re-run that changed must not be reported preserved", labels(e.Invalidated, EvalResult))
	}
	if slices.Contains(labels(e.Preserved, EvalResult), "provenance_coverage") {
		t.Error("an eval whose verdict changed was also reported preserved")
	}
	for _, r := range e.Invalidated {
		if r.Label == "provenance_coverage" && r.Reason == "" {
			t.Error("no reason")
		}
	}
}

func TestEveryEditGetsItsOwnEntryInOrder(t *testing.T) {
	doc, err := derive(baseFacts(removeCC, changeSubj, editParagrph))
	if err != nil {
		t.Fatal(err)
	}
	valid(t, doc)
	if len(doc.Entries) != 3 || doc.Entries[0].Index != 0 || doc.Entries[2].Index != 2 {
		t.Fatalf("entries = %d", len(doc.Entries))
	}
	for i, want := range []Field{Recipients, Subject, Body} {
		if doc.Entries[i].Edit.Field != want {
			t.Errorf("entry %d field = %s, want %s", i, doc.Entries[i].Edit.Field, want)
		}
	}
	// an eval some edit invalidated is not preserved overall: buyer_readiness is the only one none of them touches
	if got := labels(doc.PreservedOverall, EvalResult); !slices.Equal(got, []string{"buyer_readiness"}) {
		t.Errorf("preserved overall evals = %v, want only buyer_readiness", got)
	}
}

func TestAnUnlinkedSendIsHonestAboutWhatItCannotShow(t *testing.T) {
	f := baseFacts(removeCC)
	f.Linked, f.New = false, nil
	doc, _ := derive(f)
	valid(t, doc)
	e := doc.Entries[0]
	if doc.Status != Unavailable || len(e.Recomputed) != 0 || len(e.NotRecomputed) != len(e.Invalidated) {
		t.Fatalf("status %s recomputed %d not_recomputed %d invalidated %d", doc.Status, len(e.Recomputed), len(e.NotRecomputed), len(e.Invalidated))
	}
}

func TestAnUnknownEditKindIsAnErrorNotAGuess(t *testing.T) {
	if _, err := derive(baseFacts(literalEdit{Kind: "rewritten"})); err == nil {
		t.Fatal("an edit outside the vocabulary was given a field")
	}
}

func TestTheStateThatMovedIsReportedNotHidden(t *testing.T) {
	f := baseFacts(changeSubj)
	f.StateAfter = v(9)
	doc, _ := derive(f)
	valid(t, doc)
	if doc.AccountState.Preserved == nil || *doc.AccountState.Preserved {
		t.Fatal("the state version changed between the run and the send: it is not preserved")
	}
}

func TestStatePreservedIsProvenNotAssumed(t *testing.T) {
	cases := []struct {
		name string
		mut  func(f *facts)
		want string // true | false | unknown
	}{
		{"same version, same digest", func(f *facts) {}, "true"},
		{"same version, different digest", func(f *facts) { f.HashAfter = hashB }, "false"},
		{"different version", func(f *facts) { f.StateAfter = v(9) }, "false"},
		{"different version, no digests", func(f *facts) { f.StateAfter, f.HashBefore, f.HashAfter = v(9), "", "" }, "false"},
		{"the run's version is unknown", func(f *facts) { f.StateBefore = nil }, "unknown"},
		{"the send read no state", func(f *facts) { f.StateAfter = nil }, "unknown"},
		{"same version but the run's digest is unknown", func(f *facts) { f.HashBefore = "" }, "unknown"},
		{"same version but the send's digest is unknown", func(f *facts) { f.HashAfter = "" }, "unknown"},
	}
	for _, tc := range cases {
		f := baseFacts(changeSubj)
		tc.mut(&f)
		doc, err := derive(f)
		if err != nil {
			t.Fatal(err)
		}
		valid(t, doc)
		got := "unknown"
		if p := doc.AccountState.Preserved; p != nil {
			got = map[bool]string{true: "true", false: "false"}[*p]
		}
		if got != tc.want {
			t.Errorf("%s: preserved = %s, want %s", tc.name, got, tc.want)
		}
		claimed := len(refsOf(doc.PreservedOverall, AccountState)) + len(refsOf(doc.Entries[0].Preserved, AccountState))
		if (tc.want == "true") != (claimed == 2) || (tc.want != "true" && claimed != 0) {
			t.Errorf("%s: the state is listed as preserved %d times; it may be listed (twice) only when proven", tc.name, claimed)
		}
	}
}

func TestNoStateIsClaimedPreservedWithoutASend(t *testing.T) {
	for name, mut := range map[string]func(f *facts){
		"not decided": func(f *facts) { f.Decided = false },
		"discarded":   func(f *facts) { f.SendDecision = "discard"; f.StateAfter, f.HashAfter = nil, "" },
		"edits saved": func(f *facts) { f.SendDecision = "pending"; f.StateAfter, f.HashAfter = nil, "" },
	} {
		f := baseFacts(changeSubj)
		mut(&f)
		doc, _ := derive(f)
		valid(t, doc)
		if doc.AccountState.Preserved != nil || len(refsOf(doc.PreservedOverall, AccountState)) != 0 {
			t.Errorf("%s: preserved %v, overall %+v: with no send there is nothing to compare", name, doc.AccountState.Preserved, doc.PreservedOverall)
		}
	}
}

// A result is paired with the result of the same eval AND kind: a semantic result of the same eval type is not the
// re-run of a deterministic one.
func TestAResultIsPairedByEvalTypeAndKind(t *testing.T) {
	f := baseFacts(removeCC)
	f.New = append(f.New, judged{"0e1a0000-0000-4000-8000-000000000b09", "champion_continuity", "deterministic", "fail"})
	doc, _ := derive(f)
	valid(t, doc)
	for _, r := range doc.Entries[0].Recomputed {
		if r.Label == "champion_continuity" && r.Replaces != nil {
			t.Errorf("a deterministic champion_continuity result was paired with the semantic one: %+v", r)
		}
	}
	if !slices.Contains(labels(doc.Entries[0].NotRecomputed, EvalResult), "champion_continuity") {
		t.Errorf("the semantic champion_continuity was not re-run: it belongs in not_recomputed, got %v", labels(doc.Entries[0].NotRecomputed, EvalResult))
	}
}
