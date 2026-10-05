package crmarena

import (
	"encoding/json"
	"fmt"
	"strings"
)

// WP32 (HAR-131) open question 2, decided 2026-10-02. When the labelled synthetic layer is on, its stage path and
// outcome SUPERSEDE this loader's snapshot StageName event for the deals it covers. The base has no dated stage
// history (OpportunityHistory rows are 2025 load times, IsWon is always false), so the snapshot is a value dated
// heuristically at the last activity, not an observed event; omitting it removes nothing real. A deal whose REAL
// outcome is visible (a signed contract, or a quote whose final status is Accepted, Rejected or Denied) is never
// covered: it keeps its real outcome, so a synthetic outcome can never contradict a real event the agent sees.

// Real terminal outcomes.
const (
	RealWon  = "won"
	RealLost = "lost"
)

var quoteTerminal = map[string]string{"Accepted": RealWon, "Rejected": RealLost, "Denied": RealLost}

func objectType(e Event) string {
	var p struct {
		ObjectType string `json:"object_type"`
	}
	_ = json.Unmarshal(e.Source.Payload, &p)
	return p.ObjectType
}

// RealTerminalDeals returns the deals whose real outcome is visible in the events: won when a contract linked to
// the deal is signed or a quote ends Accepted, lost when a quote ends Rejected or Denied (won takes precedence).
func RealTerminalDeals(events []Event) map[string]string {
	out := map[string]string{}
	for _, e := range events {
		if e.DealID == "" || e.Source.SourceSystem != "crm" {
			continue
		}
		outcome := ""
		switch {
		case objectType(e) == "Contract":
			outcome = RealWon
		case objectType(e) == "Quote" && strings.HasPrefix(e.Source.SourceEventKey, "field:Status:"):
			outcome = quoteTerminal[strings.TrimPrefix(e.Source.SourceEventKey, "field:Status:")]
		}
		if outcome == RealWon || (outcome == RealLost && out[e.DealID] == "") {
			out[e.DealID] = outcome
		}
	}
	return out
}

// isSnapshotStage is the loader's snapshot StageName event of an opportunity (crm_events.go, phaseSnapshot).
func isSnapshotStage(e Event) bool {
	return e.phase == phaseSnapshot && e.Source.SourceSystem == "crm" &&
		strings.HasPrefix(e.Source.SourceEventKey, "field:StageName:") && objectType(e) == "Opportunity"
}

// SupersedeSnapshotStage drops the snapshot StageName event of every covered deal, keeping every other event in
// order. Covering a deal whose real outcome is visible is refused.
func SupersedeSnapshotStage(events []Event, covered map[string]bool) ([]Event, error) {
	real := RealTerminalDeals(events)
	for deal := range covered {
		if outcome, ok := real[deal]; ok {
			return nil, fmt.Errorf("crmarena: deal %s has a visible real outcome (%s); synthetic outcomes may not cover it", deal, outcome)
		}
	}
	out := make([]Event, 0, len(events))
	for _, e := range events {
		if covered[e.DealID] && isSnapshotStage(e) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}
