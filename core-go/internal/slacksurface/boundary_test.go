package slacksurface

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/demoboundary"
)

// HAR-129 demo boundary: Cliff in Slack is one of the two audience surfaces, so its copy never names
// developer tooling, terminal commands, proof plumbing or out-of-scope experiments
// (contracts/demo/boundary.v1.json), and the channel messages come only from real core objects: the
// fixture episode and the fake core power previews and tests, never the live surface.

func boundary(t *testing.T) *demoboundary.Boundary {
	t.Helper()
	root, err := demoboundary.RepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	b, err := demoboundary.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// goStrings returns every string literal of the non-test Go files in dir, keyed "file:line".
func goStrings(t *testing.T, dir string) map[string]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if _, ok := n.(*ast.ImportSpec); ok {
				return false
			}
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatalf("%s: unquote %s: %v", fset.Position(lit.Pos()), lit.Value, err)
			}
			pos := fset.Position(lit.Pos())
			out[filepath.Base(pos.Filename)+":"+strconv.Itoa(pos.Line)] += s + "\n"
			return true
		})
	}
	return out
}

// slackTexts returns every text the Block Kit JSON shows a person: the values of "text", "alt_text" and
// "placeholder" anywhere in the document.
func slackTexts(v any, out *[]string) {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			if s, ok := child.(string); ok && (k == "text" || k == "alt_text" || k == "placeholder") {
				*out = append(*out, s)
				continue
			}
			slackTexts(child, out)
		}
	case []any:
		for _, child := range x {
			slackTexts(child, out)
		}
	}
}

// internalIDs matches the dotted protocol ids the surface sends but never shows: action_ids, block_ids,
// callback_ids (contracts/slack/actions.md) and the example preview host. They keep their "ghost." prefix
// so messages already posted keep working; the retired brand name is only forbidden in rendered copy.
var internalIDs = regexp.MustCompile(`ghost\.[a-z0-9_.]+`)

func TestSlackCopyStaysInsideTheDemoBoundary(t *testing.T) {
	b := boundary(t)
	for where, text := range goStrings(t, ".") {
		for _, h := range b.Scan(internalIDs.ReplaceAllString(text, " ")) {
			t.Errorf("slacksurface %s: Slack copy names %s", where, h)
		}
	}
	goldens, err := filepath.Glob(filepath.Join("testdata", "golden", "*.json"))
	if err != nil || len(goldens) == 0 {
		t.Fatalf("no Slack goldens found: %v", err)
	}
	for _, g := range goldens {
		raw, err := os.ReadFile(g)
		if err != nil {
			t.Fatal(err)
		}
		var doc any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: %v", g, err)
		}
		var texts []string
		slackTexts(doc, &texts)
		for _, s := range texts {
			for _, h := range b.Scan(s) {
				t.Errorf("%s: rendered Slack text names %s", g, h)
			}
		}
	}
}

func TestBoundaryScanCatchesASeededSlackViolation(t *testing.T) {
	b := boundary(t)
	var texts []string
	slackTexts(map[string]any{"blocks": []any{map[string]any{"type": "section",
		"text": map[string]any{"type": "mrkdwn", "text": "Run `ghostctl demo verify` to see the proof harness result."}}}}, &texts)
	if len(texts) != 1 || len(b.Scan(texts[0])) == 0 {
		t.Fatalf("a seeded CLI/proof line in a Slack block was not caught: %v", texts)
	}
}

// The embedded eval wording table is rendered into Message 2 and 3, so every string in it is Slack copy.
func TestSlackEvalWordingNamesNoRetiredBrand(t *testing.T) {
	b := boundary(t)
	var doc any
	if err := json.Unmarshal(wordingJSON, &doc); err != nil {
		t.Fatal(err)
	}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			for _, h := range b.Scan(x) {
				t.Errorf("eval_wording.json: %q names %s", x, h)
			}
		case map[string]any:
			for _, c := range x {
				walk(c)
			}
		case []any:
			for _, c := range x {
				walk(c)
			}
		}
	}
	walk(doc)
}

func TestBoundaryScanCatchesTheRetiredBrandInSlackText(t *testing.T) {
	b := boundary(t)
	var texts []string
	slackTexts(map[string]any{"blocks": []any{map[string]any{"type": "header",
		"text": map[string]any{"type": "plain_text", "text": "Ghost drafted 3 moves"}}}}, &texts)
	if len(texts) != 1 || len(b.Scan(texts[0])) == 0 {
		t.Fatalf("the retired brand in a Slack block was not caught: %v", texts)
	}
	if hits := b.Scan("gtm_ai drafted 3 moves"); len(hits) != 0 {
		t.Fatalf("the new brand was flagged: %v", hits)
	}
}

// previewFiles hold the offline fixture episode and the --dry-run preview. Every identifier they declare
// is preview-only: no other file of the live Slack surface may reference it.
var previewFiles = map[string]bool{"dryrun.go": true, "fixture.go": true}

// forbiddenImports are fake backends that must never be wired into the live Slack surface.
var forbiddenImports = []string{"internal/fakecore", "internal/slackfake", "internal/demosmoke"}

// previewOnly returns the package-level identifiers declared in the preview files of dir.
func previewOnly(t *testing.T, dir string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	fset := token.NewFileSet()
	for name := range previewFiles {
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					names[d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch sp := spec.(type) {
					case *ast.TypeSpec:
						names[sp.Name.Name] = true
					case *ast.ValueSpec:
						for _, n := range sp.Names {
							names[n.Name] = true
						}
					}
				}
			}
		}
	}
	return names
}

// fixtureUses reports every forbidden import and every reference to a preview-only identifier in the
// non-test files of dir, package-level declarations included, outside skipFiles and allowedFuncs.
func fixtureUses(t *testing.T, dir string, previewIdents, skipFiles, allowedFuncs map[string]bool) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	var bad []string
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || skipFiles[filepath.Base(f)] {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range file.Imports {
			for _, fb := range forbiddenImports {
				if strings.HasSuffix(strings.Trim(imp.Path.Value, `"`), fb) {
					bad = append(bad, fset.Position(imp.Pos()).String()+": imports "+fb)
				}
			}
		}
		for _, decl := range file.Decls {
			owner := "package level"
			if fn, ok := decl.(*ast.FuncDecl); ok {
				if allowedFuncs[fn.Name.Name] {
					continue
				}
				owner = fn.Name.Name
			}
			for _, id := range packageRefs(decl) {
				if previewIdents[id.Name] {
					bad = append(bad, fset.Position(id.Pos()).String()+": "+owner+" uses "+id.Name)
				}
			}
		}
	}
	return bad
}

// packageRefs returns the identifiers in node that can name a package-level declaration of slacksurface:
// bare identifiers and slacksurface.X selectors. Field names, struct-literal keys and field or method
// selectors (x.Preview) name members, not the preview declarations.
func packageRefs(node ast.Node) []*ast.Ident {
	member := map[*ast.Ident]bool{}
	var refs []*ast.Ident
	ast.Inspect(node, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			if pkg, ok := x.X.(*ast.Ident); ok && pkg.Name == "slacksurface" {
				refs = append(refs, x.Sel)
			}
			member[x.Sel] = true
		case *ast.Field:
			for _, name := range x.Names {
				member[name] = true
			}
		case *ast.KeyValueExpr:
			if k, ok := x.Key.(*ast.Ident); ok {
				member[k] = true
			}
		case *ast.Ident:
			if !member[x] {
				refs = append(refs, x)
			}
		}
		return true
	})
	return refs
}

// TestLiveSlackMessagesComeFromRealCoreObjects: the live surface (this package and cmd/slackbot) posts
// what the core returns; the fixture episode reaches only the offline preview and the fakes only tests.
func TestLiveSlackMessagesComeFromRealCoreObjects(t *testing.T) {
	preview := previewOnly(t, ".")
	for _, name := range []string{"NewFixture", "FixtureDocs", "Previews", "Fixture"} {
		if !preview[name] {
			t.Fatalf("preview identifier %s not found in %v; update the origin check", name, previewFiles)
		}
	}
	for _, bad := range fixtureUses(t, ".", preview, previewFiles, nil) {
		t.Errorf("live Slack surface: %s", bad)
	}
	for _, bad := range fixtureUses(t, filepath.Join("..", "..", "cmd", "slackbot"), preview, nil, map[string]bool{"dryRun": true}) {
		t.Errorf("cmd/slackbot: %s", bad)
	}
}

func TestOriginCheckCatchesASeededFixturePost(t *testing.T) {
	dir := t.TempDir()
	src := "package x\n\nimport \"github.com/harneet2512/gtm-work/core-go/internal/fakecore\"\n\n" +
		"var _ = fakecore.New\n\nfunc postDemo() { _ = NewFixture() }\n\nfunc NewFixture() int { return 0 }\n"
	if err := os.WriteFile(filepath.Join(dir, "live.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := fixtureUses(t, dir, map[string]bool{"NewFixture": true, "FixtureDocs": true}, nil, nil)
	if len(bad) != 3 {
		t.Fatalf("want the fake-core import, the package-level fixture read and the fixture post caught, got %v", bad)
	}
}
