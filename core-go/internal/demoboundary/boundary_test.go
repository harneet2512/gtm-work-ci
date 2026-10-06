package demoboundary

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func load(t *testing.T) (*Boundary, string) {
	t.Helper()
	root, err := RepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	b, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return b, root
}

func termIDs(hits []Hit) []string {
	ids := make([]string, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, h.TermID)
	}
	return ids
}

func TestScanFlagsSeededViolations(t *testing.T) {
	b, _ := load(t)
	cases := map[string]string{
		"Run ghostctl demo play to release Event N":         "ghostctl",
		"Then run scripts/demo/demo.ps1 verify":             "repo_scripts",
		"double-click start-demo.ps1":                       "shell_scripts",
		"Use the Ghost CLI":                                 "cli_word",
		"Open a terminal and press enter":                   "terminal_phrases",
		"cd core-go && go run ./cmd/core":                   "build_and_test_commands",
		"psql $DATABASE_URL":                                "service_and_db_commands",
		"See the confirmation matrix in demo-report.md":     "proof_plumbing",
		"Blind A/B/C shows B > A":                           "abc_experiment",
		"knowledge uplift":                                  "uplift",
		"no negative transfer":                              "negative_transfer",
		"inject irrelevant knowledge":                       "irrelevant_knowledge_injection",
		"this is the counterfactual draft":                  "counterfactual",
		"served by fakecore":                                "fake_services",
		"proof-har129 wrote the artifacts":                  "proof_plumbing",
		"cliff posted it from the command line":             "terminal_phrases",
		"ghostctl freeze printed the manifest id":           "ghostctl",
		"python -m bench.uplift run":                        "build_and_test_commands",
		"GhostCTL is the operator tool":                     "ghostctl",
		"the bootstrap.py say command posts to #ghost-demo": "repo_scripts",
		"a counterfactual arm and the B>C claim":            "counterfactual",
		"run `npm run dev` before Play":                     "build_and_test_commands",
		"Causal attribution of the improvement":             "uplift",
		"knowledge-uplift experiment":                       "uplift",
		"the irrelevant-knowledge arm":                      "irrelevant_knowledge_injection",
		"open the terminal window":                          "terminal_phrases",
		"docker compose up":                                 "service_and_db_commands",
		"artifacts/har129/run-1 holds the evidence":         "proof_plumbing",
		"demosmoke verify passes":                           "fake_services",
		"Negative-transfer rate 0":                          "negative_transfer",
		"migrate up first":                                  "service_and_db_commands",
		"pytest worker-py":                                  "build_and_test_commands",
		"curl the core":                                     "service_and_db_commands",
		"demo_smoke.py posts the fixture":                   "repo_scripts",
		"the proof harness":                                 "proof_plumbing",
		"vitest run":                                        "build_and_test_commands",
		"this is the ghostcli":                              "ghostcli",
		"start it from a terminal command":                  "terminal_phrases",
		"slackfake recorded it":                             "fake_services",
		"npx playwright test":                               "build_and_test_commands",
		"go test -p 1 ./...":                                "build_and_test_commands",
		"scripts/ci/mirror.sh origin/main":                  "repo_scripts",
		"goose up":                                          "service_and_db_commands",
		"run arm B with context-only knowledge":             "abc_experiment",
		"ghostctl-free abc-pack build":                      "ghostctl",
		"irrelevant knowledge changes nothing (C equals A)": "abc_experiment",
		"Ghost's pick":                                      "legacy_brand",
		"How GHOST checks its work":                         "legacy_brand",
		"Ghost drafted 3 moves":                             "legacy_brand",
		"the ghost-demo channel":                            "legacy_brand",
		"Dana Kim (dana@ghostvendor.com)":                   "seller_domain",
	}
	for text, want := range cases {
		if ids := termIDs(b.Scan(text)); !contains(ids, want) {
			t.Errorf("Scan(%q) = %v, want it to flag %s", text, ids, want)
		}
	}
}

func TestScanLeavesProductLanguageAlone(t *testing.T) {
	b, _ := load(t)
	clean := []string{
		"Press Play to release the next event",
		"Strategy A is recommended; you chose Strategy B",
		"Choose B",
		"CONFIRMED and REJECTED transitions are terminal",
		"Rule check · AI judge · Run audit · Edit analysis",
		"Cliff noticed that Acme's security lead joined the thread",
		"Inspect evals",
		"View trace",
		"Knowledge applied: renewal timing rule",
		"Why did this action change?",
		"Decision & Learning",
		"System diagnostics: tokens, latency, cost",
		"the command center",
		"marshal the evidence",
		"Irrelevant knowledge rejected",
		"You chose B over A",
		"Choose Strategy A/B/C",
		"a 5% renewal price uplift",
		"the transition is in a terminal state",
		"irrelevant precedent intrusion",
		"gtm_ai's pick",
		"How gtm_ai checks its work",
		"gtm_ai Demo - Start",
	}
	for _, text := range clean {
		if hits := b.Scan(text); len(hits) != 0 {
			t.Errorf("Scan(%q) flagged %v; product language must not be a violation", text, hits)
		}
	}
}

func TestTheContractKeepsTheBoundary(t *testing.T) {
	b, _ := load(t)
	if problems := b.Check(); len(problems) != 0 {
		t.Fatalf("contracts/demo/boundary.v1.json breaks the HAR-129 boundary:\n  %s", strings.Join(problems, "\n  "))
	}
}

func mutated(t *testing.T, fn func(*Boundary)) *Boundary {
	t.Helper()
	_, root := load(t)
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ContractPath)))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	again, _ := json.Marshal(doc)
	b, err := Parse(again)
	if err != nil {
		t.Fatal(err)
	}
	fn(b)
	return b
}

func TestCheckRejectsSeededBoundaryViolations(t *testing.T) {
	cases := map[string]func(*Boundary){
		"a terminal step in the audience flow": func(b *Boundary) {
			b.AudienceFlow[1].What = "The presenter runs ghostctl demo play in a terminal."
		},
		"a proof step in the audience flow": func(b *Boundary) {
			b.AudienceFlow = append(b.AudienceFlow, Step{Step: len(b.AudienceFlow) + 1, Surface: "web_control_plane",
				What: "Show the proof harness confirmation matrix."})
		},
		"a third surface": func(b *Boundary) {
			b.AudienceSurface = append(b.AudienceSurface, Surface{ID: "terminal", Name: "Terminal", Job: "run commands"})
		},
		"a step on another surface": func(b *Boundary) { b.AudienceFlow[0].Surface = "terminal" },
		"Play moved to Slack": func(b *Boundary) {
			b.AudienceFlow[1].Surface = "cliff_in_slack"
		},
		"no trigger": func(b *Boundary) { b.AudienceFlow[1].Trigger = false },
		"a script as the trigger": func(b *Boundary) {
			b.VisibleTrigger.Control = "demo.ps1 play"
		},
		"Cliff speaks before Play": func(b *Boundary) {
			b.AudienceFlow[0].Surface = "cliff_in_slack"
		},
		"a reworded invariant": func(b *Boundary) { b.Invariant = "The demo is mostly web and Slack." },
		"an A/B/C step": func(b *Boundary) {
			b.AudienceFlow[10].What = "Run the A/B/C uplift experiment."
		},
		"Cliff's play_next without a confirmation": func(b *Boundary) { b.VisibleTrigger.Also[0].RequiresConfirmation = false },
		"Cliff's play_next calling another route": func(b *Boundary) {
			b.VisibleTrigger.Also[0].CoreEndpoint = "POST /replay/play"
		},
		"a second Slack trigger": func(b *Boundary) {
			b.VisibleTrigger.Also = append(b.VisibleTrigger.Also, b.VisibleTrigger.Also[0])
		},
		"no Ask Cliff DM":             func(b *Boundary) { b.AskCliff.DM = false },
		"no Ask Cliff mention thread": func(b *Boundary) { b.AskCliff.MentionInThread = false },
		"a top-level message beyond M1 M2 M3": func(b *Boundary) {
			b.AskCliff.ChannelTopLevelMessages = append(b.AskCliff.ChannelTopLevelMessages, "answers")
		},
		"an email send tool":          func(b *Boundary) { b.AskCliff.Actions = append(b.AskCliff.Actions, "send_email") },
		"a CRM write action":          func(b *Boundary) { b.AskCliff.Actions = append(b.AskCliff.Actions, "crm_write") },
		"a draft that is not dry run": func(b *Boundary) { b.AskCliff.DryRunOnly = []string{"crm_update_preview"} },
	}
	for name, fn := range cases {
		if problems := mutated(t, fn).Check(); len(problems) == 0 {
			t.Errorf("%s: Check found no problem", name)
		}
	}
}

func TestTheContractAllowsExactlyTheApprovedAskCliffSurface(t *testing.T) {
	b, _ := load(t)
	if !b.AskCliff.DM || !b.AskCliff.MentionInThread {
		t.Fatalf("Ask Cliff must answer in a DM and in a mention thread: %+v", b.AskCliff)
	}
	if got := strings.Join(b.AskCliff.Actions, ","); got != "play_next,demo_status" {
		t.Errorf("actions = %s", got)
	}
	if got := strings.Join(b.AskCliff.DryRunOnly, ","); got != "draft_followup,crm_update_preview" {
		t.Errorf("dry_run_only = %s", got)
	}
	if len(b.VisibleTrigger.Also) != 1 || b.VisibleTrigger.Also[0].Control != "play_next" ||
		b.VisibleTrigger.Also[0].CoreEndpoint != b.VisibleTrigger.CoreEndpoint {
		t.Errorf("Cliff's play_next must call the same route as web Play: %+v", b.VisibleTrigger)
	}
}

func TestWalkthroughExtractsOnlyTheAudienceSection(t *testing.T) {
	md := "# Demo\n\n## Before the demo (operator, hidden)\n\n```\nghostctl demo up\n```\n\n" +
		"## Audience walkthrough\n\n1. Press Play.\n2. Cliff posts Message 1.\n\n### Notes\nstill inside\n\n" +
		"## After the demo (internal)\n\nghostctl proof-har129\n"
	got := Walkthrough(md)
	if len(got) != 1 {
		t.Fatalf("Walkthrough found %d sections, want 1", len(got))
	}
	if !strings.Contains(got[0], "Press Play") || !strings.Contains(got[0], "still inside") {
		t.Errorf("the audience section lost its body: %q", got[0])
	}
	if strings.Contains(got[0], "ghostctl") {
		t.Errorf("operator sections leaked into the audience section: %q", got[0])
	}
	if len(Walkthrough("# Doc\n\nno audience section\n")) != 0 {
		t.Error("a doc without the heading has no audience section")
	}
}

// walkthroughProblems is the docs rule: an audience walkthrough names no forbidden term and holds no
// command block.
func walkthroughProblems(b *Boundary, md string) []string {
	var problems []string
	for _, section := range Walkthrough(md) {
		for _, h := range b.Scan(section) {
			problems = append(problems, h.String())
		}
		if strings.Contains(section, "```") {
			problems = append(problems, "a code block (a command) in the audience walkthrough")
		}
	}
	return problems
}

func TestSeededWalkthroughViolationsAreCaught(t *testing.T) {
	b, _ := load(t)
	bad := []string{
		"## Audience walkthrough\n\n1. Run `ghostctl demo play`.\n",
		"## Audience walkthrough\n\n```powershell\n.\\start\n```\n",
		"## Audience walkthrough\n\n7. Run verify: the proof harness prints PASS.\n",
	}
	for _, md := range bad {
		if len(walkthroughProblems(b, md)) == 0 {
			t.Errorf("no problem found in %q", md)
		}
	}
}

// TestDemoWalkthroughsStayInProductTerms reads every demo doc's audience walkthrough. docs/ is stripped
// from the public CI mirror, so there the test skips; it must pass locally before a merge.
func TestDemoWalkthroughsStayInProductTerms(t *testing.T) {
	b, root := load(t)
	docs := filepath.Join(root, "docs")
	if _, err := os.Stat(docs); err != nil {
		t.Skip("docs/ is not in this checkout (the CI mirror strips Markdown)")
	}
	var files []string
	for _, pattern := range []string{"demo/*.md", "*demo*.md"} {
		m, err := filepath.Glob(filepath.Join(docs, filepath.FromSlash(pattern)))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range walkthroughProblems(b, string(raw)) {
			t.Errorf("%s: audience walkthrough: %s", f, p)
		}
	}
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
