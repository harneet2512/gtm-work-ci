package proof

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/schemacheck"
)

func fixedNow() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) }

func runProof(t *testing.T) (*Manifest, string) {
	t.Helper()
	dir := t.TempDir()
	m, runDir, err := Run(Options{OutDir: dir, RunID: "har129-test-0001", Now: fixedNow})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return m, runDir
}

// TestRunEmitsExactlyTheThirteenFiles proves the HAR-129 section H directory.
func TestRunEmitsExactlyTheThirteenFiles(t *testing.T) {
	_, runDir := runProof(t)
	entries, err := os.ReadDir(runDir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range entries {
		got[e.Name()] = true
	}
	if len(got) != 13 {
		t.Fatalf("run dir has %d files, want exactly 13: %v", len(got), keys(got))
	}
	for _, name := range ArtifactNames {
		if !got[name] {
			t.Errorf("missing required artifact %s", name)
		}
	}
}

// TestManifestConformsAndIsAllNotRun proves the section I matrix is complete and honest.
func TestManifestConformsAndIsAllNotRun(t *testing.T) {
	m, runDir := runProof(t)

	v, err := schemacheck.New()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(runDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Validate("har129_proof", raw); err != nil {
		t.Fatalf("manifest.json does not conform to har129_proof: %v", err)
	}

	reqPath, err := FindRequirements(".")
	if err != nil {
		t.Fatal(err)
	}
	reqs, _, err := LoadRequirements(reqPath)
	if err != nil {
		t.Fatal(err)
	}

	if len(m.Matrix) != len(reqs.Requirements) {
		t.Fatalf("matrix has %d rows, want one per requirement (%d)", len(m.Matrix), len(reqs.Requirements))
	}
	seen := map[string]bool{}
	g := 0
	for _, r := range m.Matrix {
		if seen[r.RequirementID] {
			t.Errorf("duplicate matrix row %s", r.RequirementID)
		}
		seen[r.RequirementID] = true
		if r.Status != StatusNotRun {
			t.Errorf("row %s is %s, want NOT RUN before any integrated run", r.RequirementID, r.Status)
		}
		if len(r.Evidence) != 0 {
			t.Errorf("row %s carries evidence without a run: %v", r.RequirementID, r.Evidence)
		}
		if r.Gap == "" {
			t.Errorf("row %s must state its gap while NOT RUN", r.RequirementID)
		}
		if r.Section == "" {
			t.Errorf("row %s must carry its §-reference", r.RequirementID)
		}
		if strings.HasPrefix(r.RequirementID, "HAR129-G-") {
			g++
		}
	}
	for _, r := range reqs.Requirements {
		if !seen[r.ID] {
			t.Errorf("requirement %s has no matrix row", r.ID)
		}
	}
	if g != 26 {
		t.Errorf("matrix has %d §G rows, want the 26 listed live checks", g)
	}
	if m.Summary.NotRun != m.Summary.Total || m.Summary.Total != len(m.Matrix) {
		t.Errorf("summary %+v does not match an all-NOT-RUN matrix of %d rows", m.Summary, len(m.Matrix))
	}
	if m.Status != StatusNotRun {
		t.Errorf("run status is %s, want NOT RUN", m.Status)
	}
	if m.RequirementSource.SHA256 == "" || len(m.RequirementSource.SHA256) != 64 {
		t.Errorf("requirement source sha256 is missing or malformed: %q", m.RequirementSource.SHA256)
	}
}

// TestArtifactFilesConform proves the eleven evidence files are the right shape and carry no
// fabricated evidence.
func TestArtifactFilesConform(t *testing.T) {
	_, runDir := runProof(t)
	v, err := schemacheck.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range evidenceArtifacts {
		raw, err := os.ReadFile(filepath.Join(runDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := v.Validate("har129_artifact", raw); err != nil {
			t.Errorf("%s does not conform to har129_artifact: %v", name, err)
		}
		var doc ArtifactDoc
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		if doc.Status != StatusNotRun {
			t.Errorf("%s status is %s, want NOT RUN", name, doc.Status)
		}
		if len(doc.Evidence) != 0 {
			t.Errorf("%s carries evidence without a run: %v", name, doc.Evidence)
		}
	}
}

// TestReportHasTheConfirmationMatrix proves the human report carries the §I columns, every row id
// and the remaining-work list.
func TestReportHasTheConfirmationMatrix(t *testing.T) {
	m, runDir := runProof(t)
	raw, err := os.ReadFile(filepath.Join(runDir, "demo-report.md"))
	if err != nil {
		t.Fatal(err)
	}
	report := string(raw)
	header := "| HAR-129 requirement | Required behavior | Observed live behavior | Evidence | Status | Gap |"
	if !strings.Contains(report, header) {
		t.Fatalf("report is missing the §I matrix header")
	}
	for _, r := range m.Matrix {
		if !strings.Contains(report, r.RequirementID) {
			t.Errorf("report is missing row %s", r.RequirementID)
		}
	}
	if !strings.Contains(report, "## Remaining work") {
		t.Error("report is missing the remaining-work section")
	}
	if !strings.Contains(report, "## Summary") {
		t.Error("report is missing the summary counts")
	}
}

// TestRunIDIsGenerated proves a run without an explicit id gets a valid, unique id.
func TestRunIDIsGenerated(t *testing.T) {
	dir := t.TempDir()
	m, _, err := Run(Options{OutDir: dir, Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(m.RunID, "har129-20261004T120000Z-") {
		t.Fatalf("unexpected generated run id %q", m.RunID)
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
