package store_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// JSON <-> SQL parity for pipeline_progress.v1.json (HAR-145): the stage, status and failure_kind vocabularies the
// contract publishes are exactly the ones the pipeline_stage_events CHECKs accept. `waiting` is the one contract
// status with no row (a stage that has not run is never stored).

type progressContract struct {
	Defs struct {
		StageID     struct{ Enum []string } `json:"stageId"`
		StageStatus struct{ Enum []string } `json:"stageStatus"`
		FailureKind struct{ Enum []string } `json:"failureKind"`
	} `json:"$defs"`
}

func loadProgressContract(t *testing.T) progressContract {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		raw, err := os.ReadFile(filepath.Join(dir, "contracts", "schemas", "pipeline_progress.v1.json"))
		if err == nil {
			var c progressContract
			if err := json.Unmarshal(raw, &c); err != nil {
				t.Fatal(err)
			}
			return c
		}
		if filepath.Dir(dir) == dir {
			t.Fatal("contracts/schemas/pipeline_progress.v1.json not found")
		}
		dir = filepath.Dir(dir)
	}
}

var quoted = regexp.MustCompile(`'([a-z_]+)'`)

// checkValues returns the literals of the table's `column IN (...)` CHECK.
func checkValues(t *testing.T, column string) []string {
	t.Helper()
	rows, err := env.DB.Query(`SELECT pg_get_constraintdef(oid) FROM pg_constraint
 WHERE conrelid = 'pipeline_stage_events'::regclass AND contype = 'c'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	prefix := "CHECK ((" + column + " = ANY"
	for rows.Next() {
		var def string
		if err := rows.Scan(&def); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(def, prefix) {
			continue
		}
		var out []string
		for _, m := range quoted.FindAllStringSubmatch(def, -1) {
			out = append(out, m[1])
		}
		sort.Strings(out)
		return out
	}
	t.Fatalf("no CHECK (%s IN (...)) on pipeline_stage_events", column)
	return nil
}

func sorted(in []string, drop string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s != drop {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func TestPipelineProgressVocabulariesMatchTheStageEventChecks(t *testing.T) {
	c := loadProgressContract(t)
	for _, tc := range []struct {
		column string
		want   []string
	}{
		{"stage", sorted(c.Defs.StageID.Enum, "")},
		{"status", sorted(c.Defs.StageStatus.Enum, "waiting")},
		{"failure_kind", sorted(c.Defs.FailureKind.Enum, "")},
	} {
		if len(tc.want) < 3 {
			t.Fatalf("%s: the contract lost values: %v", tc.column, tc.want)
		}
		if got := checkValues(t, tc.column); strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s: SQL CHECK %v != contract %v", tc.column, got, tc.want)
		}
	}
}
