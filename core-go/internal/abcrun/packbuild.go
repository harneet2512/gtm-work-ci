package abcrun

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/crmarena"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

// SiblingWindow is how far back the events of the account's OTHER deals are visible: a quote or stage change of a
// sibling deal within 30 days is what makes 'another quote is open' observable.
const SiblingWindow = 30 * 24 * time.Hour

// Spec is one situation as chosen by bench/uplift/select.py (by decision relevance, never by outcome).
type Spec struct {
	ID              string  `json:"id"`
	Kind            string  `json:"kind"`
	DecisionPoint   string  `json:"decision_point"`
	DealID          string  `json:"deal_id"`
	AccountID       string  `json:"account_id"`
	SelectionReason string  `json:"selection_reason"`
	Trigger         Trigger `json:"trigger"`
}

// BuildInput names the frozen data a pack is built from. Both directories are read-only and git-ignored; a built pack
// is committed so that nothing downstream needs them.
type BuildInput struct {
	ExportDir    string // the CRMArena-Pro export (WP31 loader input)
	SyntheticDir string // the generated synthetic layer (events/, labels/)
	Specs        []Spec
}

// BuildPack assembles each situation's world: the account's records and the deal's real events up to the trigger
// (every payload as the world knew it at its own time), the synthetic layer's events of the deal up to the trigger
// (the synthetic stage path replaces the loader's snapshot stage on covered deals), and the 30 days of quotes and
// stage changes of the account's other deals before the trigger. Nothing dated after the trigger is read.
func BuildPack(in BuildInput) (Pack, error) {
	snap, err := crmarena.Load(in.ExportDir)
	if err != nil {
		return Pack{}, err
	}
	res, err := crmarena.Build(snap)
	if err != nil {
		return Pack{}, err
	}
	covered, err := readCovered(filepath.Join(in.SyntheticDir, "labels", "covered_deals.json"))
	if err != nil {
		return Pack{}, err
	}
	base, err := crmarena.SupersedeSnapshotStage(res.Events, covered)
	if err != nil {
		return Pack{}, err
	}
	pack := Pack{Version: PackVersion, Company: res.Reps.Company("users")}
	for _, spec := range in.Specs {
		s, err := buildSituation(in, base, spec)
		if err != nil {
			return Pack{}, fmt.Errorf("abcrun: situation %s: %w", spec.ID, err)
		}
		pack.Situations = append(pack.Situations, s)
	}
	return pack, pack.Check()
}

func readCovered(path string) (map[string]bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f struct {
		Deals []string `json:"deals"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(f.Deals))
	for _, d := range f.Deals {
		out[d] = true
	}
	return out, nil
}

func syntheticEvents(dir, account, deal string) ([]crmarena.Event, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "events", account, deal+".json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var evs []normalize.SourceEvent
	if err := json.Unmarshal(raw, &evs); err != nil {
		return nil, err
	}
	out := make([]crmarena.Event, len(evs))
	for i, e := range evs {
		out[i] = crmarena.Event{Source: e, AccountID: account, DealID: deal}
	}
	return out, nil
}

// siblingRelevant keeps the events of another deal that make an open quote visible: a Quote record or a stage change.
func siblingRelevant(e crmarena.Event) bool {
	if strings.HasPrefix(e.Source.SourceEventKey, "field:StageName:") {
		return true
	}
	var p struct {
		ObjectType string `json:"object_type"`
	}
	return json.Unmarshal(e.Source.Payload, &p) == nil && p.ObjectType == "Quote"
}

func buildSituation(in BuildInput, base []crmarena.Event, spec Spec) (Situation, error) {
	trigger := spec.Trigger.OccurredAt
	dealIDs := map[string]bool{}
	var picked []crmarena.Event
	for _, e := range base {
		if e.AccountID != spec.AccountID || e.OccurredAt().After(trigger) {
			continue
		}
		if e.DealID != "" {
			dealIDs[e.DealID] = true
		}
		switch {
		case e.DealID == "" || e.DealID == spec.DealID:
		case e.OccurredAt().After(trigger.Add(-SiblingWindow)) && siblingRelevant(e):
		default:
			continue
		}
		known, _, err := e.AsKnown()
		if err != nil {
			return Situation{}, err
		}
		picked = append(picked, known)
	}
	var synthetic []crmarena.Event
	for deal := range dealIDs {
		evs, err := syntheticEvents(in.SyntheticDir, spec.AccountID, deal)
		if err != nil {
			return Situation{}, err
		}
		for _, e := range evs {
			if e.OccurredAt().After(trigger) {
				continue
			}
			if deal != spec.DealID && (!e.OccurredAt().After(trigger.Add(-SiblingWindow)) || !siblingRelevant(e)) {
				continue
			}
			known, _, err := e.AsKnown()
			if err != nil {
				return Situation{}, err
			}
			synthetic = append(synthetic, known)
		}
	}
	sort.SliceStable(synthetic, func(i, j int) bool { return synthetic[i].OccurredAt().Before(synthetic[j].OccurredAt()) })
	merged := mergeByTime(picked, synthetic)
	s := Situation{ID: spec.ID, Kind: spec.Kind, DecisionPoint: spec.DecisionPoint, DealID: spec.DealID, AccountID: spec.AccountID,
		SelectionReason: spec.SelectionReason, Trigger: spec.Trigger, Truth: map[string][]claims.Candidate{}}
	cut := -1
	for i, e := range merged {
		if e.Source.SourceObjectID == spec.Trigger.SourceObjectID && e.Source.SourceEventKey == spec.Trigger.SourceEventKey {
			cut = i
		}
	}
	if cut < 0 {
		return Situation{}, fmt.Errorf("the trigger event %s/%s is not in the world", spec.Trigger.SourceObjectID, spec.Trigger.SourceEventKey)
	}
	seen := map[string]bool{}
	for _, e := range merged[:cut+1] {
		key := e.Source.SourceSystem + "|" + e.Source.SourceObjectID + "|" + e.Source.SourceEventKey
		if seen[key] {
			continue
		}
		seen[key] = true
		s.Events = append(s.Events, e.Source)
		s.addTruth(e.Source)
		s.addPerson(e.Source)
	}
	return s, nil
}

// mergeByTime merges two time-ordered lists; at equal times the base event comes first.
func mergeByTime(base, synthetic []crmarena.Event) []crmarena.Event {
	out := make([]crmarena.Event, 0, len(base)+len(synthetic))
	i, j := 0, 0
	for i < len(base) || j < len(synthetic) {
		if j >= len(synthetic) || (i < len(base) && !synthetic[j].OccurredAt().Before(base[i].OccurredAt())) {
			out = append(out, base[i])
			i++
		} else {
			out = append(out, synthetic[j])
			j++
		}
	}
	return out
}

// addTruth records the planted facts of a rendered customer email.
func (s *Situation) addTruth(e normalize.SourceEvent) {
	if e.Origin != "synthetic" || e.SourceSystem != "email" {
		return
	}
	var p struct {
		Direction string `json:"direction"`
		Body      string `json:"body_text"`
	}
	if json.Unmarshal(e.Payload, &p) != nil || p.Direction != "inbound" {
		return
	}
	if facts := TruthFromBody(p.Body); len(facts) > 0 {
		s.Truth[e.SourceObjectID] = facts
	}
}

// addPerson records an account contact's name and title from its creation event.
func (s *Situation) addPerson(e normalize.SourceEvent) {
	var p struct {
		ObjectType string `json:"object_type"`
		Created    bool   `json:"created"`
		Fields     map[string]struct {
			New any `json:"new"`
		} `json:"fields"`
	}
	if e.SourceSystem != "crm" || json.Unmarshal(e.Payload, &p) != nil || p.ObjectType != "Contact" || !p.Created {
		return
	}
	str := func(k string) string { v, _ := p.Fields[k].New.(string); return v }
	if email := strings.ToLower(str("Email")); email != "" {
		s.People = append(s.People, Person{Email: email, Name: strings.TrimSpace(str("FirstName") + " " + str("LastName")), Title: str("Title"), Side: "buyer"})
	}
}
