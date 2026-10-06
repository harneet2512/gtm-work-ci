package knowledgestore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

var env *storetest.Env

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e }))
}

var (
	ctx = context.Background()
	t0  = time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
)

func uuid(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }

// neutral is a minimal knowledge object (no fixture account): keep the champion when a stakeholder enters.
func neutral(id string) knowledge.Knowledge {
	return knowledge.Knowledge{
		ID: id, Title: "Keep the active champion involved when a new stakeholder enters during expansion",
		SituationSignature: []knowledge.Condition{{Field: "motion", Op: "eq", Value: "expansion"},
			{Field: "diff.new_stakeholder_entered", Op: "exists"}},
		ApplicabilityConditions: []knowledge.Condition{{Field: "transition.status", Op: "in", Value: []any{"CANDIDATE"}}},
		Guidance:                knowledge.Guidance{Summary: "Include the champion", Do: []string{"cc the champion"}, Dont: []string{}},
		Exceptions: []knowledge.Exception{{Description: "delegated",
			Conditions: []knowledge.Condition{{Field: "champion_status", Op: "eq", Value: "delegated"}}}},
		EvidenceClasses: []string{"methodology"}, UsedByEvaluators: []string{"champion_continuity:v1"},
		Provenance: knowledge.Provenance{CreatedFrom: "human_delta"}, CreatedAt: t0,
	}
}

func rules(t *testing.T) knowledge.Rules {
	t.Helper()
	r, err := knowledge.LoadRules(repoFile(t, "contracts/knowledge/lifecycle.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

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

func insert(t *testing.T, tx *sql.Tx, k knowledge.Knowledge) knowledge.Knowledge {
	t.Helper()
	got, err := Insert(ctx, tx, k, "created from a human delta")
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestInsertAndGetRoundTrip(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		got := insert(t, tx, neutral(uuid(1)))
		if got.Status != knowledge.StatusCandidate || got.Key == nil || *got.Key == "" {
			t.Fatalf("got %+v", got)
		}
		if len(got.ApplicabilityConditions) != 1 || got.ApplicabilityConditions[0].Value.([]any)[0] != "CANDIDATE" {
			t.Fatalf("applicability conditions = %+v", got.ApplicabilityConditions)
		}
		if got.EvidenceClasses[0] != "methodology" || got.UsedByEvaluators[0] != "champion_continuity:v1" || !got.CreatedAt.Equal(t0) {
			t.Fatalf("got %+v", got)
		}
		if len(got.StatusHistory) != 1 || got.StatusHistory[0].FromStatus != nil || got.StatusHistory[0].Reason != "created from a human delta" {
			t.Fatalf("history = %+v", got.StatusHistory)
		}
		if _, err := knowledge.Match(got, knowledge.Situation{}); err != nil {
			t.Fatalf("stored knowledge must stay matchable: %v", err)
		}
		k17 := neutral(uuid(2))
		key := "K917"
		k17.Key, k17.CreatedAt = &key, time.Time{}
		if got := insert(t, tx, k17); *got.Key != "K917" || got.CreatedAt.IsZero() {
			t.Fatalf("explicit key / db clock: %+v", got)
		}
	})
}

func TestInsertRejectsInvalidKnowledgeAndGetReportsMissing(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		bad := neutral(uuid(3))
		bad.SituationSignature = []knowledge.Condition{{Field: "mood", Op: "eq", Value: "x"}}
		if _, err := Insert(ctx, tx, bad, "x"); !errors.Is(err, knowledge.ErrInvalidCondition) {
			t.Fatalf("err = %v", err)
		}
		if _, err := Get(ctx, tx, uuid(4)); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
	})
}

// HAR-118 seeded-history shape: 8 decisions, 6 positive reactions, 3 advanced outcomes, 1 counterexample.
func TestRecordEvidenceDrivesTheLifecycleWithHistory(t *testing.T) {
	r := rules(t)
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		id := insert(t, tx, neutral(uuid(10))).ID
		n := 100
		record := func(kind, detail string) *knowledge.Change {
			n++
			ev := knowledge.Evidence{Kind: kind, RefID: uuid(n), At: t0.Add(time.Duration(n) * time.Hour)}
			ev.Polarity, ev.OutcomeType, ev.Note = detail, detail, detail
			_, ch, err := RecordEvidence(ctx, tx, id, ev, r)
			if err != nil {
				t.Fatalf("%s: %v", kind, err)
			}
			return ch
		}
		var changes []*knowledge.Change
		for i := 0; i < 8; i++ {
			changes = append(changes, record(knowledge.EvidenceDecisionEpisode, ""))
			if i < 6 {
				changes = append(changes, record(knowledge.EvidenceCustomerReaction, "positive"))
			}
		}
		for i := 0; i < 3; i++ {
			record(knowledge.EvidenceBusinessOutcome, "stage_advanced")
		}
		record(knowledge.EvidenceCounterexample, "champion was leaving; direct handoff worked")
		got, err := Get(ctx, tx, id)
		if err != nil {
			t.Fatal(err)
		}
		want := knowledge.Counts{Decisions: 8, PositiveReactions: 6, OutcomesAdvanced: 3, Counterexamples: 1}
		if got.Status != knowledge.StatusSupported || got.Counts != want || len(got.SupportingDecisionEpisodeIDs) != 8 || len(got.Counterexamples) != 1 {
			t.Fatalf("got status %s counts %+v", got.Status, got.Counts)
		}
		assertHistory(t, got.StatusHistory)
		var support int
		if err := tx.QueryRow(`SELECT support_count FROM knowledge WHERE id = $1`, id).Scan(&support); err != nil || support != 8 {
			t.Fatalf("support_count = %d (%v)", support, err)
		}
		assertContractValid(t, got)
	})
}

func assertHistory(t *testing.T, h []knowledge.HistoryEntry) {
	t.Helper()
	if len(h) != 3 || h[1].ToStatus != knowledge.StatusProvisional || h[2].ToStatus != knowledge.StatusSupported {
		t.Fatalf("history = %+v", h)
	}
	if h[1].EvidenceKind == nil || *h[1].EvidenceKind != knowledge.EvidenceCustomerReaction || h[1].EvidenceRefID == nil {
		t.Fatalf("provisional must cite the reaction that earned it: %+v", h[1])
	}
	if *h[1].FromStatus != knowledge.StatusCandidate || h[2].Reason == "" {
		t.Fatalf("history = %+v", h)
	}
}

func TestDuplicateEvidenceIsRefusedAndTheTransactionSurvives(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		id := insert(t, tx, neutral(uuid(20))).ID
		ev := knowledge.Evidence{Kind: knowledge.EvidenceCustomerReaction, RefID: uuid(21), Polarity: "positive", At: t0}
		if _, _, err := RecordEvidence(ctx, tx, id, ev, rules(t)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := RecordEvidence(ctx, tx, id, ev, rules(t)); !errors.Is(err, ErrDuplicateEvidence) {
			t.Fatalf("err = %v", err)
		}
		got, err := Get(ctx, tx, id)
		if err != nil || got.Counts.PositiveReactions != 1 {
			t.Fatalf("counts %+v err %v", got.Counts, err)
		}
		if _, _, err := RecordEvidence(ctx, tx, uuid(29), ev, rules(t)); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing knowledge: %v", err)
		}
		bad := ev
		bad.Polarity = "ecstatic"
		if _, _, err := RecordEvidence(ctx, tx, id, bad, rules(t)); !errors.Is(err, knowledge.ErrInvalidEvidence) {
			t.Fatalf("bad evidence: %v", err)
		}
	})
}

func TestRevalidateMarksStaleAndListApplicableFilters(t *testing.T) {
	r := rules(t)
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		insert(t, tx, neutral(uuid(30)))
		for i := 0; i < 5; i++ { // earn "supported": 5 decisions, 3 positive reactions
			ev := knowledge.Evidence{Kind: knowledge.EvidenceDecisionEpisode, RefID: uuid(300 + i), At: t0}
			if i < 3 {
				react := knowledge.Evidence{Kind: knowledge.EvidenceCustomerReaction, RefID: uuid(310 + i), Polarity: "positive", At: t0}
				if _, _, err := RecordEvidence(ctx, tx, uuid(30), react, r); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := RecordEvidence(ctx, tx, uuid(30), ev, r); err != nil {
				t.Fatal(err)
			}
		}
		insert(t, tx, neutral(uuid(31)))
		list, err := ListApplicable(ctx, tx, r)
		if err != nil || len(list) != 1 || list[0].ID != uuid(30) {
			t.Fatalf("applicable = %d (%v)", len(list), err)
		}
		if _, ch, err := Revalidate(ctx, tx, uuid(30), t0.Add(24*time.Hour), r); err != nil || ch != nil {
			t.Fatalf("fresh knowledge changed: %+v %v", ch, err)
		}
		got, ch, err := Revalidate(ctx, tx, uuid(30), t0.Add(200*24*time.Hour), r)
		if err != nil || ch == nil || got.Status != knowledge.StatusStale {
			t.Fatalf("status %s change %+v err %v", got.Status, ch, err)
		}
		last := got.StatusHistory[len(got.StatusHistory)-1]
		if last.EvidenceKind != nil || last.Reason != ch.Reason {
			t.Fatalf("stale history row = %+v", last)
		}
		if list, _ := ListApplicable(ctx, tx, r); len(list) != 0 {
			t.Fatal("stale knowledge must not be applicable")
		}
		if _, _, err := Revalidate(ctx, tx, uuid(39), t0, r); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing: %v", err)
		}
	})
}

// assertContractValid checks a stored object, history included, against knowledge.v1.json.
func assertContractValid(t *testing.T, k knowledge.Knowledge) {
	t.Helper()
	c := jsonschema.NewCompiler()
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(repoFile(t, "contracts/schemas/common.v1.json")), "*.json"))
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		if err := c.AddResource(doc.(map[string]any)["$id"].(string), doc); err != nil {
			t.Fatal(err)
		}
	}
	schema, err := c.Compile("https://ghost.local/contracts/knowledge.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(k)
	inst, _ := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err := schema.Validate(inst); err != nil {
		t.Fatalf("stored knowledge violates the contract: %v\n%s", err, raw)
	}
}

// A human may create knowledge under an explicit key (K<n>) at any time. The generated keys of later
// candidates (the learning seeds insert without a key) must never collide with such a row: the key
// allocator skips every key already in use instead of assuming it owns the K<n> space.
func TestGeneratedKeyNeverCollidesWithExplicitHumanKey(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		var n int
		if err := tx.QueryRow(`SELECT nextval('knowledge_key_seq')`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		// Occupy exactly the key the sequence would hand out next, then a few after it.
		taken := map[string]bool{}
		for i := 1; i <= 3; i++ {
			key := fmt.Sprintf("K%d", n+i)
			k := neutral(uuid(70 + i))
			k.Key = &key
			insert(t, tx, k)
			taken[key] = true
		}
		got := insert(t, tx, neutral(uuid(80)))
		if got.Key == nil || taken[*got.Key] {
			t.Fatalf("generated key = %v, collides with an existing human key", got.Key)
		}
	})
}

// The column default allocates keys too (raw inserts); it must skip used keys as well.
func TestColumnDefaultKeySkipsUsedKeys(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		var n int
		if err := tx.QueryRow(`SELECT nextval('knowledge_key_seq')`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		key := fmt.Sprintf("K%d", n+1)
		if _, err := tx.Exec(`INSERT INTO knowledge (title, guidance, key) VALUES ('t','g',$1)`, key); err != nil {
			t.Fatal(err)
		}
		var got string
		if err := tx.QueryRow(`INSERT INTO knowledge (title, guidance) VALUES ('t2','g') RETURNING key`).Scan(&got); err != nil {
			t.Fatalf("default key collided: %v", err)
		}
		if got == key {
			t.Fatalf("default key = %s, already used", got)
		}
	})
}
