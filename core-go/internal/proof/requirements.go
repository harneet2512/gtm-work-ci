// Package proof writes the HAR-129 agent acceptance artifact (section H) and the confirmation
// matrix (section I). The matrix is generated from one requirement source of truth
// (contracts/har129/requirements.v1.json): one row per requirement, no hand-written rows. Every row
// starts NOT RUN and can only move when a real integrated run supplies machine evidence; a unit
// test, schema, mock, screenshot or code read can never set CONFIRMED (section I.4).
package proof

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Requirement is one HAR-129 visible/product requirement, quoted from the contract.
type Requirement struct {
	ID               string `json:"id"`
	Section          string `json:"section"`
	Reference        string `json:"reference"`
	Requirement      string `json:"requirement"`
	RequiredBehavior string `json:"required_behavior"`
}

// Section groups requirements under one HAR-129 reference.
type Section struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Reference string `json:"reference"`
}

// RequirementsFile is the decoded contracts/har129/requirements.v1.json.
type RequirementsFile struct {
	Version      string        `json:"version"`
	Source       string        `json:"source"`
	QuotedFrom   string        `json:"quoted_from"`
	Sections     []Section     `json:"sections"`
	Requirements []Requirement `json:"requirements"`
}

// SectionByID returns the section with the given id.
func (f *RequirementsFile) SectionByID(id string) (Section, bool) {
	for _, s := range f.Sections {
		if s.ID == id {
			return s, true
		}
	}
	return Section{}, false
}

// RequirementsPath is the source of truth, relative to the repository root.
const RequirementsPath = "contracts/har129/requirements.v1.json"

// LoadRequirements reads and validates the requirement source of truth.
func LoadRequirements(path string) (*RequirementsFile, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("proof: read requirements: %w", err)
	}
	var f RequirementsFile
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, "", fmt.Errorf("proof: parse requirements: %w", err)
	}
	if f.Version == "" || f.Source == "" || len(f.Sections) == 0 || len(f.Requirements) == 0 {
		return nil, "", errors.New("proof: requirements file is incomplete")
	}
	seen := map[string]bool{}
	for _, r := range f.Requirements {
		if r.ID == "" || r.Section == "" || r.Requirement == "" || r.RequiredBehavior == "" {
			return nil, "", fmt.Errorf("proof: requirement %q is incomplete", r.ID)
		}
		if seen[r.ID] {
			return nil, "", fmt.Errorf("proof: duplicate requirement id %q", r.ID)
		}
		seen[r.ID] = true
		if _, ok := f.SectionByID(r.Section); !ok {
			return nil, "", fmt.Errorf("proof: requirement %q names unknown section %q", r.ID, r.Section)
		}
	}
	sum := sha256.Sum256(raw)
	return &f, hex.EncodeToString(sum[:]), nil
}

// FindRequirements walks up from startDir to the repository root and returns the source of truth.
func FindRequirements(startDir string) (string, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", fmt.Errorf("proof: abs: %w", err)
	}
	for {
		candidate := filepath.Join(dir, filepath.FromSlash(RequirementsPath))
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("proof: %s not found above %s", RequirementsPath, startDir)
		}
		dir = parent
	}
}
