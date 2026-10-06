package crmarena

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

// repoPath finds a path relative to the repository root.
func repoPath(t *testing.T, rel string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "contracts", "schemas")); err == nil {
			return filepath.Join(dir, filepath.FromSlash(rel))
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repository root not found")
		}
		dir = parent
	}
}

func sample(t *testing.T) Snapshot {
	t.Helper()
	s, err := Load(repoPath(t, "fixtures/crmarena_sample"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func build(t *testing.T, s Snapshot) Result {
	t.Helper()
	r, err := Build(s)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// activityTypes normalizes every event and counts activity types.
func activityTypes(t *testing.T, events []Event) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, e := range events {
		act, err := normalize.Normalize(e.Source)
		if err != nil {
			t.Fatal(err)
		}
		out[act.Type()]++
	}
	return out
}

func TestSampleMapsEveryRecordToItsActivityType(t *testing.T) {
	s := sample(t)
	got := activityTypes(t, build(t, s).Events)
	closed := 0
	for _, c := range s.Cases {
		if c.ClosedDate != "" {
			closed++
		}
	}
	quotesWithStatus := 0
	for _, q := range s.Quotes {
		if q.Status != "" {
			quotesWithStatus++
		}
	}
	want := map[string]int{
		"ContactAdded": len(s.Contacts), "OpportunityStageChanged": len(s.Opportunities), "CRMTaskLogged": len(s.Tasks),
		"QuoteCreated": len(s.Quotes), "ContractSigned": len(s.Contracts), "OrderPlaced": len(s.Orders),
		"SupportTicketOpened": len(s.Cases), "SupportTicketResolved": closed, "ChatTranscriptReady": len(s.Chats),
		// accounts and opportunity creations, plus the quotes' final status
		"CRMFieldChanged": len(s.Accounts) + len(s.Opportunities) + quotesWithStatus,
	}
	emails := got["EmailSent"] + got["EmailReceived"] + got["EmailReply"]
	if emails != len(s.Emails) {
		t.Errorf("emails mapped = %d, want %d", emails, len(s.Emails))
	}
	for typ, n := range want {
		if got[typ] != n {
			t.Errorf("%s = %d, want %d", typ, got[typ], n)
		}
	}
}

func TestEventsAreInIngestOrderAndMasterDataComesFirst(t *testing.T) {
	r := build(t, sample(t))
	seenAccount := map[string]bool{}
	for i, e := range r.Events {
		if i > 0 && ingestLess(e, r.Events[i-1]) {
			t.Fatalf("event %d (%s) is out of order", i, e.Source.SourceObjectID)
		}
		if strings.HasPrefix(e.Source.SourceObjectID, "account:") {
			seenAccount[e.AccountID] = true
			continue
		}
		if e.AccountID != "" && !seenAccount[e.AccountID] {
			t.Fatalf("%s arrives before its account %s", e.Source.SourceObjectID, e.AccountID)
		}
	}
	if !r.Epoch().Equal(r.Events[0].OccurredAt()) {
		t.Error("epoch is not the first event time")
	}
}

func TestNoEventCarriesTheLoadDateOrTheSellerDomain(t *testing.T) {
	loadDate := time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC) // the org was loaded on 2025-04-20
	for _, e := range build(t, sample(t)).Events {
		if !e.OccurredAt().Before(loadDate) {
			t.Errorf("%s/%s dated %s: a load timestamp leaked into event time", e.Source.SourceObjectID, e.Source.SourceEventKey, e.OccurredAt())
		}
		for _, d := range []string{"techagents.com", "techdomain.com"} {
			if bytes.Contains(bytes.ToLower(e.Source.Payload), []byte("@"+d)) {
				t.Errorf("%s still carries a %s address", e.Source.SourceObjectID, d)
			}
		}
	}
}

func TestBuildIsDeterministicAndKeysAreUnique(t *testing.T) {
	s := sample(t)
	a, b := build(t, s), build(t, s)
	ja, _ := json.Marshal(a.SourceEvents())
	jb, _ := json.Marshal(b.SourceEvents())
	if !bytes.Equal(ja, jb) {
		t.Fatal("two builds of the same snapshot differ")
	}
	keys := map[string]bool{}
	for _, e := range a.Events {
		act, _ := normalize.Normalize(e.Source)
		if keys[act.IdempotencyKey()] {
			t.Fatalf("duplicate idempotency key for %s/%s", e.Source.SourceObjectID, e.Source.SourceEventKey)
		}
		keys[act.IdempotencyKey()] = true
	}
}

func payloadOf(t *testing.T, e Event) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(e.Source.Payload, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSnapshotOnlyValuesAreDatedAtTheDealsLastActivity(t *testing.T) {
	s := sample(t)
	r := build(t, s)
	windows, err := Windows(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range r.Events {
		key := e.Source.SourceEventKey
		switch {
		case strings.HasPrefix(e.Source.SourceObjectID, "opp:") && key == "created":
			if _, ok := payloadOf(t, e)["fields"].(map[string]any)["StageName"]; ok {
				t.Errorf("%s creation carries the snapshot stage", e.Source.SourceObjectID)
			}
		case strings.HasPrefix(key, "field:StageName:"), strings.HasPrefix(key, "field:Status:"):
			if w := windows[e.DealID]; w.HasActivity() && e.OccurredAt().Before(w.Last) {
				t.Errorf("%s %s dated %s, before the deal's last activity %s", e.Source.SourceObjectID, key, e.OccurredAt(), w.Last)
			}
		case strings.HasPrefix(e.Source.SourceObjectID, "case:") && key == "opened":
			if payloadOf(t, e)["closed_at"] != nil {
				t.Errorf("%s opened event already knows its close date", e.Source.SourceObjectID)
			}
		}
	}
}

func TestEmailDirectionFollowsTheSenderAndRepliesAreLinked(t *testing.T) {
	r := build(t, sample(t))
	inbound, outbound := 0, 0
	for _, e := range r.Events {
		if e.Source.SourceSystem != "email" {
			continue
		}
		p := payloadOf(t, e)
		from := p["from"].(map[string]any)["email"].(string)
		isOurs := normalize.IsOurEmail(from)
		if (p["direction"] == "outbound") != isOurs {
			t.Errorf("%s from %s has direction %v", e.Source.SourceObjectID, from, p["direction"])
		}
		if isOurs {
			outbound++
		} else {
			inbound++
		}
	}
	if inbound == 0 || outbound == 0 {
		t.Fatalf("sample should have both directions: inbound %d outbound %d", inbound, outbound)
	}
	if r.Stats.RepliesLinked == 0 {
		t.Error("no reply was linked")
	}
	if r.Stats.DerivedDomains != 3 || r.Stats.AccountsWithoutDomain != 0 {
		t.Errorf("domains: %+v", r.Stats)
	}
}

func TestBaseSubjectFoldsReplyMarkers(t *testing.T) {
	if got := baseSubject("RE: re:  Sharing   Case studies"); got != "sharing case studies" {
		t.Errorf("baseSubject = %q", got)
	}
}
