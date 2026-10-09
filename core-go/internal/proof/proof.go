package proof

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Contract is the requirement this artifact proves.
const Contract = "HAR-129"

// Command is the documented command that produces a run.
const Command = "ghostctl proof-har129"

// HarnessVersion is the version of this proof harness.
const HarnessVersion = "har129-proof.v1"

// Status is the lowercase form of the HAR-129 section I.4 confirmation vocabulary.
type Status string

const (
	StatusConfirmed    Status = "confirmed"
	StatusPartial      Status = "partial"
	StatusNotConfirmed Status = "not_confirmed"
	StatusFailed       Status = "failed"
	StatusNotRun       Status = "not_run"
)

// Display renders a status in the HAR-129 section I.4 spelling for the report.
func (s Status) Display() string {
	switch s {
	case StatusConfirmed:
		return "CONFIRMED"
	case StatusPartial:
		return "PARTIAL"
	case StatusNotConfirmed:
		return "NOT CONFIRMED"
	case StatusFailed:
		return "FAILED"
	default:
		return "NOT RUN"
	}
}

// ArtifactNames is the exact, ordered list HAR-129 section H requires.
var ArtifactNames = []string{
	"manifest.json",
	"environment.json",
	"episode_timeline.json",
	"state_snapshots.json",
	"graph_diffs.json",
	"decision_episodes.json",
	"eval_bundles.json",
	"human_judgments.json",
	"customer_reactions.json",
	"knowledge_history.json",
	"knowledge_usage.json",
	"e2e-results.json",
	"demo-report.md",
}

// evidenceArtifacts is the eleven JSON files that carry the run's evidence (manifest and the report
// are the index and the human view). Each names the requirement sections it will evidence once a
// real integrated run fills it.
var evidenceArtifacts = []string{
	"environment.json",
	"episode_timeline.json",
	"state_snapshots.json",
	"graph_diffs.json",
	"decision_episodes.json",
	"eval_bundles.json",
	"human_judgments.json",
	"customer_reactions.json",
	"knowledge_history.json",
	"knowledge_usage.json",
	"e2e-results.json",
}

var artifactEvidence = map[string][]string{
	"environment.json":        {},
	"episode_timeline.json":   {"G", "S13", "S16"},
	"state_snapshots.json":    {"G", "S16", "DOD"},
	"graph_diffs.json":        {"G", "S16", "CHK"},
	"decision_episodes.json":  {"S13", "S16", "DOD", "CHK"},
	"eval_bundles.json":       {"G", "S16", "CHK"},
	"human_judgments.json":    {"G", "S13", "S16"},
	"customer_reactions.json": {"G", "DOD"},
	"knowledge_history.json":  {"G", "S16", "DOD", "CHK"},
	"knowledge_usage.json":    {"G", "S16", "SF", "CHK"},
	"e2e-results.json":        {"G", "S16", "S13", "DOD", "CHK", "BND"},
}

// RequirementSource ties the matrix to an exact revision of the source of truth.
type RequirementSource struct {
	Version           string `json:"version"`
	Path              string `json:"path"`
	RequirementsTotal int    `json:"requirements_total"`
	SHA256            string `json:"sha256"`
}

// Summary is the count of rows per status.
type Summary struct {
	Confirmed    int `json:"confirmed"`
	Partial      int `json:"partial"`
	NotConfirmed int `json:"not_confirmed"`
	Failed       int `json:"failed"`
	NotRun       int `json:"not_run"`
	Total        int `json:"total"`
}

// ArtifactRef is one file in the run directory.
type ArtifactRef struct {
	Name           string   `json:"name"`
	Path           string   `json:"path"`
	Present        bool     `json:"present"`
	Status         Status   `json:"status"`
	RequirementIDs []string `json:"requirement_ids,omitempty"`
}

// MatrixRow is one confirmation-matrix row (HAR-129 section I.1-I.5).
type MatrixRow struct {
	RequirementID        string   `json:"requirement_id"`
	Section              string   `json:"section"`
	Requirement          string   `json:"requirement"`
	RequiredBehavior     string   `json:"required_behavior"`
	ObservedLiveBehavior string   `json:"observed_live_behavior"`
	Evidence             []string `json:"evidence"`
	Status               Status   `json:"status"`
	Gap                  string   `json:"gap"`
}

// Manifest is artifacts/har129/<run_id>/manifest.json.
type Manifest struct {
	RunID             string            `json:"run_id"`
	GeneratedAt       string            `json:"generated_at"`
	Contract          string            `json:"contract"`
	Command           string            `json:"command"`
	Status            Status            `json:"status"`
	RequirementSource RequirementSource `json:"requirement_source"`
	Summary           Summary           `json:"summary"`
	Artifacts         []ArtifactRef     `json:"artifacts"`
	Matrix            []MatrixRow       `json:"matrix"`
}

// ArtifactDoc is one of the eleven JSON evidence files.
type ArtifactDoc struct {
	RunID          string         `json:"run_id"`
	Artifact       string         `json:"artifact"`
	GeneratedAt    string         `json:"generated_at"`
	Status         Status         `json:"status"`
	RequirementIDs []string       `json:"requirement_ids"`
	Evidence       []string       `json:"evidence"`
	Data           map[string]any `json:"data"`
}

// Options configures a run.
type Options struct {
	// OutDir is the base directory; the run directory is OutDir/<run_id>. Default "artifacts/har129".
	OutDir string
	// RunID overrides the generated run id (used by tests). Must match ^har129-.
	RunID string
	// RequirementsPath overrides the source of truth; empty finds it from the working directory.
	RequirementsPath string
	// Now overrides the clock (used by tests).
	Now func() time.Time
}

// Run writes the 13 HAR-129 section H files plus the section I matrix and returns the manifest.
// It never fabricates evidence: without a real integrated run every row is NOT RUN.
func Run(opts Options) (*Manifest, string, error) {
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	at := now().UTC()
	runID := opts.RunID
	if runID == "" {
		var err error
		runID, err = newRunID(at)
		if err != nil {
			return nil, "", err
		}
	}
	if !strings.HasPrefix(runID, "har129-") {
		return nil, "", fmt.Errorf("proof: run id %q must start with har129-", runID)
	}

	reqPath := opts.RequirementsPath
	if reqPath == "" {
		found, err := FindRequirements(".")
		if err != nil {
			return nil, "", err
		}
		reqPath = found
	}
	reqs, sha, err := LoadRequirements(reqPath)
	if err != nil {
		return nil, "", err
	}
	if opts.OutDir == "" {
		// Default under the repository root so the command is stable from any working directory.
		root := filepath.Dir(filepath.Dir(filepath.Dir(reqPath)))
		opts.OutDir = filepath.Join(root, "artifacts", "har129")
	}

	dir := filepath.Join(opts.OutDir, runID)
	generatedAt := at.Format(time.RFC3339)
	rows := BuildMatrix(reqs)
	summary := Summarize(rows)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", fmt.Errorf("proof: create run dir: %w", err)
	}
	for _, name := range evidenceArtifacts {
		doc := ArtifactDoc{
			RunID:          runID,
			Artifact:       name,
			GeneratedAt:    generatedAt,
			Status:         StatusNotRun,
			RequirementIDs: requirementIDsFor(reqs, artifactEvidence[name]),
			Evidence:       []string{},
			Data:           artifactData(name),
		}
		if err := writeJSON(filepath.Join(dir, name), doc); err != nil {
			return nil, "", err
		}
	}

	manifest := &Manifest{
		RunID:       runID,
		GeneratedAt: generatedAt,
		Contract:    Contract,
		Command:     Command,
		Status:      overall(summary),
		RequirementSource: RequirementSource{
			Version:           reqs.Version,
			Path:              filepath.ToSlash(RequirementsPath),
			RequirementsTotal: len(reqs.Requirements),
			SHA256:            sha,
		},
		Summary:   summary,
		Artifacts: artifactRefs(reqs),
		Matrix:    rows,
	}
	if err := writeJSON(filepath.Join(dir, "manifest.json"), manifest); err != nil {
		return nil, "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "demo-report.md"), RenderReport(manifest), 0o644); err != nil {
		return nil, "", fmt.Errorf("proof: write report: %w", err)
	}
	return manifest, dir, nil
}

func newRunID(at time.Time) (string, error) {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("proof: run id randomness: %w", err)
	}
	return "har129-" + at.Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:]), nil
}

func artifactRefs(reqs *RequirementsFile) []ArtifactRef {
	refs := make([]ArtifactRef, 0, len(ArtifactNames))
	for _, name := range ArtifactNames {
		ref := ArtifactRef{Name: name, Path: name, Present: true, Status: StatusNotRun}
		if ids := requirementIDsFor(reqs, artifactEvidence[name]); len(ids) > 0 {
			ref.RequirementIDs = ids
		}
		refs = append(refs, ref)
	}
	return refs
}

func requirementIDsFor(reqs *RequirementsFile, sections []string) []string {
	if len(sections) == 0 {
		return []string{}
	}
	want := map[string]bool{}
	for _, s := range sections {
		want[s] = true
	}
	ids := []string{}
	for _, r := range reqs.Requirements {
		if want[r.Section] {
			ids = append(ids, r.ID)
		}
	}
	return ids
}

func artifactData(name string) map[string]any {
	switch name {
	case "environment.json":
		return map[string]any{
			"harness_version": HarnessVersion,
			"go_version":      runtime.Version(),
			"os":              runtime.GOOS,
			"arch":            runtime.GOARCH,
			"note":            "No integrated execution was performed; this file is a placeholder for the run environment.",
		}
	default:
		return map[string]any{
			"note": "Not run: no integrated execution has filled this file yet.",
		}
	}
}
