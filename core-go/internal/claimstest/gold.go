package claimstest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Gold is one gold checkpoint (fixtures/gold/<account>/cp<n>.json, schema gold.schema.json).
type Gold struct {
	Account        string    `json:"account"`
	Checkpoint     int       `json:"checkpoint"`
	AfterEventFile string    `json:"after_event_file"`
	AsOf           time.Time `json:"as_of"`
	Expected       Expected  `json:"expected"`
	// LiveFiles is the gold's own list of live files the checkpoint includes, when it declares one
	// (keys live_files / includes_live_files / live_event_files, at top level or under expected).
	// Otherwise the checkpoint includes every live file of the account up to after_event_file.
	LiveFiles []string `json:"-"`
}

// declaredLiveFiles reads an optional explicit list of live files from a checkpoint document.
func declaredLiveFiles(raw []byte) []string {
	var doc map[string]json.RawMessage
	if json.Unmarshal(raw, &doc) != nil {
		return nil
	}
	scopes := []map[string]json.RawMessage{doc}
	var expected map[string]json.RawMessage
	if json.Unmarshal(doc["expected"], &expected) == nil {
		scopes = append(scopes, expected)
	}
	for _, scope := range scopes {
		for _, key := range []string{"live_files", "includes_live_files", "live_event_files"} {
			var files []string
			if json.Unmarshal(scope[key], &files) == nil && len(files) > 0 {
				return files
			}
		}
	}
	return nil
}

// Expected is the expected graph and state of a checkpoint (only the parts WP6 scores).
type Expected struct {
	Entities      []Entity                   `json:"entities"`
	StateFields   map[string]json.RawMessage `json:"state_fields"`
	BuyingGroup   []GoldMember               `json:"buying_group"`
	CoverageGaps  []string                   `json:"coverage_gaps"`
	CriticalFacts []CriticalFact             `json:"critical_facts"`
	StaleFacts    []StaleFact                `json:"stale_facts"`
	Conflicts     []GoldConflict             `json:"conflicts"`
	Signals       []string                   `json:"signals"`
}

// Entity is a gold entity with its raw source identities.
type Entity struct {
	Key              string   `json:"key"`
	Kind             string   `json:"kind"`
	DisplayName      string   `json:"display_name"`
	SourceIdentities []string `json:"source_identities"`
}

// GoldMember is an expected buying-group member.
type GoldMember struct {
	PersonKey   string   `json:"person_key"`
	Roles       []string `json:"roles"`
	Status      string   `json:"status"`
	Title       string   `json:"title"`
	DelegatedTo string   `json:"delegated_to"`
}

// CriticalFact is a fact the state must get right, with its evidence. A nil EvidenceQuote means
// structured CRM evidence (no body text).
type CriticalFact struct {
	Field             string          `json:"field"`
	Subject           string          `json:"subject"`
	Value             json.RawMessage `json:"value"`
	EvidenceEventFile string          `json:"evidence_event_file"`
	EvidenceQuote     *string         `json:"evidence_quote"`
}

// StaleFact is a value some evidence supports but that must not be current.
type StaleFact struct {
	Field           string          `json:"field"`
	Subject         string          `json:"subject"`
	StaleValue      json.RawMessage `json:"stale_value"`
	SourceEventFile string          `json:"source_event_file"`
}

// GoldConflict is a field whose winner is contradicted by a newer lower-standing claim (ADR-0008).
type GoldConflict struct {
	Field                  string          `json:"field"`
	WinnerValue            json.RawMessage `json:"winner_value"`
	WinnerStanding         string          `json:"winner_standing"`
	WinnerEventFile        string          `json:"winner_event_file"`
	ContradictingValue     json.RawMessage `json:"contradicting_value"`
	ContradictingStanding  string          `json:"contradicting_standing"`
	ContradictingEventFile string          `json:"contradicting_event_file"`
	ContradictingQuote     string          `json:"contradicting_quote"`
}

// LoadGold reads every checkpoint under fixturesDir/gold, ordered by account then checkpoint.
func LoadGold(fixturesDir string) ([]Gold, error) {
	paths, err := filepath.Glob(filepath.Join(fixturesDir, "gold", "*", "cp*.json"))
	if err != nil || len(paths) == 0 {
		return nil, fmt.Errorf("claimstest: no gold checkpoints under %s: %v", fixturesDir, err)
	}
	sort.Strings(paths)
	var out []Gold
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var g Gold
		if err := json.Unmarshal(raw, &g); err != nil {
			return nil, fmt.Errorf("claimstest: parse %s: %w", p, err)
		}
		g.LiveFiles = declaredLiveFiles(raw)
		out = append(out, g)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Account != out[j].Account {
			return out[i].Account < out[j].Account
		}
		return out[i].Checkpoint < out[j].Checkpoint
	})
	return out, nil
}

// FindFixtures walks up from the working directory to the repository's fixtures directory.
func FindFixtures() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, "fixtures")
		if info, statErr := os.Stat(filepath.Join(candidate, "gold")); statErr == nil && info.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("claimstest: fixtures/ not found above the working directory")
		}
		dir = parent
	}
}

// IngestOrder returns the account's event files in ingest order: world files in lexical order
// (the WP1 convention), then the live files exactly in the order given (see LiveOrder).
func IngestOrder(worldFiles, liveFiles []string) []string {
	order := append([]string(nil), worldFiles...)
	sort.Strings(order)
	return append(order, liveFiles...)
}

// LiveOrder decides the order in which an account's live files are ingested. When the gold
// checkpoints declare their live files (Gold.LiveFiles) the longest declaration IS the order, and every
// other declaration must be a prefix of it; live files nobody declares follow in byTime order (their
// occurred_at order). A declaration naming a file that does not exist is an error.
func LiveOrder(golds []Gold, byTime []string) ([]string, error) {
	var longest []string
	for _, g := range golds {
		if len(g.LiveFiles) > len(longest) {
			longest = g.LiveFiles
		}
	}
	known := map[string]bool{}
	for _, f := range byTime {
		known[f] = true
	}
	for _, g := range golds {
		for i, f := range g.LiveFiles {
			if !known[f] {
				return nil, fmt.Errorf("claimstest: %s cp%d declares live file %s, which does not exist", g.Account, g.Checkpoint, f)
			}
			if i >= len(longest) || longest[i] != f {
				return nil, fmt.Errorf("claimstest: %s cp%d live_files %v is not a prefix of %v", g.Account, g.Checkpoint, g.LiveFiles, longest)
			}
		}
	}
	order := append([]string(nil), longest...)
	declared := map[string]bool{}
	for _, f := range longest {
		declared[f] = true
	}
	for _, f := range byTime {
		if !declared[f] {
			order = append(order, f)
		}
	}
	return order, nil
}

// Index returns the position of file in order, or -1.
func Index(order []string, file string) int {
	for i, f := range order {
		if f == file {
			return i
		}
	}
	return -1
}

// NormalizeText lower-cases and collapses whitespace for text comparison.
func NormalizeText(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}
