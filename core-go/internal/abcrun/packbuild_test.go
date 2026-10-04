package abcrun_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/abcrun"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

// The CRMArena sample export (fixtures/crmarena_sample) and a synthetic layer written here stand in for the git-ignored data.
const (
	sampleDeal    = "006Wt000007B62xIAC"
	sampleAccount = "001Wt00000PHVkAIAX"
	sampleEmail   = "02sWt000001zh67IAA" // the deal's real inbound email, 2023-11-06T11:15:00Z
)

func syntheticLayer(t *testing.T, events []normalize.SourceEvent) string {
	t.Helper()
	dir := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, "labels"), 0o755))
	must(os.MkdirAll(filepath.Join(dir, "events", sampleAccount), 0o755))
	must(os.WriteFile(filepath.Join(dir, "labels", "covered_deals.json"), []byte(`{"provenance":"synthetic:v1","deals":[]}`), 0o644))
	raw, err := json.Marshal(events)
	must(err)
	must(os.WriteFile(filepath.Join(dir, "events", sampleAccount, sampleDeal+".json"), raw, 0o644))
	return dir
}

func syntheticReply(id string, when time.Time, body string) normalize.SourceEvent {
	e := email("", id, &when, true, "buyer@securelinktech.com", "sam@ghostvendor.com", body)
	e.Origin, e.Provenance = "synthetic", "synthetic:v1"
	return e
}

func spec() abcrun.Spec {
	return abcrun.Spec{ID: "S-sample", Kind: "discriminating", DecisionPoint: "price_pushback", DealID: sampleDeal, AccountID: sampleAccount,
		SelectionReason: "fixture", Trigger: abcrun.Trigger{SourceObjectID: sampleEmail, SourceEventKey: "received", OccurredAt: time.Date(2023, 11, 6, 11, 15, 0, 0, time.UTC)}}
}

func TestBuildPackKeepsEverythingUpToTheTriggerAndNothingAfter(t *testing.T) {
	early := time.Date(2023, 11, 1, 9, 0, 0, 0, time.UTC)
	late := time.Date(2023, 11, 20, 9, 0, 0, 0, time.UTC)
	syn := syntheticLayer(t, []normalize.SourceEvent{syntheticReply("02sSYN1", early, priceBody), syntheticReply("02sSYNLATE", late, priceBody)})
	pack, err := abcrun.BuildPack(abcrun.BuildInput{ExportDir: repoFile(t, "fixtures/crmarena_sample"), SyntheticDir: syn, Specs: []abcrun.Spec{spec()}})
	if err != nil {
		t.Fatal(err)
	}
	s := pack.Situations[0]
	last := s.Events[len(s.Events)-1]
	if last.SourceObjectID != sampleEmail || last.SourceEventKey != "received" {
		t.Fatalf("the trigger must be the last event, got %s/%s", last.SourceObjectID, last.SourceEventKey)
	}
	have := map[string]bool{}
	for _, e := range s.Events {
		have[e.SourceObjectID] = true
		if e.OccurredAt.After(s.Trigger.OccurredAt) {
			t.Fatalf("future leakage: %s at %s", e.SourceObjectID, e.OccurredAt)
		}
	}
	if !have["02sSYN1"] || have["02sSYNLATE"] {
		t.Fatalf("the synthetic reply before the trigger must be in and the one after must not: %v", have)
	}
	if len(s.Truth["02sSYN1"]) != 1 || s.Truth["02sSYN1"][0].FieldPath != "objections" {
		t.Fatalf("the planted pricing objection must be reported for the synthetic email: %+v", s.Truth)
	}
	if len(pack.Company.People) == 0 || len(s.People) == 0 {
		t.Fatalf("a pack carries the vendor's employees and the account's contacts: %d / %d", len(pack.Company.People), len(s.People))
	}
	if err := pack.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestBuildPackRefusesATriggerThatIsNotInTheWorld(t *testing.T) {
	bad := spec()
	bad.Trigger.SourceObjectID = "02sDOESNOTEXIST"
	_, err := abcrun.BuildPack(abcrun.BuildInput{ExportDir: repoFile(t, "fixtures/crmarena_sample"), SyntheticDir: syntheticLayer(t, nil), Specs: []abcrun.Spec{bad}})
	if err == nil {
		t.Fatal("a pack whose trigger event is not in the world must not build")
	}
}

func TestPackCheckRefusesWrongVersionDuplicatesFutureEventsAndUnknownKinds(t *testing.T) {
	good := pack("discriminating")
	if err := good.Check(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(p *abcrun.Pack){
		"version":   func(p *abcrun.Pack) { p.Version = "abc_pack.v0" },
		"empty":     func(p *abcrun.Pack) { p.Situations = nil },
		"kind":      func(p *abcrun.Pack) { p.Situations[0].Kind = "mystery" },
		"duplicate": func(p *abcrun.Pack) { p.Situations = append(p.Situations, p.Situations[0]) },
		"future": func(p *abcrun.Pack) {
			when := p.Situations[0].Trigger.OccurredAt.Add(time.Hour)
			p.Situations[0].Events[0].OccurredAt = &when
		},
		"trigger": func(p *abcrun.Pack) { p.Situations[0].Trigger.SourceObjectID = "nope" },
	} {
		p := pack("discriminating")
		mutate(&p)
		if p.Check() == nil {
			t.Errorf("%s: Check accepted a pack it must refuse", name)
		}
	}
}

func TestTruthFromBodyReadsTheThreeObjectionTemplatesAndNothingElse(t *testing.T) {
	cases := map[string]string{
		"the pricing for Orbit is higher than what we had planned for this year":        "objections",
		"we need to understand how it would fit alongside the tools we already rely on": "objections",
		"our security and compliance team will have questions":                          "blockers",
	}
	for body, field := range cases {
		got := abcrun.TruthFromBody("Hi, " + body + ". Thanks.")
		if len(got) != 1 || string(got[0].FieldPath) != field || got[0].EvidenceQuote == "" {
			t.Errorf("%q: %+v", body, got)
		}
	}
	if got := abcrun.TruthFromBody("Thanks for the details, we are interested."); len(got) != 0 {
		t.Fatalf("a friendly email carries no planted fact: %+v", got)
	}
}

func TestSequenceIsDeterministicAndRestartsPerName(t *testing.T) {
	var a, b abcrun.Sequence
	a.Reset("S-1|B")
	b.Reset("S-1|B")
	first, second := a.Next(), a.Next()
	if first == second || first != b.Next() || second != b.Next() {
		t.Fatal("the same name must give the same ids, and ids must not repeat")
	}
	a.Reset("S-1|C")
	if a.Next() == first {
		t.Fatal("a different name must give different ids")
	}
}
