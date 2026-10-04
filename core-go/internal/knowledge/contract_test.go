package knowledge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func enumOf(t *testing.T, v any) []string {
	t.Helper()
	raw, ok := v.(map[string]any)["enum"].([]any)
	if !ok {
		t.Fatalf("no enum in %v", v)
	}
	out := make([]string, 0, len(raw))
	for _, x := range raw {
		out = append(out, x.(string))
	}
	sort.Strings(out)
	return out
}

func assertSame(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s: got %v, want %v", what, got, want)
		}
	}
}

func TestVocabulariesMatchTheContracts(t *testing.T) {
	sig := readJSON(t, repoFile(t, "contracts/schemas/signal.v1.json"))
	defs := sig["$defs"].(map[string]any)
	assertSame(t, "event signals", keys(eventSignalTypes), enumOf(t, defs["eventSignalType"]))
	assertSame(t, "standing signals", keys(standingSignalTypes), enumOf(t, defs["standingSignalType"]))
	all := append(keys(eventSignalTypes), keys(standingSignalTypes)...)
	sort.Strings(all)
	assertSame(t, "signal types", all, enumOf(t, sig["properties"].(map[string]any)["signal_type"]))
	if w := defs["eventWindowDays"].(map[string]any)["const"].(float64); time.Duration(w)*24*time.Hour != EventWindow {
		t.Fatalf("event window %v days", w)
	}

	st := readJSON(t, repoFile(t, "contracts/schemas/account_state.v1.json"))
	bg := st["properties"].(map[string]any)["buying_group"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	assertSame(t, "roles", keys(buyingGroupRoles), enumOf(t, bg["roles"].(map[string]any)["items"]))
	item := st["$defs"].(map[string]any)["listField"].(map[string]any)["properties"].(map[string]any)["value"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	assertSame(t, "item statuses", keys(itemStatuses), enumOf(t, item["status"]))
	// Parity with worker-py knowledge_evals.conditions.ENGAGED (tested there against the same schema enum).
	for s := range disengagedStatuses {
		if !hasString(enumOf(t, bg["status"]), s) {
			t.Fatalf("disengaged status %q is not a buying-group status", s)
		}
	}
	assertSame(t, "disengaged statuses", keys(disengagedStatuses), []string{"departed", "disengaged", "inactive"})
	fields := st["properties"].(map[string]any)["fields"].(map[string]any)["required"].([]any)
	if len(fields) != len(reducer.FieldNames()) {
		t.Fatalf("state fields drifted: %d vs %d", len(fields), len(reducer.FieldNames()))
	}
}

// TestEntriesAreContractValid embeds matcher output in a DecisionGuidance and validates it, including
// the contract rule that a triggered exception means applies=false.
func TestEntriesAreContractValid(t *testing.T) {
	c := jsonschema.NewCompiler()
	schemas, err := filepath.Glob(filepath.Join(filepath.Dir(repoFile(t, "contracts/schemas/common.v1.json")), "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range schemas {
		doc := readJSON(t, f)
		if err := c.AddResource(doc["$id"].(string), doc); err != nil {
			t.Fatal(err)
		}
	}
	schema, err := c.Compile("https://ghost.local/contracts/decision_guidance.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	kn := keepChampion()
	var entries []Entry
	for _, st := range []Situation{expansionState("active", "active"), expansionState("active", "departed"),
		expansionState("weakening", "active")} {
		entries = append(entries, mustMatch(t, kn, withUUIDRefs(st)).Entry)
	}
	if len(entries[0].CurrentEvidenceRefs) == 0 {
		t.Fatal("an applying entry must cite its current evidence")
	}
	doc := map[string]any{
		"id": "00000000-0000-4000-8000-0000000000c1", "account_id": "00000000-0000-4000-8000-0000000000c2", "state_version": 3,
		"recommended_action": "wait", "why_now": "x", "who_to_involve": []any{}, "who_not_to_involve": []any{},
		"supporting_knowledge": entries, "created_at": "2026-10-01T12:00:00Z",
	}
	raw, _ := json.Marshal(doc)
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(inst); err != nil {
		t.Fatalf("decision guidance invalid: %v", err)
	}
	broken := entries[1]
	broken.Applies = true
	doc["supporting_knowledge"] = []Entry{broken}
	raw, _ = json.Marshal(doc)
	inst, _ = jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if schema.Validate(inst) == nil {
		t.Fatal("a triggered exception with applies=true must be rejected by the contract")
	}
}

// withUUIDRefs gives every field and signal of a test situation a contract-valid activity id.
func withUUIDRefs(s Situation) Situation {
	n := 0
	next := func() []EvidenceRef {
		n++
		return []EvidenceRef{{ActivityID: fmt.Sprintf("00000000-0000-4000-8000-%012d", n)}}
	}
	fields := map[string]Value{}
	for name, v := range s.Fields {
		v.EvidenceRefs = next()
		fields[name] = v
	}
	s.Fields = fields
	signals := make([]Signal, len(s.Signals))
	for i, sig := range s.Signals {
		sig.EvidenceRefs = next()
		signals[i] = sig
	}
	s.Signals = signals
	return s
}
