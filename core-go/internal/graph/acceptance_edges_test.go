package graph_test

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

func TestWorldEdgesMatchGold(t *testing.T) {
	ingestWorld(t)
	w := buildWorld(t)
	events := fixtureEvents(t)

	// Scored against gold: the relationship types WP5 owns.
	want := map[[3]string]bool{}
	influencesGold := map[[3]string]bool{}
	championGold := map[[3]string]bool{}
	for _, e := range w.g.edges {
		k := [3]string{e.Src, e.RelType, e.Dst}
		switch {
		case scoredRels()[e.RelType]:
			want[k] = true
		case e.RelType == "influences":
			influencesGold[k] = true
		case e.RelType == "champion_for":
			championGold[k] = true
		}
	}
	got, crmByRel := openEdgesByKey(t, w)
	owned := scoreSets(want, got)
	t.Logf("scored edge types %v: %d gold, %d predicted, precision %.4f, recall %.4f", sortedKeys(scoredRels()), len(want), len(got), owned.precision(), owned.recall())
	for k := range want {
		if !got[k] {
			t.Errorf("missing edge %v", k)
		}
	}
	for k := range got {
		if !want[k] {
			t.Errorf("unexpected edge %v", k)
		}
	}
	if owned.precision() < minScore || owned.recall() < minScore {
		t.Errorf("edge precision %.4f / recall %.4f below %.2f", owned.precision(), owned.recall(), minScore)
	}

	// CRM-role edges: expectation comes from the fixture Role__c events and gold, not from the
	// implementation's own mappings. Gold edges of people without a CRM role are claim-derived (WP6).
	roles := fixtureRoles(t, events)
	for rel, goldSet := range map[string]map[[3]string]bool{"influences": influencesGold, "champion_for": championGold} {
		expected := map[[3]string]bool{}
		for k := range goldSet {
			if r, ok := roles[w.g.keyOfIdentityPrefix("crm:contact:", k[0])]; ok && crmRoleRels()[r] == rel {
				expected[k] = true
			}
		}
		s := scoreSets(expected, crmByRel[rel])
		t.Logf("CRM-role %s edges: fixture roles expect %d (gold has %d, the rest are claim-derived), predicted %d crm_explicit, %s",
			rel, len(expected), len(goldSet), len(crmByRel[rel]), s)
		if s.precision() < minScore || s.recall() < minScore {
			t.Errorf("CRM-role %s precision %.4f / recall %.4f below %.2f", rel, s.precision(), s.recall(), minScore)
		}
		if rel == "influences" && len(expected) == 0 {
			t.Error("no fixture CRM role expects an influences edge: the test would be vacuous")
		}
	}
}

// keyOfIdentityPrefix finds the crm:contact:<id> identity of a gold person key.
func (g gold) keyOfIdentityPrefix(prefix, goldKey string) string {
	for _, id := range g.identities[goldKey] {
		if strings.HasPrefix(id, prefix) {
			return id
		}
	}
	return ""
}

// fixtureRoles returns the last Role__c each CRM contact identity ("crm:contact:911") was given.
func fixtureRoles(t *testing.T, events []normalize.SourceEvent) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, ev := range events {
		if ev.SourceSystem != "crm" {
			continue
		}
		var c struct {
			ObjectType string                    `json:"object_type"`
			RecordID   string                    `json:"record_id"`
			Fields     map[string]map[string]any `json:"fields"`
		}
		if err := json.Unmarshal(ev.Payload, &c); err != nil {
			t.Fatal(err)
		}
		if role, ok := c.Fields["Role__c"]["new"].(string); ok && c.ObjectType == "Contact" {
			out["crm:"+c.RecordID] = role
		}
	}
	return out
}

// openEdgesByKey reads the open edges as (gold key, rel, gold key): all scored types, and the
// crm_explicit influences / champion_for edges separately.
func openEdgesByKey(t *testing.T, w world) (scored map[[3]string]bool, crm map[string]map[[3]string]bool) {
	t.Helper()
	scored, crm = map[[3]string]bool{}, map[string]map[[3]string]bool{"influences": {}, "champion_for": {}}
	rows, err := env.DB.Query(`SELECT src_type, src_id::text, rel_type, dst_type, dst_id::text, standing FROM relationships WHERE valid_to IS NULL`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var st, sid, rel, dt, did, standing string
		if err := rows.Scan(&st, &sid, &rel, &dt, &did, &standing); err != nil {
			t.Fatal(err)
		}
		k := [3]string{w.keyOf(st, sid), rel, w.keyOf(dt, did)}
		if scoredRels()[rel] {
			scored[k] = true
		}
		if m, ok := crm[rel]; ok && standing == "crm_explicit" {
			m[k] = true
		}
	}
	return scored, crm
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// participated_in, about and involves are not in gold. They are scored against an expectation
// derived independently from the fixture events: participants (through gold identities) and
// opportunity / account references (through gold identities and the calendar link of calls).
