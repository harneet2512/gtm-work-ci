// Package evalreport computes the HAR-97 §22 "evaluation of the evals" report (WP23, HAR-121): one
// JSON document per evaluator version — every row of evaluator_versions plus every version tag
// observed on eval_runs (the shipped evals run '<type>:v1' with no registry row, so an observed-only
// tag is reported with registered=false rather than skipped).
//
// The report is read-only: it joins decision_episodes, human_decisions, eval_runs, human_deltas,
// human_delta_explanations, human_strategy_decisions, strategy_candidates and evaluator_versions —
// it never writes and never calls a model. Every metric is emitted in a uniform envelope: a computed
// metric reports its numbers; a metric whose inputs do not exist yet reports status "n/a" and the
// reason, so an empty world produces a valid document, not an error.
//
// Per-version basis: an eval_runs row belongs to a version when its evaluator_version tag equals
// '<evaluator>:v<N>'. On a decided episode the version's rows on the episode's final_draft_index are
// scored together — eval_runs.created_at is the run's replay clock, not the write time, so generation
// and send-time rows cannot be temporally ordered. Agreement therefore uses EXISTS semantics: a fail
// row cited by the delta's explanations is provably a pre-decision flag the human's edit repaired; an
// uncited fail is a flag that persisted or appeared on the final artifact; and only the total absence
// of a fail proves the version passed the reviewed draft.
package evalreport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
)

// Metric is the envelope every §22 metric reports in.
type Metric[T any] struct {
	Status string `json:"status"`           // "ok" | "n/a"
	Reason string `json:"reason,omitempty"` // why the metric is n/a
	Value  *T     `json:"value,omitempty"`
}

func avail[T any](v T) Metric[T]     { return Metric[T]{Status: "ok", Value: &v} }
func na[T any](why string) Metric[T] { return Metric[T]{Status: "n/a", Reason: why} }

// Report is one evaluator version's eval-of-evals document.
type Report struct {
	Evaluator     string              `json:"evaluator"`
	Version       int                 `json:"version"`
	Tag           string              `json:"tag"` // "<evaluator>:v<N>"
	Registered    bool                `json:"registered"`
	Status        string              `json:"status"` // lifecycle status; "observed" for tags with no evaluator_versions row
	Kind          string              `json:"kind,omitempty"`
	CreatedFrom   string              `json:"created_from,omitempty"`
	AccountID     string              `json:"account_id,omitempty"`
	PromotedAt    *time.Time          `json:"promoted_at,omitempty"`
	SourceDeltaID string              `json:"source_human_delta_id,omitempty"`
	SpecError     string              `json:"spec_error,omitempty"` // shadow_spec present but malformed: spec-dependent metrics are n/a
	Attribution   AttributionCoverage `json:"axis_attribution"`
	TrackedGaps   []TrackedGap        `json:"tracked_gaps"`
	Metrics       Metrics             `json:"metrics"`
}

// Metrics is the §22 metric set of one evaluator version. Corpus-level metrics (coverage, explained
// share, discovery) embed the corpus numbers plus this version's slice, so a single-version document
// still carries every metric.
type Metrics struct {
	HumanAgreement   Metric[Agreement]          `json:"human_agreement"`
	FalsePass        Metric[FalsePass]          `json:"false_pass_rate"`
	FalseBlock       Metric[FalseBlock]         `json:"false_block_rate"`
	Calibration      Metric[Calibration]        `json:"confidence_calibration"`
	Consistency      Metric[Consistency]        `json:"repeat_consistency"`
	Coverage         Metric[Coverage]           `json:"correction_category_coverage"`
	Explained        Metric[Explained]          `json:"edits_explained_by_evals"`
	Discovery        Metric[Discovery]          `json:"new_criterion_discovery"`
	RepeatCorrection Metric[RepeatCorrection]   `json:"repeat_semantic_correction"`
	InferenceAgree   Metric[InferenceAgreement] `json:"human_vs_inference_agreement"`
}

// Options selects which versions report.
type Options struct {
	Evaluator string // "" = every version
	Version   int    // 0 = all versions of Evaluator
	Now       time.Time
}

// ReportSet is the emitted document: one Report per selected version, keyed by '<evaluator>:v<N>'.
type ReportSet struct {
	GeneratedAt time.Time          `json:"generated_at"`
	Reports     map[string]*Report `json:"reports"`
}

// Encode is the document encoding `ghostctl learn report` emits and the committed benchmark reports
// use: two-space-indented JSON with a trailing newline.
func Encode(set ReportSet) ([]byte, error) {
	raw, err := json.MarshalIndent(set, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// txBeginner is what *sql.DB offers: the report opens its own snapshot transaction on it.
type txBeginner interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// betweenReads is a test seam: called after each table read with the number of reads completed.
var betweenReads func(step int)

// Generate computes the report. A *sql.DB (anything with BeginTx) is read inside ONE read-only
// REPEATABLE READ transaction: every table is read from the same snapshot, so a concurrent write cannot
// make the report internally inconsistent, and the read-only flag makes "never writes" a database
// guarantee rather than a convention. A caller passing a *sql.Tx supplies its own snapshot (open it
// read-only REPEATABLE READ to get the same guarantee).
func Generate(ctx context.Context, db claimstore.DB, opts Options) (ReportSet, error) {
	w, err := loadSnapshot(ctx, db)
	if err != nil {
		return ReportSet{}, err
	}
	at := opts.Now
	if at.IsZero() {
		at = time.Now()
	}
	keys, err := w.selectKeys(opts)
	if err != nil {
		return ReportSet{}, err
	}
	out := ReportSet{GeneratedAt: at.UTC(), Reports: map[string]*Report{}}
	for _, k := range keys {
		out.Reports[k.tag] = w.report(k)
	}
	return out, nil
}

// ErrUnknownSelection is returned when --evaluator/--version name nothing the database knows: a miss
// must fail loudly, because an empty document would read as a pass to a CI gate.
var ErrUnknownSelection = errors.New("evalreport: no such evaluator version")

// selectKeys enumerates evaluator_versions ∪ observed eval_runs tags, filtered by opts. A filter that
// matches nothing is an error (ErrUnknownSelection); an unfiltered selection of an empty world is a
// valid empty set.
func (w *world) selectKeys(opts Options) ([]versionKey, error) {
	if opts.Version > 0 && opts.Evaluator == "" {
		return nil, fmt.Errorf("%w: --version %d needs an evaluator", ErrUnknownSelection, opts.Version)
	}
	var out []versionKey
	axisSeen := false
	for _, k := range w.ordered {
		if opts.Evaluator != "" && k.evaluator != opts.Evaluator {
			continue
		}
		axisSeen = true
		if opts.Version > 0 && k.version != opts.Version {
			continue
		}
		out = append(out, k)
	}
	switch {
	case opts.Evaluator != "" && !axisSeen:
		return nil, fmt.Errorf("%w: %q has no registered or observed version", ErrUnknownSelection, opts.Evaluator)
	case opts.Version > 0 && len(out) == 0:
		return nil, fmt.Errorf("%w: %s:v%d is neither registered nor observed", ErrUnknownSelection,
			opts.Evaluator, opts.Version)
	}
	return out, nil
}

// versionKey is one report target.
type versionKey struct {
	evaluator string
	version   int
	tag       string
}

func keyOf(evaluator string, version int) versionKey {
	return versionKey{evaluator: evaluator, version: version,
		tag: fmt.Sprintf("%s:v%d", evaluator, version)}
}

// sortKeys orders report keys for stable output.
func sortKeys(ks []versionKey) {
	sort.Slice(ks, func(i, j int) bool {
		if ks[i].evaluator != ks[j].evaluator {
			return ks[i].evaluator < ks[j].evaluator
		}
		return ks[i].version < ks[j].version
	})
}
