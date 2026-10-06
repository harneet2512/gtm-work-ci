package claimstest

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// Fact is the verdict on one gold fact at one checkpoint.
type Fact struct {
	Account  string
	CP       int
	Field    string // state field, or "buying_group:<person>:<attribute>", or "coverage_gaps"
	Knowable bool   // the fake worker / rule extractors are given the evidence for it
	Correct  bool
	Want     string
	Got      string
	Reason   string // why a miss is a miss (always set for misses)
	Notes    []string
}

// stateFieldOrder is the scored state fields: the 11 the gold schema requires plus the optional ones gold fills in.
var stateFieldOrder = []string{
	"stage", "health", "owner", "motion", "champion", "champion_status", "economic_buyer", "blockers", "next_milestone",
	"next_meeting", "last_customer_interaction", "objections", "decision_criteria", "decision_process", "current_commitments",
	"relationship_risk", "product_use_case", "commercial_issue",
}

// RequiredFields returns the state fields every gold checkpoint must state (gold.schema.json).
func RequiredFields() []string { return append([]string(nil), requiredFields...) }

var requiredFields = []string{
	"stage", "health", "owner", "motion", "champion", "champion_status", "economic_buyer", "blockers", "next_milestone",
	"next_meeting", "last_customer_interaction",
}

// ruleFields are produced by deterministic rules or derivation, not by the worker: CRM stage, motion
// and owner, calendar next_meeting, and the derived last_customer_interaction.
var ruleFields = map[string]bool{"stage": true, "owner": true, "motion": true, "next_meeting": true, "last_customer_interaction": true}

var personFields = map[string]bool{"owner": true, "champion": true, "economic_buyer": true}

// Scorer judges one checkpoint's state against its gold.
type Scorer struct {
	Gold Gold
	// AccountGolds are all checkpoints of the account (their critical facts define what the fake knows).
	AccountGolds []Gold
	// Order is the account's event files in ingest order.
	Order []string
	// PersonID maps gold person keys to database ids.
	PersonID map[string]string
}

// ScoreState scores every gold state field against the stored state JSON.
func (s Scorer) ScoreState(stateJSON []byte) ([]Fact, error) {
	var st reducer.AccountState
	if err := json.Unmarshal(stateJSON, &st); err != nil {
		return nil, fmt.Errorf("claimstest: decode state: %w", err)
	}
	var out []Fact
	for _, name := range stateFieldOrder {
		want, ok := s.Gold.Expected.StateFields[name]
		if !ok {
			continue
		}
		f := Fact{Account: s.Gold.Account, CP: s.Gold.Checkpoint, Field: name}
		got := st.Fields.Field(name)
		f.Knowable, f.Reason = s.knowable(name, want)
		f.Correct, f.Want, f.Got, f.Notes = s.compare(name, want, got)
		if !f.Correct && f.Reason == "" {
			f.Reason = "MISMATCH on a knowable field (logic bug)"
		}
		if f.Correct {
			f.Reason = ""
		}
		out = append(out, f)
	}
	return out, nil
}

// knowable reports whether the evidence for the gold value is available to the pipeline at this
// checkpoint, mechanically from the gold: a rule field, or a critical fact (with quote) whose evidence
// file is ingested by now; list fields additionally need every gold item to be a critical-fact value.
func (s Scorer) knowable(name string, want json.RawMessage) (bool, string) {
	if ruleFields[name] {
		return true, ""
	}
	cfField := name
	if name == "current_commitments" {
		cfField = "commitment"
	}
	limit := Index(s.Order, s.Gold.AfterEventFile)
	have := map[string]bool{}
	found, sameValue := false, false
	for _, g := range s.AccountGolds {
		for _, cf := range g.Expected.CriticalFacts {
			if cf.Field != cfField || cf.EvidenceQuote == nil {
				continue
			}
			if idx := Index(s.Order, cf.EvidenceEventFile); idx < 0 || idx > limit {
				continue
			}
			found = true
			if text, ok := claims.StringValue(cf.Value); ok {
				have[NormalizeText(text)] = true
			}
			if valueText(cf.Value) == valueText(want) {
				sameValue = true
			}
		}
	}
	if !isList(name) && found && !sameValue {
		return false, "critical facts exist for this field but none carries the gold value at this checkpoint (the gold value is a later or different one)"
	}
	if !found {
		return false, "no critical fact (with a quote) for this field exists in the evidence ingested by this checkpoint, so the fake worker cannot know it"
	}
	if isList(name) {
		var items []struct{ Text string }
		if json.Unmarshal(want, &items) == nil {
			var missing []string
			for _, it := range items {
				if !have[NormalizeText(it.Text)] {
					missing = append(missing, it.Text)
				}
			}
			if len(missing) > 0 {
				return false, "gold lists items that no critical fact provides: " + strings.Join(missing, " | ")
			}
		}
	}
	return true, ""
}

// valueText renders a gold or critical-fact value for equality: strings case- and space-insensitive.
func valueText(raw json.RawMessage) string {
	if str, ok := claims.StringValue(raw); ok {
		return NormalizeText(str)
	}
	return NormalizeText(string(raw))
}

func isList(name string) bool {
	switch name {
	case "blockers", "objections", "decision_criteria", "current_commitments":
		return true
	}
	return false
}

func (s Scorer) compare(name string, want json.RawMessage, got *reducer.Field) (ok bool, wantS, gotS string, notes []string) {
	if isList(name) {
		return s.compareList(want, got)
	}
	wantS = s.renderWant(name, want)
	gotS = renderGot(got)
	return wantS == gotS, wantS, gotS, nil
}

func (s Scorer) renderWant(name string, raw json.RawMessage) string {
	if claims.IsNull(raw) {
		return "null"
	}
	str, isStr := claims.StringValue(raw)
	if !isStr {
		return NormalizeText(string(raw))
	}
	if personFields[name] && strings.HasPrefix(str, "person:") {
		if id, ok := s.PersonID[str]; ok {
			return id
		}
	}
	return NormalizeText(str)
}

func renderGot(f *reducer.Field) string {
	switch {
	case !f.Known:
		return "unknown"
	case f.Value == nil:
		return "null"
	}
	if s, ok := f.Value.(string); ok {
		return NormalizeText(s)
	}
	b, _ := json.Marshal(f.Value)
	return NormalizeText(string(b))
}

func (s Scorer) compareList(want json.RawMessage, got *reducer.Field) (bool, string, string, []string) {
	if str, ok := claims.StringValue(want); ok && strings.EqualFold(str, claims.Unknown) {
		return !got.Known, "unknown", renderItems(got), nil
	}
	var wantItems []struct{ Text, Status string }
	_ = json.Unmarshal(want, &wantItems)
	gotItems := got.ListItems()
	byText := map[string]string{}
	for _, it := range gotItems {
		byText[NormalizeText(it.Text)] = it.Status
	}
	var wantTexts []string
	var notes []string
	for _, it := range wantItems {
		key := NormalizeText(it.Text)
		wantTexts = append(wantTexts, key)
		if status, ok := byText[key]; ok && it.Status != "" && status != it.Status {
			notes = append(notes, fmt.Sprintf("status of %q is %s, gold says %s", it.Text, status, it.Status))
		}
	}
	gotTexts := make([]string, 0, len(byText))
	for k := range byText {
		gotTexts = append(gotTexts, k)
	}
	sort.Strings(wantTexts)
	sort.Strings(gotTexts)
	return strings.Join(wantTexts, " | ") == strings.Join(gotTexts, " | "), strings.Join(wantTexts, " | "), strings.Join(gotTexts, " | "), notes
}

func renderItems(f *reducer.Field) string {
	if !f.Known {
		return "unknown"
	}
	items := f.ListItems()
	var texts []string
	for _, it := range items {
		texts = append(texts, NormalizeText(it.Text))
	}
	sort.Strings(texts)
	return strings.Join(texts, " | ")
}

// ScoreBuyingGroup scores members (presence, roles, status, delegation, title) and coverage gaps.
func (s Scorer) ScoreBuyingGroup(stateJSON []byte) ([]Fact, error) {
	var st reducer.AccountState
	if err := json.Unmarshal(stateJSON, &st); err != nil {
		return nil, fmt.Errorf("claimstest: decode state: %w", err)
	}
	byID := map[string]reducer.Member{}
	for _, m := range st.BuyingGroup {
		byID[m.PersonID] = m
	}
	var out []Fact
	add := func(attr, key string, ok bool, want, got, reason string) {
		f := Fact{Account: s.Gold.Account, CP: s.Gold.Checkpoint, Field: "buying_group:" + key + ":" + attr, Correct: ok, Want: want, Got: got}
		if !ok {
			f.Reason = reason
		}
		out = append(out, f)
	}
	for _, gm := range s.Gold.Expected.BuyingGroup {
		m, present := byID[s.PersonID[gm.PersonKey]]
		add("member", gm.PersonKey, present, "present", presence(present), "no claim makes this person a member (no critical fact for buying_group.member, role or champion, and no CRM contact rule hit)")
		if !present {
			continue
		}
		add("roles", gm.PersonKey, sameSet(gm.Roles, m.Roles), strings.Join(sortedCopy(gm.Roles), ","), strings.Join(sortedCopy(m.Roles), ","), "role claims for this person are not among the fake's critical facts")
		add("status", gm.PersonKey, gm.Status == m.Status, gm.Status, m.Status, "status rule disagrees with gold")
		wantDel, gotDel := "", ""
		if gm.DelegatedTo != "" {
			wantDel = s.PersonID[gm.DelegatedTo]
		}
		if m.DelegatedToPerson != nil {
			gotDel = *m.DelegatedToPerson
		}
		add("delegated_to", gm.PersonKey, wantDel == gotDel, wantDel, gotDel, "delegation claim missing")
		gotTitle := ""
		if m.Title != nil {
			gotTitle = *m.Title
		}
		add("title", gm.PersonKey, gm.Title == "" || gotTitle == gm.Title, gm.Title, gotTitle, "title claim missing or outranked wrongly")
	}
	wantGaps, gotGaps := s.Gold.Expected.CoverageGaps, st.CoverageGaps
	out = append(out, Fact{Account: s.Gold.Account, CP: s.Gold.Checkpoint, Field: "buying_group:*:coverage_gaps", Correct: sameSet(wantGaps, gotGaps),
		Want: strings.Join(sortedCopy(wantGaps), ","), Got: strings.Join(sortedCopy(gotGaps), ","),
		Reason: "a required role (security) is only implied by free text the fake does not model"})
	return out, nil
}

func presence(b bool) string {
	if b {
		return "present"
	}
	return "absent"
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func sameSet(a, b []string) bool {
	return strings.Join(sortedCopy(a), ",") == strings.Join(sortedCopy(b), ",")
}

// Tally counts correct and total facts, optionally restricted to knowable ones.
func Tally(facts []Fact, knowableOnly bool) (correct, total int) {
	for _, f := range facts {
		if knowableOnly && !f.Knowable {
			continue
		}
		total++
		if f.Correct {
			correct++
		}
	}
	return correct, total
}
