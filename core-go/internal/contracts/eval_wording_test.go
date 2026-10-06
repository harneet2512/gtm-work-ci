package contracts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type wordingDoc struct {
	Verdicts   map[string]wordingVerdict `json:"verdicts"`
	NotChecked wordingVerdict            `json:"not_checked"`
	EvalTypes  map[string]struct {
		Name     string `json:"name"`
		Question string `json:"question"`
	} `json:"eval_types"`
	Diagnostics map[string]string `json:"diagnostics"`
}

type wordingVerdict struct {
	Order      int    `json:"order"`
	Label      string `json:"label"`
	Icon       string `json:"icon"`
	SlackEmoji string `json:"slack_emoji"`
}

func readInto(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// TestEvalWordingValidatesAndCoversTheCatalog: the one eval vocabulary every surface renders (eval design
// spec 4b-3) is schema-valid, names every catalog eval type and every diagnostic, orders verdicts worst
// first, and gives each verdict its own word, icon and Slack emoji.
func TestEvalWordingValidatesAndCoversTheCatalog(t *testing.T) {
	root := contractsDir(t)
	file := filepath.Join(root, "evals", "eval_wording.json")
	if err := schemaFor(t, compiler(t, root), "eval_wording").Validate(readJSON(t, file)); err != nil {
		t.Fatalf("eval_wording.json invalid: %v", err)
	}
	var wording wordingDoc
	readInto(t, file, &wording)
	var catalog struct {
		EvalTypes map[string]json.RawMessage `json:"eval_types"`
	}
	readInto(t, filepath.Join(root, "evals", "eval_catalog.json"), &catalog)

	for evalType := range catalog.EvalTypes {
		if _, ok := wording.EvalTypes[evalType]; !ok {
			t.Errorf("catalog eval type %s has no wording", evalType)
		}
	}
	for evalType := range wording.EvalTypes {
		if _, ok := catalog.EvalTypes[evalType]; !ok {
			t.Errorf("wording names %s, which the catalog does not have", evalType)
		}
	}
	names := map[string]string{}
	for evalType, w := range wording.EvalTypes {
		if other, dup := names[w.Name]; dup {
			t.Errorf("%s and %s share the name %q", evalType, other, w.Name)
		}
		names[w.Name] = evalType
	}

	var result struct {
		Defs struct {
			Diagnostic struct {
				Enum []string `json:"enum"`
			} `json:"diagnostic"`
		} `json:"$defs"`
	}
	readInto(t, filepath.Join(root, "schemas", "eval_result.v1.json"), &result)
	for _, d := range result.Defs.Diagnostic.Enum {
		if wording.Diagnostics[d] == "" {
			t.Errorf("diagnostic %s has no wording", d)
		}
	}

	order := []string{"fail", "warn", "abstain", "pass", "not_relevant"}
	seen := map[string]bool{}
	for i, v := range order {
		w := wording.Verdicts[v]
		if w.Order != i {
			t.Errorf("verdict %s has order %d, want %d (worst first)", v, w.Order, i)
		}
		for _, key := range []string{"label:" + w.Label, "icon:" + w.Icon, "emoji:" + w.SlackEmoji} {
			if seen[key] {
				t.Errorf("verdict %s reuses %s", v, key)
			}
			seen[key] = true
		}
	}
	if wording.NotChecked.Order <= wording.Verdicts["not_relevant"].Order {
		t.Errorf("not checked must sort after not relevant")
	}
}
