package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/crmarena"
)

// copyWithLateContact copies the sample and adds one contact whose only appearance is an email on a
// current deal of the split, sent after the cutoff. It returns the directory and the new contact's id.
func copyWithLateContact(t *testing.T, sample string, split crmarena.Split) (string, string) {
	t.Helper()
	dir := t.TempDir()
	entries, err := os.ReadDir(sample)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			raw, err := os.ReadFile(filepath.Join(sample, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, e.Name()), raw, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	var emails, contacts, opps []map[string]any
	readJSON(t, filepath.Join(dir, "EmailMessage.json"), &emails)
	readJSON(t, filepath.Join(dir, "Contact.json"), &contacts)
	readJSON(t, filepath.Join(dir, "Opportunity.json"), &opps)
	deal := split.Current[0]
	var rep, domain, account string
	for _, o := range opps {
		if o["Id"] == deal {
			account = o["AccountId"].(string)
		}
	}
	for _, e := range emails {
		if e["RelatedToId"] != deal || rep != "" {
			continue
		}
		for _, field := range []string{"FromAddress", "ToAddress"} {
			for _, addr := range strings.Split(strings.ToLower(strings.TrimSpace(e[field].(string))), ";") {
				if strings.HasSuffix(addr, "@techagents.com") || strings.HasSuffix(addr, "@techdomain.com") {
					rep = strings.TrimSpace(addr)
				}
			}
		}
	}
	if rep == "" {
		t.Fatalf("deal %s has no email with one of the seller's reps", deal)
	}
	for _, c := range contacts {
		if addr, _ := c["Email"].(string); addr != "" && c["AccountId"] == account {
			domain = addr[strings.Index(addr, "@")+1:]
			break
		}
	}
	const id = "003LATE0000000001"
	contacts = append(contacts, map[string]any{"Id": id, "AccountId": account, "FirstName": "Lena", "LastName": "Latecomer",
		"Email": "lena@" + domain, "Title": "CFO", "Department": "Finance"})
	emails = append(emails, map[string]any{"Id": "02sLATE0000000001", "RelatedToId": deal, "FromAddress": "lena@" + domain, "ToAddress": rep,
		"Subject": "Joining the evaluation", "TextBody": "Hello, I will join.", "MessageDate": "2023-11-20T09:00:00.000+0000"})
	writeJSON(t, filepath.Join(dir, "EmailMessage.json"), emails)
	writeJSON(t, filepath.Join(dir, "Contact.json"), contacts)
	return dir, id
}

func readJSON(t *testing.T, path string, into any) {
	t.Helper()
	if err := json.Unmarshal(mustRead(t, path), into); err != nil {
		t.Fatal(err)
	}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestALateContactArrivesWithItsFirstEmailInTheReplay: a stakeholder who first writes on a current deal
// after the cutoff is not in the --until timeline, and the replay creates them immediately before that
// email, so a "new stakeholder entered" change can fire.
func TestALateContactArrivesWithItsFirstEmailInTheReplay(t *testing.T) {
	sample := abs(t, crmarenaSample)
	t.Chdir(t.TempDir())
	t.Setenv("DATABASE_URL", "")
	var out bytes.Buffer
	if err := run([]string{"crmarena-split", "--cutoff", "2023-11-01", sample, "split.json"}, &out); err != nil {
		t.Fatal(err)
	}
	split, err := readSplit("split.json")
	if err != nil {
		t.Fatal(err)
	}
	dir, id := copyWithLateContact(t, sample, split)
	// The split is recomputed on the copy: the added email extends the deal's last event, not its role.
	if err := run([]string{"crmarena-split", "--cutoff", "2023-11-01", dir, "split2.json"}, &out); err != nil {
		t.Fatal(err)
	}
	replay := replayed(t, "--split", "split2.json", dir)
	cut, _ := time.Parse(time.RFC3339, sampleCutoff)
	var contactAt int = -1
	for i, e := range replay {
		if e.SourceObjectID == "contact:"+id {
			contactAt = i
		}
	}
	if contactAt < 0 || contactAt+1 == len(replay) {
		t.Fatalf("contact %s is not introduced by the replay (position %d of %d)", id, contactAt, len(replay))
	}
	next := replay[contactAt+1]
	if next.SourceObjectID != "02sLATE0000000001" || !next.OccurredAt.Equal(*replay[contactAt].OccurredAt) || replay[contactAt].OccurredAt.Before(cut) {
		t.Errorf("the contact must be created at the instant of its first email, got %s then %s at %s",
			replay[contactAt].SourceObjectID, next.SourceObjectID, next.OccurredAt)
	}
	var kept bytes.Buffer
	if err := run([]string{"import-crmarena", "--dry-run", "--until", sampleCutoff, dir}, &kept); err != nil {
		t.Fatal(err)
	}
	for _, e := range crmarenaAsOf(t, dir, cut) {
		if e.Source.SourceObjectID == "contact:"+id {
			t.Errorf("contact %s is in the --until timeline", id)
		}
	}
}

func crmarenaAsOf(t *testing.T, dir string, cut time.Time) []crmarena.Event {
	t.Helper()
	snap, err := crmarena.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := crmarena.Build(snap)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := crmarena.AsOf(res.Events, cut)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
