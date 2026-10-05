// Package contracts holds the Go-side conformance tests for contracts/schemas.
// Every example in contracts/examples must validate; known-bad mutations must not.
package contracts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func contractsDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		candidate := filepath.Join(dir, "contracts")
		if info, statErr := os.Stat(filepath.Join(candidate, "schemas")); statErr == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("contracts/ not found")
		}
		dir = parent
	}
}

func compiler(t *testing.T, root string) *jsonschema.Compiler {
	t.Helper()
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	files, err := filepath.Glob(filepath.Join(root, "schemas", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no schemas found: %v", err)
	}
	for _, f := range files {
		doc := readJSON(t, f)
		id, _ := doc.(map[string]any)["$id"].(string)
		if id == "" {
			t.Fatalf("%s has no $id", f)
		}
		if err := c.AddResource(id, doc); err != nil {
			t.Fatalf("add %s: %v", f, err)
		}
	}
	return c
}

func schemaFor(t *testing.T, c *jsonschema.Compiler, name string) *jsonschema.Schema {
	t.Helper()
	s, err := c.Compile("https://ghost.local/contracts/" + name + ".v1.json")
	if err != nil {
		t.Fatalf("compile %s: %v", name, err)
	}
	return s
}

func readJSON(t *testing.T, path string) any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	doc, err := jsonschema.UnmarshalJSON(f)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return doc
}

func TestEveryExampleValidates(t *testing.T) {
	root := contractsDir(t)
	c := compiler(t, root)
	examples, err := filepath.Glob(filepath.Join(root, "examples", "*.example.json"))
	if err != nil {
		t.Fatalf("glob examples: %v", err)
	}
	if len(examples) < 12 {
		t.Fatalf("expected at least 12 examples, found %d", len(examples))
	}
	for _, ex := range examples {
		name := strings.TrimSuffix(filepath.Base(ex), ".example.json")
		t.Run(name, func(t *testing.T) {
			if err := schemaFor(t, c, name).Validate(readJSON(t, ex)); err != nil {
				t.Fatalf("%s example invalid: %v", name, err)
			}
		})
	}
}

func TestEverySchemaHasAnExample(t *testing.T) {
	root := contractsDir(t)
	schemas, err := filepath.Glob(filepath.Join(root, "schemas", "*.v1.json"))
	if err != nil {
		t.Fatalf("glob schemas: %v", err)
	}
	for _, s := range schemas {
		name := strings.TrimSuffix(filepath.Base(s), ".v1.json")
		if name == "common" {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, "examples", name+".example.json")); err != nil {
			t.Errorf("schema %s has no example", name)
		}
	}
}

// TestEvalCatalogValidates checks the one eval vocabulary (contracts/evals, HAR-114) against its schema.
func TestEvalCatalogValidates(t *testing.T) {
	root := contractsDir(t)
	// eval_registry.json and completeness_matrix.json (HAR-140) have their own schemas: registry_split_test.go.
	schema := schemaFor(t, compiler(t, root), "eval_catalog")
	if err := schema.Validate(readJSON(t, filepath.Join(root, "evals", "eval_catalog.json"))); err != nil {
		t.Errorf("eval_catalog.json invalid: %v", err)
	}
}

// field returns one AccountState field object from a decoded example.
func field(m map[string]any, name string) map[string]any {
	return m["fields"].(map[string]any)[name].(map[string]any)
}

// member returns the first buying-group member of a decoded state example.
func member(m map[string]any) map[string]any {
	return m["buying_group"].([]any)[0].(map[string]any)
}

// mutate returns a deep copy of the example with fn applied.
func mutate(t *testing.T, root, name string, fn func(m map[string]any)) any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "examples", name+".example.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	fn(m)
	b, _ := json.Marshal(m)
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestKnownBadInstancesAreRejected(t *testing.T) {
	root := contractsDir(t)
	c := compiler(t, root)
	cases := []struct {
		name, schema string
		fn           func(m map[string]any)
	}{
		{"AI claim without evidence quote (HAR-96 §18 provenance)", "claim", func(m map[string]any) { delete(m, "evidence_quote") }},
		{"unknown standing", "claim", func(m map[string]any) { m["standing"] = "vibes" }},
		{"E7 keeps used apart from retrieved and applicable", "knowledge_attribution", func(m map[string]any) { delete(m, "used") }},
		{"an E7 verdict is pass, warn, fail or unknown", "knowledge_attribution", func(m map[string]any) {
			obj(list(obj(m["influence"]), "evals")[1])["verdict"] = "maybe"
		}},
		{"without a compared counterfactual there is no changed column", "knowledge_attribution", func(m map[string]any) {
			obj(obj(m["influence"])["counterfactual"])["status"] = "not_run"
		}},
		{"a compared counterfactual gives every candidate its changed column", "knowledge_attribution", func(m map[string]any) {
			obj(list(obj(m["influence"]), "candidates")[0])["change"] = nil
		}},
		{"a strategy set says whether any candidate is acceptable", "strategy_set", func(m map[string]any) { delete(m, "no_acceptable_candidate") }},
		{"no_acceptable_candidate is a boolean", "strategy_set", func(m map[string]any) { m["no_acceptable_candidate"] = "yes" }},
		{"unknown state field must have value 'unknown' (§18 unknown is legal)", "account_state", func(m map[string]any) {
			field(m, "economic_buyer")["value"] = "Jane Doe"
		}},
		{"known state field must point to evidence (§7)", "account_state", func(m map[string]any) {
			field(m, "stage")["evidence_refs"] = []any{}
		}},
		{"known state field cannot be 'unknown'", "account_state", func(m map[string]any) {
			field(m, "stage")["value"] = "unknown"
		}},
		{"an opportunity state is keyed by a deal (ADR-0016)", "opportunity_state", func(m map[string]any) { delete(m, "opportunity_id") }},
		{"an opportunity state cannot name the deal 'null'", "opportunity_state", func(m map[string]any) { m["opportunity_id"] = nil }},
		{"opportunity state: known stage must point to evidence (§7)", "opportunity_state", func(m map[string]any) {
			field(m, "stage")["evidence_refs"] = []any{}
		}},
		{"opportunity state: unknown amount must say 'unknown'", "opportunity_state", func(m map[string]any) {
			f := field(m, "amount")
			f["known"] = false
			f["winning_claim_id"] = nil
			f["evidence_refs"] = []any{}
		}},
		{"opportunity state carries no account-level opportunity list", "opportunity_state", func(m map[string]any) { m["opportunities"] = []any{} }},
		{"opportunity summary needs is_primary", "account_state", func(m map[string]any) {
			delete(m["opportunities"].([]any)[0].(map[string]any), "is_primary")
		}},
		{"member role_source must be recorded or inferred (ADR-0016)", "account_state", func(m map[string]any) { member(m)["role_source"] = "guessed" }},
		{"a member with a role cannot have a null role_source", "account_state", func(m map[string]any) {
			member(m)["role_source"] = nil
			member(m)["role_basis"] = nil
		}},
		{"a member whose only role is unknown has no role_source", "account_state", func(m map[string]any) {
			member(m)["roles"] = []any{"unknown"}
		}},
		{"an opportunity state's members are held to the same role provenance rule", "opportunity_state", func(m map[string]any) { member(m)["role_source"] = "guessed" }},
		{"role_provenance source must be recorded or inferred", "account_state", func(m map[string]any) {
			member(m)["role_provenance"] = []any{map[string]any{"role": "champion", "source": "guessed", "basis": "x"}}
		}},
		{"role_provenance is empty for a member whose only role is unknown", "account_state", func(m map[string]any) {
			member(m)["roles"] = []any{"unknown"}
			member(m)["role_source"] = nil
			member(m)["role_basis"] = nil
		}},
		{"a state diff change's opportunity_id is a uuid", "state_diff", func(m map[string]any) {
			m["changes"].([]any)[0].(map[string]any)["opportunity_id"] = "deal-a"
		}},
		{"role_basis is short", "account_state", func(m map[string]any) { member(m)["role_basis"] = strings.Repeat("x", 201) }},
		{"ineligible trigger cannot carry a run (§10)", "trigger_evaluation", func(m map[string]any) { m["eligible"] = false }},
		{"eligible trigger needs eligible reasons", "trigger_evaluation", func(m map[string]any) {
			m["reason_codes"] = []any{"no_material_change"}
		}},
		{"trigger evaluation needs a reason", "trigger_evaluation", func(m map[string]any) { m["reason_codes"] = []any{} }},
		{"edit decision needs edited artifact", "human_decision", func(m map[string]any) { m["edited_artifact"] = nil }},
		{"activity type outside the canonical families", "activity", func(m map[string]any) { m["activity_type"] = "Telepathy" }},
		{"activity idempotency key must be sha256 hex", "activity", func(m map[string]any) { m["idempotency_key"] = "abc" }},
		{"source event requires an event key", "source_event", func(m map[string]any) { delete(m, "source_event_key") }},
		// WP32 (HAR-131) origin markers
		{"synthetic event names its layer version", "source_event", func(m map[string]any) { m["origin"] = "synthetic" }},
		{"provenance needs an origin", "source_event", func(m map[string]any) { m["provenance"] = "synthetic:v1" }},
		{"live event has no dataset provenance", "source_event", func(m map[string]any) {
			m["origin"], m["provenance"] = "live", "crmarena-pro:b2b"
		}},
		{"synthetic provenance is never a dataset", "source_event", func(m map[string]any) {
			m["origin"], m["provenance"] = "dataset", "synthetic:v1"
		}},
		{"synthetic layers are versioned", "source_event", func(m map[string]any) {
			m["origin"], m["provenance"] = "synthetic", "synthetic:latest"
		}},
		{"agent run needs trigger activities (§11 traceability)", "agent_run", func(m map[string]any) { m["trigger_activity_ids"] = []any{} }},
		{"dry run cannot be executed (§18)", "agent_run", func(m map[string]any) { m["status"] = "executed" }},
		{"dry run cannot record an external effect (§18)", "agent_run", func(m map[string]any) {
			m["steps"].([]any)[2].(map[string]any)["external_effect_id"] = "gmail:123"
		}},
		{"claim candidate quote cannot be empty", "claim_candidate", func(m map[string]any) { m["evidence_quote"] = "" }},
		{"only computed fields may be derived (ADR-0008)", "account_state", func(m map[string]any) {
			f := field(m, "stage")
			f["derived"] = true
			f["winning_claim_id"] = nil
		}},
		{"derived field still needs evidence (§7)", "account_state", func(m map[string]any) {
			field(m, "last_customer_interaction")["evidence_refs"] = []any{}
		}},
		{"contradiction signal must name both claims (ADR-0008)", "signal", func(m map[string]any) {
			m["signal_type"] = "field_contradicted"
		}},
		{"an EVENT signal carries the end of its window (ADR-0015)", "signal", func(m map[string]any) {
			m["signal_type"] = "new_stakeholder_entered"
		}},
		{"a STANDING signal has no window (ADR-0015)", "signal", func(m map[string]any) {
			m["expires_at"] = "2026-10-13T15:42:00Z"
		}},
		{"semantic eval that can block must state its blocking rule (HAR-116)", "eval_catalog", func(m map[string]any) {
			delete(m["eval_types"].(map[string]any)["champion_continuity"].(map[string]any), "blocking_rule")
		}},
		{"email proposal needs a recipient", "agent_run_output", func(m map[string]any) { m["recipients"] = []any{} }},
		{"proposed action must cite evidence (§11)", "agent_run_output", func(m map[string]any) { m["evidence_refs"] = []any{} }},
		{"internal-only person must be an employee (WP17, ADR-0014)", "deterministic_eval_input", func(m map[string]any) {
			m["people"].([]any)[1].(map[string]any)["internal_only"] = true
		}},
		{"execute mode vocabulary (WP17)", "deterministic_eval_input", func(m map[string]any) {
			m["execute_mode"] = "yolo"
		}},
		{"employee cannot belong to a customer account", "deterministic_eval_input", func(m map[string]any) {
			m["people"].([]any)[0].(map[string]any)["account_id"] = m["account_id"]
		}},
		{"autonomy level vocabulary (ADR-0014)", "deterministic_eval_input", func(m map[string]any) {
			m["policy"].(map[string]any)["autonomy_level"] = "yolo"
		}},
		{"tool vocabulary (ADR-0014)", "deterministic_eval_input", func(m map[string]any) {
			m["policy"].(map[string]any)["allowed_tools"] = []any{"teleport"}
		}},
		{"confidential asset states its sharing condition", "deterministic_eval_input", func(m map[string]any) {
			m["assets"].([]any)[0].(map[string]any)["share_condition"] = nil
		}},
		{"prior meeting records its start", "deterministic_eval_input", func(m map[string]any) {
			pa := m["prior_actions"].([]any)[0].(map[string]any)
			pa["action"] = "schedule_meeting"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := schemaFor(t, c, tc.schema).Validate(mutate(t, root, tc.schema, tc.fn)); err == nil {
				t.Fatalf("expected %s to be rejected", tc.schema)
			}
		})
	}
}
