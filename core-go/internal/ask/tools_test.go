package ask

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestAccountStateAnswersTheNov9QuestionFromTheRealReads(t *testing.T) {
	b := medtech()
	res := run(t, newTools(b), "account_state", map[string]any{"account": "MedTech", "as_of": "2023-11-09"})
	if !res.OK || res.Empty {
		t.Fatalf("result = %+v", res)
	}
	// A date names the whole day, so the world "as of" Nov 9 is the world strictly before Nov 10.
	if got := b.getQuery(t, "/accounts/"+acctID+"/state").Get("world_as_of"); got != "2023-11-10T00:00:00Z" {
		t.Errorf("world_as_of = %q", got)
	}
	d := dataText(res)
	for _, want := range []string{"security review", "MedTech Advances", "send security package"} {
		if !strings.Contains(d, want) {
			t.Errorf("data lacks %q: %s", want, d)
		}
	}
	if len(res.Links) != 1 || res.Links[0].URL != webBase+"/accounts/"+acctID {
		t.Errorf("links = %v", res.Links)
	}
}

func TestAsOfAcceptsATimeAndRefusesNonsense(t *testing.T) {
	b := medtech()
	run(t, newTools(b), "account_state", map[string]any{"account": "MedTech", "as_of": "2023-11-09T09:30:00Z"})
	if got := b.getQuery(t, "/accounts/"+acctID+"/state").Get("world_as_of"); got != "2023-11-09T09:30:00Z" {
		t.Errorf("an exact time must pass through, got %q", got)
	}
	_, err := newTools(medtech()).Run(context.Background(), "account_state", map[string]any{"account": "MedTech", "as_of": "last Thursday"})
	if !errors.Is(err, ErrBadArguments) {
		t.Errorf("err = %v", err)
	}
}

func TestCurrentStateAddsTheLatestUpdateAndMaterialChanges(t *testing.T) {
	res := run(t, newTools(medtech()), "account_state", map[string]any{"account": "medtech advances"})
	d := dataText(res)
	if !strings.Contains(d, "latest_business_update") || !strings.Contains(d, "recent_material_changes") {
		t.Errorf("data = %s", d)
	}
}

func TestTimelineBoundsTheDatesAndLimit(t *testing.T) {
	b := medtech()
	res := run(t, newTools(b), "timeline", map[string]any{"account": "MedTech", "from": "2023-11-09", "to": "2023-11-09"})
	if d := dataText(res); !strings.Contains(d, "security review before we sign") || strings.Contains(d, "quote") {
		t.Errorf("only Nov 9 should remain: %s", d)
	}
	if got := b.getQuery(t, "/accounts/"+acctID+"/timeline").Get("before"); got != "2023-11-10T00:00:00Z" {
		t.Errorf("before = %q", got)
	}
}

func TestEpisodeOfTheLatestRunByAccountName(t *testing.T) {
	res := run(t, newTools(medtech()), "episode", map[string]any{"account": "MedTech"})
	if !strings.Contains(dataText(res), "send the security package") {
		t.Errorf("data = %s", dataText(res))
	}
	if len(res.Links) != 2 || res.Links[0].URL != webBase+"/episodes/"+episodeID || res.Links[1].URL != webBase+"/runs/"+runID+"/evals" {
		t.Errorf("links = %v", res.Links)
	}
}

func TestGateResultsLinkEachWarningToItsTraceSpan(t *testing.T) {
	res := run(t, newTools(medtech()), "gate_results", map[string]any{"episode_id": "latest", "account": "MedTech"})
	d := dataText(res)
	if !strings.Contains(d, `"gate":"D4"`) || !strings.Contains(d, "The email names no date") {
		t.Errorf("data = %s", d)
	}
	var span string
	for _, l := range res.Links {
		if strings.Contains(l.URL, "span=") {
			span = l.URL
		}
	}
	if span != webBase+"/episodes/"+episodeID+"?mode=trace&span=candidates%3Ac1" {
		t.Errorf("span link = %q (links %v)", span, res.Links)
	}
	for _, l := range res.Links {
		if strings.Contains(l.Label, "B2") {
			t.Errorf("a passing gate must not be linked as a warning: %v", l)
		}
	}
}

func TestKnowledgeAttributionKeepsRetrievedApplicableAndUsedApartAndNeverCallsItInfluence(t *testing.T) {
	res := run(t, newTools(medtech()), "knowledge_attribution", map[string]any{"account": "MedTech"})
	m := res.Data.(map[string]any)
	for _, k := range []string{"retrieved", "applicable", "used"} {
		if n := len(m[k].([]any)); n != 1 {
			t.Errorf("%s has %d spans, want 1", k, n)
		}
	}
	if strings.Contains(strings.ToLower(dataText(res)), "influenced") || m["note"] != attributionNote {
		t.Errorf("attribution wording: %s", dataText(res))
	}
	if strings.Contains(strings.ToLower(fmt.Sprint(m["retrieved"])), "influence") {
		t.Error("retrieved must not be presented as influence")
	}
}

func TestStrategiesDecisionAndInferenceSayNothingYetInsteadOfFailing(t *testing.T) {
	tl := newTools(medtech())
	if res := run(t, tl, "human_decision", map[string]any{"account": "MedTech"}); !res.Empty || !res.OK {
		t.Errorf("no decision yet should be ok and empty: %+v", res)
	}
	if res := run(t, tl, "judgment_inference", map[string]any{"account": "MedTech"}); !res.Empty {
		t.Errorf("no inference yet should be empty: %+v", res)
	}
	if res := run(t, tl, "strategies", map[string]any{"account": "MedTech"}); res.Empty || !strings.Contains(dataText(res), "send_package_and_wait") {
		t.Errorf("strategies = %s", dataText(res))
	}
}

func TestKnowledgeFiltersToTheAccount(t *testing.T) {
	if res := run(t, newTools(medtech()), "knowledge", map[string]any{"account": "MedTech"}); res.Empty {
		t.Error("K1 mentions MedTech Advances")
	}
	if res := run(t, newTools(medtech()), "knowledge", map[string]any{"account": "EcoLite"}); !res.Empty {
		t.Errorf("no knowledge mentions EcoLite: %s", dataText(res))
	}
}

func TestSearchActivitiesFindsTextAndSaysNothingWhenAbsent(t *testing.T) {
	tl := newTools(medtech())
	if res := run(t, tl, "search_activities", map[string]any{"query": "SECURITY review", "account": "MedTech"}); res.Empty {
		t.Error("the Nov 9 email says security review")
	}
	if res := run(t, tl, "search_activities", map[string]any{"query": "zebra", "account": "MedTech"}); !res.Empty {
		t.Errorf("zebra: %s", dataText(res))
	}
}

func TestUnknownAndAmbiguousAccountsAreRefusedNotGuessed(t *testing.T) {
	tl := newTools(medtech())
	res := run(t, tl, "account_state", map[string]any{"account": "Nobody Inc"})
	if res.OK || !res.Empty {
		t.Errorf("unknown account: %+v", res)
	}
	b := medtech()
	b.routes["/accounts"] = ok(map[string]any{"items": []any{map[string]any{"id": acctID, "name": "Acme West"}, map[string]any{"id": "x", "name": "Acme East"}}, "next_cursor": nil})
	res = run(t, newTools(b), "account_state", map[string]any{"account": "Acme"})
	if res.OK || !strings.Contains(dataText(res), "several accounts") {
		t.Errorf("ambiguous: %s", dataText(res))
	}
}

func TestDraftFollowupIsADryRunWithNamesNotIds(t *testing.T) {
	b := medtech()
	res := run(t, newTools(b), "draft_followup", map[string]any{"account": "MedTech", "intent": "send the security package"})
	d := dataText(res)
	if !res.DryRun || !strings.Contains(d, DraftLabel) || !strings.Contains(d, "Dana Reyes") || !strings.Contains(d, "Sam Ortiz") {
		t.Errorf("draft = %s", d)
	}
	if strings.Contains(d, `"p1"`) {
		t.Errorf("a person id leaked: %s", d)
	}
	if len(b.posts) != 0 {
		t.Errorf("a draft must write nothing: %v", b.posts)
	}
}

func TestCRMUpdatePreviewShowsFromAndToAndWritesNothing(t *testing.T) {
	b := medtech()
	res := run(t, newTools(b), "crm_update_preview", map[string]any{"account": "MedTech", "field": "stage", "value": "Closed Won"})
	w := res.Data.(map[string]any)["would_write"].(map[string]any)
	if !res.DryRun || w["from"] != "Negotiation" || w["to"] != "Closed Won" || len(b.posts) != 0 {
		t.Errorf("preview = %s posts=%v", dataText(res), b.posts)
	}
	res = run(t, newTools(medtech()), "crm_update_preview", map[string]any{"account": "MedTech", "field": "no_such_field", "value": "x"})
	if !strings.Contains(dataText(res), "not in the account state") {
		t.Errorf("missing field: %s", dataText(res))
	}
}

func TestToolsOnlyEverReadFromTheBackend(t *testing.T) {
	b := medtech()
	tl := newTools(b)
	for _, name := range ToolNames {
		_, _ = tl.Run(context.Background(), name, map[string]any{"account": "MedTech", "query": "security", "intent": "x", "field": "stage", "value": "y"})
	}
	if len(b.posts) != 0 {
		t.Fatalf("a tool wrote through the backend: %v", b.posts)
	}
	for _, name := range ToolNames {
		if strings.Contains(name, "send") || strings.Contains(name, "write") || strings.Contains(name, "email") {
			t.Errorf("tool %q can send or write", name)
		}
	}
}

func TestUnknownToolAndBadIDs(t *testing.T) {
	tl := newTools(medtech())
	if _, err := tl.Run(context.Background(), "send_email", nil); !errors.Is(err, ErrUnknownTool) {
		t.Errorf("err = %v", err)
	}
	res := run(t, tl, "episode", map[string]any{"episode_id": "not-a-uuid"})
	if res.OK {
		t.Error("a malformed id must be refused")
	}
	if _, err := tl.Run(context.Background(), "search_activities", map[string]any{}); !errors.Is(err, ErrBadArguments) {
		t.Errorf("err = %v", err)
	}
}

func TestLargeResultsAreBoundedAndSayTheyWereTruncated(t *testing.T) {
	b := medtech()
	var items []any
	for i := 0; i < 400; i++ {
		items = append(items, map[string]any{"id": i, "occurred_at": eventN, "summary": strings.Repeat("long text ", 100)})
	}
	b.routes["/accounts/"+acctID+"/timeline"] = ok(map[string]any{"items": items})
	res := run(t, newTools(b), "timeline", map[string]any{"account": "MedTech", "limit": 50})
	if !res.Truncated || size(res.Data) > maxDataBytes {
		t.Errorf("truncated=%v size=%d", res.Truncated, size(res.Data))
	}
	if strings.Contains(dataText(res), strings.Repeat("long text ", 100)) {
		t.Error("a long text must be clipped")
	}
}

func TestListAccountsShowsNamesNotIDs(t *testing.T) {
	res := run(t, newTools(medtech()), "list_accounts", nil)
	d := dataText(res)
	if !strings.Contains(d, "MedTech Advances") || !strings.Contains(d, "EcoLite") || strings.Contains(d, acctID) {
		t.Errorf("accounts = %s", d)
	}
}

func TestGraphUsesTheWorldTimeEndpoint(t *testing.T) {
	b := medtech()
	run(t, newTools(b), "graph_neighborhood", map[string]any{"account": "MedTech", "as_of": "2023-11-09"})
	if b.getQuery(t, "/accounts/"+acctID+"/graph").Get("world_as_of") != "2023-11-10T00:00:00Z" {
		t.Error("the graph must be read at world time")
	}
}
