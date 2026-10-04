package slacksurface

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/schemacheck"
)

// TestFixtureObjectsValidateAgainstTheContractSchemas checks every contract-shaped document the
// fixture is built from against contracts/schemas, so the Slack surface cannot drift from them.
func TestFixtureObjectsValidateAgainstTheContractSchemas(t *testing.T) {
	v, err := schemacheck.New()
	if err != nil {
		t.Fatal(err)
	}
	docs, err := FixtureDocs()
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) < 9 {
		t.Fatalf("only %d fixture documents", len(docs))
	}
	for name, doc := range docs {
		schema := name
		if i := bytes.IndexByte([]byte(name), ':'); i >= 0 {
			schema = name[:i]
		}
		if err := v.Validate(schema, doc); err != nil {
			t.Errorf("%s does not match %s.v1.json: %v", name, schema, err)
		}
	}
}

// TestEmbeddedExamplesAreTheContractExamples keeps the embedded copies identical to
// contracts/examples, which the contract PR owns.
func TestEmbeddedExamplesAreTheContractExamples(t *testing.T) {
	entries, err := contractExamples.ReadDir("testdata/contracts")
	if err != nil || len(entries) == 0 {
		t.Fatalf("no embedded examples: %v", err)
	}
	for _, e := range entries {
		got, _ := contractExamples.ReadFile("testdata/contracts/" + e.Name())
		want, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "examples", e.Name()))
		if err != nil {
			t.Fatalf("contract example %s: %v", e.Name(), err)
		}
		if !bytes.Equal(bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n")), bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))) {
			t.Errorf("%s differs from contracts/examples; copy it again", e.Name())
		}
	}
}

// TestRequestBodiesMatchTheOpenAPIRequestSchemas validates what Slack sends against the property
// names of the core request bodies (additionalProperties is false there).
func TestRequestBodiesMatchTheOpenAPIRequestSchemas(t *testing.T) {
	spec, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "openapi", "core.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"StrategyDecisionRequest", "SendRequest", "JudgmentVerdictRequest"} {
		if !bytes.Contains(spec, []byte("    "+name+":")) {
			t.Fatalf("%s missing from core.yaml", name)
		}
	}
	for _, field := range []string{"selected_candidate_id", "final_to", "final_cc", "final_artifact", "actor_label", "surface",
		"decision", "verdict", "corrected_statement", "note"} {
		if !bytes.Contains(spec, []byte(field+":")) {
			t.Errorf("core.yaml no longer has %q, which this surface sends", field)
		}
	}
	for _, w := range []string{mustJSON(StrategyDecisionRequest{SelectedCandidateID: "c", Surface: SurfaceSlack, ActorLabel: "a"}),
		mustJSON(SendRequest{Decision: SendSend, Surface: SurfaceSlack, ActorLabel: "a"}),
		mustJSON(VerdictRequest{Verdict: VerdictCorrected, CorrectedStatement: "x", Surface: SurfaceSlack, ActorLabel: "a"})} {
		if bytes.Contains([]byte(w), []byte("actor_source")) {
			t.Errorf("request carries a property the contract does not define: %s", w)
		}
	}
}
