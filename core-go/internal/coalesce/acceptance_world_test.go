package coalesce_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstest"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
)

// This file holds the world loading and seeding used by the gold acceptance test. WP5 (entity
// resolution) builds the real resolver in parallel, so the test stands in for it: accounts and
// opportunities come from the CRM "created" events (as ingest/world_replay_test.go does) and
// people plus every raw-identity mapping come from the gold checkpoints' entity lists. That makes
// identity an oracle, so the acceptance measures adjudication, folding and coalescing, not resolution.

type worldEvent struct {
	ingest.NamedEvent
	Rel     string // path relative to fixtures/, slash separated
	Account string
	At      time.Time
}

func relPath(fixtures, path string) string {
	rel, err := filepath.Rel(fixtures, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// accountOf names the account an event file belongs to: world files by directory, live files by
// the "<account>_" prefix of the file name.
func accountOf(rel string) string {
	parts := strings.Split(rel, "/")
	if len(parts) >= 3 && parts[0] == "world" && parts[1] == "accounts" {
		return parts[2]
	}
	base := parts[len(parts)-1]
	account, _, _ := strings.Cut(base, "_")
	return account
}

// loadWorldEvents reads fixtures/world/accounts/*/events/*.json and fixtures/live/*.json.
func loadWorldEvents(t *testing.T, fixtures string) []worldEvent {
	t.Helper()
	var paths []string
	for _, pattern := range []string{"world/accounts/*/events/*.json", "live/*.json"} {
		m, err := filepath.Glob(filepath.Join(fixtures, filepath.FromSlash(pattern)))
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, m...)
	}
	var out []worldEvent
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		evs, err := ingest.DecodeEvents(raw)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		for i, ev := range evs {
			we := worldEvent{NamedEvent: ingest.NamedEvent{File: p, Index: i, Event: ev}, Rel: relPath(fixtures, p), At: time.Unix(0, 0).UTC()}
			we.Account = accountOf(we.Rel)
			if ev.OccurredAt != nil {
				we.At = ev.OccurredAt.UTC()
			}
			out = append(out, we)
		}
	}
	return out
}

// ingestOrder is the account's events in ingest order, decided by claimstest (one definition for the
// scorer and the driver): world files in lexical file order, then the live files in the order the gold
// declares (live_files), or by occurred_at when it declares none.
func ingestOrder(t *testing.T, events []worldEvent, account string, golds []claimstest.Gold) []worldEvent {
	t.Helper()
	var world, live []worldEvent
	for _, e := range events {
		switch {
		case e.Account != account:
		case strings.HasPrefix(e.Rel, "world/"):
			world = append(world, e)
		default:
			live = append(live, e)
		}
	}
	sort.SliceStable(world, func(i, j int) bool { return world[i].Rel < world[j].Rel })
	sort.SliceStable(live, func(i, j int) bool {
		if !live[i].At.Equal(live[j].At) {
			return live[i].At.Before(live[j].At)
		}
		return live[i].Rel < live[j].Rel
	})
	byTime := make([]string, len(live))
	byRel := map[string]worldEvent{}
	for i, e := range live {
		byTime[i], byRel[e.Rel] = e.Rel, e
	}
	var accountGolds []claimstest.Gold
	for _, g := range golds {
		if g.Account == account {
			accountGolds = append(accountGolds, g)
		}
	}
	liveOrder, err := claimstest.LiveOrder(accountGolds, byTime)
	if err != nil {
		t.Fatalf("%v", err) // a gold/fixture mismatch
	}
	out := append([]worldEvent(nil), world...)
	for _, rel := range liveOrder {
		out = append(out, byRel[rel])
	}
	return out
}

type crmRecord struct {
	ObjectType      string                    `json:"object_type"`
	RecordID        string                    `json:"record_id"`
	AccountRecordID string                    `json:"account_record_id"`
	Created         bool                      `json:"created"`
	Fields          map[string]map[string]any `json:"fields"`
}

func (r crmRecord) field(name string) string {
	if v, ok := r.Fields[name]["new"]; ok {
		return fmt.Sprint(v)
	}
	return ""
}

func mapping(t *testing.T, entityType, entityID, system, key string) {
	t.Helper()
	if _, err := env.DB.Exec(`INSERT INTO entity_source_mappings (entity_type, entity_id, source_system, source_key, confidence, method)
		VALUES ($1, $2::uuid, $3, $4, 1, 'seed')`, entityType, entityID, system, key); err != nil {
		t.Fatalf("seed mapping %s/%s: %v", system, key, err)
	}
}

// seedWorld creates accounts and opportunities from the CRM created events, then people and their
// identity mappings from the gold entities. It returns person key -> database id.
func seedWorld(t *testing.T, events []worldEvent, golds []claimstest.Gold) map[string]string {
	t.Helper()
	resetAll(t)
	accounts := map[string]string{}
	var opps []crmRecord
	for _, e := range events {
		if e.Event.SourceSystem != "crm" {
			continue
		}
		var rec crmRecord
		if err := json.Unmarshal(e.Event.Payload, &rec); err != nil || !rec.Created {
			continue
		}
		if rec.ObjectType == "Opportunity" {
			opps = append(opps, rec)
			continue
		}
		if rec.ObjectType != "Account" {
			continue
		}
		domain := strings.TrimPrefix(strings.ToLower(rec.field("Website")), "www.")
		id := scalar(t, `INSERT INTO accounts (name, domain) VALUES ($1, NULLIF($2, '')) RETURNING id::text`, rec.field("Name"), domain)
		accounts[rec.RecordID] = id
		mapping(t, "account", id, "crm", rec.RecordID)
		if slug, _, _ := strings.Cut(strings.ToLower(rec.field("Name")), " "); slug != "" {
			mapping(t, "account", id, "slack", "#deal-"+slug)
		}
	}
	motions := map[string]string{"Expansion": "expansion", "Renewal": "renewal"}
	for _, rec := range opps {
		motion := motions[rec.field("Type")]
		if motion == "" {
			motion = "new_business"
		}
		id := scalar(t, `INSERT INTO opportunities (account_id, name, motion) VALUES ($1::uuid, $2, $3) RETURNING id::text`,
			accounts[rec.AccountRecordID], rec.field("Name"), motion)
		mapping(t, "opportunity", id, "crm", rec.RecordID)
	}

	return seedPeople(t, accounts, golds)
}

// seedPeople creates the gold persons (contacts by email domain, employees otherwise) and every raw-identity mapping.
func seedPeople(t *testing.T, accounts map[string]string, golds []claimstest.Gold) map[string]string {
	t.Helper()
	people := claimstest.PeopleFrom(golds)
	byDomain := map[string]string{}
	for _, id := range accounts {
		byDomain[scalar(t, `SELECT domain FROM accounts WHERE id = $1::uuid`, id)] = id
	}
	personID := map[string]string{}
	keys := make([]string, 0, len(people))
	for k := range people {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		p := people[key]
		domain := p.Email[strings.LastIndex(p.Email, "@")+1:]
		if acct, ok := byDomain[domain]; ok {
			personID[key] = scalar(t, `INSERT INTO people (kind, display_name, primary_email, account_id) VALUES ('contact', $1, $2, $3::uuid) RETURNING id::text`, p.DisplayName, p.Email, acct)
		} else {
			personID[key] = scalar(t, `INSERT INTO people (kind, display_name, primary_email) VALUES ('employee', $1, $2) RETURNING id::text`, p.DisplayName, p.Email)
		}
	}
	seen := map[string]bool{}
	for _, g := range golds {
		for _, e := range g.Expected.Entities {
			id, ok := personID[e.Key]
			if !ok {
				continue
			}
			for _, raw := range e.SourceIdentities {
				system, key, _ := strings.Cut(raw, ":")
				if system == "docs" || system == "domain" || seen[system+"|"+key] {
					continue
				}
				seen[system+"|"+key] = true
				mapping(t, "person", id, system, key)
			}
		}
	}
	return personID
}

// A second live file may land between the world and the existing live file (PR #4 adds
// live/acme_mnda_countersigned.json, 09-28, before live/acme_marco_soc2_email.json, 09-29): live files
// are ordered by occurred_at, not by name, so the checkpoint driver needs no change.
func TestIngestOrderPutsLiveFilesAfterWorldFilesInTimeOrder(t *testing.T) {
	at := func(day int) time.Time { return time.Date(2026, 9, day, 12, 0, 0, 0, time.UTC) }
	events := []worldEvent{
		{Rel: "live/acme_marco_soc2_email.json", Account: "acme", At: at(29)},
		{Rel: "world/accounts/acme/events/002_b.json", Account: "acme", At: at(2)},
		{Rel: "live/acme_mnda_countersigned.json", Account: "acme", At: at(28)},
		{Rel: "world/accounts/acme/events/001_a.json", Account: "acme", At: at(3)},
		{Rel: "world/accounts/beta/events/001_x.json", Account: "beta", At: at(1)},
	}
	var got []string
	for _, e := range ingestOrder(t, events, "acme", nil) {
		got = append(got, e.Rel)
	}
	want := "world/accounts/acme/events/001_a.json,world/accounts/acme/events/002_b.json,live/acme_mnda_countersigned.json,live/acme_marco_soc2_email.json"
	if strings.Join(got, ",") != want {
		t.Fatalf("order = %v", got)
	}
	for rel, account := range map[string]string{"world/accounts/northstar/events/001_x.json": "northstar", "live/beta_nonmaterial_event.json": "beta", "live/acme_mnda_countersigned.json": "acme"} {
		if accountOf(rel) != account {
			t.Errorf("accountOf(%s) = %s, want %s", rel, accountOf(rel), account)
		}
	}
}
