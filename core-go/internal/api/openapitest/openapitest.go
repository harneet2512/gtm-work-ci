// Package openapitest checks HTTP responses against contracts/openapi/core.yaml, so a handler test
// can assert "this body is exactly what the contract says a <status> of <method> <path> returns",
// $refs into contracts/schemas included. It is test support; the only non-test user is internal/fakecore,
// which validates request bodies the way the real core must accept them.
package openapitest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
)

const (
	schemaPrefix = "https://ghost.local/contracts/"
	specID       = "https://ghost.local/openapi/core.json"
)

// Spec is the compiled core.yaml.
type Spec struct {
	compiler *jsonschema.Compiler
	doc      map[string]any
}

// Operation is one (method, path template) of the spec.
type Operation struct {
	Method string // upper case
	Path   string // as in the spec, e.g. /accounts/{account_id}/state
}

// Load reads contracts/openapi/core.yaml and every contracts/schemas/*.json, found by walking up
// from the working directory.
func Load() (*Spec, error) {
	root, err := findContracts()
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	files, err := filepath.Glob(filepath.Join(root, "schemas", "*.json"))
	if err != nil || len(files) == 0 {
		return nil, fmt.Errorf("openapitest: no schemas under %s: %v", root, err)
	}
	for _, f := range files {
		if err := addJSON(c, f, ""); err != nil {
			return nil, err
		}
	}
	raw, err := os.ReadFile(filepath.Join(root, "openapi", "core.yaml"))
	if err != nil {
		return nil, fmt.Errorf("openapitest: read core.yaml: %w", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("openapitest: parse core.yaml: %w", err)
	}
	rewritten := rewriteRefs(doc)
	asJSON, err := json.Marshal(rewritten)
	if err != nil {
		return nil, fmt.Errorf("openapitest: encode core.yaml: %w", err)
	}
	parsed, err := jsonschema.UnmarshalJSON(bytes.NewReader(asJSON))
	if err != nil {
		return nil, fmt.Errorf("openapitest: reparse core.yaml: %w", err)
	}
	if err := c.AddResource(specID, parsed); err != nil {
		return nil, fmt.Errorf("openapitest: add core.yaml: %w", err)
	}
	return &Spec{compiler: c, doc: rewritten.(map[string]any)}, nil
}

func addJSON(c *jsonschema.Compiler, path, id string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("openapitest: read %s: %w", path, err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("openapitest: parse %s: %w", path, err)
	}
	if id == "" {
		id, _ = doc.(map[string]any)["$id"].(string)
	}
	if id == "" {
		return fmt.Errorf("openapitest: %s has no $id", path)
	}
	return c.AddResource(id, doc)
}

// rewriteRefs points "../schemas/x.json#..." refs at the schemas' absolute ids.
func rewriteRefs(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			if s, ok := e.(string); ok && k == "$ref" && strings.HasPrefix(s, "../schemas/") {
				out[k] = schemaPrefix + strings.TrimPrefix(s, "../schemas/")
				continue
			}
			out[k] = rewriteRefs(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = rewriteRefs(e)
		}
		return out
	default:
		return v
	}
}

// Operations lists every (method, path) in the spec, sorted.
func (s *Spec) Operations() []Operation {
	var ops []Operation
	paths, _ := s.doc["paths"].(map[string]any)
	for p, item := range paths {
		for m := range item.(map[string]any) {
			switch m {
			case "get", "post", "put", "patch", "delete":
				ops = append(ops, Operation{Method: strings.ToUpper(m), Path: p})
			}
		}
	}
	sort.Slice(ops, func(i, j int) bool {
		if ops[i].Path != ops[j].Path {
			return ops[i].Path < ops[j].Path
		}
		return ops[i].Method < ops[j].Method
	})
	return ops
}

// Statuses lists the response codes the spec documents for the operation.
func (s *Spec) Statuses(method, path string) []string {
	op := s.operation(method, path)
	var out []string
	if op != nil {
		for code := range op["responses"].(map[string]any) {
			out = append(out, code)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Spec) operation(method, path string) map[string]any {
	item, _ := s.doc["paths"].(map[string]any)[path].(map[string]any)
	op, _ := item[strings.ToLower(method)].(map[string]any)
	return op
}

// Response validates body against the JSON schema the spec gives for status of method path. A
// status the spec does not document is an error: undocumented behaviour is contract drift.
func (s *Spec) Response(method, path string, status int, body []byte) error {
	op := s.operation(method, path)
	if op == nil {
		return fmt.Errorf("openapitest: %s %s is not in core.yaml", method, path)
	}
	code := fmt.Sprint(status)
	resp, ok := op["responses"].(map[string]any)[code].(map[string]any)
	if !ok {
		return fmt.Errorf("openapitest: %s %s does not document a %s response (documented: %v)", method, path, code, s.Statuses(method, path))
	}
	pointer := []string{"paths", path, strings.ToLower(method), "responses", code}
	if ref, ok := resp["$ref"].(string); ok {
		name, found := strings.CutPrefix(ref, "#/components/responses/")
		if !found {
			return fmt.Errorf("openapitest: unsupported response ref %q", ref)
		}
		resp = s.doc["components"].(map[string]any)["responses"].(map[string]any)[name].(map[string]any)
		pointer = []string{"components", "responses", name}
	}
	if _, ok := resp["content"]; !ok {
		if len(bytes.TrimSpace(body)) != 0 {
			return fmt.Errorf("openapitest: %s %s %s documents no body but got one", method, path, code)
		}
		return nil
	}
	pointer = append(pointer, "content", "application/json", "schema")
	schema, err := s.compiler.Compile(specID + "#" + (&url.URL{Fragment: jsonPointer(pointer)}).EscapedFragment())
	if err != nil {
		return fmt.Errorf("openapitest: compile %s %s %s: %w", method, path, code, err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("openapitest: body is not JSON: %w", err)
	}
	if err := schema.Validate(inst); err != nil {
		return fmt.Errorf("openapitest: %s %s %s body violates the contract: %w", method, path, code, err)
	}
	return nil
}

// Request validates a JSON request body against the requestBody schema the spec gives for method path, so a
// server (or a fake of it) can refuse a body the contract does not allow. An operation without a request
// body is an error.
func (s *Spec) Request(method, path string, body []byte) error {
	op := s.operation(method, path)
	if op == nil {
		return fmt.Errorf("openapitest: %s %s is not in core.yaml", method, path)
	}
	if _, ok := op["requestBody"]; !ok {
		return fmt.Errorf("openapitest: %s %s documents no request body", method, path)
	}
	pointer := []string{"paths", path, strings.ToLower(method), "requestBody", "content", "application/json", "schema"}
	schema, err := s.compiler.Compile(specID + "#" + (&url.URL{Fragment: jsonPointer(pointer)}).EscapedFragment())
	if err != nil {
		return fmt.Errorf("openapitest: compile %s %s request: %w", method, path, err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("openapitest: body is not JSON: %w", err)
	}
	if err := schema.Validate(inst); err != nil {
		return fmt.Errorf("openapitest: %s %s request violates the contract: %w", method, path, err)
	}
	return nil
}

func jsonPointer(parts []string) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteByte('/')
		b.WriteString(strings.NewReplacer("~", "~0", "/", "~1").Replace(p))
	}
	return b.String()
}

// findContracts walks up from the working directory to the repository's contracts directory.
func findContracts() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		cand := filepath.Join(dir, "contracts")
		if st, err := os.Stat(filepath.Join(cand, "openapi", "core.yaml")); err == nil && !st.IsDir() {
			return cand, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("openapitest: contracts/openapi/core.yaml not found above the working directory")
		}
		dir = parent
	}
}
