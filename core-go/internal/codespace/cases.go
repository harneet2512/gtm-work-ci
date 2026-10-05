// Package codespace is the cloud demo's hidden automation (HAR-129 sections 2-13, HAR-124): the two frozen demo
// cases, each in its own database with a pristine template to restore from, the activation of one case at a time on
// the single core, the readiness status the operator section of the System page shows, the control service behind its Reset
// button, and the every-start boot sequence. It reuses internal/demorun (service plan, supervisor, seed flow);
// nothing here is needed on a laptop.
//
// Why one core and a switch rather than two cores: the Slack bot, the web app and the worker all talk to one core
// URL, a Socket Mode app token delivers a button click to one of its connections at random, and Neo4j Community has
// one database. The graph is a projection of Postgres, so activating a case rebuilds it from that case's database.
package codespace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

// Slots of the two demo cases, in demo order: case 1 learns, case 2 receives what it learned (HAR-129 section 13).
const (
	SlotCase1 = "case1"
	SlotCase2 = "case2"
)

// Case is one frozen demo case. Each lives in its own database because the freeze requires an empty one.
type Case struct {
	Slot          string
	Label         string // what the UI shows before the case is seeded
	OpportunityID string
	Database      string
	// Graph is the Neo4j instance that holds this case's graph (demorun.Config.GraphSlot): one per case, so activating a later
	// case never rebuilds a graph.
	Graph int
}

// DefaultCases are the two demo cases, in chronological order: MedTech Advances (Event N 2023-11-09) first, then EcoLite
// Innovations (account 001Wt00000PFsmaIAD, opportunity 006Wt000007BDAnIAO "EcoLite Advanced Lighting Collaboration", rank 9
// of bench/reports/demo-cases-2026-10-04.md, Event N 2023-11-29, similarity 0.7212 to MedTech on the HAR-129 proof runner).
// Event N of case 2 is later than case 1's, so what case 1 learns can flow forward without hindsight. (An earlier build
// of the freeze failed the report's history check on EcoLite and used EcoVision Engineering instead; on the current base
// EcoLite freezes cleanly, verified end to end in a scratch directory, and it is the demo's second case.)
func DefaultCases() []Case {
	return []Case{
		{Slot: SlotCase1, Label: "MedTech Advances", OpportunityID: demorun.MedTechOpportunity, Database: "ghost_case1", Graph: 0},
		{Slot: SlotCase2, Label: "EcoLite Innovations", OpportunityID: EcoLiteOpportunity, Database: "ghost_case2", Graph: 1},
	}
}

// EcoLiteOpportunity is the opportunity of EcoLite Innovations in the mined demo-cases report.
const EcoLiteOpportunity = "006Wt000007BDAnIAO"

// CasesFromEnv is DefaultCases with case 2's opportunity replaceable (GHOST_DEMO_CASE2_OPPORTUNITY), for the day the
// default case cannot be frozen faithfully on the data at hand. The replacement must be a ranked case of the mined report
// whose Event N is later than case 1's, or knowledge would flow backwards.
func CasesFromEnv(env map[string]string) []Case {
	cases := DefaultCases()
	if opp := env["GHOST_DEMO_CASE2_OPPORTUNITY"]; opp != "" && opp != cases[1].OpportunityID {
		cases[1].OpportunityID, cases[1].Label = opp, "Case 2 ("+opp+")"
	}
	return cases
}

// Template is the database holding the case exactly as frozen at Event N-1, which Reset restores from.
func (c Case) Template() string { return c.Database + "_frozen" }

var (
	databaseName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	slotName     = regexp.MustCompile(`^[a-z][a-z0-9]{0,15}$`)
	opportunity  = regexp.MustCompile(`^006[A-Za-z0-9]{12,15}$`)
)

// Validate rejects a case that could name a path or SQL identifier it should not.
func (c Case) Validate() error {
	switch {
	case !slotName.MatchString(c.Slot):
		return fmt.Errorf("codespace: slot %q is not a short lower-case name", c.Slot)
	case !databaseName.MatchString(c.Database) || !databaseName.MatchString(c.Template()):
		return fmt.Errorf("codespace: database %q is not a lower-case identifier of at most 54 characters", c.Database)
	case !opportunity.MatchString(c.OpportunityID):
		return fmt.Errorf("codespace: %q is not a Salesforce opportunity id", c.OpportunityID)
	}
	return nil
}

// FindCase returns the case of a slot.
func FindCase(cases []Case, slot string) (Case, error) {
	for _, c := range cases {
		if c.Slot == slot {
			return c, nil
		}
	}
	names := make([]string, 0, len(cases))
	for _, c := range cases {
		names = append(names, c.Slot)
	}
	return Case{}, fmt.Errorf("codespace: unknown case %q (known: %s)", slot, strings.Join(names, ", "))
}

// Paths are the per-case files under the demo state directory (git-ignored, on the persistent workspace volume).
type Paths struct{ Layout demorun.Layout }

// CaseDir holds one case's manifest and state.
func (p Paths) CaseDir(slot string) string { return filepath.Join(p.Layout.Dir(), "cases", slot) }

// Manifest is the frozen manifest JSON of a case.
func (p Paths) Manifest(slot string) string { return filepath.Join(p.CaseDir(slot), "manifest.json") }

// State is the case's seed state (ids, never a secret).
func (p Paths) State(slot string) string { return filepath.Join(p.CaseDir(slot), "state.json") }

// ActiveFile remembers which case core serves.
func (p Paths) ActiveFile() string { return filepath.Join(p.Layout.Dir(), "active-case") }

// GraphMarker remembers that the case's own Neo4j instance holds a graph built from its database (it holds the slot's name).
// Reset removes it, so the next activation rebuilds; a handoff to a case whose marker is current rebuilds nothing.
func (p Paths) GraphMarker(slot string) string { return filepath.Join(p.Layout.Dir(), "graph-"+slot) }

// ReadMarker reads a one-word marker file; a missing file is "".
func ReadMarker(path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("codespace: read %s: %w", path, err)
	}
	return strings.TrimSpace(string(b)), nil
}

// WriteMarker writes a marker atomically.
func WriteMarker(path, value string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("codespace: create %s: %w", filepath.Dir(path), err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(value+"\n"), 0o644); err != nil {
		return fmt.Errorf("codespace: write %s: %w", path, err)
	}
	return os.Rename(tmp, path)
}
