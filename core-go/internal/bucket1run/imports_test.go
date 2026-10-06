package bucket1run

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// The Bucket 1 runner must not depend on the Bucket 2 package: the shared result types live in gateresult.
func TestBucket1RunDoesNotImportBucket2(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no source files: %v", err)
	}
	for _, f := range files {
		ast, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		for _, imp := range ast.Imports {
			if strings.HasSuffix(strings.Trim(imp.Path.Value, `"`), "/internal/bucket2") {
				t.Errorf("%s imports bucket2; use gateresult", f)
			}
		}
	}
}
