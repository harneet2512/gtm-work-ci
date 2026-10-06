package evalreport

// types.go — the value types of the per-metric documents (the envelope, Report and the option/selection
// types stay in evalreport.go).

import "time"

// Agreement is the version's verdict on the reviewed artifact vs the human's action, per decided
// episode the version judged. Categories:
//   - true_pass: pass + APPROVE_UNCHANGED (the accept-without-edit leg)
//   - true_block_reject: fail + REJECT
//   - true_block_repaired: fail + an edit whose delta's explanations cite this version
//   - pass_other_axis: pass + an edit the axis map does not attribute to this evaluator (UNSCORED: it
//     confirms nothing about a judge that was never responsible for the edit)
//   - abstain / warn: the version had no pass/fail row on the episode (separate buckets; abstain_rate and
//     worst_case_rate count abstention instead of letting it vanish from the rates)
//   - pass_rejected: pass + REJECT (the eval passed what the human rejected for an unrecoverable reason)
//   - false_pass: pass + an edit attributed to this axis (see FalsePass.Basis)
//   - false_block: fail + APPROVE_UNCHANGED
//   - fail_unresolved_edit: fail + an edit not explained by this version (the flag was not what the
//     human fixed — neither confirmed nor refuted, so unscored like warn/abstain and IGNORE)
type Agreement struct {
	EpisodesEvaluated int            `json:"n_episodes"`
	Categories        map[string]int `json:"categories"`
	Consistent        int            `json:"consistent"`
	Contradicted      int            `json:"contradicted"`
	Unscored          int            `json:"unscored"`
	Abstained         int            `json:"abstained"`                 // the version abstained (E19.5) — its own bucket
	Warned            int            `json:"warned"`                    // the version only warned — its own bucket
	Rate              *float64       `json:"rate,omitempty"`            // consistent / (consistent + contradicted)
	AbstainRate       *float64       `json:"abstain_rate,omitempty"`    // abstained / n_episodes
	WorstCaseRate     *float64       `json:"worst_case_rate,omitempty"` // consistent / (consistent + contradicted + abstained)
	AcceptUnchanged   int            `json:"accept_unchanged"`
	AcceptRate        *float64       `json:"accept_unchanged_rate,omitempty"` // accept_unchanged / n_episodes
}

// FalsePass measures the version passing a draft the human substantively edited on its axis.
type FalsePass struct {
	AttributedEdits int      `json:"n_axis_edits_evaluated"` // edited episodes attributed to the axis the version judged
	FalsePass       int      `json:"false_pass"`
	TrueDetection   int      `json:"true_detection"` // attributed edits explained by this version's own failure
	FailNotCited    int      `json:"fail_not_cited"` // the version failed the draft but the explanation cited another eval
	Rate            *float64 `json:"rate,omitempty"` // false_pass / n_axis_edits_evaluated
	Basis           string   `json:"basis"`
}

// FalseBlock measures the version failing a draft the human approved unchanged. The headline rate is
// the §22 false-block rate: of the unchanged approvals the version judged (pass or fail), the share it
// blocked — so a fail-everything evaluator scores 1.0. fail_precision keeps the original denominator
// (every fail verdict) as a separate number: the share of the version's fail verdicts that landed on an
// unchanged approval (its false-blocks per flag raised; lower is better, despite the name's history —
// the field is kept verbatim from the first report so existing readers do not break).
type FalseBlock struct {
	UnchangedApprovals int      `json:"n_unchanged_approvals"`    // unchanged approvals the version judged (pass or fail)
	FailVerdicts       int      `json:"n_fail_verdicts"`          // decided episodes where the version's reviewed-draft verdict is fail
	FalseBlock         int      `json:"false_block"`              // of those, APPROVE_UNCHANGED
	FailUnresolved     int      `json:"fail_unresolved_edit"`     // of those, edits the delta's explanations do not cite this version for
	Rate               *float64 `json:"rate,omitempty"`           // false_block / n_unchanged_approvals
	FailPrecision      *float64 `json:"fail_precision,omitempty"` // false_block / n_fail_verdicts
}

// Calibration buckets eval_runs.confidence against human-confirmed correctness of the same verdict.
type Calibration struct {
	Rows    int      `json:"n_confidence_rows"`
	Scored  int      `json:"n_scored_rows"` // rows carrying score but no confidence (informational only)
	Buckets []Bucket `json:"buckets"`
	// Rejections scopes the REJECT rows: a rejection confirms or refutes an axis only when its stated
	// reason (or its delta) points at that axis; every other rejected row is dropped.
	Rejections RejectionScope `json:"rejections"`
	ECE        *float64       `json:"ece,omitempty"` // Σ bucket weight * |mean confidence − observed correct|
	Basis      string         `json:"basis"`
}

// RejectionScope counts confidence rows on rejected episodes by whether the rejection points at the axis.
type RejectionScope struct {
	AxisScoped int `json:"axis_scoped"` // reason/delta names this axis: scored
	Unscoped   int `json:"unscoped"`    // no stated reason, or another axis: dropped
}

// Bucket is one calibration bin.
type Bucket struct {
	Range    string  `json:"range"`
	N        int     `json:"n"`
	Mean     float64 `json:"mean_confidence"`
	Observed float64 `json:"observed_correct"`
}

// Consistency reports whether the same input reproduces the same verdict.
type Consistency struct {
	RepeatedGroups      int      `json:"n_repeated_groups"`       // same-artifact re-judgments (unchanged sends)
	SkippedInputChanged int      `json:"n_skipped_input_changed"` // repeats after an edit — detection, not instability
	Agreeing            int      `json:"n_agreeing"`              // groups whose verdicts are all identical
	Rate                *float64 `json:"rate,omitempty"`
	Replay              *Replay  `json:"spec_replay,omitempty"`
	ReplayNote          string   `json:"spec_replay_note,omitempty"` // why spec_replay is absent (malformed shadow_spec)
	Basis               string   `json:"basis"`
}

// Replay is the determinism leg for versions with an executable shadow_spec: each stored kind
// 'human_delta' result is re-derived by running the spec on the judged artifact reconstructed from
// the stored rows (candidate or final draft), and compared with the stored verdict.
type Replay struct {
	Replayable   int              `json:"n_replayable"` // rows whose judged draft the stored rows can rebuild (excluded rows not counted)
	Matching     int              `json:"n_matching"`
	Rate         *float64         `json:"rate,omitempty"`
	Excluded     int              `json:"n_excluded"`
	ExcludedRows []ExcludedReplay `json:"excluded,omitempty"`
}

// ExcludedReplay is a stored result the replay cannot fairly re-derive, with the reason.
type ExcludedReplay struct {
	RowID  string `json:"eval_run_id"`
	Reason string `json:"reason"`
}

// Coverage is the corpus-level coverage of human correction categories plus the version's slice.
type Coverage struct {
	Labels        []LabelCoverage `json:"semantic_labels"`
	Unobserved    []string        `json:"unobserved_labels"`
	SuggestedAxes []AxisCoverage  `json:"suggested_eval_types"`
	ThisVersion   VersionCoverage `json:"this_version"`
	Basis         string          `json:"basis"`
	Limits        []string        `json:"limits"` // what the metric cannot see (inherited from the delta capture)
}

// LabelCoverage is one HumanDelta semantic label's footprint.
type LabelCoverage struct {
	Label       string `json:"label"`
	Deltas      int    `json:"deltas"`
	Unexplained int    `json:"unexplained"`
	Seeded      int    `json:"seeded_candidates"`
	Covered     bool   `json:"covered"` // an existing eval explained it or a candidate version was seeded
}

// AxisCoverage is one suggested_eval_type's footprint.
type AxisCoverage struct {
	Axis   string `json:"axis"`
	Deltas int    `json:"deltas"`
	Seeded int    `json:"versions_seeded"`
}

// VersionCoverage is this version's slice of the coverage metric.
type VersionCoverage struct {
	AxisDeltas   int  `json:"axis_deltas"`            // deltas attributed to this evaluator
	SeededByThis bool `json:"seeded_by_this_version"` // this version was seeded by an unexplained delta
}

// Explained is the share of human edits the existing evals predicted (unexplained=false).
type Explained struct {
	Deltas          int      `json:"n_deltas"`
	ExplainedDeltas int      `json:"n_explained"`
	Rate            *float64 `json:"rate,omitempty"`
	ByThisVersion   int      `json:"n_explained_by_version"` // deltas whose explanations cite this version's results
	ByThisAxis      int      `json:"n_explained_by_axis"`    // deltas whose explanations cite any version of the evaluator
	AxisAttributed  int      `json:"n_axis_attributed"`      // deltas the axis map attributes to this evaluator
}

// Discovery is the candidate-criterion discovery rate of the corpus plus this version's lineage.
type Discovery struct {
	Episodes    int      `json:"n_episodes"` // decided episodes (the denominator that could produce deltas)
	Unexplained int      `json:"n_unexplained_deltas"`
	Seeded      int      `json:"n_candidates_seeded"`              // evaluator_versions rows seeded by a delta or a verdict
	Yield       *float64 `json:"unexplained_yield,omitempty"`      // seeded-source deltas / unexplained deltas
	PerEpisodes *float64 `json:"candidates_per_episode,omitempty"` // seeded / decided episodes
	ThisSeeded  bool     `json:"this_version_seeded"`
}

// RepeatCorrection is the headline §22 metric: how often the same correction repeats before vs after
// the version's activation (promoted_at).
type RepeatCorrection struct {
	PromotedAt   time.Time  `json:"promoted_at"`
	InForceUntil *time.Time `json:"in_force_until,omitempty"` // retirement / supersession; absent = still in force
	Method       string     `json:"method"`                   // "spec_replay" | "label_match" (spec cannot execute)
	Scope        string     `json:"scope"`                    // "account <id>" | "global"
	SeedExcluded bool       `json:"seed_episode_excluded"`    // the version's own seeding episode is not a repeat of itself
	Basis        string     `json:"basis"`
	Before       Window     `json:"before"`
	After        Window     `json:"after"`
	AxisFail     AxisFail   `json:"axis_fail_rate"`
}

// Window is one side of the activation split.
type Window struct {
	Episodes        int      `json:"n_episodes"`   // in-scope, in-force episodes (after: the version ran on them)
	Unscorable      int      `json:"n_unscorable"` // episodes whose candidate/final drafts cannot be rebuilt from stored rows
	Deltas          int      `json:"n_deltas"`
	Matching        int      `json:"n_matching_deltas"`
	Rate            *float64 `json:"delta_rate,omitempty"` // matching / deltas
	MatchingEpisode int      `json:"n_matching_episodes"`
	EpisodeRate     *float64 `json:"episode_rate,omitempty"` // matching episodes / episodes
}

// AxisFail is the share of the version's own results still failing before vs after activation (the
// same correction still needed, per evaluated draft).
type AxisFail struct {
	BeforeN    int      `json:"before_n"`
	BeforeFail int      `json:"before_fail"`
	BeforeRate *float64 `json:"before_rate,omitempty"`
	AfterN     int      `json:"after_n"`
	AfterFail  int      `json:"after_fail"`
	AfterRate  *float64 `json:"after_rate,omitempty"`
}

// TrackedGap is a §22 / E19-E22 measurement the report does not (yet) make, stated explicitly so its
// absence cannot be read as a clean result.
type TrackedGap struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"`  // "open"
	Reason  string `json:"reason"`  // why it is not measured
	Interim string `json:"interim"` // what the report offers instead
}

// AttributionCoverage reports how reachable the version's axis is for false-pass measurement.
type AttributionCoverage struct {
	Axis            string   `json:"axis"`
	Attributable    bool     `json:"attributable"`       // a change kind or a corpus suggested_eval_type reaches the axis
	ChangeKinds     []string `json:"change_kinds"`       // literal-change kinds that attribute an edit to the axis
	SuggestedDeltas int      `json:"suggested_deltas"`   // corpus deltas whose suggested_eval_type is the axis
	EditedEpisodes  int      `json:"n_edited_episodes"`  // edited decided episodes the version judged
	AttributedEdits int      `json:"n_attributed_edits"` // of those, attributed to the axis
	Share           *float64 `json:"attribution_share,omitempty"`
	Basis           string   `json:"basis"`
}
