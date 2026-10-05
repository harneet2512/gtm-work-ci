package normalize

import (
	"strings"
	"time"
)

type clockPayload struct {
	Kind       string         `json:"kind"`
	AccountRef string         `json:"account_ref"`
	Rule       string         `json:"rule"`
	ObservedAt time.Time      `json:"observed_at"`
	Details    map[string]any `json:"details"`
}

// normalizeClock implements the CustomerWentSilent row. Only the customer_silence rule has a
// mapping; the other contract rules are well-formed but unsupported.
func normalizeClock(ev SourceEvent) (Activity, error) {
	if ev.SourceEventKey != "tick" {
		return Activity{}, unsupported("ghost.clock event key %q has no mapping (want tick)", ev.SourceEventKey)
	}
	p, err := decodePayload[clockPayload](ev, "clock_tick")
	if err != nil {
		return Activity{}, err
	}
	if strings.TrimSpace(p.AccountRef) == "" || p.ObservedAt.IsZero() {
		return Activity{}, invalid("clock tick requires account_ref and observed_at")
	}
	switch p.Rule {
	case "customer_silence":
	case "commitment_due", "next_meeting_missing":
		return Activity{}, unsupported("clock rule %q has no activity mapping", p.Rule)
	default:
		return Activity{}, invalid("clock rule %q is not a known rule", p.Rule)
	}
	if !strings.HasPrefix(ev.SourceObjectID, p.AccountRef+":"+p.Rule+":") {
		return Activity{}, invalid("source_object_id %q must start with \"%s:%s:\"", ev.SourceObjectID, p.AccountRef, p.Rule)
	}

	act := newActivity(ev, "CustomerWentSilent", p.ObservedAt)
	act.accountHints = hintOf(HintCRM, p.AccountRef)
	act.summary = summarize("No customer activity detected for " + p.AccountRef)
	return act, nil
}
