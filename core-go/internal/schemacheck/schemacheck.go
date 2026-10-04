// Package schemacheck validates documents against the repository's JSON Schemas
// (contracts/schemas). It exists so tests of packages that produce contract-shaped JSON
// (account state, claims, state diffs) can assert conformance the same way
// internal/contracts does, without each test re-implementing schema loading.
package schemacheck

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Validator holds the compiled contract schemas.
type Validator struct {
	compiler *jsonschema.Compiler
}

// New loads every schema under contracts/schemas, found by walking up from the working
// directory.
func New() (*Validator, error) {
	root, err := findContracts()
	if err != nil {
		return nil, fmt.Errorf("schemacheck: locate contracts: %w", err)
	}
	files, err := filepath.Glob(filepath.Join(root, "schemas", "*.json"))
	if err != nil || len(files) == 0 {
		return nil, fmt.Errorf("schemacheck: no schemas under %s: %v", root, err)
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("schemacheck: read %s: %w", f, err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("schemacheck: parse %s: %w", f, err)
		}
		id, _ := doc.(map[string]any)["$id"].(string)
		if id == "" {
			return nil, fmt.Errorf("schemacheck: %s has no $id", f)
		}
		if err := c.AddResource(id, doc); err != nil {
			return nil, fmt.Errorf("schemacheck: add %s: %w", f, err)
		}
	}
	return &Validator{compiler: c}, nil
}

// Validate checks doc (JSON text) against contracts/schemas/<name>.v1.json.
func (v *Validator) Validate(name string, doc []byte) error {
	return v.validate("https://ghost.local/contracts/"+name+".v1.json", doc)
}

// ValidateDef checks doc against one $defs entry of a schema — for payloads that are not the root document
// (episode_replay.v1.json advanceResult / resetResult / the §H artifact payloads).
func (v *Validator) ValidateDef(name, def string, doc []byte) error {
	return v.validate("https://ghost.local/contracts/"+name+".v1.json#/$defs/"+def, doc)
}

func (v *Validator) validate(ref string, doc []byte) error {
	schema, err := v.compiler.Compile(ref)
	if err != nil {
		return fmt.Errorf("schemacheck: compile %s: %w", ref, err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		return fmt.Errorf("schemacheck: parse document: %w", err)
	}
	return schema.Validate(inst)
}

func findContracts() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("schemacheck: working directory: %w", err)
	}
	for {
		candidate := filepath.Join(dir, "contracts")
		if info, statErr := os.Stat(filepath.Join(candidate, "schemas")); statErr == nil && info.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("schemacheck: contracts/ not found above the working directory")
		}
		dir = parent
	}
}
