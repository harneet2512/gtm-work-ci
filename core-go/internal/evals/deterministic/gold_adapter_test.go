package deterministic

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// Fixture adapter: turns a fixtures/evals case (HAR-97 gold) into an Input. The gold is never edited.
// Where a case does not carry data an eval needs, the adapter derives it as follows (and the
// gold test counts how often):
//   - evidence refs that name an activity the case does not include get a stub activity whose text
//     is the ref's own quote, so statement-level provenance is scorable (the fabricated-citation
//     check cannot be scored on those refs);
//   - a CRMNoteAdded activity that mentions the stage makes the stage field crm_explicit as of that note;
//   - an activity whose carried text is an excerpt is extended with any quote a ref takes from it;
//   - an open commitment "Share X and Y once <condition>" makes X and Y confidential assets whose
//     condition is met only when the commitment is fulfilled.

type caseFile struct {
	ID         string `json:"id"`
	ShouldPass bool   `json:"should_pass"`
	Context    struct {
		Now   time.Time `json:"now"`
		State struct {
			AccountID     string                     `json:"account_id"`
			OpportunityID *string                    `json:"opportunity_id"`
			Fields        map[string]json.RawMessage `json:"fields"`
			BuyingGroup   []reducer.Member           `json:"buying_group"`
		} `json:"state"`
		Trigger    caseAct   `json:"trigger"`
		Supporting []caseAct `json:"supporting_activities"`
	} `json:"context"`
	Candidate json.RawMessage `json:"candidate_action"`
	Expected  []struct {
		EvalType string `json:"eval_type"`
		Verdict  string `json:"verdict"`
		Blocking bool   `json:"blocking"`
	} `json:"expected"`
}

type caseAct struct {
	ActivityID   string    `json:"activity_id"`
	ActivityType string    `json:"activity_type"`
	OccurredAt   time.Time `json:"occurred_at"`
	Actor        *string   `json:"actor_person_id"`
	Text         string    `json:"text"`
}

type idsFile struct {
	Accounts map[string]struct {
		AccountID     string `json:"account_id"`
		OpportunityID string `json:"opportunity_id"`
	} `json:"accounts"`
	People map[string]struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
		Account     string `json:"account"`
	} `json:"people"`
}

const danaID = "0b0e0000-0000-4000-8000-000000000001"

var countersignedRE = regexp.MustCompile(`(?i)\b(is|was|been|now) countersigned`)

var shareOnceRE = regexp.MustCompile(`(?i)^share (.+?) once (.+)$`)

func toField(raw json.RawMessage) reducer.Field {
	var v any
	_ = json.Unmarshal(raw, &v)
	if v == nil || v == "unknown" {
		return reducer.Field{Value: "unknown", EvidenceRefs: []reducer.EvidenceRef{}}
	}
	if arr, ok := v.([]any); ok {
		v = toItems(arr)
	}
	return reducer.Field{Value: v, Known: true, WinningClaimID: ptr("c"), EvidenceRefs: []reducer.EvidenceRef{{ActivityID: "x"}}}
}

func toItems(arr []any) []reducer.Item {
	var items []reducer.Item
	for _, x := range arr {
		m, ok := x.(map[string]any)
		if !ok {
			continue
		}
		it := reducer.Item{}
		it.Text, _ = m["text"].(string)
		it.Status, _ = m["status"].(string)
		it.OwnerPersonID, _ = m["owner_person_id"].(string)
		if d, ok := m["due"].(string); ok {
			if t, err := time.Parse("2006-01-02", d); err == nil {
				it.DueAt = &t
			}
		}
		items = append(items, it)
	}
	return items
}

// stubUnresolvedRefs adds a stub activity for every evidence ref the case does not carry.
func stubUnresolvedRefs(acts []Activity, refs []EvidenceRef, account string) ([]Activity, int, int) {
	have := map[string]bool{}
	extended := 0
	for i, a := range acts {
		have[a.ActivityID] = true
		for _, r := range refs { // the case carries an excerpt: extend it with what the ref quotes
			if r.ActivityID == a.ActivityID && r.Quote != "" && !containsText(acts[i].Text, r.Quote) {
				acts[i].Text += "\n" + r.Quote
				extended++
			}
		}
	}
	stubs := 0
	for _, r := range refs {
		if have[r.ActivityID] {
			continue
		}
		have[r.ActivityID] = true
		stubs++
		at := time.Time{}
		if r.OccurredAt != nil {
			at = *r.OccurredAt
		}
		acts = append(acts, Activity{ActivityID: r.ActivityID, AccountID: ptr(account), ActivityType: "Stub", OccurredAt: at, Text: r.Quote})
	}
	return acts, stubs, extended
}

// crmExplicitStage marks the stage crm_explicit when a CRM note about the stage exists.
func crmExplicitStage(st reducer.AccountState, acts []Activity) reducer.AccountState {
	for _, a := range acts {
		if a.ActivityType == "CRMNoteAdded" && strings.Contains(strings.ToLower(a.Text), "stage") {
			st.Fields.Stage.Standing = ptr("crm_explicit")
			at := a.OccurredAt
			st.Fields.Stage.AsOf = &at
		}
	}
	return st
}

// conditionalAssets derives confidential assets from "Share X and Y once <condition>" commitments.
func conditionalAssets(st reducer.AccountState, acts []Activity, attachments []string) []Asset {
	met := false
	for _, a := range acts {
		met = met || countersignedRE.MatchString(a.Text)
	}
	var out []Asset
	for _, it := range listItems(st.Fields.CurrentCommitments) {
		m := shareOnceRE.FindStringSubmatch(it.Text)
		if m == nil {
			continue
		}
		for _, name := range strings.Split(m[1], " and ") {
			out = append(out, Asset{Name: strings.TrimSpace(name), Available: true, Confidential: true,
				ShareCondition: ptr(m[2]), ShareConditionMet: it.Status == "fulfilled" || met})
		}
	}
	return withAttachmentAliases(out, attachments)
}

// withAttachmentAliases lets a file name stand for the library asset it is named after
// ("soc2-type2-2026.pdf" for "SOC2 Type II report"); unmatched files are assumed present.
func withAttachmentAliases(assets []Asset, attachments []string) []Asset {
	if len(assets) == 0 {
		return nil
	}
	for _, f := range attachments {
		stem := strings.TrimSuffix(f, filepath.Ext(f))
		first := stageKey(strings.FieldsFunc(stem, func(r rune) bool { return r == '-' || r == '_' || r == ' ' })[0])
		matched := false
		for i := range assets {
			if strings.Contains(stageKey(assets[i].Name), first) {
				assets[i].Aliases = append(assets[i].Aliases, f)
				matched = true
			}
		}
		if !matched {
			assets = append(assets, Asset{Name: f, Available: true})
		}
	}
	return assets
}

func loadIDs(t *testing.T) idsFile {
	t.Helper()
	var ids idsFile
	raw, err := os.ReadFile(repoPath("fixtures", "evals", "ids.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &ids); err != nil {
		t.Fatal(err)
	}
	return ids
}

func loadCases(t *testing.T) []caseFile {
	t.Helper()
	files, err := filepath.Glob(repoPath("fixtures", "evals", "cases", "*", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no gold cases: %v", err)
	}
	sort.Strings(files)
	var out []caseFile
	for _, fn := range files {
		raw, err := os.ReadFile(fn)
		if err != nil {
			t.Fatal(err)
		}
		var c caseFile
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatalf("%s: %v", fn, err)
		}
		out = append(out, c)
	}
	return out
}

// adapterUse counts what the adapter had to derive for one case.
type adapterUse struct{ stubs, extended int }

func caseInput(t *testing.T, c caseFile, ids idsFile) Input {
	in, _ := caseInputCounted(t, c, ids)
	return in
}

func caseInputCounted(t *testing.T, c caseFile, ids idsFile) (Input, adapterUse) {
	t.Helper()
	var f reducer.Fields
	for _, n := range reducer.FieldNames() {
		*f.Field(n) = toField(c.Context.State.Fields[n])
	}
	st := reducer.AccountState{AccountID: c.Context.State.AccountID, OpportunityID: c.Context.State.OpportunityID,
		Version: 1, AsOf: c.Context.Now.Add(-time.Minute), Fields: f, BuyingGroup: c.Context.State.BuyingGroup}
	var out Output
	if err := json.Unmarshal(c.Candidate, &out); err != nil {
		t.Fatalf("%s: %v", c.ID, err)
	}
	people, opps := directory(ids)
	acts, prior := activitiesAndPrior(c, people)
	acts, stubs, extended := stubUnresolvedRefs(acts, out.EvidenceRefs, c.Context.State.AccountID)
	st = crmExplicitStage(st, acts)
	in := Input{AgentRunID: runID, DraftIndex: 1, WorkspaceID: "ws", AccountID: c.Context.State.AccountID,
		OpportunityID: c.Context.State.OpportunityID, RunMode: "live", EvaluatedAt: c.Context.Now, Draft: out, State: st,
		People: people, Opportunities: opps, Activities: acts, PriorActions: prior, Assets: conditionalAssets(st, acts, out.FinishedArtifact.Attachments),
		CRM: CRMRules{Stages: []string{"Discovery", "Technical evaluation", "Commercial review", "Negotiation"},
			TerminalStages: []string{"Closed won", "Closed lost"}, MaxForwardSteps: 1},
		Policy: Policy{WorkspaceID: "ws", AutonomyLevel: "customer_facing",
			AllowedTools: []string{ToolEmailSend, ToolCalendarInvite, ToolDocumentShare, ToolSlackPost, ToolCRMNote, ToolCRMUpdate}}}
	return in, adapterUse{stubs: stubs, extended: extended}
}

func directory(ids idsFile) ([]Person, []Opportunity) {
	accByKey := map[string]string{}
	var opps []Opportunity
	for k, a := range ids.Accounts {
		accByKey[k] = a.AccountID
		opps = append(opps, Opportunity{OpportunityID: a.OpportunityID, AccountID: a.AccountID, OwnerPersonID: ptr(danaID)})
	}
	var people []Person
	for _, p := range ids.People {
		if p.Account == "org" {
			people = append(people, Person{PersonID: p.ID, DisplayName: p.DisplayName, Kind: KindEmployee, HasEmail: true})
		} else {
			people = append(people, Person{PersonID: p.ID, DisplayName: p.DisplayName, Kind: "contact", AccountID: ptr(accByKey[p.Account]), HasEmail: true})
		}
	}
	return people, opps
}

// activitiesAndPrior lists the case's activities and the outbound emails and shares by our staff as prior actions.
func activitiesAndPrior(c caseFile, people []Person) ([]Activity, []PriorAction) {
	emp := map[string]bool{}
	for _, p := range people {
		emp[p.PersonID] = p.Kind == KindEmployee
	}
	var contacts []string
	for _, m := range c.Context.State.BuyingGroup {
		contacts = append(contacts, m.PersonID)
	}
	var acts []Activity
	var prior []PriorAction
	for _, a := range append([]caseAct{c.Context.Trigger}, c.Context.Supporting...) {
		acts = append(acts, Activity{ActivityID: a.ActivityID, AccountID: ptr(c.Context.State.AccountID), ActivityType: a.ActivityType, OccurredAt: a.OccurredAt, Text: a.Text})
		if a.Actor != nil && emp[*a.Actor] && strings.HasPrefix(a.ActivityType, "Email") {
			txt := a.Text
			prior = append(prior, PriorAction{RefKind: "activity", RefID: a.ActivityID, Action: ActionSendEmail, Status: "completed",
				OccurredAt: a.OccurredAt, RecipientPersonIDs: contacts, BodyText: &txt})
		}
	}
	return acts, prior
}
