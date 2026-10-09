package knowledge

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContractRulesLoad(t *testing.T) {
	r := contractRules(t)
	if r.Version != "knowledge_lifecycle:v1" || len(r.Rungs) != 3 || r.Stale.AfterDays != 180 {
		t.Fatalf("rules = %+v", r)
	}
	for status, want := range map[string]bool{
		StatusCandidate: false, StatusProvisional: true, StatusSupported: true, StatusConfirmed: true,
		StatusDisputed: false, StatusStale: false,
	} {
		if r.Applicable(status) != want {
			t.Fatalf("applicable(%s) = %v", status, !want)
		}
	}
}

func TestInvalidRulesAreRejected(t *testing.T) {
	base := func() map[string]any {
		var doc map[string]any
		raw, _ := json.Marshal(contractRules(t))
		_ = json.Unmarshal(raw, &doc)
		return doc
	}
	rung := func(doc map[string]any, i int) map[string]any { return doc["rungs"].([]any)[i].(map[string]any) }
	cases := map[string]func(map[string]any){
		"unknown key":              func(d map[string]any) { d["promote_fast"] = true },
		"no version":               func(d map[string]any) { d["version"] = "" },
		"unknown status":           func(d map[string]any) { d["applicable_statuses"] = []any{"active"} },
		"no applicable statuses":   func(d map[string]any) { d["applicable_statuses"] = []any{} },
		"rungs out of order":       func(d map[string]any) { r := d["rungs"].([]any); r[0], r[1] = r[1], r[0] },
		"two rungs":                func(d map[string]any) { d["rungs"] = d["rungs"].([]any)[:2] },
		"zero decisions":           func(d map[string]any) { rung(d, 0)["min_decisions"] = 0 },
		"share above one":          func(d map[string]any) { rung(d, 1)["max_counterexample_share"] = 1.5 },
		"non-increasing decisions": func(d map[string]any) { rung(d, 2)["min_decisions"] = 5 },
		"dispute share zero":       func(d map[string]any) { d["dispute"].(map[string]any)["min_counterexample_share"] = 0 },
		"stale zero":               func(d map[string]any) { d["stale"].(map[string]any)["after_days"] = 0 },
	}
	for name, mutate := range cases {
		doc := base()
		mutate(doc)
		raw, _ := json.Marshal(doc)
		if _, err := ParseRules(raw); !errors.Is(err, ErrInvalidRules) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	more := map[string]func(map[string]any){
		"negative minimum":          func(d map[string]any) { rung(d, 0)["min_positive_reactions"] = -1 },
		"fewer reactions higher up": func(d map[string]any) { rung(d, 2)["min_positive_reactions"] = 0 },
		"larger share higher up":    func(d map[string]any) { rung(d, 2)["max_counterexample_share"] = 0.9 },
	}
	for name, mutate := range more {
		doc := base()
		mutate(doc)
		raw, _ := json.Marshal(doc)
		if _, err := ParseRules(raw); !errors.Is(err, ErrInvalidRules) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	raw, _ := json.Marshal(base())
	if _, err := ParseRules(append(raw, []byte(` {"x":1}`)...)); !errors.Is(err, ErrInvalidRules) {
		t.Fatalf("trailing data: %v", err)
	}
	if _, err := LoadRules("does-not-exist.json"); !errors.Is(err, ErrInvalidRules) {
		t.Fatalf("missing file: %v", err)
	}
}

// narrowKnowledge is a candidate minted from a corrected verdict: scoped by transition status, endpoints,
// relationship and stage, supported by the episode it was learned from.
func narrowKnowledge() Knowledge {
	ep := "00000000-0000-4000-8000-000000000001"
	return Knowledge{
		ID: "k1", Status: StatusCandidate,
		SituationSignature: []Condition{{Field: "transition.status", Op: "eq", Value: "CANDIDATE"}},
		ApplicabilityConditions: []Condition{{Field: "transition.to_state", Op: "eq", Value: "EXPANSION"},
			{Field: "stage", Op: "eq", Value: "Negotiation"}},
		Provenance: Provenance{CreatedFrom: "manual", SourceDecisionEpisodeID: &ep},
		Counts:     Counts{Decisions: 1},
	}
}

func TestANarrowCandidateFromACorrectedVerdictIsOffered(t *testing.T) {
	r := contractRules(t)
	r.Similarity = nil // the exact-scope breadth check; similarity-matched knowledge is in similarity_test.go
	k := narrowKnowledge()
	if !r.ApplicableKnowledge(k) {
		t.Fatal("a corrected verdict's narrow candidate, seeded from its own corrected, sent episode, must be offered to the next similar case")
	}
	if r.Applicable(k.Status) {
		t.Fatal("the status stays candidate: only the narrow rule offers it")
	}
	for name, mutate := range map[string]func(*Knowledge){
		"no supporting episode yet": func(k *Knowledge) { k.Counts.Decisions = 0 },
		"scope too broad":           func(k *Knowledge) { k.ApplicabilityConditions = nil },
		"authored knowledge":        func(k *Knowledge) { k.Provenance.CreatedFrom = "authored" },
		"a delta seed":              func(k *Knowledge) { k.Provenance.CreatedFrom = "human_delta" },
		"no source episode":         func(k *Knowledge) { k.Provenance.SourceDecisionEpisodeID = nil },
		"a counterexample":          func(k *Knowledge) { k.Counts.Counterexamples = 1 },
		"a negative reaction":       func(k *Knowledge) { k.Counts.NegativeReactions = 1 },
		"disputed":                  func(k *Knowledge) { k.Status = StatusDisputed },
		"stale":                     func(k *Knowledge) { k.Status = StatusStale },
	} {
		k := narrowKnowledge()
		mutate(&k)
		if r.ApplicableKnowledge(k) {
			t.Errorf("%s: must not be offered", name)
		}
	}
	r.NarrowCandidate = nil
	if r.ApplicableKnowledge(narrowKnowledge()) {
		t.Error("without the narrow_candidate rule only the applicable statuses are offered")
	}
	if !r.ApplicableKnowledge(Knowledge{Status: StatusProvisional}) {
		t.Error("an applicable status is always offered")
	}
}

func TestInvalidNarrowCandidateRulesAreRejected(t *testing.T) {
	for name, nc := range map[string]string{
		"no sources":   `{"created_from": [], "min_decisions": 1}`,
		"zero support": `{"created_from": ["manual"], "min_decisions": 0}`,
		"unknown":      `{"created_from": ["authored"], "min_decisions": 1}`,
		"extra key":    `{"created_from": ["manual"], "min_decisions": 1, "x": 1}`,
	} {
		var doc map[string]any
		raw, _ := json.Marshal(contractRules(t))
		_ = json.Unmarshal(raw, &doc)
		var v any
		_ = json.Unmarshal([]byte(nc), &v)
		doc["narrow_candidate"] = v
		bad, _ := json.Marshal(doc)
		if _, err := ParseRules(bad); !errors.Is(err, ErrInvalidRules) {
			t.Errorf("%s: err = %v, want ErrInvalidRules", name, err)
		}
	}
}

// Only a missing similarity file means exact matching only; any other stat failure is an error, never a silent downgrade.
func TestLoadRulesFallsBackToExactMatchingOnlyWhenTheSimilarityFileIsAbsent(t *testing.T) {
	raw, err := os.ReadFile(repoFile(t, "contracts/knowledge/lifecycle.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "lifecycle.v1.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := LoadRules(path) // no similarity.v1.json beside it
	if err != nil || r.Similarity != nil {
		t.Fatalf("absent similarity file: similarity %v, err %v", r.Similarity, err)
	}
	denied := func(string) (fs.FileInfo, error) {
		return nil, &fs.PathError{Op: "stat", Path: "x", Err: fs.ErrPermission}
	}
	if _, err := loadRules(path, denied); !errors.Is(err, ErrInvalidRules) || !strings.Contains(err.Error(), "permission") {
		t.Fatalf("a stat failure other than not-exist must be returned, got %v", err)
	}
	missing := func(string) (fs.FileInfo, error) { return nil, fs.ErrNotExist }
	if r, err := loadRules(path, missing); err != nil || r.Similarity != nil {
		t.Fatalf("not-exist falls back to exact matching: %v %v", r.Similarity, err)
	}
}
