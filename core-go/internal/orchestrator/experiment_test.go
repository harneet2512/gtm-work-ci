package orchestrator_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/orchestrator"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// sequence is a deterministic Config.NewID: the same run draws the same ids every time.
func sequence() func() string {
	n := 0
	return func() string {
		n++
		return fmt.Sprintf("0e9e0000-0000-4000-8000-%012d", n)
	}
}

func TestConfiguredIDsAreDrawnFromTheInjectedSequenceAndNothingElse(t *testing.T) {
	sc := newScene(t)
	fw := newFake(sc)
	svc := serviceWith(t, fw, func(c *orchestrator.Config) { c.NewID = sequence() })
	out := mustRun(t, svc, sc.RunID)
	for _, id := range []string{out.SetID, out.EpisodeID} {
		if !strings.HasPrefix(id, "0e9e0000-0000-4000-8000-") {
			t.Fatalf("id %s did not come from the injected sequence", id)
		}
	}
	guidance := scalar(t, `SELECT decision_guidance_id::text FROM agent_runs WHERE id = $1::uuid`, sc.RunID)
	bundle := scalar(t, `SELECT min(id::text) FROM eval_bundles WHERE agent_run_id = $1::uuid`, sc.RunID)
	for _, id := range []string{guidance, bundle} {
		if !strings.HasPrefix(id, "0e9e0000-0000-4000-8000-") {
			t.Fatalf("id %s did not come from the injected sequence", id)
		}
	}
}

func TestWithoutAnInjectedSequenceIDsAreRandom(t *testing.T) {
	sc := newScene(t)
	out := mustRun(t, service(t, newFake(sc)), sc.RunID)
	if strings.HasPrefix(out.SetID, "0e9e0000-0000-4000-8000-") {
		t.Fatalf("a production run drew from a fixed sequence: %s", out.SetID)
	}
}

func TestDeterministicBlockingScoresAnyCandidateWithoutWritingOrCallingAModel(t *testing.T) {
	sc := newScene(t)
	fw := newFake(sc)
	svc := serviceWith(t, fw, func(*orchestrator.Config) {})
	mustRun(t, svc, sc.RunID)
	calls := fw.strategies
	judges := len(fw.judges)
	before := count(t, `SELECT count(*) FROM eval_runs WHERE agent_run_id = $1::uuid`, sc.RunID)

	clean := fw.threeCandidates()[0]
	findings, err := svc.DeterministicBlocking(bg, sc.RunID, clean)
	if err != nil {
		t.Fatal(err)
	}
	if findings == nil {
		t.Fatal("no findings must be an empty list, not nil")
	}

	broken := clean
	broken.To = []workerclient.Recipient{{PersonID: "00000000-0000-4000-8000-0000000000aa", Role: "to", Why: "nobody knows this person"}}
	blocked, err := svc.DeterministicBlocking(bg, sc.RunID, broken)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked) <= len(findings) {
		t.Fatalf("a send to a person the account does not have must fail more deterministic evals than a send to a known contact: %v vs %v", blocked, findings)
	}
	if fw.strategies != calls || len(fw.judges) != judges {
		t.Fatalf("scoring must call no model: %d generations (was %d), %d judge calls (was %d)", fw.strategies, calls, len(fw.judges), judges)
	}
	if after := count(t, `SELECT count(*) FROM eval_runs WHERE agent_run_id = $1::uuid`, sc.RunID); after != before {
		t.Fatalf("scoring wrote eval rows: %d -> %d", before, after)
	}
}

func TestDeterministicBlockingRefusesAMissingRun(t *testing.T) {
	sc := newScene(t)
	svc := service(t, newFake(sc))
	if _, err := svc.DeterministicBlocking(bg, "00000000-0000-4000-8000-000000000000", workerclient.Candidate{}); err == nil {
		t.Fatal("a run that does not exist must be refused")
	}
}
