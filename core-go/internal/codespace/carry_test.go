package codespace

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledgestore"
	"github.com/harneet2512/gtm-work/core-go/internal/store"
	"github.com/harneet2512/gtm-work/core-go/internal/store/embedded"
)

func repoRules(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		p := filepath.Join(dir, "contracts", "knowledge", "lifecycle.v1.json")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		if filepath.Dir(dir) == dir {
			t.Fatal("lifecycle rules not found")
		}
		dir = filepath.Dir(dir)
	}
}

// TestCarryMovesKnowledgeBetweenCaseDatabasesAsFreshCandidatesAndIsIdempotent runs on a real Postgres with two migrated
// databases: what case 1 learned appears in case 2 as a candidate created at case 1's clock, never already promoted.
func TestCarryMovesKnowledgeBetweenCaseDatabasesAsFreshCandidatesAndIsIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a Postgres")
	}
	ctx := context.Background()
	dsn, stop, err := embedded.Launch()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stop() }) // also runs after t.Fatal and a failed subtest
	admin := PGAdmin{DSN: dsn}
	cases := DefaultCases()
	dsns := map[string]string{}
	dbs := map[string]*sql.DB{}
	for _, c := range cases {
		if err := admin.Create(ctx, c.Database); err != nil {
			t.Fatal(err)
		}
		d, _ := DSNFor(dsn, c.Database)
		dsns[c.Slot] = d
		if err := migrateForTest(ctx, d); err != nil {
			t.Fatal(err)
		}
		db, err := store.Open(ctx, d)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		dbs[c.Slot] = db
	}
	learnedAt := time.Date(2023, 11, 9, 12, 0, 0, 0, time.UTC)
	kn := knowledge.Knowledge{
		ID: "00000000-0000-4000-8000-000000000a01", Title: "Keep the active champion involved when a new stakeholder enters during expansion",
		SituationSignature:      []knowledge.Condition{{Field: "motion", Op: "eq", Value: "expansion"}, {Field: "diff.new_stakeholder_entered", Op: "exists"}},
		ApplicabilityConditions: []knowledge.Condition{{Field: "transition.status", Op: "in", Value: []any{"CANDIDATE"}}},
		Guidance:                knowledge.Guidance{Summary: "Include the champion", Do: []string{"cc the champion"}, Dont: []string{}},
		Exceptions: []knowledge.Exception{{Description: "delegated",
			Conditions: []knowledge.Condition{{Field: "champion_status", Op: "eq", Value: "delegated"}}}},
		EvidenceClasses: []string{"methodology"}, UsedByEvaluators: []string{"champion_continuity:v1"},
		Provenance: knowledge.Provenance{CreatedFrom: "human_delta"}, CreatedAt: learnedAt,
	}
	tx, err := dbs[SlotCase1].BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := knowledgestore.InsertReplay(ctx, tx, kn, "learned in case 1"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// a second entry learned later in case 1: each keeps its OWN replay time through the carry
	later := kn
	later.ID, later.Title, later.CreatedAt = "00000000-0000-4000-8000-000000000a02", "Confirm timing before proposing a call", learnedAt.Add(72*time.Hour)
	tx2, err := dbs[SlotCase1].BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := knowledgestore.InsertReplay(ctx, tx2, later, "learned later in case 1"); err != nil {
		t.Fatal(err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatal(err)
	}

	carrier := KnowledgeCarrier{DSNFor: func(c Case) (string, error) { return dsns[c.Slot], nil }, RulesPath: repoRules(t)}
	n, err := carrier.Carry(ctx, cases[0], cases[1])
	if err != nil || n != 2 {
		t.Fatalf("Carry = %d, %v", n, err)
	}
	got, err := knowledgestore.Get(ctx, dbs[SlotCase2], kn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != knowledge.StatusCandidate || !got.CreatedAt.Equal(learnedAt) {
		t.Fatalf("carried = status %s created %s; want a candidate at %s", got.Status, got.CreatedAt, learnedAt)
	}
	gotLater, err := knowledgestore.Get(ctx, dbs[SlotCase2], later.ID)
	if err != nil || !gotLater.CreatedAt.Equal(later.CreatedAt) {
		t.Fatalf("each entry keeps its own replay time: %v %v", gotLater.CreatedAt, err)
	}
	probe := DBProbe{DSNFor: carrier.DSNFor}
	formed, err := probe.Formed(ctx, cases[1])
	if err != nil || len(formed) != 2 || formed[0].ID != kn.ID || !formed[0].CreatedAt.Equal(learnedAt) || !formed[1].CreatedAt.Equal(later.CreatedAt) {
		t.Fatalf("the probe must see the carried entry at the replay clock: %+v %v", formed, err)
	}
	missing := "00000000-0000-4000-8000-0000000000ff"
	_, err = probe.Retrieved(ctx, cases[1], missing)
	if err == nil || !strings.Contains(err.Error(), "no build_context step") {
		t.Fatalf("a run that does not exist has no retrieved list (and the query must be valid SQL): %v", err)
	}
	if _, err = probe.Used(ctx, cases[1], missing); err == nil || !strings.Contains(err.Error(), "no build_context step") {
		t.Fatalf("used: %v", err)
	}
	if _, err = probe.ReplayClock(ctx, cases[1], missing); err == nil || !strings.Contains(err.Error(), "no build_context step") {
		t.Fatalf("replay clock: %v", err)
	}
	if n, err := carrier.Carry(ctx, cases[0], cases[1]); err != nil || n != 0 {
		t.Fatalf("a second carry must write nothing: %d %v", n, err)
	}
	// Nothing learned yet: nothing to carry, and no error.
	empty, err := carrier.Carry(ctx, cases[1], cases[0])
	if err != nil || empty != 1 && empty != 0 {
		t.Fatalf("reverse carry = %d %v", empty, err)
	}
	bad := KnowledgeCarrier{DSNFor: carrier.DSNFor, RulesPath: filepath.Join(t.TempDir(), "missing.json")}
	if _, err := bad.Carry(ctx, cases[0], cases[1]); err == nil {
		t.Fatal("missing rules must be an error")
	}
}

func migrateForTest(ctx context.Context, dsn string) error {
	db, err := store.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	m, err := store.NewMigrator(db)
	if err != nil {
		return err
	}
	return m.Up(ctx)
}
