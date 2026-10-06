package contracts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// The HAR-129 demo audience boundary (contracts/demo/boundary.v1.json) must conform to its schema, and
// the schema itself must refuse the ways the boundary drifts: a third audience surface, a trigger
// outside the web control plane, a reworded invariant. internal/demoboundary checks the flow and scans
// the audience copy against the forbidden terms.

const demoBoundaryPath = "demo/boundary.v1.json"

func demoBoundaryDoc(t *testing.T, root string, fn func(m map[string]any)) any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, demoBoundaryPath))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if fn != nil {
		fn(m)
	}
	b, _ := json.Marshal(m)
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestDemoBoundaryConforms(t *testing.T) {
	root := contractsDir(t)
	if err := schemaFor(t, compiler(t, root), "demo_boundary").Validate(demoBoundaryDoc(t, root, nil)); err != nil {
		t.Fatalf("%s does not conform to demo_boundary: %v", demoBoundaryPath, err)
	}
}

func TestDemoBoundarySchemaRefusesDrift(t *testing.T) {
	root := contractsDir(t)
	schema := schemaFor(t, compiler(t, root), "demo_boundary")
	cases := map[string]func(m map[string]any){
		"a third audience surface": func(m map[string]any) {
			m["audience_surfaces"] = append(m["audience_surfaces"].([]any),
				map[string]any{"id": "web_control_plane", "name": "Web Control Plane", "job": "again"})
		},
		"a terminal surface": func(m map[string]any) {
			m["audience_surfaces"].([]any)[1].(map[string]any)["id"] = "terminal"
		},
		"a flow step in a terminal": func(m map[string]any) {
			m["audience_flow"].([]any)[1].(map[string]any)["surface"] = "terminal"
		},
		"the trigger in Slack": func(m map[string]any) {
			m["visible_trigger"].(map[string]any)["surface"] = "cliff_in_slack"
		},
		"a CLI trigger": func(m map[string]any) {
			m["visible_trigger"].(map[string]any)["control"] = "ghostctl demo play"
		},
		"a reworded invariant": func(m map[string]any) {
			m["invariant"] = "The demo is web and Slack, mostly."
		},
		"no forbidden terms": func(m map[string]any) { m["forbidden_terms"] = []any{} },
	}
	for name, fn := range cases {
		if err := schema.Validate(demoBoundaryDoc(t, root, fn)); err == nil {
			t.Errorf("%s: the demo_boundary schema accepted it", name)
		}
	}
}
