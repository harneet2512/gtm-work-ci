package knowledge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Minimal neutral situations: no fixture account, only the fields a test needs.
var now = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

const opp = "00000000-0000-4000-8000-0000000000a1"

func ref(id string) []EvidenceRef { return []EvidenceRef{{ActivityID: id}} }

func known(v any) Value {
	return Value{Known: true, Scalar: v, EvidenceRefs: ref("act-" + toString(v))}
}

func unknown() Value { return Value{} }

func items(list ...Item) Value {
	return Value{Known: true, List: true, Items: list, EvidenceRefs: ref("act-list")}
}

func toString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func cond(field, op string, value ...any) Condition {
	c := Condition{Field: field, Op: op}
	switch len(value) {
	case 0:
	case 1:
		c.Value = value[0]
	default:
		c.Value = append([]any(nil), value...)
	}
	return c
}

func list(values ...any) []any { return values }

func situation(fields map[string]Value) Situation {
	return Situation{AccountID: "acct", OpportunityID: opp, Now: now, Fields: fields, Conflicts: map[string][]string{}}
}

func with(s Situation, mutate func(*Situation)) Situation {
	mutate(&s)
	return s
}

func signal(typ string, age time.Duration) Signal {
	return Signal{ID: "sig-" + typ, Type: typ, CreatedAt: now.Add(-age), EvidenceRefs: ref("act-" + typ)}
}

// k builds a knowledge object with the given signature and exceptions.
func k(signature []Condition, exceptions ...Exception) Knowledge {
	return Knowledge{ID: "00000000-0000-4000-8000-000000000017", Title: "t", SituationSignature: signature, Exceptions: exceptions}
}

func exc(desc string, conds ...Condition) Exception {
	return Exception{Description: desc, Conditions: conds}
}

func mustMatch(t *testing.T, kn Knowledge, s Situation) Result {
	t.Helper()
	r, err := Match(kn, s)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	return r
}

// holdsIn evaluates a single condition in a situation.
func holdsIn(t *testing.T, s Situation, c Condition) bool {
	t.Helper()
	ok, _, err := newEnv(s).holds(c)
	if err != nil {
		t.Fatalf("%s: %v", render(c), err)
	}
	return ok
}

// repoFile finds a path relative to the repository root.
func repoFile(t *testing.T, rel string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		p := filepath.Join(dir, rel)
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("%s not found", rel)
		}
		dir = parent
	}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}
