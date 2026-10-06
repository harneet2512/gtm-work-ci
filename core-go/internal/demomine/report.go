package demomine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/crmarena"
)

// ReportSchema names the report format.
const ReportSchema = "demo-cases.v1"

// Report is the ranked demo-case report (bench/reports/demo-cases-<date>.{json,md}). It holds no database
// ids and no wall-clock time, so the same snapshot, config and flags always give the same bytes.
type Report struct {
	Schema   string        `json:"schema"`
	Date     string        `json:"date"`
	Snapshot SnapshotInfo  `json:"snapshot"`
	Scoring  ScoringInfo   `json:"scoring"`
	Method   string        `json:"method"`
	Scope    ScopeInfo     `json:"scope"`
	Counts   Counts        `json:"counts"`
	Notes    []string      `json:"notes"`
	Top      []Case        `json:"top_cases"`
	Ranking  []RankedBrief `json:"ranking_all_candidates"`
}

// SnapshotInfo identifies the data mined.
type SnapshotInfo struct {
	Name           string `json:"name"`
	Dataset        string `json:"dataset"`
	Licence        string `json:"licence"`
	ManifestSHA256 string `json:"manifest_sha256,omitempty"`
}

// ScoringInfo ties the ranking to the committed config.
type ScoringInfo struct {
	ConfigFile string `json:"config_file"`
	Version    string `json:"version"`
	SHA256     string `json:"sha256"`
}

// ScopeInfo says which opportunities were scanned and why.
type ScopeInfo struct {
	Opportunities      string `json:"opportunities"`
	SplitCutoff        string `json:"split_cutoff,omitempty"`
	TransitionDetector bool   `json:"transition_detector"`
	Synthetic          string `json:"synthetic_layer"`
}

// Counts are the scan totals.
type Counts struct {
	EventsReplayed         int            `json:"events_replayed"`
	AccountsScanned        int            `json:"accounts_scanned"`
	OpportunitiesInScope   int            `json:"opportunities_in_scope"`
	OpportunitiesScanned   int            `json:"opportunities_scanned"`
	ScannedEvents          int            `json:"scanned_events"`
	MaterialEvents         int            `json:"material_events"`
	OpportunitiesWithCase  int            `json:"opportunities_with_a_candidate_case"`
	MaterialEventsPerDim   map[string]int `json:"material_events_per_dimension"`
	OpportunitiesNoCaseWhy map[string]int `json:"opportunities_without_a_case_by_reason"`
	EventsByLayer          map[string]int `json:"scanned_events_by_layer"`
	// Extractor is how the LLM-extraction claims were served; absent when only rule extractors ran.
	Extractor *ExtractorStats `json:"extractor,omitempty"`
}

// RankedBrief is one line of the full ranking (every candidate, not just the top ten). It carries what
// freezing the case needs (the held-out event, each history event's id and dimensions, why it was selected), so
// any ranked case can be frozen from the report and checked against the replay.
type RankedBrief struct {
	Rank          int        `json:"rank"`
	OpportunityID string     `json:"opportunity_id"`
	Score         float64    `json:"score"`
	HistoryEvents int        `json:"history_events"`
	HeldOutEvent  string     `json:"held_out_event_id"`
	HeldOutDims   []string   `json:"held_out_dimensions"`
	HistoryIDs    []string   `json:"history_event_ids"`
	HistoryDims   [][]string `json:"history_dimensions"`
	Signature     Signature  `json:"dimension_signature"`
	WhySelected   string     `json:"why_selected"`
}

// briefs summarizes every ranked case.
func briefs(cases []Case) []RankedBrief {
	out := make([]RankedBrief, 0, len(cases))
	for _, c := range cases {
		b := RankedBrief{Rank: c.Rank, OpportunityID: c.OpportunityID, Score: c.Score.Total, HistoryEvents: len(c.History),
			HeldOutEvent: c.HeldOut.EventID, HeldOutDims: nonNil(c.HeldOut.Dimensions), Signature: c.Signature, WhySelected: c.WhySelected,
			HistoryIDs: make([]string, 0, len(c.History)), HistoryDims: make([][]string, 0, len(c.History))}
		for _, e := range c.History {
			b.HistoryIDs = append(b.HistoryIDs, e.EventID)
			b.HistoryDims = append(b.HistoryDims, nonNil(e.Dimensions))
		}
		out = append(out, b)
	}
	return out
}

// Find returns the ranked case (top case, else the brief of any other ranked case) for an opportunity and
// held-out event, in the form freezing needs. ok is false when the report ranks no such case.
func (r Report) Find(opportunityID, heldOutEventID string) (Case, bool) {
	for _, c := range r.Top {
		if c.OpportunityID == opportunityID && c.HeldOut.EventID == heldOutEventID {
			return c, true
		}
	}
	for _, b := range r.Ranking {
		if b.OpportunityID != opportunityID || b.HeldOutEvent != heldOutEventID || len(b.HistoryIDs) != len(b.HistoryDims) {
			continue
		}
		c := Case{Rank: b.Rank, OpportunityID: b.OpportunityID, Score: Score{Total: b.Score}, WhySelected: b.WhySelected,
			Signature: b.Signature, HeldOut: EventRecord{EventID: b.HeldOutEvent, Dimensions: b.HeldOutDims}}
		for i, id := range b.HistoryIDs {
			c.History = append(c.History, EventRecord{EventID: id, Dimensions: b.HistoryDims[i]})
		}
		return c, true
	}
	return Case{}, false
}

func buildReport(o MineOptions, snap crmarena.Snapshot, res crmarena.Result, scan map[string]bool, seqs []Sequence, cases []Case) Report {
	r := Report{Schema: ReportSchema, Date: o.Date,
		Snapshot: SnapshotInfo{Name: filepath.Base(filepath.Clean(o.Dir)), Dataset: "CRMArena-Pro B2B Salesforce org (Salesforce AI Research)",
			Licence: "CC BY-NC 4.0 (non-commercial use only)", ManifestSHA256: fileDigest(filepath.Join(o.Dir, "manifest.json"))},
		Scoring: ScoringInfo{ConfigFile: "bench/config/demo_case_scoring.v1.json", Version: o.Config.Version, SHA256: o.Config.SHA256},
		Method: "Every event of the snapshot is replayed once, in its real order (time, phase, source identity), through the real ingest -> " +
			"coalesced recompute -> StateDiff -> signals -> trigger path on a private embedded Postgres, one event at a time with a clock set to the " +
			"event's own time. Each event is measured after it is drained. No LLM is called: claims come from CRM-structured events and the existing " +
			"rules. Date fields dated after an event are removed from that event (AsKnown). For every scanned opportunity each split into history " +
			"(events 1..N-1) and held-out event N is scored with the committed weights and the best split is kept.",
		Scope: ScopeInfo{Opportunities: o.SplitLabel, TransitionDetector: o.Rules != nil,
			Synthetic: "not generated yet (HAR-131): every event is base (origin dataset, crmarena-pro:b2b)"},
		Counts: Counts{EventsReplayed: len(res.Events), OpportunitiesInScope: len(scan), OpportunitiesScanned: len(seqs),
			MaterialEventsPerDim: map[string]int{}, OpportunitiesNoCaseWhy: map[string]int{}, EventsByLayer: map[string]int{}},
		Notes: []string{
			"Event N's record in a case is the miner's view of what Event N did, kept for audit; a frozen manifest holds Event N bare.",
			"Dimension evidence is rule-based: field changes of the deal state, signals, the transition detector when on, and the trigger evaluation.",
			"recommended_action means the trigger evaluation of the event is eligible: the account agent would run on it.",
			"Claims come from the CRM-structured events and the existing rules, plus (when counts.extractor is present) LLM extraction replayed from recorded cassettes by the offline replay worker: no provider, no key, no live call. A miss yields no claims and is counted.",
		}}
	if o.Split != nil {
		r.Scope.SplitCutoff = o.Split.Cutoff
	}
	for _, s := range seqs {
		for _, e := range s.Events {
			r.Counts.ScannedEvents++
			r.Counts.EventsByLayer[e.Layer]++
			if e.IsMaterial() {
				r.Counts.MaterialEvents++
			}
			for _, d := range e.Dimensions {
				r.Counts.MaterialEventsPerDim[d]++
			}
		}
	}
	r.Counts.OpportunitiesWithCase = len(cases)
	accounts, ranked := map[string]bool{}, map[string]bool{}
	for _, c := range cases {
		ranked[c.OpportunityID] = true
	}
	for _, s := range seqs {
		accounts[s.AccountID] = true
		if !ranked[s.OpportunityID] {
			r.Counts.OpportunitiesNoCaseWhy[o.Config.NoCaseReason(s)]++
		}
	}
	r.Counts.AccountsScanned = len(accounts)
	r.Ranking = briefs(cases)
	r.Top = cases[:min(TopCases, len(cases))]
	if r.Top == nil {
		r.Top, r.Ranking = []Case{}, []RankedBrief{}
	}
	return r
}

func fileDigest(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// JSON renders the report as indented JSON with a trailing newline.
func (r Report) JSON() ([]byte, error) {
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("demomine: encode report: %w", err)
	}
	return append(raw, '\n'), nil
}

// Markdown renders the top cases for reading: a ranked table, then each case's event sequence.
func (r Report) Markdown() []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# Demo-case mining report, %s\n\n", r.Date)
	fmt.Fprintf(&b, "Snapshot `%s` (%s, %s). Scoring `%s` %s, sha256 `%s`.\n\n", r.Snapshot.Name, r.Snapshot.Dataset, r.Snapshot.Licence,
		r.Scoring.ConfigFile, r.Scoring.Version, r.Scoring.SHA256)
	fmt.Fprintf(&b, "**Scanned:** %d of %d opportunities in scope (%s), %d events replayed in total, %d events in scanned sequences, %d of them material; %d opportunities have a candidate case.\n\n",
		r.Counts.OpportunitiesScanned, r.Counts.OpportunitiesInScope, r.Scope.Opportunities, r.Counts.EventsReplayed, r.Counts.ScannedEvents,
		r.Counts.MaterialEvents, r.Counts.OpportunitiesWithCase)
	fmt.Fprintf(&b, "**Provenance:** %s.\n\n**Method:** %s\n\n", r.Scope.Synthetic, r.Method)
	b.WriteString("| Rank | Account | Opportunity | Score | History events | Held-out event (time, source) | Held-out dimensions |\n|---|---|---|---|---|---|---|\n")
	for _, c := range r.Top {
		fmt.Fprintf(&b, "| %d | %s (%s) | %s (%s) | %.2f | %d | %s, %s/%s | %s |\n", c.Rank, c.AccountName, c.AccountID, c.OpportunityName, c.OpportunityID,
			c.Score.Total, len(c.History), c.HeldOut.OccurredAt, c.HeldOut.SourceSystem, c.HeldOut.SourceObjectID, strings.Join(c.HeldOut.Dimensions, ", "))
	}
	for _, c := range r.Top {
		fmt.Fprintf(&b, "\n## %d. %s / %s\n\n%s\n\n", c.Rank, c.AccountName, c.OpportunityName, c.WhySelected)
		b.WriteString("| # | Time (UTC) | Source | Layer | Account v | Deal v | Dimensions | Changed fields | Signals |\n|---|---|---|---|---|---|---|---|---|\n")
		for _, e := range c.History {
			fmt.Fprintf(&b, "| %d | %s | %s/%s | %s | %d | %d | %s | %s | %s |\n", e.Position, e.OccurredAt, e.SourceSystem, e.SourceObjectID, e.Layer,
				e.AccountStateVersion, e.OpportunityStateVersion, dash(e.Dimensions), dash(e.ChangedFields), dash(e.Signals))
		}
		h := c.HeldOut
		fmt.Fprintf(&b, "| **%d (held out)** | %s | %s/%s | %s | %d | %d | %s | %s | %s |\n", h.Position, h.OccurredAt, h.SourceSystem, h.SourceObjectID,
			h.Layer, h.AccountStateVersion, h.OpportunityStateVersion, dash(h.Dimensions), dash(h.ChangedFields), dash(h.Signals))
		fmt.Fprintf(&b, "\nScore parts: history dimensions %.2f, material events %.2f, revisited %.2f, held-out %.2f, novel %.2f, decision change %.2f. Provenance: %v.\n",
			c.Score.HistoryDimensionPts, c.Score.HistoryMaterialPts, c.Score.RevisitedPts, c.Score.HeldOutPts, c.Score.NovelPts, c.Score.DecisionPts, c.Provenance)
	}
	b.WriteString("\n" + strings.Join(prefixAll(r.Notes, "- "), "\n") + "\n")
	return []byte(b.String())
}

// signatureText renders the per-dimension history counts in contract order.
func signatureText(s Signature) string {
	var parts []string
	for _, d := range Dimensions {
		if n := s.History[d]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s x%d", d, n))
		}
	}
	return listOrNone(parts)
}

func dash(items []string) string {
	if len(items) == 0 {
		return "-"
	}
	return strings.Join(items, ", ")
}

func prefixAll(items []string, p string) []string {
	out := make([]string, len(items))
	for i, s := range items {
		out[i] = p + s
	}
	return out
}
