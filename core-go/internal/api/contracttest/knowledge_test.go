package contracttest

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledgestore"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

// knowledgeFixture is one seeded knowledge object: id and its human key.
type knowledgeFixture struct{ id, key, status string }

// seedKnowledge writes a new candidate in a committed transaction (the HTTP stack reads on another
// connection), so the row outlives the test. An explicit key orders the list by number, not text.
func seedKnowledge(t *testing.T, key, title string) knowledgeFixture {
	t.Helper()
	k := knowledge.Knowledge{
		ID: strategytest.NewID(), Key: &key, Title: title,
		SituationSignature: []knowledge.Condition{
			{Field: "motion", Op: "eq", Value: "expansion"},
			{Field: "diff.new_stakeholder_entered", Op: "exists"}},
		ApplicabilityConditions: []knowledge.Condition{
			{Field: "transition.status", Op: "in", Value: []any{"CANDIDATE"}}},
		Guidance: knowledge.Guidance{Summary: "Keep the champion in the thread.",
			Do: []string{"cc the champion"}, Dont: []string{"send a lone-stakeholder recap"}},
		Exceptions: []knowledge.Exception{{Description: "the champion delegated the deal",
			Conditions: []knowledge.Condition{{Field: "champion_status", Op: "eq", Value: "delegated"}}}},
		EvidenceClasses:  []string{"methodology"},
		UsedByEvaluators: []string{"champion_continuity:v1"},
		Provenance:       knowledge.Provenance{CreatedFrom: "human_delta"},
	}
	tx, err := env.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	got, err := knowledgestore.Insert(context.Background(), tx, k, "created from a human delta")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return knowledgeFixture{id: got.ID, key: *got.Key, status: got.Status}
}

// seedSupportedKnowledge records the evidence the supported rung of lifecycle.v1.json needs
// (5 decisions, 3 positive reactions, counterexample share <= 0.2) through the store's own write
// path, so the served object carries real counts, supporting episodes and status history.
func seedSupportedKnowledge(t *testing.T, key, title string) knowledgeFixture {
	t.Helper()
	fx := seedKnowledge(t, key, title)
	rules, err := knowledge.LoadRules(repoFile(t, "contracts/knowledge/lifecycle.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := env.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	evidence := []knowledge.Evidence{}
	for i := 0; i < 6; i++ {
		evidence = append(evidence, knowledge.Evidence{
			Kind: knowledge.EvidenceDecisionEpisode, RefID: strategytest.NewID(), At: at.Add(time.Duration(i) * time.Hour)})
	}
	for i := 0; i < 3; i++ {
		evidence = append(evidence, knowledge.Evidence{
			Kind: knowledge.EvidenceCustomerReaction, RefID: strategytest.NewID(), Polarity: "positive",
			At: at.Add(time.Duration(6+i) * time.Hour)})
	}
	for _, ev := range evidence {
		if _, _, err := knowledgestore.RecordEvidence(context.Background(), tx, fx.id, ev, rules); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return knowledgeFixture{id: fx.id, key: fx.key, status: knowledge.StatusSupported}
}

// repoFile finds a path inside the repository by walking up from the working directory.
func repoFile(t *testing.T, rel string) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			return filepath.Join(dir, rel)
		}
		if filepath.Dir(dir) == dir {
			t.Fatalf("%s not found", rel)
		}
		dir = filepath.Dir(dir)
	}
}

func knowledgeItems(t *testing.T, r reply) []map[string]any {
	t.Helper()
	var doc struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(r.body, &doc); err != nil {
		t.Fatalf("items body: %v\n%s", err, clip(r.body))
	}
	return doc.Items
}

func itemHas(t *testing.T, items []map[string]any, id string) map[string]any {
	t.Helper()
	for _, it := range items {
		if it["id"] == id {
			return it
		}
	}
	return nil
}

// freshKeyPair returns two unused human keys low < high whose lexicographic order disagrees with
// their numeric order (a shorter key whose text sorts after the longer one, e.g. K9 vs K10), so the
// list's numeric ordering is really exercised. The shared world persists between tests, so the pair
// is derived from the largest key in use.
func freshKeyPair(t *testing.T) (low, high string) {
	t.Helper()
	max := scalar(t, `SELECT coalesce(max((regexp_replace(key, '^K', ''))::int), 0) FROM knowledge`)
	n, err := strconv.Atoi(max)
	if err != nil {
		t.Fatal(err)
	}
	a := n + 1
	b := int(math.Pow10(len(strconv.Itoa(a)))) // the next power of ten: one digit longer
	if a*10 == b {
		a++ // a itself a power of ten sorts like its prefix; nudge it so the texts disagree
	}
	return "K" + strconv.Itoa(a), "K" + strconv.Itoa(b)
}

// TestKnowledgeEndpointsConformToTheContract walks the WP24 (HAR-122) read paths over HTTP: the list
// with its status filter and limit, and the detail with its not-found and auth failures. Every
// response is checked against core.yaml (which references knowledge.v1.json) by stack.do.
func TestKnowledgeEndpointsConformToTheContract(t *testing.T) {
	s := newStack(t)
	list, detail := "/knowledge", "/knowledge/{knowledge_id}"

	// The shorter key sorts after the longer one lexically; numerically it comes first.
	lowKey, highKey := freshKeyPair(t)
	low := seedKnowledge(t, lowKey, "Keep the champion in the thread when a new stakeholder enters")
	high := seedSupportedKnowledge(t, highKey, "Send a stage-advance recap to the buying group, not one person")
	if low.status != "candidate" || high.status != "supported" {
		t.Fatalf("seeded statuses %q %q", low.status, high.status)
	}

	r := s.get(list, list)
	if r.status != 200 {
		t.Fatalf("list: %d %s", r.status, clip(r.body))
	}
	items := knowledgeItems(t, r)
	lowItem, highItem := itemHas(t, items, low.id), itemHas(t, items, high.id)
	if lowItem == nil || highItem == nil {
		t.Fatalf("seeded knowledge missing from %d items:\n%s", len(items), clip(r.body))
	}
	lowIdx, highIdx := -1, -1
	for i, it := range items {
		switch it["id"] {
		case low.id:
			lowIdx = i
		case high.id:
			highIdx = i
		}
	}
	if lowIdx > highIdx {
		t.Fatalf("K%s must sort before K%s, got positions %d and %d", low.key, high.key, lowIdx, highIdx)
	}
	counts, ok := highItem["counts"].(map[string]any)
	if !ok || counts["decisions"] != float64(6) || counts["positive_reactions"] != float64(3) {
		t.Fatalf("supported counts = %v", highItem["counts"])
	}
	if episodes, _ := highItem["supporting_decision_episode_ids"].([]any); len(episodes) != 6 {
		t.Fatalf("supporting episodes = %v", highItem["supporting_decision_episode_ids"])
	}

	// status filter: supported keeps only the supported rung; candidate keeps only candidates.
	for _, it := range knowledgeItems(t, s.get(list+"?status=supported", list)) {
		if it["status"] != "supported" {
			t.Fatalf("status=supported returned %v", it["status"])
		}
	}
	for _, it := range knowledgeItems(t, s.get(list+"?status=candidate", list)) {
		if it["status"] != "candidate" {
			t.Fatalf("status=candidate returned %v", it["status"])
		}
	}
	if itemHas(t, knowledgeItems(t, s.get(list+"?status=supported", list)), low.id) != nil {
		t.Fatal("a candidate leaked into status=supported")
	}
	s.expectError(s.get(list+"?status=bogus", list), 400, "bad_request")
	s.expectError(s.get(list+"?limit=0", list), 400, "bad_request")

	// Detail: the seeded object, a missing id, a malformed id, auth and the method guard.
	d := s.get("/knowledge/"+high.id, detail)
	if d.status != 200 {
		t.Fatalf("detail: %d %s", d.status, clip(d.body))
	}
	var doc map[string]any
	if err := json.Unmarshal(d.body, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["id"] != high.id || doc["key"] != high.key || doc["status"] != "supported" {
		t.Fatalf("detail doc = %v %v %v", doc["id"], doc["key"], doc["status"])
	}
	history, _ := doc["status_history"].([]any)
	if len(history) < 2 { // insert + promotion
		t.Fatalf("status_history = %v", doc["status_history"])
	}
	s.expectError(s.get("/knowledge/"+missingID, detail), 404, "not_found")
	s.expectError(s.get("/knowledge/not-a-uuid", detail), 404, "not_found")
	if r := s.do("GET", "/knowledge", list, "", nil); r.status != 401 {
		t.Fatalf("no token: %d", r.status)
	}
	if r := s.do("POST", "/knowledge", "", apiToken, []byte(`{}`)); r.status != 405 {
		t.Fatalf("POST /knowledge: %d", r.status)
	}
}
