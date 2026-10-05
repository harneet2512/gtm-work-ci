package demomine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Config is the committed scoring configuration (bench/config/demo_case_scoring.v1.json).
type Config struct {
	Version          string             `json:"version"`
	FrozenOn         string             `json:"frozen_on"`
	Note             string             `json:"note"`
	DimensionWeights map[string]float64 `json:"dimension_weights"`
	History          HistoryWeights     `json:"history"`
	HeldOut          HeldOutWeights     `json:"held_out"`
	Requirements     Requirements       `json:"requirements"`
	Rationale        map[string]string  `json:"rationale"`
	// SHA256 is the digest of the file the config was loaded from (not part of the file).
	SHA256 string `json:"-"`
}

// HistoryWeights score events 1..N-1.
type HistoryWeights struct {
	DistinctDimension     float64 `json:"distinct_dimension"`
	MaterialEvent         float64 `json:"material_event"`
	MaterialEventCap      int     `json:"material_event_cap"`
	RevisitedDimension    float64 `json:"revisited_dimension"`
	RevisitedDimensionCap int     `json:"revisited_dimension_cap"`
}

// HeldOutWeights score Event N.
type HeldOutWeights struct {
	Material       float64 `json:"material"`
	PerDimension   float64 `json:"per_dimension"`
	NovelDimension float64 `json:"novel_dimension"`
	DecisionChange float64 `json:"decision_change"`
}

// Requirements make a sequence a candidate at all.
type Requirements struct {
	MinHistoryEvents             int  `json:"min_history_events"`
	MinHistoryDistinctDimensions int  `json:"min_history_distinct_dimensions"`
	HeldOutMustBeMaterial        bool `json:"held_out_must_be_material"`
	HeldOutStrictlyAfterHistory  bool `json:"held_out_strictly_after_history"`
	// HeldOutExcludedKeyPrefixes are source event keys that cannot be Event N (the snapshot's final-stage values).
	HeldOutExcludedKeyPrefixes []string `json:"held_out_excluded_event_key_prefixes"`
}

// LoadConfig reads and validates the scoring config. Every weight must have a stated rationale.
func LoadConfig(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("demomine: read scoring config: %w", err)
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return Config{}, fmt.Errorf("demomine: decode scoring config %s: %w", path, err)
	}
	// Hash the file with LF line endings: a Windows checkout (CRLF) and a Linux one must report the same digest.
	sum := sha256.Sum256(bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n")))
	c.SHA256 = hex.EncodeToString(sum[:])
	return c, c.Validate()
}

// Validate rejects a config with a missing or negative weight, or a weight without a rationale.
func (c Config) Validate() error {
	for _, d := range Dimensions {
		w, ok := c.DimensionWeights[d]
		if !ok || w < 0 {
			return fmt.Errorf("demomine: scoring config needs a non-negative weight for dimension %s", d)
		}
	}
	for _, key := range []string{
		"dimension_weights", "history.distinct_dimension", "history.material_event", "history.revisited_dimension",
		"held_out.material", "held_out.per_dimension", "held_out.novel_dimension", "held_out.decision_change",
		"requirements.min_history_events", "requirements.min_history_distinct_dimensions",
		"requirements.held_out_must_be_material", "requirements.held_out_strictly_after_history",
		"requirements.held_out_excluded_event_key_prefixes",
	} {
		if c.Rationale[key] == "" {
			return fmt.Errorf("demomine: scoring config has no rationale for %s", key)
		}
	}
	h, o := c.History, c.HeldOut
	if h.DistinctDimension < 0 || h.MaterialEvent < 0 || h.RevisitedDimension < 0 || h.MaterialEventCap < 0 || h.RevisitedDimensionCap < 0 ||
		o.Material < 0 || o.PerDimension < 0 || o.NovelDimension < 0 || o.DecisionChange < 0 {
		return fmt.Errorf("demomine: scoring config has a negative weight")
	}
	return nil
}

// Score is one sequence's score with every component, so the ranking can be audited line by line.
type Score struct {
	Total                float64  `json:"total"`
	HistoryDimensions    []string `json:"history_distinct_dimensions"`
	HistoryDimensionPts  float64  `json:"history_dimension_points"`
	HistoryMaterialCount int      `json:"history_material_events"`
	HistoryMaterialPts   float64  `json:"history_material_event_points"`
	Revisited            []string `json:"revisited_dimensions"`
	RevisitedPts         float64  `json:"revisited_points"`
	HeldOutDimensions    []string `json:"held_out_dimensions"`
	HeldOutPts           float64  `json:"held_out_points"`
	NovelDimensions      []string `json:"held_out_novel_dimensions"`
	NovelPts             float64  `json:"held_out_novel_points"`
	DecisionChange       bool     `json:"decision_change"`
	DecisionPts          float64  `json:"decision_change_points"`
}

// ScoreSequence scores history (events 1..N-1) with heldOut (event N). ok is false, with the reason, when
// the sequence fails a requirement and so is not a candidate.
func (c Config) ScoreSequence(history []EventRecord, heldOut EventRecord, strictlyLater bool) (s Score, ok bool, why string) {
	hist := historyDimensionCounts(history)
	s.HistoryDimensions = sortedDims(hist)
	req := c.Requirements
	switch {
	case len(history) < req.MinHistoryEvents:
		return s, false, fmt.Sprintf("history has %d events, needs %d", len(history), req.MinHistoryEvents)
	case len(hist) < req.MinHistoryDistinctDimensions:
		return s, false, fmt.Sprintf("history has %d distinct dimensions, needs %d", len(hist), req.MinHistoryDistinctDimensions)
	case excluded(heldOut, req.HeldOutExcludedKeyPrefixes):
		return s, false, "held-out event is of an excluded kind: " + heldOut.SourceEventKey
	case req.HeldOutMustBeMaterial && !heldOut.IsMaterial():
		return s, false, "held-out event changes no dimension"
	case req.HeldOutStrictlyAfterHistory && !strictlyLater:
		return s, false, "held-out event is not dated strictly after the last history event"
	}
	for _, d := range s.HistoryDimensions {
		s.HistoryDimensionPts += c.DimensionWeights[d] * c.History.DistinctDimension
		if hist[d] >= 2 {
			s.Revisited = append(s.Revisited, d)
		}
	}
	for _, e := range history {
		if e.IsMaterial() {
			s.HistoryMaterialCount++
		}
	}
	s.HistoryMaterialPts = c.History.MaterialEvent * float64(min(s.HistoryMaterialCount, c.History.MaterialEventCap))
	s.RevisitedPts = c.History.RevisitedDimension * float64(min(len(s.Revisited), c.History.RevisitedDimensionCap))
	if s.Revisited == nil {
		s.Revisited = []string{}
	}
	s.HeldOutDimensions = append([]string{}, heldOut.Dimensions...)
	s.NovelDimensions = []string{}
	s.HeldOutPts = c.HeldOut.Material
	for _, d := range heldOut.Dimensions {
		s.HeldOutPts += c.HeldOut.PerDimension * c.DimensionWeights[d]
		if hist[d] == 0 {
			s.NovelDimensions = append(s.NovelDimensions, d)
			s.NovelPts += c.HeldOut.NovelDimension * c.DimensionWeights[d]
		}
	}
	if heldOut.Has(DimAction) {
		s.DecisionChange, s.DecisionPts = true, c.HeldOut.DecisionChange
	}
	s.Total = round4(s.HistoryDimensionPts + s.HistoryMaterialPts + s.RevisitedPts + s.HeldOutPts + s.NovelPts + s.DecisionPts)
	return s, true, ""
}

// historyDimensionCounts counts, per dimension, the history events that changed it.
func historyDimensionCounts(history []EventRecord) map[string]int {
	out := map[string]int{}
	for _, e := range history {
		for _, d := range e.Dimensions {
			out[d]++
		}
	}
	return out
}

// sortedDims returns the keys in contract order.
func sortedDims(m map[string]int) []string {
	out := []string{}
	for _, d := range Dimensions {
		if m[d] > 0 {
			out = append(out, d)
		}
	}
	return out
}

func round4(v float64) float64 {
	return float64(int64(v*10000+0.5)) / 10000
}

func excluded(r EventRecord, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(r.SourceEventKey, p) {
			return true
		}
	}
	return false
}
