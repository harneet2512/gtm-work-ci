package controlplane

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/evalarea"
	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
)

// CompareSide is eval_run_comparison.v1.json side: the EvalRun a column of the comparison reads.
type CompareSide struct {
	EvalRunID         string    `json:"eval_run_id"`
	DecisionEpisodeID *string   `json:"decision_episode_id"`
	AccountID         string    `json:"account_id"`
	AccountName       string    `json:"account_name"`
	EvaluatedAt       time.Time `json:"evaluated_at"`
	Counts            Counts    `json:"counts"`
}

// CompareRow compares one eval type across the two runs. A and B are the side's worst verdict (nil: not checked).
type CompareRow struct {
	EvalType   string   `json:"eval_type"`
	FamilyID   string   `json:"family_id"`
	Area       string   `json:"area"`
	A          *string  `json:"a"`
	B          *string  `json:"b"`
	ABlocking  bool     `json:"a_blocking"`
	BBlocking  bool     `json:"b_blocking"`
	Change     string   `json:"change"`
	AResultIDs []string `json:"a_result_ids"`
	BResultIDs []string `json:"b_result_ids"`
}

// Overall is the whole-episode row: how many rows moved which way, and B's counts minus A's.
type Overall struct {
	Change    string `json:"change"`
	Improved  int    `json:"improved"`
	Regressed int    `json:"regressed"`
	Unchanged int    `json:"unchanged"`
	Added     int    `json:"added"`
	Removed   int    `json:"removed"`
	// Inconclusive counts rows where one side was unknown and the other was not.
	Inconclusive int `json:"inconclusive"`
	// AddedFail counts newly checked eval types whose verdict is fail; it makes the overall change regressed.
	AddedFail int    `json:"added_fail"`
	Delta     Counts `json:"delta"`
}

// Comparison is eval_run_comparison.v1.json.
type Comparison struct {
	A       CompareSide  `json:"a"`
	B       CompareSide  `json:"b"`
	Rows    []CompareRow `json:"rows"`
	Overall Overall      `json:"overall"`
}

// ErrNotComparable: the two runs were not triggered by the same activities, so one is not a re-run of the other.
var ErrNotComparable = errors.New("controlplane: the runs are not re-runs of the same trigger")

// CompareEvalRuns compares EvalRun a with EvalRun b. ErrInvalid: a malformed id. ErrNotFound: either run is unknown or
// has no EvalResults. ErrNotComparable: the runs do not share their trigger activities (another event, or another
// account): a comparison is the before/after of one trigger, and cross-account comparison is out of scope.
func (r *Reader) CompareEvalRuns(ctx context.Context, a, b string) (Comparison, error) {
	if !readmodel.ValidUUID(a) || !readmodel.ValidUUID(b) {
		return Comparison{}, fmt.Errorf("a and b must be run uuids: %w", readmodel.ErrInvalid)
	}
	var out Comparison
	err := r.snapshot(ctx, func(db claimstore.DB) error {
		runA, err := oneEvalRun(ctx, db, a)
		if err != nil {
			return err
		}
		runB, err := oneEvalRun(ctx, db, b)
		if err != nil {
			return err
		}
		if err := requireSameTrigger(ctx, db, a, b); err != nil {
			return err
		}
		results, err := loadResults(ctx, db, []string{a, b})
		if err != nil {
			return err
		}
		out = compare(runA, runB, results[a], results[b])
		return nil
	})
	return out, err
}

// requireSameTrigger is nil when both runs were triggered by the same, non-empty set of activities.
func requireSameTrigger(ctx context.Context, db claimstore.DB, a, b string) error {
	var same bool
	err := db.QueryRowContext(ctx, `SELECT coalesce(cardinality(ra.trigger_activity_ids) > 0
   AND (SELECT array_agg(x ORDER BY x) FROM unnest(ra.trigger_activity_ids) x) = (SELECT array_agg(x ORDER BY x) FROM unnest(rb.trigger_activity_ids) x), false)
 FROM agent_runs ra, agent_runs rb WHERE ra.id = $1::uuid AND rb.id = $2::uuid`, a, b).Scan(&same)
	if err != nil {
		return fmt.Errorf("controlplane: compare triggers: %w", err)
	}
	if !same {
		return ErrNotComparable
	}
	return nil
}

func sideOf(run EvalRun) CompareSide {
	return CompareSide{EvalRunID: run.ID, DecisionEpisodeID: run.DecisionEpisodeID, AccountID: run.AccountID,
		AccountName: run.AccountName, EvaluatedAt: run.EvaluatedAt, Counts: run.Counts}
}

// compare builds the per-eval-type rows and the overall row from the two runs' results.
func compare(runA, runB EvalRun, ra, rb []result) Comparison {
	var types []string
	for _, r := range append(slices.Clone(ra), rb...) {
		if !slices.Contains(types, r.EvalType) {
			types = append(types, r.EvalType)
		}
	}
	slices.SortFunc(types, compareTypeOrder)
	cmp := Comparison{A: sideOf(runA), B: sideOf(runB), Rows: make([]CompareRow, 0, len(types))}
	for _, t := range types {
		row := compareRow(t, ofType(ra, t), ofType(rb, t))
		cmp.Rows = append(cmp.Rows, row)
		switch row.Change {
		case changeImproved:
			cmp.Overall.Improved++
		case changeRegressed:
			cmp.Overall.Regressed++
		case changeInconclusive:
			cmp.Overall.Inconclusive++
		case changeAdded:
			cmp.Overall.Added++
			if row.B != nil && *row.B == vFail {
				cmp.Overall.AddedFail++
			}
		case changeRemoved:
			cmp.Overall.Removed++
		default:
			cmp.Overall.Unchanged++
		}
	}
	cmp.Overall.Change = overallChange(cmp.Overall)
	cmp.Overall.Delta = runB.Counts.Minus(runA.Counts)
	return cmp
}

func compareRow(evalType string, a, b []result) CompareRow {
	va, blockA, idsA := worstOf(a)
	vb, blockB, idsB := worstOf(b)
	family, _ := evalarea.FamilyOf(evalType)
	area, _ := evalarea.AreaOf(evalType)
	return CompareRow{
		EvalType: evalType, FamilyID: family, Area: string(area), A: verdictPtr(va), B: verdictPtr(vb),
		ABlocking: blockA, BBlocking: blockB, Change: changeOf(va, vb), AResultIDs: idsA, BResultIDs: idsB,
	}
}

func verdictPtr(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

// overallChange: a regression, or an eval type that is newly checked and fails, makes the episode regressed; else any
// improvement makes it improved; else any row that could not be judged makes it inconclusive.
func overallChange(o Overall) string {
	switch {
	case o.Regressed > 0 || o.AddedFail > 0:
		return changeRegressed
	case o.Improved > 0:
		return changeImproved
	case o.Inconclusive > 0:
		return changeInconclusive
	default:
		return changeUnchanged
	}
}

// typePosition orders eval types by area, then family (registry order inside the area); unmapped types go last.
func typePosition(evalType string) (area, family int) {
	f, ok := evalarea.FamilyOf(evalType)
	if !ok {
		return 99, 99
	}
	for _, info := range evalarea.Areas() {
		if i := slices.Index(info.Families, f); i >= 0 {
			return info.Order, i
		}
	}
	return 99, 99
}

func compareTypeOrder(x, y string) int {
	ax, fx := typePosition(x)
	ay, fy := typePosition(y)
	switch {
	case ax != ay:
		return ax - ay
	case fx != fy:
		return fx - fy
	default:
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
		return 0
	}
}
