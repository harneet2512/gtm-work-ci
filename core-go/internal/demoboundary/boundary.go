// Package demoboundary reads the HAR-129 demo audience boundary (contracts/demo/boundary.v1.json) and
// scans audience copy for terms that must never reach it: developer tooling, terminal commands, proof
// plumbing and out-of-scope experiments. The audience surface is the Web Control Plane and Cliff in
// Slack; everything else is internal. Tests of the Slack surface, the contracts and the demo docs use
// it so the forbidden-term list lives in one non-Markdown file.
package demoboundary

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ContractPath is the boundary contract, relative to the repository root.
const ContractPath = "contracts/demo/boundary.v1.json"

// Surface is one audience surface.
type Surface struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Job  string `json:"job"`
}

// Trigger is the one visible trigger of the product flow.
type Trigger struct {
	Surface      string         `json:"surface"`
	Control      string         `json:"control"`
	CoreEndpoint string         `json:"core_endpoint"`
	Also         []SlackTrigger `json:"also"`
}

// SlackTrigger is Cliff's play_next: the same core route as web Play, always behind a confirmation.
type SlackTrigger struct {
	Surface              string `json:"surface"`
	Control              string `json:"control"`
	CoreEndpoint         string `json:"core_endpoint"`
	RequiresConfirmation bool   `json:"requires_confirmation"`
}

// AskCliff is the owner-approved Ask Cliff surface (2026-10-06).
type AskCliff struct {
	DM                      bool     `json:"dm"`
	MentionInThread         bool     `json:"mention_in_thread"`
	ChannelTopLevelMessages []string `json:"channel_top_level_messages"`
	Actions                 []string `json:"actions"`
	DryRunOnly              []string `json:"dry_run_only"`
}

// Step is one step of the audience flow.
type Step struct {
	Step    int    `json:"step"`
	Surface string `json:"surface"`
	What    string `json:"what"`
	Trigger bool   `json:"trigger,omitempty"`
}

// Term is one forbidden pattern.
type Term struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Pattern       string `json:"pattern"`
	CaseSensitive bool   `json:"case_sensitive"`
	Why           string `json:"why"`
}

// Boundary is the decoded contract with its patterns compiled.
type Boundary struct {
	Version         string    `json:"version"`
	Invariant       string    `json:"invariant"`
	AudienceSurface []Surface `json:"audience_surfaces"`
	VisibleTrigger  Trigger   `json:"visible_trigger"`
	AskCliff        AskCliff  `json:"ask_cliff"`
	AudienceFlow    []Step    `json:"audience_flow"`
	ForbiddenTerms  []Term    `json:"forbidden_terms"`
	compiled        []*regexp.Regexp
}

// Hit is one forbidden term found in a piece of audience copy.
type Hit struct {
	TermID string
	Kind   string
	Match  string
}

func (h Hit) String() string { return fmt.Sprintf("%s (%s): %q", h.TermID, h.Kind, h.Match) }

// RepoRoot walks up from the working directory to the directory holding the boundary contract.
func RepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(ContractPath))); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("demoboundary: %s not found above the working directory", ContractPath)
		}
		dir = parent
	}
}

// Load reads and compiles the boundary contract under the repository root.
func Load(root string) (*Boundary, error) {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ContractPath)))
	if err != nil {
		return nil, fmt.Errorf("demoboundary: read the contract: %w", err)
	}
	return Parse(raw)
}

// Parse decodes a boundary contract and compiles its forbidden terms.
func Parse(raw []byte) (*Boundary, error) {
	var b Boundary
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, fmt.Errorf("demoboundary: decode the contract: %w", err)
	}
	if len(b.ForbiddenTerms) == 0 {
		return nil, errors.New("demoboundary: the contract lists no forbidden terms")
	}
	for _, t := range b.ForbiddenTerms {
		expr := t.Pattern
		if !t.CaseSensitive {
			expr = "(?i)" + expr
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("demoboundary: term %s: %w", t.ID, err)
		}
		b.compiled = append(b.compiled, re)
	}
	return &b, nil
}

// Scan returns every forbidden term found in text.
func (b *Boundary) Scan(text string) []Hit {
	var hits []Hit
	for i, re := range b.compiled {
		for _, m := range re.FindAllString(text, -1) {
			t := b.ForbiddenTerms[i]
			hits = append(hits, Hit{TermID: t.ID, Kind: t.Kind, Match: m})
		}
	}
	return hits
}

// SurfaceIDs is the set of audience surface ids.
func (b *Boundary) SurfaceIDs() map[string]bool {
	ids := map[string]bool{}
	for _, s := range b.AudienceSurface {
		ids[s.ID] = true
	}
	return ids
}

// Invariant is the boundary sentence the contract must carry verbatim.
const Invariant = "Ghost demo audience surface = Web Control Plane + Cliff in Slack. Internal developer/admin/proof " +
	"tooling must never be promoted into the audience-facing demo without an explicit HAR-129 contract change " +
	"approved by the product owner."

// Check returns every way the contract breaks the boundary: the invariant is not verbatim, the audience
// surfaces are not exactly the two HAR-129 names, a flow step is on another surface or names a forbidden
// term, or the flow does not start the product from Play in the web control plane before any Cliff step.
func (b *Boundary) Check() []string {
	var problems []string
	if b.Invariant != Invariant {
		problems = append(problems, "the invariant is not the HAR-129 boundary sentence verbatim")
	}
	ids := b.SurfaceIDs()
	if len(b.AudienceSurface) != 2 || !ids["web_control_plane"] || !ids["cliff_in_slack"] {
		problems = append(problems, "the audience surfaces must be exactly web_control_plane and cliff_in_slack")
	}
	if b.VisibleTrigger.Surface != "web_control_plane" || b.VisibleTrigger.Control != "Play" {
		problems = append(problems, "the visible trigger must be Play in the web control plane")
	}
	problems = append(problems, b.checkAskCliff()...)
	triggers, firstCliff, triggerAt := 0, 0, 0
	for i, s := range b.AudienceFlow {
		if s.Step != i+1 {
			problems = append(problems, fmt.Sprintf("flow step %d is numbered %d", i+1, s.Step))
		}
		if !ids[s.Surface] {
			problems = append(problems, fmt.Sprintf("flow step %d is on %q, which is not an audience surface", s.Step, s.Surface))
		}
		for _, h := range b.Scan(s.What) {
			problems = append(problems, fmt.Sprintf("flow step %d names %s", s.Step, h))
		}
		if s.Surface == "cliff_in_slack" && firstCliff == 0 {
			firstCliff = s.Step
		}
		if s.Trigger {
			triggers++
			triggerAt = s.Step
			if s.Surface != b.VisibleTrigger.Surface {
				problems = append(problems, fmt.Sprintf("the trigger step %d is not on the web control plane", s.Step))
			}
		}
	}
	if triggers != 1 {
		problems = append(problems, fmt.Sprintf("the flow has %d trigger steps, want exactly 1 (Play)", triggers))
	} else if firstCliff != 0 && triggerAt > firstCliff {
		problems = append(problems, "Cliff speaks before Play: the demo must begin from web Play")
	}
	return problems
}

// walkthroughHeading names the section of a demo doc that is the audience walkthrough.
const walkthroughHeading = "audience walkthrough"

// Walkthrough returns the body of every '## Audience walkthrough' section of a Markdown document (up to
// the next heading of the same or a higher level). A demo doc keeps operator setup elsewhere; this
// section is what a presenter shows or says, so it must be free of forbidden terms and command blocks.
func Walkthrough(markdown string) []string {
	var sections []string
	var cur *strings.Builder
	level := 0
	for _, line := range strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n") {
		if l := headingLevel(line); l > 0 {
			if cur != nil && l <= level {
				sections = append(sections, cur.String())
				cur = nil
			}
			if cur == nil && strings.EqualFold(strings.TrimSpace(strings.TrimLeft(line, "#")), walkthroughHeading) {
				cur, level = &strings.Builder{}, l
				continue
			}
		}
		if cur != nil {
			cur.WriteString(line)
			cur.WriteByte('\n')
		}
	}
	if cur != nil {
		sections = append(sections, cur.String())
	}
	return sections
}

func headingLevel(line string) int {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	if n == 0 || n >= len(line) || line[n] != ' ' {
		return 0
	}
	return n
}
