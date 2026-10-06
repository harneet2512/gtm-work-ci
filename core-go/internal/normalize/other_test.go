package normalize

import (
	"fmt"
	"testing"
)

func enrichmentJSON(subject string) string {
	return fmt.Sprintf(`{"kind":"enrichment","provider":"clearbit","subject":%s,
		"observed_at":"2026-09-28T06:00:00Z","facts":{"title":"VP Security","headcount":4200}}`, subject)
}

func documentJSON(sharedWith string) string {
	return fmt.Sprintf(`{"kind":"document","document_id":"doc-7","title":"SOC2 report","shared_at":"2026-10-01T16:00:00Z",
		"shared_by":"Dana@vendor.example","shared_with":%s}`, sharedWith)
}

const clockTickJSON = `{"kind":"clock_tick","account_ref":"account:AC-4","rule":"customer_silence","observed_at":"2026-10-05T00:00:00Z"}`

func TestNormalizeEnrichmentDocsAndClock(t *testing.T) {
	runCases(t, []normCase{
		{
			name: "enrichment by email -> EnrichmentUpdated with subject as mentioned",
			ev:   event(t, "enrichment", "clearbit:Priya@acme.com:2026-09-28T06:00:00Z", "observed", "", enrichmentJSON(`{"email":"Priya@acme.com"}`)),
			want: want{
				typ: "EnrichmentUpdated", occurred: "2026-09-28T06:00:00Z",
				participants: []Participant{part("priya@acme.com", "", "mentioned")},
				accountHint:  "acme.com",
				summary:      "Enrichment from clearbit for priya@acme.com",
			},
		},
		{
			name: "enrichment by domain has no participant",
			ev:   event(t, "enrichment", "clearbit:acme.com:2026-09-28T06:00:00Z", "observed", "", enrichmentJSON(`{"domain":"ACME.com","email":null}`)),
			want: want{typ: "EnrichmentUpdated", occurred: "2026-09-28T06:00:00Z", accountHint: "acme.com", summary: "Enrichment from clearbit for acme.com"},
		},
		{
			name: "enrichment about our own domain yields no hint",
			ev:   event(t, "enrichment", "clearbit:vendor.example:2026-09-28T06:00:00Z", "observed", "", enrichmentJSON(`{"domain":"vendor.example"}`)),
			want: want{typ: "EnrichmentUpdated", occurred: "2026-09-28T06:00:00Z", summary: "Enrichment from clearbit for vendor.example"},
		},
		{
			name: "document -> DocumentShared with recipient domain hint",
			ev:   event(t, "docs", "doc-7", "shared:2026-10-01T16:00:00Z", "", documentJSON(`["lee@vendor.example","Priya@Acme.com"]`)),
			want: want{
				typ: "DocumentShared", occurred: "2026-10-01T16:00:00Z",
				participants: []Participant{
					part("dana@vendor.example", "", "actor"),
					part("lee@vendor.example", "", "to"),
					part("priya@acme.com", "", "to"),
				},
				accountHint: "acme.com",
				summary:     "Document shared: SOC2 report",
			},
		},
		{
			name: "document key time may use a different UTC offset",
			ev:   event(t, "docs", "doc-7", "shared:2026-10-01T18:00:00+02:00", "", documentJSON(`["priya@acme.com"]`)),
			want: want{
				typ: "DocumentShared", occurred: "2026-10-01T16:00:00Z",
				participants: []Participant{part("dana@vendor.example", "", "actor"), part("priya@acme.com", "", "to")},
				accountHint:  "acme.com", summary: "Document shared: SOC2 report",
			},
		},
		{
			name: "customer_silence tick -> CustomerWentSilent hinted by account_ref",
			ev:   event(t, "ghost.clock", "account:AC-4:customer_silence:2026-10-05", "tick", "", clockTickJSON),
			want: want{
				typ: "CustomerWentSilent", occurred: "2026-10-05T00:00:00Z",
				accountKind: HintCRM, accountHint: "account:AC-4",
				summary: "No customer activity detected for account:AC-4",
			},
		},
	})
}

func TestNormalizeEnrichmentDocsAndClockRejectInvalidInput(t *testing.T) {
	runCases(t, []normCase{
		{name: "enrichment key other than observed", ev: event(t, "enrichment", "clearbit:a.com:x", "updated", "", enrichmentJSON(`{"domain":"a.com"}`)), errCode: CodeUnsupportedEvent},
		{name: "enrichment without email or domain", ev: event(t, "enrichment", "clearbit:x:x", "observed", "", enrichmentJSON(`{}`)), errCode: CodeInvalidEvent},
		{name: "enrichment object id lacks provider prefix", ev: event(t, "enrichment", "other:a.com:x", "observed", "", enrichmentJSON(`{"domain":"a.com"}`)), errCode: CodeInvalidEvent},
		{name: "enrichment facts missing", ev: event(t, "enrichment", "clearbit:a.com:x", "observed", "", `{"kind":"enrichment","provider":"clearbit","subject":{"domain":"a.com"},"observed_at":"2026-09-28T06:00:00Z"}`), errCode: CodeInvalidEvent},
		{name: "document key does not match shared_at", ev: event(t, "docs", "doc-7", "shared:2026-10-02T16:00:00Z", "", documentJSON(`["priya@acme.com"]`)), errCode: CodeInvalidEvent},
		{name: "document key without shared: prefix", ev: event(t, "docs", "doc-7", "viewed", "", documentJSON(`["priya@acme.com"]`)), errCode: CodeUnsupportedEvent},
		{name: "document key with unparseable time", ev: event(t, "docs", "doc-7", "shared:soon", "", documentJSON(`["priya@acme.com"]`)), errCode: CodeInvalidEvent},
		{name: "document object id differs", ev: event(t, "docs", "doc-8", "shared:2026-10-01T16:00:00Z", "", documentJSON(`["priya@acme.com"]`)), errCode: CodeInvalidEvent},
		{name: "document recipient is not an email", ev: event(t, "docs", "doc-7", "shared:2026-10-01T16:00:00Z", "", documentJSON(`["priya"]`)), errCode: CodeInvalidEvent},
		{name: "clock key other than tick", ev: event(t, "ghost.clock", "account:AC-4:customer_silence:2026-10-05", "ping", "", clockTickJSON), errCode: CodeUnsupportedEvent},
		{name: "clock rule without a mapping", ev: event(t, "ghost.clock", "account:AC-4:commitment_due:2026-10-05", "tick", "", `{"kind":"clock_tick","account_ref":"account:AC-4","rule":"commitment_due","observed_at":"2026-10-05T00:00:00Z"}`), errCode: CodeUnsupportedEvent},
		{name: "clock unknown rule", ev: event(t, "ghost.clock", "account:AC-4:nap:2026-10-05", "tick", "", `{"kind":"clock_tick","account_ref":"account:AC-4","rule":"nap","observed_at":"2026-10-05T00:00:00Z"}`), errCode: CodeInvalidEvent},
		{name: "clock object id does not start with account_ref:rule:", ev: event(t, "ghost.clock", "tick-1", "tick", "", clockTickJSON), errCode: CodeInvalidEvent},
		{name: "clock missing account_ref", ev: event(t, "ghost.clock", "x:customer_silence:d", "tick", "", `{"kind":"clock_tick","rule":"customer_silence","observed_at":"2026-10-05T00:00:00Z"}`), errCode: CodeInvalidEvent},
	})
}
